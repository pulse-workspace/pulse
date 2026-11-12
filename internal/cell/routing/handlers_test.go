package routing

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/pulse-workspace/pulse/internal/cell/message"
	"github.com/pulse-workspace/pulse/internal/cell/presence"
	"github.com/pulse-workspace/pulse/internal/cell/reaction"
)

// stubDeps is a test double for ServiceDeps that records the calls
// and returns canned errors when configured to. Lets the handler
// dispatch path be exercised without a live DB pool.
type stubDeps struct {
	msgErr  error
	presErr error
	rxnErr  error

	gotMsg []byte
	gotPre []byte
	gotRxn []byte
}

func (s *stubDeps) ProjectInboundMessage(payload []byte) error {
	s.gotMsg = append(s.gotMsg, payload...)
	return s.msgErr
}
func (s *stubDeps) ProjectInboundPresence(payload []byte) error {
	s.gotPre = append(s.gotPre, payload...)
	return s.presErr
}
func (s *stubDeps) ProjectInboundReaction(payload []byte) error {
	s.gotRxn = append(s.gotRxn, payload...)
	return s.rxnErr
}

// nopLogger returns a logger that drops everything. The Logger
// type alias in shared/logger is zerolog.Logger directly so we
// can pass a Nop() through.
func nopLogger() zerolog.Logger { return zerolog.Nop() }

func TestDefaultHandlers_DispatchByKind(t *testing.T) {
	deps := &stubDeps{}
	onMsg, onPre, onRxn := DefaultHandlers(deps, nopLogger())

	onMsg(Envelope{Kind: "message", FromCellID: "A", ToCellID: "B", Payload: []byte(`{"msg":"hi"}`)})
	onPre(Envelope{Kind: "presence", FromCellID: "A", ToCellID: "B", Payload: []byte(`{"online":true}`)})
	onRxn(Envelope{Kind: "reaction", FromCellID: "A", ToCellID: "B", Payload: []byte(`{"reaction":"like"}`)})

	require.NotEmpty(t, deps.gotMsg, "message handler must forward payload")
	require.NotEmpty(t, deps.gotPre, "presence handler must forward payload")
	require.NotEmpty(t, deps.gotRxn, "reaction handler must forward payload")
}

func TestDefaultHandlers_ServiceErrorIsSwallowed(t *testing.T) {
	// A returning error from the service is logged but doesn't panic
	// or break the subscription; routing must keep dispatching.
	deps := &stubDeps{msgErr: errors.New("db down")}
	onMsg, _, _ := DefaultHandlers(deps, nopLogger())
	require.NotPanics(t, func() {
		onMsg(Envelope{Kind: "message", Payload: []byte(`{"x":1}`)})
	})
}

// Compile-time assertion that the domain payload types from message/
// presence/reaction are compatible with ConcreteServiceDeps' decoded
// shape — guards against a future field rename in either package
// silently breaking the cross-cell handler.
func TestDomainPayloadDecoding(t *testing.T) {
	var mp message.InboundMessagePayload
	require.NoError(t, json.Unmarshal([]byte(`{"msg_id":"m_1","conv_id":"c_1","sender_id":"u_a","content_type":101,"content":{"text":"hi"},"seq":42,"created_at":"2026-07-22T10:00:00Z","from_cell_id":"cell-A"}`), &mp))
	require.Equal(t, "m_1", mp.MsgID)
	require.Equal(t, "cell-A", mp.FromCellID)

	var pp presence.InboundPresencePayload
	require.NoError(t, json.Unmarshal([]byte(`{"user_id":"u_a","online":true,"platform":"web","cell_id":"cell-A","updated_at":"2026-07-22T10:00:00Z"}`), &pp))
	require.Equal(t, "u_a", pp.UserID)
	require.True(t, pp.Online)

	var rp reaction.InboundReactionPayload
	require.NoError(t, json.Unmarshal([]byte(`{"msg_id":"m_1","user_id":"u_a","reaction":"like","cell_id":"cell-A"}`), &rp))
	require.Equal(t, "m_1", rp.MsgID)
	require.Equal(t, "u_a", rp.UserID)
}
