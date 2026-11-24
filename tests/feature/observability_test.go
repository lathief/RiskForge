package feature

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/goravel/framework/contracts/http"

	"goravel/app/facades"
	"goravel/app/http/middleware"
	"goravel/app/platform/database"
	"goravel/app/platform/metrics"
)

// Observability and security (RF-023, SYSTEM_DESIGN.md 15): security headers, metrics, and the
// bound on a duplicate request that waits for an Idempotency-Key.

const metricsPath = "/metrics"

func (s *PolicyTestSuite) metricsText() string {
	resp := s.send("demo_admin", "GET", metricsPath, "").AssertOk()
	body, err := resp.Content()
	s.Require().NoError(err)
	return body
}

func (s *PolicyTestSuite) TestEveryAnswerCarriesTheSecurityHeaders() {
	s.activate()
	cases := map[string]int{
		"/health/live":          http.StatusOK,
		"/no/such/route":        http.StatusNotFound,
		"/api/v1/auth/me":       http.StatusUnauthorized,
		"/api/v1/audit/events":  http.StatusUnauthorized,
		"/metrics":              http.StatusUnauthorized,
		"/api/v1/products/NONE": http.StatusUnauthorized,
	}
	for path, want := range cases {
		resp := s.send("", "GET", path, "")
		resp.AssertStatus(want)
		for name, value := range middleware.SecurityHeaderValues {
			s.Equal(value, resp.Headers().Get(name), "%s on %s", name, path)
		}
	}
	// A handler may replace a header: a login answer is no-store like all others, and a 403 and a
	// 200 carry the same set.
	for _, user := range []string{"demo_admin", "demo_analyst"} {
		resp := s.send(user, "GET", "/api/v1/auth/me", "").AssertOk()
		s.Equal("no-store", resp.Headers().Get("Cache-Control"))
		s.Equal("nosniff", resp.Headers().Get("X-Content-Type-Options"))
	}
	forbidden := s.send("demo_auditor", "GET", metricsPath, "")
	forbidden.AssertStatus(http.StatusForbidden)
	s.Equal("DENY", forbidden.Headers().Get("X-Frame-Options"))
}

func (s *PolicyTestSuite) TestOnlyTheAdminReadsTheMetrics() {
	for user, want := range map[string]int{
		"": http.StatusUnauthorized, "demo_admin": http.StatusOK, "demo_analyst": http.StatusForbidden,
		"demo_approver_1": http.StatusForbidden, "demo_auditor": http.StatusForbidden, "demo_loan_application": http.StatusForbidden,
	} {
		s.send(user, "GET", metricsPath, "").AssertStatus(want)
	}
}

func (s *PolicyTestSuite) TestMetricsAreTextInThePrometheusFormat() {
	resp := s.send("demo_admin", "GET", metricsPath, "").AssertOk()
	s.Equal("text/plain; version=0.0.4; charset=utf-8", resp.Headers().Get("Content-Type"))
	body := s.metricsText()
	for _, name := range []string{
		"riskforge_http_requests_total", "riskforge_http_request_duration_seconds",
		"riskforge_decision_requests_total", "riskforge_decision_duration_seconds",
		"riskforge_approval_actions_total", "riskforge_policy_publish_total",
		"riskforge_policy_cache_hits_total", "riskforge_policy_cache_misses_total", "riskforge_policy_cache_loads_total",
		"riskforge_policy_cache_waits_total", "riskforge_policy_cache_entries",
		"riskforge_rate_limited_total", "riskforge_uptime_seconds", "riskforge_go_goroutines",
	} {
		s.Contains(body, "# TYPE "+name+" ", name)
	}
	// The request that read the metrics is counted by the time of the next read.
	s.Regexp(`riskforge_http_requests_total\{module="metrics",code="200"\} \d+`, s.metricsText())
}

func (s *PolicyTestSuite) TestDecisionMetricsCountOutcomesAndNeverTurnAFailureIntoAReject() {
	s.activate()
	count := func(outcome string) uint64 { return metrics.DecisionRequests.Value(outcome) }
	approve, reject, replayed := count("APPROVE"), count("REJECT"), count("replayed")
	notFound, input, conflict := count("product_not_found"), count("input_error"), count("idempotency_conflict")
	durations := metrics.DecisionDuration.Count("APPROVE")

	s.evaluateKey("demo_loan_application", "metrics-key-1", evaluateBody()).AssertOk()
	s.evaluateKey("demo_loan_application", "metrics-key-1", evaluateBody()).AssertOk() // replay
	s.evaluate("demo_loan_application", evaluateBody(`"age_years":29`, `"age_years":17`)).AssertOk()
	s.evaluate("demo_loan_application", strings.Replace(evaluateBody(), "KTA_DEMO", "NO_SUCH_PRODUCT", 1)).AssertStatus(http.StatusNotFound)
	s.evaluate("demo_loan_application", `{"application_id":"x"}`).AssertStatus(http.StatusUnprocessableEntity)
	s.evaluateKey("demo_loan_application", "metrics-key-1", evaluateBody(`"age_years":29`, `"age_years":40`)).AssertStatus(http.StatusConflict)

	s.Equal(approve+1, count("APPROVE"))
	s.Equal(replayed+1, count("replayed"))
	s.Equal(reject+1, count("REJECT"), "only the real rejection of the 17 year old")
	s.Equal(notFound+1, count("product_not_found"))
	s.Equal(input+1, count("input_error"))
	s.Equal(conflict+1, count("idempotency_conflict"))
	s.Equal(durations+1, metrics.DecisionDuration.Count("APPROVE"))

	body := s.metricsText()
	s.Contains(body, `riskforge_decision_requests_total{outcome="APPROVE"}`)
	s.Contains(body, `riskforge_decision_requests_total{outcome="product_not_found"}`)
	s.NotContains(body, "LOAN-APP-DEMO-0001", "no application data in the metrics")
	s.NotContains(body, "monthly_income")
}

func (s *PolicyTestSuite) TestAPublishAndItsApprovalActionsAreCounted() {
	s.activate()
	policyID, _ := s.activeIDs()
	approvals, publishes := metrics.ApprovalActions.Value("APPROVE"), metrics.PolicyPublish.Value()
	rejections := metrics.ApprovalActions.Value("REJECT")

	id := s.submitted(s.stricterDraft(policyID, "", 30))
	s.act("demo_approver_1", id, `{"action":"APPROVE"}`).AssertOk()
	s.Equal(approvals+1, metrics.ApprovalActions.Value("APPROVE"))
	s.Equal(publishes, metrics.PolicyPublish.Value(), "nothing is published before the last tier")
	s.act("demo_approver_1", id, `{"action":"APPROVE"}`).AssertStatus(http.StatusConflict)
	s.Equal(approvals+1, metrics.ApprovalActions.Value("APPROVE"), "a refused action is not counted")
	s.act("demo_approver_2", id, `{"action":"APPROVE"}`).AssertOk()
	s.Equal(approvals+2, metrics.ApprovalActions.Value("APPROVE"))
	s.Equal(publishes+1, metrics.PolicyPublish.Value())

	id = s.submitted(s.stricterDraft(policyID, "", 31))
	s.act("demo_approver_1", id, `{"action":"REJECT","comment":"no"}`).AssertOk()
	s.Equal(rejections+1, metrics.ApprovalActions.Value("REJECT"))
	s.Equal(publishes+1, metrics.PolicyPublish.Value(), "a rejection publishes nothing")

	body := s.metricsText()
	s.Contains(body, `riskforge_approval_actions_total{action="APPROVE"}`)
	s.Regexp(`riskforge_policy_publish_total \d+`, body)
}

func (s *PolicyTestSuite) TestEveryRequestIsCountedByModuleAndStatus() {
	before := metrics.HTTPRequests.Value("policy", "403")
	beforeDuration := metrics.HTTPDuration.Count("policy")
	s.send("demo_auditor", "POST", policiesPath, `{"product_code":"KTA_DEMO"}`).AssertStatus(http.StatusForbidden)
	s.Equal(before+1, metrics.HTTPRequests.Value("policy", "403"))
	s.Equal(beforeDuration+1, metrics.HTTPDuration.Count("policy"))
	none := metrics.HTTPRequests.Value("none", "404")
	s.send("", "GET", "/no/such/route", "").AssertStatus(http.StatusNotFound)
	s.Equal(none+1, metrics.HTTPRequests.Value("none", "404"), "an unmatched route has no module")
}

func (s *PolicyTestSuite) TestThePolicyCacheStatisticsAreExported() {
	s.activate()
	s.evaluate("demo_loan_application", evaluateBody()).AssertOk()
	s.evaluate("demo_loan_application", evaluateBody()).AssertOk()
	body := s.metricsText()
	s.Regexp(`riskforge_policy_cache_hits_total [1-9]\d*`, body)
	s.Regexp(`riskforge_policy_cache_loads_total [1-9]\d*`, body)
	s.Regexp(`riskforge_policy_cache_entries [1-9]\d*`, body)
}

// A duplicate request waits for the transaction that holds its Idempotency-Key. After the lock
// timeout (2 s, below the 3 s request timeout) it answers 503 with a retry hint instead of
// being cut off at the deadline, and holds no connection after that (SEC-05, TST-05).
func (s *PolicyTestSuite) TestADuplicateThatWaitsTooLongAnswersServiceBusy() {
	s.activate()
	clientID := s.userID("demo_loan_application")
	db := database.NewManager(facades.DB())
	busy := metrics.DecisionRequests.Value("busy")
	var elapsed time.Duration
	errRollback := errors.New("rollback")

	err := db.WithinTransaction(context.Background(), func(ctx context.Context) error {
		// The first request of the key has claimed it and is still running.
		if err := db.Querier(ctx).Statement(
			`INSERT INTO decision.requests (client_id, idempotency_key, request_hash) VALUES ($1::uuid, 'held-key-1', repeat('a', 64))`, clientID); err != nil {
			return err
		}
		start := time.Now()
		resp := s.evaluateKey("demo_loan_application", "held-key-1", evaluateBody())
		elapsed = time.Since(start)
		resp.AssertStatus(http.StatusServiceUnavailable)
		s.Equal("1", resp.Headers().Get("Retry-After"))
		code, _ := s.errorOf(resp)
		s.Equal("SERVICE_BUSY", code)
		return errRollback
	})
	s.Require().ErrorIs(err, errRollback)
	s.GreaterOrEqual(elapsed, 1900*time.Millisecond, "the waiter waited for the lock timeout")
	s.Less(elapsed, 2900*time.Millisecond, "and answered before the 3 second request timeout")
	s.Equal(busy+1, metrics.DecisionRequests.Value("busy"))
	s.Equal(0, s.rows("decision.runs"), "the waiter stored nothing")

	// The first request rolled back, so the key is free: the retry is evaluated normally.
	s.evaluateKey("demo_loan_application", "held-key-1", evaluateBody()).AssertOk()
	s.Equal(1, s.rows("decision.runs"))
}

// A duplicate of a request that has already committed is not delayed: it gets the stored decision.
func (s *PolicyTestSuite) TestADuplicateOfACommittedRequestIsNotDelayed() {
	s.activate()
	first := s.json(s.evaluateKey("demo_loan_application", "quick-key-1", evaluateBody()).AssertOk())
	resp := s.evaluateKey("demo_loan_application", "quick-key-1", evaluateBody())
	resp.AssertOk()
	s.Equal("true", resp.Headers().Get("Idempotent-Replayed"))
	s.Equal(first["decision_id"], s.json(resp)["decision_id"])
}
