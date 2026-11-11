// Package search — service.go: cell.Service implementation + Search method.
//
// The Service is stateless aside from the DB handle — there's no cache,
// no projection, no NATS subscription. Start/Stop log + return.
//
// ACL posture: every search is scoped to the calling user. We enforce
// this in SQL by JOINing conversations and filtering on `participants
// @> ARRAY[$userID]` — see persistence.go. The UserID on SearchInput is
// the ONLY source of truth; the handler doesn't trust any caller-side
// scoping.
package search

import (
	"context"
	"fmt"
	"strings"

	"github.com/pulse-im/pulse/internal/cell"
	"github.com/pulse-im/pulse/pkg/shared/db"
	"github.com/pulse-im/pulse/pkg/shared/logger"
)

// Service is the full-text search cell service.
type Service struct {
	cell *cell.Cell
	log  logger.Logger
	db   db.DBTX
}

// NewService constructs the search service. The cell bundle's DB is
// used directly; no separate repository, no cache.
func NewService(c *cell.Cell) *Service {
	return &Service{
		cell: c,
		log:  c.Log.With().Str("service", "search").Logger(),
		db:   c.DB,
	}
}

// Name implements cell.Service.
func (s *Service) Name() string { return "search" }

// Start logs the service is up. No background work.
func (s *Service) Start(ctx context.Context) error {
	s.log.Info().Int64("max_query_bytes", MaxQueryLength).Msg("search service started")
	return nil
}

// Stop is a no-op — no goroutines, no subscriptions to drain.
func (s *Service) Stop(ctx context.Context) error { return nil }

// Search is the only public method. It trims/normalises the query
// (defence-in-depth for the handler), then delegates to runSearch +
// countSearches.
//
// Limits are clamped here, not in the handler, so any future caller
// (CLI / admin tool / batch job) gets the same envelope.
func (s *Service) Search(ctx context.Context, in SearchInput) (*SearchResult, error) {
	if in.UserID == "" {
		return nil, fmt.Errorf("search: user_id required")
	}
	q := strings.TrimSpace(in.Query)
	if q == "" {
		return nil, ErrEmptyQuery
	}
	if len(q) > MaxQueryLength {
		return nil, ErrInvalidQueryLength
	}
	limit := in.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	if limit > MaxLimit {
		limit = MaxLimit
	}
	offset := max(0, in.Offset)
	offset = min(offset, 10_000)

	hits, err := s.runSearch(ctx, in.UserID, q, in.ConvID, limit, offset)
	if err != nil {
		return nil, err
	}
	total, err := s.countSearches(ctx, in.UserID, q, in.ConvID)
	if err != nil {
		// Surface the hits even if the count query failed — the result
		// list is still useful, and the client can detect the missing
		// total by checking `total==0 && len(hits)>0`.
		s.log.Warn().Err(err).Msg("search: count query failed")
	}

	return finaliseResult(hits, total), nil
}

// finaliseResult is the non-nil-hits guarantee. Pulled out of Search
// so the test for it (TestSearchResult_GuaranteesNonNilHits) can
// exercise the path without spinning a full DB pool. Production and
// test code share the same finalisation function.
func finaliseResult(hits []SearchHit, total int) *SearchResult {
	if hits == nil {
		hits = []SearchHit{}
	}
	return &SearchResult{Hits: hits, Total: total}
}
