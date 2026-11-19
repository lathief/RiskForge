package feature

import (
	"strings"

	"github.com/goravel/framework/contracts/http"
	contractstesting "github.com/goravel/framework/contracts/testing/http"

	"goravel/app/facades"
	"goravel/database/seeders"
)

// The Decision API (RF-014). The policy is the seeded demo policy, activated the way the
// development seeder does it; real activation arrives with the approval workflow (M4).

const evaluatePath = "/api/v1/decisions/evaluate"

func evaluateBody(changes ...string) string {
	applicant := seeders.DemoApplicant
	for i := 0; i+1 < len(changes); i += 2 {
		applicant = strings.Replace(applicant, changes[i], changes[i+1], 1)
	}
	return `{"application_id":"LOAN-APP-DEMO-0001","product_code":"KTA_DEMO","applicant":` + applicant + `}`
}

func (s *PolicyTestSuite) evaluate(user, body string) contractstesting.Response {
	return s.send(user, "POST", evaluatePath, body)
}

// activate seeds the demo policy and makes it the active one.
func (s *PolicyTestSuite) activate() {
	s.Require().NoError(seeders.SeedDemoPolicy())
	s.Require().NoError(seeders.SeedDemoActivePolicy())
}

func (s *PolicyTestSuite) TestEvaluateAuthorization() {
	s.activate()
	for user, want := range map[string]int{
		"": http.StatusUnauthorized, "demo_admin": http.StatusForbidden, "demo_analyst": http.StatusForbidden,
		"demo_approver_1": http.StatusForbidden, "demo_auditor": http.StatusForbidden,
		"demo_loan_application": http.StatusOK,
	} {
		s.evaluate(user, evaluateBody()).AssertStatus(want)
	}
}

func (s *PolicyTestSuite) TestEvaluateApprovesTheExampleApplicantWithAFullTrace() {
	s.activate()
	resp := s.evaluate("demo_loan_application", evaluateBody())
	resp.AssertOk()
	body := s.json(resp)

	s.Equal("LOAN-APP-DEMO-0001", body["application_id"])
	s.Equal("KTA_DEMO", body["product_code"])
	s.Equal("COMPLETED", body["status"])
	s.Equal("APPROVE", body["final_decision"])
	s.Equal("A", body["risk_grade"])
	s.Equal("685.00", body["total_score"])
	s.Equal("350.00", body["base_score"])
	s.Equal([]any{}, body["reason_codes"])
	s.Equal(false, body["refer_flag"])

	version := body["policy_version"].(map[string]any)
	s.Equal(float64(1), version["number"])
	s.Len(version["checksum"], 64)
	var active []string
	s.Require().NoError(selectStrings(&active, `SELECT policy_version_id::text FROM policy.active_policy_bindings`))
	s.Equal([]string{version["id"].(string)}, active, "the decision names the version the binding points to")

	features := body["derived_features"].([]any)
	s.Require().Len(features, 2)
	dsr := features[0].(map[string]any)
	s.Equal("dsr_ratio", dsr["code"])
	s.Equal("5/24", dsr["exact"])
	s.Equal("0.208333", dsr["value"])
	s.Equal(float64(1), dsr["formula_version"])
	s.Equal("existing_installment / monthly_income", dsr["expression"])
	s.Equal("9500000.00", features[1].(map[string]any)["value"])

	rac := body["rac_results"].([]any)
	s.Require().Len(rac, 3)
	first := rac[0].(map[string]any)
	s.Equal("RAC_MIN_AGE", first["rule_code"])
	s.Equal("AGE_BELOW_MINIMUM", first["reason_code"])
	s.Equal("REJECT", first["action"])
	s.Equal(true, first["passed"])
	s.Equal([]any{map[string]any{"variable": "age_years", "value": "29"}}, first["inputs"])
	s.Equal([]any{}, body["rac_not_evaluated"])

	score := body["score_breakdown"].([]any)
	s.Require().Len(score, 3)
	age := score[0].(map[string]any)
	s.Equal("age_group", age["characteristic"])
	s.Equal("85.00", age["points"])
	s.Equal(float64(2), age["bin_order"])
	s.Equal(false, age["missing"])
	s.Equal("150.00", score[1].(map[string]any)["points"])
	s.Equal("100.00", score[2].(map[string]any)["points"])
	matrix := body["matrix"].(map[string]any)
	s.Equal("APPROVE", matrix["decision"])
}

func (s *PolicyTestSuite) TestEvaluateTheOtherWorkedRuns() {
	s.activate()
	tests := map[string]struct {
		changes []string
		want    string
		grade   any
		score   any
		reasons []any
	}{
		"under age":    {[]string{`"age_years":29`, `"age_years":19`}, "REJECT", nil, nil, []any{"AGE_BELOW_MINIMUM"}},
		"blacklisted":  {[]string{`"is_blacklisted":false`, `"is_blacklisted":true`}, "REJECT", nil, nil, []any{"BLACKLISTED"}},
		"needs review": {[]string{`"slik_max_kol":1`, `"slik_max_kol":3`}, "REFER", "A", "685.00", []any{"SLIK_REVIEW_REQUIRED"}},
	}
	for name, tc := range tests {
		resp := s.evaluate("demo_loan_application", evaluateBody(tc.changes...))
		resp.AssertOk()
		body := s.json(resp)
		s.Equal(tc.want, body["final_decision"], name)
		s.Equal(tc.grade, body["risk_grade"], name)
		s.Equal(tc.score, body["total_score"], name)
		s.Equal(tc.reasons, body["reason_codes"], name)
	}

	// A hard reject stops before scoring and says which rules did not run.
	body := s.json(s.evaluate("demo_loan_application", evaluateBody(`"age_years":29`, `"age_years":19`)).AssertOk())
	s.Equal([]any{}, body["score_breakdown"])
	s.Nil(body["matrix"])
	s.Equal([]any{"RAC_BLACKLIST", "RAC_SLIK"}, body["rac_not_evaluated"])
	s.Len(body["rac_results"], 1)
	s.Len(body["derived_features"], 2)
}

func (s *PolicyTestSuite) TestEvaluateIsDeterministicAndStoresNothing() {
	s.activate()
	var before []int
	s.Require().NoError(selectInts(&before, `SELECT COUNT(*) FROM policy.policy_versions`))
	first := s.json(s.evaluate("demo_loan_application", evaluateBody()).AssertOk())
	second := s.json(s.evaluate("demo_loan_application", evaluateBody()).AssertOk())
	s.Equal(first, second, "the same request and the same active version give the same response")
	var after []int
	s.Require().NoError(selectInts(&after, `SELECT COUNT(*) FROM policy.policy_versions`))
	s.Equal(before, after)
}

func (s *PolicyTestSuite) TestEvaluateInputErrorsAreNeverARejection() {
	s.activate()
	tests := map[string]struct {
		changes []string
		want    string
	}{
		"null required":     {[]string{`"monthly_income":"12000000.00"`, `"monthly_income":null`}, "applicant.monthly_income:required"},
		"zero income":       {[]string{`"monthly_income":"12000000.00"`, `"monthly_income":"0.00"`}, "applicant.monthly_income:below_min_value"},
		"wrong type":        {[]string{`"age_years":29`, `"age_years":"29"`}, "applicant.age_years:invalid_type"},
		"number for money":  {[]string{`"monthly_income":"12000000.00"`, `"monthly_income":12000000`}, "applicant.monthly_income:invalid_type"},
		"out of range":      {[]string{`"age_years":29`, `"age_years":121`}, "applicant.age_years:above_max_value"},
		"too many decimals": {[]string{`"monthly_income":"12000000.00"`, `"monthly_income":"12000000.001"`}, "applicant.monthly_income:exceeds_scale"},
		"derived supplied":  {[]string{`"age_years":29`, `"age_years":29,"dsr_ratio":"0.01"`}, "applicant.dsr_ratio:derived_not_allowed"},
		"unknown key":       {[]string{`"age_years":29`, `"age_years":29,"hair_colour":"red"`}, "applicant.hair_colour:unknown_variable"},
	}
	for name, tc := range tests {
		resp := s.evaluate("demo_loan_application", evaluateBody(tc.changes...))
		resp.AssertStatus(http.StatusUnprocessableEntity)
		code, _ := s.errorOf(resp)
		s.Equal("INPUT_VALIDATION_ERROR", code, name)
		s.Contains(s.issuePairs(resp), tc.want, name)
	}
}

func (s *PolicyTestSuite) TestEvaluateRejectsAMalformedRequest() {
	s.activate()
	tests := map[string]struct {
		body string
		want []string
	}{
		"not an object":   {`[]`, nil},
		"unknown field":   {`{"application_id":"A-1","product_code":"KTA_DEMO","applicant":{},"extra":1}`, []string{"extra:not_allowed"}},
		"empty":           {`{}`, []string{"applicant:required", "application_id:required", "product_code:required"}},
		"id wrong type":   {`{"application_id":1,"product_code":"KTA_DEMO","applicant":{}}`, []string{"application_id:must_be_string"}},
		"id bad format":   {`{"application_id":"has space","product_code":"KTA_DEMO","applicant":{}}`, []string{"application_id:invalid_format"}},
		"product bad":     {`{"application_id":"A-1","product_code":"kta","applicant":{}}`, []string{"product_code:invalid_format"}},
		"applicant array": {`{"application_id":"A-1","product_code":"KTA_DEMO","applicant":[]}`, []string{"applicant:must_be_object"}},
		"applicant null":  {`{"application_id":"A-1","product_code":"KTA_DEMO","applicant":null}`, []string{"applicant:required"}},
	}
	for name, tc := range tests {
		resp := s.evaluate("demo_loan_application", tc.body)
		resp.AssertStatus(http.StatusUnprocessableEntity)
		code, _ := s.errorOf(resp)
		s.Equal("INPUT_VALIDATION_ERROR", code, name)
		if tc.want != nil {
			s.ElementsMatch(tc.want, s.issuePairs(resp), name)
		}
	}
}

func (s *PolicyTestSuite) TestEvaluateWithoutAnActivePolicyIsAConflict() {
	// The demo draft exists but nothing was activated.
	s.Require().NoError(seeders.SeedDemoPolicy())
	resp := s.evaluate("demo_loan_application", evaluateBody())
	resp.AssertStatus(http.StatusConflict)
	code, _ := s.errorOf(resp)
	s.Equal("POLICY_NOT_ACTIVE", code, "a draft or a pending version is never evaluated")

	// An unknown product is a 404, and a retired product is not evaluated.
	resp = s.evaluate("demo_loan_application", strings.Replace(evaluateBody(), "KTA_DEMO", "NO_SUCH_PRODUCT", 1))
	resp.AssertNotFound()
	code, _ = s.errorOf(resp)
	s.Equal("PRODUCT_NOT_FOUND", code)
	resp = s.evaluate("demo_loan_application", strings.Replace(evaluateBody(), "KTA_DEMO", "OLD_PROD", 1))
	resp.AssertStatus(http.StatusConflict)
	code, _ = s.errorOf(resp)
	s.Equal("POLICY_NOT_ACTIVE", code)
}

func (s *PolicyTestSuite) TestAPendingVersionIsNotEvaluatedUntilItIsActive() {
	s.Require().NoError(seeders.SeedDemoPolicy())
	list := s.json(s.send("demo_auditor", "GET", policiesPath+"?product_code=KTA_DEMO", "").AssertOk())["data"].([]any)
	v1 := list[0].(map[string]any)["latest_version"].(map[string]any)["id"].(string)
	s.submitRaw(v1).AssertOk() // PENDING_APPROVAL
	resp := s.evaluate("demo_loan_application", evaluateBody())
	resp.AssertStatus(http.StatusConflict)
	code, _ := s.errorOf(resp)
	s.Equal("POLICY_NOT_ACTIVE", code)
}

func (s *PolicyTestSuite) TestActivationSeedingIsRepeatableAndLeavesTheVersionFrozen() {
	s.activate()
	s.Require().NoError(seeders.SeedDemoActivePolicy(), "a second run changes nothing")
	var n []int
	s.Require().NoError(selectInts(&n, `SELECT COUNT(*) FROM policy.active_policy_bindings`))
	s.Equal([]int{1}, n)

	list := s.json(s.send("demo_auditor", "GET", policiesPath+"?product_code=KTA_DEMO", "").AssertOk())["data"].([]any)
	latest := list[0].(map[string]any)["latest_version"].(map[string]any)
	s.Equal("ACTIVE", latest["status"])
	v1 := latest["id"].(string)
	s.putTestCases(v1, `{"test_cases":[]}`).AssertStatus(http.StatusConflict)
	s.putRules("demo_analyst", v1, demoRulesBody).AssertStatus(http.StatusConflict)
	s.Error(s.exec(`UPDATE policy.policy_versions SET snapshot = '{}'::jsonb WHERE id = $1::uuid`, v1), "the snapshot of the active version cannot change")
	// A clone starts a new draft; the active version keeps deciding.
	policyID := list[0].(map[string]any)["id"].(string)
	s.clone(policyID, `{}`)
	s.evaluate("demo_loan_application", evaluateBody()).AssertOk()
}

func (s *PolicyTestSuite) TestTheActiveBindingCannotPointAtAnythingButAnActiveVersionOfItsPackage() {
	s.activate()
	list := s.json(s.send("demo_auditor", "GET", policiesPath+"?product_code=KTA_DEMO", "").AssertOk())["data"].([]any)
	policyID := list[0].(map[string]any)["id"].(string)
	v1 := list[0].(map[string]any)["latest_version"].(map[string]any)["id"].(string)
	draft := s.clone(policyID, `{}`)["id"].(string)

	s.Error(s.exec(`UPDATE policy.active_policy_bindings SET policy_version_id = $1::uuid`, draft), "never a draft")
	s.Error(s.exec(`INSERT INTO policy.active_policy_bindings (package_id, policy_version_id) VALUES ($1::uuid, $2::uuid)`, policyID, v1),
		"one binding per package")

	// A second package (another product and scope) cannot borrow this version.
	s.Require().NoError(s.exec(`INSERT INTO catalog.products (code, name, status) VALUES ('OTHER_PROD', 'Other', 'ACTIVE') ON CONFLICT (code) DO NOTHING`))
	_, other := s.createPolicy(`{"product_code":"OTHER_PROD"}`)
	var pkg []string
	s.Require().NoError(selectStrings(&pkg, `SELECT package_id::text FROM policy.policy_versions WHERE id = $1::uuid`, other))
	s.Error(s.exec(`INSERT INTO policy.active_policy_bindings (package_id, policy_version_id) VALUES ($1::uuid, $2::uuid)`, pkg[0], v1),
		"a version belongs to one package")
}

func selectStrings(dest *[]string, query string, args ...any) error {
	return facades.DB().Select(dest, query, args...)
}

func selectInts(dest *[]int, query string, args ...any) error {
	return facades.DB().Select(dest, query, args...)
}
