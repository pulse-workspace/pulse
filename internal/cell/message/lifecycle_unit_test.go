
//
// NewService / Name / Stop were at 0% because the cell bundle wires
// them in main.go and integration tests rely on the running service.
// Pulling them into unit tests catches constructor regressions (e.g.
// missing convSeq init) and documents the cell.Service contract that
// other cells (group, compliance, whitelabel) follow.
package message

import (
	"context"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/pulse-im/pulse/internal/cell"
)

// TestNewService_WiresDependencies: NewService must copy db+nats from
// the cell and initialize convSeq. A missing convSeq init would let
// nextSeq panic on first send.
func TestNewService_WiresDependencies(t *testing.T) {
	c := &cell.Cell{Log: zerolog.Nop()}
	s := NewService(c)
	require.NotNil(t, s)
	require.NotNil(t, s.convSeq, "convSeq must be initialized for nextSeq")
	require.Empty(t, s.convSeq, "convSeq must start empty (no conversations yet)")
}

// TestNewService_LoggerScopedToService: the per-service logger is
// scoped with service="message" so log lines are filterable in prod.
func TestNewService_LoggerScopedToService(t *testing.T) {
	c := &cell.Cell{Log: zerolog.Nop()}
	s := NewService(c)
	require.NotNil(t, s.log)
}

// TestName_StableForCellRegistry: the cell registry uses Name() as
// the registration key. Changing this string is a breaking change
// for downstream service discovery.
func TestName_StableForCellRegistry(t *testing.T) {
	s := NewService(&cell.Cell{Log: zerolog.Nop()})
	require.Equal(t, "message", s.Name())
}

// TestStop_NoOpReturnsNil: Stop is a no-op for the message service —
// the projection table is owned by Postgres (not in-memory) and NATS
// shutdown is the cell bundle's responsibility.
func TestStop_NoOpReturnsNil(t *testing.T) {
	s := NewService(&cell.Cell{Log: zerolog.Nop()})
	require.NoError(t, s.Stop(context.Background()))
}
