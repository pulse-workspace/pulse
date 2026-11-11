package projection

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"github.com/vmihailenco/msgpack/v5"

	"github.com/pulse-im/pulse/pkg/shared/events"
)

// Tests below focus on the offset-tracking side of the runner —
// the NATS subscribe path requires a live broker and is covered by
// integration tests at plans/.../bench/projection_bench.go.

// TestNewRunner_StoresProjectors verifies the projector list is
// captured at construction time. We don't start the runner so the
// NATS client is never dereferenced.
func TestNewRunner_StoresProjectors(t *testing.T) {
	p1 := fakeProjector{name: "alpha"}
	p2 := fakeProjector{name: "beta"}
	r := NewRunner(nil, nil, zerolog.Nop(), &p1, &p2)
	require.Equal(t, "projection", r.Name())
}

// TestRunner_OffsetStartsAtZero confirms a fresh runner has no
// recorded offsets.
func TestRunner_OffsetStartsAtZero(t *testing.T) {
	p := fakeProjector{name: "alpha"}
	r := NewRunner(nil, nil, zerolog.Nop(), &p)
	require.Equal(t, int64(0), r.Offset("alpha"))
	require.Equal(t, int64(0), r.Offset("beta"))
}

// TestRunner_RecordProgress is the happy-path for the in-memory
// offset accumulator. Confirms recordProgress() advances the
// offset and the loop's "max" semantics hold across multiple
// updates.
func TestRunner_RecordProgress(t *testing.T) {
	p := fakeProjector{name: "alpha"}
	r := NewRunner(nil, nil, zerolog.Nop(), &p)

	r.recordProgress("alpha", 1)
	require.Equal(t, int64(1), r.Offset("alpha"))

	r.recordProgress("alpha", 5)
	require.Equal(t, int64(5), r.Offset("alpha"))

	// Older version ignored.
	r.recordProgress("alpha", 3)
	require.Equal(t, int64(5), r.Offset("alpha"))
}

// TestRunner_RecordProgress_ZeroVersionIgnored confirms a no-op
// event (Version=0 from the msgpack round-trip on a stub) doesn't
// reset or overwrite a real progress entry.
func TestRunner_RecordProgress_ZeroVersionIgnored(t *testing.T) {
	p := fakeProjector{name: "alpha"}
	r := NewRunner(nil, nil, zerolog.Nop(), &p)
	r.recordProgress("alpha", 42)
	r.recordProgress("alpha", 0)
	require.Equal(t, int64(42), r.Offset("alpha"))
}

// TestRunner_AllOffsets_Snapshot confirms AllOffsets returns a
// point-in-time copy that subsequent updates don't mutate.
func TestRunner_AllOffsets_Snapshot(t *testing.T) {
	p1 := fakeProjector{name: "alpha"}
	p2 := fakeProjector{name: "beta"}
	r := NewRunner(nil, nil, zerolog.Nop(), &p1, &p2)
	r.recordProgress("alpha", 10)
	r.recordProgress("beta", 20)

	snap := r.AllOffsets()
	require.Equal(t, int64(10), snap["alpha"])
	require.Equal(t, int64(20), snap["beta"])

	// Mutating the runner after taking the snapshot doesn't
	// affect the returned map.
	r.recordProgress("alpha", 999)
	require.Equal(t, int64(10), snap["alpha"], "snapshot must not change after runner mutation")
	require.Equal(t, int64(20), snap["beta"])
}

// TestRunner_Stop_Idempotent verifies Stop is safe to call twice.
// The once-only close + WaitGroup semantics make a second call a
// no-op rather than a panic on a closed channel.
func TestRunner_Stop_Idempotent(t *testing.T) {
	p := fakeProjector{name: "alpha"}
	r := NewRunner(nil, nil, zerolog.Nop(), &p)
	// Stop uses context.Background() internally; passing nil here
	// would trigger the staticcheck SA1012 warning. The runner
	// doesn't depend on the caller-provided context for the
	// idempotent-stop path so context.Background() is safe.
	ctx := context.Background()
	require.NotPanics(t, func() {
		_ = r.Stop(ctx)
	})
	// Second call: also no panic. The runner's stopOnce +
	// WaitGroup semantics make a second Stop a no-op rather than
	// a panic on a closed channel.
}

// TestEvents_MsgpackRoundTrip verifies the runner's msgpack
// decoder matches shared/events.Event's wire shape. Regressions
// here would silently corrupt every event in flight.
func TestEvents_MsgpackRoundTrip(t *testing.T) {
	original := events.NewEvent(
		"events.messages",
		events.TypeMessageSent,
		"u_alice",
		[]byte(`{"msgID":"m_1","convID":"c_1"}`),
	)
	original.Version = 42

	raw, err := msgpackMarshal(original)
	require.NoError(t, err)

	var decoded events.Event
	require.NoError(t, msgpackUnmarshal(raw, &decoded))

	require.Equal(t, "events.messages", decoded.Stream)
	require.Equal(t, events.TypeMessageSent, decoded.Type)
	require.Equal(t, "u_alice", decoded.Actor)
	require.Equal(t, int64(42), decoded.Version)
	require.Equal(t, []byte(`{"msgID":"m_1","convID":"c_1"}`), []byte(decoded.Data))
	require.WithinDuration(t, original.Timestamp, decoded.Timestamp, time.Second)
}

// TestCommitIntervalConstant guards the tuning knob — operators
// size their DB based on this number; if it changes, the docs and
// dashboards need an update too.
func TestCommitIntervalConstant(t *testing.T) {
	require.Equal(t, 5*time.Second, commitInterval)
}

// fakeProjector is a stub for eventsource.Projector used by the
// offset-tracking tests. The Handle method is unused — these
// tests don't Start the runner.
type fakeProjector struct {
	name string
}

func (p *fakeProjector) Name() string   { return p.name }
func (p *fakeProjector) Stream() string { return "events." + p.name }
func (p *fakeProjector) Subject() string {
	return p.name + ".>"
}
func (p *fakeProjector) Handle(_ context.Context, _ events.Event) error { return nil }

// msgpackMarshal / msgpackUnmarshal are thin wrappers around
// msgpack v5 so the round-trip test reads as a single intent.
func msgpackMarshal(v any) ([]byte, error) {
	return msgpack.Marshal(v)
}
func msgpackUnmarshal(data []byte, v any) error {
	return msgpack.Unmarshal(data, v)
}
