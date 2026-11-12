// Package handlers provides HTTP REST endpoints for the Pulse platform.
package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/pulse-workspace/pulse/internal/cell/push"
	"github.com/pulse-workspace/pulse/pkg/shared/authn"
	"github.com/pulse-workspace/pulse/pkg/shared/logger"
)

// DeviceRegisterRequest defines the payload for registering a push token.
type DeviceRegisterRequest struct {
	Platform    string `json:"platform"`
	Token       string `json:"token"`
	AppBundleID string `json:"app_bundle_id"`
}

// DeviceUnregisterRequest defines the payload for unregistering a push token.
type DeviceUnregisterRequest struct {
	Token string `json:"token"`
}

// DeviceRegister handles POST /api/v1/devices/register.
// Binds or updates a push token for the authenticated user.
func DeviceRegister(pushSvc *push.Service, log logger.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := authn.ClaimsFromContext(r.Context())
		if c == nil || c.UserID == "" {
			WriteError(w, http.StatusUnauthorized, 10001, "unauthorized: missing user claims")
			return
		}

		var req DeviceRegisterRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, 10005, "invalid request body")
			return
		}

		deviceToken := &push.DeviceToken{
			UserID:      c.UserID,
			Platform:    req.Platform,
			Token:       req.Token,
			AppBundleID: req.AppBundleID,
		}

		if err := pushSvc.RegisterDevice(r.Context(), deviceToken); err != nil {
			log.Warn().Err(err).Str("user_id", c.UserID).Msg("failed to register device token")
			WriteError(w, http.StatusBadRequest, 10005, err.Error())
			return
		}

		WriteOK(w, map[string]any{
			"registered": true,
			"token_id":   deviceToken.ID,
		})
	}
}

// DeviceUnregister handles POST /api/v1/devices/unregister.
// Deactivates a device push token upon user sign-out.
func DeviceUnregister(pushSvc *push.Service, log logger.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := authn.ClaimsFromContext(r.Context())
		if c == nil || c.UserID == "" {
			WriteError(w, http.StatusUnauthorized, 10001, "unauthorized: missing user claims")
			return
		}

		var req DeviceUnregisterRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, 10005, "invalid request body")
			return
		}

		if err := pushSvc.UnregisterDevice(r.Context(), c.UserID, req.Token); err != nil {
			log.Warn().Err(err).Str("user_id", c.UserID).Msg("failed to unregister device token")
			WriteError(w, http.StatusInternalServerError, 10000, "failed to unregister device token")
			return
		}

		WriteOK(w, map[string]any{
			"unregistered": true,
		})
	}
}

// DeviceList handles GET /api/v1/devices.
// Retrieves all active push tokens registered to the authenticated user.
func DeviceList(pushSvc *push.Service, log logger.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := authn.ClaimsFromContext(r.Context())
		if c == nil || c.UserID == "" {
			WriteError(w, http.StatusUnauthorized, 10001, "unauthorized: missing user claims")
			return
		}

		tokens, err := pushSvc.ListActive(r.Context(), c.UserID)
		if err != nil {
			log.Warn().Err(err).Str("user_id", c.UserID).Msg("failed to list active device tokens")
			WriteError(w, http.StatusInternalServerError, 10000, "failed to query device tokens")
			return
		}

		WriteOK(w, tokens)
	}
}
