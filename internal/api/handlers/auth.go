// Package handlers exposes HTTP handlers for the Pulse REST API.
package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/pulse-im/pulse/pkg/shared/authn"
	"github.com/pulse-im/pulse/pkg/shared/logger"
)

// adminPasswordLookup is the narrow password-validation contract.
type adminPasswordLookup interface {
	VerifyAdminPassword(ctx context.Context, userID, password string) (bool, error)
}

// AdminPasswordLookup is the exported alias used by Server.
type AdminPasswordLookup = adminPasswordLookup

// AdminPasswordFunc is the function-shaped adapter.
type AdminPasswordFunc func(ctx context.Context, userID, password string) (bool, error)

// VerifyAdminPassword implements the AdminPasswordLookup interface.
func (f AdminPasswordFunc) VerifyAdminPassword(ctx context.Context, userID, password string) (bool, error) {
	return f(ctx, userID, password)
}

// VerifyAdminPasswordHash compares argon2id hash.
func VerifyAdminPasswordHash(plain, encoded string) bool {
	return verifyPassword(plain, encoded)
}

// loginRequest is the body for POST /api/v1/auth/login.
type loginRequest struct {
	UserID   string `json:"user_id"`
	Password string `json:"password"`
}

// Login handles standard admin/user login in OSS Core.
func Login(passwords adminPasswordLookup, issuer *authn.Issuer, log logger.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req loginRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, 10005, "invalid request body")
			return
		}
		req.UserID = strings.TrimSpace(req.UserID)
		if req.UserID == "" || req.Password == "" {
			WriteError(w, http.StatusBadRequest, 10005, "user_id and password required")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		ok, err := passwords.VerifyAdminPassword(ctx, req.UserID, req.Password)
		if err != nil {
			log.Error().Err(err).Str("user_id", req.UserID).Msg("verify admin password")
			WriteError(w, http.StatusInternalServerError, 10001, "internal error")
			return
		}
		if !ok {
			WriteError(w, http.StatusUnauthorized, 1001, "invalid credentials")
			return
		}

		tok, _, err := issuer.Issue(authn.TokenAdmin, req.UserID, "WEB", "", "", nil, 1)
		if err != nil {
			log.Error().Err(err).Str("user_id", req.UserID).Msg("issue token")
			WriteError(w, http.StatusInternalServerError, 10001, "internal error")
			return
		}
		WriteOK(w, map[string]any{
			"user_id": req.UserID,
			"token":   tok,
		})
	}
}
