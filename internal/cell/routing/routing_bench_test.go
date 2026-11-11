package routing

import (
	"encoding/json"
	"testing"

	"github.com/rs/zerolog"
)

// Benchmark handlers_test.go with a stub deps so the dispatch hot
// path is exercised. We don't measure the NATS round-trip — that's
// a separate bench at the integration layer (see plans/.../bench).
// BenchmarkDefaultHandlers_MessageDispatch measures the closure
// dispatch path only (kind switch + log + ProjectInbound call).
// The ProjectInbound step here uses stubDeps (slice append); it
// does not measure network IO or database execution.
func BenchmarkDefaultHandlers_MessageDispatch(b *testing.B) {
	deps := &stubDeps{}
	onMsg, _, _ := DefaultHandlers(deps, zerolog.Nop())
	payload := mustMarshal(map[string]any{
		"msg_id":       "m_01J7G3...",
		"conv_id":      "si_alice_bob",
		"sender_id":    "u_alice",
		"content_type": 101,
		"content":      map[string]any{"text": "hello"},
		"seq":          42,
		"created_at":   "2026-07-22T10:00:00Z",
	})
	env := Envelope{
		FromCellID: "cell-A",
		ToCellID:   "cell-B",
		Kind:       "message",
		Payload:    payload,
	}
	// Reset deps so the per-iteration gotMsg slice does not grow unbounded.
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		deps.gotMsg = deps.gotMsg[:0]
		onMsg(env)
	}
}

func BenchmarkEnvelopeJSONMarshal(b *testing.B) {
	env := Envelope{
		FromCellID: "cell-A",
		ToCellID:   "cell-B",
		Kind:       "message",
		Payload:    []byte(`{"msg":"hi"}`),
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = mustMarshal(env)
	}
}

// mustMarshal is a small helper that fails the bench on a marshal
// error (which would never happen for the static payloads we use).
func mustMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
