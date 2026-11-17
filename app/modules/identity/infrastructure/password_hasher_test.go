package infrastructure

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"goravel/app/modules/identity/application"
)

// blockingHash implements hash.Hash. Check blocks until release is closed and records
// the highest number of concurrent calls.
type blockingHash struct {
	release  chan struct{}
	inFlight atomic.Int32
	peak     atomic.Int32
}

func (b *blockingHash) Make(v string) (string, error) { return "h:" + v, nil }
func (b *blockingHash) NeedsRehash(string) bool       { return false }
func (b *blockingHash) Check(v, h string) bool {
	n := b.inFlight.Add(1)
	for {
		p := b.peak.Load()
		if n <= p || b.peak.CompareAndSwap(p, n) {
			break
		}
	}
	<-b.release
	b.inFlight.Add(-1)
	return h == "h:"+v
}

func TestCheckNeverExceedsMaxConcurrent(t *testing.T) {
	inner := &blockingHash{release: make(chan struct{})}
	hasher := NewPasswordHasher(inner, 2, 5*time.Second)

	var wg sync.WaitGroup
	results := make([]bool, 6)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ok, err := hasher.Check(context.Background(), "pw", "h:pw")
			assert.NoError(t, err)
			results[i] = ok
		}(i)
	}

	require.Eventually(t, func() bool { return inner.inFlight.Load() == 2 }, time.Second, 5*time.Millisecond)
	time.Sleep(50 * time.Millisecond) // give extra goroutines a chance to (wrongly) enter
	assert.Equal(t, int32(2), inner.inFlight.Load())

	close(inner.release)
	wg.Wait()
	assert.Equal(t, int32(2), inner.peak.Load())
	for _, ok := range results {
		assert.True(t, ok)
	}
}

func TestCheckFailsFastWithErrBusyWhenSaturated(t *testing.T) {
	inner := &blockingHash{release: make(chan struct{})}
	defer close(inner.release)
	hasher := NewPasswordHasher(inner, 1, 30*time.Millisecond)

	go func() { _, _ = hasher.Check(context.Background(), "pw", "h:pw") }()
	require.Eventually(t, func() bool { return inner.inFlight.Load() == 1 }, time.Second, 5*time.Millisecond)

	start := time.Now()
	ok, err := hasher.Check(context.Background(), "pw", "h:pw")
	assert.ErrorIs(t, err, application.ErrBusy)
	assert.False(t, ok)
	assert.Less(t, time.Since(start), 500*time.Millisecond)
}

func TestCheckStopsWaitingWhenContextEnds(t *testing.T) {
	inner := &blockingHash{release: make(chan struct{})}
	defer close(inner.release)
	hasher := NewPasswordHasher(inner, 1, 5*time.Second)

	go func() { _, _ = hasher.Check(context.Background(), "pw", "h:pw") }()
	require.Eventually(t, func() bool { return inner.inFlight.Load() == 1 }, time.Second, 5*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := hasher.Check(ctx, "pw", "h:pw")
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestSlotIsReleasedAfterEachCheck(t *testing.T) {
	inner := &blockingHash{release: make(chan struct{})}
	close(inner.release)
	hasher := NewPasswordHasher(inner, 1, 10*time.Millisecond)

	for i := 0; i < 5; i++ {
		ok, err := hasher.Check(context.Background(), "pw", "h:pw")
		require.NoError(t, err, "iteration %d", i)
		assert.True(t, ok)
	}
	ok, err := hasher.Check(context.Background(), "pw", "")
	require.NoError(t, err)
	assert.False(t, ok, "empty hash never matches")
}

func TestZeroMaxConcurrentUsesCPUCount(t *testing.T) {
	hasher := NewPasswordHasher(&blockingHash{}, 0, time.Second)
	assert.Greater(t, cap(hasher.slots), 0)
}
