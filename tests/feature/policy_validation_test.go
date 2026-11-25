package feature

import (
	"encoding/json"
	"strings"

	"github.com/goravel/framework/contracts/http"
	contractstesting "github.com/goravel/framework/contracts/testing/http"

	"github.com/lathief/RiskForge/app/facades"
	policydomain "github.com/lathief/RiskForge/app/modules/policy/domain"
	"github.com/lathief/RiskForge/database/seeders"
)

func testCasesPath(versionID string) string { return versionsPath + "/" + versionID + "/test-cases" }
func validatePath(versionID string) string  { return versionsPath + "/" + versionID + "/validate" }

func (s *PolicyTestSuite) putTestCasesAs(user, versionID, body string) contractstesting.Response {
	req := s.Http(s.T()).WithHeader("Content-Type", "application/json")
	if user != "" {
		req = req.WithToken(s.tokens[user])
	}
	resp, err := req.Put(testCasesPath(versionID), strings.NewReader(body))
	s.Require().NoError(err)
	return resp
}

func (s *PolicyTestSuite) putTestCases(versionID, body string) contractstesting.Response {
	return s.putTestCasesAs("demo_analyst", versionID, body)
}

func (s *PolicyTestSuite) getTestCases(versionID string) map[string]any {
	return s.json(s.send("demo_auditor", "GET", testCasesPath(versionID), "").AssertOk())
}

func (s *PolicyTestSuite) validate(user, versionID string) contractstesting.Response {
	return s.send(user, "POST", validatePath(versionID), "")
}

// detailPairs returns "field:issue" for the issues of a validation report.
func detailPairs(report map[string]any) []string {
	var out []string
	for _, d := range report["issues"].([]any) {
		m := d.(map[string]any)
		out = append(out, m["field"].(string)+":"+m["issue"].(string))
	}
	return out
}

// demoTestCasesBody is the seeded golden cases, as a request body.
func demoTestCasesBody(changes ...string) string {
	body := `{"test_cases":[
	{"case_code":"GOLDEN_HAPPY_PATH","input":` + seeders.DemoApplicant + `,"expected":{"outcome":"DECISION","decision":"APPROVE","grade":"A"}},
	{"case_code":"GOLDEN_AGE_BELOW_MINIMUM","input":` + strings.Replace(seeders.DemoApplicant, `"age_years":29`, `"age_years":19`, 1) + `,"expected":{"outcome":"DECISION","decision":"REJECT","reason_codes":["AGE_BELOW_MINIMUM"]}},
	{"case_code":"GOLDEN_MISSING_INCOME","required":true,"input":` + strings.Replace(seeders.DemoApplicant, `"monthly_income":"12000000.00"`, `"monthly_income":null`, 1) + `,"expected":{"outcome":"INPUT_VALIDATION_ERROR"}}]}`
	for i := 0; i+1 < len(changes); i += 2 {
		body = strings.Replace(body, changes[i], changes[i+1], 1)
	}
	return body
}

// demoDraft creates a draft with the demo RAC rules, scorecard, and matrix (not the test cases).
func (s *PolicyTestSuite) demoDraft() string {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	s.putRules("demo_analyst", v1, demoRulesBody).AssertOk()
	s.putScorecard(v1, demoScorecardBody).AssertOk()
	s.putMatrix(v1, demoMatrixBody).AssertOk()
	return v1
}

// --- authorization ----------------------------------------------------------------------

func (s *PolicyTestSuite) TestTestCaseAndValidateAuthorization() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	readers := map[string]int{
		"": http.StatusUnauthorized, "demo_admin": http.StatusForbidden, "demo_analyst": http.StatusOK,
		"demo_approver_1": http.StatusOK, "demo_auditor": http.StatusOK, "demo_loan_application": http.StatusForbidden,
	}
	for user, want := range readers {
		s.send(user, "GET", testCasesPath(v1), "").AssertStatus(want)
	}
	for _, user := range []string{"", "demo_admin", "demo_approver_1", "demo_auditor", "demo_loan_application"} {
		want := http.StatusForbidden
		if user == "" {
			want = http.StatusUnauthorized
		}
		s.putTestCasesAs(user, v1, `{"test_cases":[]}`).AssertStatus(want)
		// Validate is the dry run of submit, so it needs policy:submit (the analyst).
		s.validate(user, v1).AssertStatus(want)
	}
	s.validate("demo_analyst", v1).AssertOk()
}

// --- test cases -------------------------------------------------------------------------

func (s *PolicyTestSuite) TestPutStoresTestCasesAndGetReturnsThem() {
	v1 := s.demoDraft()
	resp := s.putTestCases(v1, demoTestCasesBody())
	resp.AssertOk()
	body := s.json(resp)
	cases := body["test_cases"].([]any)
	s.Require().Len(cases, 3)
	first := cases[0].(map[string]any)
	s.Equal("GOLDEN_HAPPY_PATH", first["case_code"])
	s.Equal(true, first["required"])
	s.Equal(map[string]any{"outcome": "DECISION", "decision": "APPROVE", "grade": "A", "reason_codes": []any{}}, first["expected"])
	s.Equal(float64(29), first["input"].(map[string]any)["age_years"])
	s.Equal("12000000.00", first["input"].(map[string]any)["monthly_income"], "decimal strings are kept as written")
	third := cases[2].(map[string]any)
	s.Equal(map[string]any{"outcome": "INPUT_VALIDATION_ERROR", "decision": nil, "grade": nil, "reason_codes": []any{}}, third["expected"])
	s.Equal(body, s.getTestCases(v1), "GET returns exactly what PUT stored")

	again := s.json(s.putTestCases(v1, demoTestCasesBody()).AssertOk())
	s.Equal(body["updated_at"], again["updated_at"], "the same list changes nothing")

	cleared := s.json(s.putTestCases(v1, `{"test_cases":[]}`).AssertOk())
	s.Equal([]any{}, cleared["test_cases"])
}

func (s *PolicyTestSuite) TestTestCaseValidation() {
	v1 := s.demoDraft()
	happy := seeders.DemoApplicant
	one := func(c string) string { return `{"test_cases":[` + c + `]}` }
	cases := map[string]struct {
		body string
		want []string
	}{
		"empty body":     {`{}`, []string{"test_cases:required"}},
		"not a list":     {`{"test_cases":{}}`, []string{"test_cases:must_be_array"}},
		"case shape":     {one(`"x"`), []string{"test_cases[0]:must_be_object"}},
		"unknown key":    {one(`{"case_code":"CASE_X","input":` + happy + `,"expected":{"outcome":"DECISION","decision":"APPROVE"},"weight":1}`), []string{"test_cases[0].weight:not_allowed"}},
		"no expected":    {one(`{"case_code":"CASE_X","input":` + happy + `}`), []string{"test_cases[0].expected:required"}},
		"expected shape": {one(`{"case_code":"CASE_X","input":` + happy + `,"expected":{"outcome":1,"reason_codes":"x","extra":true}}`), []string{"test_cases[0].expected.extra:not_allowed", "test_cases[0].expected.outcome:must_be_string", "test_cases[0].expected.reason_codes:must_be_array"}},
		"required type":  {one(`{"case_code":"CASE_X","required":"yes","input":` + happy + `,"expected":{"outcome":"DECISION","decision":"APPROVE"}}`), []string{"test_cases[0].required:must_be_boolean"}},
		"input array":    {one(`{"case_code":"CASE_X","input":[1],"expected":{"outcome":"DECISION","decision":"APPROVE"}}`), []string{"test_cases[0].input:must_be_object"}},
		"missing fields": {one(`{}`), []string{"test_cases[0].expected:required"}},
		"bad references": {one(`{"case_code":"CASE_X","input":` + happy + `,"expected":{"outcome":"DECISION","decision":"APPROVE","grade":"C","reason_codes":["NOT_A_REASON"]}}`),
			[]string{"test_cases[0].expected.grade:unknown_grade", "test_cases[0].expected.reason_codes[0]:unknown_reason_code"}},
		"invalid input for a decision": {one(`{"case_code":"CASE_X","input":` + strings.Replace(happy, `"age_years":29`, `"age_years":"29"`, 1) + `,"expected":{"outcome":"DECISION","decision":"APPROVE"}}`),
			[]string{"test_cases[0].input.age_years:invalid_type"}},
		"monthly income zero for a decision": {one(`{"case_code":"CASE_X","input":` + strings.Replace(happy, `"12000000.00"`, `"0.00"`, 1) + `,"expected":{"outcome":"DECISION","decision":"REJECT"}}`),
			[]string{"test_cases[0].input.monthly_income:below_min_value"}},
		"valid input for a validation error": {one(`{"case_code":"CASE_X","input":` + happy + `,"expected":{"outcome":"INPUT_VALIDATION_ERROR"}}`),
			[]string{"test_cases[0].input:validation_error_not_raised"}},
		"derived input": {one(`{"case_code":"CASE_X","input":` + strings.Replace(happy, `{`, `{"dsr_ratio":"0.2",`, 1) + `,"expected":{"outcome":"DECISION","decision":"APPROVE"}}`),
			[]string{"test_cases[0].input.dsr_ratio:derived_not_allowed"}},
		"duplicate code": {demoTestCasesBody(`"GOLDEN_AGE_BELOW_MINIMUM"`, `"GOLDEN_HAPPY_PATH"`), []string{"test_cases[1].case_code:duplicate"}},
	}
	for name, c := range cases {
		resp := s.putTestCases(v1, c.body)
		resp.AssertStatus(http.StatusUnprocessableEntity)
		code, _ := s.errorOf(resp)
		s.Equal("INPUT_VALIDATION_ERROR", code, name)
		s.ElementsMatch(c.want, s.issuePairs(resp), name)
	}
	s.Equal([]any{}, s.getTestCases(v1)["test_cases"], "no invalid list stored anything")
}

func (s *PolicyTestSuite) TestFrozenVersionRefusesTestCaseChanges() {
	v1 := s.demoDraft()
	s.putTestCases(v1, demoTestCasesBody()).AssertOk()
	s.submitRaw(v1).AssertOk()
	resp := s.putTestCases(v1, `{"test_cases":[]}`)
	resp.AssertStatus(http.StatusConflict)
	s.Len(s.getTestCases(v1)["test_cases"], 3)
	s.Error(s.exec(`DELETE FROM policy.test_cases WHERE policy_version_id = $1::uuid`, v1), "the trigger freezes test cases")
	s.Error(s.exec(`UPDATE policy.test_cases SET expected_decision = 'REJECT' WHERE policy_version_id = $1::uuid`, v1))
}

func (s *PolicyTestSuite) TestDatabaseTestCaseConstraints() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	insert := func(order int, code, input, outcome string, decision any, codes string) error {
		return s.exec(`INSERT INTO policy.test_cases (policy_version_id, case_order, case_code, input, expected_outcome, expected_decision, expected_reason_codes)
			VALUES ($1::uuid, $2, $3, $4::jsonb, $5, $6, $7::jsonb)`, v1, order, code, input, outcome, decision, codes)
	}
	s.NoError(insert(1, "CASE_A", `{}`, "DECISION", "APPROVE", `[]`), "a draft accepts a valid row")
	s.Error(insert(1, "CASE_B", `{}`, "DECISION", "APPROVE", `[]`), "order is unique")
	s.Error(insert(2, "CASE_A", `{}`, "DECISION", "APPROVE", `[]`), "code is unique")
	s.Error(insert(2, "case_b", `{}`, "DECISION", "APPROVE", `[]`), "code format")
	s.Error(insert(2, "CASE_B", `[1]`, "DECISION", "APPROVE", `[]`), "input is an object")
	s.Error(insert(2, "CASE_B", `{}`, "PASS", "APPROVE", `[]`), "outcome")
	s.Error(insert(2, "CASE_B", `{}`, "DECISION", nil, `[]`), "a decision case names its decision")
	s.Error(insert(2, "CASE_B", `{}`, "INPUT_VALIDATION_ERROR", "REJECT", `[]`), "a validation-error case names no decision")
	s.Error(insert(2, "CASE_B", `{}`, "INPUT_VALIDATION_ERROR", nil, `["X_REASON"]`), "nor reason codes")
	s.Error(insert(2, "CASE_B", `{}`, "DECISION", "APPROVE", `{}`), "reason codes are an array")
}

func (s *PolicyTestSuite) TestCloneCopiesTestCases() {
	policyID, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	s.submit(v1).AssertOk()
	v2 := s.clone(policyID, `{}`)["id"].(string)
	s.Equal(s.getTestCases(v1)["test_cases"], s.getTestCases(v2)["test_cases"])
}

// --- validate and submit (RF-010) --------------------------------------------------------

func (s *PolicyTestSuite) TestValidateReportsAnIncompleteDraft() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	report := s.json(s.validate("demo_analyst", v1).AssertOk())
	s.Equal(false, report["valid"])
	s.Equal([]string{"scorecard:missing_section", "decision_matrix:missing_section", "test_cases:no_required_test_cases"}, detailPairs(report))
	s.Nil(report["score_range"])
	s.Equal(map[string]any{"rac_rules": float64(0), "scorecard_characteristics": float64(0), "decision_matrix_rows": float64(0),
		"test_cases": float64(0), "required_test_cases": float64(0)}, report["summary"])
	pending := report["pending_checks"].([]any)
	s.Require().Len(pending, 1)
	s.Equal("golden_test_execution", pending[0].(map[string]any)["check"], "not run is never reported as passed")
	s.Contains(pending[0].(map[string]any)["reason"], "other validation issues")
	s.Equal(map[string]any{"executed": float64(0), "passed": float64(0), "failed": float64(0), "results": []any{}}, report["golden_tests"])
	s.Equal("DRAFT", s.getVersion(v1)["status"], "validate changes nothing")
}

// --- golden test execution (RF-013) -------------------------------------------------------

func (s *PolicyTestSuite) TestValidateExecutesTheGoldenTests() {
	v1 := s.demoDraft()
	s.putTestCases(v1, demoTestCasesBody()).AssertOk()
	report := s.json(s.validate("demo_analyst", v1).AssertOk())
	s.Equal(true, report["valid"], detailPairs(report))
	s.Equal([]any{}, report["pending_checks"], "everything ran")
	golden := report["golden_tests"].(map[string]any)
	s.Equal(float64(3), golden["executed"])
	s.Equal(float64(3), golden["passed"])
	s.Equal(float64(0), golden["failed"])
	results := golden["results"].([]any)
	s.Require().Len(results, 3)
	happy := results[0].(map[string]any)
	s.Equal("GOLDEN_HAPPY_PATH", happy["case_code"])
	s.Equal(true, happy["passed"])
	s.Equal(map[string]any{"outcome": "DECISION", "decision": "APPROVE", "grade": "A", "total_score": "685.00",
		"reason_codes": []any{}, "message": nil}, happy["actual"])
	s.Equal([]any{}, happy["mismatches"])
	reject := results[1].(map[string]any)["actual"].(map[string]any)
	s.Equal("REJECT", reject["decision"])
	s.Nil(reject["grade"], "a RAC reject is never scored")
	s.Nil(reject["total_score"])
	s.Equal([]any{"AGE_BELOW_MINIMUM"}, reject["reason_codes"])
	missing := results[2].(map[string]any)["actual"].(map[string]any)
	s.Equal("INPUT_VALIDATION_ERROR", missing["outcome"], "bad input is an error, never a REJECT")
	s.Nil(missing["decision"])
	s.Contains(missing["message"], "monthly_income: required")
}

func (s *PolicyTestSuite) TestSubmitRefusesARequiredGoldenTestThatFails() {
	v1 := s.demoDraft()
	// The applicant is approved, so expecting a REJECT is wrong.
	s.putTestCases(v1, demoTestCasesBody(`"decision":"APPROVE","grade":"A"`, `"decision":"REJECT","grade":"A"`)).AssertOk()

	report := s.json(s.validate("demo_analyst", v1).AssertOk())
	s.Equal(false, report["valid"])
	want := []string{"test_cases[0].expected.decision:decision_mismatch"}
	s.Equal(want, detailPairs(report))
	golden := report["golden_tests"].(map[string]any)
	s.Equal(float64(1), golden["failed"])
	first := golden["results"].([]any)[0].(map[string]any)
	s.Equal(false, first["passed"])
	s.Equal("expected REJECT, got APPROVE", first["mismatches"].([]any)[0].(map[string]any)["message"])

	resp := s.submitRaw(v1)
	resp.AssertStatus(http.StatusUnprocessableEntity)
	code, _ := s.errorOf(resp)
	s.Equal("POLICY_VALIDATION_FAILED", code)
	s.Equal(want, s.issuePairs(resp))
	s.Equal("DRAFT", s.getVersion(v1)["status"], "a failing golden test stops the submit")
}

func (s *PolicyTestSuite) TestAnOptionalGoldenTestThatFailsDoesNotBlockSubmit() {
	v1 := s.demoDraft()
	s.putTestCases(v1, demoTestCasesBody(
		`"case_code":"GOLDEN_HAPPY_PATH"`, `"case_code":"GOLDEN_HAPPY_PATH","required":false`,
		`"decision":"APPROVE","grade":"A"`, `"decision":"REJECT","grade":"A"`)).AssertOk()
	report := s.json(s.validate("demo_analyst", v1).AssertOk())
	s.Equal(true, report["valid"], detailPairs(report))
	golden := report["golden_tests"].(map[string]any)
	s.Equal(float64(1), golden["failed"], "it is reported")
	s.Equal(false, golden["results"].([]any)[0].(map[string]any)["passed"])
	s.submitRaw(v1).AssertOk()
}

func (s *PolicyTestSuite) TestGoldenTestsDetectARuleChange() {
	v1 := s.demoDraft()
	s.putTestCases(v1, demoTestCasesBody()).AssertOk()
	s.Equal(true, s.json(s.validate("demo_analyst", v1).AssertOk())["valid"])
	// The happy-path applicant is 29, so raising the minimum age to 30 changes its decision.
	s.putRules("demo_analyst", v1, strings.Replace(demoRulesBody, "age_years >= 21", "age_years >= 30", 1)).AssertOk()
	report := s.json(s.validate("demo_analyst", v1).AssertOk())
	s.Equal(false, report["valid"])
	s.Contains(detailPairs(report), "test_cases[0].expected.decision:decision_mismatch")
}

func (s *PolicyTestSuite) TestAFrozenVersionIsExecutedAgainstItsOwnSnapshot() {
	v1 := s.demoDraft()
	s.putTestCases(v1, demoTestCasesBody()).AssertOk()
	s.submitRaw(v1).AssertOk()
	report := s.json(s.validate("demo_analyst", v1).AssertOk())
	s.Equal("PENDING_APPROVAL", report["status"])
	s.Equal(true, report["valid"], detailPairs(report))
	s.Equal(float64(3), report["golden_tests"].(map[string]any)["passed"])
}

func (s *PolicyTestSuite) TestACaseWithZeroIncomeIsRefusedWhereItIsSaved() {
	v1 := s.demoDraft()
	// monthly_income 0.00 is below the dictionary minimum, so it is refused where the case is saved.
	zero := strings.Replace(seeders.DemoApplicant, `"monthly_income":"12000000.00"`, `"monthly_income":"0.00"`, 1)
	resp := s.putTestCases(v1, `{"test_cases":[{"case_code":"CASE_ZERO","input":`+zero+`,"expected":{"outcome":"DECISION","decision":"REJECT"}}]}`)
	resp.AssertStatus(http.StatusUnprocessableEntity)
	s.Contains(s.issuePairs(resp), "test_cases[0].input.monthly_income:below_min_value")
}

func (s *PolicyTestSuite) TestValidateAndSubmitAgreeOnACompletePolicy() {
	v1 := s.demoDraft()
	s.putTestCases(v1, demoTestCasesBody()).AssertOk()
	report := s.json(s.validate("demo_analyst", v1).AssertOk())
	s.Equal(true, report["valid"], detailPairs(report))
	s.Equal([]any{}, report["issues"])
	s.Equal(map[string]any{"min": "420.00", "max": "685.00"}, report["score_range"])
	s.Equal(map[string]any{"rac_rules": float64(3), "scorecard_characteristics": float64(3), "decision_matrix_rows": float64(3),
		"test_cases": float64(3), "required_test_cases": float64(3)}, report["summary"])

	body := s.json(s.submitRaw(v1).AssertOk())
	snap := body["snapshot"].(map[string]any)
	s.Equal(float64(2), snap["format"])
	s.Len(snap["test_cases"], 3)
	raw, err := json.Marshal(snap)
	s.Require().NoError(err)
	s.True(policydomain.VerifySnapshot(raw, body["checksum"].(string)))

	frozen := s.json(s.validate("demo_analyst", v1).AssertOk())
	s.Equal("PENDING_APPROVAL", frozen["status"])
	s.Equal(true, frozen["valid"], "a frozen version can be validated again")
}

func (s *PolicyTestSuite) TestSubmitReturnsTheSameIssuesAsValidate() {
	v1 := s.demoDraft()
	// Optional cases do not satisfy the requirement, and a stale reason code is reported.
	s.putTestCases(v1, demoTestCasesBody(`"case_code":"GOLDEN_HAPPY_PATH"`, `"case_code":"GOLDEN_HAPPY_PATH","required":false`,
		`"case_code":"GOLDEN_AGE_BELOW_MINIMUM"`, `"case_code":"GOLDEN_AGE_BELOW_MINIMUM","required":false`,
		`"case_code":"GOLDEN_MISSING_INCOME","required":true`, `"case_code":"GOLDEN_MISSING_INCOME","required":false`)).AssertOk()
	s.putRules("demo_analyst", v1, `{"rules":[{"rule_code":"RAC_BLACKLIST","reason_code":"BLACKLISTED","action":"REJECT","expression":"is_blacklisted == false"}]}`).AssertOk()

	report := s.json(s.validate("demo_analyst", v1).AssertOk())
	want := []string{"test_cases[1].expected.reason_codes[0]:unknown_reason_code", "test_cases:no_required_test_cases"}
	s.Equal(want, detailPairs(report))

	resp := s.submitRaw(v1)
	resp.AssertStatus(http.StatusUnprocessableEntity)
	code, _ := s.errorOf(resp)
	s.Equal("POLICY_VALIDATION_FAILED", code)
	s.Equal(want, s.issuePairs(resp))
	s.Equal("DRAFT", s.getVersion(v1)["status"])
}

func (s *PolicyTestSuite) TestEverySectionIsCheckedTogether() {
	v1 := s.demoDraft()
	s.putTestCases(v1, demoTestCasesBody()).AssertOk()
	s.putScorecard(v1, strings.Replace(demoScorecardBody, `"350.00"`, `"700.00"`, 1)).AssertOk()
	s.send("demo_analyst", "PATCH", versionsPath+"/"+v1, `{"dictionary_schema_version":1}`).AssertOk()
	got := detailPairs(s.json(s.validate("demo_analyst", v1).AssertOk()))
	for _, want := range []string{
		"rules[1].expression:unknown_variable",
		"scorecard.characteristics[1].variable:unknown_variable",
		"decision_matrix.rows[2].max_score:does_not_cover_score_range",
		"test_cases[0].input.is_blacklisted:unknown_variable",
	} {
		s.Contains(got, want)
	}
}

func (s *PolicyTestSuite) TestValidateOfAnUnknownVersion() {
	s.validate("demo_analyst", missingID).AssertNotFound()
	s.validate("demo_analyst", "not-a-uuid").AssertNotFound()
	s.send("demo_analyst", "GET", testCasesPath(missingID), "").AssertNotFound()
	s.putTestCases(missingID, `{"test_cases":[]}`).AssertNotFound()
}

// --- the whole M2 flow over HTTP --------------------------------------------------------

// TestM2EndToEnd runs the whole policy-configuration flow of Milestone 2 over HTTP as the
// analyst, with the writes the Postman smoke run does not exercise: open a policy, configure
// RAC rules, scorecard, matrix, and golden cases, validate, submit, and clone into a revision.
func (s *PolicyTestSuite) TestM2EndToEnd() {
	resp := s.send("demo_analyst", "POST", policiesPath, `{"product_code":"KTA_DEMO","description":"M2 end to end"}`)
	resp.AssertCreated()
	created := s.json(resp)
	policyID := created["id"].(string)
	v1 := created["versions"].([]any)[0].(map[string]any)["id"].(string)

	s.putRules("demo_analyst", v1, demoRulesBody).AssertOk()
	s.putScorecard(v1, demoScorecardBody).AssertOk()
	s.putMatrix(v1, demoMatrixBody).AssertOk()
	cases := make([]string, 0, len(seeders.DemoTestCases()))
	for _, c := range seeders.DemoTestCases() {
		exp := `{"outcome":"` + c.Outcome + `"`
		if c.Decision != "" {
			exp += `,"decision":"` + c.Decision + `"`
		}
		if c.Grade != "" {
			exp += `,"grade":"` + c.Grade + `"`
		}
		if len(c.ReasonCodes) > 0 {
			exp += `,"reason_codes":["` + strings.Join(c.ReasonCodes, `","`) + `"]`
		}
		cases = append(cases, `{"case_code":"`+c.Code+`","input":`+string(c.Input)+`,"expected":`+exp+`}}`)
	}
	s.putTestCases(v1, `{"test_cases":[`+strings.Join(cases, ",")+`]}`).AssertOk()

	report := s.json(s.validate("demo_analyst", v1).AssertOk())
	s.Equal(true, report["valid"], detailPairs(report))
	frozen := s.json(s.submitRaw(v1).AssertOk())
	s.Equal("PENDING_APPROVAL", frozen["status"])
	s.Len(frozen["snapshot"].(map[string]any)["test_cases"], 6)

	// A revision starts as an exact copy that is valid as it is.
	v2 := s.clone(policyID, `{"description":"Tighten the age rule."}`)["id"].(string)
	s.Equal(true, s.json(s.validate("demo_analyst", v2).AssertOk())["valid"])
	s.putRules("demo_analyst", v2, strings.Replace(demoRulesBody, "age_years >= 21", "age_years >= 25", 1)).AssertOk()
	revised := s.json(s.submitRaw(v2).AssertOk())
	s.NotEqual(frozen["checksum"], revised["checksum"])
	detail := s.json(s.send("demo_approver_1", "GET", policiesPath+"/"+policyID, "").AssertOk())
	s.Len(detail["versions"], 2, "the approver sees both versions")

	var n []int
	s.Require().NoError(facades.DB().Select(&n, `SELECT COUNT(*) FROM policy.test_cases WHERE policy_version_id = $1::uuid`, v2))
	s.Equal([]int{6}, n)
}

func (s *PolicyTestSuite) TestTheSeededDemoPolicyIsValid() {
	s.Require().NoError(seeders.SeedDemoPolicy())
	list := s.json(s.send("demo_auditor", "GET", policiesPath+"?product_code=KTA_DEMO", "").AssertOk())["data"].([]any)
	v1 := list[0].(map[string]any)["latest_version"].(map[string]any)["id"].(string)
	s.Len(s.getTestCases(v1)["test_cases"], 6)
	report := s.json(s.validate("demo_analyst", v1).AssertOk())
	s.Equal(true, report["valid"], detailPairs(report))
	s.Require().NoError(seeders.SeedDemoPolicy())
	s.Len(s.getTestCases(v1)["test_cases"], 6, "seeding twice adds nothing")
}
