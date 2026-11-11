//go:build integration
// +build integration

package integration

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/websocket"
	"github.com/vmihailenco/msgpack/v5"
)

// Frame types (must mirror internal/gateway/gateway.go).
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

// WelcomeMsg is the payload of FrameWelcome.
type WelcomeMsg struct {
	SessionID string `msgpack:"session_id"`
	UserID    string `msgpack:"user_id"`
	CellID    string `msgpack:"cell_id"`
}

// AckMsg is the payload of FrameMessageAck (also used for history responses).
type AckMsg struct {
	ConvID      string           `msgpack:"convID"`
	ServerMsgID string           `msgpack:"serverMsgID"`
	ClientMsgID string           `msgpack:"clientMsgID"`
	Seq         int64            `msgpack:"seq"`
	SendTime    int64            `msgpack:"sendTime"`
	Type        string           `msgpack:"type"`
	Messages    []map[string]any `msgpack:"messages"`
}

// RecvMsg is the payload of FrameMessageRecv.
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

// encodeFrame marshals payload (any) into a Pulse binary frame:
//   [type:2][length:4][body:length]
func encodeFrame(frameType uint16, payload any) []byte {
	var body []byte
	if payload != nil {
		b, _ := msgpack.Marshal(payload)
		body = b
	}
	out := make([]byte, 0, 2+4+len(body))
	out = append(out, byte(frameType>>8), byte(frameType))
	out = append(out, byte(len(body)>>24), byte(len(body)>>16), byte(len(body)>>8), byte(len(body)))
	out = append(out, body...)
	return out
}

// decodeFrame extracts the type + payload of a Pulse binary frame.
func decodeFrame(raw []byte) (uint16, []byte, error) {
	if len(raw) < 6 {
		return 0, nil, websocket.ErrBadHandshake
	}
	frameType := uint16(raw[0])<<8 | uint16(raw[1])
	ln := int(raw[2])<<24 | int(raw[3])<<16 | int(raw[4])<<8 | int(raw[5])
	if ln < 0 || len(raw) < 6+ln {
		return frameType, raw[6:], nil
	}
	return frameType, raw[6 : 6+ln], nil
}

// msgpackUnmarshal is a thin wrapper around msgpack.Unmarshal for test brevity.
func msgpackUnmarshal(data []byte, v any) error {
	return msgpack.Unmarshal(data, v)
}

// msgpackHelloPayload builds a FrameHello body (msgpack) for the given token.
func msgpackHelloPayload(token, platform, deviceID string) map[string]any {
	return map[string]any{
		"t": token,
		"p": platform,
		"d": deviceID,
	}
}

// dialWS opens a WS connection to /ws with the given bearer token.
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

// httpPost is a JSON-POST helper.
func httpPost(ctx context.Context, target string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return http.DefaultClient.Do(req)
}

// httpPostAuth is a JSON-POST helper that adds a bearer token.
func httpPostAuth(ctx context.Context, target string, body []byte, token string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	return http.DefaultClient.Do(req)
}