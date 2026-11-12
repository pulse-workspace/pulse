package webhook

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// DBTX encapsulates PostgreSQL query execution interface.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) error
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Repository manages persistence for webhooks.
type Repository struct {
	db DBTX
}

// NewRepository constructs a Webhook repository instance.
func NewRepository(db DBTX) *Repository {
	return &Repository{db: db}
}

// Create inserts a new webhook subscription record.
//
// Business Logic:
// Persists webhook URL, events list, and generated HMAC secret.
func (r *Repository) Create(ctx context.Context, hook *Webhook) error {
	now := time.Now()
	hook.CreatedAt = now
	hook.UpdatedAt = now

	query := `
		INSERT INTO webhooks (id, tenant_id, url, secret, events, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`
	return r.db.Exec(
		ctx,
		query,
		hook.ID,
		hook.TenantID,
		hook.URL,
		hook.Secret,
		hook.Events,
		hook.IsActive,
		hook.CreatedAt,
		hook.UpdatedAt,
	)
}

// ListByTenant retrieves all registered webhooks for a given tenant.
func (r *Repository) ListByTenant(ctx context.Context, tenantID string) ([]*Webhook, error) {
	query := `
		SELECT id, tenant_id, url, secret, events, is_active, created_at, updated_at
		FROM webhooks
		WHERE tenant_id = $1
		ORDER BY created_at DESC
	`
	rows, err := r.db.Query(ctx, query, tenantID)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		return []*Webhook{}, nil
	}
	defer rows.Close()

	var result []*Webhook
	for rows.Next() {
		var w Webhook
		err := rows.Scan(
			&w.ID,
			&w.TenantID,
			&w.URL,
			&w.Secret,
			&w.Events,
			&w.IsActive,
			&w.CreatedAt,
			&w.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		result = append(result, &w)
	}

	return result, nil
}

// ListActiveByEvent returns active webhooks subscribed to a specific event type.
//
// Business Logic:
// Queries webhooks matching tenantID and is_active = true where the events array
// contains the specified event identifier (e.g. "MessageSent").
func (r *Repository) ListActiveByEvent(ctx context.Context, tenantID, event string) ([]*Webhook, error) {
	query := `
		SELECT id, tenant_id, url, secret, events, is_active, created_at, updated_at
		FROM webhooks
		WHERE tenant_id = $1 AND is_active = true AND $2 = ANY(events)
	`
	rows, err := r.db.Query(ctx, query, tenantID, event)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		return []*Webhook{}, nil
	}
	defer rows.Close()

	var result []*Webhook
	for rows.Next() {
		var w Webhook
		err := rows.Scan(
			&w.ID,
			&w.TenantID,
			&w.URL,
			&w.Secret,
			&w.Events,
			&w.IsActive,
			&w.CreatedAt,
			&w.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		result = append(result, &w)
	}

	return result, nil
}

// Delete removes a webhook subscription by ID.
func (r *Repository) Delete(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("webhook id is required")
	}
	query := `DELETE FROM webhooks WHERE id = $1`
	return r.db.Exec(ctx, query, id)
}

