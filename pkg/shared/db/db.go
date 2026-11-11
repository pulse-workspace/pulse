// Package db wraps pgxpool with Pulse-specific helpers.
package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB is the per-cell database connection pool.
type DB struct {
	pool *pgxpool.Pool
}

// Pool exposes the underlying pgxpool.Pool for callers that need
// raw access (e.g. migration tools that need to run arbitrary SQL
// including multi-statement scripts).
func (d *DB) Pool() *pgxpool.Pool { return d.pool }

// Open connects to Postgres and returns a ready pool.
func Open(ctx context.Context, dsn string, maxConns int) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	if maxConns > 0 {
		cfg.MaxConns = int32(maxConns)
	}
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.HealthCheckPeriod = 1 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}

	return &DB{pool: pool}, nil
}

// Close closes the pool.
func (d *DB) Close() {
	if d.pool != nil {
		d.pool.Close()
	}
}

// WithTx runs fn inside a transaction, rolling back on error.
func (d *DB) WithTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := d.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Exec runs a single statement.
func (d *DB) Exec(ctx context.Context, sql string, args ...any) error {
	_, err := d.pool.Exec(ctx, sql, args...)
	return err
}

// QueryRow runs a query returning a single row.
func (d *DB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return d.pool.QueryRow(ctx, sql, args...)
}

// Query runs a query returning multiple rows.
func (d *DB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return d.pool.Query(ctx, sql, args...)
}
