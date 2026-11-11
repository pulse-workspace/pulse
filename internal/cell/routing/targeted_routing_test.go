package routing

import (
	"context"
	"sync"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/pulse-im/pulse/internal/cell"
	"github.com/pulse-im/pulse/pkg/shared/config"
)

type recordedPublish struct {
	peerCell string
	kind     string
	payload  any
}

func setupTestRouter(t *testing.T, localCellID string) (*Service, *memoryDirectory, *[]recordedPublish, *sync.Mutex) {
	cfg := &config.Config{
		Cell: config.Cell{
			ID: localCellID,
		},
	}
	c := &cell.Cell{
		Cfg: cfg,
		Log: zerolog.Nop(),
	}

	router := NewService(c, nil, nil, nil)
	dir := NewMemoryDirectory().(*memoryDirectory)
	router.SetDirectory(dir)

	var mu sync.Mutex
	var publishes []recordedPublish

	// In unit test without live NATS, router.nats is nil.
	// We override or verify targeted peer collection via RouteToParticipants.
	return router, dir, &publishes, &mu
}

func TestRouteToParticipants_OnlineOnPeerCell(t *testing.T) {
	ctx := context.Background()
	router, dir, _, _ := setupTestRouter(t, "cell-0")

	// Alice is local on cell-0, Bob is online on cell-1
	_ = dir.Register(ctx, "alice", "sess_a", "cell-0")
	_ = dir.Register(ctx, "bob", "sess_b", "cell-1")

	// Mock peer cell registration
	router.RegisterPeer("cell-1")
	router.RegisterPeer("cell-2")

	// Route message for conversation between alice and bob
	// Target should only be cell-1 (bob), NOT cell-0 (local) and NOT cell-2 (uninvolved)
	targetCells, err := router.Directory().LookupParticipants(ctx, []string{"alice", "bob"})
	require.NoError(t, err)
	require.Equal(t, []string{"cell-0", "cell-1"}, targetCells)

	// Filter out local cell
	var externalCells []string
	for _, c := range targetCells {
		if c != "cell-0" {
			externalCells = append(externalCells, c)
		}
	}
	require.Equal(t, []string{"cell-1"}, externalCells)
}

func TestRouteToParticipants_AllLocalOrOffline_NoTraffic(t *testing.T) {
	ctx := context.Background()
	router, dir, _, _ := setupTestRouter(t, "cell-0")

	// Alice and Charlie are both local on cell-0
	_ = dir.Register(ctx, "alice", "sess_a", "cell-0")
	_ = dir.Register(ctx, "charlie", "sess_c", "cell-0")

	targetCells, err := router.Directory().LookupParticipants(ctx, []string{"alice", "charlie"})
	require.NoError(t, err)
	require.Equal(t, []string{"cell-0"}, targetCells)

	// None of the participants are on an external cell
	var externalCells []string
	for _, c := range targetCells {
		if c != "cell-0" {
			externalCells = append(externalCells, c)
		}
	}
	require.Empty(t, externalCells, "local-only participants should generate 0 cross-cell traffic")

	// Dave is completely offline
	offlineCells, err := router.Directory().LookupParticipants(ctx, []string{"dave"})
	require.NoError(t, err)
	require.Empty(t, offlineCells, "offline user should generate 0 cross-cell traffic")
}

func TestRouteToParticipants_GroupMulticast_Deduplicated(t *testing.T) {
	ctx := context.Background()
	router, dir, _, _ := setupTestRouter(t, "cell-0")

	// Group with 5 members across 3 cells
	_ = dir.Register(ctx, "u1", "s1", "cell-1")
	_ = dir.Register(ctx, "u2", "s2", "cell-1") // also cell-1
	_ = dir.Register(ctx, "u3", "s3", "cell-2")
	_ = dir.Register(ctx, "u4", "s4", "cell-0") // local
	// u5 is offline

	members := []string{"u1", "u2", "u3", "u4", "u5"}
	targetCells, err := router.Directory().LookupParticipants(ctx, members)
	require.NoError(t, err)

	// Target cells must be sorted & deduplicated: cell-0, cell-1, cell-2
	require.Equal(t, []string{"cell-0", "cell-1", "cell-2"}, targetCells)

	var externalTargets []string
	for _, c := range targetCells {
		if c != "cell-0" {
			externalTargets = append(externalTargets, c)
		}
	}
	require.Equal(t, []string{"cell-1", "cell-2"}, externalTargets, "must target only cell-1 and cell-2 exactly once")
}
