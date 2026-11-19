package feature

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/goravel/framework/contracts/http"
	contractstesting "github.com/goravel/framework/contracts/testing/http"

	"goravel/app/facades"
	policydomain "goravel/app/modules/policy/domain"
	"goravel/database/seeders"
)

const demoRulesBody = `{"rules":[
	{"rule_code":"RAC_MIN_AGE","reason_code":"AGE_BELOW_MINIMUM","action":"REJECT","expression":"age_years >= 21","description":"Adults only"},
	{"rule_code":"RAC_BLACKLIST","reason_code":"BLACKLISTED","action":"REJECT","expression":"is_blacklisted == false"},
	{"rule_code":"RAC_SLIK","reason_code":"SLIK_REVIEW_REQUIRED","action":"REFER","expression":"slik_max_kol <= 2"}]}`

func racPath(versionID string) string { return versionsPath + "/" + versionID + "/rac-rules" }

func (s *PolicyTestSuite) putRules(user, versionID, body string) contractstesting.Response {
	req := s.Http(s.T()).WithHeader("Content-Type", "application/json")
	if user != "" {
		req = req.WithToken(s.tokens[user])
	}
	resp, err := req.Put(racPath(versionID), strings.NewReader(body))
	s.Require().NoError(err)
	return resp
}

func (s *PolicyTestSuite) getRules(versionID string) map[string]any {
	return s.json(s.send("demo_auditor", "GET", racPath(versionID), "").AssertOk())
}

// ruleCodes lists the rule codes of a rule-set response in the order returned.
func ruleCodes(set map[string]any) []string {
	var out []string
	for _, item := range set["rules"].([]any) {
		out = append(out, item.(map[string]any)["rule_code"].(string))
	}
	return out
}

// issuePairs returns "field:issue" for every detail of an error response.
func (s *PolicyTestSuite) issuePairs(resp contractstesting.Response) []string {
	_, details := s.errorOf(resp)
	out := make([]string, 0, len(details))
	for _, d := range details {
		m := d.(map[string]any)
		out = append(out, m["field"].(string)+":"+m["issue"].(string))
	}
	return out
}

// --- authorization ----------------------------------------------------------------------

func (s *PolicyTestSuite) TestRacAuthorization() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	readers := map[string]int{
		"": http.StatusUnauthorized, "demo_admin": http.StatusForbidden, "demo_analyst": http.StatusOK,
		"demo_approver_1": http.StatusOK, "demo_auditor": http.StatusOK, "demo_loan_application": http.StatusForbidden,
	}
	for user, want := range readers {
		s.send(user, "GET", racPath(v1), "").AssertStatus(want)
	}
	for _, user := range []string{"", "demo_admin", "demo_approver_1", "demo_auditor", "demo_loan_application"} {
		want := http.StatusForbidden
		if user == "" {
			want = http.StatusUnauthorized
		}
		s.putRules(user, v1, demoRulesBody).AssertStatus(want)
	}
	s.Empty(s.getRules(v1)["rules"], "no rejected write left a rule")
	s.putRules("demo_analyst", v1, demoRulesBody).AssertOk()
}

// --- replace and read -------------------------------------------------------------------

func (s *PolicyTestSuite) TestPutStoresRulesInOrderAndGetReturnsThem() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	resp := s.putRules("demo_analyst", v1, demoRulesBody)
	resp.AssertOk()
	body := s.json(resp)
	s.Equal(v1, body["policy_version_id"])
	s.Equal("DRAFT", body["status"])
	s.Equal(float64(8), body["dictionary_schema_version"])
	s.Equal([]string{"RAC_MIN_AGE", "RAC_BLACKLIST", "RAC_SLIK"}, ruleCodes(body))
	first := body["rules"].([]any)[0].(map[string]any)
	s.Equal(map[string]any{
		"order": float64(1), "rule_code": "RAC_MIN_AGE", "reason_code": "AGE_BELOW_MINIMUM",
		"expression": "age_years >= 21", "action": "REJECT", "description": "Adults only",
	}, first)
	s.Equal(float64(3), body["rules"].([]any)[2].(map[string]any)["order"])
	s.Equal("REFER", body["rules"].([]any)[2].(map[string]any)["action"])

	s.Equal(body, s.getRules(v1), "GET returns what PUT stored")
}

func (s *PolicyTestSuite) TestPutReordersClearsAndIgnoresAnUnchangedList() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	first := s.json(s.putRules("demo_analyst", v1, demoRulesBody).AssertOk())

	again := s.json(s.putRules("demo_analyst", v1, demoRulesBody).AssertOk())
	s.Equal(first["updated_at"], again["updated_at"], "an identical list writes nothing")

	reordered := `{"rules":[
		{"rule_code":"RAC_SLIK","reason_code":"SLIK_REVIEW_REQUIRED","action":"REFER","expression":"slik_max_kol <= 2"},
		{"rule_code":"RAC_MIN_AGE","reason_code":"AGE_BELOW_MINIMUM","action":"REJECT","expression":"age_years >= 21","description":"Adults only"}]}`
	body := s.json(s.putRules("demo_analyst", v1, reordered).AssertOk())
	s.Equal([]string{"RAC_SLIK", "RAC_MIN_AGE"}, ruleCodes(body), "the array position is the evaluation order")
	s.Equal(float64(1), body["rules"].([]any)[0].(map[string]any)["order"])

	body = s.json(s.putRules("demo_analyst", v1, `{"rules":[]}`).AssertOk())
	s.Equal([]any{}, body["rules"], "an empty list removes every rule and is [] not null")
	s.Equal([]any{}, s.getRules(v1)["rules"])
}

func (s *PolicyTestSuite) TestExpressionsAreStoredTrimmed() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	body := s.json(s.putRules("demo_analyst", v1,
		`{"rules":[{"rule_code":"RAC_MIN_AGE","reason_code":"AGE_BELOW_MINIMUM","action":"REJECT","expression":"  age_years >= 21  "}]}`).AssertOk())
	s.Equal("age_years >= 21", body["rules"].([]any)[0].(map[string]any)["expression"])
}

func (s *PolicyTestSuite) TestRulesOfAnUnknownVersion() {
	s.send("demo_analyst", "GET", racPath(missingID), "").AssertNotFound()
	s.send("demo_analyst", "GET", racPath("not-a-uuid"), "").AssertNotFound()
	s.putRules("demo_analyst", missingID, demoRulesBody).AssertNotFound()
	s.putRules("demo_analyst", "not-a-uuid", demoRulesBody).AssertNotFound()
}

// --- validation -------------------------------------------------------------------------

func (s *PolicyTestSuite) TestPutValidation() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	rule := func(code, reason, action, expression string) string {
		return fmt.Sprintf(`{"rule_code":%q,"reason_code":%q,"action":%q,"expression":%q}`, code, reason, action, expression)
	}
	cases := map[string]struct {
		body string
		want []string
	}{
		"missing rules":      {`{}`, []string{"rules:required"}},
		"rules not a list":   {`{"rules":{}}`, []string{"rules:must_be_array"}},
		"element not object": {`{"rules":["x"]}`, []string{"rules[0]:must_be_object"}},
		"unknown top key":    {`{"rules":[],"extra":1}`, []string{"extra:not_allowed"}},
		"unknown rule key":   {`{"rules":[{"rule_code":"RAC_A","reason_code":"A_REASON","action":"REJECT","expression":"true","order":5}]}`, []string{"rules[0].order:not_allowed"}},
		"wrong types":        {`{"rules":[{"rule_code":1,"reason_code":[],"action":true,"expression":{}}]}`, []string{"rules[0].rule_code:must_be_string", "rules[0].reason_code:must_be_string", "rules[0].action:must_be_string", "rules[0].expression:must_be_string"}},
		"empty rule":         {`{"rules":[{}]}`, []string{"rules[0].rule_code:required", "rules[0].reason_code:required", "rules[0].action:required", "rules[0].expression:required"}},
		"bad codes":          {`{"rules":[` + rule("bad code", "x", "BLOCK", "true") + `]}`, []string{"rules[0].rule_code:invalid_format", "rules[0].reason_code:invalid_format", "rules[0].action:invalid_value"}},
		"no reason code":     {`{"rules":[` + rule("RAC_A", "", "REJECT", "true") + `]}`, []string{"rules[0].reason_code:required"}},
		"duplicate code":     {`{"rules":[` + rule("RAC_A", "REASON_A", "REJECT", "true") + `,` + rule("RAC_A", "REASON_B", "REFER", "true") + `]}`, []string{"rules[1].rule_code:duplicate"}},
		"syntax error":       {`{"rules":[` + rule("RAC_A", "REASON_A", "REJECT", "age_years >=") + `]}`, []string{"rules[0].expression:syntax_error"}},
		"unknown variable":   {`{"rules":[` + rule("RAC_A", "REASON_A", "REJECT", "agee_years >= 21") + `]}`, []string{"rules[0].expression:unknown_variable"}},
		"type error":         {`{"rules":[` + rule("RAC_A", "REASON_A", "REJECT", "is_blacklisted >= 21") + `]}`, []string{"rules[0].expression:type_error"}},
		"not boolean":        {`{"rules":[` + rule("RAC_A", "REASON_A", "REJECT", "age_years + 1") + `]}`, []string{"rules[0].expression:type_error"}},
		"string literal":     {`{"rules":[` + rule("RAC_A", "REASON_A", "REJECT", `age_years == "21"`) + `]}`, []string{"rules[0].expression:unsupported_syntax"}},
		"function call":      {`{"rules":[` + rule("RAC_A", "REASON_A", "REJECT", "abs(age_years) > 1") + `]}`, []string{"rules[0].expression:unsupported_syntax"}},
		"multi line":         {`{"rules":[` + rule("RAC_A", "REASON_A", "REJECT", "age_years >= 21\nand true") + `]}`, []string{"rules[0].expression:invalid_characters"}},
		"too long":           {`{"rules":[` + rule("RAC_A", "REASON_A", "REJECT", strings.Repeat("age_years >= 21 and ", 30)+"true") + `]}`, []string{"rules[0].expression:too_long"}},
		"several at once": {
			`{"rules":[` + rule("RAC_A", "REASON_A", "REJECT", "agee_years >= 21") + `,` + rule("RAC_B", "", "REFER", "age_years >=") + `]}`,
			[]string{"rules[0].expression:unknown_variable", "rules[1].reason_code:required", "rules[1].expression:syntax_error"},
		},
	}
	for name, c := range cases {
		resp := s.putRules("demo_analyst", v1, c.body)
		resp.AssertStatus(http.StatusUnprocessableEntity)
		code, _ := s.errorOf(resp)
		s.Equal("INPUT_VALIDATION_ERROR", code, name)
		s.ElementsMatch(c.want, s.issuePairs(resp), name)
	}
	s.Empty(s.getRules(v1)["rules"], "no invalid list stored anything")

	// More than a hundred rules.
	var many []string
	for i := 0; i < policydomain.MaxRules+1; i++ {
		many = append(many, `{}`)
	}
	resp := s.putRules("demo_analyst", v1, `{"rules":[`+strings.Join(many, ",")+`]}`)
	resp.AssertStatus(http.StatusUnprocessableEntity)
	s.Equal([]string{"rules:too_many"}, s.issuePairs(resp))
	for _, body := range []string{"not json", `["array"]`, `null`} {
		s.putRules("demo_analyst", v1, body).AssertStatus(http.StatusUnprocessableEntity)
	}
}

func (s *PolicyTestSuite) TestErrorsExplainExpressionProblems() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	resp := s.putRules("demo_analyst", v1,
		`{"rules":[{"rule_code":"RAC_A","reason_code":"REASON_A","action":"REJECT","expression":"agee_years >= 21"}]}`)
	resp.AssertStatus(http.StatusUnprocessableEntity)
	_, details := s.errorOf(resp)
	s.Require().Len(details, 1)
	s.Contains(details[0].(map[string]any)["message"], "agee_years")
}

func (s *PolicyTestSuite) TestExpressionsAreCheckedAgainstTheDictionaryTheDraftIsPinnedTo() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	// At dictionary schema version 1 only age_years exists.
	s.send("demo_analyst", "PATCH", versionsPath+"/"+v1, `{"dictionary_schema_version":1}`).AssertOk()
	resp := s.putRules("demo_analyst", v1, demoRulesBody)
	resp.AssertStatus(http.StatusUnprocessableEntity)
	s.ElementsMatch([]string{"rules[1].expression:unknown_variable", "rules[2].expression:unknown_variable"}, s.issuePairs(resp))

	s.send("demo_analyst", "PATCH", versionsPath+"/"+v1, `{"dictionary_schema_version":8}`).AssertOk()
	s.putRules("demo_analyst", v1, demoRulesBody).AssertOk()
}

func (s *PolicyTestSuite) TestTheTypesComeFromTheDictionary() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	// Dictionary variables: monthly_income and existing_installment are decimals, dsr_ratio is derived.
	ok := `{"rules":[
		{"rule_code":"RAC_INCOME","reason_code":"INCOME_TOO_LOW","action":"REJECT","expression":"monthly_income >= 3000000"},
		{"rule_code":"RAC_DSR","reason_code":"DSR_TOO_HIGH","action":"REFER","expression":"dsr_ratio <= 0.35"},
		{"rule_code":"RAC_NET","reason_code":"NET_INCOME_LOW","action":"REFER","expression":"monthly_income - existing_installment > 2000000 and not is_blacklisted"},
		{"rule_code":"RAC_KOL","reason_code":"KOL_REVIEW","action":"REFER","expression":"slik_max_kol in [1, 2]"}]}`
	s.putRules("demo_analyst", v1, ok).AssertOk()
}

// --- freezing ---------------------------------------------------------------------------

func (s *PolicyTestSuite) TestFrozenVersionRefusesRuleChangesButStillShowsThem() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	s.putRules("demo_analyst", v1, demoRulesBody).AssertOk()
	s.submit(v1).AssertOk()
	before := s.getRules(v1)

	for name, body := range map[string]string{
		"replace": `{"rules":[]}`, "same list": demoRulesBody, "invalid list": `{"rules":[{}]}`,
	} {
		resp := s.putRules("demo_analyst", v1, body)
		resp.AssertStatus(http.StatusConflict)
		code, _ := s.errorOf(resp)
		s.Equal("POLICY_VERSION_FROZEN", code, name)
	}
	after := s.getRules(v1)
	s.Equal("PENDING_APPROVAL", after["status"])
	s.Equal(before["rules"], after["rules"])
	s.Equal([]string{"RAC_MIN_AGE", "RAC_BLACKLIST", "RAC_SLIK"}, ruleCodes(after))
}

func (s *PolicyTestSuite) TestSubmitFreezesTheRulesIntoTheVerifiableSnapshot() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	s.putRules("demo_analyst", v1, demoRulesBody).AssertOk()
	body := s.json(s.submit(v1).AssertOk())

	snapshot := body["snapshot"].(map[string]any)
	rules := snapshot["rac_rules"].([]any)
	s.Require().Len(rules, 3)
	s.Equal(map[string]any{
		"order": float64(1), "rule_code": "RAC_MIN_AGE", "reason_code": "AGE_BELOW_MINIMUM",
		"action": "REJECT", "expression": "age_years >= 21", "description": "Adults only",
	}, rules[0])
	s.Equal("RAC_SLIK", rules[2].(map[string]any)["rule_code"])

	raw, err := json.Marshal(snapshot)
	s.Require().NoError(err)
	s.True(policydomain.VerifySnapshot(raw, body["checksum"].(string)))

	// The version without rules has another checksum.
	policyID, _ := s.createPolicy(`{"product_code":"KTA_DEMO","scope":"RETAIL"}`)
	detail := s.json(s.send("demo_auditor", "GET", policiesPath+"/"+policyID, "").AssertOk())
	emptyID := detail["versions"].([]any)[0].(map[string]any)["id"].(string)
	empty := s.json(s.submit(emptyID).AssertOk())
	s.NotEqual(body["checksum"], empty["checksum"])
	s.Equal([]any{}, empty["snapshot"].(map[string]any)["rac_rules"])
}

func (s *PolicyTestSuite) TestSubmitRefusesRulesThatNoLongerCompile() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	s.putRules("demo_analyst", v1, demoRulesBody).AssertOk()
	// Pointing the draft at an older dictionary removes variables its rules use. PATCH does
	// not re-check the rules; submit does.
	s.send("demo_analyst", "PATCH", versionsPath+"/"+v1, `{"dictionary_schema_version":1}`).AssertOk()

	resp := s.submit(v1)
	resp.AssertStatus(http.StatusUnprocessableEntity)
	code, _ := s.errorOf(resp)
	s.Equal("POLICY_VALIDATION_FAILED", code)
	s.ElementsMatch([]string{"rules[1].expression:unknown_variable", "rules[2].expression:unknown_variable"}, s.issuePairs(resp))
	v := s.getVersion(v1)
	s.Equal("DRAFT", v["status"], "a failed gate leaves the draft editable")
	s.Nil(v["checksum"])

	s.send("demo_analyst", "PATCH", versionsPath+"/"+v1, `{"dictionary_schema_version":8}`).AssertOk()
	s.putTestCases(v1, `{"test_cases":[]}`).AssertOk() // the filler case was written for dictionary version 1
	s.submit(v1).AssertOk()
}

// --- clone ------------------------------------------------------------------------------

func (s *PolicyTestSuite) TestCloneCopiesRulesAndTheCopyIsIndependent() {
	policyID, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	s.putRules("demo_analyst", v1, demoRulesBody).AssertOk()
	s.submit(v1).AssertOk()

	v2 := s.clone(policyID, `{}`)["id"].(string)
	copied := s.getRules(v2)
	s.Equal("DRAFT", copied["status"])
	s.Equal([]string{"RAC_MIN_AGE", "RAC_BLACKLIST", "RAC_SLIK"}, ruleCodes(copied))
	s.Equal(s.getRules(v1)["rules"], copied["rules"])

	// Revising the copy: tighten the age rule, drop the SLIK rule.
	s.putRules("demo_analyst", v2, `{"rules":[
		{"rule_code":"RAC_MIN_AGE","reason_code":"AGE_BELOW_MINIMUM","action":"REJECT","expression":"age_years >= 25"},
		{"rule_code":"RAC_BLACKLIST","reason_code":"BLACKLISTED","action":"REJECT","expression":"is_blacklisted == false"}]}`).AssertOk()
	s.Len(s.getRules(v1)["rules"], 3, "the frozen source is untouched")
	s.Len(s.getRules(v2)["rules"], 2)

	frozen1 := s.getVersion(v1)["checksum"]
	frozen2 := s.json(s.submit(v2).AssertOk())["checksum"]
	s.NotEqual(frozen1, frozen2)
}

// --- concurrency ------------------------------------------------------------------------

func (s *PolicyTestSuite) TestConcurrentReplacesLeaveExactlyOneWholeList() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	const workers = 8
	lists := make([][]policydomain.RuleInput, workers)
	for i := range lists {
		for j := 0; j < 3; j++ {
			lists[i] = append(lists[i], policydomain.RuleInput{
				RuleCode: fmt.Sprintf("RAC_W%d_R%d", i, j), ReasonCode: "REASON_X", Action: "REJECT",
				Expression: fmt.Sprintf("age_years >= %d", 18+i),
			})
		}
	}
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, err := s.service.ReplaceRules(context.Background(), v1, lists[i])
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		s.Require().NoError(err)
	}

	stored := s.getRules(v1)
	codes := ruleCodes(stored)
	s.Require().Len(codes, 3, "never a mixture of two lists")
	prefix := codes[0][:strings.LastIndex(codes[0], "_R")]
	for i, c := range codes {
		s.Equal(fmt.Sprintf("%s_R%d", prefix, i), c)
	}
}

func (s *PolicyTestSuite) TestSubmitRacingWithAReplaceNeverLeavesTheSnapshotAndTheRulesApart() {
	for round := 0; round < 6; round++ {
		s.Require().NoError(facades.DB().Statement("TRUNCATE TABLE decision.rule_traces, decision.runs, decision.requests, policy.active_policy_bindings, policy.test_cases, policy.decision_matrix_rows, policy.scorecard_bins, policy.scorecard_characteristics, policy.scorecards, policy.rac_rules, policy.policy_versions, policy.policy_packages"))
		_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
		s.putRules("demo_analyst", v1, `{"rules":[{"rule_code":"RAC_OLD","reason_code":"OLD_REASON","action":"REJECT","expression":"age_years >= 18"}]}`).AssertOk()
		s.complete(v1)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = s.service.Submit(context.Background(), "", v1)
		}()
		go func() {
			defer wg.Done()
			// Same reason code and action as RAC_OLD: since RF-013 submit runs the filler golden case, and
			// it must give the same result whichever of the two calls wins the race.
			_, _, _ = s.service.ReplaceRules(context.Background(), v1, []policydomain.RuleInput{
				{RuleCode: "RAC_NEW", ReasonCode: "OLD_REASON", Action: "REJECT", Expression: "age_years >= 30"}})
		}()
		wg.Wait()

		v := s.getVersion(v1)
		s.Equal("PENDING_APPROVAL", v["status"])
		var inSnapshot []string
		for _, r := range v["snapshot"].(map[string]any)["rac_rules"].([]any) {
			inSnapshot = append(inSnapshot, r.(map[string]any)["rule_code"].(string))
		}
		s.Equal(ruleCodes(s.getRules(v1)), inSnapshot, "round %d: the frozen snapshot and the stored rules are the same list", round)
		raw, _ := json.Marshal(v["snapshot"])
		s.True(policydomain.VerifySnapshot(raw, v["checksum"].(string)))
	}
}

// --- database guarantees ----------------------------------------------------------------

func (s *PolicyTestSuite) TestDatabaseFreezesRulesOfSubmittedVersions() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	s.putRules("demo_analyst", v1, demoRulesBody).AssertOk()
	s.submit(v1).AssertOk()
	for name, stmt := range map[string]string{
		"insert": `INSERT INTO policy.rac_rules (policy_version_id, rule_order, rule_code, reason_code, action, expression)
		           VALUES ($1::uuid, 9, 'RAC_LATE', 'LATE_REASON', 'REJECT', 'true')`,
		"update expression": `UPDATE policy.rac_rules SET expression = 'true' WHERE policy_version_id = $1::uuid`,
		"update action":     `UPDATE policy.rac_rules SET action = 'REFER' WHERE policy_version_id = $1::uuid`,
		"delete":            `DELETE FROM policy.rac_rules WHERE policy_version_id = $1::uuid`,
	} {
		s.Error(s.exec(stmt, v1), name)
	}
	s.Len(s.getRules(v1)["rules"], 3)
}

func (s *PolicyTestSuite) TestDatabaseAllowsRuleChangesOfADraftAndEnforcesConstraints() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	insert := func(order int, code, reason, action, expression string) error {
		return s.exec(`INSERT INTO policy.rac_rules (policy_version_id, rule_order, rule_code, reason_code, action, expression)
		               VALUES ($1::uuid, $2, $3, $4, $5, $6)`, v1, order, code, reason, action, expression)
	}
	s.NoError(insert(1, "RAC_ONE", "ONE_REASON", "REJECT", "true"), "a draft accepts rules")
	s.Error(insert(1, "RAC_TWO", "TWO_REASON", "REJECT", "true"), "order is unique within a version")
	s.Error(insert(2, "RAC_ONE", "TWO_REASON", "REJECT", "true"), "rule code is unique within a version")
	s.Error(insert(0, "RAC_TWO", "TWO_REASON", "REJECT", "true"), "order starts at 1")
	s.Error(insert(2, "rac_two", "TWO_REASON", "REJECT", "true"), "rule code format")
	s.Error(insert(2, "RAC_TWO", "two", "REJECT", "true"), "reason code format")
	s.Error(insert(2, "RAC_TWO", "TWO_REASON", "BLOCK", "true"), "action")
	s.Error(insert(2, "RAC_TWO", "TWO_REASON", "REJECT", "  "), "blank expression")
	s.Error(insert(2, "RAC_TWO", "TWO_REASON", "REJECT", "true\nand true"), "single-line expression")
	s.Error(insert(2, "RAC_TWO", "TWO_REASON", "REJECT", strings.Repeat("a", 501)), "expression length")
	s.Error(s.exec(`INSERT INTO policy.rac_rules (policy_version_id, rule_order, rule_code, reason_code, action, expression)
	                VALUES ($1::uuid, 2, 'RAC_TWO', 'TWO_REASON', 'REJECT', 'true')`, missingID), "a rule needs an existing version")
	s.NoError(s.exec(`UPDATE policy.rac_rules SET expression = 'false' WHERE policy_version_id = $1::uuid`, v1))
	s.NoError(s.exec(`DELETE FROM policy.rac_rules WHERE policy_version_id = $1::uuid`, v1))
}

// --- seeder -----------------------------------------------------------------------------

func (s *PolicyTestSuite) TestDemoSeederAddsTheExampleRulesOnceAndNeverOverwritesEdits() {
	s.Require().NoError(seeders.SeedDemoPolicy())
	s.Require().NoError(seeders.SeedDemoPolicy())
	list := s.json(s.send("demo_auditor", "GET", policiesPath+"?product_code=KTA_DEMO", "").AssertOk())["data"].([]any)
	s.Require().Len(list, 1)
	v1 := list[0].(map[string]any)["latest_version"].(map[string]any)["id"].(string)
	rules := s.getRules(v1)
	s.Equal([]string{"RAC_MIN_AGE", "RAC_BLACKLIST", "RAC_SLIK"}, ruleCodes(rules))

	// An analyst edits the age rule; seeding again must not put the example back.
	s.putRules("demo_analyst", v1, `{"rules":[{"rule_code":"RAC_MIN_AGE","reason_code":"AGE_BELOW_MINIMUM","action":"REJECT","expression":"age_years >= 25"}]}`).AssertOk()
	s.Require().NoError(seeders.SeedDemoPolicy())
	edited := s.getRules(v1)
	s.Equal([]string{"RAC_MIN_AGE"}, ruleCodes(edited))
	s.Equal("age_years >= 25", edited["rules"].([]any)[0].(map[string]any)["expression"])

	// The seeded rules satisfy the checker, whatever the dictionary is.
	var sorted []string
	for _, r := range seeders.DemoRacRules {
		sorted = append(sorted, r.RuleCode)
	}
	sort.Strings(sorted)
	s.Equal([]string{"RAC_BLACKLIST", "RAC_MIN_AGE", "RAC_SLIK"}, sorted)
	_, _, err := s.service.ReplaceRules(context.Background(), v1, seeders.DemoRacRules)
	s.Require().NoError(err)
}
