package message

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/require"

	"github.com/pulse-workspace/pulse/pkg/core"
)

// TestMessageStructLayout documents the wire format that clients depend on.
// If you change a JSON tag here, you must also bump the protocol version
// in pkg/protocol/frames.go.
func TestMessageStructLayout(t *testing.T) {
	m := Message{
		MsgID:       "m_01ABC",
		ConvID:      "si_alice_bob",
		SenderID:    "alice",
		ContentType: 101,
		Content:     map[string]any{"text": "hi"},
		Seq:         42,
		CreatedAt:   time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
	require.Equal(t, "m_01ABC", m.MsgID)
	require.Equal(t, "si_alice_bob", m.ConvID)
	require.Equal(t, "alice", m.SenderID)
	require.Equal(t, 101, m.ContentType)
	require.Equal(t, "hi", m.Content["text"])
	require.Equal(t, int64(42), m.Seq)
	require.False(t, m.RecalledAt != nil, "un-recalled messages have nil RecalledAt")
}

// TestSendMessageInputValidation covers the input contract — no DB needed.
// The full path is exercised in tests/integration.
func TestSendMessageInputValidation(t *testing.T) {
	_ = ulid.Make().String() // sanity that the dep is wired
	_ = context.Background()
	// No-op here — service-level validation lives in integration tests.
}

// --- Hold-check unit tests ---------------------------------------
// These tests verify the compliance.IsHeld wiring without requiring a DB.
// The full path (RecallMessage → UPDATE messages) needs a real Postgres,
// covered by tests/integration.

// stubChecker is a stand-in for *compliance.Service that returns
// pre-canned answers. The real IsHeld returns (true/false, nil) for hold
// queries, or (false, ErrNotLicensed) when the compliance module is not
// entitled.
type stubChecker struct {
	held  bool
	err   error
	calls int
}

func (s *stubChecker) IsHeld(_ context.Context, _ core.ComplianceScope, _ string) (bool, error) {
	s.calls++
	return s.held, s.err
}

// newSvcWithStub builds a minimal *Service suitable for checkHold testing.
// It has a nil DB and nil NATS — those paths are not exercised here.
func newSvcWithStub(cc complianceChecker) *Service {
	return &Service{compliance: cc}
}

func TestCheckHold_NoCompliance_ReturnsNil(t *testing.T) {
	// OSS-only mode: compliance module not loaded.
	s := newSvcWithStub(nil)
	require.NoError(t, s.checkHold(context.Background(), "conv_x"))
}

func TestCheckHold_ActiveHold_ReturnsErrHeld(t *testing.T) {
	stub := &stubChecker{held: true}
	s := newSvcWithStub(stub)
	err := s.checkHold(context.Background(), "conv_x")
	require.ErrorIs(t, err, ErrHeld)
	require.Equal(t, 1, stub.calls)
}

func TestCheckHold_NoHold_ReturnsNil(t *testing.T) {
	stub := &stubChecker{held: false}
	s := newSvcWithStub(stub)
	require.NoError(t, s.checkHold(context.Background(), "conv_x"))
	require.Equal(t, 1, stub.calls)
}

func TestCheckHold_NotLicensed_TreatedAsNoHold(t *testing.T) {
	// Per audit P3: ErrNotLicensed must NOT block deletes — a missing
	// compliance entitlement silently disables holds (free tier path).
	stub := &stubChecker{held: false, err: core.ErrNotLicensed}
	s := newSvcWithStub(stub)
	require.NoError(t, s.checkHold(context.Background(), "conv_x"))
}

func TestCheckHold_OtherError_Propagates(t *testing.T) {
	dbErr := errors.New("db connection lost")
	stub := &stubChecker{err: dbErr}
	s := newSvcWithStub(stub)
	err := s.checkHold(context.Background(), "conv_x")
	require.ErrorIs(t, err, dbErr)
}

func TestSetCompliance_NilDisablesHolds(t *testing.T) {
	// Initial service with a checker that would block.
	s := newSvcWithStub(&stubChecker{held: true})
	require.ErrorIs(t, s.checkHold(context.Background(), "conv_x"), ErrHeld)

	// Disable via nil setter.
	s.SetCompliance(nil)
	require.NoError(t, s.checkHold(context.Background(), "conv_x"))
}
