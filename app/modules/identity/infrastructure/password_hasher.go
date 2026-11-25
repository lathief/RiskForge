package infrastructure

import (
	"context"
	"runtime"
	"time"

	"github.com/goravel/framework/contracts/hash"

	"github.com/lathief/RiskForge/app/modules/identity/application"
)

// PasswordHasher adapts the Goravel hash facade (bcrypt by default, see config/hashing.go)
// and bounds how many password comparisons run at once.
//
// bcrypt is CPU-bound and ignores context cancellation, so a burst of logins could occupy
// every core and keep running after the request has timed out. A counting semaphore caps
// concurrency; a caller that cannot get a slot within queueTimeout (or before ctx ends)
// fails fast with application.ErrBusy instead of queueing indefinitely.
type PasswordHasher struct {
	hash         hash.Hash
	slots        chan struct{}
	queueTimeout time.Duration
}

var _ application.PasswordHasher = (*PasswordHasher)(nil)

// NewPasswordHasher creates the hasher. maxConcurrent <= 0 means runtime.NumCPU().
func NewPasswordHasher(h hash.Hash, maxConcurrent int, queueTimeout time.Duration) *PasswordHasher {
	if maxConcurrent <= 0 {
		maxConcurrent = runtime.NumCPU()
	}
	return &PasswordHasher{
		hash:         h,
		slots:        make(chan struct{}, maxConcurrent),
		queueTimeout: queueTimeout,
	}
}

// Make is used for one-off hashing (seeding, the timing-equalizer hash) and is not limited.
func (h *PasswordHasher) Make(plain string) (string, error) {
	return h.hash.Make(plain)
}

func (h *PasswordHasher) Check(ctx context.Context, plain, hashed string) (bool, error) {
	if err := h.acquire(ctx); err != nil {
		return false, err
	}
	defer func() { <-h.slots }()

	if hashed == "" {
		return false, nil
	}
	return h.hash.Check(plain, hashed), nil
}

func (h *PasswordHasher) acquire(ctx context.Context) error {
	select {
	case h.slots <- struct{}{}:
		return nil
	default:
	}

	timer := time.NewTimer(h.queueTimeout)
	defer timer.Stop()
	select {
	case h.slots <- struct{}{}:
		return nil
	case <-timer.C:
		return application.ErrBusy
	case <-ctx.Done():
		return ctx.Err()
	}
}
