// Package testhelpers: testcontainer fallback for local development.
//

// set (PULSE_TEST_POSTGRES) but PULSE_TEST_INFRA=1 is, these helpers
// spin up ephemeral Postgres containers via testcontainers-go. CI's
// docker-compose service path stays the default — the container path
// is for developers without docker available, or for running
// integration tests on a machine where the docker-compose stack isn't
// up.
//
// All helpers register a t.Cleanup that terminates the container so
// resources are reclaimed even on test failure.
//
// Skipped (not failed) when:
//   - PULSE_TEST_INFRA != "1"
//   - And no PULSE_TEST_POSTGRES env var set
//
// This matches the existing pattern in db_test.go 
// which uses os.Getenv("PULSE_TEST_POSTGRES") to skip when missing.
package testhelpers

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// PostgresDSN returns a Postgres connection string suitable for the
// shared/db Open helper. Resolution order:
//
//  1. PULSE_TEST_POSTGRES env var (CI docker-compose path) — returned as-is
//  2. PULSE_TEST_INFRA=1 — start a Postgres 16-alpine testcontainer
//  3. Otherwise — t.Skip so the test stays runnable without infra
//
// The container path uses port 5432 internally; testcontainers maps
// it to a random free host port and the connection string includes
// sslmode=disable for parity with CI's docker-compose DSN.
func PostgresDSN(t *testing.T) string {
	t.Helper()

	if dsn := os.Getenv("PULSE_TEST_POSTGRES"); dsn != "" {
		return dsn
	}
	if os.Getenv("PULSE_TEST_INFRA") != "1" {
		t.Skip("set PULSE_TEST_POSTGRES (docker-compose) or PULSE_TEST_INFRA=1 (testcontainers) to run this test")
	}
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	pg, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("pulse_test"),
		postgres.WithUsername("pulse"),
		postgres.WithPassword("pulse"),
		// ForListeningPort avoids CI flakes vs log-based wait strategies.
		// WithStartupTimeout keeps cold-image pulls from timing out
		// (Postgres image is ~80 MB, first pull can take 30-60s).
		testcontainers.WithWaitStrategy(
			wait.ForListeningPort("5432/tcp").
				WithStartupTimeout(120*time.Second),
		),
	)
	require.NoError(t, err, "start postgres testcontainer")

	t.Cleanup(func() {
		// Use a fresh context for termination — the test's context may
		// already be canceled if the test failed early.
		termCtx, termCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer termCancel()
		if err := pg.Terminate(termCtx); err != nil {
			t.Logf("postgres testcontainer terminate: %v", err)
		}
	})

	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err, "postgres testcontainer connection string")
	return dsn
}