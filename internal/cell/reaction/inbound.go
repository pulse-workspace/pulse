package reaction

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// inboundApplyTimeout caps the per-inbound-reaction write to prevent slow DB writes from blocking the routing consumer.
const inboundApplyTimeout = 5 * time.Second

// InboundReactionPayload is the wire shape routed cross-cell for
// reactions. Lives in the reaction package so the domain owns the
// row insert; routing decodes envelopes into this type.
type InboundReactionPayload struct {
	MsgID    string `json:"msg_id"`
	UserID   string `json:"user_id"`
	Reaction string `json:"reaction"`
	// Add distinguishes an insert (true) from a remove (false). The
	// inbound apply mirrors the originating cell's operation.
	Add    bool   `json:"add"`
	CellID string `json:"cell_id"`
}

// ApplyInboundReaction persists a foreign-cell reaction (or removal)
// into the local reactions table. Called from the cross-cell
// routing service.
//
// Idempotency: the reactions table has a composite PK on
// (msg_id, user_id, reaction_type); an add duplicate is silently
// ignored via ON CONFLICT DO NOTHING. A remove on a non-existent
// row is a 0-row delete (no-op).
func (s *Service) ApplyInboundReaction(p InboundReactionPayload) error {
	if p.MsgID == "" || p.UserID == "" || p.Reaction == "" {
		return errors.New("inbound: msg_id, user_id and reaction required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), inboundApplyTimeout)
	defer cancel()
	if p.Add {
		if err := s.db.Exec(ctx, `
			INSERT INTO reactions (msg_id, user_id, reaction_type, created_at)
			VALUES ($1, $2, $3, NOW())
			ON CONFLICT (msg_id, user_id, reaction_type) DO NOTHING`,
			p.MsgID, p.UserID, p.Reaction); err != nil {
			return fmt.Errorf("inbound: insert reaction: %w", err)
		}
	} else {
		if err := s.db.Exec(ctx, `
			DELETE FROM reactions
			WHERE msg_id = $1 AND user_id = $2 AND reaction_type = $3`,
			p.MsgID, p.UserID, p.Reaction); err != nil {
			return fmt.Errorf("inbound: delete reaction: %w", err)
		}
	}
	s.log.Info().
		Str("msg_id", p.MsgID).
		Str("user_id", p.UserID).
		Str("reaction", p.Reaction).
		Bool("add", p.Add).
		Str("from_cell", p.CellID).
		Msg("applied inbound reaction")
	return nil
}
