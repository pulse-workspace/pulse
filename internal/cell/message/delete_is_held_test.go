
//
// Tests compliance IsHeld enforcement on message delete and recall.
// added the complianceChecker interface (message.go:50). If IsHeld is not
// regression-tested, the held-items branch may regress in prod under
// either the sync (RecallMessage/DeleteMessage) or async (DSRWorker) path.
//
// Coverage matrix:
//
//	Held     → DeleteMessage/RecallMessage return ErrHeld, db untouched
//	No hold  → DELETE/UPDATE proceeds against real Postgres, row mutated
//
// Integration tests gated on PULSE_TEST_POSTGRES (mirrors db_test.go).
package message

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/require"

	"github.com/pulse-workspace/pulse/pkg/shared/db"
	"github.com/pulse-workspace/pulse/pkg/core"
)

// heldStub satisfies complianceChecker with a pre-canned answer; counts
// calls so tests can assert IsHeld was consulted exactly once.
type heldStub struct {
	held  bool
	err   error
	calls int
}

func (s *heldStub) IsHeld(_ context.Context, _ core.ComplianceScope, _ string) (bool, error) {
	s.calls++
	return s.held, s.err
}

// svcWithHeld wires Service{compliance: stub} with nil db+nats. The
// held-block path never reaches db/nats (checkHold runs first), so nil
// db is safe and panics if production regresses to mutate before the
// hold check.
func svcWithHeld(stub *heldStub) *Service {
	return &Service{compliance: stub}
}

// --- Unit tests: held-block path. No DB required. -------------------------

func TestDeleteMessage_HeldBlockReturnsErrHeld(t *testing.T) {
	stub := &heldStub{held: true}
	err := svcWithHeld(stub).DeleteMessage(context.Background(), "conv_held", "m_xyz")
	require.ErrorIs(t, err, ErrHeld)
	require.Equal(t, 1, stub.calls, "IsHeld must be consulted exactly once")
}

func TestRecallMessage_HeldBlockReturnsErrHeld(t *testing.T) {
	stub := &heldStub{held: true}
	err := svcWithHeld(stub).RecallMessage(context.Background(), "conv_held", "m_xyz", "alice")
	require.ErrorIs(t, err, ErrHeld)
	require.Equal(t, 1, stub.calls, "IsHeld must be consulted exactly once")
}

// TestHeldBlock_CheckHoldErrorPropagates: ErrNotLicensed is treated as
// no-hold (free-tier per audit P3); any other error must propagate.
func TestHeldBlock_CheckHoldErrorPropagates(t *testing.T) {
	dbErr := errors.New("db connection lost")
	stub := &heldStub{held: false, err: dbErr}
	err := svcWithHeld(stub).DeleteMessage(context.Background(), "conv_x", "m_y")
	require.ErrorIs(t, err, dbErr, "non-ErrNotLicensed errors must propagate")
}

// --- Integration tests: full DB path. Gated on PULSE_TEST_POSTGRES. -------

// pgDSN: when PULSE_TEST_POSTGRES is unset, integration cases skip.
// Pure-unit cases above run without Postgres.
func pgDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("PULSE_TEST_POSTGRES")
	if v == "" {
		t.Skip("PULSE_TEST_POSTGRES not set; skipping live-DB IsHeld regression")
	}
	return v
}

func seedMessage(t *testing.T, d *db.DB, convID, senderID string) string {
	t.Helper()
	msgID := "m_" + ulid.Make().String()
	err := d.Exec(context.Background(),
		`INSERT INTO messages (msg_id, conv_id, sender_id, content_type, content, seq, created_at)
		 VALUES ($1, $2, $3, 101, '{"text":"hi"}'::jsonb, 1, $4)`,
		msgID, convID, senderID, time.Now().UTC())
	require.NoError(t, err, "seed message %s", msgID)
	t.Cleanup(func() {
		_ = d.Exec(context.Background(), `DELETE FROM messages WHERE msg_id = $1`, msgID)
	})
	return msgID
}

func rowExists(t *testing.T, d *db.DB, msgID string) bool {
	t.Helper()
	var one int
	err := d.QueryRow(context.Background(),
		`SELECT 1 FROM messages WHERE msg_id = $1`, msgID).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return false
	}
	require.NoError(t, err)
	return true
}

func rowRecalledAt(t *testing.T, d *db.DB, msgID string) *time.Time {
	t.Helper()
	var recalled *time.Time
	err := d.QueryRow(context.Background(),
		`SELECT revoked_at FROM messages WHERE msg_id = $1`, msgID).Scan(&recalled)
	require.NoError(t, err)
	return recalled
}

// TestDeleteMessage_HeldBlocksDBMutation: core regression — held conv →
// ErrHeld → row is still present (no DELETE fired).
func TestDeleteMessage_HeldBlocksDBMutation(t *testing.T) {
	d, err := db.Open(context.Background(), pgDSN(t), 2)
	require.NoError(t, err)
	t.Cleanup(d.Close)

	convID := "si_isheld_del_" + ulid.Make().String()
	msgID := seedMessage(t, d, convID, "alice")
	require.True(t, rowExists(t, d, msgID), "seed must be visible before SUT")

	s := &Service{db: d, compliance: &heldStub{held: true}}
	err = s.DeleteMessage(context.Background(), convID, msgID)
	require.ErrorIs(t, err, ErrHeld)
	require.True(t, rowExists(t, d, msgID),
		"row must NOT be deleted when conv is under active legal hold")
}

// TestRecallMessage_HeldBlocksDBMutation: held → ErrHeld → revoked_at NULL.
func TestRecallMessage_HeldBlocksDBMutation(t *testing.T) {
	d, err := db.Open(context.Background(), pgDSN(t), 2)
	require.NoError(t, err)
	t.Cleanup(d.Close)

	convID := "si_isheld_recall_" + ulid.Make().String()
	msgID := seedMessage(t, d, convID, "alice")
	require.Nil(t, rowRecalledAt(t, d, msgID), "seed must not be recalled before SUT")

	s := &Service{db: d, compliance: &heldStub{held: true}}
	err = s.RecallMessage(context.Background(), convID, msgID, "alice")
	require.ErrorIs(t, err, ErrHeld)
	require.Nil(t, rowRecalledAt(t, d, msgID),
		"revoked_at must remain NULL when conv is under active legal hold")
}

// TestDeleteMessage_NoHoldProceeds: positive path — IsHeld=false → DELETE.
func TestDeleteMessage_NoHoldProceeds(t *testing.T) {
	d, err := db.Open(context.Background(), pgDSN(t), 2)
	require.NoError(t, err)
	t.Cleanup(d.Close)

	convID := "si_nohold_del_" + ulid.Make().String()
	msgID := seedMessage(t, d, convID, "alice")

	s := &Service{db: d, compliance: &heldStub{held: false}}
	err = s.DeleteMessage(context.Background(), convID, msgID)
	require.NoError(t, err)
	require.False(t, rowExists(t, d, msgID),
		"row must be deleted when no legal hold is active")
}

// TestRecallMessage_NoHoldProceeds: positive recall path. RecallMessage
// also publishes to NATS on success; nats stays nil because production
// only logs a warning on publish failure (recall.go:33). DB assertion is
// the load-bearing check.
func TestRecallMessage_NoHoldProceeds(t *testing.T) {
	d, err := db.Open(context.Background(), pgDSN(t), 2)
	require.NoError(t, err)
	t.Cleanup(d.Close)

	convID := "si_nohold_recall_" + ulid.Make().String()
	msgID := seedMessage(t, d, convID, "alice")
	require.Nil(t, rowRecalledAt(t, d, msgID))

	s := &Service{db: d, compliance: &heldStub{held: false}}
	err = s.RecallMessage(context.Background(), convID, msgID, "alice")
	require.NoError(t, err)
	require.NotNil(t, rowRecalledAt(t, d, msgID),
		"revoked_at must be set when no legal hold is active")
}
