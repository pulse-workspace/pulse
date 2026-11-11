
// machinery the async projection refactor introduced. The cache
// tests + the event log fallback are net-new coverage; the
// refactor's existing send_unit_test.go was rewritten in the same
// commit.
package message

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

// ---- recentCache unit tests (cache.go) -------------------------------

func TestRecentCache_AddAndGet(t *testing.T) {
	c := newRecentCache(10)
	c.Add(&Message{MsgID: "m_1", Content: map[string]any{"text": "a"}})

	got, ok := c.Get("m_1")
	require.True(t, ok)
	require.Equal(t, "m_1", got.MsgID)
	require.Equal(t, "a", got.Content["text"])

	_, ok = c.Get("missing")
	require.False(t, ok)
}

func TestRecentCache_AddUpdatesValue(t *testing.T) {
	// Re-adding an existing msgID with a different Message updates
	// the value in place (caller may re-send with corrected content).
	c := newRecentCache(10)
	c.Add(&Message{MsgID: "m_1", Content: map[string]any{"text": "v1"}})
	c.Add(&Message{MsgID: "m_1", Content: map[string]any{"text": "v2"}})

	got, ok := c.Get("m_1")
	require.True(t, ok)
	require.Equal(t, "v2", got.Content["text"])
	require.Equal(t, 1, c.Len(), "re-add must not duplicate")
}

func TestRecentCache_LRUEviction(t *testing.T) {
	// Cap of 3. Insert 4 → the oldest (m_0) is evicted.
	c := newRecentCache(3)
	for i := 0; i < 4; i++ {
		c.Add(&Message{MsgID: "m_" + string(rune('0'+i))})
	}
	require.Equal(t, 3, c.Len())

	// m_0 evicted, m_1..m_3 present.
	_, ok := c.Get("m_0")
	require.False(t, ok, "m_0 must have been evicted")
	for _, id := range []string{"m_1", "m_2", "m_3"} {
		_, ok := c.Get(id)
		require.True(t, ok, "%s must be in cache", id)
	}
}

func TestRecentCache_GetPromotesToFront(t *testing.T) {
	// Cap 2 → inserting 3 evicts the LRU. Touching m_0 (oldest)
	// promotes it; the next insertion evicts m_1 instead.
	c := newRecentCache(2)
	c.Add(&Message{MsgID: "m_0"})
	c.Add(&Message{MsgID: "m_1"})
	// Touch m_0 → m_0 is now MRU; m_1 is LRU.
	_, _ = c.Get("m_0")
	// Inserting m_2 → evicts m_1 (LRU), keeps m_0 + m_2.
	c.Add(&Message{MsgID: "m_2"})
	_, ok := c.Get("m_0")
	require.True(t, ok, "m_0 should survive — promoted to front by Get")
	_, ok = c.Get("m_1")
	require.False(t, ok, "m_1 should be evicted (was LRU after m_0 promotion)")
}

func TestRecentCache_NilMessageSafe(t *testing.T) {
	c := newRecentCache(10)
	require.NotPanics(t, func() {
		c.Add(nil)
		c.Add(&Message{MsgID: ""})
	})
	require.Equal(t, 0, c.Len())
}

// ---- getMessageFromEventLog fallback tests -----------------------

// TestGetMessage_EventLogFallback exercises the  
// third-tier fallback: when neither cache nor projection has the
// message, reconstruct from the events table's JSONB data column.
//
// The fake mocks QueryRow so the projection SELECT returns
// ErrNoRows; the same QueryRow mock returns the event row for the
// events-table SELECT. The test asserts the reconstructed Message
// matches the canonical fields.
func TestGetMessage_EventLogFallback(t *testing.T) {
	evtData, _ := json.Marshal(map[string]any{
		"msgID":       "m_evt_1",
		"convID":      "si_a_b",
		"senderID":    "alice",
		"contentType": 101,
		"content":     map[string]any{"text": "hello"},
		"seq":         7,
		"createdAt":   "2026-07-22T10:00:00Z",
	})

	// QueryRowFn dispatches based on the SQL string. The projection
	// SELECT hits the messages table; the fallback hits events.
	fake := &FakeTx{
		QueryRowFn: func(_ context.Context, sql string, _ []any) pgx.Row {
			if stringsContains(sql, "FROM messages") {
				return &fakeRow{err: pgx.ErrNoRows}
			}
			if stringsContains(sql, "FROM events") {
				// Return the stream/type/text tuple the fallback
				// SELECT expects.
				return &cannedRow{values: []any{"events.messages", "MessageSent", string(evtData)}}
			}
			return &fakeRow{err: errors.New("unexpected SQL")}
		},
	}
	s := svcWithFake(fake)
	// cache miss → projection miss → event log hit
	m, err := s.GetMessage(context.Background(), "m_evt_1")
	require.NoError(t, err)
	require.Equal(t, "m_evt_1", m.MsgID)
	require.Equal(t, "si_a_b", m.ConvID)
	require.Equal(t, "alice", m.SenderID)
	require.Equal(t, 101, m.ContentType)
	require.Equal(t, "hello", m.Content["text"])
	require.Equal(t, int64(7), m.Seq)
	require.Equal(t, 2026, m.CreatedAt.Year())
}

// TestGetMessage_CacheHit verifies the read-after-write contract:
// send → immediate GetMessage → cache hit (no projection lookup,
// no event log fallback).
func TestGetMessage_CacheHit(t *testing.T) {
	s := svcWithFake(&FakeTx{})
	// Seed the cache directly (saves us from dealing with the
	// nil-nats panic on SendMessage's publish step).
	want := &Message{
		MsgID:       "m_cached",
		ConvID:      "si_a_b",
		SenderID:    "alice",
		ContentType: 101,
		Content:     map[string]any{"text": "from cache"},
	}
	s.cache.Add(want)

	got, err := s.GetMessage(context.Background(), "m_cached")
	require.NoError(t, err)
	require.Equal(t, want, got)
}

// TestGetMessage_AllThreeMiss_ReturnsErrNotFound — cache miss,
// projection miss, event log miss → 404.
func TestGetMessage_AllThreeMiss_ReturnsErrNotFound(t *testing.T) {
	fake := &FakeTx{
		QueryRowFn: func(_ context.Context, sql string, _ []any) pgx.Row {
			if stringsContains(sql, "FROM messages") {
				return &fakeRow{err: pgx.ErrNoRows}
			}
			if stringsContains(sql, "FROM events") {
				return &fakeRow{err: pgx.ErrNoRows}
			}
			return &fakeRow{err: errors.New("unexpected SQL")}
		},
	}
	s := svcWithFake(fake)
	_, err := s.GetMessage(context.Background(), "m_ghost")
	require.ErrorIs(t, err, ErrNotFound)
}

// TestGetMessage_ProjectionErrorBubbles — a non-ErrNoRows
// projection failure must surface as an error (not silently
// fall through to the event log with a 404).
func TestGetMessage_ProjectionErrorBubbles(t *testing.T) {
	dbErr := errors.New("projection db down")
	fake := &FakeTx{
		QueryRowFn: func(_ context.Context, sql string, _ []any) pgx.Row {
			if stringsContains(sql, "FROM messages") {
				return &fakeRow{err: dbErr}
			}
			return &fakeRow{err: errors.New("unexpected SQL")}
		},
	}
	s := svcWithFake(fake)
	_, err := s.GetMessage(context.Background(), "m_x")
	require.ErrorIs(t, err, dbErr)
}

// TestRecentCache_Drain_OrderMatchesLRU — drain returns messages
// most-recent-first (the same order Get uses to promote on hit).
func TestRecentCache_Drain_OrderMatchesLRU(t *testing.T) {
	c := newRecentCache(10)
	c.Add(&Message{MsgID: "m_0"})
	c.Add(&Message{MsgID: "m_1"})
	c.Add(&Message{MsgID: "m_2"})

	out := c.drain()
	require.Len(t, out, 3)
	require.Equal(t, "m_2", out[0].MsgID, "MRU must be first")
	require.Equal(t, "m_0", out[2].MsgID, "LRU must be last")
}

// TestRecentCache_ConcurrentAccess_NoRace — a quick concurrent
// smoke test (run with `go test -race` to catch races).
func TestRecentCache_ConcurrentAccess_NoRace(t *testing.T) {
	c := newRecentCache(100)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			c.Add(&Message{MsgID: "race_a"})
		}
		close(done)
	}()
	for i := 0; i < 100; i++ {
		_, _ = c.Get("race_a")
	}
	<-done
	// Drain must show at least one entry (cap allows the latest 100
	// insertions to coexist; some may have evicted each other).
	out := c.drain()
	require.NotEmpty(t, out)
}

// stringsContains is a thin alias for strings.Contains. Kept as a
// tiny shim so the test dispatch reads as "FROM messages" /
// "FROM events" without the imports.StringContains noise at every
// call site.
func stringsContains(s, sub string) bool { return strings.Contains(s, sub) }

// ---- SanitizedContent (code-review H2/H3) -------------------------------

func TestSanitizedContent_StripsUnderscorePrefixed(t *testing.T) {
	// Code-review  §4.3  H2: every read edge
	// (cache, projection, event log) MUST pass through
	// SanitizedContent so the idempotency key never leaks to
	// downstream consumers.
	m := &Message{
		MsgID: "m_1",
		Content: map[string]any{
			"text":         "hello",
			"_clientMsgID": "client-abc",
			"_internal":    "should not leak",
		},
	}
	out := m.SanitizedContent()
	require.Equal(t, "hello", out["text"], "user-visible key preserved")
	_, ok := out["_clientMsgID"]
	require.False(t, ok, "idempotency key must be stripped")
	_, ok = out["_internal"]
	require.False(t, ok, "any underscore-prefixed key must be stripped")
}

func TestSanitizedContent_NilSafe(t *testing.T) {
	require.NotPanics(t, func() {
		var m *Message
		_ = m.SanitizedContent()
	})
	m := &Message{Content: nil}
	require.Nil(t, m.SanitizedContent())
}

func TestSanitizedContent_ReturnsCopy(t *testing.T) {
	// Mutating the returned map must not affect the source.
	m := &Message{Content: map[string]any{"text": "hi", "_k": "v"}}
	out := m.SanitizedContent()
	out["text"] = "changed"
	require.Equal(t, "hi", m.Content["text"], "source must not be mutated through the returned map")
}

// TestGetHistory_SanitizesRows mirrors TestGetMessage_AllThreeMiss's
// sanitisation story at the GetHistory read edge. The history
// reader returns multiple rows; every row must pass through
// SanitizedContent. (Code-review  §4.3  H2/H3 —
// the docs sync-back agent flagged this gap; this test closes it.)
func TestGetHistory_SanitizesRows(t *testing.T) {
	// Two rows; both carry _clientMsgID in the projection. After
	// GetHistory, the underscore-prefixed key must be stripped from
	// every row's Content.
	row1 := []any{
		"m_1", "si_a_b", "alice", 101,
		map[string]any{"text": "hi", "_clientMsgID": "client-1"},
		int64(1), time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC), (*time.Time)(nil),
	}
	row2 := []any{
		"m_2", "si_a_b", "bob", 101,
		map[string]any{"text": "reply", "_clientMsgID": "client-2"},
		int64(2), time.Date(2026, 7, 22, 10, 1, 0, 0, time.UTC), (*time.Time)(nil),
	}
	fake := &FakeTx{
		QueryFn: func(_ context.Context, _ string, _ []any) (pgx.Rows, error) {
			return &cannedRows{rows: [][]any{row1, row2}}, nil
		},
	}
	s := svcWithFake(fake)
	got, err := s.GetHistory(context.Background(), "si_a_b", 50)
	require.NoError(t, err)
	require.Len(t, got, 2)
	for i, m := range got {
		require.NotNil(t, m)
		_, hasClientMsgID := m.Content["_clientMsgID"]
		require.False(t, hasClientMsgID,
			"row %d must have _clientMsgID stripped; got: %v", i, m.Content)
	}
	// user-visible content preserved
	require.Equal(t, "hi", got[0].Content["text"])
	require.Equal(t, "reply", got[1].Content["text"])
}
