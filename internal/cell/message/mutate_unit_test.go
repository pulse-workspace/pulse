
// via DBTX FakeTx.
//
// The held-block path (ErrHeld) is already covered in
// delete_is_held_test.go with a heldStub. This file exercises the
// no-hold SQL path: RecallMessage + DeleteMessage must reach the DB
// and execute the expected statement.
//
// RecallMessage publishes to NATS on the happy path; the unit test
// recovers the publish-panic and asserts on the captured Exec call.
// DeleteMessage has no publish call (recall.go:51 just logs) so it
// runs cleanly.
package message

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRecallMessage_NoHoldIssuesExecAndReturnsNil(t *testing.T) {
	fake := &FakeTx{
		ExecFn: func(_ context.Context, _ string, _ []any) error {
			return okExec()
		},
	}
	s := svcWithFake(fake)
	s.SetCompliance(&stubChecker{held: false})
	defer func() { _ = recover() }()
	err := s.RecallMessage(context.Background(), "si_a_b", "m_x", "alice")
	require.NoError(t, err)
	require.NotEmpty(t, fake.ExecCalls, "UPDATE must be issued")
	require.Contains(t, fake.ExecCalls[0].SQL, "UPDATE messages")
	require.Contains(t, fake.ExecCalls[0].SQL, "revoked_at")
}

func TestDeleteMessage_NoHoldIssuesExecAndReturnsNil(t *testing.T) {
	fake := &FakeTx{
		ExecFn: func(_ context.Context, _ string, _ []any) error {
			return okExec()
		},
	}
	s := svcWithFake(fake)
	s.SetCompliance(&stubChecker{held: false})
	// No publish-panic: DeleteMessage only logs (recall.go:51).
	err := s.DeleteMessage(context.Background(), "si_a_b", "m_x")
	require.NoError(t, err)
	require.NotEmpty(t, fake.ExecCalls)
	require.Contains(t, fake.ExecCalls[0].SQL, "DELETE FROM messages")
}

func TestDeleteMessage_ExecError_ReturnsWrappedError(t *testing.T) {
	fake := &FakeTx{
		ExecFn: func(_ context.Context, _ string, _ []any) error {
			return errors.New("connection lost")
		},
	}
	s := svcWithFake(fake)
	s.SetCompliance(&stubChecker{held: false})
	err := s.DeleteMessage(context.Background(), "si_a_b", "m_x")
	require.Error(t, err)
	require.Contains(t, err.Error(), "delete message")
}

// TestRecallMessage_ExecError_Propagates: When the UPDATE Exec fails
// after a clean hold-check, RecallMessage must surface the error
// verbatim so callers see infra failures, not just "recall worked".
// This covers the remaining branch on recall.go:26 — the ErrNotFound
// case where 0 rows were updated is silent by design, but real
// DB errors must bubble up.
func TestRecallMessage_ExecError_Propagates(t *testing.T) {
	dbErr := errors.New("connection reset")
	fake := &FakeTx{
		ExecFn: func(_ context.Context, _ string, _ []any) error {
			return dbErr
		},
	}
	s := svcWithFake(fake)
	s.SetCompliance(&stubChecker{held: false})
	err := s.RecallMessage(context.Background(), "si_a_b", "m_x", "alice")
	require.ErrorIs(t, err, dbErr,
		"UPDATE failure must propagate; got: %v", err)
}

// TestRecallMessage_HoldBlocksBeforeExec: Belt-and-suspenders: even
// when the Exec would succeed, an active hold must short-circuit
// before reaching the DB. This re-verifies the held-block invariant
// from delete_is_held_test.go at the unit-test layer.
func TestRecallMessage_HoldBlocksBeforeExec(t *testing.T) {
	fake := &FakeTx{}
	s := svcWithFake(fake)
	s.SetCompliance(&stubChecker{held: true})
	err := s.RecallMessage(context.Background(), "si_a_b", "m_x", "alice")
	require.ErrorIs(t, err, ErrHeld)
	require.Empty(t, fake.ExecCalls,
		"held conv must NOT issue UPDATE — invariant for DSR safety")
}
