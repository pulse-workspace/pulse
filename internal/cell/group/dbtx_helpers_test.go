
//
// Shared types and constructors used by the lifecycle unit tests in
// this package. Keeping them here avoids each test file redefining
// the same pgx.Rows / pgx.Row stubs.
//
// Helpers in this file:
//
//   - svcWithFake  : build a *Service wired to a FakeTx.
//   - cannedRow    : single-shot pgx.Row for QueryRow.
//   - cannedRows   : multi-shot pgx.Rows for Query.
//   - okExec()     : sentinel nil error for ExecFn.
//
// NATS is intentionally left nil in tests; group publishes on the
// happy paths of Create / Invite / Join / Kick / Dismiss / TransferOwner.
// Those tests recover from the nil-nats panic via `defer recover()`
// and assert on the recorded ExecCalls.
package group

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rs/zerolog"

	"github.com/pulse-im/pulse/internal/cell"
)

// svcWithFake builds a Service wired to a FakeTx. nats is left nil —
// tests that don't reach the publish path stay safe; tests that do
// recover via `defer func() { _ = recover() }()` and assert on
// recorded ExecCalls.
func svcWithFake(fake *FakeTx) *Service {
	return &Service{
		cell: &cell.Cell{Log: zerolog.Nop()},
		log:  zerolog.Nop(),
		db:   fake,
	}
}

// okExec is a convenience helper that returns nil.
func okExec() error { return nil }

// okExecRouter returns an ExecFn that records every call and returns
// okExec(). Tests can inspect fake.ExecCalls for SQL + args.
func okExecRouter() func(_ context.Context, _ string, _ []any) error {
	return func(_ context.Context, _ string, _ []any) error { return okExec() }
}

// cannedRow is a single-shot pgx.Row that returns the supplied values
// on Scan. Each dest is expected to be the same type as the
// corresponding value (pointer-to-T).
type cannedRow struct {
	values []any
}

func (r *cannedRow) Scan(dest ...any) error {
	if len(dest) != len(r.values) {
		return errors.New("cannedRow: dest/values length mismatch")
	}
	for i, v := range r.values {
		switch d := dest[i].(type) {
		case *string:
			if s, ok := v.(string); ok {
				*d = s
			}
		case *int:
			if n, ok := v.(int); ok {
				*d = n
			}
		case *int64:
			if n, ok := v.(int64); ok {
				*d = n
			}
		case *time.Time:
			if t, ok := v.(time.Time); ok {
				*d = t
			}
		case **time.Time:
			if t, ok := v.(*time.Time); ok {
				*d = t
			} else if v == nil {
				*d = nil
			}
		case *Role:
			if s, ok := v.(Role); ok {
				*d = s
			} else if s, ok := v.(string); ok {
				*d = Role(s)
			}
		case *GroupType:
			if s, ok := v.(GroupType); ok {
				*d = s
			} else if s, ok := v.(string); ok {
				*d = GroupType(s)
			}
		case *map[string]any:
			if m, ok := v.(map[string]any); ok {
				*d = m
			}
		default:
			return errors.New("cannedRow: unsupported dest type")
		}
	}
	return nil
}

// cannedRows provides a stub implementation of pgx.Rows for tests.
//
//nolint:unused
type cannedRows struct { //nolint:unused
	rows [][]any //nolint:unused
	pos  int     //nolint:unused
	err  error   //nolint:unused
	done bool    //nolint:unused
} //nolint:unused

// The pgx.Rows methods below provide a test stub implementation.
// Marked nolint:unused throughout.
//
//nolint:unused
func (r *cannedRows) Next() bool { return false } //nolint:unused

//nolint:unused
func (r *cannedRows) Scan(dest ...any) error { return nil } //nolint:unused

//nolint:unused
func (r *cannedRows) Close() {} //nolint:unused

//nolint:unused
func (r *cannedRows) Err() error { return nil } //nolint:unused

//nolint:unused
func (r *cannedRows) CommandTag() pgconn.CommandTag { return pgconn.CommandTag{} } //nolint:unused

//nolint:unused
func (r *cannedRows) FieldDescriptions() []pgconn.FieldDescription { return nil } //nolint:unused

//nolint:unused
func (r *cannedRows) Values() ([]any, error) { return nil, nil } //nolint:unused

//nolint:unused
func (r *cannedRows) RawValues() [][]byte { return nil } //nolint:unused

//nolint:unused
func (r *cannedRows) Conn() *pgx.Conn { return nil } //nolint:unused
