package tests

import (
	"github.com/goravel/framework/testing"

	"goravel/bootstrap"
)

// TestBcryptRounds is the bcrypt cost tests expect. The hash service is built during boot,
// so the cost must come from the environment: run tests with HASH_BCRYPT_ROUNDS=4
// (see AGENT_RUNBOOK.md). Cost 12 exceeds the 3s request timeout under the race detector.
// Startup validation in main.go rejects such a low cost for the running API.
const TestBcryptRounds = 4

func init() {
	bootstrap.Boot()
	registerTestRoutes()
}

type TestCase struct {
	testing.TestCase
}
