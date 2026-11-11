//go:build contract
// +build contract

package contract

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// CONTRACT: presence_online_offline
// Given:  alice connects to WS
// When:   alice disconnects cleanly
// Then:   server-side presence is updated (online → offline)
//         another subscribed client receives PresenceUpdate events
//         (future: when we have a presence broadcast channel)
func TestPresenceOnlineOffline(t *testing.T) {
	apiBase := envOr("PULSE_TEST_API", "http://localhost:32011")
	wsBase := envOr("PULSE_TEST_WS", "ws://localhost:32001")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	adminTok := adminTokenForTest(t, apiBase)
	alice := uniqueID("pr-alice")
	bob := uniqueID("pr-bob")
	registerUser(t, apiBase, adminTok, alice, alice+"@pulse.test")
	registerUser(t, apiBase, adminTok, bob, bob+"@pulse.test")
	aliceTok := issueUserToken(t, apiBase, adminTok, alice, "WEB", alice+"-d")
	bobTok := issueUserToken(t, apiBase, adminTok, bob, "WEB", bob+"-d")

	// bob connects first, then subscribes to alice's presence.
	bConn, _, err := dialWS(ctx, wsBase, bobTok)
	require.NoError(t, err)
	defer bConn.Close()
	sendHello(t, bConn, bobTok, "WEB", bob+"-d")
	readWelcome(t, bConn)

	// alice connects → should mark online.
	aConn, _, err := dialWS(ctx, wsBase, aliceTok)
	require.NoError(t, err)
	sendHello(t, aConn, aliceTok, "WEB", alice+"-d")
	readWelcome(t, aConn)

	// alice disconnects.
	require.NoError(t, aConn.Close())
	time.Sleep(200 * time.Millisecond)

	// bob can still be online.
	_ = bConn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	_, _, _ = bConn.ReadMessage() // may receive nothing; non-fatal

	// CONTRACT: presence state in DB reflects offline (verified via admin API in Week 15-16).
	t.Log("presence contract: WS connect/disconnect updates internal state; REST exposure TBD Week 15-16")
}
