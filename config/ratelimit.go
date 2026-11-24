package config

import (
	"goravel/app/facades"
)

func init() {
	config := facades.Config()
	config.Add("ratelimit", map[string]any{
		// RiskForge (RF-023): token bucket limits per client, in memory, per process.
		// enabled: false switches every limiter off (for example in a load test).
		// per_minute is the sustained rate, burst the number of requests allowed at once.
		"enabled": config.Env("RATE_LIMIT_ENABLED", true),
		// Login, per client address: guessing passwords.
		"login": map[string]any{
			"per_minute": config.Env("RATE_LIMIT_LOGIN_PER_MIN", 30),
			"burst":      config.Env("RATE_LIMIT_LOGIN_BURST", 10),
		},
		// POST /decisions/evaluate, per authenticated caller.
		"decision": map[string]any{
			"per_minute": config.Env("RATE_LIMIT_DECISION_PER_MIN", 1200),
			"burst":      config.Env("RATE_LIMIT_DECISION_BURST", 100),
		},
		// Routes that compile and run a policy for an analyst or reviewer (simulate, validate,
		// diff, review), per authenticated caller.
		"analysis": map[string]any{
			"per_minute": config.Env("RATE_LIMIT_ANALYSIS_PER_MIN", 60),
			"burst":      config.Env("RATE_LIMIT_ANALYSIS_BURST", 20),
		},
	})
}
