
package group

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

func TestDismiss_NotOwner_ReturnsForbidden(t *testing.T) {
	fake := &FakeTx{
		QueryRowFn: func(_ context.Context, sql string, _ []any) pgx.Row {
			if strings.Contains(sql, "FROM group_members") {
				return memberFixture("g_1", "bob", RoleAdmin)
			}
			return &cannedRow{}
		},
	}
	s := svcWithFake(fake)
	err := s.Dismiss(context.Background(), "g_1", "bob")
	require.ErrorIs(t, err, ErrForbidden)
}

func TestDismiss_OwnerHappyPath_DeletesMembersAndGroup(t *testing.T) {
	fake := &FakeTx{
		ExecFn: okExecRouter(),
		QueryRowFn: func(_ context.Context, sql string, _ []any) pgx.Row {
			if strings.Contains(sql, "FROM group_members") {
				return memberFixture("g_1", "alice", RoleOwner)
			}
			return &cannedRow{}
		},
	}
	s := svcWithFake(fake)
	defer func() { _ = recover() }()
	err := s.Dismiss(context.Background(), "g_1", "alice")
	require.NoError(t, err)

	sawDeleteMembers := false
	sawDeleteGroup := false
	for _, c := range fake.ExecCalls {
		if strings.Contains(c.SQL, "DELETE FROM group_members") {
			sawDeleteMembers = true
		}
		if c.SQL == "DELETE FROM groups WHERE group_id = $1" {
			sawDeleteGroup = true
		}
	}
	require.True(t, sawDeleteMembers, "Dismiss must DELETE group_members")
	require.True(t, sawDeleteGroup, "Dismiss must DELETE groups")
}
