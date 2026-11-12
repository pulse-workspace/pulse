package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pulse-workspace/pulse/internal/cell/call"
	"github.com/pulse-workspace/pulse/pkg/shared/authn"
	"github.com/pulse-workspace/pulse/pkg/shared/logger"
)

func setupCallTest(t *testing.T) (*call.Service, *authn.Issuer, string, logger.Logger) {
	log := logger.New(logger.Config{Level: "debug"})
	cfg := &call.Config{
		LiveKitURL:    "https://livekit.pulse.internal",
		LiveKitAPIKey: "api_key_123",
		LiveKitSecret: "secret_456",
	}
	svc, err := call.NewService(cfg, nil, log)
	if err != nil {
		t.Fatalf("failed to create call service: %v", err)
	}

	issuer := authn.NewIssuer("test-secret-at-least-32-chars-long!", time.Hour, time.Hour, 0)
	token, _, err := issuer.Issue(authn.TokenUser, "usr_alice", "WEB", "dev_1", "", nil, 1)
	if err != nil {
		t.Fatalf("failed to issue token: %v", err)
	}

	return svc, issuer, token, log
}

func TestCallHandlers(t *testing.T) {
	svc, issuer, token, log := setupCallTest(t)

	t.Run("CallToken succeeds for authenticated user", func(t *testing.T) {
		reqBody := map[string]any{
			"roomName":        "room_daily_standup",
			"participantName": "Alice",
		}
		bodyBytes, _ := json.Marshal(reqBody)
		req := httptest.NewRequest("POST", "/api/v1/calls/token", bytes.NewReader(bodyBytes))
		req.Header.Set("Authorization", "Bearer "+token)

		w := httptest.NewRecorder()
		handler := authn.RequireUser(issuer, log)(CallToken(svc, log))
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
		}

		var res struct {
			ErrCode int               `json:"errCode"`
			Data    CallTokenResponse `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
			t.Fatalf("failed to parse response: %v", err)
		}

		if res.ErrCode != 0 || res.Data.Token == "" || res.Data.URL != "https://livekit.pulse.internal" {
			t.Fatalf("unexpected response data: %+v", res)
		}
	})

	t.Run("CallToken fails when unauthenticated", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/v1/calls/token", bytes.NewReader([]byte("{}")))
		w := httptest.NewRecorder()
		handler := CallToken(svc, log)
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 Unauthorized, got %d", w.Code)
		}
	})

	t.Run("CallSignal succeeds for valid signal", func(t *testing.T) {
		reqBody := map[string]any{
			"signalType": "Ring",
			"targetID":   "usr_bob",
			"roomName":   "room_daily_standup",
		}
		bodyBytes, _ := json.Marshal(reqBody)
		req := httptest.NewRequest("POST", "/api/v1/calls/signal", bytes.NewReader(bodyBytes))
		req.Header.Set("Authorization", "Bearer "+token)

		w := httptest.NewRecorder()
		handler := authn.RequireUser(issuer, log)(CallSignal(svc, log))
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
		}
	})
}
