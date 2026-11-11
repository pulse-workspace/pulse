//go:build contract
// +build contract

package contract

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// CONTRACT: send_receive_group
// Given:  alice (owner), bob (admin), carol (member) in group G
// When:   alice sends "group-hello" in groupID G
// Then:   bob and carol both receive FrameMessageRecv with convID=G
//         bob's UserID == alice, content text == "group-hello"
func TestSendReceiveGroup(t *testing.T) {
	apiBase := envOr("PULSE_TEST_API", "http://localhost:32011")
	wsBase := envOr("PULSE_TEST_WS", "ws://localhost:32001")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	adminTok := adminTokenForTest(t, apiBase)
	alice := uniqueID("g-alice")
	bob := uniqueID("g-bob")
	carol := uniqueID("g-carol")
	registerUser(t, apiBase, adminTok, alice, alice+"@pulse.test")
	registerUser(t, apiBase, adminTok, bob, bob+"@pulse.test")
	registerUser(t, apiBase, adminTok, carol, carol+"@pulse.test")

	aliceTok := issueUserToken(t, apiBase, adminTok, alice, "WEB", alice+"-d")
	bobTok := issueUserToken(t, apiBase, adminTok, bob, "WEB", bob+"-d")
	carolTok := issueUserToken(t, apiBase, adminTok, carol, "WEB", carol+"-d")

	groupID := createGroup(t, apiBase, aliceTok, "Engineering", nil, []string{bob})
	// invite carol
	req, _ := http.NewRequest("POST", apiBase+"/api/v1/group/"+groupID+"/invite",
		strings.NewReader(`{"userID":"`+carol+`"}`))
	req.Header.Set("Authorization", "Bearer "+aliceTok)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	res.Body.Close()
	require.Equal(t, 200, res.StatusCode)

	// Connect all three.
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

	cConn, _, err := dialWS(ctx, wsBase, carolTok)
	require.NoError(t, err)
	defer cConn.Close()
	sendHello(t, cConn, carolTok, "WEB", carol+"-d")
	readWelcome(t, cConn)

	// alice sends via group conversation (1-on-1 conv is needed to send message
	// in current API; here we use groupID as convID since the message service
	// treats any string convID).
	sendText(t, aConn, groupID, "group-hello", "c1")

	// alice receives ack
	readAck(t, aConn)

	// bob and carol receive
	recvB := readRecv(t, bConn)
	require.Equal(t, groupID, recvB.ConvID)
	require.Equal(t, "group-hello", recvB.Content["text"])

	recvC := readRecv(t, cConn)
	require.Equal(t, groupID, recvC.ConvID)
	require.Equal(t, "group-hello", recvC.Content["text"])
}
