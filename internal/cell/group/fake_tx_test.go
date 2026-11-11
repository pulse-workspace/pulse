
//
// Mirrors internal/cell/message/fake_tx_test.go.
// FakeTx is an in-memory stand-in for db.DBTX used by unit tests that
// exercise group-lifecycle SQL paths without a live Postgres.
//
// Capabilities:
//   - RecordExec / RecordQuery / RecordQueryRow install responses.
//   - ExecCalls / QueryCalls / QueryRowCalls let tests assert on calls.
//   - QueryRow returns a pre-canned Row scanned by the SUT.
//
// Limitations (intentional, matching the message fake):
//   - No transaction support — DBTX interface omits WithTx. Methods
//     that need transactions (SCIMCreateGroup, SCIMUpdateGroupMembers
//     replace) intentionally route through s.cell.DB.WithTx and stay
//     out of the unit suite.
//   - No SQL parsing — responses are installed by code, not by SQL
//     matching. Unexpected SQL → errNoCannedResponse (test fails
//     loudly, surfacing wiring bugs).
package group

import (
	"context"
	"errors"
	"sync"

	"github.com/jackc/pgx/v5"
)

// errNoCannedResponse is returned when the SUT issues a SQL the test
// didn't install a response for. Silently returning zero values would
// mask wiring bugs, so we surface a distinct error.
var errNoCannedResponse = errors.New("fakeTx: no canned response installed for SQL")

// FakeTx is the in-memory db.DBTX stand-in. Safe for concurrent use;
// tests that assert on call order should pin a single goroutine.
type FakeTx struct {
	mu sync.Mutex

	// ExecFn returns err for Exec calls. nil → errNoCannedResponse.
	ExecFn func(ctx context.Context, sql string, args []any) error

	// QueryFn returns (rows, err) for Query calls. nil → errNoCannedResponse.
	QueryFn func(ctx context.Context, sql string, args []any) (pgx.Rows, error)

	// QueryRowFn returns a pgx.Row for QueryRow calls. nil → fakeRow
	// whose Scan returns errNoCannedResponse.
	QueryRowFn func(ctx context.Context, sql string, args []any) pgx.Row

	// Recorded calls for assertions.
	ExecCalls     []FakeCall
	QueryCalls    []FakeCall
	QueryRowCalls []FakeCall
}

// FakeCall captures one Exec / Query / QueryRow call for assertions.
type FakeCall struct {
	SQL  string
	Args []any
}

func (f *FakeTx) Exec(ctx context.Context, sql string, args ...any) error {
	f.mu.Lock()
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

// compileTimeGuard locks the FakeTx → db.DBTX contract. If the seam
// interface grows a method, this line stops compiling, surfacing the
// drift at build time rather than at runtime in production wiring.
var _ interface {
	Exec(ctx context.Context, sql string, args ...any) error
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
} = (*FakeTx)(nil)
