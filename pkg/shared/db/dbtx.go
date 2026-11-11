// Package db provides database connection pooling and transaction interfaces.
package db

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// DBTX is the minimum Postgres surface domain services depend on for
// unit tests. Production wiring passes *DB (the per-cell wrapper);
// unit tests pass a *FakeTx that records calls and returns canned
// responses.
//
// Signatures match *DB (NOT raw *pgxpool.Pool) so the wrapper
// itself satisfies the interface and production code does not need
// to change. Exec returns error only — the wrapper drops the
// CommandTag because no domain service currently consumes it.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) error
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Compile-time guard: *DB (the per-cell wrapper) must satisfy DBTX.
// If db.go changes a signature we want a build error here, not a
// runtime surprise inside cmd/pulse/main.go.
var _ DBTX = (*DB)(nil)