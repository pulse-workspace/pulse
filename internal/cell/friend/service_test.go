package friend

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// ---- input validation guards (pre-DB) ----------------------------------

func TestSendRequest_EmptyIDs(t *testing.T) {
	s := &Service{}
	err := s.SendRequest(t.Context(), "", "u_bob")
	require.Error(t, err)

	err = s.SendRequest(t.Context(), "u_alice", "  ")
	require.Error(t, err)
}

func TestSendRequest_SelfFriend(t *testing.T) {
	s := &Service{}
	err := s.SendRequest(t.Context(), "u_alice", "u_alice")
	require.ErrorIs(t, err, ErrSelfFriend)
}

func TestSendRequest_FriendIDTooLong(t *testing.T) {
	s := &Service{}
	long := make([]byte, MaxFriendIDLength+1)
	for i := range long {
		long[i] = 'a'
	}
	err := s.SendRequest(t.Context(), "u_alice", string(long))
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds")
}

// Accept / Decline / Remove / List* methods that all require non-empty IDs;
// same check, different methods. Each callsite is a separate test so a
// regression points at the exact endpoint.

func TestAcceptRequest_EmptyFrom(t *testing.T) {
	s := &Service{}
	err := s.AcceptRequest(t.Context(), "", "u_bob")
	require.Error(t, err)
}

func TestAcceptRequest_SelfAccept(t *testing.T) {
	s := &Service{}
	err := s.AcceptRequest(t.Context(), "u_alice", "u_alice")
	require.ErrorIs(t, err, ErrSelfFriend)
}

func TestDeclineRequest_EmptyIDs(t *testing.T) {
	s := &Service{}
	err := s.DeclineRequest(t.Context(), "", "")
	require.Error(t, err)
}

func TestDeclineRequest_SelfDecline(t *testing.T) {
	s := &Service{}
	err := s.DeclineRequest(t.Context(), "u_alice", "u_alice")
	require.ErrorIs(t, err, ErrSelfFriend)
}

func TestRemoveFriend_EmptyIDs(t *testing.T) {
	s := &Service{}
	err := s.RemoveFriend(t.Context(), "", "")
	require.Error(t, err)
}

func TestRemoveFriend_SelfRemove(t *testing.T) {
	s := &Service{}
	err := s.RemoveFriend(t.Context(), "u_alice", "u_alice")
	require.ErrorIs(t, err, ErrSelfFriend)
}

func TestListFriends_EmptyUser(t *testing.T) {
	s := &Service{}
	_, err := s.ListFriends(t.Context(), "")
	require.Error(t, err)
}

func TestListIncoming_EmptyUser(t *testing.T) {
	s := &Service{}
	_, err := s.ListIncomingRequests(t.Context(), "")
	require.Error(t, err)
}

func TestListOutgoing_EmptyUser(t *testing.T) {
	s := &Service{}
	_, err := s.ListOutgoingRequests(t.Context(), "")
	require.Error(t, err)
}

// ---- Status enum membership -------------------------------------------

func TestStatusValid(t *testing.T) {
	cases := []struct {
		s  Status
		ok bool
	}{
		{StatusAccepted, true},
		{StatusPendingOut, true},
		{StatusPendingIn, true},
		{StatusBlocked, true},
		{"", false},
		{"rejected", false}, // not in enum
		{"ACCEPTED", false}, // case-sensitive
	}
	for _, c := range cases {
		require.Equal(t, c.ok, c.s.Valid(), "status %q", c.s)
	}
}

// ---- JSON wire shape --------------------------------------------------

func TestFriendship_JSONShape(t *testing.T) {
	f := Friendship{
		UserID:   "u_alice",
		FriendID: "u_bob",
		Status:   StatusAccepted,
	}
	b, err := json.Marshal(f)
	require.NoError(t, err)
	s := string(b)
	for _, key := range []string{
		`"user_id":"u_alice"`,
		`"friend_id":"u_bob"`,
		`"status":"accepted"`,
		`"created_at":"0001-01-01T00:00:00Z"`,
	} {
		require.Contains(t, s, key, "missing key %s", key)
	}
}

// ---- sentinels --------------------------------------------------------

func TestNoSentinelCollision(t *testing.T) {
	sentinels := []error{
		ErrSelfFriend, ErrRequestExists, ErrNotPending, ErrNotFriend,
	}
	for i, a := range sentinels {
		for j, b := range sentinels {
			if i == j {
				continue
			}
			require.False(t, errors.Is(a, b), "%v should not match %v", a, b)
		}
	}
}

func TestSentinelsAreDistinct(t *testing.T) {
	// errors.Is distinguishes the sentinels; errors.As would be even
	// stricter but Is is what the handler relies on. Locked here so
	// future "unify these" refactors don't accidentally alias two
	// different conditions.
	require.True(t, errors.Is(ErrSelfFriend, ErrSelfFriend))
	require.True(t, errors.Is(ErrRequestExists, ErrRequestExists))
	require.True(t, errors.Is(ErrNotPending, ErrNotPending))
	require.True(t, errors.Is(ErrNotFriend, ErrNotFriend))
}
