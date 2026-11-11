package presence

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// inboundApplyTimeout caps the per-inbound-presence write timeout.
const inboundApplyTimeout = 5 * time.Second

// InboundPresencePayload is the wire shape routed cross-cell for
// presence events. Lives in the presence package (not routing/) so
// the domain layer owns the cache update.
type InboundPresencePayload struct {
	UserID    string `json:"user_id"`
	Online    bool   `json:"online"`
	Platform  string `json:"platform,omitempty"`
	CellID    string `json:"cell_id"` // cell that owns the user
	UpdatedAt string `json:"updated_at"`
}

// ApplyInboundPresence updates the local presence cache for a user
// whose owning cell reported them online/offline. Called from the
// cross-cell routing service when a peer's presence envelope lands.
//
// Stale-write protection: the inbound apply skips when the payload's
// UpdatedAt is older than the stored entry's last update to prevent
// delayed or replayed older envelopes from clobbering fresher state.
func (s *Service) ApplyInboundPresence(p InboundPresencePayload) error {
	if p.UserID == "" {
		return errors.New("inbound: user_id required")
	}
	updatedAt, err := time.Parse(time.RFC3339, p.UpdatedAt)
	if err != nil {
		return fmt.Errorf("inbound: parse updated_at: %w", err)
	}
	s.mu.Lock()
	if e, ok := s.state[p.UserID]; ok && e.UpdatedAt.After(updatedAt) {
		s.mu.Unlock()
		s.log.Debug().
			Str("user_id", p.UserID).
			Time("stored", e.UpdatedAt).
			Time("inbound", updatedAt).
			Msg("stale inbound presence ignored")
		return nil
	}
	s.mu.Unlock()

	var status Status
	if p.Online {
		status = StatusOnline
	} else {
		status = StatusOffline
	}
	ctx, cancel := context.WithTimeout(context.Background(), inboundApplyTimeout)
	defer cancel()
	_ = ctx // reserved for future "lock for write" semantics; current apply uses s.mu

	s.apply(p.UserID, status, p.Platform)
	// Update last_seen on the entry we just touched so the inbound
	// mark-online doesn't lag behind the originating cell's wall
	// clock.
	s.mu.Lock()
	if e, ok := s.state[p.UserID]; ok && p.Online {
		e.LastSeen = updatedAt
		e.UpdatedAt = updatedAt
	}
	s.mu.Unlock()
	s.log.Info().
		Str("user_id", p.UserID).
		Bool("online", p.Online).
		Str("from_cell", p.CellID).
		Msg("applied inbound presence")
	return nil
}
