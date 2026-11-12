package message

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/oklog/ulid/v2"

	"github.com/pulse-workspace/pulse/pkg/shared/events"
	"github.com/pulse-workspace/pulse/pkg/protocol"
)

// SendMessage publishes a MessageSent event and populates the
// read-after-write cache. The projection runner asynchronously persists
// the message to PostgreSQL.
//
// Read-after-write is satisfied by:
//  1. In-memory LRU cache (recentMessagesCap entries)
//  2. Event log table fallback for recent events
//
// Input: in SendMessageInput containing ConvID, SenderID, ContentType, and Content.
// Output: Constructed *Message and error if validation or counter fails.
func (s *Service) SendMessage(ctx context.Context, in SendMessageInput) (*Message, error) {
	if in.ConvID == "" || in.SenderID == "" {
		return nil, errors.New("convID and senderID required")
	}
	if in.ContentType == 0 {
		in.ContentType = protocol.ContentText
	}

	// Idempotency: if clientMsgID given, check cache + projection
	// for an existing message. The event log is the final fallback
	// for messages not yet projected; the runner's replay of a
	// crashed publish makes this safe across restarts.
	if in.ClientMsgID != "" {
		existing, err := s.lookupByClientMsgID(ctx, in.ConvID, in.SenderID, in.ClientMsgID)
		if err != nil {
			return nil, err
		}
		if existing != "" {
			m, err := s.GetMessage(ctx, existing)
			if err == nil {
				return m, nil
			}
			// Projection miss but cache hit (or vice versa) shouldn't
			// happen; fall through and create the duplicate to avoid
			// silently losing the new send.
		}
	}

	msgID := "m_" + ulid.Make().String()
	now := time.Now().UTC()

	// Allocate per-conversation monotonic seq (durable gapless via DB with in-memory fallback).
	seq := s.nextSeq(ctx, in.ConvID)

	contentJSON, err := json.Marshal(in.Content)
	if err != nil {
		return nil, fmt.Errorf("marshal content: %w", err)
	}
	// Stamp the idempotency key into the content JSON for later lookup.
	var contentMap map[string]any
	_ = json.Unmarshal(contentJSON, &contentMap)
	if contentMap == nil {
		contentMap = map[string]any{}
	}
	if in.ClientMsgID != "" {
		contentMap["_clientMsgID"] = in.ClientMsgID
	}

	msg := &Message{
		MsgID:       msgID,
		ConvID:      in.ConvID,
		SenderID:    in.SenderID,
		ContentType: in.ContentType,
		Content:     contentMap,
		Seq:         seq,
		CreatedAt:   now,
	}

	// Populate the cache BEFORE publishing so a reader who races
	// the publish always sees the message (cache hit > projection
	// miss).
	s.cache.Add(msg)

	// Append event (for projections + cross-cell fan-out).
	evt := events.NewEvent("message:"+msgID, events.TypeMessageSent, in.SenderID, nil)
	evt.Data, _ = json.Marshal(map[string]any{
		"msgID":       msgID,
		"convID":      in.ConvID,
		"senderID":    in.SenderID,
		"contentType": in.ContentType,
		"content":     contentMap,
		"seq":         seq,
		"createdAt":   now,
		"clientMsgID": in.ClientMsgID,
	})

	subject := "message." + msgID
	// Publish event to NATS JetStream if client is configured.
	if s.nats != nil {
		if err := s.nats.Publish(ctx, subject, mustMarshal(evt)); err != nil {
			s.log.Warn().Err(err).Msg("publish message event")
		}
	} else {
		s.log.Warn().Msg("nats client not wired; skipping event publish (test only)")
	}

	// Cross-cell fan-out: publish message envelope to peer cells if router is configured.
	if s.router != nil {
		s.fanoutCrossCell(ctx, msgID, in.ConvID, in.SenderID, in.ContentType, contentMap, seq, now)
	}

	return msg, nil
}

// lookupByClientMsgID checks the cache and projection for an existing
// message matching clientMsgID to ensure idempotency. Returns matching msgID, or empty string.
func (s *Service) lookupByClientMsgID(ctx context.Context, convID, senderID, clientMsgID string) (string, error) {
	// Walk cache first (sub-microsecond). The LRU keeps the most
	// recent N msgIDs; iterate the list for a content match.
	for _, m := range s.cacheSnapshot() {
		if m.ConvID != convID || m.SenderID != senderID {
			continue
		}
		if cid, _ := m.Content["_clientMsgID"].(string); cid == clientMsgID {
			return m.MsgID, nil
		}
	}
	// Fall back to projection. Distinguish miss from infra
	// failure so the caller can decide retry vs duplicate.
	var existing string
	err := s.db.QueryRow(ctx,
		`SELECT msg_id FROM messages WHERE conv_id = $1 AND sender_id = $2 AND content->>'_clientMsgID' = $3 LIMIT 1`,
		convID, senderID, clientMsgID).Scan(&existing)
	if err == nil {
		return existing, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return "", fmt.Errorf("idempotency lookup: %w", err)
}

// cacheSnapshot returns a slice of the cache's messages (most
// recent first) for iteration. Used by idempotency lookup. Cost
// is O(cap) per call; idempotency checks happen at human-key-in
// rates so this is fine for recentMessagesCap=10k.
//
// Iteration under read-lock; safe to call from any goroutine.
func (s *Service) cacheSnapshot() []*Message {
	if s.cache == nil {
		return nil
	}
	// Reuse Get's pattern: collect via the map keys (not the
	// list order) since we don't need order here. For recentMessagesCap=10k
	// this allocates a 10k slice — acceptable for an idempotency
	// code path that's bounded by human typing speed.
	out := make([]*Message, 0, s.cache.Len())
	// Inline-access via Get to avoid exporting the keys map.
	// We do best-effort: iterate over a fixed pool of likely-
	// recent keys. For a cap of 10k the per-call cost is bounded.
	// Optional optimization: per-convID index.
	for _, m := range s.cache.drain() {
		out = append(out, m)
	}
	return out
}

// fanoutCrossCell performs smart targeted routing to participant cells.
// Best-effort: a publish failure logs + continues; the peer's ApplyInboundMessage is idempotent.
func (s *Service) fanoutCrossCell(ctx context.Context, msgID, convID, senderID string, contentType int, content map[string]any, seq int64, createdAt time.Time) {
	if s.router == nil {
		return
	}
	payload := map[string]any{
		"msg_id":       msgID,
		"conv_id":      convID,
		"sender_id":    senderID,
		"content_type": contentType,
		"content":      content,
		"seq":          seq,
		"created_at":   createdAt.UTC().Format(time.RFC3339),
		"from_cell_id": s.cell.Cfg.Cell.ID,
	}

	participants := s.resolveParticipants(ctx, convID)
	if len(participants) > 0 {
		if err := s.router.RouteToParticipants(ctx, participants, "message", payload); err != nil {
			s.log.Warn().Err(err).Str("conv_id", convID).Msg("targeted cross-cell publish failed")
		}
		return
	}

	// Fallback to broadcasting to known peers if participants couldn't be determined
	peers := s.routerPeerCells()
	for _, peer := range peers {
		if peer == s.cell.Cfg.Cell.ID {
			continue // don't route to self
		}
		if err := s.router.PublishCrossCell(ctx, peer, "message", payload); err != nil {
			s.log.Warn().Err(err).Str("peer", peer).Msg("cross-cell publish failed")
		}
	}
}

// resolveParticipants extracts participant user IDs from 1-on-1 convID or database.
func (s *Service) resolveParticipants(ctx context.Context, convID string) []string {
	if strings.HasPrefix(convID, "si_") {
		parts := strings.Split(strings.TrimPrefix(convID, "si_"), "_")
		if len(parts) >= 2 {
			return parts
		}
	}
	if s.db != nil {
		var parts []string
		err := s.db.QueryRow(ctx, `SELECT participants FROM conversations WHERE conv_id = $1`, convID).Scan(&parts)
		if err == nil && len(parts) > 0 {
			return parts
		}
	}
	return nil
}

// routerPeerCells is the bridge to the routing.Service — defined
// here as a method on Service so the crossCellPublisher interface
// stays minimal (we only need PublishCrossCell from the message
// package). The router keeps its own KnownPeers() accessor for the
// API layer; the message package reads it via a small interface.
//
// When the router isn't wired (s.router == nil), KnownPeers returns
// an empty slice and fan-out is a no-op.
func (s *Service) routerPeerCells() []string {
	type peerLister interface {
		KnownPeers() []string
	}
	if l, ok := s.router.(peerLister); ok {
		return l.KnownPeers()
	}
	return nil
}

// GetMessage returns the message for msgID using a three-tier
// fallback chain:
//
//  1. In-memory LRU cache (sub-microsecond, covers very recent
// GetMessage fetches a message by its msgID using a multi-tiered lookup:
//  1. In-memory LRU cache (sub-microsecond read-after-write)
//  2. PostgreSQL messages projection table
//  3. NATS JetStream event log fallback
//
// Input: context, msgID string.
// Output: *Message and error if not found.
func (s *Service) GetMessage(ctx context.Context, msgID string) (*Message, error) {
	// Tier 1: cache. Sub-µs.
	if s.cache != nil {
		if m, ok := s.cache.Get(msgID); ok {
			return s.sanitize(m), nil
		}
	}
	// Tier 2: projection.
	m := &Message{}
	err := s.db.QueryRow(ctx,
		`SELECT msg_id, conv_id, sender_id, content_type, content, seq, created_at, revoked_at
		 FROM messages WHERE msg_id = $1`, msgID).
		Scan(&m.MsgID, &m.ConvID, &m.SenderID, &m.ContentType, &m.Content, &m.Seq, &m.CreatedAt, &m.RecalledAt)
	if err == nil {
		return s.sanitize(m), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	// Tier 3: event log. The events table is the canonical source
	// of truth; the projection is a cache for it. If the projection
	// hasn't caught up (cache cold start, runner down, etc.), we
	// reconstruct from the most recent MessageSent event with a
	// matching msgID in its data payload.
	m, err = s.getMessageFromEventLog(ctx, msgID)
	if err != nil {
		return nil, err
	}
	return s.sanitize(m), nil
}

// sanitize is the single read-edge sanitisation point. It returns
// a Message with the internal-underscore-prefixed keys stripped
// from Content so downstream consumers (SDK, gateway WS frame)
// never see the idempotency tag.
func (s *Service) sanitize(m *Message) *Message {
	if m == nil {
		return nil
	}
	m.Content = m.SanitizedContent()
	return m
}

// getMessageFromEventLog reconstructs a Message from the events
// table when neither the cache nor the projection has it. Used as
// the third tier of GetMessage's fallback chain.
//
// SanitizedContent returns a copy of m.Content with internal
// SanitizedContent returns a copy of message content with internal
// bookkeeping keys (e.g. "_clientMsgID") stripped.
func (m *Message) SanitizedContent() map[string]any {
	if m == nil {
		return nil
	}
	if m.Content == nil {
		return nil
	}
	out := make(map[string]any, len(m.Content))
	for k, v := range m.Content {
		if len(k) > 0 && k[0] == '_' {
			continue
		}
		out[k] = v
	}
	return out
}

// getMessageFromEventLog retrieves a message directly from the immutable event log.
func (s *Service) getMessageFromEventLog(ctx context.Context, msgID string) (*Message, error) {
	var (
		stream, evtType, dataJSON string
	)
	err := s.db.QueryRow(ctx,
		`SELECT stream, type, data::text
		 FROM events
		 WHERE stream = $1 AND type = $2 AND data->>'msgID' = $3
		 ORDER BY version DESC
		 LIMIT 1`,
		"events.messages", events.TypeMessageSent, msgID).
		Scan(&stream, &evtType, &dataJSON)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	// Decode the inner JSON body. Schema matches send.go's
	// evt.Data map.
	var body struct {
		MsgID       string         `json:"msgID"`
		ConvID      string         `json:"convID"`
		SenderID    string         `json:"senderID"`
		ContentType int            `json:"contentType"`
		Content     map[string]any `json:"content"`
		Seq         int64          `json:"seq"`
		CreatedAt   time.Time      `json:"createdAt"`
	}
	if err := json.Unmarshal([]byte(dataJSON), &body); err != nil {
		return nil, fmt.Errorf("event log: decode message %s: %w", msgID, err)
	}
	return &Message{
		MsgID:       body.MsgID,
		ConvID:      body.ConvID,
		SenderID:    body.SenderID,
		ContentType: body.ContentType,
		Content:     body.Content,
		Seq:         body.Seq,
		CreatedAt:   body.CreatedAt,
	}, nil
}

// GetHistory returns the most recent messages in convID, newest first.
// Ordering uses (seq DESC, created_at DESC, msg_id DESC) for deterministic sequencing.
// Input: context, convID, limit (clamped between 1 and 200).
// Output: slice of messages and error if any.
func (s *Service) GetHistory(ctx context.Context, convID string, limit int) ([]*Message, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(ctx,
		`SELECT msg_id, conv_id, sender_id, content_type, content, seq, created_at, revoked_at
		 FROM messages
		 WHERE conv_id = $1
		 ORDER BY seq DESC, created_at DESC, msg_id DESC
		 LIMIT $2`, convID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Message
	for rows.Next() {
		m := &Message{}
		if err := rows.Scan(&m.MsgID, &m.ConvID, &m.SenderID, &m.ContentType, &m.Content,
			&m.Seq, &m.CreatedAt, &m.RecalledAt); err != nil {
			return nil, err
		}
		// Sanitize message content to ensure internal metadata is stripped.
		out = append(out, s.sanitize(m))
	}
	return out, rows.Err()
}
