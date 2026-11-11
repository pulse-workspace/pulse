//go:build integration
// +build integration

package integration

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestGroupFlow covers Week 7-8 group service via REST:
//   1. admin init/login
//   2. register alice, bob, carol
//   3. alice creates group "Engineering" with bob as admin
//   4. alice invites carol
//   5. group members = 3
//   6. bob (admin) invites dave
//   7. carol quits
//   8. member_count = 3
//   9. bob kicks dave
//  10. members = 2
//  11. alice dismisses the group
//  12. GET /group/{id} returns 404
func TestGroupFlow(t *testing.T) {
	apiBase := envOr("PULSE_TEST_API", "http://localhost:32011")

	adminTok := adminEnsure(t, apiBase)

	// Register users
	for _, u := range []struct{ id, email string }{
		{"alice", "alice-g-at-test"},
		{"bob", "bob-g-at-test"},
		{"carol", "carol-g-at-test"},
		{"dave", "dave-g-at-test"},
	} {
		register(t, apiBase, adminTok, u.id, u.email)
	}

	// Mint user tokens
	aliceTok := issueUserTokenMsg(t, apiBase, adminTok, "alice", "WEB", "alice-g-1")
	bobTok := issueUserTokenMsg(t, apiBase, adminTok, "bob", "WEB", "bob-g-1")

	// 3. alice creates group with bob as admin
	groupID := createGroup(t, apiBase, aliceTok, "Engineering", nil, []string{"bob"})
	require.True(t, strings.HasPrefix(groupID, "g_"), "expected g_ prefix, got %q", groupID)
	t.Logf("group created: %s", groupID)

	// 5. members = 2 (alice + bob)
	members := listMembers(t, apiBase, aliceTok, groupID)
	require.Len(t, members, 2)
	t.Logf("members after create: %d", len(members))

	// 4. alice invites carol
	inviteOK(t, apiBase, aliceTok, groupID, "carol")
	members = listMembers(t, apiBase, aliceTok, groupID)
	require.Len(t, members, 3, "expected 3 members after inviting carol, got %d", len(members))
	t.Logf("members after carol: %d", len(members))

	// 6. bob (admin) invites dave
	inviteOK(t, apiBase, bobTok, groupID, "dave")
	members = listMembers(t, apiBase, aliceTok, groupID)
	require.Len(t, members, 4, "expected 4 members after bob invites dave")

	// 7. carol quits
	carolTok := issueUserTokenMsg(t, apiBase, adminTok, "carol", "WEB", "carol-g-1")
	quitOK(t, apiBase, carolTok, groupID)
	members = listMembers(t, apiBase, aliceTok, groupID)
	require.Len(t, members, 3, "expected 3 members after carol quits, got %d", len(members))

	// 9. bob kicks dave
	inviteOKErr(t, apiBase, bobTok, groupID, "dave", "dave is already kicked (re-invite should 409)") // sanity
	kickOK(t, apiBase, bobTok, groupID, "dave")
	members = listMembers(t, apiBase, aliceTok, groupID)
	require.Len(t, members, 2, "expected 2 members after kicking dave, got %d", len(members))

	// 11. alice dismisses group
	dismissOK(t, apiBase, aliceTok, groupID)

	// 12. GET returns 404
	req, _ := http.NewRequest("GET", apiBase+"/api/v1/group/"+groupID, nil)
	req.Header.Set("Authorization", "Bearer "+aliceTok)
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, 404, res.StatusCode, "expected 404 after dismiss")

	t.Log("group flow OK")
}

// --- group-test-only helpers ---------------------------------------------

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
	require.Equal(t, 200, res.StatusCode, "createGroup status=%d", res.StatusCode)
	var out struct {
		Data struct {
			GroupID string `json:"group_id"`
		} `json:"data"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
	require.NotEmpty(t, out.Data.GroupID)
	return out.Data.GroupID
}

func listMembers(t *testing.T, apiBase, userTok, groupID string) []map[string]any {
	t.Helper()
	req, _ := http.NewRequest("GET", apiBase+"/api/v1/group/"+groupID+"/members", nil)
	req.Header.Set("Authorization", "Bearer "+userTok)
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, 200, res.StatusCode, "listMembers status=%d", res.StatusCode)
	var out struct {
		Data []map[string]any `json:"data"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
	return out.Data
}

func inviteOK(t *testing.T, apiBase, userTok, groupID, targetID string) {
	t.Helper()
	body := `{"userID":"` + targetID + `"}`
	req, _ := http.NewRequest("POST", apiBase+"/api/v1/group/"+groupID+"/invite", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+userTok)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, 200, res.StatusCode, "invite %s status=%d", targetID, res.StatusCode)
}

func inviteOKErr(t *testing.T, apiBase, userTok, groupID, targetID, desc string) {
	t.Helper()
	body := `{"userID":"` + targetID + `"}`
	req, _ := http.NewRequest("POST", apiBase+"/api/v1/group/"+groupID+"/invite", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+userTok)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	// 409 expected (already a member or just kicked).
	require.True(t, res.StatusCode >= 400, "%s: expected error, got %d", desc, res.StatusCode)
}

func kickOK(t *testing.T, apiBase, userTok, groupID, targetID string) {
	t.Helper()
	body := `{"userID":"` + targetID + `"}`
	req, _ := http.NewRequest("POST", apiBase+"/api/v1/group/"+groupID+"/kick", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+userTok)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, 200, res.StatusCode, "kick %s status=%d", targetID, res.StatusCode)
}

func quitOK(t *testing.T, apiBase, userTok, groupID string) {
	t.Helper()
	req, _ := http.NewRequest("POST", apiBase+"/api/v1/group/"+groupID+"/quit", nil)
	req.Header.Set("Authorization", "Bearer "+userTok)
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, 200, res.StatusCode, "quit status=%d", res.StatusCode)
}

func dismissOK(t *testing.T, apiBase, userTok, groupID string) {
	t.Helper()
	req, _ := http.NewRequest("POST", apiBase+"/api/v1/group/"+groupID+"/dismiss", nil)
	req.Header.Set("Authorization", "Bearer "+userTok)
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, 200, res.StatusCode, "dismiss status=%d", res.StatusCode)
}