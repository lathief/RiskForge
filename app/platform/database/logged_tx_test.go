package database

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/goravel/framework/contracts/database/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"goravel/app/platform/logging"
)

// fakeTx records nothing; it returns canned results so logging can be checked in isolation.
type fakeTx struct{ err error }

func (f *fakeTx) Commit() error   { return nil }
func (f *fakeTx) Rollback() error { return nil }
func (f *fakeTx) Delete(string, ...any) (*db.Result, error) {
	return &db.Result{RowsAffected: 2}, f.err
}
func (f *fakeTx) Insert(string, ...any) (*db.Result, error) {
	return &db.Result{RowsAffected: 1}, f.err
}
func (f *fakeTx) Update(string, ...any) (*db.Result, error) {
	return &db.Result{RowsAffected: 3}, f.err
}
func (f *fakeTx) Select(dest any, _ string, _ ...any) error {
	if rows, ok := dest.(*[]int); ok {
		*rows = []int{1, 2, 3, 4}
	}
	return f.err
}
func (f *fakeTx) Statement(string, ...any) error { return f.err }
func (f *fakeTx) Table(string) db.Query          { return nil }

func captureQueries(t *testing.T) (*bytes.Buffer, func()) {
	t.Helper()
	buf := &bytes.Buffer{}
	previous := logging.Default()
	logging.SetDefault(logging.NewWithWriters(logging.Config{AppName: "RiskForge"}, map[string]io.Writer{logging.TypeQuery: buf}))
	return buf, func() { logging.SetDefault(previous) }
}

func queryLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		var m map[string]any
		require.NoError(t, json.Unmarshal(line, &m))
		out = append(out, m)
	}
	return out
}

func TestLoggedTxRecordsEveryStatementWithoutArgumentValues(t *testing.T) {
	buf, restore := captureQueries(t)
	defer restore()

	ctx := context.WithValue(context.Background(), logging.RequestIDKey, "req-q")
	q := &loggedTx{inner: &fakeTx{}, ctx: ctx, module: "catalog", inTx: true}

	var rows []int
	require.NoError(t, q.Select(&rows, "SELECT id FROM t WHERE secret = $1", "hash-value-must-not-be-logged"))
	_, _ = q.Insert("INSERT INTO t VALUES ($1, $2)", "a", "b")
	_, _ = q.Update("UPDATE t SET a = $1", "a")
	_, _ = q.Delete("DELETE FROM t")
	_ = q.Statement("TRUNCATE t")

	got := queryLines(t, buf)
	require.Len(t, got, 5)
	kinds := []string{"select", "insert", "update", "delete", "statement"}
	rowCounts := []float64{4, 1, 3, 2, 0}
	argCounts := []float64{1, 2, 1, 0, 0}
	for i, e := range got {
		assert.Equal(t, kinds[i], e["kind"])
		assert.Equal(t, rowCounts[i], e["rows"], kinds[i])
		assert.Equal(t, argCounts[i], e["args"], kinds[i])
		assert.Equal(t, "catalog", e["module"])
		assert.Equal(t, "req-q", e["requestID"])
		assert.Equal(t, true, e["inTransaction"])
		assert.Contains(t, e, "durationMs")
	}
	assert.NotContains(t, buf.String(), "hash-value-must-not-be-logged")
}

func TestLoggedTxRecordsErrors(t *testing.T) {
	buf, restore := captureQueries(t)
	defer restore()

	q := &loggedTx{inner: &fakeTx{err: errors.New("duplicate key")}, ctx: context.Background(), module: "identity"}
	var rows []int
	assert.Error(t, q.Select(&rows, "SELECT 1"))

	got := queryLines(t, buf)
	require.Len(t, got, 1)
	assert.Equal(t, "error", got[0]["level"])
	assert.Equal(t, "duplicate key", got[0]["error"])
	assert.Equal(t, 0.0, got[0]["rows"])
	assert.Equal(t, false, got[0]["inTransaction"])
}

func TestTransactionEntryLevels(t *testing.T) {
	business := errors.New("product not found")
	assert.Equal(t, "COMMIT", transactionEntry("catalog", nil, nil).SQL)
	assert.Equal(t, "", transactionEntry("catalog", nil, nil).Level, "commit keeps the default level")

	e := transactionEntry("catalog", business, business)
	assert.Equal(t, "ROLLBACK", e.SQL)
	assert.Equal(t, "warn", e.Level, "a business rollback is not an incident")

	commitFailed := errors.New("commit: connection reset")
	assert.Equal(t, "error", transactionEntry("catalog", commitFailed, nil).Level)

	deadline := fmt.Errorf("query: %w", context.DeadlineExceeded)
	assert.Equal(t, "error", transactionEntry("catalog", deadline, deadline).Level)

	panicked := errors.New("panic: boom") // Goravel turns a panic into an error; fn returned nothing
	assert.Equal(t, "error", transactionEntry("catalog", panicked, nil).Level)
}

func TestWithModuleKeepsConnection(t *testing.T) {
	m := NewManager(nil)
	assert.Equal(t, logging.ModulePlatform, m.module)
	c := m.WithModule("catalog")
	assert.Equal(t, "catalog", c.module)
	assert.Equal(t, logging.ModulePlatform, m.module, "original manager unchanged")
}
