package middleware

import (
	"math"
	"strconv"
	"time"

	"github.com/goravel/framework/contracts/http"

	"goravel/app/http/auth"
	"goravel/app/http/responses"
	"goravel/app/platform/metrics"
	"goravel/app/platform/ratelimit"
)

// KeyFunc names the client a request counts against.
type KeyFunc func(ctx http.Context) string

// ByIP counts requests per client address. It is the only choice before authentication, as for
// login. Behind a reverse proxy every client shares the proxy's address unless the proxy is
// configured as a trusted proxy of the HTTP driver (docs/adr/0024-observability-and-rate-limits.md).
func ByIP(ctx http.Context) string { return "ip:" + ctx.Request().Ip() }

// ByUser counts requests per authenticated user, and per address when there is none. Put it
// after Authenticate.
func ByUser(ctx http.Context) string {
	if p, ok := auth.PrincipalFrom(ctx); ok {
		return "user:" + p.UserID
	}
	return ByIP(ctx)
}

// RateLimit refuses a request with 429 and Retry-After when its client has used up the
// budget of the limiter (RF-023, SEC-02). It does nothing when limiting is switched off
// (RATE_LIMIT_ENABLED=false). A refused request still reaches activity.log and the metrics.
type RateLimit struct {
	limiter *ratelimit.Limiter
	key     KeyFunc
}

func NewRateLimit(l *ratelimit.Limiter, key KeyFunc) *RateLimit {
	return &RateLimit{limiter: l, key: key}
}

func (m *RateLimit) Signature() string { return "rate_limit:" + m.limiter.Name() }

func (m *RateLimit) Handle(ctx http.Context) {
	if !ratelimit.Enabled() {
		ctx.Request().Next()
		return
	}
	before := m.limiter.FullCount()
	ok, wait := m.limiter.Allow(m.key(ctx))
	if m.limiter.FullCount() != before {
		metrics.RateLimiterFull.Inc(m.limiter.Name())
	}
	if !ok {
		metrics.RateLimited.Inc(m.limiter.Name())
		seconds := int(math.Ceil(wait.Round(time.Millisecond).Seconds()))
		if seconds < 1 {
			seconds = 1
		}
		_ = responses.TooManyRequests(ctx, strconv.Itoa(seconds)).Abort()
		return
	}
	ctx.Request().Next()
}
