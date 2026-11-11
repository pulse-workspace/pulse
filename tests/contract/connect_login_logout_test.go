//go:build contract
// +build contract

// Package contract defines the SDK behavioural contract tests.
//
// CONTRACT: connect_login_logout
// Given:  a fresh user with admin-minted token
// When:   client connects via WS with that token, then disconnects
// Then:   - Hello frame is accepted; Welcome frame received with user_id + cell_id
//         - Disconnect is clean: server registers sessionID
//         - Reconnect with same deviceID succeeds
package contract

import (
	"context"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// TestConnectLoginLogout validates the Hello/Welcome/disconnect flow.
func TestConnectLoginLogout(t *testing.T) {
	apiBase := envOr("PULSE_TEST_API", "http://localhost:32011")
	wsBase := envOr("PULSE_TEST_WS", "ws://localhost:32001")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	adminTok := adminTokenForTest(t, apiBase)
	uid := uniqueID("ct-connect")
	registerUser(t, apiBase, adminTok, uid, uid+"@pulse.test")
	userTok := issueUserToken(t, apiBase, adminTok, uid, "WEB", "device-1")

	conn, _, err := dialWS(ctx, wsBase, userTok)
	require.NoError(t, err)
	defer conn.Close()

	sendHello(t, conn, userTok, "WEB", "device-1")
	w := readWelcome(t, conn)
	require.Equal(t, uid, w.UserID)
	require.NotEmpty(t, w.SessionID)
	require.NotEmpty(t, w.CellID)
	t.Logf("connect: session=%s cell=%s", w.SessionID, w.CellID)

	// Ping/Pong.
	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, encodeFrame(FramePing, nil)))
	readFrame(t, conn, FramePong, 5*time.Second)

	// Clean disconnect.
	require.NoError(t, conn.WriteMessage(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, "bye")))

	// Reconnect.
	conn2, _, err := dialWS(ctx, wsBase, userTok)
	require.NoError(t, err)
	defer conn2.Close()
	sendHello(t, conn2, userTok, "WEB", "device-1")
	w2 := readWelcome(t, conn2)
	require.NotEmpty(t, w2.SessionID)
	require.Equal(t, uid, w2.UserID)
	t.Log("reconnect OK; lifecycle stable")
}
