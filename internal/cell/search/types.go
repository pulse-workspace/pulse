// Package search provides in-message full-text search backed by PostgreSQL FTS.
//
// File layout:
//   - types.go        — SearchInput / SearchHit / SearchResult DTOs and errors
//   - persistence.go  — SQL queries matching user conversations via tsvector
//   - service.go      — search domain service implementation
// Authorization: a search is always scoped to the caller's conversations
// (JOIN conversations ON participants @> $userID). The query parameter
// itself is treated as untrusted text — the handler trims + length-caps
// before passing it to the service so a 100kB junk payload can't amplify
// into a 100kB tsquery.
package search

import (
	"errors"
	"time"
)

// SearchInput is the service-level DTO Search expects. Fields mirror the
// query params of /api/v1/search/messages — keep them in lock-step.
type SearchInput struct {
	UserID string // required — used for the participants-ACL JOIN
	Query  string // required (trimmed + length-capped by the handler)
	ConvID string // optional — empty = search across every conv the user is in
	Limit  int    // 0 uses default (DefaultLimit); > MaxLimit is clamped
	Offset int    // for pagination
}

// SearchHit is one message that matched the query, with a rank so the
// caller can render them in relevance order (or fall back to created_at
// when ranks tie).
type SearchHit struct {
	MsgID       string         `json:"msg_id"`
	ConvID      string         `json:"conv_id"`
	SenderID    string         `json:"sender_id"`
	ContentType int            `json:"content_type"`
	Content     map[string]any `json:"content"`
	Rank        float32        `json:"rank"`
	CreatedAt   time.Time      `json:"created_at"`
}

// SearchResult wraps the hits + a Total count for clients that want to
// render "X results" copy. Total is the count BEFORE limit/offset so the
// caller can show pagination. Computing it requires a second COUNT(*)
// query — if that turns hot we can switch to a cursor or use
// pg_stat_statements.
type SearchResult struct {
	Hits  []SearchHit `json:"hits"`
	Total int         `json:"total"`
}

// ErrEmptyQuery — the handler refused an empty / whitespace-only query.
// Distinct from "no results" so the handler can map it to 400 instead
// of an empty 200.
var ErrEmptyQuery = errors.New("search: empty query")

// ErrInvalidQueryLength — the trimmed query exceeded MaxQueryLength.
// Surfaces to the API as 400 (caller error) with a clear retry hint.
var ErrInvalidQueryLength = errors.New("search: query exceeds maximum length")

// DefaultLimit is the page size when the caller doesn't specify one.
// 50 keeps one GIN result-set comfortable under a 100ms p95 search SLA.
const DefaultLimit = 50

// MaxLimit caps the worst-case page size. 200 mirrors the other list
// endpoints in the cell so a single UI sweep can land in one round trip.
const MaxLimit = 200

// MaxQueryLength bounds the untrusted query parameter. 256 bytes covers
// every realistic phrase-search length while keeping the tsvector @@
// tsquery cost predictable. The handler enforces this before the service
// is called; the service re-checks as defence-in-depth.
const MaxQueryLength = 256
