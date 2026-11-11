package presence

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEntryStructLayout(t *testing.T) {
	e := Entry{
		UserID:    "alice",
		Status:    StatusOnline,
		LastSeen:  time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	require.Equal(t, "alice", e.UserID)
	require.Equal(t, StatusOnline, e.Status)
}

func TestStatusEnum(t *testing.T) {
	require.Equal(t, Status("online"), StatusOnline)
	require.Equal(t, Status("offline"), StatusOffline)
	require.Equal(t, Status("away"), StatusAway)
}
