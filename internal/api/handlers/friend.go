// Package handlers — friend.go: HTTP surface for friend-management operations.
//
// Endpoints (mounted under /api/v1/friend):
//
//	POST   /request                — body {friend_id}; creates a pending request
//	POST   /accept                 — body {friend_id}; accepts an incoming request
//	POST   /decline                — body {friend_id}; declines an incoming request
//	DELETE /{friend_id}            — removes an accepted friend
//	GET    /                       — list accepted friends
//	GET    /requests/incoming      — list incoming pending requests
//	GET    /requests/outgoing      — list outgoing pending requests
//
// Authorization: every endpoint requires a user JWT. The caller of a
// request/accept/decline/list path is always the *current user* — the
// `friend_id` body field is never trusted; it's validated against the
// user table by the service (see internal/cell/friend/service.go).
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/pulse-im/pulse/internal/cell/friend"
	"github.com/pulse-im/pulse/pkg/shared/authn"
	"github.com/pulse-im/pulse/pkg/shared/logger"
)

// FriendService is the narrow handler-package view of *friend.Service.
// Mirrors the MFAService pattern so server.go stays free of the
// internal/cell/friend import path.
type FriendService interface {
	SendRequest(ctx context.Context, userA, userB string) error
	AcceptRequest(ctx context.Context, userA, fromB string) error
	DeclineRequest(ctx context.Context, userA, fromB string) error
	RemoveFriend(ctx context.Context, userA, userB string) error
	ListFriends(ctx context.Context, userID string) ([]*friend.Friendship, error)
	ListIncomingRequests(ctx context.Context, userID string) ([]*friend.Friendship, error)
	ListOutgoingRequests(ctx context.Context, userID string) ([]*friend.Friendship, error)
}

// RequireFriendService is the chi middleware that returns 503 when
// the friend service hasn't been wired in. Same posture as
// RequireMediaService / RequireSearchService — keeps the cell bootable
// in environments where the table isn't yet present.
func RequireFriendService(s FriendService) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if s == nil {
				WriteError(w, http.StatusServiceUnavailable, 10001, "friend service unavailable")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// friendIDBody is the body shape for /request, /accept, /decline. All
// three endpoints take the same JSON so the request parser can be
// reused. friend_id is required; an empty/missing field → 400.
type friendIDBody struct {
	FriendID string `json:"friend_id"`
}

// decodeFriendID pulls the body and validates friend_id is non-empty.
func decodeFriendID(w http.ResponseWriter, r *http.Request) (string, bool) {
	var body friendIDBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, 10005, "invalid request body")
		return "", false
	}
	if body.FriendID == "" {
		WriteError(w, http.StatusBadRequest, 10005, "friend_id required")
		return "", false
	}
	return body.FriendID, true
}

// FriendRequest handles POST /api/v1/friend/request.
//
// Body:
//
//	{"friend_id": "u_bob"}
//
// Returns 200 OK with `{status: "pending"}` confirming the request was
// recorded. A 409 Conflict is returned if any edge already exists
// (race-loser or genuine duplicate) — see mapFriendError for the
// sentinel-to-status mapping.
func FriendRequest(svc FriendService, log logger.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := authn.ClaimsFromContext(r.Context())
		if c == nil {
			WriteError(w, http.StatusUnauthorized, 1001, "missing claims")
			return
		}
		friendID, ok := decodeFriendID(w, r)
		if !ok {
			return
		}

		if err := svc.SendRequest(r.Context(), c.UserID, friendID); err != nil {
			mapFriendError(w, log, err, "send")
			return
		}
		log.Info().Str("from", c.UserID).Str("to", friendID).Msg("friend request sent")
		WriteOK(w, map[string]any{"status": "pending"})
	}
}

// FriendAccept handles POST /api/v1/friend/accept.
//
// Body:
//
//	{"friend_id": "u_bob"}
//
// Returns 200 OK once the pending pair flips to accepted.
func FriendAccept(svc FriendService, log logger.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := authn.ClaimsFromContext(r.Context())
		if c == nil {
			WriteError(w, http.StatusUnauthorized, 1001, "missing claims")
			return
		}
		friendID, ok := decodeFriendID(w, r)
		if !ok {
			return
		}

		if err := svc.AcceptRequest(r.Context(), c.UserID, friendID); err != nil {
			mapFriendError(w, log, err, "accept")
			return
		}
		log.Info().Str("by", c.UserID).Str("from", friendID).Msg("friend request accepted")
		WriteOK(w, map[string]any{"status": "accepted"})
	}
}

// FriendDecline handles POST /api/v1/friend/decline.
//
// Body:
//
//	{"friend_id": "u_bob"}
//
// Returns 200 OK once the pending pair is removed. The handler never
// reports whether the row existed — non-existent / already-declined
// both map to 404 to avoid existence leaks (same posture as Delete).
func FriendDecline(svc FriendService, log logger.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := authn.ClaimsFromContext(r.Context())
		if c == nil {
			WriteError(w, http.StatusUnauthorized, 1001, "missing claims")
			return
		}
		friendID, ok := decodeFriendID(w, r)
		if !ok {
			return
		}

		if err := svc.DeclineRequest(r.Context(), c.UserID, friendID); err != nil {
			mapFriendError(w, log, err, "decline")
			return
		}
		log.Info().Str("by", c.UserID).Str("from", friendID).Msg("friend request declined")
		WriteOK(w, map[string]any{"status": "declined"})
	}
}

// FriendRemove handles DELETE /api/v1/friend/{friend_id}.
//
// No body; the friend_id is the URL path parameter. Same 404 posture
// for non-existent / non-accepted edges as Decline.
func FriendRemove(svc FriendService, log logger.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := authn.ClaimsFromContext(r.Context())
		if c == nil {
			WriteError(w, http.StatusUnauthorized, 1001, "missing claims")
			return
		}
		friendID := chi.URLParam(r, "friendID")
		if friendID == "" {
			WriteError(w, http.StatusBadRequest, 10005, "friend_id required")
			return
		}

		if err := svc.RemoveFriend(r.Context(), c.UserID, friendID); err != nil {
			mapFriendError(w, log, err, "remove")
			return
		}
		log.Info().Str("by", c.UserID).Str("friend", friendID).Msg("friend removed")
		WriteOK(w, map[string]string{"friend_id": friendID, "status": "removed"})
	}
}

// FriendList handles GET /api/v1/friend. Returns the caller's accepted
// friends in newest-first order. Always returns a non-null array so
// the client doesn't have to nil-check.
func FriendList(svc FriendService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := authn.ClaimsFromContext(r.Context())
		if c == nil {
			WriteError(w, http.StatusUnauthorized, 1001, "missing claims")
			return
		}
		friends, err := svc.ListFriends(r.Context(), c.UserID)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, 10001, "internal error")
			return
		}
		if friends == nil {
			friends = []*friend.Friendship{}
		}
		WriteOK(w, map[string]any{"friends": friends})
	}
}

// FriendRequests handles GET /api/v1/friend/requests/{direction}.
// direction is "incoming" or "outgoing". Two sub-routes:
//   - /api/v1/friend/requests/incoming
//   - /api/v1/friend/requests/outgoing
func FriendRequests(svc FriendService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := authn.ClaimsFromContext(r.Context())
		if c == nil {
			WriteError(w, http.StatusUnauthorized, 1001, "missing claims")
			return
		}
		direction := chi.URLParam(r, "direction")
		var (
			rows []*friend.Friendship
			err  error
		)
		switch direction {
		case "incoming":
			rows, err = svc.ListIncomingRequests(r.Context(), c.UserID)
		case "outgoing":
			rows, err = svc.ListOutgoingRequests(r.Context(), c.UserID)
		default:
			WriteError(w, http.StatusBadRequest, 10005, "direction must be incoming or outgoing")
			return
		}
		if err != nil {
			WriteError(w, http.StatusInternalServerError, 10001, "internal error")
			return
		}
		if rows == nil {
			rows = []*friend.Friendship{}
		}
		WriteOK(w, map[string]any{direction: rows})
	}
}

// mapFriendError centralises the sentinel-error → HTTP mapping so
// every handler stays a one-shot. Pulled out of the closures because
// each branch is otherwise identical.
func mapFriendError(w http.ResponseWriter, log logger.Logger, err error, op string) {
	switch {
	case errors.Is(err, friend.ErrSelfFriend):
		WriteError(w, http.StatusBadRequest, 10005, "cannot friend yourself")
	case errors.Is(err, friend.ErrRequestExists):
		WriteError(w, http.StatusConflict, 10006, "request already exists or you are already friends")
	case errors.Is(err, friend.ErrNotPending), errors.Is(err, friend.ErrNotFriend):
		// Same response — never leak existence of someone else's row.
		WriteError(w, http.StatusNotFound, 10012, "no matching friend or request")
	default:
		log.Error().Err(err).Str("op", op).Msg("friend service error")
		WriteError(w, http.StatusInternalServerError, 10001, "internal error")
	}
}
