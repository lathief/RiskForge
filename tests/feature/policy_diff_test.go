package feature

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/lathief/RiskForge/database/seeders"
)

// Policy diff and approval review (RF-021, SYSTEM_DESIGN.md 11.4).

func diffPath(versionID string) string { return versionsPath + "/" + versionID + "/diff" }

// age29Case is one golden case for a policy whose minimum age is 30: the example applicant
// (29) is rejected.
var age29Case = `{"test_cases":[{"case_code":"GOLDEN_AGE_29_REJECTED","input":` + seeders.DemoApplicant +
	`,"expected":{"outcome":"DECISION","decision":"REJECT","reason_codes":["AGE_BELOW_MINIMUM"]}}]}`

// reviewedDraft activates the demo policy and submits a clone whose minimum age is 30.
func (s *PolicyTestSuite) reviewedDraft() (v1, v2, requestID string) {
	s.activate()
	policyID, v1 := s.activeIDs()
	v2 = s.stricterDraft(policyID, "", 30)
	s.putTestCases(v2, age29Case).AssertOk()
	return v1, v2, s.submitted(v2)
}

func (s *PolicyTestSuite) TestTheReviewComparesTheSubmissionWithTheActiveVersion() {
	v1, v2, id := s.reviewedDraft()
	runs, actions := s.rows("decision.runs"), s.rows("approval.actions")

	body := s.json(s.send("demo_approver_1", "GET", approvalRequestsPath+"/"+id+"/review", "").AssertOk())
	s.Equal(id, body["approval_request"].(map[string]any)["id"])
	c := body["comparison"].(map[string]any)
	s.Equal(v2, c["target"].(map[string]any)["id"])
	s.Equal("PENDING_APPROVAL", c["target"].(map[string]any)["status"])
	base := c["base"].(map[string]any)
	s.Equal(v1, base["id"])
	s.Equal("ACTIVE", base["status"])
	s.Equal("ACTIVE_VERSION", c["base_reason"])
	s.Equal(true, c["changed"])

	summary := c["summary"].(map[string]any)
	// The test rule body also has other descriptions than the seeded rules, so all three differ.
	s.Equal(map[string]any{"added": float64(0), "removed": float64(0), "modified": float64(3)}, summary["rac_rules"])
	s.Equal(map[string]any{"added": float64(1), "removed": float64(6), "modified": float64(0)}, summary["test_cases"])
	s.Equal(false, summary["base_score_changed"])
	s.Contains(c["change_summary"], "RAC rule RAC_MIN_AGE: expression `age_years >= 21` -> `age_years >= 30`")
	s.Contains(c["change_summary"], "Test case GOLDEN_AGE_29_REJECTED added")
	rule := c["changes"].(map[string]any)["rac_rules"].([]any)[0].(map[string]any)
	s.Equal("RAC_MIN_AGE", rule["key"])
	s.Equal("MODIFIED", rule["change"])
	s.Contains(rule["fields"], map[string]any{"field": "expression", "before": "age_years >= 21", "after": "age_years >= 30"})

	validation := c["validation"].(map[string]any)
	s.Equal(true, validation["valid"])
	golden := validation["golden_tests"].(map[string]any)
	s.Equal(float64(1), golden["executed"])
	s.Equal(float64(1), golden["passed"])

	impact := c["impact"].(map[string]any)
	s.Equal(true, impact["available"])
	s.Equal(true, impact["base_evaluated"])
	s.Equal(float64(1), impact["cases"])
	s.Equal(float64(1), impact["changed"])
	result := impact["results"].([]any)[0].(map[string]any)
	s.Equal("APPROVE", result["base"].(map[string]any)["decision"], "the active version approves the example applicant")
	s.Equal("REJECT", result["target"].(map[string]any)["decision"])
	s.Equal(true, result["changed"])

	s.Equal(runs, s.rows("decision.runs"), "the review writes no decision")
	s.Equal(actions, s.rows("approval.actions"))
	s.Equal([]string{v1}, s.bound(), "and publishes nothing")
}

func (s *PolicyTestSuite) TestReviewAndDiffPermissions() {
	_, v2, id := s.reviewedDraft()
	for user, want := range map[string]int{
		"": http.StatusUnauthorized, "demo_admin": http.StatusForbidden, "demo_analyst": http.StatusOK,
		"demo_approver_1": http.StatusOK, "demo_auditor": http.StatusOK, "demo_loan_application": http.StatusForbidden,
	} {
		s.send(user, "GET", approvalRequestsPath+"/"+id+"/review", "").AssertStatus(want)
		s.send(user, "GET", diffPath(v2), "").AssertStatus(want)
	}
}

func (s *PolicyTestSuite) TestTheDiffOfAnyVersionAndItsBase() {
	v1, v2, _ := s.reviewedDraft()
	// Against the active version by default, the same comparison as the review.
	c := s.json(s.send("demo_analyst", "GET", diffPath(v2), "").AssertOk())
	s.Equal("ACTIVE_VERSION", c["base_reason"])
	// Against a named version of the same policy.
	c = s.json(s.send("demo_analyst", "GET", diffPath(v1)+"?base_version_id="+v2, "").AssertOk())
	s.Equal("REQUESTED", c["base_reason"])
	s.Contains(c["change_summary"], "RAC rule RAC_MIN_AGE: expression `age_years >= 30` -> `age_years >= 21`")
	// The active version itself has nothing to compare against.
	c = s.json(s.send("demo_analyst", "GET", diffPath(v1), "").AssertOk())
	s.Equal("NONE", c["base_reason"])
	s.Nil(c["base"])
	s.Equal(map[string]any{"added": float64(3), "removed": float64(0), "modified": float64(0)}, c["summary"].(map[string]any)["rac_rules"])

	// A clone without edits has no diff.
	policyID, _ := s.activeIDs()
	clone := s.clone(policyID, `{}`)["id"].(string)
	c = s.json(s.send("demo_analyst", "GET", diffPath(clone), "").AssertOk())
	s.Equal(false, c["changed"])
	s.Equal([]any{}, c["change_summary"])
}

func (s *PolicyTestSuite) TestAnIncompleteDraftHasADiffButNoImpact() {
	s.activate()
	policyID, _ := s.activeIDs()
	clone := s.clone(policyID, `{}`)["id"].(string)
	s.scorecardRequest("demo_analyst", "DELETE", clone, "").AssertNoContent()
	c := s.json(s.send("demo_analyst", "GET", diffPath(clone), "").AssertOk())
	s.Equal(true, c["summary"].(map[string]any)["base_score_changed"])
	s.Equal(map[string]any{"added": float64(0), "removed": float64(3), "modified": float64(0)}, c["summary"].(map[string]any)["scorecard_characteristics"])
	impact := c["impact"].(map[string]any)
	s.Equal(false, impact["available"])
	s.Contains(impact["reason"], "scorecard")
	s.Equal(false, c["validation"].(map[string]any)["valid"])
}

func (s *PolicyTestSuite) TestDiffAndReviewErrors() {
	v1, _, _ := s.reviewedDraft()
	other, _ := s.createPolicy(`{"product_code":"KTA_DEMO","scope":"PAYROLL"}`)
	otherVersion := s.json(s.send("demo_auditor", "GET", policiesPath+"/"+other, "").AssertOk())["versions"].([]any)[0].(map[string]any)["id"].(string)
	for name, base := range map[string]string{"other policy": otherVersion, "unknown": missingID, "malformed": "x"} {
		resp := s.send("demo_analyst", "GET", diffPath(v1)+"?base_version_id="+base, "")
		resp.AssertStatus(http.StatusUnprocessableEntity)
		s.Equal([]string{"base_version_id:not_in_policy"}, s.issuePairs(resp), name)
	}
	for _, bad := range []string{missingID, "not-a-uuid"} {
		resp := s.send("demo_analyst", "GET", diffPath(bad), "")
		resp.AssertNotFound()
		code, _ := s.errorOf(resp)
		s.Equal("POLICY_VERSION_NOT_FOUND", code)
		resp = s.send("demo_approver_1", "GET", approvalRequestsPath+"/"+bad+"/review", "")
		resp.AssertNotFound()
		code, _ = s.errorOf(resp)
		s.Equal("APPROVAL_REQUEST_NOT_FOUND", code)
	}
}
