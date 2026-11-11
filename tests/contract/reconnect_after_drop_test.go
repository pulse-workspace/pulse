//go:build contract
// +build contract

package contract

import (
	"context"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// CONTRACT: reconnect_after_drop
// Given:  alice is connected via WS
// When:   the connection is forcefully dropped (TCP RST or close)
// Then:   reconnection within 5s succeeds with another Welcome frame
//         auto-reconnect behaviour is tested at the SDK level
//         (this test simulates the drop + manual reconnect)
func TestReconnectAfterDrop(t *testing.T) {
	apiBase := envOr("PULSE_TEST_API", "http://localhost:32011")
	wsBase := envOr("PULSE_TEST_WS", "ws://localhost:32001")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	adminTok := adminTokenForTest(t, apiBase)
	alice := uniqueID("rc-alice")
	registerUser(t, apiBase, adminTok, alice, alice+"@pulse.test")
	userTok := issueUserToken(t, apiBase, adminTok, alice, "WEB", alice+"-d")

	// First connect.
	c1, _, err := dialWS(ctx, wsBase, userTok)
	require.NoError(t, err)
	sendHello(t, c1, userTok, "WEB", alice+"-d")
	w1 := readWelcome(t, c1)
	require.NotEmpty(t, w1.SessionID)
	t.Logf("initial session: %s", w1.SessionID)

	// Simulate drop: close without Close frame (TCP RST equivalent).
	require.NoError(t, c1.Close())
	time.Sleep(100 * time.Millisecond)

	// Reconnect within 5s.
	reconnectDeadline := time.Now().Add(5 * time.Second)
	var w2 WelcomeMsg
	var success bool
	for time.Now().Before(reconnectDeadline) {
		c2, _, err := dialWS(ctx, wsBase, userTok)
		if err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		sendHello(t, c2, userTok, "WEB", alice+"-d")
		w2 = readWelcome(t, c2)
		if w2.SessionID != "" {
			success = true
			_ = c2.Close()
			break
		}
		_ = c2.Close()
	}

	require.True(t, success, "reconnect within 5s should succeed")
	require.NotEmpty(t, w2.SessionID)
	t.Logf("reconnect session: %s (auto-reconnect is SDK responsibility)", w2.SessionID)

	// Verify WS ping/pong still works post-reconnect.
	c3, _, err := dialWS(ctx, wsBase, userTok)
	require.NoError(t, err)
	defer c3.Close()
	sendHello(t, c3, userTok, "WEB", alice+"-d")
	readWelcome(t, c3)
	require.NoError(t, c3.WriteMessage(websocket.BinaryMessage, encodeFrame(FramePing, nil)))
	readFrame(t, c3, FramePong, 5*time.Second)
}
