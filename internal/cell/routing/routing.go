// Package routing implements cross-cell message routing.
//
// In Pulse, every user and group is owned by exactly one cell. When a
// message is sent in a conversation whose participants span multiple
// cells, the originating cell publishes the event to NATS subject
// "pulse.route.<targetCell>" and the target cell's routing service
// picks it up + fans out to connected sessions.
package routing

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/pulse-workspace/pulse/internal/cell"
	"github.com/pulse-workspace/pulse/pkg/shared/logger"
	"github.com/pulse-workspace/pulse/pkg/shared/nats"
)

// DeliveryTarget identifies one cell + its subjects.
type DeliveryTarget struct {
	CellID  string
	Subject string // NATS subject, e.g. "user.u1" or "group.g1.events"
}

// Envelope is the cross-cell message payload.
type Envelope struct {
	FromCellID string          `json:"from"`
	ToCellID   string          `json:"to"`
	Kind       string          `json:"kind"` // "message" | "presence" | "reaction"
	Payload    json.RawMessage `json:"payload"`
	SentAt     time.Time       `json:"sent_at"`
}

// HandlerFunc is called for each routed envelope received from another
// cell. Returning a non-nil error signals to JetStream that the
// envelope should be NAK'd and redelivered (handles transient DB
// failures; the at-least-once delivery paired with our
// ON-CONFLICT-DO-NOTHING idempotency gives effective at-most-once).
//
// Returning nil signals Ack — the envelope is fully handled. A nil
// return for a permanent failure (e.g. malformed payload that won't
// retry) is the caller's responsibility — typically that means
// logging + returning nil to drop, OR routing to a dead-letter
// stream (future work).
type HandlerFunc func(env Envelope) error

// Service is the cross-cell router.
type Service struct {
	cell  *cell.Cell
	log   logger.Logger
	nats  *nats.Client
	mu    sync.Mutex
	peers map[string]bool // peer cell IDs known to this cluster

	onMessage  HandlerFunc
	onPresence HandlerFunc
	onReaction HandlerFunc

	dir Directory
}

// NewService constructs the router.
func NewService(c *cell.Cell, onMessage, onPresence, onReaction HandlerFunc) *Service {
	return &Service{
		cell:       c,
		log:        c.Log.With().Str("service", "routing").Logger(),
		nats:       c.NATS,
		peers:      map[string]bool{},
		onMessage:  onMessage,
		onPresence: onPresence,
		onReaction: onReaction,
		dir:        NewMemoryDirectory(),
	}
}

// SetDirectory configures the active user directory for targeted routing.
func (s *Service) SetDirectory(d Directory) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dir = d
}

// Directory returns the configured directory.
func (s *Service) Directory() Directory {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dir
}

// Name implements cell.Service.
func (s *Service) Name() string { return "routing" }

// Start subscribes to inbound cross-cell events.
func (s *Service) Start(ctx context.Context) error {
	if err := s.nats.EnsureStream(ctx, "events_routing", []string{"pulse.route.>"}); err != nil {
		return err
	}
	// Subscribe to the wildcard "pulse.route.<cellID>.>" (each cell listens
	// to its own bucket). Publisher emits subjects
	// "pulse.route.<peerCellID>.<kind>" so the wildcard matches the
	// trailing kind token. The 3-token subject produced by an earlier
	// iteration never matched; see docs/en/operations/cross-cell-routing.md
	// § Topology for the current canonical wire.
	subject := "pulse.route." + s.cell.Cfg.Cell.ID + ".>"
	if err := s.nats.Subscribe(ctx, "events_routing", "routing-"+s.cell.Cfg.Cell.ID, subject, func(data []byte) error {
		var env Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			// Malformed envelope — drop silently (returning nil
			// Acks so JetStream doesn't redeliver a poison payload).
			// Future work: route to a DLQ stream.
			return nil
		}
		return s.dispatch(env)
	}); err != nil {
		return err
	}
	s.log.Info().Str("subject", subject).Msg("routing listening")
	return nil
}

// Stop implements cell.Service.
func (s *Service) Stop(ctx context.Context) error {
	return nil
}

// PublishCrossCell sends an envelope to peerCell via NATS.
//
// Subject format: "pulse.route.<peerCellID>.<kind>" — must match
// the consumer's filter "pulse.route.<cellID>.>". The trailing
// <kind> token keeps per-kind subjects (so future per-kind
// filtering is possible) and is the key fix for the C1 subject
// mismatch that landed in the first review pass.
func (s *Service) PublishCrossCell(ctx context.Context, peerCell string, kind string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("cross-cell: marshal payload: %w", err)
	}
	env := Envelope{
		FromCellID: s.cell.Cfg.Cell.ID,
		ToCellID:   peerCell,
		Kind:       kind,
		Payload:    body,
		SentAt:     time.Now().UTC(),
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("cross-cell: marshal envelope: %w", err)
	}
	subject := fmt.Sprintf("pulse.route.%s.%s", peerCell, kind)
	return s.nats.Publish(ctx, subject, raw)
}

// RouteToParticipants performs targeted multicast to only those peer cells
// hosting at least one participant of the conversation.
// If the directory lookup is empty or offline, no cross-cell packets are emitted.
// If the directory is nil, it falls back to broadcasting to all known peers.
func (s *Service) RouteToParticipants(ctx context.Context, participants []string, kind string, payload any) error {
	s.mu.Lock()
	dir := s.dir
	s.mu.Unlock()

	if dir == nil || len(participants) == 0 {
		return s.broadcastToPeers(ctx, kind, payload)
	}

	targetCells, err := dir.LookupParticipants(ctx, participants)
	if err != nil {
		s.log.Warn().Err(err).Msg("directory lookup failed; falling back to broadcast")
		return s.broadcastToPeers(ctx, kind, payload)
	}

	if len(targetCells) == 0 {
		// All participants are either on the local cell or offline.
		// Avoid wasteful network broadcast.
		return nil
	}

	localCell := s.cell.Cfg.Cell.ID
	var lastErr error
	for _, target := range targetCells {
		if target == localCell {
			continue // Delivered locally, no cross-cell NATS publish needed
		}
		if err := s.PublishCrossCell(ctx, target, kind, payload); err != nil {
			s.log.Warn().Err(err).Str("target_cell", target).Msg("targeted cross-cell publish failed")
			lastErr = err
		}
	}
	return lastErr
}

// broadcastToPeers sends to all known peer cells (legacy broadcast fallback).
func (s *Service) broadcastToPeers(ctx context.Context, kind string, payload any) error {
	var lastErr error
	for _, peer := range s.KnownPeers() {
		if peer == s.cell.Cfg.Cell.ID {
			continue
		}
		if err := s.PublishCrossCell(ctx, peer, kind, payload); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// RegisterPeer adds peerCellID to the known-peer list.
func (s *Service) RegisterPeer(peerCellID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.peers[peerCellID] = true
}

// KnownPeers returns the list of peer cell IDs.
func (s *Service) KnownPeers() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.peers))
	for p := range s.peers {
		out = append(out, p)
	}
	return out
}

// dispatch routes the envelope to the matching handler and surfaces
// its error. Returning the handler's error propagates to the NATS
// Subscribe callback so JetStream NAKs + redelivers on transient
// failure (C2 fix).
func (s *Service) dispatch(env Envelope) error {
	switch env.Kind {
	case "message":
		if s.onMessage == nil {
			s.log.Warn().Msg("message handler not wired; dropping envelope")
			return nil
		}
		return s.onMessage(env)
	case "presence":
		if s.onPresence == nil {
			s.log.Warn().Msg("presence handler not wired; dropping envelope")
			return nil
		}
		return s.onPresence(env)
	case "reaction":
		if s.onReaction == nil {
			s.log.Warn().Msg("reaction handler not wired; dropping envelope")
			return nil
		}
		return s.onReaction(env)
	default:
		s.log.Warn().Str("kind", env.Kind).Msg("unknown envelope kind")
		return nil
	}
}
