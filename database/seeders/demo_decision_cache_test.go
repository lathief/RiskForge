package seeders

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"goravel/app/modules/decision/application"
	"goravel/app/modules/decision/infrastructure"
	policydomain "goravel/app/modules/policy/domain"
)

// The compiled policy cache (RF-016, SYSTEM_DESIGN.md 10.6) in front of the real demo policy.

// frozenDemoVersion freezes the demo policy as version number n, with the minimum age rule
// changed to minAge so that two versions decide differently.
func frozenDemoVersion(t testing.TB, id string, n int, minAge int) policydomain.Version {
	content, snapshot, _ := demoPolicy(t)
	rules := make([]policydomain.RuleInput, 0, len(DemoRacRules))
	for _, r := range DemoRacRules {
		if r.RuleCode == "RAC_MIN_AGE" {
			r.Expression = fmt.Sprintf("age_years >= %d", minAge)
		}
		rules = append(rules, r)
	}
	set, err := policydomain.ValidateRules(rules, policydomain.DictionaryEnvironment(snapshot))
	require.NoError(t, err)
	content.Rules = set
	v := policydomain.Version{
		ID: id, ProductCode: "KTA_DEMO", Scope: "DEFAULT", Number: n,
		Status: policydomain.StatusActive, DictionarySchemaVersion: 8,
	}
	raw, checksum, err := policydomain.BuildSnapshot(v, snapshot, content)
	require.NoError(t, err)
	v.Snapshot, v.Checksum = raw, checksum
	return v
}

func resolve(t *testing.T, r *infrastructure.PolicyResolver) application.ResolvedPolicy {
	t.Helper()
	p, err := r.ResolveActive(context.Background(), "KTA_DEMO", application.DefaultScope)
	require.NoError(t, err)
	return p
}

func TestManyDecisionsReadThePointerEveryTimeButLoadThePolicyOnce(t *testing.T) {
	active := activeDemo(t)
	resolver := infrastructure.NewPolicyResolver(active)

	var first application.ResolvedPolicy
	for i := 0; i < 25; i++ {
		p := resolve(t, resolver)
		if first == nil {
			first = p
		}
		assert.Same(t, first, p, "every decision uses the one compiled policy")
		out, err := p.Evaluate(applicantOf(t))
		require.NoError(t, err)
		assert.Equal(t, "APPROVE", out.FinalDecision)
	}
	refs, loads := active.calls()
	assert.Equal(t, 25, refs, "the active binding is read on every decision (SYSTEM_DESIGN.md 10.6)")
	assert.Equal(t, 1, loads, "the snapshot is read, verified, parsed, and compiled once")
	assert.Equal(t, infrastructure.CacheStats{Hits: 24, Misses: 1, Loads: 1, Entries: 1}, resolver.CacheStats())
}

func TestAPublishIsSeenImmediatelyAndNeverChangesARunThatAlreadyResolved(t *testing.T) {
	v1 := frozenDemoVersion(t, "11111111-1111-4111-8111-111111111111", 1, 21)
	v2 := frozenDemoVersion(t, "22222222-2222-4222-8222-222222222222", 2, 30)
	require.NotEqual(t, v1.Checksum, v2.Checksum)
	active := &fakeActive{version: v1}
	resolver := infrastructure.NewPolicyResolver(active)
	applicant := func() map[string]any { return applicantOf(t) } // age 29

	inFlight := resolve(t, resolver) // a run that resolved version 1 before the publish
	out, err := inFlight.Evaluate(applicant())
	require.NoError(t, err)
	assert.Equal(t, "APPROVE", out.FinalDecision, "age 29 passes the minimum age of 21")

	active.setVersion(v2) // the publish moves the binding

	after := resolve(t, resolver)
	assert.Equal(t, 2, after.Ref().VersionNumber, "the next decision uses the new version at once, no invalidation needed")
	out, err = after.Evaluate(applicant())
	require.NoError(t, err)
	assert.Equal(t, "REJECT", out.FinalDecision, "age 29 fails the minimum age of 30 in version 2")

	// The run that resolved before the publish still decides with version 1, every time.
	assert.Equal(t, 1, inFlight.Ref().VersionNumber)
	out, err = inFlight.Evaluate(applicant())
	require.NoError(t, err)
	assert.Equal(t, "APPROVE", out.FinalDecision)

	// Going back (or a replay of an old version) is a hit, not a load.
	active.setVersion(v1)
	assert.Same(t, inFlight, resolve(t, resolver))
	_, loads := active.calls()
	assert.Equal(t, 2, loads, "one load per version, ever")
	assert.Equal(t, 2, resolver.CacheStats().Entries)
}

func TestTheSameVersionIdWithAnotherChecksumNeverServesTheOldPolicy(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	old := frozenDemoVersion(t, id, 1, 21)
	changed := frozenDemoVersion(t, id, 1, 30) // same id and number, different frozen content
	active := &fakeActive{version: old}
	resolver := infrastructure.NewPolicyResolver(active)

	out, err := resolve(t, resolver).Evaluate(applicantOf(t))
	require.NoError(t, err)
	assert.Equal(t, "APPROVE", out.FinalDecision)

	active.setVersion(changed)
	out, err = resolve(t, resolver).Evaluate(applicantOf(t))
	require.NoError(t, err)
	assert.Equal(t, "REJECT", out.FinalDecision, "the key includes the checksum, so the old entry cannot answer for new content")
}

func TestABrokenVersionIsLoadedOnceAndAnsweredFromMemoryAfterwards(t *testing.T) {
	active := activeDemo(t)
	active.version.Snapshot = []byte(`{"format":99}`)
	resolver := infrastructure.NewPolicyResolver(active)

	for i := 0; i < 5; i++ {
		_, err := resolver.ResolveActive(context.Background(), "KTA_DEMO", application.DefaultScope)
		assert.ErrorIs(t, err, application.ErrPolicyConfiguration)
	}
	_, loads := active.calls()
	assert.Equal(t, 1, loads, "a snapshot that cannot be read is the same every time, so it costs one load, not one per request")
}

func TestATransientLoadFailureIsNotRemembered(t *testing.T) {
	active := activeDemo(t)
	active.loadErr = errors.New("connection reset")
	resolver := infrastructure.NewPolicyResolver(active)

	_, err := resolver.ResolveActive(context.Background(), "KTA_DEMO", application.DefaultScope)
	require.Error(t, err)
	assert.NotErrorIs(t, err, application.ErrPolicyConfiguration, "a database fault is not a configuration error")

	active.loadErr = nil
	assert.Equal(t, "APPROVE", mustEvaluate(t, resolve(t, resolver)).FinalDecision, "the next request loads again and succeeds")
	_, loads := active.calls()
	assert.Equal(t, 2, loads)
}

func mustEvaluate(t *testing.T, p application.ResolvedPolicy) application.Outcome {
	t.Helper()
	out, err := p.Evaluate(applicantOf(t))
	require.NoError(t, err)
	return out
}

func TestAColdCacheUnderLoadLoadsOnce(t *testing.T) {
	active := activeDemo(t)
	resolver := infrastructure.NewPolicyResolver(active)

	var wg sync.WaitGroup
	decisions := make([]string, 60)
	for i := range decisions {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := resolver.ResolveActive(context.Background(), "KTA_DEMO", application.DefaultScope)
			if !assert.NoError(t, err) {
				return
			}
			out, err := p.Evaluate(applicantOf(t))
			if assert.NoError(t, err) {
				decisions[i] = out.FinalDecision
			}
		}()
	}
	wg.Wait()
	for _, d := range decisions {
		assert.Equal(t, "APPROVE", d)
	}
	_, loads := active.calls()
	assert.Equal(t, 1, loads, "sixty simultaneous first requests share one load")
}

func TestTheCacheHoldsAtMostItsBoundOfPolicies(t *testing.T) {
	active := &fakeActive{}
	resolver := infrastructure.NewPolicyResolverWithCache(active, 2)
	versions := []policydomain.Version{
		frozenDemoVersion(t, "11111111-1111-4111-8111-111111111111", 1, 21),
		frozenDemoVersion(t, "22222222-2222-4222-8222-222222222222", 2, 22),
		frozenDemoVersion(t, "33333333-3333-4333-8333-333333333333", 3, 23),
	}
	for _, v := range versions {
		active.setVersion(v)
		resolve(t, resolver)
		assert.LessOrEqual(t, resolver.CacheStats().Entries, 2)
	}
	active.setVersion(versions[0]) // evicted: loaded again, still correct
	assert.Equal(t, 1, resolve(t, resolver).Ref().VersionNumber)
	_, loads := active.calls()
	assert.Equal(t, 4, loads)
}

// BenchmarkResolveAndEvaluate compares a decision on a warm cache with one that has to load,
// parse, and compile the snapshot, which is what every decision cost before RF-016. The numbers
// are informal (one machine, no tuning); the baseline for documentation is RF-024.
func BenchmarkResolveAndEvaluate(b *testing.B) {
	applicant := map[string]any{
		"age_years": float64(29), "monthly_income": "12000000.00", "existing_installment": "2500000.00",
		"employment_tenure_months": float64(36), "slik_max_kol": float64(1), "is_blacklisted": false,
	}
	run := func(b *testing.B, resolver func() *infrastructure.PolicyResolver) {
		for i := 0; i < b.N; i++ {
			p, err := resolver().ResolveActive(context.Background(), "KTA_DEMO", application.DefaultScope)
			if err != nil {
				b.Fatal(err)
			}
			if _, err := p.Evaluate(applicant); err != nil {
				b.Fatal(err)
			}
		}
	}
	v := frozenDemoVersion(b, "11111111-1111-4111-8111-111111111111", 1, 21)
	b.Run("cached", func(b *testing.B) {
		shared := infrastructure.NewPolicyResolver(&fakeActive{version: v})
		b.ReportAllocs()
		b.ResetTimer()
		run(b, func() *infrastructure.PolicyResolver { return shared })
	})
	b.Run("compile_every_time", func(b *testing.B) {
		active := &fakeActive{version: v}
		b.ReportAllocs()
		b.ResetTimer()
		run(b, func() *infrastructure.PolicyResolver { return infrastructure.NewPolicyResolver(active) })
	})
}
