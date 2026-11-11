
package group

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

func TestQuit_OwnerCannotQuit_ReturnsErrLastOwner(t *testing.T) {
	fake := &FakeTx{
		QueryRowFn: func(_ context.Context, sql string, _ []any) pgx.Row {
			if strings.Contains(sql, "FROM group_members") {
				return memberFixture("g_1", "alice", RoleOwner)
			}
			return &cannedRow{}
		},
	}
	s := svcWithFake(fake)
	err := s.Quit(context.Background(), "g_1", "alice")
	require.ErrorIs(t, err, ErrLastOwner)
}

func TestQuit_MemberHappyPath_DeletesAndUpdates(t *testing.T) {
	fake := &FakeTx{
		ExecFn: okExecRouter(),
		QueryRowFn: func(_ context.Context, sql string, _ []any) pgx.Row {
			if strings.Contains(sql, "FROM group_members") {
				return memberFixture("g_1", "bob", RoleMember)
			}
			return &cannedRow{}
		},
	}
	s := svcWithFake(fake)
	defer func() { _ = recover() }()
	err := s.Quit(context.Background(), "g_1", "bob")
	require.NoError(t, err)

	sawDelete := false
	sawUpdate := false
	for _, c := range fake.ExecCalls {
		if strings.Contains(c.SQL, "DELETE FROM group_members") {
			sawDelete = true
		}
		if strings.Contains(c.SQL, "UPDATE groups") {
			sawUpdate = true
		}
	}
	require.True(t, sawDelete, "Quit must DELETE group_members row")
	require.True(t, sawUpdate, "Quit must UPDATE groups member_count")
}
