
//
// FakeTx is a minimal in-memory stand-in for db.DBTX used by unit
// tests that need to exercise the message service's SQL paths without
// a live Postgres. It records every Exec/Query/QueryRow call and
// returns canned responses via the configured functions.
//
// Capabilities:
//   - RecordExec / RecordQuery / RecordQueryRow install responses.
//   - LastExec / ExecCalls / etc. let tests assert on what was called.
//   - For QueryRow, results are scanned by the SUT into caller-supplied
//     destinations; the fake just returns the pre-canned Row.
//
// Limitations (intentional):
//   - No transaction support (DBTX interface omits Begin).
//   - No actual SQL parsing — responses are installed by code, not by
//     SQL matching. If the SUT issues an unexpected SQL, the test
//     fails with a clear "no canned response for SQL" error.
//   - No Rows iteration semantics — use cannedRows for that path.
package message

import (
	"context"
	"errors"
	"sync"

	"github.com/jackc/pgx/v5"
)

// errNoCannedResponse is returned when the SUT issues a SQL the test
// didn't install a response for. This is the desired failure mode —
// silently returning zero values would mask wiring bugs.
var errNoCannedResponse = errors.New("fakeTx: no canned response installed for SQL")

// FakeTx is the in-memory db.DBTX stand-in. Safe for concurrent use
// because production code may run on multiple goroutines; tests should
// however pin single-goroutine call sequences when asserting on
// order.
//
// Exec signature matches db.DBTX (returns error only — *DB drops the
// CommandTag because no domain service consumes it).
type FakeTx struct {
	mu sync.Mutex

	// ExecFn returns err for Exec calls. nil → errNoCannedResponse.
	ExecFn func(ctx context.Context, sql string, args []any) error

	// QueryFn returns (rows, err) for Query calls. nil → errNoCannedResponse.
	QueryFn func(ctx context.Context, sql string, args []any) (pgx.Rows, error)

	// QueryRowFn returns a pgx.Row for QueryRow calls. nil → empty row
	// whose Scan returns errNoCannedResponse.
	QueryRowFn func(ctx context.Context, sql string, args []any) pgx.Row

	// Calls recorded for assertions.
	ExecCalls     []FakeCall
	QueryCalls    []FakeCall
	QueryRowCalls []FakeCall
}

// FakeCall captures one call to Exec / Query / QueryRow for assertions.
type FakeCall struct {
	SQL  string
	Args []any
}

// Compile-time guard: FakeTx must satisfy db.DBTX (the seam interface).
// We import db indirectly here — handled by the test file's package
// dependency on cell/message. The interface check lives in service
// tests rather than here to keep the fake decoupled from db's export.

func (f *FakeTx) Exec(ctx context.Context, sql string, args ...any) error {
	f.mu.Lock()
	// Copy args to avoid aliasing when the SUT mutates its slice.
	cp := append([]any(nil), args...)
	f.ExecCalls = append(f.ExecCalls, FakeCall{SQL: sql, Args: cp})
	fn := f.ExecFn
	f.mu.Unlock()
	if fn == nil {
		return errNoCannedResponse
	}
	return fn(ctx, sql, args)
}

func (f *FakeTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	f.mu.Lock()
	cp := append([]any(nil), args...)
	f.QueryCalls = append(f.QueryCalls, FakeCall{SQL: sql, Args: cp})
	fn := f.QueryFn
	f.mu.Unlock()
	if fn == nil {
		return nil, errNoCannedResponse
	}
	return fn(ctx, sql, args)
}

func (f *FakeTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	f.mu.Lock()
	cp := append([]any(nil), args...)
	f.QueryRowCalls = append(f.QueryRowCalls, FakeCall{SQL: sql, Args: cp})
	fn := f.QueryRowFn
	f.mu.Unlock()
	if fn == nil {
		return &fakeRow{err: errNoCannedResponse}
	}
	return fn(ctx, sql, args)
}

// fakeRow is the pgx.Row returned when no QueryRowFn is configured.
type fakeRow struct {
	err error
}

func (r *fakeRow) Scan(_ ...any) error { return r.err }
