package webhook

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pulse-workspace/pulse/pkg/shared/logger"
)

type mockWebhookDBTX struct {
	hooks []*Webhook
}

func (m *mockWebhookDBTX) Exec(ctx context.Context, sql string, args ...any) error {
	return nil
}

func (m *mockWebhookDBTX) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return nil, nil
}

func (m *mockWebhookDBTX) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return nil
}

func TestSignAndVerify(t *testing.T) {
	payload := []byte(`{"event":"MessageSent","text":"hello"}`)
	secret := "my_super_secret_webhook_key"

	sig, err := SignPayload(payload, secret)
	if err != nil {
		t.Fatalf("SignPayload failed: %v", err)
	}

	if len(sig) < 10 || sig[:7] != "sha256=" {
		t.Fatalf("expected signature starting with 'sha256=', got %s", sig)
	}

	if !VerifySignature(payload, secret, sig) {
		t.Fatal("VerifySignature failed for valid signature")
	}

	if VerifySignature(payload, "wrong_secret", sig) {
		t.Fatal("VerifySignature succeeded with wrong secret")
	}
}

func TestDispatcher_Deliver(t *testing.T) {
	log := logger.New(logger.Config{Level: "debug"})
	dispatcher := NewDispatcher(log)

	var receivedSig, receivedEvent, receivedDelivery string
	var receivedBody []byte

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedSig = r.Header.Get("X-Pulse-Signature")
		receivedEvent = r.Header.Get("X-Pulse-Event")
		receivedDelivery = r.Header.Get("X-Pulse-Delivery")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	hook := &Webhook{
		ID:       "wh_1",
		TenantID: "tenant_default",
		URL:      ts.URL,
		Secret:   "test_secret_key",
		IsActive: true,
	}

	payload := &EventPayload{
		DeliveryID: "del_123",
		Event:      "MessageSent",
		TenantID:   "tenant_default",
		Timestamp:  time.Now().UnixMilli(),
		Data:       map[string]any{"text": "Hello Webhook"},
	}

	err := dispatcher.Deliver(context.Background(), hook, payload)
	if err != nil {
		t.Fatalf("Deliver failed: %v", err)
	}

	if receivedEvent != "MessageSent" {
		t.Errorf("expected X-Pulse-Event 'MessageSent', got %s", receivedEvent)
	}
	if receivedDelivery != "del_123" {
		t.Errorf("expected X-Pulse-Delivery 'del_123', got %s", receivedDelivery)
	}
	if receivedSig == "" || receivedSig[:7] != "sha256=" {
		t.Errorf("expected valid sha256 signature header, got %s", receivedSig)
	}
	_ = receivedBody
}

func TestService_Register(t *testing.T) {
	log := logger.New(logger.Config{Level: "debug"})
	mockDB := &mockWebhookDBTX{}
	repo := NewRepository(mockDB)
	dispatcher := NewDispatcher(log)
	svc := NewService(repo, dispatcher, log)

	hook, secret, err := svc.Register(
		context.Background(),
		"tenant_default",
		"https://api.example.com/webhook",
		[]string{"MessageSent", "ConvCreated"},
	)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	if hook.URL != "https://api.example.com/webhook" || len(hook.Events) != 2 {
		t.Fatalf("unexpected hook returned: %+v", hook)
	}
	if len(secret) != 64 { // 32 bytes hex encoded = 64 characters
		t.Fatalf("expected 64-char hex secret, got length %d", len(secret))
	}
}

