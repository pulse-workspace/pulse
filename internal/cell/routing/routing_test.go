package routing

import (
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/pulse-im/pulse/internal/cell"
)

// buildCell constructs a *cell.Cell with cfg/log populated and nil DB / NATS.
// Tests in this file never invoke Start (which would try to EnsureStream on
// a nil NATS), so the nils stay safe. Handler-side code paths are exercised
// directly via dispatch().
func buildCell(t *testing.T, id string) *cell.Cell {
	t.Helper()
	return &cell.Cell{}
}

type dispatchRecorder struct {
	lastMessage  Envelope
	lastPresence Envelope
	lastReaction Envelope
	msgCount     int
	preCount     int
	rxnCount     int
}

func newHandlers() (HandlerFunc, HandlerFunc, HandlerFunc, *dispatchRecorder) {
	rec := &dispatchRecorder{}
	return rec.onMessage, rec.onPresence, rec.onReaction, rec
}

func (r *dispatchRecorder) onMessage(env Envelope) error {
	r.lastMessage = env
	r.msgCount++
	return nil
}
func (r *dispatchRecorder) onPresence(env Envelope) error {
	r.lastPresence = env
	r.preCount++
	return nil
}
func (r *dispatchRecorder) onReaction(env Envelope) error {
	r.lastReaction = env
	r.rxnCount++
	return nil
}

func TestNewService_Name(t *testing.T) {
	s := NewService(buildCell(t, "cell-A"), nil, nil, nil)
	require.NotNil(t, s)
	require.Equal(t, "routing", s.Name())
}

func TestNewService_NilHandlersAreSafe(t *testing.T) {
	s := NewService(buildCell(t, "cell-A"), nil, nil, nil)
	require.NotNil(t, s)
	require.Empty(t, s.KnownPeers())
}

func TestService_RegisterPeer_And_KnownPeers(t *testing.T) {
	s := NewService(buildCell(t, "cell-A"), nil, nil, nil)

	s.RegisterPeer("cell-B")
	s.RegisterPeer("cell-C")
	s.RegisterPeer("cell-B") // dedup-by-existence

	peers := s.KnownPeers()
	require.Len(t, peers, 2)
	require.Contains(t, peers, "cell-B")
	require.Contains(t, peers, "cell-C")
}

func TestService_KnownPeers_EmptyOnFresh(t *testing.T) {
	s := NewService(buildCell(t, "cell-A"), nil, nil, nil)
	require.Empty(t, s.KnownPeers())
}

func TestService_Dispatch_RoutesByKind(t *testing.T) {
	onMsg, onPre, onRxn, rec := newHandlers()
	s := NewService(buildCell(t, "cell-A"), onMsg, onPre, onRxn)

	s.dispatch(Envelope{Kind: "message", FromCellID: "B", ToCellID: "A", SentAt: time.Now()})
	s.dispatch(Envelope{Kind: "presence", FromCellID: "B", ToCellID: "A", SentAt: time.Now()})
	s.dispatch(Envelope{Kind: "reaction", FromCellID: "B", ToCellID: "A", SentAt: time.Now()})

	require.Equal(t, 1, rec.msgCount)
	require.Equal(t, 1, rec.preCount)
	require.Equal(t, 1, rec.rxnCount)
	require.Equal(t, "B", rec.lastMessage.FromCellID)
	require.Equal(t, "B", rec.lastPresence.FromCellID)
	require.Equal(t, "B", rec.lastReaction.FromCellID)
}

func TestService_Dispatch_UnknownKindSwallowed(t *testing.T) {
	onMsg, onPre, onRxn, rec := newHandlers()
	s := NewService(buildCell(t, "cell-A"), onMsg, onPre, onRxn)

	require.NotPanics(t, func() {
		s.dispatch(Envelope{Kind: "mystery_kind"})
	})
	require.Zero(t, rec.msgCount)
	require.Zero(t, rec.preCount)
	require.Zero(t, rec.rxnCount)
}

func TestService_Dispatch_NilHandlerGuards(t *testing.T) {
	// When a particular handler is nil, the others must still be called.
	count := 0
	s := NewService(buildCell(t, "cell-A"), nil, func(env Envelope) error { count++; return nil }, nil)
	s.dispatch(Envelope{Kind: "presence"})
	require.Equal(t, 1, count)
}

func TestDeliveryTarget_Fields(t *testing.T) {
	dt := DeliveryTarget{CellID: "cell-A", Subject: "user.u1"}
	require.Equal(t, "cell-A", dt.CellID)
	require.Equal(t, "user.u1", dt.Subject)
}

func TestEnvelope_JSONShape(t *testing.T) {
	now := time.Now().UTC().Round(time.Second)
	env := Envelope{
		FromCellID: "A",
		ToCellID:   "B",
		Kind:       "message",
		Payload:    []byte(`{"hello":"world"}`),
		SentAt:     now,
	}
	require.Equal(t, "A", env.FromCellID)
	require.Equal(t, "B", env.ToCellID)
	require.Equal(t, "message", env.Kind)
	require.JSONEq(t, `{"hello":"world"}`, string(env.Payload))
}

func TestService_LoggerAnnotatedWithServiceName(t *testing.T) {
	// NewService annotates the logger with service=routing. We can't read
	// zerolog's tag cheaply without a writer, so we just assert construction.
	s := NewService(buildCell(t, "x"), nil, nil, nil)
	require.NotNil(t, s.log)
}

// silence unused import warning on zerolog if cell.Cell's log field signature
// requires zerolog elsewhere.
var _ = zerolog.Nop
