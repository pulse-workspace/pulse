package webhook

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/pulse-workspace/pulse/pkg/shared/logger"
)

// Service provides high-level business operations for managing and dispatching webhooks.
type Service struct {
	repo       *Repository
	dispatcher *Dispatcher
	log        logger.Logger
}

// NewService constructs a Webhook Service instance.
func NewService(repo *Repository, dispatcher *Dispatcher, log logger.Logger) *Service {
	return &Service{
		repo:       repo,
		dispatcher: dispatcher,
		log:        log.With().Str("component", "webhook_service").Logger(),
	}
}

// Register registers a new webhook subscription for a tenant and generates an HMAC secret.
//
// Business Logic:
// 1. Validates non-empty url and events slice.
// 2. Generates 32-byte cryptographically secure random secret hex string.
// 3. Persists webhook record with unique UUID.
// 4. Returns the created Webhook model and the plain secret (shown once upon creation).
func (s *Service) Register(ctx context.Context, tenantID, url string, events []string) (*Webhook, string, error) {
	if url == "" {
		return nil, "", errors.New("webhook url is required")
	}
	if len(events) == 0 {
		return nil, "", errors.New("at least one event subscription is required")
	}

	secretBytes := make([]byte, 32)
	if _, err := rand.Read(secretBytes); err != nil {
		return nil, "", fmt.Errorf("failed to generate webhook secret: %w", err)
	}
	secret := hex.EncodeToString(secretBytes)

	hook := &Webhook{
		ID:        uuid.New().String(),
		TenantID:  tenantID,
		URL:       url,
		Secret:    secret,
		Events:    events,
		IsActive:  true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := s.repo.Create(ctx, hook); err != nil {
		return nil, "", fmt.Errorf("failed to save webhook: %w", err)
	}

	s.log.Info().Str("id", hook.ID).Str("tenant", tenantID).Str("url", url).Msg("webhook registered")
	return hook, secret, nil
}

// List returns all webhooks configured for a tenant.
func (s *Service) List(ctx context.Context, tenantID string) ([]*Webhook, error) {
	return s.repo.ListByTenant(ctx, tenantID)
}

// Delete removes a webhook subscription by ID.
func (s *Service) Delete(ctx context.Context, id string) error {
	return s.repo.Delete(ctx, id)
}

// BroadcastEvent dispatches an event to all active webhooks subscribed to that event.
//
// Business Logic:
// 1. Queries active webhooks registered for the event.
// 2. Packages event data into EventPayload envelope with unique delivery ID.
// 3. Dispatches HTTP requests concurrently with HMAC-SHA256 signatures.
func (s *Service) BroadcastEvent(ctx context.Context, tenantID, event string, data map[string]any) error {
	hooks, err := s.repo.ListActiveByEvent(ctx, tenantID, event)
	if err != nil {
		return err
	}

	if len(hooks) == 0 {
		return nil
	}

	payload := &EventPayload{
		DeliveryID: uuid.New().String(),
		Event:      event,
		TenantID:   tenantID,
		Timestamp:  time.Now().UnixMilli(),
		Data:       data,
	}

	for _, hook := range hooks {
		h := hook
		go func() {
			deliverCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			if err := s.dispatcher.Deliver(deliverCtx, h, payload); err != nil {
				s.log.Warn().Err(err).Str("hook_id", h.ID).Str("url", h.URL).Msg("webhook delivery error")
			}
		}()
	}

	return nil
}

