package message

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// inboundApplyTimeout caps the per-inbound-message DB write to prevent slow DB writes
// from blocking the routing consumer goroutine.
const inboundApplyTimeout = 5 * time.Second

// InboundMessagePayload is the wire shape routed cross-cell for messages.
// Lives in the message package (not routing/) so the message.Service
// is the only writer of the messages table — keeps the projection
// invariant inside the domain that owns it. The routing layer decodes
// envelopes into this type; the domain layer owns the row.
type InboundMessagePayload struct {
	MsgID       string         `json:"msg_id"`
	ConvID      string         `json:"conv_id"`
	SenderID    string         `json:"sender_id"`
	ContentType int            `json:"content_type"`
	Content     map[string]any `json:"content"`
	Seq         int64          `json:"seq"`
	CreatedAt   string         `json:"created_at"`
	FromCellID  string         `json:"from_cell_id"`
}

// ApplyInboundMessage persists a foreign-cell message into the local
// messages projection. Called from the cross-cell routing service
// when a peer cell publishes a MessageSent event whose conversation
// touches this cell.
//
// Idempotency: the row is INSERTed with the original msg_id; an
// existing row (already-projected replay, second-cell-double-publish)
// is silently dropped via ON CONFLICT DO NOTHING. The next
// GetMessage / GetHistory read sees the row exactly once regardless
// of how many times the envelope is delivered.
//
// Returns an error when the underlying DB write fails or the
// per-call timeout (inboundApplyTimeout) trips — the routing layer
// NAKs the envelope so JetStream redelivers. ON CONFLICT DO NOTHING
// makes the redelivery a no-op once the underlying issue clears.
//
// The function lives here (not in routing/) so the message.Service
// is the only writer of the messages table — keeps the projection
// invariant inside the domain that owns it.
func (s *Service) ApplyInboundMessage(p InboundMessagePayload) error {
	if p.MsgID == "" || p.ConvID == "" {
		return errors.New("inbound: msg_id and conv_id required")
	}
	createdAt, err := time.Parse(time.RFC3339, p.CreatedAt)
	if err != nil {
		// Some senders may omit timezone; fall back to the wall clock.
		createdAt = time.Now().UTC()
	}
	ctx, cancel := context.WithTimeout(context.Background(), inboundApplyTimeout)
	defer cancel()
	if err := s.db.Exec(ctx, `
		INSERT INTO messages (msg_id, conv_id, sender_id, content_type, content, seq, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (msg_id) DO NOTHING`,
		p.MsgID, p.ConvID, p.SenderID, p.ContentType, p.Content, p.Seq, createdAt); err != nil {
		return fmt.Errorf("inbound: insert: %w", err)
	}

	msg := &Message{
		MsgID:       p.MsgID,
		ConvID:      p.ConvID,
		SenderID:    p.SenderID,
		ContentType: p.ContentType,
		Content:     p.Content,
		Seq:         p.Seq,
		CreatedAt:   createdAt,
	}
	s.cache.Add(msg)

	s.mu.Lock()
	listener := s.inboundListener
	s.mu.Unlock()
	if listener != nil {
		listener.OnInboundMessage(msg)
	}

	s.log.Info().
		Str("msg_id", p.MsgID).
		Str("from_cell", p.FromCellID).
		Msg("applied inbound message")
	return nil
}
