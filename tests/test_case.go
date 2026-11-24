package tests

import (
	"github.com/goravel/framework/testing"

	"goravel/app/platform/ratelimit"
	"goravel/bootstrap"
)

// TestBcryptRounds is the bcrypt cost tests expect. The hash service is built during boot,
// so the cost must come from the environment: run tests with HASH_BCRYPT_ROUNDS=4
// (see AGENT_RUNBOOK.md). Cost 12 exceeds the 3s request timeout under the race detector.
// Startup validation in main.go rejects such a low cost for the running API.
const TestBcryptRounds = 4

func init() {
	bootstrap.Boot()
	// The suites log in and call the same routes far more often than a client would. The rate
	// limit tests (tests/feature/rate_limit_test.go) switch limiting on for their own duration.
	ratelimit.SetEnabled(false)
	registerTestRoutes()
}

type TestCase struct {
	testing.TestCase
}
