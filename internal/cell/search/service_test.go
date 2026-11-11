package search

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// These tests cover the input-validation paths in Service.Search +
// the wire-shape of SearchResult. The actual SQL / FTS path is
// exercised by integration tests that spin up real Postgres
// (//go:build integration); that's where you cover the SQL-injection
// posture + participants-ACL JOIN.

// ---- input validation guards (pre-DB) ----------------------------------

func TestSearchInput_RequiresUserID(t *testing.T) {
	s := &Service{}
	_, err := s.Search(t.Context(), SearchInput{Query: "hello"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "user_id")
}

func TestSearchInput_EmptyQuery(t *testing.T) {
	s := &Service{}
	for _, q := range []string{"", "   ", "\t\n"} {
		_, err := s.Search(t.Context(), SearchInput{UserID: "u_alice", Query: q})
		require.ErrorIs(t, err, ErrEmptyQuery, "query %q should map to ErrEmptyQuery", q)
	}
}

func TestSearchInput_QueryTooLong(t *testing.T) {
	s := &Service{}
	q := strings.Repeat("x", MaxQueryLength+1)
	_, err := s.Search(t.Context(), SearchInput{UserID: "u_alice", Query: q})
	require.ErrorIs(t, err, ErrInvalidQueryLength)
}

// ---- finaliseResult: the non-nil-hits guarantee (H3) -------------------

func TestFinaliseResult_GuaranteesNonNilHits(t *testing.T) {
	// Empty input hits slice + any total → never returns a nil-hits
	// SearchResult. This is the property the API client relies on.
	r := finaliseResult(nil, 0)
	require.NotNil(t, r.Hits, "hits must never be nil")
	require.Equal(t, []SearchHit{}, r.Hits)

	r = finaliseResult(nil, 7)
	require.NotNil(t, r.Hits)
	require.Equal(t, 0, len(r.Hits))
	require.Equal(t, 7, r.Total)
}

func TestFinaliseResult_PreservesExistingHits(t *testing.T) {
	in := []SearchHit{{MsgID: "m_1"}, {MsgID: "m_2"}}
	r := finaliseResult(in, 2)
	require.Equal(t, in, r.Hits)
	require.Equal(t, 2, r.Total)
}

// ---- JSON wire shape (M3) ---------------------------------------------

func TestSearchHit_JSONShape(t *testing.T) {
	h := SearchHit{
		MsgID:       "m_01",
		ConvID:      "si_alice_bob",
		SenderID:    "u_alice",
		ContentType: 101,
		Content:     map[string]any{"text": "hello world"},
		Rank:        0.124,
		CreatedAt:   time.Date(2026, 7, 15, 15, 2, 0, 0, time.UTC),
	}
	b, err := json.Marshal(h)
	require.NoError(t, err)
	s := string(b)

	// Every persisted field is asserted so a json-tag rename is
	// caught at unit-test layer, not at the SDK integration layer.
	for _, key := range []string{
		`"msg_id":"m_01"`,
		`"conv_id":"si_alice_bob"`,
		`"sender_id":"u_alice"`,
		`"content_type":101`,
		`"content":{"text":"hello world"}`,
		`"rank":0.124`,
		`"created_at":"2026-07-15T15:02:00Z"`,
	} {
		require.Contains(t, s, key, "missing key %s", key)
	}
}

func TestSearchResult_JSONShape_TotalAndHits(t *testing.T) {
	r := SearchResult{
		Hits:  []SearchHit{{MsgID: "m_1"}, {MsgID: "m_2"}},
		Total: 42,
	}
	b, err := json.Marshal(r)
	require.NoError(t, err)
	s := string(b)
	require.Contains(t, s, `"hits":[{`)
	require.Contains(t, s, `"total":42`)
}

func TestSearchResult_RoundTrip(t *testing.T) {
	// Decode the encode to make sure the wire shape is reversible —
	// protects against, e.g., json tags that lose information.
	in := SearchResult{
		Hits: []SearchHit{{
			MsgID:    "m_1",
			ConvID:   "si_alice_bob",
			SenderID: "u_alice",
			Rank:     0.5,
		}},
		Total: 1,
	}
	b, err := json.Marshal(in)
	require.NoError(t, err)
	var out SearchResult
	require.NoError(t, json.Unmarshal(b, &out))
	require.Equal(t, in.Total, out.Total)
	require.Equal(t, in.Hits[0].MsgID, out.Hits[0].MsgID)
	require.Equal(t, in.Hits[0].Rank, out.Hits[0].Rank)
}

// ---- sentinels ---------------------------------------------------------

func TestNoSentinelCollision(t *testing.T) {
	require.False(t, errors.Is(ErrEmptyQuery, ErrInvalidQueryLength))
	require.False(t, errors.Is(ErrInvalidQueryLength, ErrEmptyQuery))
}
