// Package friend owns the friendship graph — friend requests, accept/decline,
// and the friend list per user. Built on the friendships table from migration
// 0001 and tightened by 0023 to constrain status to a closed enum.
//
// File layout:
//   - types.go        — Friendship record + Status enum + sentinel errors
//   - persistence.go  — SQL helpers: send/accept/decline/remove + list friends
//   - list pending (in/out)
//   - service.go      — cell.Service implementation; cell.Service lifecycle
//   - the public method surface used by the API layer.
//
// A "friendship" is symmetric: when A and B are friends, BOTH
// (A, B, 'accepted') and (B, A, 'accepted') rows exist. Likewise for
// pending state — A's outgoing request produces (A, B, 'pending_out')
// AND (B, A, 'pending_in') so ListIncomingRequests on the recipient
// is a single-row lookup, not a JOIN. The service always writes BOTH
// rows in a single transaction so the graph stays consistent under
// concurrent accept/decline from either side.
package friend

import (
	"errors"
	"time"
)

// Status enumerates the relationship states the service can read or
// write. Constants mirror the check constraint added in
// migrations/0023_friendship_status_constraint.up.sql.
type Status string

const (
	// StatusAccepted — mutual friends. Both rows have this value.
	StatusAccepted Status = "accepted"
	// StatusPendingOut — the row's user_id has sent a request and is
	// waiting for the friend_id to respond. The reverse row is
	// StatusPendingIn.
	StatusPendingOut Status = "pending_out"
	// StatusPendingIn — the row's user_id has an incoming request from
	// friend_id. The reverse row is StatusPendingOut.
	StatusPendingIn Status = "pending_in"
	// StatusBlocked — the row's user_id has blocked friend_id.
	// Reserved for a future "block from friends list" UX path; the
	// separate blacklists table covers blocking today so this state
	// is currently dormant without requiring a schema change.
	StatusBlocked Status = "blocked"
)

// Valid reports whether the status is one of the enum values the
// CHECK constraint allows. Service-side defence-in-depth: the DB
// constraint rejects invalid writes, but the service re-checks on
// read paths so a manual UPDATE in psql doesn't slip past.
func (s Status) Valid() bool {
	switch s {
	case StatusAccepted, StatusPendingOut, StatusPendingIn, StatusBlocked:
		return true
	default:
		return false
	}
}

// Friendship is one edge of the relationship from the row's user_id
// perspective. Use it for ListFriends — the API client iterates and
// reads friend_id for the "you're friends with X" UI.
//
// For two-way introspection (mutual-friends queries) the client calls
// ListFriends twice and intersects client-side; a server-side JOIN is
// overkill at the user's friend-list scale (~10² per user typically).
//
// Status is typed as the enum so the .Valid() helper + .StatusAccepted
// constants are usable at call sites; the JSON wire format stays a
// plain string (Go's string encoding for a named-string type is
// identical to a bare string field).
type Friendship struct {
	UserID    string    `json:"user_id"`
	FriendID  string    `json:"friend_id"`
	Status    Status    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// ErrSelfFriend — caller tried to friend themselves. Map to 400.
var ErrSelfFriend = errors.New("friend: cannot friend yourself")

// ErrRequestExists — caller tried to send a request that already
// exists (either already pending or already accepted). Map to 409
// Conflict so the UI can show "you've already sent / you're already
// friends" rather than a generic 500.
var ErrRequestExists = errors.New("friend: request already exists")

// ErrNotPending — accept/reject was called on a non-pending edge.
// Could be the request was already accepted, declined, or never sent.
// Map to 404 (don't leak existence).
var ErrNotPending = errors.New("friend: no pending request to act on")

// ErrNotFriend — remove/list-target called on an edge that isn't a
// friend relationship. Same as ErrNotPending: don't leak existence.
var ErrNotFriend = errors.New("friend: not a friend")

// MaxFriendIDLength caps friend_id so a malicious client can't push a
// 64KB string into the audit-style log. user_id already has a
// VARCHAR(64) cap from the users table; the same 64-byte cap keeps the
// friendships table uniform.
const MaxFriendIDLength = 64
