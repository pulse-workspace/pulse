//go:build contract
// +build contract

package contract

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// CONTRACT: mark_read_unread
// Given:  alice sends 1 message to bob in convID
// When:   bob marks convID as read up to msgID
// Then:   bob's GET /conversation/list?limit=N shows unread=0 for convID
func TestMarkReadUnread(t *testing.T) {
	apiBase := envOr("PULSE_TEST_API", "http://localhost:32011")
	wsBase := envOr("PULSE_TEST_WS", "ws://localhost:32001")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	adminTok := adminTokenForTest(t, apiBase)
	alice := uniqueID("r-alice")
	bob := uniqueID("r-bob")
	registerUser(t, apiBase, adminTok, alice, alice+"@pulse.test")
	registerUser(t, apiBase, adminTok, bob, bob+"@pulse.test")
	aliceTok := issueUserToken(t, apiBase, adminTok, alice, "WEB", alice+"-d")
	bobTok := issueUserToken(t, apiBase, adminTok, bob, "WEB", bob+"-d")
	convID := createConversation(t, apiBase, aliceTok, []string{alice, bob})

	aConn, _, err := dialWS(ctx, wsBase, aliceTok)
	require.NoError(t, err)
	defer aConn.Close()
	sendHello(t, aConn, aliceTok, "WEB", alice+"-d")
	readWelcome(t, aConn)

	bConn, _, err := dialWS(ctx, wsBase, bobTok)
	require.NoError(t, err)
	defer bConn.Close()
	sendHello(t, bConn, bobTok, "WEB", bob+"-d")
	readWelcome(t, bConn)

	// Alice sends.
	sendText(t, aConn, convID, "ping", "c1")
	ack := readAck(t, aConn)
	_ = readRecv(t, bConn) // bob receives

	// Bob marks read.
	sendMarkRead(t, bConn, convID, ack.ServerMsgID)
	time.Sleep(300 * time.Millisecond) // let projection write

	// Read conversation list and find unread=0.
	req, _ := http.NewRequest("GET", apiBase+"/api/v1/conversation/list?limit=50", nil)
	req.Header.Set("Authorization", "Bearer "+bobTok)
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, 200, res.StatusCode)
	var out struct {
		Data []struct {
			ConvID string `json:"ConvID"`
			Unread int    `json:"Unread"`
		} `json:"data"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
	var unread = -1
	for _, c := range out.Data {
		if c.ConvID == convID {
			unread = c.Unread
			break
		}
	}
	require.Equal(t, 0, unread, "expected unread=0 after mark-read, got %d", unread)
}
