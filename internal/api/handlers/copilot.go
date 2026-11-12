package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/pulse-workspace/pulse/internal/cell/copilot"
	"github.com/pulse-workspace/pulse/pkg/shared/authn"
	"github.com/pulse-workspace/pulse/pkg/shared/logger"
)

// CopilotSummaryRequest defines payload for thread summarization request.
type CopilotSummaryRequest struct {
	Messages []copilot.ThreadMessage `json:"messages"`
}

// CopilotSmartRepliesRequest defines payload for smart reply generation request.
type CopilotSmartRepliesRequest struct {
	Messages []copilot.ThreadMessage `json:"messages"`
}

// CopilotSummary handles POST /api/v1/copilot/summary.
//
// Business Logic:
// 1. Verifies caller is authenticated.
// 2. Decodes message list and forwards to Copilot service to produce thread summary.
// 3. Returns generated summary text.
func CopilotSummary(svc *copilot.Service, log logger.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := authn.ClaimsFromContext(r.Context())
		if c == nil || c.UserID == "" {
			WriteError(w, http.StatusUnauthorized, 10001, "unauthorized caller")
			return
		}

		var req CopilotSummaryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, 10005, "invalid request body")
			return
		}

		if len(req.Messages) == 0 {
			WriteError(w, http.StatusBadRequest, 10005, "messages list cannot be empty")
			return
		}

		summary, err := svc.Summarize(r.Context(), req.Messages)
		if err != nil {
			log.Error().Err(err).Msg("failed to summarize thread")
			WriteError(w, http.StatusInternalServerError, 10000, "failed to summarize thread")
			return
		}

		WriteOK(w, map[string]any{
			"summary": summary,
		})
	}
}

// CopilotSmartReplies handles POST /api/v1/copilot/smart-replies.
//
// Business Logic:
// Generates 3 contextual quick-reply options using AI model.
func CopilotSmartReplies(svc *copilot.Service, log logger.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := authn.ClaimsFromContext(r.Context())
		if c == nil || c.UserID == "" {
			WriteError(w, http.StatusUnauthorized, 10001, "unauthorized caller")
			return
		}

		var req CopilotSmartRepliesRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, 10005, "invalid request body")
			return
		}

		if len(req.Messages) == 0 {
			WriteError(w, http.StatusBadRequest, 10005, "messages list cannot be empty")
			return
		}

		replies, err := svc.SmartReplies(r.Context(), req.Messages)
		if err != nil {
			log.Error().Err(err).Msg("failed to generate smart replies")
			WriteError(w, http.StatusInternalServerError, 10000, "failed to generate smart replies")
			return
		}

		WriteOK(w, map[string]any{
			"replies": replies,
		})
	}
}
