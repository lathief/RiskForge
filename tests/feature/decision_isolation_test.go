package feature

import (
	"strings"

	"goravel/app/facades"
)

// otherConsumer creates a second API consumer (same password as the demo users) and returns
// its access token. The demo data has only one consumer, so isolation between consumers
// could not be tested over HTTP before (M3 review).
func (s *PolicyTestSuite) otherConsumer() string {
	db := facades.DB()
	s.Require().NoError(db.Statement(
		`INSERT INTO identity.users (username, password_hash)
		 SELECT 'other_consumer', password_hash FROM identity.users WHERE username = 'demo_loan_application'
		 ON CONFLICT (lower(username)) DO NOTHING`))
	s.Require().NoError(db.Statement(
		`INSERT INTO identity.user_roles (user_id, role_code)
		 SELECT id, 'API_CONSUMER' FROM identity.users WHERE username = 'other_consumer'
		 ON CONFLICT DO NOTHING`))
	// Other suites count users and roles, so the extra user must not outlive its test.
	s.T().Cleanup(func() {
		s.NoError(db.Statement(`TRUNCATE TABLE decision.rule_traces, decision.runs, decision.requests`))
		s.NoError(db.Statement(`DELETE FROM identity.user_roles WHERE user_id IN (SELECT id FROM identity.users WHERE username = 'other_consumer')`))
		s.NoError(db.Statement(`DELETE FROM identity.users WHERE username = 'other_consumer'`))
	})
	resp, err := s.Http(s.T()).WithHeader("Content-Type", "application/json").Post("/api/v1/auth/login",
		strings.NewReader(`{"username":"other_consumer","password":"`+demoPassword+`"}`))
	s.Require().NoError(err)
	resp.AssertOk()
	body, err := resp.Json()
	s.Require().NoError(err)
	token, _ := body["access_token"].(string)
	s.Require().NotEmpty(token)
	return token
}

func (s *PolicyTestSuite) TestAnotherConsumerCannotReadAnyDecisionOfThisOne() {
	s.activate()
	s.tokens["other_consumer"] = s.otherConsumer()
	made := s.json(s.evaluateKey("demo_loan_application", "iso-1", evaluateBody()).AssertOk())
	id := made["decision_id"].(string)

	resp := s.getDecision("other_consumer", id)
	resp.AssertNotFound()
	code, _ := s.errorOf(resp)
	s.Equal("DECISION_NOT_FOUND", code, "a foreign decision looks exactly like one that does not exist")
	unknown := s.getDecision("other_consumer", "99999999-9999-4999-8999-999999999999")
	unknown.AssertNotFound()
	s.Equal(s.json(unknown)["error"], s.json(resp)["error"], "the two answers cannot be told apart")

	s.getDecision("demo_loan_application", id).AssertOk()
	s.getDecision("demo_auditor", id).AssertOk()
}

func (s *PolicyTestSuite) TestIdempotencyKeysBelongToTheCaller() {
	s.activate()
	s.tokens["other_consumer"] = s.otherConsumer()
	first := s.json(s.evaluateKey("demo_loan_application", "shared-key-1", evaluateBody()).AssertOk())

	// The same key from another consumer is a new request: it is not a replay of the first
	// consumer's decision and it is not a conflict, even with a different body.
	other := s.evaluateKey("other_consumer", "shared-key-1", evaluateBody(`"age_years":29`, `"age_years":19`))
	other.AssertOk()
	s.Empty(other.Headers().Get("Idempotent-Replayed"), "another caller's key is not a replay")
	second := s.json(other)
	s.NotEqual(first["decision_id"], second["decision_id"])
	s.Equal("REJECT", second["final_decision"])
	s.Equal("APPROVE", first["final_decision"])
	s.Equal(2, s.rows("decision.runs"))
}
