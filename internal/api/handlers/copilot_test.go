package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pulse-workspace/pulse/internal/cell/copilot"
	"github.com/pulse-workspace/pulse/pkg/shared/authn"
	"github.com/pulse-workspace/pulse/pkg/shared/logger"
)

func setupCopilotTest(t *testing.T) (*copilot.Service, *authn.Issuer, string, logger.Logger) {
	log := logger.New(logger.Config{Level: "debug"})

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		res := map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"content": "1. Option A\n2. Option B\n3. Option C",
					},
				},
			},
		}
		json.NewEncoder(w).Encode(res)
	}))

	cfg := &copilot.Config{
		Provider: "openai",
		APIKey:   "sk_mock",
		Model:    "gpt-4o",
		BaseURL:  mockServer.URL,
	}

	svc, err := copilot.NewService(cfg, log)
	if err != nil {
		t.Fatalf("failed to create copilot service: %v", err)
	}

	issuer := authn.NewIssuer("test-secret-at-least-32-chars-long!", time.Hour, time.Hour, 0)
	token, _, err := issuer.Issue(authn.TokenUser, "usr_alice", "WEB", "dev_1", "", nil, 1)
	if err != nil {
		t.Fatalf("failed to issue token: %v", err)
	}

	return svc, issuer, token, log
}

func TestCopilotHandlers(t *testing.T) {
	svc, issuer, token, log := setupCopilotTest(t)

	t.Run("CopilotSummary succeeds for authenticated caller", func(t *testing.T) {
		reqBody := map[string]any{
			"messages": []map[string]any{
				{"senderName": "Alice", "text": "Are we launching today?", "timestamp": 1000},
			},
		}
		bodyBytes, _ := json.Marshal(reqBody)
		req := httptest.NewRequest("POST", "/api/v1/copilot/summary", bytes.NewReader(bodyBytes))
		req.Header.Set("Authorization", "Bearer "+token)

		w := httptest.NewRecorder()
		handler := authn.RequireUser(issuer, log)(CopilotSummary(svc, log))
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("CopilotSmartReplies succeeds for authenticated caller", func(t *testing.T) {
		reqBody := map[string]any{
			"messages": []map[string]any{
				{"senderName": "Alice", "text": "Are we launching today?", "timestamp": 1000},
			},
		}
		bodyBytes, _ := json.Marshal(reqBody)
		req := httptest.NewRequest("POST", "/api/v1/copilot/smart-replies", bytes.NewReader(bodyBytes))
		req.Header.Set("Authorization", "Bearer "+token)

		w := httptest.NewRecorder()
		handler := authn.RequireUser(issuer, log)(CopilotSmartReplies(svc, log))
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
		}
	})
}
