package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/pulse-workspace/pulse/internal/cell/webhook"
	"github.com/pulse-workspace/pulse/pkg/shared/authn"
	"github.com/pulse-workspace/pulse/pkg/shared/logger"
)

// WebhookCreateRequest defines input body for creating a new webhook subscription.
type WebhookCreateRequest struct {
	URL    string   `json:"url"`
	Events []string `json:"events"`
}

// WebhookCreate handles POST /api/v1/webhooks.
//
// Business Logic:
// 1. Verifies authenticated admin/user context.
// 2. Decodes WebhookCreateRequest.
// 3. Registers webhook and returns created metadata along with secret.
func WebhookCreate(svc *webhook.Service, log logger.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := authn.ClaimsFromContext(r.Context())
		if c == nil || c.UserID == "" {
			WriteError(w, http.StatusUnauthorized, 10001, "unauthorized caller")
			return
		}

		var req WebhookCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, 10005, "invalid request body")
			return
		}

		if req.URL == "" || len(req.Events) == 0 {
			WriteError(w, http.StatusBadRequest, 10005, "url and at least one event are required")
			return
		}

		tenantID := c.CellID
		if tenantID == "" {
			tenantID = "default"
		}

		hook, secret, err := svc.Register(r.Context(), tenantID, req.URL, req.Events)
		if err != nil {
			log.Error().Err(err).Str("url", req.URL).Msg("failed to create webhook")
			WriteError(w, http.StatusInternalServerError, 10000, "failed to create webhook")
			return
		}

		WriteOK(w, map[string]any{
			"webhook": hook,
			"secret":  secret,
		})
	}
}

// WebhookList handles GET /api/v1/webhooks.
//
// Business Logic:
// Returns all registered webhooks for the caller's tenant.
func WebhookList(svc *webhook.Service, log logger.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := authn.ClaimsFromContext(r.Context())
		if c == nil || c.UserID == "" {
			WriteError(w, http.StatusUnauthorized, 10001, "unauthorized caller")
			return
		}

		tenantID := c.CellID
		if tenantID == "" {
			tenantID = "default"
		}

		hooks, err := svc.List(r.Context(), tenantID)
		if err != nil {
			log.Error().Err(err).Str("tenant", tenantID).Msg("failed to list webhooks")
			WriteError(w, http.StatusInternalServerError, 10000, "failed to list webhooks")
			return
		}

		WriteOK(w, map[string]any{
			"webhooks": hooks,
		})
	}
}

// WebhookDelete handles DELETE /api/v1/webhooks/{id}.
//
// Business Logic:
// Deletes a webhook subscription by URL path parameter id.
func WebhookDelete(svc *webhook.Service, log logger.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := authn.ClaimsFromContext(r.Context())
		if c == nil || c.UserID == "" {
			WriteError(w, http.StatusUnauthorized, 10001, "unauthorized caller")
			return
		}

		id := chi.URLParam(r, "id")
		if id == "" {
			WriteError(w, http.StatusBadRequest, 10005, "webhook id is required")
			return
		}

		if err := svc.Delete(r.Context(), id); err != nil {
			log.Error().Err(err).Str("id", id).Msg("failed to delete webhook")
			WriteError(w, http.StatusInternalServerError, 10000, "failed to delete webhook")
			return
		}

		WriteOK(w, map[string]any{
			"deleted": true,
		})
	}
}
