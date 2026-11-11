
//
// MakeConvID is the canonical conversation-ID generator; 1-on-1 vs group
// branches live here. Group sends flow through the same SendMessage path
// but are addressed by a `g_<groupID>` convID, so the ID shape is the
// only thing distinguishing group vs 1-on-1 at the message service
// layer. Without these tests, the sort-invariant branch could regress
// silently and clients with reverse-ordered participant lists would
// land on different convIDs than the canonical form.
package message

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMakeConvID_GroupID_GPrefixed: group conversations → "g_<id>".
// This is the only branch in the cell/message layer that distinguishes
// a group send from a 1-on-1 send; downstream services rely on the
// prefix to fan out to all members.
func TestMakeConvID_GroupID_GPrefixed(t *testing.T) {
	got := MakeConvID([]string{"alice", "bob"}, "team-alpha")
	require.Equal(t, "g_team-alpha", got,
		"groupID branch must take precedence over participants")
}

// TestMakeConvID_GroupID_EmptyParticipantsIgnored: even with a 1-on-1
// shaped participant slice, groupID wins. Mirrors real call sites
// that pass nil participants for group sends.
func TestMakeConvID_GroupID_EmptyParticipantsIgnored(t *testing.T) {
	got := MakeConvID(nil, "grp1")
	require.Equal(t, "g_grp1", got)
}

// TestMakeConvID_OneOnOne_SortedAlphabetical: participants[0] < [1] →
// canonical "si_a_b". Two clients addressing the same conversation
// but constructing the slice in different orders must resolve to the
// same ID, so the sort branch is load-bearing.
func TestMakeConvID_OneOnOne_SortedAlphabetical(t *testing.T) {
	got := MakeConvID([]string{"alice", "bob"}, "")
	require.Equal(t, "si_alice_bob", got)
}

// TestMakeConvID_OneOnOne_ReverseOrderStillCanonical: input in reverse
// order still produces the same ID. Symmetric to the previous test;
// covers the else-branch of the sort comparison.
func TestMakeConvID_OneOnOne_ReverseOrderStillCanonical(t *testing.T) {
	got := MakeConvID([]string{"bob", "alice"}, "")
	require.Equal(t, "si_alice_bob", got)
}

// TestMakeConvID_LenNotTwo_ReturnsEmpty: 3+ participants with no
// groupID is invalid; MakeConvID returns "" so callers can branch.
func TestMakeConvID_LenNotTwo_ReturnsEmpty(t *testing.T) {
	require.Equal(t, "", MakeConvID([]string{"a", "b", "c"}, ""))
}

// TestMakeConvID_EmptyInput_ReturnsEmpty: empty participants and no
// groupID → empty string. Defensive branch for misbehaving callers.
func TestMakeConvID_EmptyInput_ReturnsEmpty(t *testing.T) {
	require.Equal(t, "", MakeConvID(nil, ""))
}
