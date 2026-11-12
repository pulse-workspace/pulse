package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pulse-workspace/pulse/internal/cell/webhook"
	"github.com/pulse-workspace/pulse/pkg/shared/authn"
	"github.com/pulse-workspace/pulse/pkg/shared/logger"
)

type mockWebhookHandlerDBTX struct{}

func (m *mockWebhookHandlerDBTX) Exec(ctx context.Context, sql string, args ...any) error {
	return nil
}

func (m *mockWebhookHandlerDBTX) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return nil, nil
}

func (m *mockWebhookHandlerDBTX) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return nil
}

func setupWebhookTest(t *testing.T) (*webhook.Service, *authn.Issuer, string, logger.Logger) {
	log := logger.New(logger.Config{Level: "debug"})
	repo := webhook.NewRepository(&mockWebhookHandlerDBTX{})
	dispatcher := webhook.NewDispatcher(log)
	svc := webhook.NewService(repo, dispatcher, log)

	issuer := authn.NewIssuer("test-secret-at-least-32-chars-long!", time.Hour, time.Hour, 0)
	token, _, err := issuer.Issue(authn.TokenUser, "usr_alice", "WEB", "dev_1", "", nil, 1)
	if err != nil {
		t.Fatalf("failed to issue token: %v", err)
	}

	return svc, issuer, token, log
}

func TestWebhookHandlers(t *testing.T) {
	svc, issuer, token, log := setupWebhookTest(t)

	t.Run("WebhookCreate succeeds for authenticated caller", func(t *testing.T) {
		reqBody := map[string]any{
			"url":    "https://example.com/pulse-hook",
			"events": []string{"MessageSent", "ConvCreated"},
		}
		bodyBytes, _ := json.Marshal(reqBody)
		req := httptest.NewRequest("POST", "/api/v1/webhooks", bytes.NewReader(bodyBytes))
		req.Header.Set("Authorization", "Bearer "+token)

		w := httptest.NewRecorder()
		handler := authn.RequireUser(issuer, log)(WebhookCreate(svc, log))
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("WebhookList succeeds for authenticated caller", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/webhooks", nil)
		req.Header.Set("Authorization", "Bearer "+token)

		w := httptest.NewRecorder()
		handler := authn.RequireUser(issuer, log)(WebhookList(svc, log))
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
		}
	})
}
