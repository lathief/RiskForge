package ratelimit

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTest(perMinute, burst int) (*Limiter, *clock) {
	c := &clock{t: time.Date(2026, 11, 24, 8, 0, 0, 0, time.UTC)}
	l := New("test", perMinute, burst)
	l.now = c.now
	return l, c
}

func TestABurstIsAllowedThenRefused(t *testing.T) {
	l, _ := newTest(60, 3)
	for i := 0; i < 3; i++ {
		ok, _ := l.Allow("a")
		assert.True(t, ok, "request %d", i+1)
	}
	ok, wait := l.Allow("a")
	assert.False(t, ok)
	assert.Equal(t, time.Second, wait, "60 per minute is one token per second")
}

func TestTokensRefillAtTheConfiguredRate(t *testing.T) {
	l, c := newTest(60, 2)
	l.Allow("a")
	l.Allow("a")
	ok, _ := l.Allow("a")
	require.False(t, ok)
	c.advance(999 * time.Millisecond)
	ok, wait := l.Allow("a")
	assert.False(t, ok)
	assert.LessOrEqual(t, wait, 10*time.Millisecond)
	c.advance(2 * time.Millisecond)
	ok, _ = l.Allow("a")
	assert.True(t, ok, "one token after one second")
	c.advance(time.Hour)
	for i := 0; i < 2; i++ {
		ok, _ = l.Allow("a")
		assert.True(t, ok)
	}
	ok, _ = l.Allow("a")
	assert.False(t, ok, "an idle hour refills only up to the burst")
}

func TestClientsDoNotShareABudget(t *testing.T) {
	l, _ := newTest(60, 1)
	ok, _ := l.Allow("a")
	assert.True(t, ok)
	ok, _ = l.Allow("a")
	assert.False(t, ok)
	ok, _ = l.Allow("b")
	assert.True(t, ok)
}

func TestSlowRatesReportALongWait(t *testing.T) {
	l, _ := newTest(6, 1) // one token every ten seconds
	l.Allow("a")
	ok, wait := l.Allow("a")
	assert.False(t, ok)
	assert.Equal(t, 10*time.Second, wait)
}

func TestConfigureCapsExistingTokensAndRaisesTinyValues(t *testing.T) {
	l, _ := newTest(60, 10)
	l.Allow("a")
	l.Configure(0, 0)
	ok, _ := l.Allow("a")
	assert.True(t, ok, "the 9 tokens left were capped to the new burst of 1")
	ok, _ = l.Allow("a")
	assert.False(t, ok)
	l.Reset()
	ok, _ = l.Allow("a")
	assert.True(t, ok, "a reset gives every client a full burst")
}

func TestATableOfActiveClientsStaysBounded(t *testing.T) {
	l, c := newTest(60, 2)
	for i := 0; i < MaxKeys; i++ {
		l.Allow(fmt.Sprint("k", i))
	}
	// Every tracked client has spent a token and none has refilled: nothing can be swept.
	ok, _ := l.Allow("newcomer")
	assert.True(t, ok, "an untracked newcomer is let through")
	assert.Equal(t, uint64(1), l.FullCount())
	assert.Len(t, l.buckets, MaxKeys)
	// After they refill, the sweep frees the table and the newcomer is tracked.
	c.advance(time.Minute)
	ok, _ = l.Allow("newcomer")
	assert.True(t, ok)
	assert.Less(t, len(l.buckets), MaxKeys)
	assert.Contains(t, l.buckets, "newcomer")
}

func TestConcurrentCallsNeverAllowMoreThanTheBurst(t *testing.T) {
	l, _ := newTest(60, 50)
	var allowed int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, _ := l.Allow("same"); ok {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	assert.Equal(t, 50, allowed)
}

func TestEnabledSwitch(t *testing.T) {
	assert.True(t, Enabled())
	SetEnabled(false)
	assert.False(t, Enabled())
	SetEnabled(true)
}
