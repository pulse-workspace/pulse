package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/pulse-im/pulse/internal/cell/group"
	"github.com/pulse-im/pulse/pkg/shared/authn"
	"github.com/pulse-im/pulse/pkg/shared/logger"
)

// GroupCreateRequest is the body for POST /group/create.
type GroupCreateRequest struct {
	Name           string   `json:"name"`
	GroupType      string   `json:"groupType,omitempty"` // "Normal" (default) | "Allow"
	Avatar         string   `json:"avatar,omitempty"`
	Introduction   string   `json:"introduction,omitempty"`
	MaxMembers     int      `json:"maxMembers,omitempty"`
	InitialMembers []string `json:"initialMembers,omitempty"`
	InitialAdmins  []string `json:"initialAdmins,omitempty"`
}

// GroupCreate handles POST /api/v1/group/create.
func GroupCreate(grp *group.Service, log logger.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := authn.ClaimsFromContext(r.Context())
		if c == nil {
			WriteError(w, http.StatusUnauthorized, 1001, "missing claims")
			return
		}
		var req GroupCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, 10005, "invalid request body")
			return
		}
		if req.Name == "" {
			WriteError(w, http.StatusBadRequest, 10005, "name required")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		g, err := grp.Create(ctx, group.CreateInput{
			OwnerID:        c.UserID,
			Name:           req.Name,
			GroupType:      group.GroupType(req.GroupType),
			Avatar:         req.Avatar,
			Introduction:   req.Introduction,
			MaxMembers:     req.MaxMembers,
			InitialMembers: req.InitialMembers,
			InitialAdmin:   req.InitialAdmins,
		})
		if err != nil {
			log.Error().Err(err).Msg("create group")
			WriteError(w, http.StatusInternalServerError, 10001, "internal error")
			return
		}
		log.Info().Str("group", g.GroupID).Str("owner", c.UserID).Msg("group created via api")
		WriteOK(w, g)
	}
}

// GroupGet handles GET /api/v1/group/{groupID}.
func GroupGet(grp *group.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		groupID := chi.URLParam(r, "groupID")
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		g, err := grp.Get(ctx, groupID)
		if err != nil {
			if err == group.ErrNotFound {
				WriteError(w, http.StatusNotFound, 2001, "group not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, 10001, "internal error")
			return
		}
		WriteOK(w, g)
	}
}

// GroupMembers handles GET /api/v1/group/{groupID}/members.
func GroupMembers(grp *group.Service, log logger.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		groupID := chi.URLParam(r, "groupID")
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		members, err := grp.ListMembers(ctx, groupID, 0, 200)
		if err != nil {
			// Sanitized: never echo raw err.Error() — may carry DB internals.
			log.Error().Err(err).Str("group", groupID).Msg("list members")
			WriteError(w, http.StatusInternalServerError, 10001, "internal error")
			return
		}
		WriteOK(w, members)
	}
}

// GroupInvite handles POST /api/v1/group/{groupID}/invite.
func GroupInvite(grp *group.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := authn.ClaimsFromContext(r.Context())
		groupID := chi.URLParam(r, "groupID")
		var req struct {
			UserID string `json:"userID"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.UserID == "" {
			WriteError(w, http.StatusBadRequest, 10005, "userID required")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := grp.Invite(ctx, groupID, c.UserID, req.UserID); err != nil {
			writeGroupErr(w, err)
			return
		}
		WriteOK(w, map[string]any{"ok": true})
	}
}

// GroupKick handles POST /api/v1/group/{groupID}/kick.
func GroupKick(grp *group.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := authn.ClaimsFromContext(r.Context())
		groupID := chi.URLParam(r, "groupID")
		var req struct {
			UserID string `json:"userID"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.UserID == "" {
			WriteError(w, http.StatusBadRequest, 10005, "userID required")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := grp.Kick(ctx, groupID, c.UserID, req.UserID); err != nil {
			writeGroupErr(w, err)
			return
		}
		WriteOK(w, map[string]any{"ok": true})
	}
}

// GroupJoin handles POST /api/v1/group/{groupID}/join (TypeAllow only).
func GroupJoin(grp *group.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := authn.ClaimsFromContext(r.Context())
		groupID := chi.URLParam(r, "groupID")
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := grp.Join(ctx, groupID, c.UserID); err != nil {
			writeGroupErr(w, err)
			return
		}
		WriteOK(w, map[string]any{"ok": true})
	}
}

// GroupQuit handles POST /api/v1/group/{groupID}/quit.
func GroupQuit(grp *group.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := authn.ClaimsFromContext(r.Context())
		groupID := chi.URLParam(r, "groupID")
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := grp.Quit(ctx, groupID, c.UserID); err != nil {
			writeGroupErr(w, err)
			return
		}
		WriteOK(w, map[string]any{"ok": true})
	}
}

// GroupDismiss handles POST /api/v1/group/{groupID}/dismiss (owner only).
func GroupDismiss(grp *group.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := authn.ClaimsFromContext(r.Context())
		groupID := chi.URLParam(r, "groupID")
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := grp.Dismiss(ctx, groupID, c.UserID); err != nil {
			writeGroupErr(w, err)
			return
		}
		WriteOK(w, map[string]any{"ok": true})
	}
}

// writeGroupErr maps group sentinel errors to HTTP responses.
// Sanitized: each case uses a static, operator-safe message keyed to the
// existing Pulse error code (2001 / 1005 / 2002 / 2003 / 2004) rather than
// the raw sentinel error text. The codes are part of the public API
// contract; clients can still correlate against them.
func writeGroupErr(w http.ResponseWriter, err error) {
	switch err {
	case group.ErrNotFound, group.ErrMemberNotFound:
		WriteError(w, http.StatusNotFound, 2001, "group or member not found")
	case group.ErrForbidden:
		WriteError(w, http.StatusForbidden, 1005, "forbidden")
	case group.ErrMemberExists:
		WriteError(w, http.StatusConflict, 2002, "member already in group")
	case group.ErrGroupFull:
		WriteError(w, http.StatusConflict, 2003, "group is full")
	case group.ErrLastOwner:
		WriteError(w, http.StatusConflict, 2004, "cannot remove last owner")
	default:
		WriteError(w, http.StatusInternalServerError, 10001, "internal error")
	}
}
