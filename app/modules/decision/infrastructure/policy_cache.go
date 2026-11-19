package infrastructure

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

// DefaultCacheEntries bounds the compiled policies kept in memory. Entries are keyed by an
// immutable (version id, checksum) pair, so the number of keys that can ever be asked for is
// the number of published versions; the bound only protects memory if that grows.
const DefaultCacheEntries = 64

// CacheStats counts what the cache did. Counters only grow; Entries is the current size.
type CacheStats struct {
	Hits    uint64 // answered from memory
	Misses  uint64 // not in memory: this caller loaded, or waited for the caller that did
	Loads   uint64 // times a loader ran
	Waits   uint64 // callers that waited for a loader that was already running
	Entries int
}

// cacheEntry is a stored result: a compiled policy, or the error that compiling it gave. Only
// deterministic errors are stored (see policyCache.get).
type cacheEntry struct {
	key    string
	policy *resolvedPolicy
	err    error
}

// inflight is a load in progress that other callers can wait for.
type inflight struct {
	done   chan struct{}
	policy *resolvedPolicy
	err    error
}

// policyCache is a small LRU of compiled policies with single-flight loading (SYSTEM_DESIGN.md
// 10.6): a missing entry is loaded and compiled once, however many requests ask for it at the
// same time, and the result is shared. A frozen policy version never changes, so an entry never
// has to be invalidated: a publish moves the binding to another version, which is another key.
type policyCache struct {
	mu       sync.Mutex
	max      int
	order    *list.List // front = most recently used; values are *cacheEntry
	items    map[string]*list.Element
	inflight map[string]*inflight

	hits, misses, loads, waits atomic.Uint64
}

func newPolicyCache(max int) *policyCache {
	if max < 1 {
		max = DefaultCacheEntries
	}
	return &policyCache{max: max, order: list.New(), items: map[string]*list.Element{}, inflight: map[string]*inflight{}}
}

// errLoaderPanicked is what waiting callers get when the loader they waited for panicked.
var errLoaderPanicked = errors.New("policy loader panicked")

// get returns the compiled policy for key, calling load on a miss.
//
// load runs in the goroutine of the caller that found the miss (the leader), with that
// caller's context, because loading reads through the caller's database transaction. Other
// callers for the same key wait for the leader. If the leader fails because its own context
// ended while a waiter's context is still alive, the waiter tries again, so one cancelled
// request cannot fail the others.
//
// cacheable says whether a load error is a property of the policy version (for example a
// snapshot that cannot be compiled) and therefore will be the same next time. Such an error
// is stored, so a broken version costs one load, not one per request. Any other error (a
// database failure, a cancelled context) is returned to the waiting callers and not stored.
func (c *policyCache) get(ctx context.Context, key string, load func() (*resolvedPolicy, error), cacheable func(error) bool) (*resolvedPolicy, error) {
	for {
		c.mu.Lock()
		if el, ok := c.items[key]; ok {
			c.order.MoveToFront(el)
			e := el.Value.(*cacheEntry)
			c.mu.Unlock()
			c.hits.Add(1)
			return e.policy, e.err
		}
		if call, ok := c.inflight[key]; ok {
			c.mu.Unlock()
			c.waits.Add(1)
			select {
			case <-call.done:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			if call.err != nil && isContextError(call.err) && ctx.Err() == nil {
				continue // the leader gave up; take over
			}
			c.misses.Add(1)
			return call.policy, call.err
		}
		call := &inflight{done: make(chan struct{})}
		c.inflight[key] = call
		c.mu.Unlock()

		c.misses.Add(1)
		return c.lead(key, call, load, cacheable)
	}
}

// lead runs the loader for a key this caller owns and publishes the result. The deferred
// block always releases the waiters, even if the loader panics.
func (c *policyCache) lead(key string, call *inflight, load func() (*resolvedPolicy, error), cacheable func(error) bool) (policy *resolvedPolicy, err error) {
	c.loads.Add(1)
	finished := false
	defer func() {
		if !finished {
			call.err = errLoaderPanicked
		}
		c.mu.Lock()
		delete(c.inflight, key)
		if finished && (call.err == nil || cacheable(call.err)) {
			c.store(&cacheEntry{key: key, policy: call.policy, err: call.err})
		}
		c.mu.Unlock()
		close(call.done)
	}()
	call.policy, call.err = load()
	finished = true
	return call.policy, call.err
}

// store adds an entry and evicts the least recently used one when the cache is full. The
// caller holds the lock.
func (c *policyCache) store(e *cacheEntry) {
	if el, ok := c.items[e.key]; ok {
		el.Value = e
		c.order.MoveToFront(el)
		return
	}
	c.items[e.key] = c.order.PushFront(e)
	for c.order.Len() > c.max {
		oldest := c.order.Back()
		c.order.Remove(oldest)
		delete(c.items, oldest.Value.(*cacheEntry).key)
	}
}

func (c *policyCache) stats() CacheStats {
	c.mu.Lock()
	entries := c.order.Len()
	c.mu.Unlock()
	return CacheStats{Hits: c.hits.Load(), Misses: c.misses.Load(), Loads: c.loads.Load(), Waits: c.waits.Load(), Entries: entries}
}

func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// cacheKey is the immutable identity of a frozen version: its id and the checksum of its
// snapshot. Both are in the key so that, even if a row were ever replaced, a different
// snapshot could never be served under an old key.
func cacheKey(versionID, checksum string) string {
	return fmt.Sprintf("%s|%s", versionID, checksum)
}
