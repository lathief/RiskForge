package feature

import (
	"context"
	"strings"
	"sync"

	"github.com/goravel/framework/contracts/http"

	"goravel/app/facades"
	decisionapp "goravel/app/modules/decision/application"
	decisioninfra "goravel/app/modules/decision/infrastructure"
)

// The compiled policy cache (RF-016) against the real database and the real HTTP application.

// publishSecondVersion does by hand, for the test, what the publish of M4 will do in one
// transaction: freeze a revision whose minimum age is 22 instead of 21, move it through
// APPROVED to ACTIVE, point the binding at it, and archive the old version. It returns the ids
// of the old and the new version.
func (s *PolicyTestSuite) publishSecondVersion() (oldID, newID string) {
	list := s.json(s.send("demo_auditor", "GET", policiesPath+"?product_code=KTA_DEMO", "").AssertOk())["data"].([]any)
	policyID := list[0].(map[string]any)["id"].(string)
	oldID = list[0].(map[string]any)["latest_version"].(map[string]any)["id"].(string)

	newID = s.clone(policyID, `{}`)["id"].(string)
	s.putRules("demo_analyst", newID, strings.Replace(demoRulesBody, "age_years >= 21", "age_years >= 22", 1)).AssertOk()
	s.submitRaw(newID).AssertOk()

	s.Require().NoError(s.exec(`UPDATE policy.policy_versions SET status = 'APPROVED' WHERE id = $1::uuid`, newID))
	s.Require().NoError(s.exec(`UPDATE policy.policy_versions SET status = 'ACTIVE' WHERE id = $1::uuid`, newID))
	s.Require().NoError(s.exec(`UPDATE policy.active_policy_bindings SET policy_version_id = $1::uuid`, newID))
	s.Require().NoError(s.exec(`UPDATE policy.policy_versions SET status = 'ARCHIVED' WHERE id = $1::uuid`, oldID))
	return oldID, newID
}

func (s *PolicyTestSuite) TestAPublishIsSeenByTheNextDecisionAndReplaysKeepTheirVersion() {
	s.activate()
	// An applicant aged exactly 21 passes the minimum age of version 1 and fails that of version 2.
	age21 := evaluateBody(`"age_years":29`, `"age_years":21`)

	before := s.json(s.evaluateKey("demo_loan_application", "publish-1", age21).AssertOk())
	s.Equal("REFER", before["final_decision"], "640.00 is in the REFER band of the matrix")
	s.Equal(float64(1), before["policy_version"].(map[string]any)["number"])
	// The cache is warm now: the same version answers a second decision from memory.
	s.json(s.evaluateKey("demo_loan_application", "publish-1b", age21).AssertOk())

	oldID, newID := s.publishSecondVersion()

	after := s.json(s.evaluateKey("demo_loan_application", "publish-2", age21).AssertOk())
	s.Equal("REJECT", after["final_decision"], "the next decision uses the new version at once, without any invalidation")
	s.Equal([]any{"AGE_BELOW_MINIMUM"}, after["reason_codes"])
	version := after["policy_version"].(map[string]any)
	s.Equal(float64(2), version["number"])
	s.Equal(newID, version["id"])
	s.NotEqual(before["policy_version"].(map[string]any)["checksum"], version["checksum"])

	// A retry of the earlier request still returns the stored decision of the archived version.
	replay := s.evaluateKey("demo_loan_application", "publish-1", age21)
	replay.AssertOk()
	s.Equal("true", replay.Headers().Get("Idempotent-Replayed"))
	s.Equal(before, s.json(replay))
	s.Equal(oldID, before["policy_version"].(map[string]any)["id"])

	var runs []string
	s.Require().NoError(selectStrings(&runs, `SELECT policy_version_id::text FROM decision.runs ORDER BY created_at, id`))
	s.Len(runs, 3)
	s.Equal(newID, runs[2])
}

func (s *PolicyTestSuite) TestTheCompiledPolicyIsLoadedOnceForManyDecisions() {
	s.activate()
	resolver := decisioninfra.NewPolicyResolver(s.service)
	for i := 0; i < 10; i++ {
		p, err := resolver.ResolveActive(context.Background(), "KTA_DEMO", decisionapp.DefaultScope)
		s.Require().NoError(err)
		s.Equal(1, p.Ref().VersionNumber)
	}
	stats := resolver.CacheStats()
	s.EqualValues(1, stats.Loads, "the snapshot was read, verified, and compiled once")
	s.EqualValues(9, stats.Hits)
	s.Equal(1, stats.Entries)
}

func (s *PolicyTestSuite) TestAColdCacheUnderConcurrentLoadLoadsOnceFromTheDatabase() {
	s.activate()
	resolver := decisioninfra.NewPolicyResolver(s.service)

	const n = 24
	var wg sync.WaitGroup
	numbers := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := resolver.ResolveActive(context.Background(), "KTA_DEMO", decisionapp.DefaultScope)
			if s.NoError(err) {
				numbers[i] = p.Ref().VersionNumber
			}
		}()
	}
	wg.Wait()
	for _, v := range numbers {
		s.Equal(1, v)
	}
	s.EqualValues(1, resolver.CacheStats().Loads, "24 simultaneous first decisions, one load")
}

func (s *PolicyTestSuite) TestACachedPolicyStillRespectsAnInactiveProductAndARemovedBinding() {
	s.activate()
	s.evaluate("demo_loan_application", evaluateBody()).AssertOk() // warms the cache of the running application

	// The pointer is read on every decision, so the cache cannot keep a retired policy alive.
	s.Require().NoError(s.exec(`DELETE FROM policy.active_policy_bindings`))
	resp := s.evaluate("demo_loan_application", evaluateBody())
	resp.AssertStatus(http.StatusConflict)
	code, _ := s.errorOf(resp)
	s.Equal("POLICY_NOT_ACTIVE", code)

	// Binding the same version again is a cache hit and works.
	s.Require().NoError(seedBinding(s))
	s.evaluate("demo_loan_application", evaluateBody()).AssertOk()
}

// seedBinding points the package of KTA_DEMO at its ACTIVE version again.
func seedBinding(s *PolicyTestSuite) error {
	return facades.DB().Statement(`INSERT INTO policy.active_policy_bindings (package_id, policy_version_id)
		SELECT package_id, id FROM policy.policy_versions WHERE status = 'ACTIVE'`)
}
