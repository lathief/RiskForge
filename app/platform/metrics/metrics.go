package metrics

import (
	"runtime"
	"strconv"
	"sync/atomic"
	"time"
)

// The metrics of SYSTEM_DESIGN.md 15.3, registered on Default. They are variables so the code
// that counts an event (controllers, middleware, the rate limiter) calls them directly and
// tests can read them.
var (
	// HTTPRequests counts every request the application handled, including 401, 403, 404, 408,
	// 422, 429, and 500. module is the owner of the route, or "none" for an unmatched route.
	HTTPRequests = Default.NewCounterVec("riskforge_http_requests_total",
		"HTTP requests handled, by module and status code.", "module", "code")
	// HTTPDuration is the time from the start of the request to the response.
	HTTPDuration = Default.NewHistogramVec("riskforge_http_request_duration_seconds",
		"HTTP request duration in seconds, by module.", DurationBuckets, "module")
	// DecisionRequests counts evaluate calls by result: APPROVE, REFER, REJECT (a new decision),
	// replayed, and one value per failure class (input_error, product_not_found, policy_not_active,
	// idempotency_conflict, busy, policy_error, evaluation_error, error). Failures are never REJECT.
	DecisionRequests = Default.NewCounterVec("riskforge_decision_requests_total",
		"Decision evaluate calls by outcome.", "outcome")
	// DecisionDuration is the time the evaluate use case took, by outcome class.
	DecisionDuration = Default.NewHistogramVec("riskforge_decision_duration_seconds",
		"Decision evaluate duration in seconds (use case, without HTTP), by outcome.", DurationBuckets, "outcome")
	// ApprovalActions counts approver actions that were recorded: APPROVE and REJECT.
	ApprovalActions = Default.NewCounterVec("riskforge_approval_actions_total",
		"Recorded approval actions by action.", "action")
	// PolicyPublish counts publications: the final approval of a request.
	PolicyPublish = Default.NewCounterVec("riskforge_policy_publish_total",
		"Policy versions published by the final approval.")
	// RateLimited counts requests refused with 429, by limiter name.
	RateLimited = Default.NewCounterVec("riskforge_rate_limited_total",
		"Requests refused by a rate limiter, by limiter.", "limiter")
	// RateLimiterFull counts requests of a client the limiter could not track because its
	// table was full; they were counted against one bucket shared by all such clients.
	RateLimiterFull = Default.NewCounterVec("riskforge_rate_limiter_table_full_total",
		"Requests counted against the shared bucket because a limiter table was full, by limiter.", "limiter")
	// RequestsShed counts requests refused with 503 because the process already handled its
	// maximum number of requests (HTTP_MAX_IN_FLIGHT).
	RequestsShed = Default.NewCounterVec("riskforge_requests_shed_total",
		"Requests refused with 503 because too many were in flight.")
)

var started = time.Now()

// InFlight is the number of requests being handled now; the in-flight gate keeps it current.
var InFlight atomic.Int64

func init() {
	Default.NewGaugeFunc("riskforge_uptime_seconds", "Seconds since the process started.",
		func() float64 { return time.Since(started).Seconds() })
	Default.NewGaugeFunc("riskforge_http_in_flight", "HTTP requests being handled now.",
		func() float64 { return float64(InFlight.Load()) })
	Default.NewGaugeFunc("riskforge_go_goroutines", "Number of goroutines.",
		func() float64 { return float64(runtime.NumGoroutine()) })
	Default.NewGaugeFunc("riskforge_go_heap_alloc_bytes", "Bytes of allocated heap objects.", func() float64 {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return float64(m.HeapAlloc)
	})
}

// ObserveHTTP records one handled request.
func ObserveHTTP(module string, status int, d time.Duration) {
	if module == "" {
		module = "none"
	}
	HTTPRequests.Inc(module, strconv.Itoa(status))
	HTTPDuration.Observe(d.Seconds(), module)
}

// ObserveDecision records one evaluate call.
func ObserveDecision(outcome string, d time.Duration) {
	DecisionRequests.Inc(outcome)
	DecisionDuration.Observe(d.Seconds(), outcome)
}
