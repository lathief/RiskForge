package routes

import (
	"github.com/goravel/framework/contracts/config"

	"github.com/lathief/RiskForge/app/platform/ratelimit"
)

// LimiterSet holds the rate limiters of the API (RF-023). They are package state so a test can
// reset or reconfigure them; production code only reads the config once, in Api.
type LimiterSet struct {
	// Login limits POST /auth/login per client address.
	Login *ratelimit.Limiter
	// Decision limits POST /decisions/evaluate per caller.
	Decision *ratelimit.Limiter
	// Analysis limits simulate, validate, diff, and review per caller.
	Analysis *ratelimit.Limiter
}

// Limiters are created with the defaults of config/ratelimit.go and configured from the
// configuration when the routes are registered.
var Limiters = &LimiterSet{
	Login:    ratelimit.New("login", 30, 10),
	Decision: ratelimit.New("decision", 1200, 100),
	Analysis: ratelimit.New("analysis", 60, 20),
}

// ConfigureFrom applies the `ratelimit.*` settings.
func (s *LimiterSet) ConfigureFrom(cfg config.Config) {
	s.Login.Configure(cfg.GetInt("ratelimit.login.per_minute", 30), cfg.GetInt("ratelimit.login.burst", 10))
	s.Decision.Configure(cfg.GetInt("ratelimit.decision.per_minute", 1200), cfg.GetInt("ratelimit.decision.burst", 100))
	s.Analysis.Configure(cfg.GetInt("ratelimit.analysis.per_minute", 60), cfg.GetInt("ratelimit.analysis.burst", 20))
}

// ResetAll forgets every client of every limiter.
func (s *LimiterSet) ResetAll() {
	s.Login.Reset()
	s.Decision.Reset()
	s.Analysis.Reset()
}
