// Package ratelimit is an in-memory token bucket limiter keyed by a client (a user id or an IP
// address) (RF-023, SYSTEM_DESIGN.md 15.1). State is per process: with several instances each
// one allows its own budget, so the effective limit is the budget times the instance count.
package ratelimit

import (
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// MaxKeys bounds the number of clients one limiter tracks.
const MaxKeys = 100000

type bucket struct {
	tokens float64
	last   time.Time
}

// Limiter allows `perMinute` requests per minute per key on average, with bursts up to `burst`.
type Limiter struct {
	name string
	now  func() time.Time

	mu        sync.Mutex
	rate      float64 // tokens per second
	burst     float64
	buckets   map[string]*bucket
	lastSweep time.Time
	full      atomic.Uint64
}

// New returns a limiter. perMinute and burst below 1 are raised to 1.
func New(name string, perMinute, burst int) *Limiter {
	l := &Limiter{name: name, now: time.Now, buckets: map[string]*bucket{}}
	l.Configure(perMinute, burst)
	return l
}

// Name is the label of the limiter in metrics and logs.
func (l *Limiter) Name() string { return l.name }

// Configure changes the limits. Buckets already tracked keep their tokens, capped to the new burst.
func (l *Limiter) Configure(perMinute, burst int) {
	if perMinute < 1 {
		perMinute = 1
	}
	if burst < 1 {
		burst = 1
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rate = float64(perMinute) / 60
	l.burst = float64(burst)
	for _, b := range l.buckets {
		b.tokens = math.Min(b.tokens, l.burst)
	}
}

// Reset forgets every client, so each starts with a full burst.
func (l *Limiter) Reset() {
	l.mu.Lock()
	l.buckets = map[string]*bucket{}
	l.mu.Unlock()
}

// FullCount is the number of requests let through untracked because the table was full.
func (l *Limiter) FullCount() uint64 { return l.full.Load() }

// Allow takes one token for key. When none is left it returns false and how long until one is.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= MaxKeys {
			l.sweep(now)
		}
		if len(l.buckets) >= MaxKeys {
			// Every tracked client is active. Memory stays bounded; the newcomer is not tracked.
			l.full.Add(1)
			return true, 0
		}
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens = math.Min(l.burst, b.tokens+elapsed*l.rate)
		b.last = now
	}
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := time.Duration(math.Ceil((1-b.tokens)/l.rate*1000)) * time.Millisecond
	return false, wait
}

// sweep drops clients whose bucket has refilled completely: they are the same as new clients.
// It runs at most once a second, with the lock held.
func (l *Limiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < time.Second {
		return
	}
	l.lastSweep = now
	for k, b := range l.buckets {
		if b.tokens+now.Sub(b.last).Seconds()*l.rate >= l.burst {
			delete(l.buckets, k)
		}
	}
}

var enabled atomic.Bool

func init() { enabled.Store(true) }

// SetEnabled turns every limiter on or off. The API sets it from RATE_LIMIT_ENABLED at start.
// Tests that are not about limiting turn it off.
func SetEnabled(v bool) { enabled.Store(v) }

// Enabled reports whether limiters are active.
func Enabled() bool { return enabled.Load() }
