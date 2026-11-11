// Package user is the user domain service.
//
// State changes are appended to the user:<id> event stream in NATS JetStream.
// Read queries hit the projection table (users + presence).
package user

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/oklog/ulid/v2"

	"github.com/pulse-im/pulse/internal/cell"
	"github.com/pulse-im/pulse/pkg/shared/db"
	"github.com/pulse-im/pulse/pkg/shared/events"
	"github.com/pulse-im/pulse/pkg/shared/logger"
	"github.com/pulse-im/pulse/pkg/shared/nats"
)

// User is the projection (read model) for a single user.
type User struct {
	UserID    string
	Nickname  string
	Avatar    string
	Email     string
	Phone     string
	Status    string // active | suspended | deleted
	// MFARequired, when true, forces the user to complete WebAuthn assertion on every login.
	MFARequired bool `json:"mfa_required,omitempty"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ExtData     map[string]any
}

// Service implements the user domain service.
type Service struct {
	cell *cell.Cell
	log  logger.Logger
	db   *db.DB
	nats *nats.Client
}

// NewService creates the user service.
func NewService(c *cell.Cell) *Service {
	return &Service{
		cell: c,
		log:  c.Log.With().Str("service", "user").Logger(),
		db:   c.DB,
		nats: c.NATS,
	}
}

// Name implements cell.Service.
func (s *Service) Name() string { return "user" }

// Start subscribes to user events and applies projections.
//
// Future work: subscribe to the "user.>" stream and dispatch to handlers
// that update the users projection table. For now writes go through
// upsertProjection synchronously (see Register).
func (s *Service) Start(ctx context.Context) error {
	if err := s.nats.EnsureStream(ctx, "events_users", []string{"user.>"}); err != nil {
		return err
	}
	return nil
}

// Stop shuts the service down.
func (s *Service) Stop(ctx context.Context) error {
	return nil
}

// Register creates a new user. If userID is empty, a ULID is generated.
// Returns ErrConflict if a user with the same email or phone already exists.
func (s *Service) Register(ctx context.Context, userID, nickname, email, phone string, ext map[string]any) (*User, error) {
	if nickname == "" {
		return nil, errors.New("nickname required")
	}
	if email == "" && phone == "" {
		return nil, errors.New("email or phone required")
	}

	// Conflict check (email or phone already taken)
	if email != "" {
		var existing string
		err := s.db.QueryRow(ctx,
			`SELECT user_id FROM users WHERE email = $1 LIMIT 1`, email).Scan(&existing)
		if err == nil {
			return nil, ErrConflict
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
	}
	if phone != "" {
		var existing string
		err := s.db.QueryRow(ctx,
			`SELECT user_id FROM users WHERE phone = $1 LIMIT 1`, phone).Scan(&existing)
		if err == nil {
			return nil, ErrConflict
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
	}

	if userID == "" {
		userID = "u_" + ulid.Make().String()
	}
	now := time.Now().UTC()

	// Append event (write side)
	evt := events.NewEvent("user:"+userID, events.TypeUserCreated, "", nil)
	evt.Data, _ = msgpackMarshal(map[string]any{
		"user_id":    userID,
		"nickname":   nickname,
		"email":      email,
		"phone":      phone,
		"ext":        ext,
		"created_at": now,
	})

	subject := "user." + userID
	if err := s.nats.Publish(ctx, subject, mustMarshal(evt)); err != nil {
		return nil, fmt.Errorf("publish event: %w", err)
	}

	// Synchronous projection write so caller can read back.
	// In a fully CQRS system this would be async via consumer, but we keep
	// a synchronous write-through path for low-latency reads after Register.
	u := &User{
		UserID:    userID,
		Nickname:  nickname,
		Avatar:    "",
		Email:     email,
		Phone:     phone,
		Status:    "active",
		CreatedAt: now,
		UpdatedAt: now,
		ExtData:   ext,
	}
	if err := s.upsertProjection(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}

// GetUser returns the projection for userID, or ErrNotFound.
func (s *Service) GetUser(ctx context.Context, userID string) (*User, error) {
	u := &User{}
	err := s.db.QueryRow(ctx, `
		SELECT user_id, nickname, COALESCE(avatar, ''), COALESCE(email, ''), COALESCE(phone, ''), status,
		       COALESCE(mfa_required, false), created_at, updated_at, ext_data
		FROM users WHERE user_id = $1
	`, userID).Scan(&u.UserID, &u.Nickname, &u.Avatar, &u.Email, &u.Phone,
		&u.Status, &u.MFARequired, &u.CreatedAt, &u.UpdatedAt, &u.ExtData)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return u, nil
}

// SearchUsers finds users whose nickname/email matches keyword.
func (s *Service) SearchUsers(ctx context.Context, keyword string, limit int) ([]*User, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.Query(ctx, `
		SELECT user_id, nickname, COALESCE(avatar, ''), COALESCE(email, ''), COALESCE(phone, ''), status,
		       COALESCE(mfa_required, false), created_at, updated_at, ext_data
		FROM users
		WHERE nickname ILIKE $1 OR email ILIKE $1
		ORDER BY created_at DESC
		LIMIT $2
	`, "%"+keyword+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*User
	for rows.Next() {
		u := &User{}
		if err := rows.Scan(&u.UserID, &u.Nickname, &u.Avatar, &u.Email, &u.Phone,
			&u.Status, &u.MFARequired, &u.CreatedAt, &u.UpdatedAt, &u.ExtData); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// upsertProjection writes/updates the users projection table.
func (s *Service) upsertProjection(ctx context.Context, u *User) error {
	return s.db.Exec(ctx, `
		INSERT INTO users (user_id, nickname, avatar, email, phone, status, mfa_required, created_at, updated_at, ext_data)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (user_id) DO UPDATE
		SET nickname = EXCLUDED.nickname,
		    avatar   = EXCLUDED.avatar,
		    email    = EXCLUDED.email,
		    phone    = EXCLUDED.phone,
		    status   = EXCLUDED.status,
		    mfa_required = EXCLUDED.mfa_required,
		    ext_data = EXCLUDED.ext_data,
		    updated_at = EXCLUDED.updated_at
	`, u.UserID, u.Nickname, u.Avatar, u.Email, u.Phone, u.Status,
		u.MFARequired, u.CreatedAt, u.UpdatedAt, u.ExtData)
}

// ErrNotFound is returned when a user does not exist.
var ErrNotFound = errors.New("user not found")

// ErrConflict is returned when a user with the same email or phone already exists.
var ErrConflict = errors.New("user already exists")

// GetTokenVersion implements authn.TokenStore.
func (s *Service) GetTokenVersion(ctx context.Context, userID string) (int, bool) {
	var v int
	err := s.db.QueryRow(ctx, `SELECT token_version FROM users WHERE user_id = $1`, userID).Scan(&v)
	if err != nil {
		return 0, false
	}
	return v, true
}

// BumpTokenVersion increments the user's token_version by 1, invalidating all
// previously-issued tokens (used by /auth/logout).
func (s *Service) BumpTokenVersion(ctx context.Context, userID string) error {
	return s.db.Exec(ctx,
		`UPDATE users SET token_version = token_version + 1, updated_at = NOW() WHERE user_id = $1`, userID)
}

// SetMFARequired toggles whether the user must complete WebAuthn MFA assertion on login.
// Returns ErrNotFound if the user does not exist.
//
// The change is a write-through projection update — we do not currently emit a
// domain event for the flag flip because the field is purely an enforcement
// toggle, not a state transition that downstream cells depend on.
func (s *Service) SetMFARequired(ctx context.Context, userID string, required bool) error {
	// Existence check first so we can return ErrNotFound rather than a silent
	// no-op. Cost is one extra round-trip but the call path is admin-driven.
	var exists bool
	err := s.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM users WHERE user_id = $1)`, userID).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	return s.db.Exec(ctx,
		`UPDATE users SET mfa_required = $1, updated_at = NOW() WHERE user_id = $2`,
		required, userID)
}

// MFARequired returns the user's MFA enforcement flag. Returns ErrNotFound if
// the user does not exist so the caller can distinguish "unknown user" from
// "known user, not required".
func (s *Service) MFARequired(ctx context.Context, userID string) (bool, error) {
	var required bool
	err := s.db.QueryRow(ctx,
		`SELECT COALESCE(mfa_required, false) FROM users WHERE user_id = $1`, userID).
		Scan(&required)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, ErrNotFound
		}
		return false, err
	}
	return required, nil
}
