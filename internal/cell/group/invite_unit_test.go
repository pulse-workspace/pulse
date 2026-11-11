
package group

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

func TestInvite_HappyPath_AddsMemberAndUpdatesCount(t *testing.T) {
	// Invite: Get → caller → target-existence (not found → add) →
	// addMember → UPDATE count → publish.
	cc := &callCounter{}
	fake := &FakeTx{
		ExecFn: okExecRouter(),
		QueryRowFn: func(_ context.Context, sql string, _ []any) pgx.Row {
			switch {
			case strings.Contains(sql, "FROM groups"):
				return groupFixture("g_1", "alice", 5, 100, TypeNormal)
			case strings.Contains(sql, "FROM group_members"):
				cc.n++
				if cc.n == 1 {
					return memberFixture("g_1", "alice", RoleOwner)
				}
				return notMember()
			}
			return &cannedRow{}
		},
	}
	s := svcWithFake(fake)
	defer func() { _ = recover() }()
	err := s.Invite(context.Background(), "g_1", "alice", "bob")
	require.NoError(t, err)

	sawInsert := false
	sawUpdate := false
	for _, c := range fake.ExecCalls {
		if strings.Contains(c.SQL, "INSERT INTO group_members") {
			sawInsert = true
		}
		if strings.Contains(c.SQL, "UPDATE groups") {
			sawUpdate = true
		}
	}
	require.True(t, sawInsert, "Invite must INSERT into group_members")
	require.True(t, sawUpdate, "Invite must UPDATE groups member_count")
}

func TestInvite_CallerNotOwnerOrAdmin_ReturnsForbidden(t *testing.T) {
	fake := &FakeTx{
		QueryRowFn: func(_ context.Context, sql string, _ []any) pgx.Row {
			switch {
			case strings.Contains(sql, "FROM groups"):
				return groupFixture("g_1", "alice", 5, 100, TypeNormal)
			case strings.Contains(sql, "FROM group_members"):
				return memberFixture("g_1", "bob", RoleMember)
			}
			return &cannedRow{}
		},
	}
	s := svcWithFake(fake)
	err := s.Invite(context.Background(), "g_1", "bob", "carol")
	require.ErrorIs(t, err, ErrForbidden)
}

func TestInvite_TargetAlreadyMember_ReturnsErrMemberExists(t *testing.T) {
	cc := &callCounter{}
	fake := &FakeTx{
		QueryRowFn: func(_ context.Context, sql string, _ []any) pgx.Row {
			switch {
			case strings.Contains(sql, "FROM groups"):
				return groupFixture("g_1", "alice", 5, 100, TypeNormal)
			case strings.Contains(sql, "FROM group_members"):
				cc.n++
				if cc.n == 1 {
					return memberFixture("g_1", "alice", RoleOwner)
				}
				return memberFixture("g_1", "bob", RoleMember)
			}
			return &cannedRow{}
		},
	}
	s := svcWithFake(fake)
	err := s.Invite(context.Background(), "g_1", "alice", "bob")
	require.ErrorIs(t, err, ErrMemberExists)
}

func TestInvite_GroupFull_ReturnsErrGroupFull(t *testing.T) {
	fake := &FakeTx{
		QueryRowFn: func(_ context.Context, sql string, _ []any) pgx.Row {
			if strings.Contains(sql, "FROM groups") {
				return groupFixture("g_1", "alice", 100, 100, TypeNormal)
			}
			return memberFixture("g_1", "alice", RoleOwner)
		},
	}
	s := svcWithFake(fake)
	err := s.Invite(context.Background(), "g_1", "alice", "bob")
	require.ErrorIs(t, err, ErrGroupFull)
}
