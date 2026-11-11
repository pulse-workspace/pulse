//go:build contract
// +build contract

// Package contract defines the SDK behavioural contract tests.
//
// The helpers in this file are used by every contract test. They wrap
// REST + WebSocket primitives so individual tests focus on assertions.
package contract

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"github.com/vmihailenco/msgpack/v5"
)

// --- Frame constants (must match internal/gateway/gateway.go) -------------

const (
	FrameHello       = 1
	FrameWelcome     = 2
	FrameSendMessage = 10
	FrameMessageAck  = 11
	FrameMessageRecv = 12
	FrameMarkRead    = 14
	FrameGetHistory  = 15
	FramePing        = 80
	FramePong        = 81
	FrameError       = 100
)

// WelcomeMsg is the FrameWelcome payload.
type WelcomeMsg struct {
	SessionID string `msgpack:"session_id"`
	UserID    string `msgpack:"user_id"`
	CellID    string `msgpack:"cell_id"`
}

// AckMsg is the FrameMessageAck payload (also used for history responses).
type AckMsg struct {
	ConvID      string           `msgpack:"convID"`
	ServerMsgID string           `msgpack:"serverMsgID"`
	ClientMsgID string           `msgpack:"clientMsgID"`
	Seq         int64            `msgpack:"seq"`
	SendTime    int64            `msgpack:"sendTime"`
	Type        string           `msgpack:"type"`
	Messages    []map[string]any `msgpack:"messages"`
}

// RecvMsg is the FrameMessageRecv payload.
type RecvMsg struct {
	ConvID      string         `msgpack:"convID"`
	ServerMsgID string         `msgpack:"serverMsgID"`
	ClientMsgID string         `msgpack:"clientMsgID"`
	SenderID    string         `msgpack:"senderID"`
	ContentType int            `msgpack:"contentType"`
	Content     map[string]any `msgpack:"content"`
	Seq         int64          `msgpack:"seq"`
	SendTime    int64          `msgpack:"sendTime"`
}

// --- env / utilities -----------------------------------------------------

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func uniqueID(prefix string) string {
	// Keep short: conv_id column is VARCHAR(64). Two of these + "si_" prefix
	// plus underscore separators must fit in 64 chars.
	return fmt.Sprintf("%s%d", prefix, time.Now().UnixNano()%100000)
}

// --- REST helpers --------------------------------------------------------

func adminTokenForTest(t *testing.T, apiBase string) string {
	t.Helper()
	// Try init first (only succeeds on first call).
	body := `{"secret":"dev-mode","adminUserID":"admin","adminPassword":"Pulse@Admin1"}`
	if res, err := http.Post(apiBase+"/api/v1/admin/init", "application/json", strings.NewReader(body)); err == nil {
		defer res.Body.Close()
		if res.StatusCode == 200 {
			var out struct {
				Data struct {
					AdminToken string `json:"adminToken"`
				} `json:"data"`
			}
			if json.NewDecoder(res.Body).Decode(&out) == nil && out.Data.AdminToken != "" {
				return out.Data.AdminToken
			}
		}
	}
	// Login.
	body = `{"userID":"admin","password":"Pulse@Admin1"}`
	res, err := http.Post(apiBase+"/api/v1/admin/login", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, 200, res.StatusCode, "admin login status=%d", res.StatusCode)
	var out struct {
		Data struct {
			AdminToken string `json:"adminToken"`
		} `json:"data"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
	require.NotEmpty(t, out.Data.AdminToken)
	return out.Data.AdminToken
}

func registerUser(t *testing.T, apiBase, adminTok, userID, email string) {
	t.Helper()
	body := `{"userID":"` + userID + `","nickname":"` + userID + `","email":"` + email + `"}`
	req, _ := http.NewRequest("POST", apiBase+"/api/v1/user/register", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+adminTok)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.True(t, res.StatusCode == 200 || res.StatusCode == 409, "register %s status=%d", userID, res.StatusCode)
}

func issueUserToken(t *testing.T, apiBase, adminTok, userID, platform, device string) string {
	t.Helper()
	body := `{"userID":"` + userID + `","platform":"` + platform + `","deviceID":"` + device + `"}`
	req, _ := http.NewRequest("POST", apiBase+"/api/v1/auth/userToken", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+adminTok)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, 200, res.StatusCode)
	var out struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
	require.NotEmpty(t, out.Data.Token)
	return out.Data.Token
}

func createConversation(t *testing.T, apiBase, userTok string, participants []string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"participants": participants,
		"type":         1,
	})
	req, _ := http.NewRequest("POST", apiBase+"/api/v1/conversation/create", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+userTok)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, 200, res.StatusCode)
	var out struct {
		Data struct {
			ConvID string `json:"ConvID"`
		} `json:"data"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
	require.NotEmpty(t, out.Data.ConvID)
	return out.Data.ConvID
}

func createGroup(t *testing.T, apiBase, userTok, name string, members, admins []string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"name":          name,
		"groupType":     "Normal",
		"initialMembers": members,
		"initialAdmins":  admins,
	})
	req, _ := http.NewRequest("POST", apiBase+"/api/v1/group/create", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+userTok)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, 200, res.StatusCode)
	var out struct {
		Data struct {
			GroupID string `json:"group_id"`
		} `json:"data"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
	require.NotEmpty(t, out.Data.GroupID)
	return out.Data.GroupID
}

// --- WS helpers ----------------------------------------------------------

func dialWS(ctx context.Context, wsBase, token string) (*websocket.Conn, *http.Response, error) {
	u, _ := url.Parse(wsBase + "/ws")
	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer "+token)
	dialer := websocket.Dialer{
		Subprotocols:     []string{"pulse.protocol.v1.msgpack"},
		HandshakeTimeout: 10 * time.Second,
	}
	return dialer.DialContext(ctx, u.String(), hdr)
}

func sendHello(t *testing.T, conn *websocket.Conn, token, platform, device string) {
	t.Helper()
	payload := map[string]any{"t": token, "p": platform, "d": device}
	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, encodeFrame(FrameHello, payload)))
}

func sendText(t *testing.T, conn *websocket.Conn, convID, text, clientMsgID string) {
	t.Helper()
	payload := map[string]any{
		"c": convID,
		"t": 101,
		"x": map[string]any{"text": text},
		"i": clientMsgID,
	}
	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, encodeFrame(FrameSendMessage, payload)))
}

func sendMarkRead(t *testing.T, conn *websocket.Conn, convID, msgID string) {
	t.Helper()
	payload := map[string]any{"c": convID, "m": msgID}
	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, encodeFrame(FrameMarkRead, payload)))
}

func sendHistory(t *testing.T, conn *websocket.Conn, convID string) {
	t.Helper()
	payload := map[string]any{"c": convID, "l": 20}
	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, encodeFrame(FrameGetHistory, payload)))
}

func readWelcome(t *testing.T, conn *websocket.Conn) WelcomeMsg {
	t.Helper()
	return readFrameAs[WelcomeMsg](t, conn, FrameWelcome, 5*time.Second)
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
		_ = conn.SetReadDeadline(deadline)
		_, raw, err := conn.ReadMessage()
		require.NoError(t, err, "read frame type=%d", want)
		ft, payload, err := decodeFrame(raw)
		require.NoError(t, err)
		if ft != want {
			t.Logf("skipping frame type=%d (waiting for %d)", ft, want)
			continue
		}
		var out T
		if len(payload) == 0 {
			// Frame has no body (e.g. legacy Pong). Caller is responsible
			// for any defaulting in T's zero-value — return now.
			return out
		}
		require.NoError(t, msgpack.Unmarshal(payload, &out))
		return out
	}
}

func readFrame(t *testing.T, conn *websocket.Conn, want uint16, timeout time.Duration) {
	t.Helper()
	_ = readFrameAs[map[string]any](t, conn, want, timeout)
}

// --- Frame codec ----------------------------------------------------------

func encodeFrame(frameType uint16, payload any) []byte {
	var body []byte
	if payload != nil {
		b, _ := msgpack.Marshal(payload)
		body = b
	}
	buf := make([]byte, 6+len(body))
	binary.BigEndian.PutUint16(buf[0:2], frameType)
	binary.BigEndian.PutUint32(buf[2:6], uint32(len(body)))
	copy(buf[6:], body)
	return buf
}

func decodeFrame(raw []byte) (uint16, []byte, error) {
	if len(raw) < 6 {
		return 0, nil, io.ErrUnexpectedEOF
	}
	frameType := binary.BigEndian.Uint16(raw[0:2])
	length := binary.BigEndian.Uint32(raw[2:6])
	if len(raw) < int(6+length) {
		return frameType, raw[6:], nil
	}
	return frameType, raw[6 : 6+length], nil
}
