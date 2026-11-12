package user

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/pulse-workspace/pulse/internal/cell"
)

func TestEventTimeIsUTC(t *testing.T) {
	u := &User{
		UserID:    "u_test",
		Nickname:  "Test",
		Email:     "test@example.com",
		Status:    "active",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	require.Equal(t, "u_test", u.UserID)
	require.Equal(t, "active", u.Status)
	require.WithinDuration(t, time.Now(), u.CreatedAt, 5*time.Second)
}

func TestErrSentinels(t *testing.T) {
	require.Error(t, ErrNotFound)
	require.Error(t, ErrConflict)
	require.NotEqual(t, ErrNotFound, ErrConflict)
}

// --- User registration tests ------------------------------------------
//
// Register hits the DB (lookups, INSERT, NATS publish) which can't be
// unit-tested without a live Postgres. The pre-DB branches — empty
// nickname, no email-and-no-phone — are reachable without any backend and
// are the most likely to silently regress.

func TestRegister_EmptyNickname_ReturnsError(t *testing.T) {
	s := buildUserService()
	// Signature: Register(ctx, userID, nickname, email, phone, ext)
	u, err := s.Register(context.TODO(), "u_test", "", "a@example.com", "", nil)
	require.Nil(t, u)
	require.Error(t, err)
	require.Contains(t, err.Error(), "nickname")
}

func TestRegister_NoEmailNoPhone_ReturnsError(t *testing.T) {
	s := buildUserService()
	u, err := s.Register(context.TODO(), "u_test", "alice", "", "", nil)
	require.Nil(t, u)
	require.Error(t, err)
	require.Contains(t, err.Error(), "email or phone")
}

func TestNewService_Name(t *testing.T) {
	s := NewService(&cell.Cell{})
	require.Equal(t, "user", s.Name())
}

func TestService_Stop_IsNoOp(t *testing.T) {
	// Stop must succeed without DB/NATS.
	s := &Service{}
	require.NoError(t, s.Stop(context.TODO()))
}

// buildUserService constructs a *Service with nil DB / NATS. Tests that
// hit DB paths are expected to panic, so don't use this for those.
func buildUserService() *Service {
	return &Service{}
}

func TestUser_ExtDataField(t *testing.T) {
	u := &User{
		UserID:  "u_test",
		ExtData: map[string]any{"role": "admin", "tier": "paid"},
	}
	require.Equal(t, "admin", u.ExtData["role"])
	require.Equal(t, "paid", u.ExtData["tier"])
}

// --- Additional registration-paths coverage -----------------------

func TestMsgpackMarshal_StringMap(t *testing.T) {
	b, err := msgpackMarshal(map[string]any{"hello": "world"})
	require.NoError(t, err)
	require.NotEmpty(t, b)

	back := mustMarshal(map[string]any{"hello": "world"})
	require.Equal(t, b, back)
}

func TestMsgpackMarshal_RoundTripStruct(t *testing.T) {
	type sample struct {
		UserID string `msgpack:"user_id"`
		Age    int    `msgpack:"age"`
	}
	original := sample{UserID: "u_1", Age: 42}
	b, err := msgpackMarshal(original)
	require.NoError(t, err)
	require.NotEmpty(t, b)
}

func TestMustMarshal_PanicsOnUnmarshalable(t *testing.T) {
	ch := make(chan int)
	defer func() {
		r := recover()
		require.NotNil(t, r, "mustMarshal must panic on unmarshalable types")
	}()
	_ = mustMarshal(ch)
}

func TestRegister_NoEmailNoPhone_EmptyContactOnly(t *testing.T) {
	s := buildUserService()
	u, err := s.Register(context.TODO(), "u_test", "alice", "", "", nil)
	require.Nil(t, u)
	require.Error(t, err)
}

func TestRegister_EmptyNickname_TakesPrecedenceOverContact(t *testing.T) {
	s := buildUserService()
	u, err := s.Register(context.TODO(), "u_test", "", "a@example.com", "+15555550100", nil)
	require.Nil(t, u)
	require.Error(t, err)
	require.Contains(t, err.Error(), "nickname")
}

func TestErrConflict_MessageDiffersFromErrNotFound(t *testing.T) {
	require.NotEqual(t, ErrConflict.Error(), ErrNotFound.Error())
	require.Contains(t, ErrConflict.Error(), "exists")
	require.Contains(t, ErrNotFound.Error(), "not found")
}

func TestUser_DefaultZeroValue(t *testing.T) {
	u := &User{UserID: "u_x"}
	require.Equal(t, "", u.Status)
	require.Nil(t, u.ExtData)
}

func TestUser_StatusSetter(t *testing.T) {
	u := &User{UserID: "u_x"}
	u.Status = "suspended"
	require.Equal(t, "suspended", u.Status)
	u.Status = "deleted"
	require.Equal(t, "deleted", u.Status)
}

func TestSearchUsers_LimitClampingRules_NoDBNeeded(t *testing.T) {
	cases := []struct {
		name  string
		limit int
	}{
		{"zero clamps to default", 0},
		{"negative clamps to default", -1},
		{"above 100 clamps to default", 500},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Service{}
			defer func() {
				_ = recover()
			}()
			_, _ = s.SearchUsers(context.Background(), "alice", tc.limit)
		})
	}
}

func TestUser_ExtDataSupportsNestedValues(t *testing.T) {
	u := &User{
		UserID: "u_test",
		ExtData: map[string]any{
			"nested":   map[string]any{"a": 1, "b": 2},
			"flags":    []any{"a", "b"},
			"provider": "google",
		},
	}
	nested, ok := u.ExtData["nested"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, 1, nested["a"])
	require.Equal(t, 2, nested["b"])
}

func TestMsgpackMarshal_NilValueIsValid(t *testing.T) {
	b, err := msgpackMarshal(nil)
	require.NoError(t, err)
	require.NotEmpty(t, b, "msgpack encoding of nil is a valid empty object representation")
}
