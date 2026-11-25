package feature

import (
	"strings"

	"github.com/goravel/framework/contracts/http"
)

// The ten minimum golden cases of SYSTEM_DESIGN.md section 12, run through HTTP on the seeded
// KTA_DEMO policy (RF-024). They are regression tests of the whole path: request, dictionary
// validation, feature builder, RAC, scorecard, matrix, precedence, storage, and response. Each test
// is named after its case. The finer cases (every operator, every bin) live in the unit tests of
// the domain packages; this file only guards that the behaviours the design promises still hold
// end to end.

// goldenBoundaryScorecard maps one applicant age to one exact score, so a decision can be made to
// land exactly on, and just below, every edge of the matrix of SYSTEM_DESIGN.md 20.3
// ([0,500) D, [500,650) B, [650,901) A). Ages 20 to 24 give 499.99, 500.00, 649.99, 650.00, 900.00.
const goldenBoundaryScorecard = `{"base_score":"0.00","characteristics":[
	{"characteristic_code":"age_to_score","variable":"age_years","bins":[
		{"max_value":"20","points":"0.00","label":"lowest score"},
		{"min_value":"20","max_value":"21","points":"499.99","label":"just below B"},
		{"min_value":"21","max_value":"22","points":"500.00","label":"first B"},
		{"min_value":"22","max_value":"23","points":"649.99","label":"just below A"},
		{"min_value":"23","max_value":"24","points":"650.00","label":"first A"},
		{"min_value":"24","points":"900.00","label":"highest score"}]}]}`

const goldenBlacklistOnly = `{"rules":[{"rule_code":"RAC_BLACKLIST","reason_code":"BLACKLISTED","action":"REJECT","expression":"is_blacklisted == false"}]}`

// outcomeFields is a decision without its identity, to compare two evaluations of one input.
func outcomeFields(body map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range body {
		if k == "decision_id" || k == "created_at" {
			continue
		}
		out[k] = v
	}
	return out
}

// Case 1: the happy path is APPROVE, grade A, score 685.00, with a full trace.
func (s *PolicyTestSuite) TestGolden01HappyPathIsApproved() {
	s.activate()
	body := s.json(s.evaluate("demo_loan_application", evaluateBody()).AssertOk())
	s.Equal("APPROVE", body["final_decision"])
	s.Equal("A", body["risk_grade"])
	s.Equal("685.00", body["total_score"])
	s.Equal([]any{}, body["reason_codes"])
	s.Len(body["rac_results"], 3)
	s.Len(body["score_breakdown"], 3)
}

// Case 2: age below the RAC threshold is a hard reject; the threshold itself passes.
func (s *PolicyTestSuite) TestGolden02AgeBelowTheThresholdIsAHardReject() {
	s.activate()
	below := s.json(s.evaluate("demo_loan_application", evaluateBody(`"age_years":29`, `"age_years":20`)).AssertOk())
	s.Equal("REJECT", below["final_decision"])
	s.Equal([]any{"AGE_BELOW_MINIMUM"}, below["reason_codes"])
	s.Nil(below["total_score"], "a hard reject stops before scoring")
	at := s.json(s.evaluate("demo_loan_application", evaluateBody(`"age_years":29`, `"age_years":21`)).AssertOk())
	s.NotContains(at["reason_codes"], "AGE_BELOW_MINIMUM", "21 meets age_years >= 21")
	s.Equal("640.00", at["total_score"], "350 + 40 + 150 + 100")
}

// Case 3: a blacklisted applicant is a hard reject whatever the score would be.
func (s *PolicyTestSuite) TestGolden03BlacklistedIsAHardReject() {
	s.activate()
	body := s.json(s.evaluate("demo_loan_application", evaluateBody(`"is_blacklisted":false`, `"is_blacklisted":true`)).AssertOk())
	s.Equal("REJECT", body["final_decision"])
	s.Equal([]any{"BLACKLISTED"}, body["reason_codes"])
}

// Case 4: a soft RAC failure is REFER even though the score is high enough to approve.
func (s *PolicyTestSuite) TestGolden04SoftRACFailIsReferDespiteAHighScore() {
	s.activate()
	body := s.json(s.evaluate("demo_loan_application", evaluateBody(`"slik_max_kol":1`, `"slik_max_kol":3`)).AssertOk())
	s.Equal("REFER", body["final_decision"])
	s.Equal("A", body["risk_grade"], "the matrix alone would approve")
	s.Equal(true, body["refer_flag"])
	s.Equal([]any{"SLIK_REVIEW_REQUIRED"}, body["reason_codes"])
	ok := s.json(s.evaluate("demo_loan_application", evaluateBody(`"slik_max_kol":1`, `"slik_max_kol":2`)).AssertOk())
	s.Equal("APPROVE", ok["final_decision"], "kol 2 still passes the rule")
}

// Case 5: a total score exactly on a matrix boundary belongs to the row that starts there, and
// one cent below belongs to the row before it. The demo scorecard cannot reach the edges, so the
// test simulates a draft whose scorecard maps one age to each exact score.
func (s *PolicyTestSuite) TestGolden05ScoresOnEveryMatrixBoundary() {
	s.activate()
	policyID, _ := s.activeIDs()
	draft := s.clone(policyID, `{}`)["id"].(string)
	s.putRules("demo_analyst", draft, goldenBlacklistOnly).AssertOk()
	s.putScorecard(draft, goldenBoundaryScorecard).AssertOk()
	s.putMatrix(draft, demoMatrixBody).AssertOk()
	cases := []struct {
		age, score, grade, decision string
	}{
		{"19", "0.00", "D", "REJECT"},
		{"20", "499.99", "D", "REJECT"},
		{"21", "500.00", "B", "REFER"},
		{"22", "649.99", "B", "REFER"},
		{"23", "650.00", "A", "APPROVE"},
		{"24", "900.00", "A", "APPROVE"},
	}
	for _, c := range cases {
		body := s.simulate("demo_analyst", draft, simulateBody(`"age_years":29`, `"age_years":`+c.age))
		s.Equal(c.score, body["total_score"], "age %s", c.age)
		s.Equal(c.grade, body["risk_grade"], "score %s", c.score)
		s.Equal(c.decision, body["final_decision"], "score %s", c.score)
		s.Equal(c.decision, body["matrix"].(map[string]any)["decision"])
	}
}

// Case 6: a null, a missing, or a wrongly typed input is a validation error (422), never a
// REJECT, and stores nothing.
func (s *PolicyTestSuite) TestGolden06InvalidInputIsAValidationErrorNotAReject() {
	s.activate()
	runs := s.rows("decision.runs")
	for name, change := range map[string][2]string{
		"null income":       {`"monthly_income":"12000000.00"`, `"monthly_income":null`},
		"missing slik":      {`"slik_max_kol":1,`, ``},
		"age as string":     {`"age_years":29`, `"age_years":"29"`},
		"income as number":  {`"monthly_income":"12000000.00"`, `"monthly_income":12000000`},
		"blacklist as text": {`"is_blacklisted":false`, `"is_blacklisted":"no"`},
	} {
		resp := s.evaluate("demo_loan_application", evaluateBody(change[0], change[1]))
		resp.AssertStatus(http.StatusUnprocessableEntity)
		code, details := s.errorOf(resp)
		s.Equal("INPUT_VALIDATION_ERROR", code, name)
		s.NotEmpty(details, name)
		s.NotContains(s.json(resp), "final_decision", name)
	}
	s.Equal(runs, s.rows("decision.runs"), "no invalid request stored a decision")
}

// Case 7: monthly_income = 0 is a validation error, not a division by zero (no 500, no REJECT).
func (s *PolicyTestSuite) TestGolden07ZeroIncomeIsAValidationErrorNotADivisionByZero() {
	s.activate()
	for _, zero := range []string{`"0"`, `"0.00"`} {
		resp := s.evaluate("demo_loan_application", evaluateBody(`"monthly_income":"12000000.00"`, `"monthly_income":`+zero))
		resp.AssertStatus(http.StatusUnprocessableEntity)
		code, details := s.errorOf(resp)
		s.Equal("INPUT_VALIDATION_ERROR", code)
		s.Contains(s.issuePairs(resp), "applicant.monthly_income:below_min_value", "%v", details)
	}
	sim := s.send("demo_analyst", "POST", simulatePath(s.activeVersion()), simulateBody(`"monthly_income":"12000000.00"`, `"monthly_income":"0.00"`))
	sim.AssertStatus(http.StatusUnprocessableEntity)
}

func (s *PolicyTestSuite) activeVersion() string {
	_, v := s.activeIDs()
	return v
}

// Case 8: overlapping score bins and a matrix gap make the policy fail validation, and the
// refused body changes nothing.
func (s *PolicyTestSuite) TestGolden08BinOverlapAndMatrixGapFailValidation() {
	s.activate()
	policyID, _ := s.activeIDs()
	draft := s.clone(policyID, `{}`)["id"].(string)
	before := s.getScorecard(draft)

	overlap := `{"base_score":"350.00","characteristics":[{"characteristic_code":"age_group","variable":"age_years","bins":[
		{"max_value":"30","points":"40.00"},{"min_value":"25","points":"85.00"}]}]}`
	resp := s.putScorecard(draft, overlap)
	resp.AssertStatus(http.StatusUnprocessableEntity)
	s.Contains(s.issuePairs(resp), "characteristics[0].bins[1].min_value:overlap")
	s.Equal(before, s.getScorecard(draft), "the scorecard is unchanged")

	gap := `{"rows":[{"min_score":"0","max_score":"500","grade":"D","decision":"REJECT"},
		{"min_score":"600","max_score":"901","grade":"A","decision":"APPROVE"}]}`
	resp = s.putMatrix(draft, gap)
	resp.AssertStatus(http.StatusUnprocessableEntity)
	s.Contains(s.issuePairs(resp), "rows[1].min_score:gap")

	// The draft itself stays valid and can still be submitted.
	s.complete(draft)
	s.json(s.send("demo_analyst", "POST", versionsPath+"/"+draft+"/validate", "").AssertOk())
}

// Case 9: simulating a draft changes neither the stored decisions nor the active version.
func (s *PolicyTestSuite) TestGolden09SimulatingADraftLeavesProductionAlone() {
	s.activate()
	policyID, active := s.activeIDs()
	draft := s.clone(policyID, `{}`)["id"].(string)
	s.putRules("demo_analyst", draft, strings.Replace(demoRulesBody, "age_years >= 21", "age_years >= 40", 1)).AssertOk()
	runs := s.rows("decision.runs")
	sim := s.simulate("demo_analyst", draft, simulateBody())
	s.Equal("REJECT", sim["final_decision"])
	s.Equal(runs, s.rows("decision.runs"))
	prod := s.json(s.evaluate("demo_loan_application", evaluateBody()).AssertOk())
	s.Equal("APPROVE", prod["final_decision"])
	s.Equal(active, prod["policy_version"].(map[string]any)["id"])
}

// Case 10: evaluating the same input against the same policy snapshot always gives the same
// output, and a stored decision reads back exactly as it was returned.
func (s *PolicyTestSuite) TestGolden10ReEvaluationIsDeterministic() {
	s.activate()
	first := s.json(s.evaluate("demo_loan_application", evaluateBody()).AssertOk())
	for i := 0; i < 3; i++ {
		again := s.json(s.evaluate("demo_loan_application", evaluateBody()).AssertOk())
		s.Equal(outcomeFields(first), outcomeFields(again), "evaluation %d", i+2)
	}
	stored := s.json(s.getDecision("demo_auditor", first["decision_id"].(string)).AssertOk())
	s.Equal(first, stored, "a stored decision reads back as returned")
	sim := s.simulate("demo_analyst", s.activeVersion(), simulateBody())
	s.Equal(first["final_decision"], sim["final_decision"])
	s.Equal(first["total_score"], sim["total_score"])
	s.Equal(first["score_breakdown"], sim["score_breakdown"])
	s.Equal(first["rac_results"], sim["rac_results"])
}
