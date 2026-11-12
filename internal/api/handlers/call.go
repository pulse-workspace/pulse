package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/pulse-workspace/pulse/internal/cell/call"
	"github.com/pulse-workspace/pulse/pkg/shared/authn"
	"github.com/pulse-workspace/pulse/pkg/shared/logger"
)

// CallTokenRequest defines payload for obtaining LiveKit room token.
type CallTokenRequest struct {
	RoomName        string `json:"roomName"`
	ParticipantName string `json:"participantName,omitempty"`
}

// CallTokenResponse defines output payload with signed LiveKit JWT and SFU endpoint URL.
type CallTokenResponse struct {
	Token string `json:"token"`
	URL   string `json:"url"`
}

// CallSignalRequest defines payload for dispatching WebRTC signaling frame.
type CallSignalRequest struct {
	SignalType string         `json:"signalType"` // Ring, Accept, Reject, Hangup, Candidate
	TargetID   string         `json:"targetID"`
	RoomName   string         `json:"roomName"`
	Data       map[string]any `json:"data,omitempty"`
}

// CallToken generates an access token for participating in a LiveKit audio/video room.
//
// Business Logic:
// 1. Verifies authenticated caller from JWT context.
// 2. Validates roomName parameter.
// 3. Generates signed LiveKit JWT token using call service.
// 4. Returns token along with LiveKit gateway URL.
func CallToken(svc *call.Service, log logger.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := authn.ClaimsFromContext(r.Context())
		if c == nil || c.UserID == "" {
			WriteError(w, http.StatusUnauthorized, 10001, "unauthorized caller")
			return
		}

		var req CallTokenRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, 10005, "invalid request body")
			return
		}

		if req.RoomName == "" {
			WriteError(w, http.StatusBadRequest, 10005, "roomName is required")
			return
		}

		name := req.ParticipantName
		if name == "" {
			name = c.UserID
		}

		token, err := svc.GenerateToken(req.RoomName, c.UserID, name)
		if err != nil {
			log.Error().Err(err).Str("room", req.RoomName).Str("user", c.UserID).Msg("failed to generate call token")
			WriteError(w, http.StatusInternalServerError, 10000, "failed to generate call token")
			return
		}

		WriteOK(w, CallTokenResponse{
			Token: token,
			URL:   svc.GetLiveKitURL(),
		})
	}
}

// CallSignal relays a WebRTC signaling message to a target user across the cluster.
//
// Business Logic:
// 1. Verifies authenticated caller identity.
// 2. Decodes CallSignalRequest and validates mandatory signalType and targetID fields.
// 3. Dispatches payload via call service over NATS message broker.
func CallSignal(svc *call.Service, log logger.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := authn.ClaimsFromContext(r.Context())
		if c == nil || c.UserID == "" {
			WriteError(w, http.StatusUnauthorized, 10001, "unauthorized caller")
			return
		}

		var req CallSignalRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, 10005, "invalid request body")
			return
		}

		if req.SignalType == "" || req.TargetID == "" {
			WriteError(w, http.StatusBadRequest, 10005, "signalType and targetID are required")
			return
		}

		err := svc.DispatchSignal(
			r.Context(),
			req.SignalType,
			c.UserID,
			req.TargetID,
			req.RoomName,
			req.Data,
		)
		if err != nil {
			log.Error().Err(err).Str("target", req.TargetID).Str("signal", req.SignalType).Msg("failed to dispatch call signal")
			WriteError(w, http.StatusInternalServerError, 10000, "failed to dispatch signal")
			return
		}

		WriteOK(w, map[string]any{
			"dispatched": true,
		})
	}
}
