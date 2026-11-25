package feature

import (
	"os"
	"path/filepath"
	"sort"
)

// The synthetic applicants of the demo (scripts/demo/applications, RF-026) are sent through the
// Decision API on the seeded policy. scripts/demo.sh checks the same outcomes against a running
// stack; this test keeps them in the test run of the pull request, so a change to the policy,
// the dictionary, or a fixture cannot break the walkthrough of docs/DEMO.md unnoticed.

const demoFixtureDir = "../../scripts/demo/applications"

// demoFixtureWant is the outcome each fixture must get. An empty decision means a request that
// cannot be evaluated (422 INPUT_VALIDATION_ERROR).
var demoFixtureWant = map[string]struct {
	decision string
	reasons  []any
}{
	"approve-example.json":      {"APPROVE", []any{}},
	"refer-slik-watchlist.json": {"REFER", []any{"SLIK_REVIEW_REQUIRED"}},
	"reject-under-age.json":     {"REJECT", []any{"AGE_BELOW_MINIMUM"}},
	"reject-blacklisted.json":   {"REJECT", []any{"BLACKLISTED"}},
	"invalid-null-income.json":  {"", nil},
}

func (s *PolicyTestSuite) TestDemoFixturesGetTheirDocumentedOutcome() {
	s.activate()
	files, err := filepath.Glob(filepath.Join(demoFixtureDir, "*.json"))
	s.Require().NoError(err)
	var names []string
	for _, f := range files {
		names = append(names, filepath.Base(f))
	}
	sort.Strings(names)
	var want []string
	for n := range demoFixtureWant {
		want = append(want, n)
	}
	sort.Strings(want)
	s.Equal(want, names, "every fixture is listed here and in docs/DEMO.md, and no listed fixture is missing")

	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(demoFixtureDir, name))
		s.Require().NoError(err, name)
		resp := s.evaluate("demo_loan_application", string(raw))
		expected := demoFixtureWant[name]
		if expected.decision == "" {
			resp.AssertStatus(422)
			body := s.json(resp)
			s.Equal("INPUT_VALIDATION_ERROR", body["error"].(map[string]any)["code"], name)
			s.NotContains(body, "final_decision", "%s: an input error never carries a decision", name)
			continue
		}
		body := s.json(resp.AssertOk())
		s.Equal(expected.decision, body["final_decision"], name)
		s.Equal(expected.reasons, body["reason_codes"], name)
	}
}
