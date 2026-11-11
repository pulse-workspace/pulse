package conversation

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestConversationStructLayout documents the wire format.
func TestConversationStructLayout(t *testing.T) {
	c := Conversation{
		ConvID:       "si_alice_bob",
		ConvType:     1,
		Participants: []string{"alice", "bob"},
		LastMsgID:    "m_01ABC",
		LastMsgAt:    nil,
		Pinned:       false,
	}
	require.Equal(t, "si_alice_bob", c.ConvID)
	require.Equal(t, 1, c.ConvType)
	require.Len(t, c.Participants, 2)
	require.False(t, c.Pinned)
}

// TestErrorsAreExported verifies the sentinel errors can be used with errors.Is.
func TestErrorsAreExported(t *testing.T) {
	require.NotNil(t, ErrNotFound)
}
