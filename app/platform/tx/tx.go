// Package tx defines the framework-agnostic transaction port used by application use cases.
// The Goravel/PostgreSQL adapter lives in app/platform/database.
package tx

import "context"

// Manager runs a unit of work atomically.
//
// Every repository call made with the context passed to fn takes part in the same
// database transaction. If fn returns an error or panics, all of its writes are rolled back.
// Calling WithinTransaction with a context that already carries a transaction joins that
// transaction instead of opening a new one.
type Manager interface {
	WithinTransaction(ctx context.Context, fn func(ctx context.Context) error) error
}
