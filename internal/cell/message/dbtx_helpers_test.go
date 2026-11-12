
//
// Shared types and constructors used by the topic-specific test
// files in this package. Keeping them here avoids each test file
// redefining the same pgx.Rows stub.
//
// Helpers in this file:
//
//   - svcWithFake  : build a *Service wired to a FakeTx.
//   - cannedRow    : single-shot pgx.Row for QueryRow.
//   - cannedRows   : multi-shot pgx.Rows for Query.
//   - okExec()     : sentinel nil error for ExecFn.
//
// NATS is intentionally left nil in tests; the production code paths
// recover from publish failures with a log warning, so callers must
// either (a) avoid the publish path, (b) defer-recover the panic,
// or (c) accept that the test exercises code up to the publish.
package message

import (
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rs/zerolog"

	"github.com/pulse-workspace/pulse/internal/cell"
)

// svcWithFake builds a Service wired to a FakeTx. nats is left nil —
// tests that don't reach the publish path stay safe (recall.go:33
// logs a warning on publish failure, not a panic).
//

// every test gets a fresh cache. Tests that want to assert on
// cache behaviour use svcWithCache; this helper is the default.
func svcWithFake(fake *FakeTx) *Service {
	return &Service{
		cell:    &cell.Cell{Log: zerolog.Nop()},
		log:     zerolog.Nop(),
		db:      fake,
		cache:   newRecentCache(100), // small cache for tests
		convSeq: make(map[string]int64),
	}
}

// okExec is a convenience helper that returns nil — used in tests
// that wire ExecFn as `func(_,_,_) error { return nil }`.
func okExec() error { return nil }

// cannedRow is a single-shot pgx.Row that returns the supplied
// values on Scan. Each dest is expected to be the same type as the
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
		case *map[string]any:
			if m, ok := v.(map[string]any); ok {
				*d = m
			}
		case *int:
			if n, ok := v.(int); ok {
				*d = n
			}
		default:
			return errors.New("cannedRow: unsupported dest type")
		}
	}
	return nil
}

// cannedRows is a minimal pgx.Rows implementation backed by a slice
// of scanned-destinations. The simplest implementation re-uses
// pgx.Rows's interface but stubs every method.
type cannedRows struct {
	rows [][]any // each inner slice is the args to one Scan
	pos  int
	err  error
	done bool
}

func (r *cannedRows) Next() bool {
	if r.done {
		return false
	}
	r.pos++
	return r.pos <= len(r.rows)
}

func (r *cannedRows) Scan(dest ...any) error {
	if r.pos < 1 || r.pos > len(r.rows) {
		return errors.New("cannedRows: scan past end")
	}
	row := r.rows[r.pos-1]
	if len(dest) != len(row) {
		return errors.New("cannedRows: dest/row length mismatch")
	}
	for i, v := range row {
		switch d := dest[i].(type) {
		case *string:
			if s, ok := v.(string); ok {
				*d = s
			}
		case *int64:
			if n, ok := v.(int64); ok {
				*d = n
			}
		case *int:
			if n, ok := v.(int); ok {
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
		case *map[string]any:
			if m, ok := v.(map[string]any); ok {
				*d = m
			}
		default:
			return errors.New("cannedRows: unsupported dest type")
		}
	}
	return nil
}

func (r *cannedRows) Close()                        { r.done = true }
func (r *cannedRows) Err() error                    { return r.err }
func (r *cannedRows) CommandTag() pgconn.CommandTag { return pgconn.CommandTag{} }
func (r *cannedRows) FieldDescriptions() []pgconn.FieldDescription {
	return nil
}
func (r *cannedRows) Values() ([]any, error) { return nil, nil }
func (r *cannedRows) RawValues() [][]byte    { return nil }
func (r *cannedRows) Conn() *pgx.Conn        { return nil }
