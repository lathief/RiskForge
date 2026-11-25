package feature

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/goravel/framework/contracts/http"
	contractstesting "github.com/goravel/framework/contracts/testing/http"

	"github.com/lathief/RiskForge/app/facades"
	decisionapp "github.com/lathief/RiskForge/app/modules/decision/application"
	decisioninfra "github.com/lathief/RiskForge/app/modules/decision/infrastructure"
	"github.com/lathief/RiskForge/app/platform/database"
	"github.com/lathief/RiskForge/database/seeders"
)

// The Decision API (RF-014, RF-015). The policy is the seeded demo policy, activated the way
// the development seeder does it; real activation arrives with the approval workflow (M4).

const evaluatePath = "/api/v1/decisions/evaluate"

func evaluateBody(changes ...string) string {
	applicant := seeders.DemoApplicant
	for i := 0; i+1 < len(changes); i += 2 {
		applicant = strings.Replace(applicant, changes[i], changes[i+1], 1)
	}
	return `{"application_id":"LOAN-APP-DEMO-0001","product_code":"KTA_DEMO","applicant":` + applicant + `}`
}

var keySeq atomic.Int64

// nextKey gives every call its own idempotency key, so tests that are not about idempotency
// get independent decisions.
func nextKey() string { return fmt.Sprintf("test-key-%06d", keySeq.Add(1)) }

func (s *PolicyTestSuite) evaluateKey(user, key, body string) contractstesting.Response {
	req := s.Http(s.T()).WithHeader("Content-Type", "application/json")
	if key != "" {
		req = req.WithHeader("Idempotency-Key", key)
	}
	if user != "" {
		req = req.WithToken(s.tokens[user])
	}
	resp, err := req.Post(evaluatePath, strings.NewReader(body))
	s.Require().NoError(err)
	return resp
}

func (s *PolicyTestSuite) evaluate(user, body string) contractstesting.Response {
	return s.evaluateKey(user, nextKey(), body)
}

func (s *PolicyTestSuite) getDecision(user, id string) contractstesting.Response {
	return s.send(user, "GET", "/api/v1/decisions/"+id, "")
}

// activate seeds the demo policy and makes it the active one.
func (s *PolicyTestSuite) activate() {
	s.Require().NoError(seeders.SeedDemoPolicy())
	s.Require().NoError(seeders.SeedDemoActivePolicy())
}

func (s *PolicyTestSuite) rows(table string) int {
	var n []int
	s.Require().NoError(selectInts(&n, `SELECT COUNT(*) FROM `+table))
	s.Require().Len(n, 1)
	return n[0]
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
	s.Equal(1, s.rows("decision.runs"), "only the allowed call stored a decision")
}

func (s *PolicyTestSuite) TestEvaluateApprovesTheExampleApplicantWithAFullTrace() {
	s.activate()
	resp := s.evaluate("demo_loan_application", evaluateBody())
	resp.AssertOk()
	body := s.json(resp)

	s.Regexp(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`, body["decision_id"])
	s.Regexp(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`, body["created_at"])
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
	s.Equal("9500000.00", features[1].(map[string]any)["value"])

	rac := body["rac_results"].([]any)
	s.Require().Len(rac, 3)
	first := rac[0].(map[string]any)
	s.Equal("RAC_MIN_AGE", first["rule_code"])
	s.Equal(true, first["passed"])
	s.Equal([]any{map[string]any{"variable": "age_years", "value": "29"}}, first["inputs"])
	s.Equal([]any{}, body["rac_not_evaluated"])

	score := body["score_breakdown"].([]any)
	s.Require().Len(score, 3)
	age := score[0].(map[string]any)
	s.Equal("85.00", age["points"])
	s.Equal(float64(2), age["bin_order"])
	s.Equal("150.00", score[1].(map[string]any)["points"])
	s.Equal("100.00", score[2].(map[string]any)["points"])
	s.Equal("APPROVE", body["matrix"].(map[string]any)["decision"])
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
	s.Zero(s.rows("decision.runs"), "an invalid request stores nothing")
	s.Zero(s.rows("decision.requests"), "and does not use up its key")
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

func (s *PolicyTestSuite) TestEvaluateNeedsAValidIdempotencyKey() {
	s.activate()
	resp := s.evaluateKey("demo_loan_application", "", evaluateBody())
	resp.AssertStatus(http.StatusUnprocessableEntity)
	s.Equal([]string{"Idempotency-Key:required"}, s.issuePairs(resp))
	for _, bad := range []string{"has space", "-leading", strings.Repeat("k", 129)} {
		resp = s.evaluateKey("demo_loan_application", bad, evaluateBody())
		resp.AssertStatus(http.StatusUnprocessableEntity)
		s.Equal([]string{"Idempotency-Key:invalid_format"}, s.issuePairs(resp), bad)
	}
	s.Zero(s.rows("decision.runs"))
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
	s.Zero(s.rows("decision.requests"), "failed requests leave no trace and no used key")
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

// --- persistence and idempotency (RF-015) ------------------------------------------------

func (s *PolicyTestSuite) TestADecisionIsStoredWithItsTraceAndTheVersionThatMadeIt() {
	s.activate()
	body := s.json(s.evaluateKey("demo_loan_application", "store-1", evaluateBody()).AssertOk())
	id := body["decision_id"].(string)
	version := body["policy_version"].(map[string]any)

	s.Equal(1, s.rows("decision.requests"))
	s.Equal(1, s.rows("decision.runs"))
	s.Equal(3, s.rows("decision.rule_traces"), "one row per evaluated RAC rule")

	var run []string
	s.Require().NoError(selectStrings(&run, `SELECT application_id || '|' || product_code || '|' || policy_version_id::text || '|' || policy_checksum || '|' || final_decision || '|' || total_score::text || '|' || status
		FROM decision.runs WHERE id = $1::uuid`, id))
	s.Equal([]string{"LOAN-APP-DEMO-0001|KTA_DEMO|" + version["id"].(string) + "|" + version["checksum"].(string) + "|APPROVE|685.00|COMPLETED"}, run)

	var input []string
	s.Require().NoError(selectStrings(&input, `SELECT input_snapshot->>'monthly_income' FROM decision.runs WHERE id = $1::uuid`, id))
	s.Equal([]string{"12000000.00"}, input, "the normalized input is kept exactly")

	var traces []string
	s.Require().NoError(selectStrings(&traces, `SELECT rule_code || ':' || passed::text FROM decision.rule_traces WHERE run_id = $1::uuid ORDER BY trace_order`, id))
	s.Equal([]string{"RAC_MIN_AGE:true", "RAC_BLACKLIST:true", "RAC_SLIK:true"}, traces)

	// A hard reject has no grade and no score in the table either.
	rejected := s.json(s.evaluate("demo_loan_application", evaluateBody(`"age_years":29`, `"age_years":19`)).AssertOk())
	var nulls []int
	s.Require().NoError(selectInts(&nulls, `SELECT COUNT(*) FROM decision.runs WHERE id = $1::uuid AND risk_grade IS NULL AND total_score IS NULL`, rejected["decision_id"].(string)))
	s.Equal([]int{1}, nulls)
}

func (s *PolicyTestSuite) TestTheSameKeyAndRequestReplaysTheStoredDecision() {
	s.activate()
	first := s.evaluateKey("demo_loan_application", "replay-1", evaluateBody())
	first.AssertOk()
	s.Empty(first.Headers().Get("Idempotent-Replayed"))
	firstBody := s.json(first)

	second := s.evaluateKey("demo_loan_application", "replay-1", evaluateBody())
	second.AssertOk()
	s.Equal("true", second.Headers().Get("Idempotent-Replayed"))
	s.Equal(firstBody, s.json(second), "the same decision, with the same id and time")
	s.Equal(1, s.rows("decision.runs"), "never two runs for one key")

	// The key order of the JSON does not matter: it is the same request.
	reordered := `{"product_code":"KTA_DEMO","applicant":{"is_blacklisted":false,"slik_max_kol":1,"employment_tenure_months":36,"existing_installment":"2500000.00","monthly_income":"12000000.00","age_years":29},"application_id":"LOAN-APP-DEMO-0001"}`
	third := s.evaluateKey("demo_loan_application", "replay-1", reordered)
	third.AssertOk()
	s.Equal(firstBody["decision_id"], s.json(third)["decision_id"])
	s.Equal(1, s.rows("decision.runs"))
}

func (s *PolicyTestSuite) TestTheSameKeyWithADifferentRequestIsAConflict() {
	s.activate()
	s.evaluateKey("demo_loan_application", "conflict-1", evaluateBody()).AssertOk()
	for name, body := range map[string]string{
		"other value":       evaluateBody(`"age_years":29`, `"age_years":30`),
		"other application": strings.Replace(evaluateBody(), "LOAN-APP-DEMO-0001", "LOAN-APP-DEMO-0002", 1),
	} {
		resp := s.evaluateKey("demo_loan_application", "conflict-1", body)
		resp.AssertStatus(http.StatusConflict)
		code, _ := s.errorOf(resp)
		s.Equal("IDEMPOTENCY_CONFLICT", code, name)
	}
	s.Equal(1, s.rows("decision.runs"))
}

func (s *PolicyTestSuite) TestAFailedRequestDoesNotUseUpItsKey() {
	s.activate()
	bad := evaluateBody(`"monthly_income":"12000000.00"`, `"monthly_income":"0.00"`)
	s.evaluateKey("demo_loan_application", "retry-1", bad).AssertStatus(http.StatusUnprocessableEntity)
	s.Zero(s.rows("decision.requests"))

	resp := s.evaluateKey("demo_loan_application", "retry-1", evaluateBody())
	resp.AssertOk()
	s.Empty(resp.Headers().Get("Idempotent-Replayed"), "the corrected request is evaluated, not replayed")
	s.Equal(1, s.rows("decision.runs"))
}

func (s *PolicyTestSuite) TestAReplayDoesNotNeedTheActivePolicyAnymore() {
	s.activate()
	first := s.json(s.evaluateKey("demo_loan_application", "gone-1", evaluateBody()).AssertOk())
	s.Require().NoError(s.exec(`DELETE FROM policy.active_policy_bindings`))

	// A new key finds no active policy, but the stored decision is still returned for the old key.
	s.evaluateKey("demo_loan_application", "gone-2", evaluateBody()).AssertStatus(http.StatusConflict)
	replay := s.evaluateKey("demo_loan_application", "gone-1", evaluateBody())
	replay.AssertOk()
	s.Equal(first, s.json(replay))
}

func (s *PolicyTestSuite) TestGetReturnsTheStoredDecision() {
	s.activate()
	made := s.json(s.evaluateKey("demo_loan_application", "get-1", evaluateBody()).AssertOk())
	id := made["decision_id"].(string)

	for _, user := range []string{"demo_loan_application", "demo_auditor"} {
		resp := s.getDecision(user, id)
		resp.AssertOk()
		s.Equal(made, s.json(resp), user+" sees exactly what was returned when the decision was made")
	}
	for _, user := range []string{"", "demo_admin", "demo_analyst", "demo_approver_1"} {
		want := http.StatusForbidden
		if user == "" {
			want = http.StatusUnauthorized
		}
		s.getDecision(user, id).AssertStatus(want)
	}

	resp := s.getDecision("demo_auditor", "99999999-9999-4999-8999-999999999999")
	resp.AssertNotFound()
	code, _ := s.errorOf(resp)
	s.Equal("DECISION_NOT_FOUND", code)
	s.getDecision("demo_auditor", "not-a-uuid").AssertNotFound()
}

func (s *PolicyTestSuite) TestStoredDecisionsCannotBeChangedOrRemoved() {
	s.activate()
	made := s.json(s.evaluateKey("demo_loan_application", "immutable-1", evaluateBody()).AssertOk())
	id := made["decision_id"].(string)
	for name, stmt := range map[string]string{
		"change the decision": `UPDATE decision.runs SET final_decision = 'REJECT' WHERE id = $1::uuid`,
		"delete the run":      `DELETE FROM decision.runs WHERE id = $1::uuid`,
		"change a trace":      `UPDATE decision.rule_traces SET passed = false WHERE run_id = $1::uuid`,
		"delete a trace":      `DELETE FROM decision.rule_traces WHERE run_id = $1::uuid`,
		"change the hash":     `UPDATE decision.requests SET request_hash = repeat('0', 64) WHERE id IN (SELECT request_id FROM decision.runs WHERE id = $1::uuid)`,
		"delete the request":  `DELETE FROM decision.requests WHERE id IN (SELECT request_id FROM decision.runs WHERE id = $1::uuid)`,
	} {
		s.Error(s.exec(stmt, id), name)
	}
	s.Equal(1, s.rows("decision.runs"))
}

func (s *PolicyTestSuite) TestARunMustCarryTheChecksumOfItsPolicyVersion() {
	s.activate()
	made := s.json(s.evaluateKey("demo_loan_application", "checksum-1", evaluateBody()).AssertOk())
	version := made["policy_version"].(map[string]any)["id"].(string)
	client := s.userID("demo_loan_application")
	s.Require().NoError(s.exec(`INSERT INTO decision.requests (client_id, idempotency_key, request_hash) VALUES ($1::uuid, 'forged-1', repeat('a', 64))`, client))
	insert := func(checksum string) error {
		return s.exec(`INSERT INTO decision.runs (request_id, client_id, application_id, product_code, policy_version_id, policy_checksum,
				input_snapshot, final_decision, reason_codes, outcome)
			SELECT id, client_id, 'APP-1', 'KTA_DEMO', $2::uuid, $3, '{}'::jsonb, 'REFER', '[]'::jsonb, '{}'::jsonb
			  FROM decision.requests WHERE idempotency_key = 'forged-1'`, client, version, checksum)
	}
	s.Error(insert(strings.Repeat("0", 64)), "a checksum that is not the version's")
}

func (s *PolicyTestSuite) TestConcurrentDuplicatesProduceOneRun() {
	s.activate()
	svc := s.decisionService()
	client := s.userID("demo_loan_application")
	req := s.decisionRequest()

	const n = 12
	var wg sync.WaitGroup
	results := make([]decisionapp.Result, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = svc.Evaluate(context.Background(), client, "race-same", req)
		}()
	}
	wg.Wait()

	replays := 0
	for i := range results {
		s.Require().NoError(errs[i])
		s.Equal(results[0].Decision.ID, results[i].Decision.ID, "every caller gets the one decision")
		if results[i].Replayed {
			replays++
		}
	}
	s.Equal(n-1, replays, "exactly one caller evaluated")
	s.Equal(1, s.rows("decision.requests"))
	s.Equal(1, s.rows("decision.runs"))
	s.Equal(3, s.rows("decision.rule_traces"))
}

func (s *PolicyTestSuite) TestConcurrentRequestsWithOneKeyAndDifferentPayloadsGiveOneWinner() {
	s.activate()
	svc := s.decisionService()
	client := s.userID("demo_loan_application")

	const n = 12
	var wg sync.WaitGroup
	results := make([]decisionapp.Result, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := s.decisionRequest()
			if i%2 == 1 {
				req.Applicant["age_years"] = float64(31) // a different request
			}
			results[i], errs[i] = svc.Evaluate(context.Background(), client, "race-diff", req)
		}()
	}
	wg.Wait()

	var winner string
	for i := range results {
		if errs[i] == nil {
			if winner == "" {
				winner = results[i].Decision.ID
			}
			s.Equal(winner, results[i].Decision.ID)
			continue
		}
		s.ErrorIs(errs[i], decisionapp.ErrIdempotencyConflict)
	}
	s.NotEmpty(winner)
	s.Equal(1, s.rows("decision.runs"), "one run for one key, whatever the mix")
}

func (s *PolicyTestSuite) TestConcurrentDistinctKeysAreAllEvaluated() {
	s.activate()
	svc := s.decisionService()
	client := s.userID("demo_loan_application")

	const n = 10
	var wg sync.WaitGroup
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := svc.Evaluate(context.Background(), client, fmt.Sprintf("race-distinct-%d", i), s.decisionRequest())
			s.NoError(err)
			ids[i] = r.Decision.ID
		}()
	}
	wg.Wait()
	seen := map[string]bool{}
	for _, id := range ids {
		seen[id] = true
	}
	s.Len(seen, n)
	s.Equal(n, s.rows("decision.runs"))
}

func (s *PolicyTestSuite) TestAnAbandonedFirstRequestLetsALaterOneProceed() {
	s.activate()
	svc := s.decisionService()
	client := s.userID("demo_loan_application")

	// A request whose evaluation fails rolls back with its key, so a duplicate is not blocked.
	bad := s.decisionRequest()
	bad.Applicant["monthly_income"] = "0.00"
	_, err := svc.Evaluate(context.Background(), client, "abandon-1", bad)
	var invalid *decisionapp.InputError
	s.ErrorAs(err, &invalid)
	r, err := svc.Evaluate(context.Background(), client, "abandon-1", s.decisionRequest())
	s.Require().NoError(err)
	s.False(r.Replayed)
	s.Equal(1, s.rows("decision.runs"))
}

func (s *PolicyTestSuite) TestActivationSeedingIsRepeatableAndLeavesTheVersionFrozen() {
	s.activate()
	s.Require().NoError(seeders.SeedDemoActivePolicy(), "a second run changes nothing")
	s.Equal(1, s.rows("policy.active_policy_bindings"))

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

// decisionService builds the use case on the real repositories, to call it from many goroutines.
func (s *PolicyTestSuite) decisionService() *decisionapp.EvaluationService {
	db := database.NewManager(facades.DB()).WithModule("decision")
	return decisionapp.NewEvaluationService(decisioninfra.NewPolicyResolver(s.service), decisioninfra.NewDecisionRepository(db), db)
}

func (s *PolicyTestSuite) decisionRequest() decisionapp.Request {
	var applicant map[string]any
	s.Require().NoError(json.Unmarshal([]byte(seeders.DemoApplicant), &applicant))
	return decisionapp.Request{ApplicationID: "LOAN-APP-DEMO-0001", ProductCode: "KTA_DEMO", Applicant: applicant}
}

func selectStrings(dest *[]string, query string, args ...any) error {
	return facades.DB().Select(dest, query, args...)
}

func selectInts(dest *[]int, query string, args ...any) error {
	return facades.DB().Select(dest, query, args...)
}
