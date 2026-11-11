// Package presence tracks online/offline state + last-seen.
//
// Pulse uses a heartbeat-driven presence model: the WS gateway reports
// "online" on connect and "offline" on disconnect; a periodic heartbeat
// keeps the entry alive. Subscribers are notified via callback.
package presence

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/pulse-im/pulse/internal/cell"
	"github.com/pulse-im/pulse/pkg/shared/logger"
	"github.com/pulse-im/pulse/pkg/shared/nats"
)

// Status is the online/offline enum.
type Status string

const (
	StatusOnline  Status = "online"
	StatusOffline Status = "offline"
	StatusAway    Status = "away"
)

// Entry is a presence record for one user.
type Entry struct {
	UserID    string    `json:"user_id"`
	Status    Status    `json:"status"`
	Device    string    `json:"device,omitempty"`
	LastSeen  time.Time `json:"last_seen"`
	UpdatedAt time.Time `json:"updated_at"`
}

// crossCellPublisher is the subset of routing.Service that presence
// uses for cross-cell fan-out. Defined locally to avoid an import
// cycle (routing imports domain packages for inbound payload types;
// domains must NOT import routing).
type crossCellPublisher interface {
	PublishCrossCell(ctx context.Context, peerCell, kind string, payload any) error
	KnownPeers() []string
}

// Service tracks presence and pushes updates via NATS.
type Service struct {
	cell   *cell.Cell
	log    logger.Logger
	nats   *nats.Client
	router crossCellPublisher // optional; nil-safe
	mu     sync.RWMutex
	state  map[string]*Entry // userID → entry
	stop   chan struct{}
}

// NewService constructs the service.
func NewService(c *cell.Cell) *Service {
	return &Service{
		cell:  c,
		log:   c.Log.With().Str("service", "presence").Logger(),
		nats:  c.NATS,
		state: make(map[string]*Entry),
		stop:  make(chan struct{}),
	}
}

// SetRouter wires the cross-cell presence event publisher.
// Pass nil to disable cross-cell presence fan-out.
func (s *Service) SetRouter(r crossCellPublisher) {
	s.router = r
}

// Name implements cell.Service.
func (s *Service) Name() string { return "presence" }

// Start subscribes to gateway presence events from NATS.
func (s *Service) Start(ctx context.Context) error {
	if err := s.nats.EnsureStream(ctx, "events_presence", []string{"presence.>"}); err != nil {
		return err
	}
	return s.nats.Subscribe(ctx, "events_presence", "presence-watcher", "presence.>", func(data []byte) error {
		// Payload format: {"user_id":..,"status":..,"device":..}
		// Parsed inline; minimal schema since this is a hot path.
		var p struct {
			UserID string `json:"user_id"`
			Status string `json:"status"`
			Device string `json:"device"`
		}
		if err := jsonUnmarshal(data, &p); err != nil {
			return nil
		}
		s.apply(p.UserID, Status(p.Status), p.Device)
		return nil
	})
}

// Stop implements cell.Service.
func (s *Service) Stop(ctx context.Context) error {
	close(s.stop)
	return nil
}

// MarkOnline records userID as online on device.
func (s *Service) MarkOnline(userID, device string) {
	s.apply(userID, StatusOnline, device)
	s.fanoutCrossCell(userID, true, device)
}

// MarkOffline records userID as offline.
func (s *Service) MarkOffline(userID string) {
	s.apply(userID, StatusOffline, "")
	s.fanoutCrossCell(userID, false, "")
}

// fanoutCrossCell publishes a presence event to every known peer.
// Best-effort: a publish failure logs + continues; the peer's
// ApplyInboundPresence is idempotent on (user_id, cell_id, updated_at).
func (s *Service) fanoutCrossCell(userID string, online bool, device string) {
	if s.router == nil {
		return
	}
	peers := s.router.KnownPeers()
	if len(peers) == 0 {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	payload := map[string]any{
		"user_id":    userID,
		"online":     online,
		"platform":   device,
		"cell_id":    s.cell.Cfg.Cell.ID,
		"updated_at": now,
	}
	for _, peer := range peers {
		if err := s.router.PublishCrossCell(context.Background(), peer, "presence", payload); err != nil {
			s.log.Warn().Err(err).Str("peer", peer).Str("user_id", userID).Msg("cross-cell presence publish failed")
		}
	}
}

// Touch updates last-seen without changing status.
func (s *Service) Touch(userID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.state[userID]; ok {
		e.LastSeen = time.Now().UTC()
		e.UpdatedAt = time.Now().UTC()
	}
}

// Get returns the current entry for userID.
func (s *Service) Get(userID string) (*Entry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.state[userID]
	return e, ok
}

// Online returns all currently-online userIDs.
func (s *Service) Online() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []string{}
	for u, e := range s.state {
		if e.Status == StatusOnline {
			out = append(out, u)
		}
	}
	return out
}

func (s *Service) apply(userID string, status Status, device string) {
	now := time.Now().UTC()
	s.mu.Lock()
	e, ok := s.state[userID]
	if !ok {
		e = &Entry{UserID: userID}
		s.state[userID] = e
	}
	e.Status = status
	e.Device = device
	e.UpdatedAt = now
	if status == StatusOnline {
		e.LastSeen = now
	}
	s.mu.Unlock()
	s.log.Debug().Str("user", userID).Str("status", string(status)).Msg("presence")
}

// jsonUnmarshal is a thin wrapper to keep the package isolated from
// the encoding/json import in the hot path.
func jsonUnmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
