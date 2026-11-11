//go:build integration
// +build integration

package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// TestMessagingFlow covers Week 5-6:
//   1. admin init + login
//   2. register alice + bob
//   3. mint user tokens for both
//   4. create 1-on-1 conversation via REST
//   5. both connect via WS
//   6. alice sends "hello"
//   7. bob receives FrameMessageRecv
//   8. alice receives FrameMessageAck
//   9. alice asks for history; her message is there
//  10. bob marks read; unread → 0
func TestMessagingFlow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	apiBase := envOr("PULSE_TEST_API", "http://localhost:32011")
	wsBase := envOr("PULSE_TEST_WS", "ws://localhost:32001")

	// 1. admin ensure
	adminTok := adminEnsure(t, apiBase)

	// 2. register alice + bob (409 ok if already)
	register(t, apiBase, adminTok, "alice", "alice-at-test-dot-pulse-dot-dev")
	register(t, apiBase, adminTok, "bob", "bob-at-test-dot-pulse-dot-dev")

	// 3. mint user tokens
	aliceTok := issueUserTokenMsg(t, apiBase, adminTok, "alice", "WEB", "alice-1")
	bobTok := issueUserTokenMsg(t, apiBase, adminTok, "bob", "WEB", "bob-1")

	// 4. create 1-on-1 conversation
	convID := createConv(t, apiBase, aliceTok, []string{"alice", "bob"})
	require.True(t, strings.HasPrefix(convID, "si_"), "expected si_ prefix, got %q", convID)

	// 5. both connect via WS
	aliceConn, _, err := dialWS(ctx, wsBase, aliceTok)
	require.NoError(t, err)
	defer aliceConn.Close()
	bobConn, _, err := dialWS(ctx, wsBase, bobTok)
	require.NoError(t, err)
	defer bobConn.Close()

	// Send Hello (auth) + read Welcome.
	sendHello(t, aliceConn, aliceTok)
	sendHello(t, bobConn, bobTok)

	aliceHello := readWelcome(t, aliceConn)
	bobHello := readWelcome(t, bobConn)
	require.Equal(t, "alice", aliceHello.UserID)
	require.Equal(t, "bob", bobHello.UserID)
	t.Logf("alice session=%s, bob session=%s", aliceHello.SessionID, bobHello.SessionID)

	// 6. alice sends "hello bob"
	sendText(t, aliceConn, convID, "hello bob", "c1")

	// 7. alice receives ack
	ack := readAck(t, aliceConn)
	require.Equal(t, convID, ack.ConvID)
	require.Equal(t, "c1", ack.ClientMsgID)
	require.NotEmpty(t, ack.ServerMsgID)
	t.Logf("alice ack serverMsgID=%s seq=%d", ack.ServerMsgID, ack.Seq)

	// 8. bob receives the message
	recv := readRecv(t, bobConn)
	require.Equal(t, convID, recv.ConvID)
	require.Equal(t, "alice", recv.SenderID)
	require.Equal(t, "hello bob", recv.Content["text"])
	require.Equal(t, ack.ServerMsgID, recv.ServerMsgID)
	t.Logf("bob received: %v", recv.Content["text"])

	// 9. alice gets history
	sendHistory(t, aliceConn, convID)
	hist := readHistory(t, aliceConn)
	require.Equal(t, convID, hist.ConvID)
	require.Len(t, hist.Messages, 1)
	t.Logf("history msg keys: %v", hist.Messages[0])
	content, _ := hist.Messages[0]["content"].(map[string]any)
	if content != nil {
		require.Equal(t, "hello bob", content["text"])
	}
	t.Logf("history returned %d messages", len(hist.Messages))

	// 10. bob marks read; unread → 0
	sendMarkRead(t, bobConn, convID, ack.ServerMsgID)
	time.Sleep(300 * time.Millisecond)
	unread := getUnread(t, apiBase, bobTok, convID)
	require.Equal(t, 0, unread, "expected 0 unread after mark-read, got %d", unread)
	t.Logf("bob unread after mark-read: %d", unread)

	t.Log("messaging flow OK")
}

// --- messaging-test-only helpers ------------------------------------------

func register(t *testing.T, apiBase, adminTok, userID, email string) {
	t.Helper()
	body := `{"userID":"` + userID + `","nickname":"` + strings.Title(userID) + `","email":"` + email + `"}`
	res, err := httpPostAuth(t.Context(), apiBase+"/api/v1/user/register", []byte(body), adminTok)
	require.NoError(t, err)
	defer res.Body.Close()
	require.True(t, res.StatusCode == 200 || res.StatusCode == 409,
		"register status=%d", res.StatusCode)
}

func issueUserTokenMsg(t *testing.T, apiBase, adminTok, userID, platform, device string) string {
	t.Helper()
	body := `{"userID":"` + userID + `","platform":"` + platform + `","deviceID":"` + device + `"}`
	res, err := httpPostAuth(t.Context(), apiBase+"/api/v1/auth/userToken", []byte(body), adminTok)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, 200, res.StatusCode, "issueUserToken status=%d", res.StatusCode)
	var out struct {
		ErrCode int `json:"errCode"`
		Data    struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
	require.Equal(t, 0, out.ErrCode)
	require.NotEmpty(t, out.Data.Token)
	return out.Data.Token
}

func createConv(t *testing.T, apiBase, userTok string, participants []string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"participants": participants,
		"type":         1, // single
	})
	req, _ := http.NewRequest("POST", apiBase+"/api/v1/conversation/create", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+userTok)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, 200, res.StatusCode, "createConv status=%d", res.StatusCode)
	var out struct {
		Data struct {
			ConvID string `json:"ConvID"`
		}
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
	require.NotEmpty(t, out.Data.ConvID)
	return out.Data.ConvID
}

func adminEnsure(t *testing.T, apiBase string) string {
	t.Helper()
	body := `{"secret":"dev-mode","adminUserID":"admin","adminPassword":"Pulse@Admin1"}`
	res, err := httpPost(t.Context(), apiBase+"/api/v1/admin/init", []byte(body))
	if err == nil && res.StatusCode == 200 {
		defer res.Body.Close()
		var out struct {
			ErrCode int `json:"errCode"`
			Data    struct {
				AdminToken string `json:"adminToken"`
			} `json:"data"`
		}
		if json.NewDecoder(res.Body).Decode(&out) == nil && out.ErrCode == 0 && out.Data.AdminToken != "" {
			return out.Data.AdminToken
		}
	}
	if res != nil {
		res.Body.Close()
	}

	body = `{"userID":"admin","password":"Pulse@Admin1"}`
	res, err = httpPost(t.Context(), apiBase+"/api/v1/admin/login", []byte(body))
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, 200, res.StatusCode, "admin login status=%d", res.StatusCode)
	var out struct {
		ErrCode int `json:"errCode"`
		Data    struct {
			AdminToken string `json:"adminToken"`
		} `json:"data"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
	require.Equal(t, 0, out.ErrCode)
	require.NotEmpty(t, out.Data.AdminToken)
	return out.Data.AdminToken
}

func readWelcome(t *testing.T, conn *websocket.Conn) WelcomeMsg {
	t.Helper()
	return readFrameAs[WelcomeMsg](t, conn, FrameWelcome, 5*time.Second)
}

// sendHello sends a FrameHello with the given user token so the gateway
// authenticates and returns a Welcome frame.
func sendHello(t *testing.T, conn *websocket.Conn, userToken string) {
	t.Helper()
	payload := map[string]any{"t": userToken}
	frame := encodeFrame(FrameHello, payload)
	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, frame))
}

func sendText(t *testing.T, conn *websocket.Conn, convID, text, clientMsgID string) {
	t.Helper()
	payload := map[string]any{
		"c": convID,
		"t": 101, // ContentText
		"x": map[string]any{"text": text},
		"i": clientMsgID,
	}
	frame := encodeFrame(FrameSendMessage, payload)
	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, frame))
}

func sendMarkRead(t *testing.T, conn *websocket.Conn, convID, msgID string) {
	t.Helper()
	payload := map[string]any{"c": convID, "m": msgID}
	frame := encodeFrame(FrameMarkRead, payload)
	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, frame))
}

func sendHistory(t *testing.T, conn *websocket.Conn, convID string) {
	t.Helper()
	payload := map[string]any{"c": convID, "l": 20}
	frame := encodeFrame(FrameGetHistory, payload)
	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, frame))
}

func readAck(t *testing.T, conn *websocket.Conn) AckMsg {
	t.Helper()
	return readFrameAs[AckMsg](t, conn, FrameMessageAck, 5*time.Second)
}

func readRecv(t *testing.T, conn *websocket.Conn) RecvMsg {
	t.Helper()
	return readFrameAs[RecvMsg](t, conn, FrameMessageRecv, 5*time.Second)
}

func readHistory(t *testing.T, conn *websocket.Conn) AckMsg {
	t.Helper()
	return readFrameAs[AckMsg](t, conn, FrameMessageAck, 5*time.Second)
}

func readFrameAs[T any](t *testing.T, conn *websocket.Conn, want uint16, timeout time.Duration) T {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		conn.SetReadDeadline(deadline)
		_, raw, err := conn.ReadMessage()
		require.NoError(t, err, "read frame type=%d", want)
		ft, payload, err := decodeFrame(raw)
		require.NoError(t, err)
		if ft != want {
			t.Logf("skipping frame type=%d (waiting for %d)", ft, want)
			continue
		}
		var out T
		require.NoError(t, msgpackUnmarshal(payload, &out))
		return out
	}
}

func getUnread(t *testing.T, apiBase, userTok, convID string) int {
	t.Helper()
	req, _ := http.NewRequest("GET", apiBase+"/api/v1/conversation/list?limit=10", nil)
	req.Header.Set("Authorization", "Bearer "+userTok)
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	var out struct {
		Data []struct {
			ConvID string `json:"ConvID"`
			Unread int    `json:"Unread"`
		}
	}
	_ = json.NewDecoder(res.Body).Decode(&out)
	for _, c := range out.Data {
		if c.ConvID == convID {
			return c.Unread
		}
	}
	return -1
}