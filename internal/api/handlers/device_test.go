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
	"github.com/pulse-workspace/pulse/internal/cell"
	"github.com/pulse-workspace/pulse/internal/cell/push"
	"github.com/pulse-workspace/pulse/pkg/shared/authn"
	"github.com/pulse-workspace/pulse/pkg/shared/config"
	"github.com/pulse-workspace/pulse/pkg/shared/logger"
)

type mockPushDBTX struct {
	tokens []*push.DeviceToken
}

func (m *mockPushDBTX) Exec(ctx context.Context, sql string, args ...any) error {
	return nil
}

func (m *mockPushDBTX) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return nil, nil
}

func (m *mockPushDBTX) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return nil
}

func setupDeviceTest(t *testing.T) (*push.Service, *authn.Issuer, string, logger.Logger) {
	cfg := &config.Config{}
	cfg.Cell.ID = "cell-test"
	log := logger.New(logger.Config{Level: "debug"})
	c := cell.New(cfg, log, nil, nil, nil)
	db := &mockPushDBTX{}
	repo := push.NewRepository(db)
	pushSvc := push.NewService(c, repo, nil)

	issuer := authn.NewIssuer("test-secret-at-least-32-chars-long!", time.Hour, time.Hour, 0)
	token, _, err := issuer.Issue(authn.TokenUser, "usr_alice", "WEB", "dev_1", "", nil, 1)
	if err != nil {
		t.Fatalf("failed to issue token: %v", err)
	}

	return pushSvc, issuer, token, log
}

func TestDeviceRegister_Unauthorized(t *testing.T) {
	pushSvc, _, _, log := setupDeviceTest(t)

	reqBody := `{"platform":"fcm","token":"tok_123","app_bundle_id":"com.pulse.im"}`
	req := httptest.NewRequest("POST", "/api/v1/devices/register", bytes.NewBufferString(reqBody))
	w := httptest.NewRecorder()

	handler := DeviceRegister(pushSvc, log)
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized, got %d", w.Code)
	}
}

func TestDeviceRegister_Success(t *testing.T) {
	pushSvc, issuer, token, log := setupDeviceTest(t)

	reqBody := `{"platform":"fcm","token":"tok_123","app_bundle_id":"com.pulse.im"}`
	req := httptest.NewRequest("POST", "/api/v1/devices/register", bytes.NewBufferString(reqBody))
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	handler := authn.RequireUser(issuer, log)(DeviceRegister(pushSvc, log))
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d, body: %s", w.Code, w.Body.String())
	}

	var res JSONResponse
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if res.ErrCode != 0 {
		t.Fatalf("expected errCode 0, got %d", res.ErrCode)
	}
}

func TestDeviceUnregister_Success(t *testing.T) {
	pushSvc, issuer, token, log := setupDeviceTest(t)

	reqBody := `{"token":"tok_123"}`
	req := httptest.NewRequest("POST", "/api/v1/devices/unregister", bytes.NewBufferString(reqBody))
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	handler := authn.RequireUser(issuer, log)(DeviceUnregister(pushSvc, log))
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}
}

func TestDeviceList_Success(t *testing.T) {
	pushSvc, issuer, token, log := setupDeviceTest(t)

	req := httptest.NewRequest("GET", "/api/v1/devices", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	handler := authn.RequireUser(issuer, log)(DeviceList(pushSvc, log))
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}
}

