//go:build contract
// +build contract

package contract

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// CONTRACT: send_receive_text
// Given:  two users alice + bob with valid tokens
// When:   alice sends "hello" to bob in convID
// Then:   alice receives FrameMessageAck with serverMsgID + seq >= 1
//         bob receives FrameMessageRecv with text == "hello"
//         bob can pull history containing the message
func TestSendReceiveText(t *testing.T) {
	apiBase := envOr("PULSE_TEST_API", "http://localhost:32011")
	wsBase := envOr("PULSE_TEST_WS", "ws://localhost:32001")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	adminTok := adminTokenForTest(t, apiBase)
	alice := uniqueID("ct-alice")
	bob := uniqueID("ct-bob")
	registerUser(t, apiBase, adminTok, alice, alice+"@pulse.test")
	registerUser(t, apiBase, adminTok, bob, bob+"@pulse.test")
	aliceTok := issueUserToken(t, apiBase, adminTok, alice, "WEB", alice+"-d1")
	bobTok := issueUserToken(t, apiBase, adminTok, bob, "WEB", bob+"-d1")
	convID := createConversation(t, apiBase, aliceTok, []string{alice, bob})

	// Both connect.
	aConn, _, err := dialWS(ctx, wsBase, aliceTok)
	require.NoError(t, err)
	defer aConn.Close()
	sendHello(t, aConn, aliceTok, "WEB", alice+"-d1")
	readWelcome(t, aConn)

	bConn, _, err := dialWS(ctx, wsBase, bobTok)
	require.NoError(t, err)
	defer bConn.Close()
	sendHello(t, bConn, bobTok, "WEB", bob+"-d1")
	readWelcome(t, bConn)

	// alice sends.
	sendText(t, aConn, convID, "hello", "c1")

	// alice receives ack.
	ack := readAck(t, aConn)
	require.Equal(t, convID, ack.ConvID)
	require.Equal(t, "c1", ack.ClientMsgID)
	require.NotEmpty(t, ack.ServerMsgID)
	require.GreaterOrEqual(t, ack.Seq, int64(1))

	// bob receives.
	recv := readRecv(t, bConn)
	require.Equal(t, convID, recv.ConvID)
	require.Equal(t, alice, recv.SenderID)
	t.Logf("recv content: %+v", recv.Content)
	require.Equal(t, "hello", recv.Content["text"])

	// bob can pull history.
	sendHistory(t, bConn, convID)
	hist := readHistory(t, bConn)
	require.Equal(t, convID, hist.ConvID)
	require.Len(t, hist.Messages, 1)
	t.Logf("history msg[0]: %+v", hist.Messages[0])
	// History returns Content as nested map; msgpack may decode to
	// map[string]any or map[interface{}]interface{} — handle both.
	txt := extractText(hist.Messages[0]["content"])
	require.Equal(t, "hello", txt)
}

// extractText safely pulls "text" out of a msgpack-decoded content map
// regardless of whether it decoded as map[string]any or map[any]any.
func extractText(v any) string {
	if v == nil {
		return ""
	}
	if m, ok := v.(map[string]any); ok {
		if s, ok := m["text"].(string); ok {
			return s
		}
	}
	if m, ok := v.(map[any]any); ok {
		if s, ok := m["text"].(string); ok {
			return s
		}
	}
	return ""
}
