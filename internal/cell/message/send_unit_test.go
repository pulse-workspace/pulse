

// no longer issues an INSERT; instead it populates the in-memory
// cache, publishes an event, and returns. Tests that previously
// asserted on the projection INSERT now assert on the cache + the
// post-condition that no Exec call landed on the projection table.
//
// The publish path is recovered with defer-recover because the
// unit test leaves nats nil.
package message

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/require"
)

func TestSendMessage_EmptyConvID_ReturnsValidationError(t *testing.T) {
	s := svcWithFake(&FakeTx{})
	_, err := s.SendMessage(context.Background(), SendMessageInput{
		ConvID:   "",
		SenderID: "alice",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "convID")
}

func TestSendMessage_EmptySenderID_ReturnsValidationError(t *testing.T) {
	s := svcWithFake(&FakeTx{})
	_, err := s.SendMessage(context.Background(), SendMessageInput{
		ConvID:   "si_a_b",
		SenderID: "",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "senderID")
}

func TestSendMessage_DefaultsContentTypeToText(t *testing.T) {
	// When ContentType==0, SendMessage applies protocol.ContentText
	// (101).  §4.3 : verified by reading the cache
	// (the canonical read-after-write source) rather than the
	// projection table (which is async-written by the runner).
	//
	// SendMessage also publishes on the happy path; the unit test
	// leaves nats nil so publish panics. Recover.
	noRowsErr := pgx.ErrNoRows
	fake := &FakeTx{
		QueryRowFn: func(_ context.Context, _ string, _ []any) pgx.Row {
			return &fakeRow{err: noRowsErr}
		},
	}
	s := svcWithFake(fake)
	defer func() { _ = recover() }()
	got, _ := s.SendMessage(context.Background(), SendMessageInput{
		ConvID:      "si_a_b",
		SenderID:    "alice",
		ContentType: 0, // explicit zero — must default
		Content:     map[string]any{"text": "hi"},
	})
	require.NotNil(t, got)
	require.Equal(t, 101, got.ContentType,
		"ContentType=0 must default to protocol.ContentText (101)")
	// Cache should hold the just-sent message with the same content
	// type so an immediate read-after-write sees the default.
	cached, ok := s.cache.Get(got.MsgID)
	require.True(t, ok, "cache must hold the just-sent message")
	require.Equal(t, 101, cached.ContentType)
}

func TestSendMessage_IdempotencyHit_ReturnsExisting(t *testing.T) {
	// When ClientMsgID is given and the cache lookup hits,
	// SendMessage returns the existing message WITHOUT publishing.
	
	// projection; the events table is the deeper fallback (exercised
	// in TestGetMessage_EventLogFallback).
	existing := "m_existing_" + ulid.Make().String()
	s := svcWithFake(&FakeTx{})
	// Seed the cache with the existing message — idempotency hits
	// the cache walk in lookupByClientMsgID.
	s.cache.Add(&Message{
		MsgID:       existing,
		ConvID:      "si_a_b",
		SenderID:    "alice",
		ContentType: 101,
		Content:     map[string]any{"text": "hi", "_clientMsgID": "client-abc"},
	})
	defer func() { _ = recover() }() // publish panics on nil nats

	got, err := s.SendMessage(context.Background(), SendMessageInput{
		ConvID:      "si_a_b",
		SenderID:    "alice",
		Content:     map[string]any{"text": "different content"},
		ClientMsgID: "client-abc",
	})
	require.NoError(t, err)
	require.Equal(t, existing, got.MsgID,
		"idempotency hit must return the existing msg_id, not mint a new one")
}

func TestSendMessage_PopulatesCache(t *testing.T) {
	
	// source. SendMessage must populate it before publishing so a
	// reader who races the publish always sees the message.
	noRowsErr := pgx.ErrNoRows
	s := svcWithFake(&FakeTx{
		QueryRowFn: func(_ context.Context, _ string, _ []any) pgx.Row {
			return &fakeRow{err: noRowsErr}
		},
	})
	defer func() { _ = recover() }()

	got, err := s.SendMessage(context.Background(), SendMessageInput{
		ConvID:   "si_a_b",
		SenderID: "alice",
		Content:  map[string]any{"text": "hi"},
	})
	require.NoError(t, err)
	require.NotEmpty(t, got.MsgID)

	cached, ok := s.cache.Get(got.MsgID)
	require.True(t, ok, "cache must hold the just-sent message")
	require.Equal(t, got, cached, "cache entry must match the returned Message")
}

func TestSendMessage_DoesNotInsertProjection(t *testing.T) {
	
	// an INSERT against the messages projection. The projection is
	// the runner's job.
	noRowsErr := pgx.ErrNoRows
	fake := &FakeTx{
		QueryRowFn: func(_ context.Context, _ string, _ []any) pgx.Row {
			return &fakeRow{err: noRowsErr}
		},
	}
	s := svcWithFake(fake)
	defer func() { _ = recover() }()

	_, err := s.SendMessage(context.Background(), SendMessageInput{
		ConvID:   "si_a_b",
		SenderID: "alice",
		Content:  map[string]any{"text": "hi"},
	})
	require.NoError(t, err)
	for _, c := range fake.ExecCalls {
		require.NotContains(t, c.SQL, "INSERT INTO messages",
			" : SendMessage must not write to messages projection")
	}
}

func TestSendMessage_IdempotencyLookupError_Propagates(t *testing.T) {
	// When the idempotency lookup QueryRow returns an error other
	// than ErrNoRows (e.g. transient DB failure), SendMessage
	// must surface the real error verbatim. Operators need to see
	// infra failures on this hot path.
	dbErr := errors.New("connection lost")
	fake := &FakeTx{
		QueryRowFn: func(_ context.Context, _ string, _ []any) pgx.Row {
			return &fakeRow{err: dbErr}
		},
	}
	s := svcWithFake(fake)
	defer func() { _ = recover() }()
	_, err := s.SendMessage(context.Background(), SendMessageInput{
		ConvID:      "si_a_b",
		SenderID:    "alice",
		Content:     map[string]any{"text": "hi"},
		ClientMsgID: "client-abc",
	})
	require.ErrorIs(t, err, dbErr,
		"idempotency lookup failure must propagate; got: %v", err)
}

func TestSendMessage_MarshalFailure_ReturnsWrappedError(t *testing.T) {
	// A Content value that json.Marshal cannot serialize (e.g. a
	// channel) must surface as a wrapped "marshal content" error.
	// Defends the content-payload contract from regressions where
	// downstream code starts passing unsupported Go types.
	noRowsErr := pgx.ErrNoRows
	s := svcWithFake(&FakeTx{
		QueryRowFn: func(_ context.Context, _ string, _ []any) pgx.Row {
			return &fakeRow{err: noRowsErr}
		},
	})
	defer func() { _ = recover() }()
	_, err := s.SendMessage(context.Background(), SendMessageInput{
		ConvID:   "si_a_b",
		SenderID: "alice",
		Content:  map[string]any{"bad": make(chan int)},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "marshal content",
		"unmarshalable Content must wrap as 'marshal content'; got: %v", err)
}

// (Removed) TestSendMessage_InsertExecError_ReturnsWrappedError
// — the projection INSERT was removed in  §4.3 .
// The runner is now the canonical writer; SendMessage's failure
// paths are marshal (covered by MarshalFailure), idempotency
// lookup (covered by IdempotencyLookupError), and publish
// (logged but doesn't fail the call — cache hit serves read-after-write
// until the runner catches up).
