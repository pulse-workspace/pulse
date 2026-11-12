package message

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/pulse-workspace/pulse/pkg/shared/events"
)

// RecallMessage marks a message as revoked and publishes a MessageRecalled event.
// Only the original sender (or admin) may recall. If the conversation is under
// an active legal hold (compliance.IsHeld), returns ErrHeld and does not mutate.
//
// Signature: (ctx, convID, msgID, operatorID). convID is needed to consult the
// legal-hold table before mutating; only the caller knows it.
func (s *Service) RecallMessage(ctx context.Context, convID, msgID, operatorID string) error {
	if err := s.checkHold(ctx, convID); err != nil {
		return err
	}
	err := s.db.Exec(ctx,
		`UPDATE messages
		 SET revoked_at = NOW()
		 WHERE msg_id = $1 AND sender_id = $2 AND revoked_at IS NULL`,
		msgID, operatorID)
	if err != nil {
		return err
	}

	evt := events.NewEvent("message:"+msgID, events.TypeMessageRecalled, operatorID, nil)
	evt.Data, _ = json.Marshal(map[string]any{"msgID": msgID, "operatorID": operatorID, "convID": convID})
	if err := s.nats.Publish(ctx, "message."+msgID, mustMarshal(evt)); err != nil {
		s.log.Warn().Err(err).Msg("publish recall event")
	}
	return nil
}

// DeleteMessage hard-deletes a message from the projection table.
// HARD DELETE — irreversible; breaks audit/event replay. Use with care.
// If the conversation is under an active legal hold, returns ErrHeld and does
// not mutate. If compliance is not loaded (cc==nil), hold check is skipped —
// the OSS-core message service must still be functional when the compliance
// module is not present.
func (s *Service) DeleteMessage(ctx context.Context, convID, msgID string) error {
	if err := s.checkHold(ctx, convID); err != nil {
		return err
	}
	if err := s.db.Exec(ctx, `DELETE FROM messages WHERE msg_id = $1`, msgID); err != nil {
		return fmt.Errorf("delete message: %w", err)
	}
	s.log.Info().Str("convID", convID).Str("msgID", msgID).Msg("message hard-deleted")
	return nil
}
