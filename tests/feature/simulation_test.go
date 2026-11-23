package feature

import (
	"fmt"
	"strings"

	"github.com/goravel/framework/contracts/http"

	"goravel/database/seeders"
)

// The policy simulator (RF-017, SYSTEM_DESIGN.md 12). It evaluates any version with the code of
// the Decision API and writes nothing.

func simulatePath(versionID string) string { return versionsPath + "/" + versionID + "/simulate" }

func simulateBody(changes ...string) string {
	applicant := seeders.DemoApplicant
	for i := 0; i+1 < len(changes); i += 2 {
		applicant = strings.Replace(applicant, changes[i], changes[i+1], 1)
	}
	return `{"applicant":` + applicant + `}`
}

func (s *PolicyTestSuite) simulate(user, versionID, body string) map[string]any {
	return s.json(s.send(user, "POST", simulatePath(versionID), body))
}

// activeIDs returns the package id and the active version id after s.activate().
func (s *PolicyTestSuite) activeIDs() (policyID, versionID string) {
	var packages, versions []string
	s.Require().NoError(selectStrings(&packages, `SELECT package_id::text FROM policy.active_policy_bindings`))
	s.Require().NoError(selectStrings(&versions, `SELECT policy_version_id::text FROM policy.active_policy_bindings`))
	s.Require().Len(packages, 1)
	return packages[0], versions[0]
}

func (s *PolicyTestSuite) TestSimulateAuthorization() {
	s.activate()
	_, active := s.activeIDs()
	for user, want := range map[string]int{
		"": http.StatusUnauthorized, "demo_admin": http.StatusForbidden, "demo_analyst": http.StatusOK,
		"demo_approver_1": http.StatusForbidden, "demo_auditor": http.StatusForbidden,
		"demo_loan_application": http.StatusForbidden,
	} {
		s.send(user, "POST", simulatePath(active), simulateBody()).AssertStatus(want)
	}
}

// A simulation of the active version gives the decision the Decision API gives, field for field
// (SYSTEM_DESIGN.md 12: the same evaluation core).
func (s *PolicyTestSuite) TestSimulationMatchesTheDecisionOfTheSameVersion() {
	s.activate()
	_, active := s.activeIDs()
	for name, changes := range map[string][]string{
		"approve":      nil,
		"hard reject":  {`"age_years":29`, `"age_years":19`},
		"refer":        {`"slik_max_kol":1`, `"slik_max_kol":3`},
		"blacklisted":  {`"is_blacklisted":false`, `"is_blacklisted":true`},
		"second range": {`"monthly_income":"12000000.00"`, `"monthly_income":"4000000.00"`},
	} {
		simulated := s.simulate("demo_analyst", active, simulateBody(changes...))
		decided := s.json(s.evaluate("demo_loan_application", evaluateBody(changes...)).AssertOk())
		for _, key := range []string{"final_decision", "risk_grade", "total_score", "reason_codes", "refer_flag", "derived_features",
			"rac_results", "rac_not_evaluated", "base_score", "score_breakdown", "matrix"} {
			s.Equal(decided[key], simulated[key], name+": "+key)
		}
	}
}

func (s *PolicyTestSuite) TestSimulationResponseHasNoDecisionIdentity() {
	s.activate()
	_, active := s.activeIDs()
	body := s.simulate("demo_analyst", active, simulateBody())
	s.Equal("SIMULATED", body["status"])
	s.Equal(false, body["stored"])
	for _, key := range []string{"decision_id", "created_at", "application_id", "product_code"} {
		s.NotContains(body, key)
	}
	version := body["policy_version"].(map[string]any)
	s.Equal(active, version["id"])
	s.Equal("ACTIVE", version["status"])
	s.Equal("KTA_DEMO", version["product_code"])
	s.Equal("DEFAULT", version["scope"])
	s.Len(version["checksum"], 64, "a frozen version shows its checksum")
	s.Equal("APPROVE", body["final_decision"])
	s.Equal("685.00", body["total_score"])
}

// Simulating a draft never changes the decisions made with the active policy, and writes no
// decision, key, trace, binding change, or version change.
func (s *PolicyTestSuite) TestSimulatingADraftWritesNothingAndLeavesTheActivePolicyAlone() {
	s.activate()
	policyID, active := s.activeIDs()
	draft := s.clone(policyID, `{}`)["id"].(string)
	// A stricter draft: applicants under 30 are rejected.
	s.putRules("demo_analyst", draft, strings.Replace(demoRulesBody, "age_years >= 21", "age_years >= 30", 1)).AssertOk()

	var before []string
	const stamp = `SELECT updated_at::text || status FROM policy.policy_versions WHERE id = $1`
	s.Require().NoError(selectStrings(&before, stamp, draft))
	counts := func() []int {
		return []int{s.rows("decision.requests"), s.rows("decision.runs"), s.rows("decision.rule_traces"),
			s.rows("policy.active_policy_bindings"), s.rows("policy.policy_versions")}
	}
	countsBefore := counts()

	body := s.simulate("demo_analyst", draft, simulateBody())
	s.Equal("REJECT", body["final_decision"], "the draft rules are what the simulation applies")
	s.Equal([]any{"AGE_BELOW_MINIMUM"}, body["reason_codes"])
	s.Equal("DRAFT", body["policy_version"].(map[string]any)["status"])
	s.Nil(body["policy_version"].(map[string]any)["checksum"], "a draft has no checksum yet")

	s.Equal(countsBefore, counts(), "no row was written")
	var after, bound []string
	s.Require().NoError(selectStrings(&after, stamp, draft))
	s.Equal(before, after, "the draft was not touched")
	s.Require().NoError(selectStrings(&bound, `SELECT policy_version_id::text FROM policy.active_policy_bindings`))
	s.Equal([]string{active}, bound, "the active binding did not move")

	// The production decision for the same applicant still uses the active version.
	decided := s.json(s.evaluate("demo_loan_application", evaluateBody()).AssertOk())
	s.Equal("APPROVE", decided["final_decision"])
	s.Equal(active, decided["policy_version"].(map[string]any)["id"])
}

// A repeated simulation of the same input gives the same result and stores nothing, with or
// without an Idempotency-Key.
func (s *PolicyTestSuite) TestSimulationIsRepeatableAndNeedsNoIdempotencyKey() {
	s.activate()
	_, active := s.activeIDs()
	first := s.simulate("demo_analyst", active, simulateBody())
	second := s.simulate("demo_analyst", active, simulateBody())
	s.Equal(first, second)
	s.Zero(s.rows("decision.requests"))
	s.Zero(s.rows("decision.runs"))
}

// Edits to the draft are visible at once, which is what a simulation is for.
func (s *PolicyTestSuite) TestSimulationSeesTheCurrentContentOfTheDraft() {
	v1 := s.demoDraft()
	s.putTestCases(v1, demoTestCasesBody()).AssertOk()
	s.Equal("APPROVE", s.simulate("demo_analyst", v1, simulateBody())["final_decision"])
	s.putRules("demo_analyst", v1, strings.Replace(demoRulesBody, "age_years >= 21", "age_years >= 30", 1)).AssertOk()
	s.Equal("REJECT", s.simulate("demo_analyst", v1, simulateBody())["final_decision"])
}

func (s *PolicyTestSuite) TestAPendingVersionCanBeSimulated() {
	v1 := s.demoDraft()
	s.putTestCases(v1, demoTestCasesBody()).AssertOk()
	s.submitRaw(v1).AssertOk()
	body := s.simulate("demo_analyst", v1, simulateBody())
	s.Equal("PENDING_APPROVAL", body["policy_version"].(map[string]any)["status"])
	s.Equal("APPROVE", body["final_decision"])
}

// An incomplete draft is a statement about the draft, not a decision about the applicant.
func (s *PolicyTestSuite) TestADraftThatIsNotCompleteCannotBeSimulatedYet() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	resp := s.send("demo_analyst", "POST", simulatePath(v1), simulateBody())
	resp.AssertStatus(http.StatusUnprocessableEntity)
	code, _ := s.errorOf(resp)
	s.Equal("POLICY_NOT_EVALUABLE", code)
	s.Contains(s.json(resp)["error"].(map[string]any)["message"], "scorecard")
	s.Zero(s.rows("decision.runs"))

	// Rules alone are not enough, the scorecard and matrix are needed to reach a decision.
	s.putRules("demo_analyst", v1, demoRulesBody).AssertOk()
	s.send("demo_analyst", "POST", simulatePath(v1), simulateBody()).AssertStatus(http.StatusUnprocessableEntity)
}

func (s *PolicyTestSuite) TestSimulateInputErrorsAreNeverARejection() {
	s.activate()
	_, active := s.activeIDs()
	tests := map[string]struct {
		changes []string
		want    string
	}{
		"null required":    {[]string{`"monthly_income":"12000000.00"`, `"monthly_income":null`}, "applicant.monthly_income:required"},
		"zero income":      {[]string{`"monthly_income":"12000000.00"`, `"monthly_income":"0.00"`}, "applicant.monthly_income:below_min_value"},
		"wrong type":       {[]string{`"age_years":29`, `"age_years":"29"`}, "applicant.age_years:invalid_type"},
		"number for money": {[]string{`"monthly_income":"12000000.00"`, `"monthly_income":12000000`}, "applicant.monthly_income:invalid_type"},
		"derived supplied": {[]string{`"age_years":29`, `"age_years":29,"dsr_ratio":"0.01"`}, "applicant.dsr_ratio:derived_not_allowed"},
		"unknown key":      {[]string{`"age_years":29`, `"age_years":29,"hair_colour":"red"`}, "applicant.hair_colour:unknown_variable"},
	}
	for name, tc := range tests {
		resp := s.send("demo_analyst", "POST", simulatePath(active), simulateBody(tc.changes...))
		resp.AssertStatus(http.StatusUnprocessableEntity)
		code, _ := s.errorOf(resp)
		s.Equal("INPUT_VALIDATION_ERROR", code, name)
		s.Contains(s.issuePairs(resp), tc.want, name)
	}
}

func (s *PolicyTestSuite) TestSimulateRejectsAMalformedRequest() {
	s.activate()
	_, active := s.activeIDs()
	tests := map[string]string{
		"not an object":   `[]`,
		"no applicant":    `{}`,
		"null applicant":  `{"applicant":null}`,
		"applicant shape": `{"applicant":[1]}`,
		"unknown key":     `{"applicant":` + seeders.DemoApplicant + `,"application_id":"X"}`,
	}
	for _, body := range tests {
		s.send("demo_analyst", "POST", simulatePath(active), body).AssertStatus(http.StatusUnprocessableEntity)
	}
	too := make([]string, 0, 101)
	for i := 0; i < 101; i++ {
		too = append(too, fmt.Sprintf(`"field_%03d":1`, i))
	}
	s.send("demo_analyst", "POST", simulatePath(active), `{"applicant":{`+strings.Join(too, ",")+`}}`).AssertStatus(http.StatusUnprocessableEntity)
	s.Zero(s.rows("decision.runs"))
}

func (s *PolicyTestSuite) TestSimulateUnknownVersionIsNotFound() {
	for _, id := range []string{missingID, "not-a-uuid"} {
		resp := s.send("demo_analyst", "POST", simulatePath(id), simulateBody())
		resp.AssertNotFound()
		code, _ := s.errorOf(resp)
		s.Equal("POLICY_VERSION_NOT_FOUND", code)
	}
}

// The simulator does not read or fill the policy cache of the Decision API: a simulation of a
// version that was never decided with leaves the decision tables and the cache path untouched.
func (s *PolicyTestSuite) TestSimulationDoesNotTouchTheDecisionSchema() {
	s.activate()
	policyID, _ := s.activeIDs()
	draft := s.clone(policyID, `{}`)["id"].(string)
	s.send("demo_analyst", "POST", simulatePath(draft), simulateBody()).AssertOk()
	for _, table := range []string{"decision.requests", "decision.runs", "decision.rule_traces"} {
		s.Zero(s.rows(table), table)
	}
	var bound []int
	s.Require().NoError(selectInts(&bound, `SELECT COUNT(*) FROM policy.active_policy_bindings WHERE policy_version_id = $1`, draft))
	s.Equal([]int{0}, bound)
}
