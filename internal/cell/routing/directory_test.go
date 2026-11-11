package routing

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMemoryDirectory_RegisterAndLookup(t *testing.T) {
	ctx := context.Background()
	dir := NewMemoryDirectory()

	// Alice registers on cell-0
	err := dir.Register(ctx, "alice", "sess_a1", "cell-0")
	require.NoError(t, err)

	// Alice registers another device on cell-1
	err = dir.Register(ctx, "alice", "sess_a2", "cell-1")
	require.NoError(t, err)

	cells, err := dir.Lookup(ctx, "alice")
	require.NoError(t, err)
	require.Equal(t, []string{"cell-0", "cell-1"}, cells)

	// Bob is offline
	bobCells, err := dir.Lookup(ctx, "bob")
	require.NoError(t, err)
	require.Empty(t, bobCells)

	// LookupParticipants (alice + bob)
	partCells, err := dir.LookupParticipants(ctx, []string{"alice", "bob"})
	require.NoError(t, err)
	require.Equal(t, []string{"cell-0", "cell-1"}, partCells)

	// Unregister alice sess_a1
	err = dir.Unregister(ctx, "alice", "sess_a1")
	require.NoError(t, err)

	cells, err = dir.Lookup(ctx, "alice")
	require.NoError(t, err)
	require.Equal(t, []string{"cell-1"}, cells)

	// Unregister alice sess_a2
	err = dir.Unregister(ctx, "alice", "sess_a2")
	require.NoError(t, err)

	cells, err = dir.Lookup(ctx, "alice")
	require.NoError(t, err)
	require.Empty(t, cells)
}

func TestKeyForUserSanitization(t *testing.T) {
	require.Equal(t, "usr.alice", keyForUser("alice"))
	require.Equal(t, "usr.u_01HXYZ123", keyForUser("u_01HXYZ123"))
	require.Equal(t, "usr.user_domain.com", keyForUser("user@domain.com"))
}

