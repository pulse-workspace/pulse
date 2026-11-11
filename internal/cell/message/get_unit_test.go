
package message

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
)

func TestGetMessage_NotFound_ReturnsErrNotFound(t *testing.T) {
	fake := &FakeTx{
		QueryRowFn: func(_ context.Context, _ string, _ []any) pgx.Row {
			return &fakeRow{err: pgx.ErrNoRows}
		},
	}
	s := svcWithFake(fake)
	got, err := s.GetMessage(context.Background(), "m_missing")
	require.Nil(t, got)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestGetMessage_ScanError_Propagates(t *testing.T) {
	// pgx.ErrNoRows is mapped to ErrNotFound; any other scan error
	// must propagate verbatim so callers see the real failure.
	scanErr := errors.New("type mismatch")
	fake := &FakeTx{
		QueryRowFn: func(_ context.Context, _ string, _ []any) pgx.Row {
			return &fakeRow{err: scanErr}
		},
	}
	s := svcWithFake(fake)
	got, err := s.GetMessage(context.Background(), "m_x")
	require.Nil(t, got)
	require.ErrorIs(t, err, scanErr)
}

func TestGetHistory_DefaultsLimit(t *testing.T) {
	// limit=0 or limit>200 → service applies a 50 default. Assert
	// by inspecting the args slice passed to Query.
	var sawLimit int64 = -1
	fake := &FakeTx{
		QueryFn: func(_ context.Context, _ string, args []any) (pgx.Rows, error) {
			if len(args) >= 2 {
				if n, ok := args[1].(int); ok {
					sawLimit = int64(n)
				}
			}
			return &cannedRows{rows: nil}, nil
		},
	}
	s := svcWithFake(fake)
	_, err := s.GetHistory(context.Background(), "si_a_b", 0)
	require.NoError(t, err)
	require.Equal(t, int64(50), sawLimit, "limit=0 must be clamped to 50")
}

// TestGetHistory_QueryError_Propagates: A failure on the initial
// Query call must surface — e.g. connection refused before any rows
// are read. Defends the early-return branch on send.go:125.
func TestGetHistory_QueryError_Propagates(t *testing.T) {
	queryErr := errors.New("connection refused")
	fake := &FakeTx{
		QueryFn: func(_ context.Context, _ string, _ []any) (pgx.Rows, error) {
			return nil, queryErr
		},
	}
	s := svcWithFake(fake)
	got, err := s.GetHistory(context.Background(), "si_a_b", 10)
	require.Nil(t, got)
	require.ErrorIs(t, err, queryErr)
}

// TestGetHistory_ScanError_Propagates: rows.Scan failure on any
// iterated row must surface — operators must see real type-mismatch
// / DB-corruption issues, not silently truncate results.
func TestGetHistory_ScanError_Propagates(t *testing.T) {
	scanErr := errors.New("type mismatch")
	fake := &FakeTx{
		QueryFn: func(_ context.Context, _ string, _ []any) (pgx.Rows, error) {
			return &scanErrRows{err: scanErr}, nil
		},
	}
	s := svcWithFake(fake)
	got, err := s.GetHistory(context.Background(), "si_a_b", 10)
	require.Nil(t, got)
	require.ErrorIs(t, err, scanErr)
}

// TestGetHistory_RowsErr_Propagates: When rows.Err() reports a
// non-EOF error after iteration, GetHistory must surface it. Covers
// the post-loop branch on send.go:139 — a real failure mode when
// the connection drops mid-iteration.
func TestGetHistory_RowsErr_Propagates(t *testing.T) {
	iterErr := errors.New("connection dropped mid-iteration")
	fake := &FakeTx{
		QueryFn: func(_ context.Context, _ string, _ []any) (pgx.Rows, error) {
			return &iterErrRows{err: iterErr}, nil
		},
	}
	s := svcWithFake(fake)
	got, err := s.GetHistory(context.Background(), "si_a_b", 10)
	require.Empty(t, got)
	require.ErrorIs(t, err, iterErr)
}

// --- Minimal Rows stubs for error-path testing. --------------------------

// scanErrRows is a pgx.Rows that returns a fixed error on every Scan
// call. Next() always returns true so the SUT enters the Scan branch.
type scanErrRows struct {
	err error
}

func (r *scanErrRows) Next() bool                    { return true }
func (r *scanErrRows) Scan(_ ...any) error           { return r.err }
func (r *scanErrRows) Close()                        {}
func (r *scanErrRows) Err() error                    { return nil }
func (r *scanErrRows) CommandTag() pgconn.CommandTag { return pgconn.CommandTag{} }
func (r *scanErrRows) FieldDescriptions() []pgconn.FieldDescription {
	return nil
}
func (r *scanErrRows) Values() ([]any, error) { return nil, nil }
func (r *scanErrRows) RawValues() [][]byte    { return nil }
func (r *scanErrRows) Conn() *pgx.Conn        { return nil }

// iterErrRows is a pgx.Rows that reports a fixed error via Err() and
// signals end-of-iteration immediately. Covers the post-loop
// rows.Err() branch.
type iterErrRows struct {
	err error
}

func (r *iterErrRows) Next() bool                    { return false }
func (r *iterErrRows) Scan(_ ...any) error           { return nil }
func (r *iterErrRows) Close()                        {}
func (r *iterErrRows) Err() error                    { return r.err }
func (r *iterErrRows) CommandTag() pgconn.CommandTag { return pgconn.CommandTag{} }
func (r *iterErrRows) FieldDescriptions() []pgconn.FieldDescription {
	return nil
}
func (r *iterErrRows) Values() ([]any, error) { return nil, nil }
func (r *iterErrRows) RawValues() [][]byte    { return nil }
func (r *iterErrRows) Conn() *pgx.Conn        { return nil }
