// Package message is the message domain service.
//
// Messages flow through the cell as events on the NATS JetStream subject
// "message.>". For Week 5-6 we support 1-on-1 and group messages with
// a synchronous projection write so reads are immediately consistent.
//
// Week 7-8 adds async projections + outbox for cross-cell fan-out.
package message

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/vmihailenco/msgpack/v5"

	"github.com/pulse-im/pulse/internal/cell"
	"github.com/pulse-im/pulse/pkg/shared/db"
	"github.com/pulse-im/pulse/pkg/shared/logger"
	"github.com/pulse-im/pulse/pkg/shared/metrics"
	"github.com/pulse-im/pulse/pkg/shared/nats"
	"github.com/pulse-im/pulse/pkg/core"
)

// Message is the projection (read model) for a single message.
type Message struct {
	MsgID       string         `json:"msgID"           msgpack:"msgID"`
	ConvID      string         `json:"convID"          msgpack:"convID"`
	SenderID    string         `json:"senderID"        msgpack:"senderID"`
	ContentType int            `json:"contentType"     msgpack:"contentType"`
	Content     map[string]any `json:"content"         msgpack:"content"`
	Seq         int64          `json:"seq"             msgpack:"seq"`
	CreatedAt   time.Time      `json:"createdAt"       msgpack:"createdAt"`
	RecalledAt  *time.Time     `json:"recalledAt,omitempty" msgpack:"recalledAt,omitempty"`
}

// Service is the message domain service.
type Service struct {
	cell       *cell.Cell
	log        logger.Logger
	db         db.DBTX
	nats       *nats.Client
	compliance      complianceChecker
	router          crossCellPublisher // optional; nil-safe — when nil, no fan-out
	inboundListener InboundMessageListener
	cache           *recentCache     // bounded LRU for read-after-write
	mu              sync.Mutex
	convSeq         map[string]int64 // convID → last seq per conversation
}

// InboundMessageListener receives notifications when a cross-cell message arrives.
// Gateways implement this to push real-time WebSocket frames to local users.
type InboundMessageListener interface {
	OnInboundMessage(msg *Message)
}

// complianceChecker is the subset of compliance.Service that message needs.
// Defined locally to keep message testable without dragging in the full
// compliance dependency tree at test time. *compliance.Service satisfies this.
type complianceChecker interface {
	IsHeld(ctx context.Context, scope core.ComplianceScope, target string) (bool, error)
}

// crossCellPublisher is the subset of routing.Service that message uses
// for cross-cell fan-out. Defined locally so the message package
// doesn't have to import routing (which imports message for the inbound
// payload type) — keeps the dependency cycle impossible.
//
// *routing.Service satisfies this via PublishCrossCell & RouteToParticipants.
type crossCellPublisher interface {
	PublishCrossCell(ctx context.Context, peerCell, kind string, payload any) error
	RouteToParticipants(ctx context.Context, participants []string, kind string, payload any) error
}

// NewService creates the message service without a compliance reference.
// Use SetCompliance after construction to wire the hold-check dependency.
// (Order matters in cmd/pulse/main.go: compliance.NewService may need to be
// constructed after message.NewService because other paid services depend on
// the cell bundle but compliance depends on license.Gate which is constructed
// after the cell bundle.)
func NewService(c *cell.Cell) *Service {
	return &Service{
		cell:    c,
		log:     c.Log.With().Str("service", "message").Logger(),
		db:      c.DB,
		nats:    c.NATS,
		cache:   newRecentCache(recentMessagesCap),
		convSeq: make(map[string]int64),
	}
}

// SetCompliance wires the compliance checker used for legal-hold lookups on
// recall/delete. Pass nil to disable hold checks (OSS-only deployments).
func (s *Service) SetCompliance(cc complianceChecker) {
	s.compliance = cc
}

// SetRouter wires the cross-cell routing publisher. When configured,
// SendMessage fans out persisted messages to peer cells via the
// "pulse.route.<peerCell>" NATS subject. Pass nil for single-cell deployments.
func (s *Service) SetRouter(r crossCellPublisher) {
	s.router = r
}

// SetInboundListener wires the listener for cross-cell inbound messages.
func (s *Service) SetInboundListener(l InboundMessageListener) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inboundListener = l
}

// Name implements cell.Service.
func (s *Service) Name() string { return "message" }

// Start ensures the message stream exists.
func (s *Service) Start(ctx context.Context) error {
	if err := s.nats.EnsureStream(ctx, "pulse_messages", []string{"message.>"}); err != nil {
		return fmt.Errorf("ensure message stream: %w", err)
	}
	s.log.Info().Msg("message service started")
	return nil
}

// Stop shuts the service down.
func (s *Service) Stop(ctx context.Context) error {
	return nil
}

// ErrNotFound is returned when a message does not exist.
var ErrNotFound = errors.New("message not found")

// ErrRecalled is returned when reading a recalled message.
var ErrRecalled = errors.New("message recalled")

// ErrHeld is returned when a message delete/recall is blocked by an active
// legal hold on the conversation. See compliance.Service.IsHeld.
var ErrHeld = errors.New("message held by legal hold")

// SendMessageInput is the input for SendMessage.
type SendMessageInput struct {
	ConvID      string         `json:"convID"`
	SenderID    string         `json:"senderID"`
	ContentType int            `json:"contentType"`
	Content     map[string]any `json:"content"`
	ClientMsgID string         `json:"clientMsgID"` // idempotency key from client
}

// MakeConvID returns the canonical conversation ID for a 1-on-1 or group.
func MakeConvID(participants []string, groupID string) string {
	if groupID != "" {
		return "g_" + groupID
	}
	if len(participants) != 2 {
		return "" // 1-on-1 only
	}
	if participants[0] < participants[1] {
		return "si_" + participants[0] + "_" + participants[1]
	}
	return "si_" + participants[1] + "_" + participants[0]
}

// mustMarshal serializes v into binary msgpack format matching shared/events.Event.
func mustMarshal(v any) []byte {
	b, err := msgpack.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// nextSeq allocates the next monotonic seq for a conversation.
// In production with PostgreSQL, it performs an atomic UPSERT on conversations.max_seq
// to guarantee gapless monotonic ordering across node reboots and replicas.
// If database execution is unavailable (e.g. unit test mocks or offline), it falls back
// to the in-memory counter.
func (s *Service) nextSeq(ctx context.Context, convID string) int64 {
	if s.db != nil {
		var seq int64
		err := s.db.QueryRow(ctx,
			`INSERT INTO conversations (conv_id, conv_type, participants, max_seq, created_at, updated_at)
			 VALUES ($1, 1, '{}', 1, NOW(), NOW())
			 ON CONFLICT (conv_id) DO UPDATE
			 SET max_seq = conversations.max_seq + 1, updated_at = NOW()
			 RETURNING max_seq`,
			convID,
		).Scan(&seq)
		if err == nil {
			s.mu.Lock()
			s.convSeq[convID] = seq
			s.mu.Unlock()
			return seq
		}
		// Fall back to in-memory counter if query fails (e.g. in tests using fakes)
		s.log.Warn().Err(err).Str("conv_id", convID).Msg("db sequence increment failed, falling back to in-memory counter")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.convSeq[convID]++
	return s.convSeq[convID]
}

// checkHold returns ErrHeld if the conversation is under an active legal hold.
// Returns nil if compliance is not loaded (OSS-only mode) or if the hold check
// returns (false, nil).
func (s *Service) checkHold(ctx context.Context, convID string) error {
	if s.compliance == nil {
		// OSS-only deployments have no compliance module; do not increment
		// the counter (a single 0 label set would be misleading).
		return nil
	}
	held, err := s.compliance.IsHeld(ctx, core.ScopeConv, convID)
	if err != nil {
		// Per audit P3: ErrNotLicensed means the compliance module is not
		// entitled; treat as no hold (do not silently block deletes).
		if errors.Is(err, core.ErrNotLicensed) {
			metrics.IncComplianceHoldCheck(metrics.ComplianceHoldNoHold)
			return nil
		}
		metrics.IncComplianceHoldCheck(metrics.ComplianceHoldError)
		return fmt.Errorf("hold check: %w", err)
	}
	if held {
		metrics.IncComplianceHoldCheck(metrics.ComplianceHoldActive)
		return ErrHeld
	}
	metrics.IncComplianceHoldCheck(metrics.ComplianceHoldNoHold)
	return nil
}
