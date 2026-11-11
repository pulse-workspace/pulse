package reaction

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestReactionStructLayout(t *testing.T) {
	r := Reaction{
		MsgID:     "m_01ABC",
		UserID:    "alice",
		Type:      "+1",
		ConvID:    "si_alice_bob",
		CreatedAt: time.Now().UTC(),
	}
	require.Equal(t, "m_01ABC", r.MsgID)
	require.Equal(t, "alice", r.UserID)
	require.Equal(t, "+1", r.Type)
	require.NotZero(t, r.CreatedAt)
}

func TestErrNotFoundExported(t *testing.T) {
	require.NotNil(t, ErrNotFound)
}
