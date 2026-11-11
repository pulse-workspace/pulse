package events

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewEvent(t *testing.T) {
	e := NewEvent("user:alice", TypeUserCreated, "admin", []byte(`{"x":1}`))
	require.NotEmpty(t, e.ID)
	require.Equal(t, "user:alice", e.Stream)
	require.Equal(t, TypeUserCreated, e.Type)
	require.Equal(t, "admin", e.Actor)
	require.Equal(t, []byte(`{"x":1}`), e.Data)
	require.WithinDuration(t, time.Now().UTC(), e.Timestamp, 1*time.Second)
	require.Equal(t, int64(0), e.Version, "Version set by Append, not by New")
}

func TestEventTypes(t *testing.T) {
	// Just make sure the canonical event types are set as expected strings.
	require.Equal(t, "UserCreated", TypeUserCreated)
	require.Equal(t, "GroupCreated", TypeGroupCreated)
	require.Equal(t, "GroupMemberAdded", TypeGroupMemberAdded)
	require.Equal(t, "GroupMemberRemoved", TypeGroupMemberRemoved)
	require.Equal(t, "GroupOwnerTransferred", TypeGroupOwnerTransferred)
	require.Equal(t, "GroupInfoUpdated", TypeGroupInfoUpdated)
	require.Equal(t, "MessageSent", TypeMessageSent)
	require.Equal(t, "MessageRecalled", TypeMessageRecalled)
	require.Equal(t, "MessageRead", TypeMessageRead)
	require.Equal(t, "ReactionAdded", TypeReactionAdded)
	require.Equal(t, "ReactionRemoved", TypeReactionRemoved)
	require.Equal(t, "PresenceUpdated", TypePresenceUpdated)
}

func TestEventTypeOrder(t *testing.T) {
	// All event types should be distinct strings (catch copy-paste bugs).
	all := []string{
		TypeUserCreated, TypeUserUpdated, TypeUserDeleted,
		TypeGroupCreated, TypeGroupDismissed,
		TypeGroupMemberAdded, TypeGroupMemberRemoved, TypeGroupOwnerTransferred, TypeGroupInfoUpdated,
		TypeMemberAdded, TypeMemberRemoved, TypeMemberRoleChanged,
		TypeConvCreated, TypeConvUpdated,
		TypeMessageSent, TypeMessageRecalled, TypeMessageRead,
		TypeReactionAdded, TypeReactionRemoved,
		TypePresenceUpdated,
	}
	seen := make(map[string]bool, len(all))
	for _, et := range all {
		require.False(t, seen[et], "duplicate event type: %q", et)
		seen[et] = true
	}
}
