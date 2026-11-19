package infrastructure

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errConfig = errors.New("deterministic configuration error")

func isConfig(err error) bool { return errors.Is(err, errConfig) }

func policyNamed(name string) *resolvedPolicy {
	return &resolvedPolicy{known: map[string]bool{name: true}}
}

func TestACacheMissLoadsOnceAndLaterRequestsHit(t *testing.T) {
	c := newPolicyCache(4)
	p := policyNamed("a")
	var loads int
	load := func() (*resolvedPolicy, error) { loads++; return p, nil }

	for i := 0; i < 5; i++ {
		got, err := c.get(context.Background(), "k", load, isConfig)
		require.NoError(t, err)
		assert.Same(t, p, got, "every request gets the same immutable policy")
	}
	assert.Equal(t, 1, loads)
	assert.Equal(t, CacheStats{Hits: 4, Misses: 1, Loads: 1, Entries: 1}, c.stats())
}

func TestDifferentKeysAreDifferentEntries(t *testing.T) {
	c := newPolicyCache(4)
	a, b := policyNamed("a"), policyNamed("b")
	ga, _ := c.get(context.Background(), cacheKey("v1", "sum1"), func() (*resolvedPolicy, error) { return a, nil }, isConfig)
	gb, _ := c.get(context.Background(), cacheKey("v2", "sum2"), func() (*resolvedPolicy, error) { return b, nil }, isConfig)
	assert.Same(t, a, ga)
	assert.Same(t, b, gb)
	// The same version id with another checksum never reuses the old entry.
	gc, _ := c.get(context.Background(), cacheKey("v1", "other"), func() (*resolvedPolicy, error) { return b, nil }, isConfig)
	assert.Same(t, b, gc)
	assert.Equal(t, 3, c.stats().Entries)
	assert.Equal(t, "v1|sum1", cacheKey("v1", "sum1"))
}

func TestConcurrentMissesForOneKeyShareASingleLoad(t *testing.T) {
	c := newPolicyCache(4)
	p := policyNamed("a")
	var loads atomic.Int32
	release := make(chan struct{})
	load := func() (*resolvedPolicy, error) {
		loads.Add(1)
		<-release
		return p, nil
	}

	const n = 40
	var wg sync.WaitGroup
	results := make([]*resolvedPolicy, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := c.get(context.Background(), "k", load, isConfig)
			assert.NoError(t, err)
			results[i] = got
		}()
	}
	// Wait until every caller is either the leader or waiting for it, then let the loader finish.
	require.Eventually(t, func() bool { return c.stats().Waits == n-1 }, 5*time.Second, time.Millisecond)
	close(release)
	wg.Wait()

	assert.EqualValues(t, 1, loads.Load(), "one load for forty simultaneous requests")
	for _, r := range results {
		assert.Same(t, p, r)
	}
	s := c.stats()
	assert.EqualValues(t, 1, s.Loads)
	assert.EqualValues(t, n, s.Misses)
	assert.EqualValues(t, n-1, s.Waits)
}

func TestAnOrdinaryLoadErrorIsReturnedToEveryWaiterAndNotStored(t *testing.T) {
	c := newPolicyCache(4)
	boom := errors.New("database is down")
	var loads atomic.Int32
	release := make(chan struct{})
	load := func() (*resolvedPolicy, error) { loads.Add(1); <-release; return nil, boom }

	var wg sync.WaitGroup
	errs := make([]error, 6)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = c.get(context.Background(), "k", load, isConfig)
		}()
	}
	require.Eventually(t, func() bool { return c.stats().Waits == 5 }, 5*time.Second, time.Millisecond)
	close(release)
	wg.Wait()
	for _, err := range errs {
		assert.ErrorIs(t, err, boom)
	}
	assert.EqualValues(t, 1, loads.Load())
	assert.Equal(t, 0, c.stats().Entries, "a transient failure is not remembered")

	// The next request tries again and can succeed.
	p := policyNamed("a")
	got, err := c.get(context.Background(), "k", func() (*resolvedPolicy, error) { return p, nil }, isConfig)
	require.NoError(t, err)
	assert.Same(t, p, got)
}

func TestADeterministicErrorIsStoredSoABrokenVersionCostsOneLoad(t *testing.T) {
	c := newPolicyCache(4)
	var loads int
	load := func() (*resolvedPolicy, error) { loads++; return nil, fmt.Errorf("%w: version v1", errConfig) }

	for i := 0; i < 4; i++ {
		got, err := c.get(context.Background(), "k", load, isConfig)
		assert.Nil(t, got)
		assert.ErrorIs(t, err, errConfig)
	}
	assert.Equal(t, 1, loads)
	assert.Equal(t, 1, c.stats().Entries)
	assert.EqualValues(t, 3, c.stats().Hits)
}

func TestTheLeastRecentlyUsedEntryIsEvicted(t *testing.T) {
	c := newPolicyCache(2)
	loads := map[string]int{}
	get := func(key string) {
		_, err := c.get(context.Background(), key, func() (*resolvedPolicy, error) { loads[key]++; return policyNamed(key), nil }, isConfig)
		require.NoError(t, err)
	}
	get("a")
	get("b")
	get("a") // a is now the most recently used
	get("c") // evicts b
	assert.Equal(t, 2, c.stats().Entries)
	get("a")
	get("b") // loaded again
	assert.Equal(t, map[string]int{"a": 1, "b": 2, "c": 1}, loads)
}

func TestACacheNeverHoldsMoreThanItsBound(t *testing.T) {
	c := newPolicyCache(3)
	for i := 0; i < 50; i++ {
		key := fmt.Sprintf("k%d", i)
		_, err := c.get(context.Background(), key, func() (*resolvedPolicy, error) { return policyNamed(key), nil }, isConfig)
		require.NoError(t, err)
		assert.LessOrEqual(t, c.stats().Entries, 3)
	}
	assert.Equal(t, DefaultCacheEntries, newPolicyCache(0).max, "a non-positive bound means the default")
}

func TestAWaiterTakesOverWhenTheLeaderIsCancelled(t *testing.T) {
	c := newPolicyCache(4)
	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	started := make(chan struct{})
	leaderLoad := func() (*resolvedPolicy, error) {
		close(started)
		<-leaderCtx.Done()
		return nil, leaderCtx.Err() // the request of the leader ended
	}
	leaderErr := make(chan error, 1)
	go func() {
		_, err := c.get(leaderCtx, "k", leaderLoad, isConfig)
		leaderErr <- err
	}()
	<-started

	p := policyNamed("a")
	var waiterLoads atomic.Int32
	waiterDone := make(chan *resolvedPolicy, 1)
	go func() {
		got, err := c.get(context.Background(), "k", func() (*resolvedPolicy, error) { waiterLoads.Add(1); return p, nil }, isConfig)
		assert.NoError(t, err)
		waiterDone <- got
	}()
	require.Eventually(t, func() bool { return c.stats().Waits == 1 }, 5*time.Second, time.Millisecond)
	cancelLeader()

	assert.ErrorIs(t, <-leaderErr, context.Canceled)
	select {
	case got := <-waiterDone:
		assert.Same(t, p, got, "the waiter loaded the policy itself instead of failing with the leader's cancellation")
	case <-time.After(5 * time.Second):
		t.Fatal("the waiter never finished")
	}
	assert.EqualValues(t, 1, waiterLoads.Load())
}

func TestAWaiterWhoseOwnContextEndsStopsWaiting(t *testing.T) {
	c := newPolicyCache(4)
	release := make(chan struct{})
	started := make(chan struct{})
	go func() {
		_, _ = c.get(context.Background(), "k", func() (*resolvedPolicy, error) {
			close(started)
			<-release
			return policyNamed("a"), nil
		}, isConfig)
	}()
	<-started

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := c.get(ctx, "k", func() (*resolvedPolicy, error) { t.Fatal("must not load"); return nil, nil }, isConfig)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	close(release)
}

func TestALoaderThatPanicsReleasesItsWaiters(t *testing.T) {
	c := newPolicyCache(4)
	started := make(chan struct{})
	release := make(chan struct{})
	leaderDone := make(chan any, 1)
	go func() {
		defer func() { leaderDone <- recover() }()
		_, _ = c.get(context.Background(), "k", func() (*resolvedPolicy, error) {
			close(started)
			<-release
			panic("boom")
		}, isConfig)
	}()
	<-started

	waiterErr := make(chan error, 1)
	go func() {
		_, err := c.get(context.Background(), "k", func() (*resolvedPolicy, error) { return policyNamed("x"), nil }, isConfig)
		waiterErr <- err
	}()
	require.Eventually(t, func() bool { return c.stats().Waits == 1 }, 5*time.Second, time.Millisecond)
	close(release)

	assert.Equal(t, "boom", <-leaderDone, "the panic still propagates to the leader")
	select {
	case err := <-waiterErr:
		assert.ErrorIs(t, err, errLoaderPanicked)
	case <-time.After(5 * time.Second):
		t.Fatal("a waiter was left hanging after the loader panicked")
	}
	assert.Equal(t, 0, c.stats().Entries)
	// And the key is usable again.
	got, err := c.get(context.Background(), "k", func() (*resolvedPolicy, error) { return policyNamed("ok"), nil }, isConfig)
	require.NoError(t, err)
	assert.NotNil(t, got)
}

func TestManyGoroutinesAndKeysUnderTheRaceDetector(t *testing.T) {
	c := newPolicyCache(5)
	var loads atomic.Int32
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				key := fmt.Sprintf("k%d", (g+i)%8)
				got, err := c.get(context.Background(), key, func() (*resolvedPolicy, error) { loads.Add(1); return policyNamed(key), nil }, isConfig)
				if assert.NoError(t, err) {
					assert.True(t, got.known[key], "a policy is never served under another key")
				}
			}
		}()
	}
	wg.Wait()
	s := c.stats()
	assert.LessOrEqual(t, s.Entries, 5)
	assert.EqualValues(t, 32*200, s.Hits+s.Misses, "every request is a hit or a miss")
	assert.EqualValues(t, loads.Load(), s.Loads)
}
