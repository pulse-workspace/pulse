package gateway

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/pulse-im/pulse/pkg/shared/logger"
)

// HandoffEvent is published to subscribers when a session moves
// from one gateway to another (or when one is replaced).
type HandoffEvent struct {
	FromSessionID string
	ToSessionID   string
	Snapshot      []byte
}

// natsHandoffChannel is the production handoff channel.
// Wraps NATS JetStream subjects: "pulse.handoff.{cellID}"
//
// (Real wiring lands in Week 5-6 as we stand up the NATS topology for
// cross-cell routing. For now the in-memory fallback covers single-cell.)
type natsHandoffChannel struct {
	mu   sync.RWMutex
	subs map[string][]chan HandoffEvent
}

// NewNATSHandoffChannel is a stub. Real impl in Week 5-6 cross-cell work.
func NewNATSHandoffChannel() *natsHandoffChannel {
	return &natsHandoffChannel{subs: make(map[string][]chan HandoffEvent)}
}

// PublishHandoff fans out to all subscribers of the user's channel.
func (n *natsHandoffChannel) PublishHandoff(userID, oldSessionID, newSessionID string, snapshot []byte) error {
	evt := HandoffEvent{
		FromSessionID: oldSessionID,
		ToSessionID:   newSessionID,
		Snapshot:      snapshot,
	}
	n.mu.RLock()
	defer n.mu.RUnlock()
	for _, ch := range n.subs[userID] {
		select {
		case ch <- evt:
		default:
			// Drop on full subscriber; the consumer will catch up via missed-bus list.
		}
	}
	return nil
}

// SubscribeHandoffs returns a channel of handoff events for a user.
// The returned func unsubscribes.
func (n *natsHandoffChannel) SubscribeHandoffs(userID string) (<-chan HandoffEvent, func(), error) {
	ch := make(chan HandoffEvent, 8)
	n.mu.Lock()
	n.subs[userID] = append(n.subs[userID], ch)
	n.mu.Unlock()

	cancel := func() {
		n.mu.Lock()
		defer n.mu.Unlock()
		subs := n.subs[userID]
		for i, s := range subs {
			if s == ch {
				n.subs[userID] = append(subs[:i], subs[i+1:]...)
				break
			}
		}
		close(ch)
	}
	return ch, cancel, nil
}

// HandoffPayload is the on-the-wire JSON representation of a handoff.
type HandoffPayload struct {
	FromSessionID string `json:"from"`
	ToSessionID   string `json:"to"`
	Snapshot      []byte `json:"snapshot"`
}

// Marshal returns the JSON form for transit.
func (h HandoffPayload) Marshal() ([]byte, error) {
	return json.Marshal(h)
}

// Compile-time guards.
var (
	_ = context.Background
	_ logger.Logger
)
