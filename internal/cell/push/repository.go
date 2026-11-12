// Package push repository handles database operations for registered device push tokens.
package push

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/oklog/ulid/v2"
	"github.com/pulse-workspace/pulse/pkg/shared/db"
)

// Repository manages persistence for client device push notification tokens.
type Repository struct {
	dbtx db.DBTX
}

// NewRepository initializes a Repository with database access.
func NewRepository(dbtx db.DBTX) *Repository {
	if dbtx == nil {
		panic("dbtx cannot be nil")
	}
	return &Repository{dbtx: dbtx}
}

// Register upserts a device token for an authenticated user.
// If the token already exists for the user, it reactivates it and updates timestamps.
func (r *Repository) Register(ctx context.Context, token *DeviceToken) error {
	if err := token.Validate(); err != nil {
		return fmt.Errorf("validate device token: %w", err)
	}

	if token.ID == "" {
		token.ID = "devtok_" + ulid.Make().String()
	}
	now := time.Now().UTC()
	token.CreatedAt = now
	token.UpdatedAt = now
	token.IsActive = true

	query := `
		INSERT INTO device_tokens (id, user_id, platform, token, app_bundle_id, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (user_id, token) DO UPDATE SET
			is_active = true,
			platform = EXCLUDED.platform,
			app_bundle_id = EXCLUDED.app_bundle_id,
			updated_at = EXCLUDED.updated_at
	`
	err := r.dbtx.Exec(ctx, query,
		token.ID,
		token.UserID,
		token.Platform,
		token.Token,
		token.AppBundleID,
		token.IsActive,
		token.CreatedAt,
		token.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("register device token: %w", err)
	}
	return nil
}

// Unregister deactivates a device token for a user.
func (r *Repository) Unregister(ctx context.Context, userID, token string) error {
	query := `
		UPDATE device_tokens
		SET is_active = false, updated_at = NOW()
		WHERE user_id = $1 AND token = $2
	`
	err := r.dbtx.Exec(ctx, query, userID, token)
	if err != nil {
		return fmt.Errorf("unregister device token: %w", err)
	}
	return nil
}

// ListActive retrieves all currently active push tokens registered to a user.
func (r *Repository) ListActive(ctx context.Context, userID string) ([]*DeviceToken, error) {
	query := `
		SELECT id, user_id, platform, token, app_bundle_id, is_active, created_at, updated_at
		FROM device_tokens
		WHERE user_id = $1 AND is_active = true
		ORDER BY updated_at DESC
	`
	rows, err := r.dbtx.Query(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("query active device tokens: %w", err)
	}
	if rows == nil {
		return []*DeviceToken{}, nil
	}
	defer rows.Close()

	var tokens []*DeviceToken
	for rows.Next() {
		t := &DeviceToken{}
		if err := rows.Scan(
			&t.ID,
			&t.UserID,
			&t.Platform,
			&t.Token,
			&t.AppBundleID,
			&t.IsActive,
			&t.CreatedAt,
			&t.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan device token: %w", err)
		}
		tokens = append(tokens, t)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate device tokens: %w", err)
	}
	return tokens, nil
}

// Deactivate disables a token across all users when reported invalid by push gateways.
func (r *Repository) Deactivate(ctx context.Context, token string) error {
	query := `
		UPDATE device_tokens
		SET is_active = false, updated_at = NOW()
		WHERE token = $1
	`
	return r.dbtx.Exec(ctx, query, token)
}

// ScanRow decodes a single pgx.Row into a DeviceToken.
func ScanRow(row pgx.Row) (*DeviceToken, error) {
	t := &DeviceToken{}
	if err := row.Scan(
		&t.ID,
		&t.UserID,
		&t.Platform,
		&t.Token,
		&t.AppBundleID,
		&t.IsActive,
		&t.CreatedAt,
		&t.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return t, nil
}
