// Package push service implements presence-aware push dispatch and silent sync.
package push

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/pulse-workspace/pulse/internal/cell"
	"github.com/pulse-workspace/pulse/internal/cell/presence"
	"github.com/pulse-workspace/pulse/pkg/shared/logger"
)

// PresenceChecker checks whether a user is currently online.
type PresenceChecker interface {
	Get(userID string) (*presence.Entry, bool)
}

// Service coordinates mobile push notification delivery across multiple platforms.
type Service struct {
	repo     *Repository
	log      logger.Logger
	presence PresenceChecker
	mu       sync.RWMutex
	drivers  map[string]Driver
}

// NewService constructs the Push Service.
func NewService(c *cell.Cell, repo *Repository, p PresenceChecker) *Service {
	if repo == nil {
		panic("repository cannot be nil")
	}
	s := &Service{
		repo:     repo,
		log:      c.Log.With().Str("service", "push").Logger(),
		presence: p,
		drivers:  make(map[string]Driver),
	}
	return s
}

// RegisterDriver binds a platform push delivery driver (e.g. FCM, APNs, WebPush).
func (s *Service) RegisterDriver(driver Driver) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.drivers[driver.Platform()] = driver
}

// Name implements cell.Service.
func (s *Service) Name() string {
	return "push"
}

// Start implements cell.Service.
func (s *Service) Start(ctx context.Context) error {
	s.log.Info().Msg("push dispatcher service started")
	return nil
}

// Stop implements cell.Service.
func (s *Service) Stop(ctx context.Context) error {
	s.log.Info().Msg("push dispatcher service stopped")
	return nil
}

// RegisterDevice registers or reactivates a mobile push token.
func (s *Service) RegisterDevice(ctx context.Context, token *DeviceToken) error {
	return s.repo.Register(ctx, token)
}

// UnregisterDevice deactivates a mobile push token upon sign-out.
func (s *Service) UnregisterDevice(ctx context.Context, userID, token string) error {
	return s.repo.Unregister(ctx, userID, token)
}

// ListActive retrieves active device tokens registered to a user.
func (s *Service) ListActive(ctx context.Context, userID string) ([]*DeviceToken, error) {
	return s.repo.ListActive(ctx, userID)
}

// DispatchMessage evaluates presence and sends visible push alerts to offline user devices.
// Presence-Aware Suppression: If the recipient is currently online, the loud push is suppressed.
func (s *Service) DispatchMessage(ctx context.Context, notif *PushNotification) error {
	if notif == nil {
		return errors.New("notification cannot be nil")
	}

	// 1. Check recipient presence: Suppress loud push if recipient is currently online
	if s.presence != nil {
		if entry, ok := s.presence.Get(notif.RecipientID); ok && entry.Status == presence.StatusOnline {
			s.log.Debug().
				Str("recipient_id", notif.RecipientID).
				Str("conv_id", notif.ConvID).
				Msg("push suppressed: recipient is currently online")
			return nil
		}
	}

	// 2. Fetch active tokens for recipient
	tokens, err := s.repo.ListActive(ctx, notif.RecipientID)
	if err != nil {
		return fmt.Errorf("list recipient tokens: %w", err)
	}
	if len(tokens) == 0 {
		return nil
	}

	// 3. Dispatch to each registered device token
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, tok := range tokens {
		driver, ok := s.drivers[tok.Platform]
		if !ok {
			s.log.Warn().
				Str("platform", tok.Platform).
				Str("user_id", tok.UserID).
				Msg("no push driver registered for platform")
			continue
		}

		if err := driver.Send(ctx, tok.Token, notif); err != nil {
			s.log.Warn().
				Err(err).
				Str("token", tok.Token).
				Str("platform", tok.Platform).
				Msg("failed to dispatch loud push notification")
		}
	}

	return nil
}

// DispatchSilentReadDismissal broadcasts silent background push to user's other devices
// to dismiss visual notification banners when a message is read on one device.
func (s *Service) DispatchSilentReadDismissal(
	ctx context.Context,
	recipientID string,
	convID string,
	readSeq int64,
	exceptToken string,
) error {
	tokens, err := s.repo.ListActive(ctx, recipientID)
	if err != nil {
		return fmt.Errorf("fetch active tokens for silent dismissal: %w", err)
	}

	silent := &SilentPush{
		RecipientID: recipientID,
		Action:      ActionDismissNotification,
		ConvID:      convID,
		ReadSeq:     readSeq,
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, tok := range tokens {
		if exceptToken != "" && tok.Token == exceptToken {
			continue // Skip the device that triggered the read receipt
		}

		driver, ok := s.drivers[tok.Platform]
		if !ok {
			continue
		}

		if err := driver.SendSilent(ctx, tok.Token, silent); err != nil {
			s.log.Warn().
				Err(err).
				Str("platform", tok.Platform).
				Msg("failed to deliver silent notification dismissal")
		}
	}

	return nil
}
