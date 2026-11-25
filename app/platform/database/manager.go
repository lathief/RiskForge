// Package database is the Goravel/PostgreSQL adapter for the transaction port.
// Repository adapters must obtain their query handle from Manager.Querier so that every
// write in one business operation uses the same transaction, and so that every statement
// is recorded in query.log with the repository's module.
package database

import (
	"context"
	"errors"
	"time"

	"github.com/goravel/framework/contracts/database/db"

	"github.com/lathief/RiskForge/app/platform/logging"
	"github.com/lathief/RiskForge/app/platform/tx"
)

type txKey struct{}

// Manager implements tx.Manager and hands out the correct query handle to repositories.
type Manager struct {
	base   db.DB
	module string
}

var _ tx.Manager = (*Manager)(nil)

func NewManager(base db.DB) *Manager {
	return &Manager{base: base, module: logging.ModulePlatform}
}

// WithModule returns a manager whose queries are labelled with module in query.log.
// It shares the connection and joins the same transactions as the original.
func (m *Manager) WithModule(module string) *Manager {
	return &Manager{base: m.base, module: module}
}

// Querier returns the transaction carried by ctx, or a plain connection bound to ctx when
// there is none. Repositories must never use the default facade connection directly.
func (m *Manager) Querier(ctx context.Context) db.Tx {
	if t, ok := ctx.Value(txKey{}).(db.Tx); ok {
		return &loggedTx{inner: t, ctx: ctx, module: m.module, inTx: true}
	}
	return &loggedTx{inner: m.base.WithContext(ctx), ctx: ctx, module: m.module}
}

// WithinTransaction runs fn in a database transaction.
//
// Goravel converts a panic inside fn into a returned error after rolling back, so a panic
// never commits and never escapes this method. Errors returned by fn are returned unchanged
// after rollback. The outcome (commit or rollback) and total duration go to query.log.
func (m *Manager) WithinTransaction(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := ctx.Value(txKey{}).(db.Tx); ok {
		return fn(ctx)
	}

	start := time.Now()
	var fnErr error
	err := m.base.WithContext(ctx).Transaction(func(t db.Tx) error {
		fnErr = fn(context.WithValue(ctx, txKey{}, t))
		return fnErr
	})
	logging.Default().Query(ctx, transactionEntry(m.module, err, fnErr), time.Since(start), err)
	return err
}

// transactionEntry describes a transaction outcome for query.log. A rollback caused by the
// business operation (validation, conflict, not found) is "warn": the failing statement, if
// any, is logged on its own. A failure of BEGIN or COMMIT, a database deadline, or a panic
// has no other error-level line, so it is "error".
func transactionEntry(module string, err, fnErr error) logging.QueryEntry {
	e := logging.QueryEntry{Module: module, Kind: "transaction", SQL: "COMMIT", InTransaction: true}
	if err == nil {
		return e
	}
	e.SQL = "ROLLBACK"
	business := fnErr != nil && errors.Is(err, fnErr) &&
		!errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled)
	if business {
		e.Level = "warn"
	} else {
		e.Level = "error"
	}
	return e
}
