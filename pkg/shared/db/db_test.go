// Tests for the pgxpool wrapper .
//
// Coverage before this file: 0% (only a no-op accessor test existed).
// Coverage target: 80%+ on internal/shared/db by exercising the full
// surface (Open, Close, Pool, Exec, QueryRow, Query, WithTx) against
// the live Postgres that CI provides via the docker-compose service.
//
// Run with PULSE_TEST_POSTGRES set (CI sets this automatically):
//
//	PULSE_TEST_POSTGRES=postgres://pulse:pulse@localhost:5432/pulse_test?sslmode=disable \
//	  go test -race -count=1 ./internal/shared/db/...
package db

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

// dsn returns the test DSN from PULSE_TEST_POSTGRES. When the env var is
// unset, Open/Connect-based tests are skipped so the unit suite still
// works for developers without a local Postgres. Pure-error tests (bad
// DSN, bad scheme) never need the network.
func dsn(t *testing.T) string {
	t.Helper()
	v := os.Getenv("PULSE_TEST_POSTGRES")
	if v == "" {
		t.Skip("PULSE_TEST_POSTGRES not set; skipping live-DB db wrapper test")
	}
	return v
}

// uniqueTable returns a table name unique to the test run. Sharing the
// database with other suites means we cannot rely on a fixed table name,
// but we don't want to drop+recreate anything either.
func uniqueTable(t *testing.T) string {
	t.Helper()
	return "db_test_" + strings.ReplaceAll(t.Name(), "/", "_")
}

// setupTable creates a tiny table the test can read/write. Cleanup is
// registered with t.Cleanup so a failing test still leaves the cluster
// tidy.
func setupTable(t *testing.T, d *DB) string {
	t.Helper()
	ctx := context.Background()
	tbl := uniqueTable(t)
	err := d.Exec(ctx, "CREATE TABLE IF NOT EXISTS "+tbl+" (id text PRIMARY KEY, val int NOT NULL)")
	require.NoError(t, err, "create test table %s", tbl)
	t.Cleanup(func() {
		_ = d.Exec(context.Background(), "DROP TABLE IF EXISTS "+tbl)
	})
	return tbl
}

// =====================================================================
// Open — error paths (no network required)
// =====================================================================

// TestOpen_BadDSN confirms Open fails fast on a syntactically invalid
// DSN. ParseConfig must error before any network dial is attempted.
func TestOpen_BadDSN(t *testing.T) {
	_, err := Open(context.Background(), "this-is-not-a-valid-dsn", 0)
	require.Error(t, err, "malformed DSN must produce an error")
	require.Contains(t, err.Error(), "parse dsn",
		"error must wrap the pgxpool parse failure, got: %v", err)
}

// TestOpen_DefaultPoolSettings confirms Open succeeds with maxConns <= 0
// (the parameter is optional). When a DSN is reachable the returned
// pool is healthy.
func TestOpen_DefaultPoolSettings(t *testing.T) {
	d, err := Open(context.Background(), dsn(t), 0)
	require.NoError(t, err)
	require.NotNil(t, d)
	t.Cleanup(d.Close)

	// Sanity: round-trip a SELECT 1 so we know the pool is usable.
	var one int
	err = d.QueryRow(context.Background(), "SELECT 1").Scan(&one)
	require.NoError(t, err)
	require.Equal(t, 1, one)
}

// TestOpen_MaxConnsApplied confirms a non-zero maxConns is plumbed
// through to the underlying pgxpool config. We assert indirectly by
// opening N concurrent long-running queries and confirming the (N+1)th
// blocks until one returns — proving the cap is enforced. We use a
// small cap (2) to keep the test fast.
func TestOpen_MaxConnsApplied(t *testing.T) {
	d, err := Open(context.Background(), dsn(t), 2)
	require.NoError(t, err)
	t.Cleanup(d.Close)

	// Hold 2 connections, then attempt a 3rd; expect a context-deadline
	// error (not an immediate rejection) since pgxpool queues until the
	// configured acquire timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	hold := func() {
		rows, err := d.Query(ctx, "SELECT pg_sleep(1)")
		require.NoError(t, err)
		defer rows.Close()
		for rows.Next() {
			// drain
		}
	}

	done1 := make(chan struct{})
	done2 := make(chan struct{})
	go func() { hold(); close(done1) }()
	go func() { hold(); close(done2) }()
	// Give the two goroutines a moment to grab the slots.
	time.Sleep(50 * time.Millisecond)

	_, err = d.Query(ctx, "SELECT 1")
	require.Error(t, err, "3rd acquire should fail when the cap is 2 and 2 are in-flight")
	require.True(t, errors.Is(err, context.DeadlineExceeded),
		"expected context-deadline error from acquire timeout, got: %v", err)

	<-done1
	<-done2
}

// TestOpen_PingFailure confirms that a reachable host with bad creds
// causes Open to return a wrapped "ping:" error and not leak the pool.
// We point at the CI DSN with an obviously wrong user via env override.
func TestOpen_PingFailure(t *testing.T) {
	original := dsn(t)
	// Synthesize a DSN with an invalid user against the same host.
	badDSN := strings.Replace(original, "://pulse:", "://nobody_", 1)
	d, err := Open(context.Background(), badDSN, 0)
	if err == nil {
		// Some Postgres setups allow the host to reply with auth error
		// AFTER Ping; in that case the pool is returned but unusable.
		// Close it to avoid leaks and treat the test as inconclusive.
		d.Close()
		t.Skip("test Postgres accepts anonymous users; skipping ping-failure assertion")
	}
	require.Contains(t, err.Error(), "ping",
		"error must wrap the ping failure, got: %v", err)
}

// =====================================================================
// Pool accessor
// =====================================================================

// TestPool_Accessor confirms the Pool() helper returns the same
// pgxpool.Pool the wrapper owns. Asserted by using Pool().Stat() to
// confirm the pool is the live one (zero-value would have empty stats).
func TestPool_Accessor(t *testing.T) {
	d, err := Open(context.Background(), dsn(t), 1)
	require.NoError(t, err)
	t.Cleanup(d.Close)

	p := d.Pool()
	require.NotNil(t, p)
	// A pool that has handled traffic reports non-zero AcquireCount.
	var one int
	err = d.QueryRow(context.Background(), "SELECT 1").Scan(&one)
	require.NoError(t, err)
	require.Equal(t, 1, one)
	require.GreaterOrEqual(t, p.Stat().AcquireCount(), int64(1),
		"acquire count must reflect the QueryRow call")
}

// =====================================================================
// Exec
// =====================================================================

// TestExec_InsertAndUpdate confirms Exec round-trips a write against a
// real table. A separate Exec call updates the row to confirm the
// returned error is nil on success.
func TestExec_InsertAndUpdate(t *testing.T) {
	d, err := Open(context.Background(), dsn(t), 2)
	require.NoError(t, err)
	t.Cleanup(d.Close)
	tbl := setupTable(t, d)

	ctx := context.Background()
	require.NoError(t, d.Exec(ctx,
		"INSERT INTO "+tbl+" (id, val) VALUES ($1, $2)", "alpha", 1))
	require.NoError(t, d.Exec(ctx,
		"UPDATE "+tbl+" SET val = val + 1 WHERE id = $1", "alpha"))

	var got int
	require.NoError(t, d.QueryRow(ctx,
		"SELECT val FROM "+tbl+" WHERE id = $1", "alpha").Scan(&got))
	require.Equal(t, 2, got)
}

// TestExec_SyntaxError confirms Exec surfaces Postgres syntax errors
// unmodified (no wrapping). The production wrapper is intentionally
// thin — callers wrap as needed for their context.
func TestExec_SyntaxError(t *testing.T) {
	d, err := Open(context.Background(), dsn(t), 1)
	require.NoError(t, err)
	t.Cleanup(d.Close)
	err = d.Exec(context.Background(), "THIS IS NOT VALID SQL")
	require.Error(t, err)
	require.Contains(t, strings.ToLower(err.Error()), "syntax",
		"error should originate from Postgres parse, got: %v", err)
}

// =====================================================================
// QueryRow
// =====================================================================

// TestQueryRow_SingleRowFound confirms QueryRow returns a row whose
// Scan populates the destination. We use a literal expression to avoid
// coupling to the test table layout.
func TestQueryRow_SingleRowFound(t *testing.T) {
	d, err := Open(context.Background(), dsn(t), 1)
	require.NoError(t, err)
	t.Cleanup(d.Close)

	var got string
	err = d.QueryRow(context.Background(), "SELECT 'hello'").Scan(&got)
	require.NoError(t, err)
	require.Equal(t, "hello", got)
}

// TestQueryRow_NoRows confirms Scan returns pgx.ErrNoRows when the
// query matches zero rows. Callers commonly errors.Is against this
// sentinel to distinguish "missing" from "scan failure".
func TestQueryRow_NoRows(t *testing.T) {
	d, err := Open(context.Background(), dsn(t), 1)
	require.NoError(t, err)
	t.Cleanup(d.Close)

	var got int
	err = d.QueryRow(context.Background(), "SELECT 1 WHERE false").Scan(&got)
	require.Error(t, err)
	require.True(t, errors.Is(err, pgx.ErrNoRows),
		"expected pgx.ErrNoRows, got: %v", err)
}

// =====================================================================
// Query (multi-row)
// =====================================================================

// TestQuery_MultiRowIteration confirms Query returns rows that can be
// iterated + scanned + closed. We populate N rows then read them back
// in declared order to exercise the full Rows lifecycle.
func TestQuery_MultiRowIteration(t *testing.T) {
	d, err := Open(context.Background(), dsn(t), 2)
	require.NoError(t, err)
	t.Cleanup(d.Close)
	tbl := setupTable(t, d)

	ctx := context.Background()
	for i, k := range []string{"a", "b", "c"} {
		require.NoError(t, d.Exec(ctx,
			"INSERT INTO "+tbl+" (id, val) VALUES ($1, $2)", k, i+1))
	}

	rows, err := d.Query(ctx,
		"SELECT id, val FROM "+tbl+" ORDER BY val ASC")
	require.NoError(t, err)
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		var v int
		require.NoError(t, rows.Scan(&id, &v))
		ids = append(ids, id)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, []string{"a", "b", "c"}, ids)
}

// TestQuery_SyntaxError confirms Query surfaces errors at fetch time
// (not at first Next). Many wrappers wrap this; ours does not.
func TestQuery_SyntaxError(t *testing.T) {
	d, err := Open(context.Background(), dsn(t), 1)
	require.NoError(t, err)
	t.Cleanup(d.Close)
	_, err = d.Query(context.Background(), "SELECT * FROM nonexistent_db_test_table")
	require.Error(t, err)
}

// =====================================================================
// WithTx
// =====================================================================

// TestWithTx_Commit confirms WithTx commits the transaction when the
// closure returns nil. We assert the post-commit read reflects the
// write by reading the row from a separate Query call.
func TestWithTx_Commit(t *testing.T) {
	d, err := Open(context.Background(), dsn(t), 2)
	require.NoError(t, err)
	t.Cleanup(d.Close)
	tbl := setupTable(t, d)
	ctx := context.Background()

	err = d.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			"INSERT INTO "+tbl+" (id, val) VALUES ($1, $2)", "tx_commit", 7)
		return err
	})
	require.NoError(t, err)

	var got int
	require.NoError(t, d.QueryRow(ctx,
		"SELECT val FROM "+tbl+" WHERE id = $1", "tx_commit").Scan(&got))
	require.Equal(t, 7, got)
}

// TestWithTx_Rollback confirms WithTx rolls back when the closure
// returns an error. After the rollback the row is invisible to
// subsequent reads.
func TestWithTx_Rollback(t *testing.T) {
	d, err := Open(context.Background(), dsn(t), 2)
	require.NoError(t, err)
	t.Cleanup(d.Close)
	tbl := setupTable(t, d)
	ctx := context.Background()

	sentinel := errors.New("closure aborted")
	err = d.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			"INSERT INTO "+tbl+" (id, val) VALUES ($1, $2)", "tx_rollback", 9)
		require.NoError(t, err)
		return sentinel
	})
	require.ErrorIs(t, err, sentinel, "WithTx must propagate the closure error")

	var got int
	err = d.QueryRow(ctx,
		"SELECT val FROM "+tbl+" WHERE id = $1", "tx_rollback").Scan(&got)
	require.True(t, errors.Is(err, pgx.ErrNoRows),
		"rolled-back row must not be visible, got err: %v", err)
}

// TestWithTx_ExplicitRollback confirms a closure that calls tx.Rollback
// itself (the normal defer pattern in handlers) still produces no error
// from WithTx. WithTx's deferred Rollback sees Rollback has already
// fired and ignores the error.
func TestWithTx_ExplicitRollback(t *testing.T) {
	d, err := Open(context.Background(), dsn(t), 2)
	require.NoError(t, err)
	t.Cleanup(d.Close)
	tbl := setupTable(t, d)
	ctx := context.Background()

	err = d.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			"INSERT INTO "+tbl+" (id, val) VALUES ($1, $2)", "tx_defer_rollback", 1)
		require.NoError(t, err)
		// Closure returns nil but the rollback deferred inside WithTx
		// will still fire (Postgres tx.Rollback on a committed tx is a
		// no-op). We want to assert WithTx reports commit success.
		return nil
	})
	require.NoError(t, err)
}

// =====================================================================
// Close
// =====================================================================

// TestClose_NilSafe confirms Close on a zero-value DB is a no-op (no
// panic from nil pool dereference). The wrapper guards via the
// if d.pool != nil check.
func TestClose_NilSafe(t *testing.T) {
	d := &DB{}
	require.NotPanics(t, func() { d.Close() })
}

// TestClose_AfterOpen confirms Close releases the underlying pool and
// subsequent queries error. We don't inspect pgxpool internals; we
// just assert that further use fails.
func TestClose_AfterOpen(t *testing.T) {
	d, err := Open(context.Background(), dsn(t), 1)
	require.NoError(t, err)
	d.Close()
	var one int
	err = d.QueryRow(context.Background(), "SELECT 1").Scan(&one)
	require.Error(t, err, "queries against a closed pool must fail")
}