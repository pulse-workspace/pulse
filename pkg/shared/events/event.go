// Package events defines the Event type used by the event-sourced write side.
package events

import (
	"time"

	"github.com/oklog/ulid/v2"
)

// Event is the canonical envelope for every state change in Pulse.
//
// All events are appended to a NATS JetStream stream. The Stream identifier
// partitions events by the entity they affect (user:<id>, group:<id>, ...).
// Version is monotonic per-stream.
type Event struct {
	ID        string            `msgpack:"id"          json:"id"`
	Stream    string            `msgpack:"stream"      json:"stream"`
	Type      string            `msgpack:"type"        json:"type"`
	Version   int64             `msgpack:"v"           json:"v"`
	Timestamp time.Time         `msgpack:"ts"          json:"ts"`
	Actor     string            `msgpack:"actor"       json:"actor,omitempty"`
	Data      []byte            `msgpack:"data"        json:"data"`
	Metadata  map[string]string `msgpack:"metadata,omitempty" json:"metadata,omitempty"`
}

// NewEvent builds an Event with a fresh ULID and current timestamp.
func NewEvent(stream, eventType, actor string, data []byte) Event {
	return Event{
		ID:        ulid.Make().String(),
		Stream:    stream,
		Type:      eventType,
		Version:   0, // set by Append when persisted
		Timestamp: time.Now().UTC(),
		Actor:     actor,
		Data:      data,
	}
}

// Common event types (extend as new services come online).
const (
	TypeUserCreated = "UserCreated"
	TypeUserUpdated = "UserUpdated"
	TypeUserDeleted = "UserDeleted"

	TypeGroupCreated          = "GroupCreated"
	TypeGroupDismissed        = "GroupDismissed"
	TypeGroupMemberAdded      = "GroupMemberAdded"
	TypeGroupMemberRemoved    = "GroupMemberRemoved"
	TypeGroupOwnerTransferred = "GroupOwnerTransferred"
	TypeGroupInfoUpdated      = "GroupInfoUpdated"
	TypeMemberAdded           = "MemberAdded"
	TypeMemberRemoved         = "MemberRemoved"
	TypeMemberRoleChanged     = "MemberRoleChanged"

	TypeConvCreated = "ConvCreated"
	TypeConvUpdated = "ConvUpdated"

	TypeMessageSent     = "MessageSent"
	TypeMessageRecalled = "MessageRecalled"
	TypeMessageRead     = "MessageRead"

	TypeReactionAdded   = "ReactionAdded"
	TypeReactionRemoved = "ReactionRemoved"

	TypePresenceUpdated = "PresenceUpdated"
)
