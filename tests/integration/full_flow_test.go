//go:build integration

// Package integration contains end-to-end smoke tests for Pulse.
//
// Run with:
//   PULSE_TEST_POSTGRES=postgres://pulse:pulse@localhost:32432/pulse_test?sslmode=disable \
//   PULSE_TEST_NATS=nats://localhost:32222 \
//   go test -tags=integration ./tests/integration/...
package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/vmihailenco/msgpack/v5"
	"github.com/stretchr/testify/require"

	"github.com/pulse-im/pulse/pkg/shared/config"
)

const (
	adminSecret = "dev-mode"
	testAPIURL  = "http://localhost:32011"
	testWSURL   = "ws://localhost:32001"
)

// TestPulseFullFlow runs the canonical 1-1 chat smoke test:
//
//   1. /admin/init        -> admin token
//   2. /user/register     -> register u1 (alice)
//   3. /user/register     -> register u2 (bob)
//   4. /auth/userToken    -> mint user token for alice
//   5. WebSocket connect  -> alice connects
//   6. WebSocket send     -> alice sends a hello to bob (will fail in Week 1-2
//                             because message sending lands Week 5-6; we verify
//                             the connection is alive + ping/pong works)
func TestPulseFullFlow(t *testing.T) {
	apiBase := envOr("PULSE_TEST_API", testAPIURL)
	wsBase := envOr("PULSE_TEST_WS", testWSURL)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 1. Admin init
	adminToken := adminInit(t, ctx, apiBase)
	t.Logf("admin token: %s...", adminToken[:32])

	// 2. Register u1
	registerUser(t, ctx, apiBase, adminToken, "u1", "Alice", "alice@example.com", "")
	t.Log("registered u1")

	// 3. Register u2
	registerUser(t, ctx, apiBase, adminToken, "u2", "Bob", "bob@example.com", "")
	t.Log("registered u2")

	// 4. Mint user token for alice
	userToken := issueUserToken(t, ctx, apiBase, adminToken, "u1", "WEB", "device-1")
	t.Logf("user token: %s...", userToken[:32])

	// 5. WebSocket connect
	conn, _, err := dialWS(ctx, wsBase, userToken)
	require.NoError(t, err)
	defer conn.Close()

	// Send Hello + read Welcome.
	helloPayload := msgpackHelloPayload(userToken, "WEB", "device-1")
	helloFrame := encodeFrame(FrameHello, helloPayload)
	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, helloFrame))
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, _, err = conn.ReadMessage() // Welcome
	require.NoError(t, err)

	t.Log("WebSocket handshake OK (Welcome received)")

	// 7. Ping/pong roundtrip
	pingFrame := encodeFrame(FramePing, nil)
	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, pingFrame))

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, pongMsg, err := conn.ReadMessage()
	require.NoError(t, err, "expected pong frame within 5s")

	ft, _, err := decodeFrame(pongMsg)
	require.NoError(t, err)
	require.Equal(t, uint16(FramePong), ft, "expected FramePong (81), got %d", ft)

	t.Log("WebSocket ping/pong OK")

	// For Week 1-2 we stop here. Week 5-6 adds SendMessage + MessageReceived.
}

// --- helpers --------------------------------------------------------------

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func adminInit(t *testing.T, ctx context.Context, apiBase string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"secret":        adminSecret,
		"adminUserID":   "admin",
		"adminPassword": "Pulse@Admin1",
	})
	res, err := httpPost(ctx, apiBase+"/api/v1/admin/init", body)
	require.NoError(t, err, "admin init should succeed")
	defer res.Body.Close()

	if res.StatusCode == http.StatusConflict {
		// Already bootstrapped — log in instead.
		body, _ := json.Marshal(map[string]any{
			"userID":   "admin",
			"password": "Pulse@Admin1",
		})
		res, err = httpPost(ctx, apiBase+"/api/v1/admin/login", body)
		require.NoError(t, err)
		defer res.Body.Close()
	}

	require.True(t, res.StatusCode < 300, "admin init/login status=%d", res.StatusCode)

	var resp struct {
		ErrCode int `json:"errCode"`
		Data    struct {
			AdminToken string `json:"adminToken"`
		} `json:"data"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&resp))
	require.Equal(t, 0, resp.ErrCode)
	require.NotEmpty(t, resp.Data.AdminToken, "adminToken must be non-empty")
	return resp.Data.AdminToken
}

func registerUser(t *testing.T, ctx context.Context, apiBase, adminToken, userID, nickname, email, phone string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"userID":   userID,
		"nickname": nickname,
		"email":    email,
		"phone":    phone,
	})
	res, err := httpPostAuth(ctx, apiBase+"/api/v1/user/register", body, adminToken)
	require.NoError(t, err)
	defer res.Body.Close()

	// Conflict (2002) is acceptable if a previous test run left the user.
	if res.StatusCode == http.StatusConflict {
		return
	}
	require.True(t, res.StatusCode < 300, "register status=%d", res.StatusCode)
}

func issueUserToken(t *testing.T, ctx context.Context, apiBase, adminToken, userID, platform, deviceID string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"userID":   userID,
		"platform": platform,
		"deviceID": deviceID,
	})
	res, err := httpPostAuth(ctx, apiBase+"/api/v1/auth/userToken", body, adminToken)
	require.NoError(t, err)
	defer res.Body.Close()

	require.True(t, res.StatusCode < 300, "issue user token status=%d", res.StatusCode)

	var resp struct {
		ErrCode int `json:"errCode"`
		Data    struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&resp))
	require.Equal(t, 0, resp.ErrCode)
	require.NotEmpty(t, resp.Data.Token, "user token must be non-empty")
	return resp.Data.Token
}

// (dialWS / encodeFrame / decodeFrame / HTTP helpers live in helpers.go)

// Reference config package so go.mod picks it up.
var _ = config.Defaults

// Reference msgpack so go.mod picks it up.
var _ = msgpack.Marshal