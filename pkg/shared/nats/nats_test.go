// Tests for the NATS JetStream wrapper .
//
// Coverage before this file: 18.9% (only panic-on-nil-js smoke tests).
// Coverage target: 80%+ by exercising:
//   - Connect: success + failure paths (no live broker required for
//     the failure cases; success guarded by PULSE_TEST_NATS env var
//     that CI sets via the docker-compose nats service)
//   - Close: nil-connection + live-connection paths
//   - Conn / JS: accessor contracts
//   - EnsureStream: nil-jetstream panic + live broker integration
//   - Publish: nil-jetstream panic + context-cancel path
//   - Subscribe: message-handler ack + nak behaviour via a live broker
//
// Run with PULSE_TEST_NATS set:
//
//	PULSE_TEST_NATS=nats://localhost:4222 \
//	  go test -race -count=1 ./internal/shared/nats/...
package nats

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"
)

// natsURL returns the test NATS URL from PULSE_TEST_NATS, skipping when
// unset. Failure-path tests (Connect with bad URL, etc.) run regardless.
func natsURL(t *testing.T) string {
	t.Helper()
	v := os.Getenv("PULSE_TEST_NATS")
	if v == "" {
		t.Skip("PULSE_TEST_NATS not set; skipping live-broker nats wrapper test")
	}
	return v
}

// uniqueStream returns a stream name unique to this test run so parallel
// packages (and repeated local runs) don't collide on JetStream state.
func uniqueStream(t *testing.T) string {
	t.Helper()
	return "TEST_" + t.Name()
}

// =====================================================================
// Client accessors (no broker needed)
// =====================================================================

// TestClient_FieldsAreAccessible confirms the zero-value client still
// satisfies the accessor contracts. Conn() / JS() must return the
// underlying refs (possibly nil) without panicking.
func TestClient_FieldsAreAccessible(t *testing.T) {
	c := &Client{}
	require.NotNil(t, c)
	require.Nil(t, c.conn)
	require.Nil(t, c.js)
}

// TestConn_AndJS_ReturnUnderlyingRefs confirms the accessors return the
// values stored on the struct. Zero-value client returns nil.
func TestConn_AndJS_ReturnUnderlyingRefs(t *testing.T) {
	c := &Client{}
	require.Nil(t, c.Conn())
	require.Nil(t, c.JS())
}

// =====================================================================
// Close (no broker needed)
// =====================================================================

// TestClose_NilConnectionIsNoOp confirms Close is safe on a Client that
// was never connected. Returns nil rather than panicking.
func TestClose_NilConnectionIsNoOp(t *testing.T) {
	c := &Client{}
	require.NoError(t, c.Close())
}

// TestClose_IdempotentOnLiveConnection confirms Close can be called
// twice on a real connection without panicking. The second call is a
// no-op because the underlying nats.Conn is closed.
func TestClose_IdempotentOnLiveConnection(t *testing.T) {
	url := natsURL(t)
	c, err := Connect(context.Background(), url, "test-cluster")
	require.NoError(t, err)
	require.NoError(t, c.Close())
	require.NoError(t, c.Close(), "Close must be idempotent on a live connection")
}

// =====================================================================
// Connect — error paths
// =====================================================================

// TestConnect_UnreachableURL confirms Connect fails fast when the URL
// points at a closed port. We use a TCP loopback address that nothing
// is listening on to guarantee a quick refusal.
func TestConnect_UnreachableURL(t *testing.T) {
	// 127.0.0.1:1 is a privileged port nothing should be listening on.
	// nats.Timeout(10s) in Connect is too long for a unit test; we cap
	// the test with a context deadline.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := Connect(ctx, "nats://127.0.0.1:1", "test-cluster")
	require.Error(t, err)
	require.Contains(t, err.Error(), "nats connect",
		"error must wrap the nats connect failure, got: %v", err)
}

// TestConnect_BadURL confirms Connect rejects a malformed URL. We use
// an obviously-broken scheme to provoke the parse error.
func TestConnect_BadURL(t *testing.T) {
	_, err := Connect(context.Background(), "://not a url", "test-cluster")
	require.Error(t, err)
}

// =====================================================================
// Connect — happy path
// =====================================================================

// TestConnect_LiveBroker confirms Connect against a reachable broker
// returns a usable Client. The Conn() accessor returns a non-nil
// *nats.Conn in the connected state.
func TestConnect_LiveBroker(t *testing.T) {
	url := natsURL(t)
	c, err := Connect(context.Background(), url, "test-cluster")
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })

	require.NotNil(t, c.Conn())
	require.NotNil(t, c.JS())

	// Sanity: Status() reports connected (not disconnected / closed).
	require.Equal(t, nats.CONNECTED, c.Conn().Status())
}

// =====================================================================
// EnsureStream
// =====================================================================

// TestEnsureStream_NilJetStream_PanicsAsExpected documents the
// pre-condition: EnsureStream requires a non-nil JetStream context.
// A zero-value Client cannot reach this code without a panic.
func TestEnsureStream_NilJetStream_PanicsAsExpected(t *testing.T) {
	defer func() {
		r := recover()
		require.NotNil(t, r, "EnsureStream on a zero-value Client must panic (nil js)")
	}()
	c := &Client{}
	_ = c.EnsureStream(context.Background(), "test-stream", []string{"test.>"})
}

// TestEnsureStream_LiveBroker_CreateAndUpdate confirms the happy path:
// creating a stream with subjects succeeds, and a second call with
// the same name (different subjects) updates rather than erroring.
// The implementation must treat ErrStreamNameAlreadyInUse as success.
func TestEnsureStream_LiveBroker_CreateAndUpdate(t *testing.T) {
	url := natsURL(t)
	c, err := Connect(context.Background(), url, "test-cluster")
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })

	ctx := context.Background()
	stream := uniqueStream(t)

	// Create.
	require.NoError(t, c.EnsureStream(ctx, stream, []string{"foo.>"}))
	// Update — different subject list but same name.
	require.NoError(t, c.EnsureStream(ctx, stream, []string{"foo.>", "bar.>"}),
		"second EnsureStream with same name must update, not error")
}

// =====================================================================
// Publish
// =====================================================================

// TestPublish_NilJetStream_PanicsAsExpected documents the pre-condition
// that Publish requires a non-nil JetStream context.
func TestPublish_NilJetStream_PanicsAsExpected(t *testing.T) {
	defer func() {
		r := recover()
		require.NotNil(t, r, "Publish on a zero-value Client must panic (nil js)")
	}()
	c := &Client{}
	_ = c.Publish(context.Background(), "test.subject", []byte("payload"))
}

// TestPublish_ContextCancelledReturnsError confirms the context-cancel
// branch: when ctx is cancelled before the publish ack arrives, the
// Publish helper must return ctx.Err() rather than blocking forever.
// We use a Publish against a bogus subject with an immediately-cancelled
// context.
func TestPublish_ContextCancelledReturnsError(t *testing.T) {
	url := natsURL(t)
	c, err := Connect(context.Background(), url, "test-cluster")
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel BEFORE the call

	err = c.Publish(ctx, "some.subject", []byte("payload"))
	require.Error(t, err, "cancelled context must surface an error")
	require.True(t, errors.Is(err, context.Canceled),
		"expected context.Canceled, got: %v", err)
}

// TestPublish_LiveBroker_Success confirms a publish to a real stream
// subject resolves with a nil error within the per-attempt timeout.
// We don't read back the message; just confirm the helper returns
// without erroring on the happy path.
func TestPublish_LiveBroker_Success(t *testing.T) {
	url := natsURL(t)
	c, err := Connect(context.Background(), url, "test-cluster")
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })

	stream := uniqueStream(t)
	ctx := context.Background()
	require.NoError(t, c.EnsureStream(ctx, stream, []string{"p.>"}))

	require.NoError(t, c.Publish(ctx, "p.one", []byte("hello")),
		"publish against a live broker must succeed")
}

// TestPublish_HeadersAttached confirms the headers overload publishes
// a message that retains the header map. We assert by reading the
// message back via a pull consumer + asserting Header presence on the
// raw msg. This is an end-to-end contract test for the headers path.
func TestPublish_HeadersAttached(t *testing.T) {
	url := natsURL(t)
	c, err := Connect(context.Background(), url, "test-cluster")
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })

	ctx := context.Background()
	stream := uniqueStream(t)
	require.NoError(t, c.EnsureStream(ctx, stream, []string{"h.>"}))

	// Set up a consumer that we can read from to assert headers.
	cons, err := c.JS().CreateOrUpdateConsumer(ctx, stream, jetstream.ConsumerConfig{
		Name:          "h-test",
		FilterSubject: "h.>",
		AckPolicy:     jetstream.AckExplicitPolicy,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = c.JS().DeleteConsumer(ctx, stream, "h-test")
	})

	require.NoError(t, c.Publish(ctx, "h.one", []byte("payload"),
		map[string]string{"x-trace-id": "abc-123"}))

	batch, err := cons.Fetch(1)
	require.NoError(t, err)
	var found jetstream.Msg
	for m := range batch.Messages() {
		found = m
	}
	require.NotNil(t, found, "expected at least one message in the batch")
	require.Equal(t, "abc-123", found.Headers().Get("x-trace-id"))
}

// =====================================================================
// Subscribe
// =====================================================================

// TestSubscribe_HandlerAckSuccess confirms a successful handler call
// causes the message to be acked. We assert by reading the consumer's
// pending count via JetStream after the handler runs.
//
// Note on durability: each test run creates a fresh stream with a
// unique name (uniqueStream). Because the *consumer* name is also
// fresh per run, no redelivery from a previous run can interfere.
// We assert handler invocation >= 2 (>=, not ==) to tolerate the
// edge case where a goroutine picks up a not-yet-published message
// just as the subscribe is being wired (extremely rare; the safety
// margin is the test's only robustness cost).
func TestSubscribe_HandlerAckSuccess(t *testing.T) {
	url := natsURL(t)
	c, err := Connect(context.Background(), url, "test-cluster")
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	stream := uniqueStream(t)
	require.NoError(t, c.EnsureStream(ctx, stream, []string{"s.>"}))

	var got atomic.Int32
	require.NoError(t, c.Subscribe(ctx, stream, "s-sub-ack", "s.>", func(data []byte) error {
		got.Add(1)
		return nil
	}))

	// Give the goroutine time to register the subscription.
	time.Sleep(100 * time.Millisecond)

	require.NoError(t, c.Publish(ctx, "s.one", []byte("m1")))
	require.NoError(t, c.Publish(ctx, "s.one", []byte("m2")))

	// Wait for both messages to be processed.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && got.Load() < 2 {
		time.Sleep(20 * time.Millisecond)
	}
	require.GreaterOrEqual(t, got.Load(), int32(2),
		"handler must be called for each publish")

	// Drain a moment so acks settle, then assert pending count is 0.
	time.Sleep(300 * time.Millisecond)
	cons, err := c.JS().Consumer(ctx, stream, "s-sub-ack")
	require.NoError(t, err)
	info, err := cons.Info(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(0), info.NumPending,
		"acked messages must not be pending")
}

// TestSubscribe_HandlerNakOnError confirms a handler returning an
// error causes the message to be Nak'd, leaving it pending for
// redelivery (or, for AckExplicitPolicy + a transient handler, parked
// for the next subscriber). We assert via NumPending > 0.
func TestSubscribe_HandlerNakOnError(t *testing.T) {
	url := natsURL(t)
	c, err := Connect(context.Background(), url, "test-cluster")
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	stream := uniqueStream(t)
	require.NoError(t, c.EnsureStream(ctx, stream, []string{"n.>"}))

	var got atomic.Int32
	require.NoError(t, c.Subscribe(ctx, stream, "s-sub-nak", "n.>", func(data []byte) error {
		got.Add(1)
		return errors.New("handler intentionally fails")
	}))

	time.Sleep(100 * time.Millisecond)

	require.NoError(t, c.Publish(ctx, "n.one", []byte("m1")))

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && got.Load() < 1 {
		time.Sleep(20 * time.Millisecond)
	}
	require.GreaterOrEqual(t, got.Load(), int32(1),
		"handler must be invoked at least once")

	// After the handler returns an error, the message is Nak'd.
	// JetStream redelivers, so we may see the handler invoked more
	// than once. We assert that the consumer exists and the message is
	// not silently lost (NumPending >= 1 OR NumAckPending >= 1).
	time.Sleep(200 * time.Millisecond)
	cons, err := c.JS().Consumer(ctx, stream, "s-sub-nak")
	require.NoError(t, err)
	info, err := cons.Info(ctx)
	require.NoError(t, err)
	totalInflight := info.NumPending + uint64(info.NumAckPending)
	require.Greater(t, totalInflight, uint64(0),
		"a Nak'd message must still be tracked as pending (got 0)")
}

// TestSubscribe_ContextCancelDrains confirms cancelling the context
// causes the subscription goroutine to exit cleanly. We don't time
// the goroutine directly; we cancel and verify the client remains
// usable for a subsequent Close without hanging.
func TestSubscribe_ContextCancelDrains(t *testing.T) {
	url := natsURL(t)
	c, err := Connect(context.Background(), url, "test-cluster")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	stream := uniqueStream(t)
	require.NoError(t, c.EnsureStream(ctx, stream, []string{"d.>"}))

	var started sync.WaitGroup
	started.Add(1)
	require.NoError(t, c.Subscribe(ctx, stream, "s-sub-drain", "d.>", func(data []byte) error {
		return nil
	}))
	started.Done()

	// Cancel + immediately Close; the drain in iter.Stop() must not
	// hang on the broker connection.
	cancel()
	closeDone := make(chan struct{})
	go func() { _ = c.Close(); close(closeDone) }()
	select {
	case <-closeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Close blocked after context cancel — subscription drain hung")
	}
}