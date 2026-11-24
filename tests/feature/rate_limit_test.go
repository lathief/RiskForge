package feature

import (
	"strings"

	"github.com/goravel/framework/contracts/http"
	contractstesting "github.com/goravel/framework/contracts/testing/http"

	"goravel/app/facades"
	"goravel/app/platform/metrics"
	"goravel/app/platform/ratelimit"
	"goravel/database/seeders"
	"goravel/routes"
)

// Rate limits (RF-023, SEC-02, M4-SEC-04). The suites run with limiting off (tests/test_case.go);
// each test here switches it on with small budgets and restores the configuration afterwards.

func (s *PolicyTestSuite) limitTo(l *ratelimit.Limiter, perMinute, burst int) {
	ratelimit.SetEnabled(true)
	l.Configure(perMinute, burst)
	routes.Limiters.ResetAll()
	s.T().Cleanup(func() {
		ratelimit.SetEnabled(false)
		routes.Limiters.ConfigureFrom(facades.Config())
		routes.Limiters.ResetAll()
	})
}

func (s *PolicyTestSuite) login(username, password string) contractstesting.Response {
	resp, err := s.Http(s.T()).WithHeader("Content-Type", "application/json").Post("/api/v1/auth/login",
		strings.NewReader(`{"username":"`+username+`","password":"`+password+`"}`))
	s.Require().NoError(err)
	return resp
}

func (s *PolicyTestSuite) TestLoginIsLimitedPerClientAddress() {
	s.limitTo(routes.Limiters.Login, 6, 3)
	before := metrics.RateLimited.Value("login")
	for i := 0; i < 3; i++ {
		s.login("demo_analyst", "wrong password").AssertStatus(http.StatusUnauthorized) // reaches the credential check
	}
	resp := s.login("demo_analyst", demoPassword)
	resp.AssertStatus(http.StatusTooManyRequests) // even the right password, once the budget is used
	s.Equal("10", resp.Headers().Get("Retry-After"), "6 per minute is one token every ten seconds")
	code, _ := s.errorOf(resp)
	s.Equal("RATE_LIMITED", code)
	s.NotEmpty(s.json(resp)["request_id"])
	s.Equal(before+1, metrics.RateLimited.Value("login"))
	s.Contains(s.metricsText(), `riskforge_rate_limited_total{limiter="login"}`)
}

func (s *PolicyTestSuite) TestEvaluateIsLimitedPerCallerAndAFreshBudgetStartsFull() {
	s.activate()
	s.limitTo(routes.Limiters.Decision, 60, 2)
	runs := s.rows("decision.runs")
	s.evaluate("demo_loan_application", evaluateBody()).AssertOk()
	s.evaluate("demo_loan_application", evaluateBody()).AssertOk()
	resp := s.evaluate("demo_loan_application", evaluateBody())
	resp.AssertStatus(http.StatusTooManyRequests)
	s.Equal("1", resp.Headers().Get("Retry-After"))
	code, _ := s.errorOf(resp)
	s.Equal("RATE_LIMITED", code)
	s.Equal(runs+2, s.rows("decision.runs"), "the refused request was not evaluated or stored")
	s.Equal("no-store", resp.Headers().Get("Cache-Control"))
	// Reading a decision is not limited by the evaluate budget.
	s.getDecision("demo_auditor", "00000000-0000-4000-8000-000000000000").AssertNotFound()
	routes.Limiters.ResetAll()
	s.evaluate("demo_loan_application", evaluateBody()).AssertOk()
}

func (s *PolicyTestSuite) TestAnalysisRoutesShareOneBudgetPerUser() {
	s.activate()
	_, active := s.activeIDs()
	s.limitTo(routes.Limiters.Analysis, 60, 3)
	simulate := `{"applicant":` + seeders.DemoApplicant + `}`
	call := func(user, method, suffix, body string) contractstesting.Response {
		return s.send(user, method, versionsPath+"/"+active+suffix, body)
	}
	call("demo_analyst", "POST", "/simulate", simulate).AssertOk()
	call("demo_analyst", "POST", "/validate", "").AssertOk()
	call("demo_analyst", "GET", "/diff", "").AssertOk()
	before := metrics.RateLimited.Value("analysis")
	for _, c := range [][3]string{{"POST", "/simulate", simulate}, {"POST", "/validate", ""}, {"GET", "/diff", ""}} {
		resp := call("demo_analyst", c[0], c[1], c[2])
		resp.AssertStatus(http.StatusTooManyRequests)
		s.NotEmpty(resp.Headers().Get("Retry-After"), c[1])
	}
	s.Equal(before+3, metrics.RateLimited.Value("analysis"))
	// Other routes of the same version are not limited. The budget is the caller's own: the
	// approver has none left of the analyst's, and is refused for permission, not for rate.
	call("demo_analyst", "GET", "", "").AssertOk()
	call("demo_approver_1", "GET", "/diff", "").AssertOk()
}

func (s *PolicyTestSuite) TestTheReviewIsLimitedPerApprover() {
	s.activate()
	policyID, _ := s.activeIDs()
	id := s.submitted(s.stricterDraft(policyID, "", 30))
	s.limitTo(routes.Limiters.Analysis, 60, 1)
	path := approvalRequestsPath + "/" + id + "/review"
	s.send("demo_approver_1", "GET", path, "").AssertOk()
	s.send("demo_approver_1", "GET", path, "").AssertStatus(http.StatusTooManyRequests)
	s.send("demo_approver_2", "GET", path, "").AssertOk()
	s.send("demo_auditor", "GET", path, "").AssertOk()
	// Approving is not an analysis route and is never limited by this budget.
	s.act("demo_approver_1", id, `{"action":"APPROVE"}`).AssertOk()
}

func (s *PolicyTestSuite) TestAnUnauthenticatedRequestIsRefusedBeforeItCountsAgainstAnyone() {
	s.activate()
	s.limitTo(routes.Limiters.Decision, 60, 1)
	for i := 0; i < 3; i++ {
		s.send("", "POST", evaluatePath, evaluateBody()).AssertStatus(http.StatusUnauthorized)
	}
	s.evaluate("demo_loan_application", evaluateBody()).AssertOk()
}

func (s *PolicyTestSuite) TestLimitsAreOffWhenSwitchedOff() {
	s.activate()
	s.limitTo(routes.Limiters.Decision, 60, 1)
	ratelimit.SetEnabled(false)
	for i := 0; i < 5; i++ {
		s.evaluate("demo_loan_application", evaluateBody()).AssertOk()
	}
}
