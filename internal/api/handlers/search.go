// Package handlers — search.go: HTTP surface for in-message full-text search.
//
// Endpoint:
//
//	GET /api/v1/search/messages?q=<query>&conv_id=<id>&limit=<n>&offset=<n>
//
// Authorization: a user JWT is required. The service-side SQL is the
// authority on what the caller can see — see internal/cell/search; the
// handler does NOT pre-filter conv_ids because that's brittle and the
// service-side JOIN is already gated by `participants @> ARRAY[$userID]`.
package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/pulse-im/pulse/internal/cell/search"
	"github.com/pulse-im/pulse/pkg/shared/authn"
	"github.com/pulse-im/pulse/pkg/shared/logger"
)

// SearchService is the narrow handler-package view of *search.Service.
// Mirrors the MediaService pattern so server.go can stay free of the
// internal/cell/search import.
type SearchService interface {
	Search(ctx context.Context, in search.SearchInput) (*search.SearchResult, error)
}

// RequireSearchService is the chi middleware that returns 503 when the
// search service hasn't been wired in (e.g. the migration hasn't run on
// the cell). Same posture as RequireMediaService / RequireMFAService.
func RequireSearchService(s SearchService) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if s == nil {
				WriteError(w, http.StatusServiceUnavailable, 10001, "search service unavailable")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// SearchMessages handles GET /api/v1/search/messages.
//
// Query parameters:
//
//	q        — required; trimmed; max 256 bytes (search.MaxQueryLength)
//	conv_id  — optional; restricts to one conversation
//	limit    — optional; clamped to [1, MaxLimit]; default 50
//	offset   — optional; clamped to [0, 10000]
//
// Returns 200 with `{hits, total}` (always non-nil empty slice when no
// matches so the client doesn't have to null-check). 400 on empty / too
// long query. 503 if the search service wasn't wired.
func SearchMessages(svc SearchService, log logger.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := authn.ClaimsFromContext(r.Context())
		if c == nil {
			WriteError(w, http.StatusUnauthorized, 1001, "missing claims")
			return
		}

		q := strings.TrimSpace(r.URL.Query().Get("q"))
		if q == "" {
			WriteError(w, http.StatusBadRequest, 10005, "q (query) required")
			return
		}
		if len(q) > search.MaxQueryLength {
			WriteError(w, http.StatusBadRequest, 10005, "q too long")
			return
		}

		in := search.SearchInput{
			UserID: c.UserID,
			Query:  q,
			ConvID: r.URL.Query().Get("conv_id"),
		}
		if v := r.URL.Query().Get("limit"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				WriteError(w, http.StatusBadRequest, 10005, "limit must be a non-negative integer")
				return
			}
			in.Limit = n
		}
		if v := r.URL.Query().Get("offset"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				WriteError(w, http.StatusBadRequest, 10005, "offset must be a non-negative integer")
				return
			}
			in.Offset = n
		}

		res, err := svc.Search(r.Context(), in)
		if err != nil {
			switch {
			case errors.Is(err, search.ErrEmptyQuery):
				WriteError(w, http.StatusBadRequest, 10005, "q (query) required")
			case errors.Is(err, search.ErrInvalidQueryLength):
				WriteError(w, http.StatusBadRequest, 10005, "q too long")
			default:
				log.Error().Err(err).Str("user_id", c.UserID).Msg("search: query failed")
				WriteError(w, http.StatusInternalServerError, 10001, "internal error")
			}
			return
		}
		log.Info().
			Str("user_id", c.UserID).
			Int("hits", len(res.Hits)).
			Int("total", res.Total).
			Str("conv_id", in.ConvID).
			Msg("search executed")
		WriteOK(w, res)
	}
}
