// Package routing — handlers.go: default HandlerFunc wiring for the
// three envelope kinds (message / presence / reaction).
//
// The cross-cell router publishes to NATS subject "pulse.route.<peer>.
// >" and the receiving cell's Start subscribes to
// "pulse.route.<localCellID>.>" — i.e. each cell listens to its own
// bucket. When an envelope lands in the bucket, dispatch() routes it
// to one of three HandlerFuncs the wiring layer (cmd/pulse/main.go)
// provides.
//
// Production wiring (cmd/pulse/main.go) supplies:
//
//	onMessage  → message.Service.ApplyInboundMessage — insert a
//	             foreign-cell message into the local messages table.
//	onPresence  → presence.Service.ApplyInboundPresence — same flow
//	             for the presence service.
//	onReaction  → reaction.Service.ApplyInboundReaction — same flow
//	             for reactions.
//
// These are wrapped in DefaultHandlers(...) below so the wiring site
// stays a one-liner:
//
//	routerSvc := routing.NewService(c, routing.DefaultHandlers(msgSvc, presSvc, rxnSvc)...)
//
// The handlers themselves do not return errors; they log + swallow so
// a misrouted envelope doesn't kill the subscription. Future work
// will route into a dead-letter NATS stream so a poisoned envelope
// can be retried after a code fix.
package routing

import (
	"encoding/json"
	"fmt"

	"github.com/pulse-workspace/pulse/internal/cell/message"
	"github.com/pulse-workspace/pulse/internal/cell/presence"
	"github.com/pulse-workspace/pulse/internal/cell/reaction"
	"github.com/pulse-workspace/pulse/pkg/shared/logger"
)

// ServiceDeps is the narrow interface the default handlers need to
// apply an inbound envelope. Defined here so the handlers can be
// unit-tested with fakes that satisfy each method.
type ServiceDeps interface {
	// ProjectInboundMessage inserts a foreign-cell message into the
	// local messages table. Idempotent on msg_id so a replayed
	// envelope is a no-op.
	ProjectInboundMessage(payload []byte) error
	// ProjectInboundPresence applies a presence update from another
	// cell to the local presence cache.
	ProjectInboundPresence(payload []byte) error
	// ProjectInboundReaction applies a reaction from another cell.
	ProjectInboundReaction(payload []byte) error
}

// DefaultHandlers builds the three HandlerFuncs that delegate to the
// supplied ServiceDeps. The returned triplet has the order
// (onMessage, onPresence, onReaction) — matching NewService's
// positional argument list so cmd/pulse/main.go can pass them inline.
//
// Logging: every handler logs the FromCellID + ToCellID so an operator
// debugging a missing cross-cell message can grep the cell's logs for
// "routing inbound".
func DefaultHandlers(svc ServiceDeps, log logger.Logger) (HandlerFunc, HandlerFunc, HandlerFunc) {
	onMessage := func(env Envelope) error {
		log.Info().
			Str("from", env.FromCellID).
			Str("to", env.ToCellID).
			Int("bytes", len(env.Payload)).
			Msg("routing inbound: message")
		if err := svc.ProjectInboundMessage(env.Payload); err != nil {
			log.Warn().Err(err).Str("from", env.FromCellID).Msg("project inbound message failed")
			// Return the error so the Subscribe callback can NAK the
			// envelope and JetStream redelivers. ON CONFLICT DO NOTHING
			// in the inbound writer makes the redelivery a no-op once
			// the underlying failure clears.
			return err
		}
		return nil
	}
	onPresence := func(env Envelope) error {
		log.Info().
			Str("from", env.FromCellID).
			Str("to", env.ToCellID).
			Int("bytes", len(env.Payload)).
			Msg("routing inbound: presence")
		if err := svc.ProjectInboundPresence(env.Payload); err != nil {
			log.Warn().Err(err).Str("from", env.FromCellID).Msg("project inbound presence failed")
			return err
		}
		return nil
	}
	onReaction := func(env Envelope) error {
		log.Info().
			Str("from", env.FromCellID).
			Str("to", env.ToCellID).
			Int("bytes", len(env.Payload)).
			Msg("routing inbound: reaction")
		if err := svc.ProjectInboundReaction(env.Payload); err != nil {
			log.Warn().Err(err).Str("from", env.FromCellID).Msg("project inbound reaction failed")
			return err
		}
		return nil
	}
	return onMessage, onPresence, onReaction
}

// ConcreteServiceDeps composes the three real services into a single
// ServiceDeps that the default handlers can use. The wiring site in
// cmd/pulse/main.go constructs this once and feeds it to
// DefaultHandlers.
//
// Each Project* method delegates to the corresponding cell service after
// the payload is JSON-decoded into the domain's typed payload struct.
// The wire shape lives in the domain package (message.InboundMessagePayload
// etc.) so the domain owns the row insert.
type ConcreteServiceDeps struct {
	Msg  *message.Service
	Pres *presence.Service
	Rxn  *reaction.Service
}

// NewConcreteServiceDeps is the constructor wiring site.
func NewConcreteServiceDeps(msg *message.Service, pres *presence.Service, rxn *reaction.Service) *ConcreteServiceDeps {
	return &ConcreteServiceDeps{Msg: msg, Pres: pres, Rxn: rxn}
}

// ProjectInboundMessage decodes + hands off to message.Service.
func (c *ConcreteServiceDeps) ProjectInboundMessage(payload []byte) error {
	var p message.InboundMessagePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return fmt.Errorf("decode message payload: %w", err)
	}
	if p.MsgID == "" || p.ConvID == "" {
		return fmt.Errorf("invalid message payload (missing msg_id or conv_id)")
	}
	if c.Msg == nil {
		return fmt.Errorf("message service not wired")
	}
	return c.Msg.ApplyInboundMessage(p)
}

// ProjectInboundPresence decodes + hands off to presence.Service.
func (c *ConcreteServiceDeps) ProjectInboundPresence(payload []byte) error {
	var p presence.InboundPresencePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return fmt.Errorf("decode presence payload: %w", err)
	}
	if p.UserID == "" {
		return fmt.Errorf("invalid presence payload (missing user_id)")
	}
	if c.Pres == nil {
		return fmt.Errorf("presence service not wired")
	}
	return c.Pres.ApplyInboundPresence(p)
}

// ProjectInboundReaction decodes + hands off to reaction.Service.
func (c *ConcreteServiceDeps) ProjectInboundReaction(payload []byte) error {
	var p reaction.InboundReactionPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return fmt.Errorf("decode reaction payload: %w", err)
	}
	if p.MsgID == "" || p.UserID == "" {
		return fmt.Errorf("invalid reaction payload (missing msg_id or user_id)")
	}
	if c.Rxn == nil {
		return fmt.Errorf("reaction service not wired")
	}
	return c.Rxn.ApplyInboundReaction(p)
}
