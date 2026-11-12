package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/pulse-workspace/pulse/internal/cell/conversation"
	"github.com/pulse-workspace/pulse/pkg/shared/authn"
	"github.com/pulse-workspace/pulse/pkg/shared/logger"
	"github.com/pulse-workspace/pulse/pkg/protocol"
)

// ConversationCreateRequest is the body for /conversation/create.
type ConversationCreateRequest struct {
	Participants []string `json:"participants"`
	Type         int      `json:"type"` // 1=single, 2=group
	GroupID      string   `json:"groupID,omitempty"`
	Name         string   `json:"name,omitempty"`
}

// ConversationCreate handles POST /api/v1/conversation/create.
//
// For single conversations (type=1), convID is derived deterministically
// as "si_" + sorted(userIDs).join("_") so two callers get the same convID.
func ConversationCreate(conv *conversation.Service, log logger.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req ConversationCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, 10005, "invalid request body")
			return
		}
		if len(req.Participants) == 0 {
			WriteError(w, http.StatusBadRequest, 10005, "participants required")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		// Default type based on participant count.
		if req.Type == 0 {
			if len(req.Participants) > 2 {
				req.Type = int(protocol.ConvGroup)
			} else {
				req.Type = int(protocol.ConvSingle)
			}
		}

		c, err := conv.GetOrCreate(ctx, req.Participants, req.Type)
		if err != nil {
			log.Error().Err(err).Msg("create conversation")
			WriteError(w, http.StatusInternalServerError, 10001, "internal error")
			return
		}
		log.Info().Str("conv", c.ConvID).Int("participants", len(req.Participants)).Msg("conversation created")
		WriteOK(w, c)
	}
}

// ConversationList handles GET /api/v1/conversation/list?limit=N.
func ConversationList(conv *conversation.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := authn.ClaimsFromContext(r.Context())
		if c == nil {
			WriteError(w, http.StatusUnauthorized, 1001, "missing claims")
			return
		}
		limit := 50
		if v := r.URL.Query().Get("limit"); v != "" {
			// ignore parse errors; default
			var n int
			_, _ = json.Number(v).Int64()
			// simple parse fallback:
			if len(v) > 0 {
				n = 0
				for _, ch := range v {
					if ch < '0' || ch > '9' {
						n = -1
						break
					}
					n = n*10 + int(ch-'0')
				}
				if n > 0 {
					limit = n
				}
			}
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		convs, err := conv.List(ctx, c.UserID, limit)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, 10001, "internal error")
			return
		}
		WriteOK(w, convs)
	}
}
