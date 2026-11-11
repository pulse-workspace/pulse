// Package eventsource implements the append-only event log and projection
// projector for Pulse cells.
//
// Each cell owns streams partitioned by entity type:
//
//	events.users      → subject user.<userID>
//	events.groups     → subject group.<groupID>
//	events.messages   → subject message.<msgID>
//	events.conversations → subject conv.<convID>
//	events.presence   → subject presence.<userID>
//
// Projectors subscribe to a stream, build the read model, and persist it
// to PostgreSQL projection tables.
package eventsource

import (
	"context"
	"errors"
	"fmt"

	"github.com/pulse-im/pulse/pkg/shared/db"
	"github.com/pulse-im/pulse/pkg/shared/events"
	"github.com/pulse-im/pulse/pkg/shared/logger"
	"github.com/pulse-im/pulse/pkg/shared/nats"
	"github.com/vmihailenco/msgpack/v5"
)

// Projector is implemented by every domain service that maintains a
// read-model table built from the event stream.
type Projector interface {
	Name() string
	Stream() string  // e.g., "events.users"
	Subject() string // e.g., "user.>"
	Handle(ctx context.Context, evt events.Event) error
}

// Append writes a single event to the appropriate stream subject.
func Append(ctx context.Context, n *nats.Client, evt events.Event) error {
	subject, err := subjectForStream(evt.Stream)
	if err != nil {
		return err
	}
	raw, err := msgpack.Marshal(evt)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	return n.Publish(ctx, subject, raw)
}

// Run starts a projector subscription. Blocks until ctx is cancelled.
func Run(ctx context.Context, n *nats.Client, p Projector, log logger.Logger, db *db.DB) error {
	if err := n.EnsureStream(ctx, p.Stream(), []string{p.Subject()}); err != nil {
		return err
	}
	log.Info().Str("projector", p.Name()).Str("stream", p.Stream()).Msg("starting projector")

	return n.Subscribe(ctx, p.Stream(), "projector-"+p.Name(), p.Subject(), func(data []byte) error {
		var evt events.Event
		if err := msgpack.Unmarshal(data, &evt); err != nil {
			log.Error().Err(err).Msg("unmarshal event")
			return nil // drop malformed event instead of retrying
		}
		return p.Handle(ctx, evt)
	})
}

// subjectForStream maps a stream identifier (user:<id>) to a NATS subject (user.<id>).
func subjectForStream(stream string) (string, error) {
	if len(stream) == 0 {
		return "", errors.New("empty stream")
	}
	// user:abc → user.abc
	for i := 0; i < len(stream); i++ {
		if stream[i] == ':' {
			return stream[:i] + "." + stream[i+1:], nil
		}
	}
	return "", fmt.Errorf("invalid stream format %q", stream)
}
