// Package handlers: small focused HTTP handlers.
package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/pulse-workspace/pulse/internal/cell/user"
	"github.com/pulse-workspace/pulse/pkg/shared/authn"
	"github.com/pulse-workspace/pulse/pkg/shared/logger"
)

// Health returns 200 with a small JSON payload.
func Health(started time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		WriteOK(w, map[string]any{
			"status":  "ok",
			"started": started.Format(time.RFC3339),
			"uptime":  time.Since(started).String(),
		})
	}
}

// AdminHealth returns 200 for an authenticated admin caller.
func AdminHealth() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		WriteOK(w, map[string]any{"status": "admin-ok"})
	}
}

// Logout bumps the user's token_version, invalidating all previously-issued
// tokens for that user. Subsequent requests with the old token will be
// rejected by the auth middleware (Verify checks token_version).
func Logout(users *user.Service, log logger.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := authn.ClaimsFromContext(r.Context())
		if c == nil || c.UserID == "" {
			WriteError(w, http.StatusUnauthorized, 1001, "missing claims")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := users.BumpTokenVersion(ctx, c.UserID); err != nil {
			if errors.Is(err, user.ErrNotFound) {
				WriteError(w, http.StatusNotFound, 2001, "user not found")
				return
			}
			log.Error().Err(err).Str("user", c.UserID).Msg("logout bump")
			WriteError(w, http.StatusInternalServerError, 10001, "internal error")
			return
		}
		log.Info().Str("user", c.UserID).Msg("logout: tokens revoked")
		WriteOK(w, map[string]any{"logged_out": true})
	}
}
