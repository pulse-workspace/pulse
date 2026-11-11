// Package testhelpers provides a NATS testcontainer fallback for development tests.
package testhelpers

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/nats"
	"github.com/testcontainers/testcontainers-go/wait"
)

// NATSURL returns a NATS connection URL (nats://host:port) suitable for
// the shared/nats client. Resolution order:
//
//  1. PULSE_TEST_NATS env var (CI docker-compose path) — returned as-is
//  2. PULSE_TEST_INFRA=1 — start a NATS 2.10-alpine testcontainer
//  3. testcontainers.SkipIfProviderIsNotHealthy — docker not available, skip
//  4. Otherwise — t.Skip
func NATSURL(t *testing.T) string {
	t.Helper()

	if url := os.Getenv("PULSE_TEST_NATS"); url != "" {
		return url
	}
	if os.Getenv("PULSE_TEST_INFRA") != "1" {
		t.Skip("set PULSE_TEST_NATS (docker-compose) or PULSE_TEST_INFRA=1 (testcontainers) to run this test")
	}
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	nc, err := nats.Run(ctx,
		"nats:2.10-alpine",
		// ForListeningPort avoids macOS flakes vs log-based wait
		// strategies. WithStartupTimeout keeps cold-image pulls
		// from timing out (Postgres image is ~80 MB, NATS ~20 MB).
		testcontainers.WithWaitStrategy(
			wait.ForListeningPort("4222/tcp").
				WithStartupTimeout(120*time.Second),
		),
	)
	require.NoError(t, err, "start nats testcontainer")

	t.Cleanup(func() {
		termCtx, termCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer termCancel()
		if err := nc.Terminate(termCtx); err != nil {
			t.Logf("nats testcontainer terminate: %v", err)
		}
	})

	url, err := nc.ConnectionString(ctx)
	require.NoError(t, err, "nats testcontainer connection string")
	return url
}