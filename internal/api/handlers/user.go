package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/pulse-workspace/pulse/internal/cell/user"
	"github.com/pulse-workspace/pulse/pkg/shared/authn"
	"github.com/pulse-workspace/pulse/pkg/shared/logger"
)

// UserRegisterRequest is the POST body for /api/v1/user/register.
type UserRegisterRequest struct {
	UserID   string         `json:"userID"`
	Nickname string         `json:"nickname"`
	Email    string         `json:"email,omitempty"`
	Phone    string         `json:"phone,omitempty"`
	Password string         `json:"password,omitempty"`
	Avatar   string         `json:"avatar,omitempty"`
	Ext      map[string]any `json:"ext,omitempty"`
}

// UserRegister handles POST /api/v1/user/register.
//
// Requires admin token (only admins can register users on behalf of an org).
func UserRegister(svc *user.Service, log logger.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req UserRegisterRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, 10005, "invalid request body")
			return
		}
		if req.UserID == "" {
			req.UserID = "" // user.Service generates one if blank
		}
		if strings.TrimSpace(req.Nickname) == "" {
			WriteError(w, http.StatusBadRequest, 10005, "nickname required")
			return
		}
		if req.Email == "" && req.Phone == "" {
			WriteError(w, http.StatusBadRequest, 10005, "email or phone required")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		u, err := svc.Register(ctx, req.UserID, req.Nickname, req.Email, req.Phone, req.Ext)
		if err != nil {
			if errors.Is(err, user.ErrConflict) {
				WriteError(w, http.StatusConflict, 2002, "user already exists")
				return
			}
			log.Error().Err(err).Msg("register user")
			WriteError(w, http.StatusInternalServerError, 10001, "internal error")
			return
		}
		log.Info().Str("user", u.UserID).Msg("user registered")
		WriteOK(w, u)
	}
}

// UserGet handles GET /api/v1/user/get?userID=...
func UserGet(svc *user.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := chi.URLParam(r, "userID")
		if userID == "" {
			userID = r.URL.Query().Get("userID")
		}
		if userID == "" {
			WriteError(w, http.StatusBadRequest, 10005, "userID required")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		u, err := svc.GetUser(ctx, userID)
		if err != nil {
			if errors.Is(err, user.ErrNotFound) {
				WriteError(w, http.StatusNotFound, 2001, "user not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, 10001, "internal error")
			return
		}
		WriteOK(w, u)
	}
}

// UserSelf returns the caller themselves (identified by token sub).
func UserSelf(svc *user.Service, issuer *authn.Issuer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := authn.ClaimsFromContext(r.Context())
		if c == nil {
			WriteError(w, http.StatusUnauthorized, 1001, "missing claims")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		u, err := svc.GetUser(ctx, c.UserID)
		if err != nil {
			WriteError(w, http.StatusNotFound, 2001, "user not found")
			return
		}
		WriteOK(w, u)
	}
}

// IssueUserTokenRequest is the body for /api/v1/auth/userToken.
type IssueUserTokenRequest struct {
	UserID   string `json:"userID"`
	Platform string `json:"platform"`
	DeviceID string `json:"deviceID,omitempty"`
}

// IssueUserToken mints a Pulse user token for the given user.
// Requires admin token (admin is minting on behalf of end users).
func IssueUserToken(issuer *authn.Issuer, svc *user.Service, log logger.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req IssueUserTokenRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, 10005, "invalid request body")
			return
		}
		if req.UserID == "" {
			WriteError(w, http.StatusBadRequest, 10005, "userID required")
			return
		}

		// Verify user exists
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if _, err := svc.GetUser(ctx, req.UserID); err != nil {
			if errors.Is(err, user.ErrNotFound) {
				WriteError(w, http.StatusNotFound, 2001, "user not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, 10001, "internal error")
			return
		}

		tok, exp, err := issuer.Issue(authn.TokenUser, req.UserID, req.Platform, req.DeviceID, "", nil, 1)
		if err != nil {
			log.Error().Err(err).Msg("issue user token")
			WriteError(w, http.StatusInternalServerError, 10001, "internal error")
			return
		}
		WriteOK(w, map[string]any{
			"token":     tok,
			"userID":    req.UserID,
			"expiresAt": exp,
		})
	}
}
