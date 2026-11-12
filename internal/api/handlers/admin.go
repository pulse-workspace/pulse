package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/argon2"

	"github.com/pulse-workspace/pulse/pkg/shared/authn"
	"github.com/pulse-workspace/pulse/pkg/shared/config"
	"github.com/pulse-workspace/pulse/pkg/shared/db"
	"github.com/pulse-workspace/pulse/pkg/shared/logger"
)

// AdminInitRequest is the POST body for first-time bootstrap.
type AdminInitRequest struct {
	Secret        string `json:"secret"`
	AdminUserID   string `json:"adminUserID"`
	AdminPassword string `json:"adminPassword"`
}

// AdminInit handles POST /api/v1/admin/init.
//
// Idempotent: refuses if an admin already exists, unless the bootstrap secret
// matches and the request supplies a fresh password.
func AdminInit(cfg *config.Config, issuer *authn.Issuer, d *db.DB, log logger.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req AdminInitRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, 10005, "invalid request body")
			return
		}

		expected := os.Getenv("PULSE_ADMIN_SECRET")
		if expected == "" {
			expected = cfg.AdminInitSecret()
		}
		if req.Secret != expected {
			WriteError(w, http.StatusUnauthorized, 1001, "invalid bootstrap secret")
			return
		}

		if req.AdminUserID == "" || req.AdminPassword == "" {
			WriteError(w, http.StatusBadRequest, 10005, "adminUserID and adminPassword required")
			return
		}
		if len(req.AdminPassword) < 8 {
			WriteError(w, http.StatusBadRequest, 2004, "admin password too weak (min 8 chars)")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		// Ensure table exists (dev convenience; migrations land in Week 1-2).
		if err := ensureAdminsTable(ctx, d); err != nil {
			log.Error().Err(err).Msg("ensure admins table")
			WriteError(w, http.StatusInternalServerError, 10001, "internal error")
			return
		}

		// Count existing admins; refuse if already bootstrapped (idempotent).
		var count int
		if err := d.QueryRow(ctx, "SELECT count(*) FROM pulse_admins").Scan(&count); err != nil {
			log.Error().Err(err).Msg("count admins")
			WriteError(w, http.StatusInternalServerError, 10001, "internal error")
			return
		}
		if count > 0 {
			WriteError(w, http.StatusConflict, 1001, "admin already initialised")
			return
		}

		hash, err := hashPassword(req.AdminPassword)
		if err != nil {
			log.Error().Err(err).Msg("hash password")
			WriteError(w, http.StatusInternalServerError, 10001, "internal error")
			return
		}

		if err := d.Exec(ctx,
			`INSERT INTO pulse_admins (user_id, password_hash, is_bootstrap, created_at)
			 VALUES ($1, $2, true, NOW())`,
			req.AdminUserID, hash); err != nil {
			log.Error().Err(err).Msg("insert admin")
			WriteError(w, http.StatusInternalServerError, 10001, "internal error")
			return
		}

		tok, exp, err := issuer.Issue(authn.TokenAdmin, req.AdminUserID, "", "", "", nil, 1)
		if err != nil {
			log.Error().Err(err).Msg("issue token")
			WriteError(w, http.StatusInternalServerError, 10001, "internal error")
			return
		}
		log.Info().Str("admin", req.AdminUserID).Msg("admin initialised")

		WriteOK(w, map[string]any{
			"adminToken": tok,
			"expiresAt":  exp,
		})
	}
}

// AdminLoginRequest is the body for POST /api/v1/admin/login.
type AdminLoginRequest struct {
	UserID   string `json:"userID"`
	Password string `json:"password"`
}

// AdminLogin handles POST /api/v1/admin/login.
func AdminLogin(issuer *authn.Issuer, d *db.DB, log logger.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req AdminLoginRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, 10005, "invalid request body")
			return
		}
		if req.UserID == "" || req.Password == "" {
			WriteError(w, http.StatusBadRequest, 10005, "userID and password required")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		var (
			dbUserID string
			hash     string
		)
		err := d.QueryRow(ctx,
			`SELECT user_id, password_hash FROM pulse_admins WHERE user_id = $1`, req.UserID).
			Scan(&dbUserID, &hash)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				WriteError(w, http.StatusUnauthorized, 1001, "invalid credentials")
				return
			}
			log.Error().Err(err).Msg("query admin")
			WriteError(w, http.StatusInternalServerError, 10001, "internal error")
			return
		}

		if !verifyPassword(req.Password, hash) {
			WriteError(w, http.StatusUnauthorized, 1001, "invalid credentials")
			return
		}

		tok, exp, err := issuer.Issue(authn.TokenAdmin, req.UserID, "", "", "", nil, 1)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, 10001, "internal error")
			return
		}
		WriteOK(w, map[string]any{
			"adminToken": tok,
			"expiresAt":  exp,
		})
	}
}

// ensureAdminsTable creates the pulse_admins table if missing.
// Convenience until migration 0002 lands.
func ensureAdminsTable(ctx context.Context, d *db.DB) error {
	return d.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS pulse_admins (
			user_id        VARCHAR(64) PRIMARY KEY,
			password_hash  VARCHAR(255) NOT NULL,
			is_bootstrap   BOOLEAN NOT NULL DEFAULT false,
			created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`)
}

// Suppress unused-import warnings if user handler is removed.
var _ = argon2.IDKey
