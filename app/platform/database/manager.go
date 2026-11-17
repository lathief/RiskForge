// Package database is the Goravel/PostgreSQL adapter for the transaction port.
// Repository adapters must obtain their query handle from Manager.Querier so that every
// write in one business operation uses the same transaction.
package database

import (
	"context"

	"github.com/goravel/framework/contracts/database/db"

	"goravel/app/platform/tx"
)

type txKey struct{}

// Manager implements tx.Manager and hands out the correct query handle to repositories.
type Manager struct {
	base db.DB
}

var _ tx.Manager = (*Manager)(nil)

func NewManager(base db.DB) *Manager {
	return &Manager{base: base}
}

// Querier returns the transaction carried by ctx, or a plain connection bound to ctx when
// there is none. Repositories must never use the default facade connection directly.
func (m *Manager) Querier(ctx context.Context) db.Tx {
	if t, ok := ctx.Value(txKey{}).(db.Tx); ok {
		return t
	}
	return m.base.WithContext(ctx)
}

// WithinTransaction runs fn in a database transaction.
//
// Goravel converts a panic inside fn into a returned error after rolling back, so a panic
// never commits and never escapes this method. Errors returned by fn are returned unchanged
// after rollback.
func (m *Manager) WithinTransaction(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := ctx.Value(txKey{}).(db.Tx); ok {
		return fn(ctx)
	}

	return m.base.WithContext(ctx).Transaction(func(t db.Tx) error {
		return fn(context.WithValue(ctx, txKey{}, t))
	})
}
