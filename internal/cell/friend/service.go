// Package friend — service.go: cell.Service implementation + public method
// surface used by the API layer.
//
// The Service is a thin wrapper around the persistence helpers; its only
// non-trivial logic is:
//
//   - SendRequest normalises the input (no self-friend, no duplicates)
//     and writes both edges of the new pending pair.
//   - Accept/Decline/Remove thread the request through the persistence
//     layer with the right `requiredStatus` filter so concurrent
//     declines don't surface as "Already declined" errors.
//   - List* methods are simple fan-outs.
//
// All paths defend against self-targeted writes (A→A) and against the
// caller forging the second edge of an existing pair (re-routing).
package friend

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/pulse-im/pulse/internal/cell"
	"github.com/pulse-im/pulse/pkg/shared/db"
	"github.com/pulse-im/pulse/pkg/shared/logger"
)

// Service is the friend cell service.
type Service struct {
	cell *cell.Cell
	log  logger.Logger
	db   db.DBTX
}

// NewService constructs the friend service. Pure wiring — no license
// gate (friend management is core chat UX).
func NewService(c *cell.Cell) *Service {
	return &Service{
		cell: c,
		log:  c.Log.With().Str("service", "friend").Logger(),
		db:   c.DB,
	}
}

// Name implements cell.Service.
func (s *Service) Name() string { return "friend" }

// Start logs the service is up. No background goroutines.
func (s *Service) Start(ctx context.Context) error {
	s.log.Info().Msg("friend service started")
	return nil
}

// Stop is a no-op.
func (s *Service) Stop(ctx context.Context) error { return nil }

// ---- public API -------------------------------------------------------

// SendRequest creates a pending friend request from userA to userB.
//
// Guards:
//   - empty / self-target → 400 Bad Request
//   - friend_id longer than MaxFriendIDLength → 400 (auditing cap)
//   - existing edge (any direction, any status) → 409 Conflict
//
// The race window where two concurrent SendRequest calls both pass
// edgeExists is closed by ON CONFLICT DO NOTHING + the transaction; the
// loser sees ErrRequestExists via the row-count check.
func (s *Service) SendRequest(ctx context.Context, userA, userB string) error {
	userA = strings.TrimSpace(userA)
	userB = strings.TrimSpace(userB)
	if userA == "" || userB == "" {
		return errors.New("friend: user_id and friend_id required")
	}
	if userA == userB {
		return ErrSelfFriend
	}
	if len(userB) > MaxFriendIDLength {
		return fmt.Errorf("friend: friend_id exceeds %d bytes", MaxFriendIDLength)
	}
	exists, err := s.edgeExists(ctx, userA, userB)
	if err != nil {
		return err
	}
	if exists {
		return ErrRequestExists
	}
	return s.sendRequest(ctx, userA, userB)
}

// AcceptRequest flips the pending pair userA / fromB to 'accepted'.
//
// Guards:
//   - userA == fromB → ErrSelfFriend
//   - no pending pair → ErrNotPending (which the handler maps to 404)
//
// Note the direction: fromB is the original requester whose
// pending_out row gets flipped; userA's pending_in row flips in the
// same UPDATE. Both `StatusAccepted` rows are observed by ListFriends
// from either side.
func (s *Service) AcceptRequest(ctx context.Context, userA, fromB string) error {
	userA = strings.TrimSpace(userA)
	fromB = strings.TrimSpace(fromB)
	if userA == "" || fromB == "" {
		return errors.New("friend: user_id and from_id required")
	}
	if userA == fromB {
		return ErrSelfFriend
	}
	return s.acceptRequest(ctx, userA, fromB)
}

// DeclineRequest removes the pending pair (used when the recipient
// doesn't want to accept). Same shape as AcceptRequest minus the
// flipped status — both edges are deleted in a transaction.
func (s *Service) DeclineRequest(ctx context.Context, userA, fromB string) error {
	userA = strings.TrimSpace(userA)
	fromB = strings.TrimSpace(fromB)
	if userA == "" || fromB == "" {
		return errors.New("friend: user_id and from_id required")
	}
	if userA == fromB {
		return ErrSelfFriend
	}
	return s.removeEdge(ctx, userA, fromB, "pending_out")
}

// RemoveFriend removes an existing friendship. Same SQL path as
// Decline but with status='accepted' guard so we don't accidentally
// delete a pending pair.
func (s *Service) RemoveFriend(ctx context.Context, userA, userB string) error {
	userA = strings.TrimSpace(userA)
	userB = strings.TrimSpace(userB)
	if userA == "" || userB == "" {
		return errors.New("friend: user_id and friend_id required")
	}
	if userA == userB {
		return ErrSelfFriend
	}
	return s.removeEdge(ctx, userA, userB, "accepted")
}

// ListFriends returns the user's accepted friend edges, newest first.
func (s *Service) ListFriends(ctx context.Context, userID string) ([]*Friendship, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, errors.New("friend: user_id required")
	}
	hits, err := s.listByStatus(ctx, userID, StatusAccepted)
	if err != nil {
		return nil, err
	}
	return hits, nil
}

// ListIncomingRequests returns the rows where the caller has a pending
// incoming request. Newest first so the most recent poke shows up top.
func (s *Service) ListIncomingRequests(ctx context.Context, userID string) ([]*Friendship, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, errors.New("friend: user_id required")
	}
	return s.listByStatus(ctx, userID, StatusPendingIn)
}

// ListOutgoingRequests returns the rows where the caller has sent a
// pending request that hasn't been answered yet.
func (s *Service) ListOutgoingRequests(ctx context.Context, userID string) ([]*Friendship, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, errors.New("friend: user_id required")
	}
	return s.listByStatus(ctx, userID, StatusPendingOut)
}
