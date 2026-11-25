package middleware

import (
	"strings"
	"sync"

	"github.com/goravel/framework/contracts/http"

	"github.com/lathief/RiskForge/app/http/responses"
	"github.com/lathief/RiskForge/app/platform/metrics"
)

// InFlight is the process-wide limit on requests being handled at once. The API sets it from
// HTTP_MAX_IN_FLIGHT when the routes are registered; tests change it with SetLimit.
var InFlight = &Gate{limit: DefaultMaxInFlight}

// DefaultMaxInFlight is the limit when HTTP_MAX_IN_FLIGHT is not set. Throughput on the
// reference machine stops growing at about 8 to 32 concurrent requests (docs/BENCHMARK.md) and
// a request waits as long as the queue in front of it, so 64 keeps the wait of a request in the
// 3 second request timeout where an unbounded queue does not (the M5 stress test: from 128
// concurrent clients every request timed out while the server kept working on them).
const DefaultMaxInFlight = 64

// Gate counts requests in flight and refuses a new one beyond its limit. The count covers the
// whole handler, including work that goes on after the driver has answered 408, so abandoned
// requests keep their place until they really end.
type Gate struct {
	mu       sync.Mutex
	limit    int // 0 means unlimited
	inFlight int
}

// SetLimit changes the limit; 0 switches the gate off. Requests already in flight are not
// interrupted.
func (g *Gate) SetLimit(n int) {
	g.mu.Lock()
	g.limit = n
	g.mu.Unlock()
}

// Limit is the current limit (0 when off).
func (g *Gate) Limit() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.limit
}

// InFlightNow is the number of requests being handled.
func (g *Gate) InFlightNow() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.inFlight
}

// Enter takes a place. It returns false when the gate is full.
func (g *Gate) Enter() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.limit > 0 && g.inFlight >= g.limit {
		return false
	}
	g.inFlight++
	metrics.InFlight.Store(int64(g.inFlight))
	return true
}

// Leave gives the place back.
func (g *Gate) Leave() {
	g.mu.Lock()
	if g.inFlight > 0 {
		g.inFlight--
	}
	metrics.InFlight.Store(int64(g.inFlight))
	g.mu.Unlock()
}

// exemptFromGate is true for the health endpoints: an overloaded process must still answer its
// orchestrator, so that it is judged by what it reports and not restarted for being busy. The
// path is normalized like the other path rules (case and trailing slash).
func exemptFromGate(path string) bool {
	p := strings.ToLower(strings.TrimRight(path, "/"))
	return p == "/health" || strings.HasPrefix(p, "/health/")
}

// InFlightLimit refuses a request with 503 SERVICE_BUSY and Retry-After when the process already
// handles HTTP_MAX_IN_FLIGHT requests. Beyond the capacity of the process a queue only moves the
// wait from the client to the server and ends in 408 for everybody; refusing early keeps the
// requests that are accepted fast, and the client can retry. Register it after ActivityLog, so a
// refused request is still logged and counted.
type InFlightLimit struct{ gate *Gate }

// NewInFlightLimit uses the process-wide gate.
func NewInFlightLimit() *InFlightLimit { return &InFlightLimit{gate: InFlight} }

func (m *InFlightLimit) Signature() string { return "in_flight_limit" }

func (m *InFlightLimit) Handle(ctx http.Context) {
	if exemptFromGate(ctx.Request().Path()) {
		ctx.Request().Next()
		return
	}
	if !m.gate.Enter() {
		metrics.RequestsShed.Inc()
		_ = responses.ServiceBusy(ctx, "1").Abort()
		return
	}
	defer m.gate.Leave()
	ctx.Request().Next()
}
