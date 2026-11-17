package database

import (
	"context"
	"reflect"
	"time"

	"github.com/goravel/framework/contracts/database/db"

	"goravel/app/platform/logging"
)

// loggedTx wraps a Goravel query handle and records every raw SQL statement in query.log.
//
// Only the raw SQL methods are timed. Table() returns Goravel's query builder unwrapped;
// repositories in this project use raw parameterized SQL, so this is documented rather than
// silently assumed (see AGENT_RUNBOOK.md).
type loggedTx struct {
	inner  db.Tx
	ctx    context.Context
	module string
	inTx   bool
}

var _ db.Tx = (*loggedTx)(nil)

func (t *loggedTx) Select(dest any, sql string, args ...any) error {
	start := time.Now()
	err := t.inner.Select(dest, sql, args...)
	t.log("select", sql, len(args), resultRows(dest, err), start, err)
	return err
}

func (t *loggedTx) Insert(sql string, args ...any) (*db.Result, error) {
	return t.exec("insert", t.inner.Insert, sql, args)
}

func (t *loggedTx) Update(sql string, args ...any) (*db.Result, error) {
	return t.exec("update", t.inner.Update, sql, args)
}

func (t *loggedTx) Delete(sql string, args ...any) (*db.Result, error) {
	return t.exec("delete", t.inner.Delete, sql, args)
}

func (t *loggedTx) Statement(sql string, args ...any) error {
	start := time.Now()
	err := t.inner.Statement(sql, args...)
	t.log("statement", sql, len(args), 0, start, err)
	return err
}

func (t *loggedTx) Table(name string) db.Query { return t.inner.Table(name) }
func (t *loggedTx) Commit() error              { return t.inner.Commit() }
func (t *loggedTx) Rollback() error            { return t.inner.Rollback() }

func (t *loggedTx) exec(kind string, fn func(string, ...any) (*db.Result, error), sql string, args []any) (*db.Result, error) {
	start := time.Now()
	res, err := fn(sql, args...)
	var rows int64
	if res != nil {
		rows = res.RowsAffected
	}
	t.log(kind, sql, len(args), rows, start, err)
	return res, err
}

func (t *loggedTx) log(kind, sql string, args int, rows int64, start time.Time, err error) {
	logging.Default().Query(t.ctx, logging.QueryEntry{
		Module:        t.module,
		Kind:          kind,
		SQL:           sql,
		Args:          args,
		Rows:          rows,
		InTransaction: t.inTx,
	}, time.Since(start), err)
}

// resultRows counts the rows a Select produced: the slice length, or 1 for a struct target.
func resultRows(dest any, err error) int64 {
	if err != nil {
		return 0
	}
	v := reflect.Indirect(reflect.ValueOf(dest))
	if v.Kind() == reflect.Slice {
		return int64(v.Len())
	}
	return 1
}
