// Package search — persistence.go: SQL for full-text search.
//
// The query joins messages against conversations so the participants
// ACL is enforced in SQL — no Go-side post-filter is needed (and that
// would be wrong anyway because it leaks Total counts of conversations
// the user isn't in).
//
// Both the hits + the total COUNT use the same JOIN + filter so the
// counts reported to the client match the visible hits.
package search

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// searchQueryWithConvScope is the canonical search SELECT. The query
// is parameterised so no caller-controlled string lands in the SQL —
// Postgres binding handles escaping. The conv filter is added as an
// extra AND clause when set; the participants check stays the gate.
// searchSelectBase is the canonical search SELECT. The query
// is parameterised so no caller-controlled string lands in the SQL —
// Postgres binding handles escaping. The conv filter is added as an
// extra AND clause when set; the participants check stays the gate.
//
// `revoked_at IS NULL` is what marks a message as not-recalled —
// the messages table uses `revoked_at` (not `recalled_at`) per the
// 0001 migration; see internal/cell/message/recall.go for the writer.
const searchSelectBase = `
	SELECT m.msg_id, m.conv_id, m.sender_id, m.content_type, m.content,
	       ts_rank(m.tsv, q.q) AS rank, m.created_at
	FROM messages m
	JOIN conversations c ON c.conv_id = m.conv_id
	JOIN plainto_tsquery('simple', $2) q ON true
	WHERE c.participants @> ARRAY[$1]::text[]
	  AND m.revoked_at IS NULL
	  AND m.tsv @@ q.q`

// searchCountBase mirrors searchSelectBase but returns COUNT only. Used
// to populate SearchResult.Total so the client can render pagination.
const searchCountBase = `
	SELECT COUNT(*)
	FROM messages m
	JOIN conversations c ON c.conv_id = m.conv_id
	JOIN plainto_tsquery('simple', $2) q ON true
	WHERE c.participants @> ARRAY[$1]::text[]
	  AND m.revoked_at IS NULL
	  AND m.tsv @@ q.q`

// runSearch returns up to `limit` hits starting at `offset`, ordered by
// ts_rank DESC then created_at DESC. When convID is non-empty the
// result is scoped to that single conversation.
//
// The plainto_tsquery join is split out as a separate CTE-style join
// (`JOIN plainto_tsquery(...) q ON true`) so the tsquery is parsed once
// per row and reused for both the @@ check and the ts_rank() call —
// this is the canonical Postgres pattern for ranked FTS.
func (s *Service) runSearch(ctx context.Context, userID, query, convID string, limit, offset int) ([]SearchHit, error) {
	args := []any{userID, query}
	where := searchSelectBase
	if convID != "" {
		where += " AND m.conv_id = $3"
		args = append(args, convID)
	}
	// $4 / $5 are limit + offset when they're needed. We need a stable
	// ORDER BY so pagination is deterministic — ts_rank alone ties on
	// equal text.
	where += "\n\t\t  ORDER BY rank DESC, m.created_at DESC"
	args = append(args, limit, offset)
	where += fmt.Sprintf("\n\t\t  LIMIT $%d OFFSET $%d", len(args)-1, len(args))

	rows, err := s.db.Query(ctx, where, args...)
	if err != nil {
		return nil, fmt.Errorf("search: query: %w", err)
	}
	defer rows.Close()

	var hits []SearchHit
	for rows.Next() {
		var h SearchHit
		if err := rows.Scan(&h.MsgID, &h.ConvID, &h.SenderID, &h.ContentType, &h.Content,
			&h.Rank, &h.CreatedAt); err != nil {
			return nil, fmt.Errorf("search: scan: %w", err)
		}
		hits = append(hits, h)
	}
	return hits, rows.Err()
}

// countSearches returns the total matches for the same predicate as
// runSearch but without LIMIT/OFFSET, so SearchResult.Total reflects
// the full result-set size the caller is paging through.
func (s *Service) countSearches(ctx context.Context, userID, query, convID string) (int, error) {
	args := []any{userID, query}
	where := searchCountBase
	if convID != "" {
		where += " AND m.conv_id = $3"
		args = append(args, convID)
	}
	var n int
	if err := s.db.QueryRow(ctx, where, args...).Scan(&n); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// COUNT(*) never returns ErrNoRows, but defend anyway.
			return 0, nil
		}
		return 0, fmt.Errorf("search: count: %w", err)
	}
	return n, nil
}
