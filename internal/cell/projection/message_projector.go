// Package projection implements read-model projectors consumed from event streams.
package projection

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/pulse-workspace/pulse/internal/cell/eventsource"
	"github.com/pulse-workspace/pulse/pkg/shared/db"
	sharedevents "github.com/pulse-workspace/pulse/pkg/shared/events"
	"github.com/pulse-workspace/pulse/pkg/shared/logger"
)

// MessageProjector implements eventsource.Projector for the
// `events.messages` stream.
//
// Each TypeMessageSent event carries a JSON-serialised
// message-content body in evt.Data. The projector decodes the
// JSON, extracts the projection row fields, and runs an idempotent
// INSERT.
//
// Naming: the eventsource.Projector interface requires Name() /
// Stream() / Subject(). We follow the eventsource convention
// (`events.<entity>`) so the project's existing subject scheme
// (`message.<msgID>` under `events.messages`) keeps working.
type MessageProjector struct {
	db  db.DBTX
	log logger.Logger
}

// NewMessageProjector constructs the projector. Pass the cell's
// *db.DB (or any DBTX-compatible test double).
func NewMessageProjector(d db.DBTX, log logger.Logger) *MessageProjector {
	return &MessageProjector{
		db:  d,
		log: log.With().Str("component", "projection.messages").Logger(),
	}
}

// Name satisfies eventsource.Projector.
func (p *MessageProjector) Name() string { return "messages" }

// Stream satisfies eventsource.Projector. Matches the NATS stream
// configured in message.Service ("pulse_messages").
func (p *MessageProjector) Stream() string { return "pulse_messages" }

// Subject satisfies eventsource.Projector. Mirrors the
// message.Service.SendMessage subject so the subscriber catches
// every event the publisher emits.
func (p *MessageProjector) Subject() string { return "message.>" }

// Handle decodes the message event payload and applies it to the
// messages projection. Idempotent via ON CONFLICT (msg_id) DO NOTHING
// so a replayed envelope is a no-op.
func (p *MessageProjector) Handle(ctx context.Context, evt sharedevents.Event) error {
	if evt.Type != "MessageSent" {
		// Unknown event types in this stream — skip. We don't want
		// to NAK + redeliver a poison type we can't act on.
		p.log.Debug().Str("type", evt.Type).Msg("skip non-MessageSent event")
		return nil
	}
	var body messageEventBody
	if err := json.Unmarshal(evt.Data, &body); err != nil {
		// Malformed payload — log and drop.
		p.log.Warn().Err(err).Str("event_id", evt.ID).Msg("decode message event body failed")
		return nil
	}
	if body.MsgID == "" {
		p.log.Warn().Str("event_id", evt.ID).Msg("message event missing msg_id")
		return nil
	}
	if err := p.db.Exec(ctx, `
		INSERT INTO messages (msg_id, conv_id, sender_id, content_type, content, seq, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (msg_id) DO NOTHING`,
		body.MsgID, body.ConvID, body.SenderID, body.ContentType, body.Content, body.Seq, evt.Timestamp); err != nil {
		return fmt.Errorf("projection: insert message %s: %w", body.MsgID, err)
	}
	p.log.Debug().
		Str("msg_id", body.MsgID).
		Str("conv_id", body.ConvID).
		Str("from_event", evt.ID).
		Msg("projected message")
	return nil
}

// messageEventBody mirrors the JSON payload that message.Service.SendMessage
// publishes into evt.Data. Keep the field names in sync with
// internal/cell/message/send.go's mustMarshal(evt) call.
type messageEventBody struct {
	MsgID       string         `json:"msgID"`
	ConvID      string         `json:"convID"`
	SenderID    string         `json:"senderID"`
	ContentType int            `json:"contentType"`
	Content     map[string]any `json:"content"`
	Seq         int64          `json:"seq"`
	CreatedAt   string         `json:"createdAt"`
}

// Compile-time assertion: MessageProjector satisfies
// eventsource.Projector so the runner accepts it without a runtime
// check.
var _ eventsource.Projector = (*MessageProjector)(nil)
