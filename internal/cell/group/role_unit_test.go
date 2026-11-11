
package group

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

func TestTransferOwner_CallerNotOwner_ReturnsForbidden(t *testing.T) {
	fake := &FakeTx{
		QueryRowFn: func(_ context.Context, sql string, _ []any) pgx.Row {
			if strings.Contains(sql, "FROM group_members") {
				return memberFixture("g_1", "alice", RoleAdmin)
			}
			return &cannedRow{}
		},
	}
	s := svcWithFake(fake)
	err := s.TransferOwner(context.Background(), "g_1", "alice", "bob")
	require.ErrorIs(t, err, ErrForbidden)
}

func TestTransferOwner_SameUserIsNoop(t *testing.T) {
	// caller == newOwnerID → return nil without writing.
	fake := &FakeTx{
		QueryRowFn: func(_ context.Context, sql string, _ []any) pgx.Row {
			return memberFixture("g_1", "alice", RoleOwner)
		},
	}
	s := svcWithFake(fake)
	err := s.TransferOwner(context.Background(), "g_1", "alice", "alice")
	require.NoError(t, err)
	require.Empty(t, fake.ExecCalls, "noop must not write")
}

func TestTransferOwner_HappyPath_UpdatesRolesAndOwner(t *testing.T) {
	cc := &callCounter{}
	fake := &FakeTx{
		ExecFn: okExecRouter(),
		QueryRowFn: func(_ context.Context, sql string, _ []any) pgx.Row {
			if strings.Contains(sql, "FROM group_members") {
				cc.n++
				if cc.n == 1 {
					return memberFixture("g_1", "alice", RoleOwner)
				}
				return memberFixture("g_1", "bob", RoleAdmin)
			}
			return &cannedRow{}
		},
	}
	s := svcWithFake(fake)
	defer func() { _ = recover() }()
	err := s.TransferOwner(context.Background(), "g_1", "alice", "bob")
	require.NoError(t, err)

	sawC := false
	sawB := false
	sawGroups := false
	for _, c := range fake.ExecCalls {
		switch {
		case strings.Contains(c.SQL, "UPDATE group_members SET role = 'admin'") &&
			len(c.Args) >= 2 && c.Args[1] == "alice":
			sawC = true
		case strings.Contains(c.SQL, "UPDATE group_members SET role = 'owner'") &&
			len(c.Args) >= 2 && c.Args[1] == "bob":
			sawB = true
		case strings.Contains(c.SQL, "UPDATE groups SET owner_id"):
			sawGroups = true
		}
	}
	require.True(t, sawC, "TransferOwner must demote previous owner to admin")
	require.True(t, sawB, "TransferOwner must promote new owner")
	require.True(t, sawGroups, "TransferOwner must UPDATE groups.owner_id")
}

func TestSetRole_InvalidRole_ReturnsError(t *testing.T) {
	fake := &FakeTx{
		QueryRowFn: func(_ context.Context, sql string, _ []any) pgx.Row {
			return memberFixture("g_1", "alice", RoleOwner)
		},
	}
	s := svcWithFake(fake)
	err := s.SetRole(context.Background(), "g_1", "alice", "bob", Role("nope"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid role")
}

func TestSetRole_PromoteToMember_IssuesUpdate(t *testing.T) {
	cc := &callCounter{}
	fake := &FakeTx{
		ExecFn: okExecRouter(),
		QueryRowFn: func(_ context.Context, sql string, _ []any) pgx.Row {
			if strings.Contains(sql, "FROM group_members") {
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
	err := s.SetRole(context.Background(), "g_1", "alice", "bob", RoleAdmin)
	require.NoError(t, err)

	sawUpdate := false
	for _, c := range fake.ExecCalls {
		if strings.Contains(c.SQL, "UPDATE group_members SET role = $3") {
			sawUpdate = true
		}
	}
	require.True(t, sawUpdate, "SetRole must UPDATE group_members.role")
}

func TestSetRole_PromoteToOwner_DelegatesToTransferOwner(t *testing.T) {
	// SetRole(owner) → TransferOwner: each issues GetMember(caller) +
	// GetMember(target) = 4 SELECTs on group_members. Discriminate by
	// the userID arg (args[1] = userID column) so we return alice→owner
	// and bob→member regardless of call order.
	fake := &FakeTx{
		ExecFn: okExecRouter(),
		QueryRowFn: func(_ context.Context, sql string, args []any) pgx.Row {
			if strings.Contains(sql, "FROM group_members") {
				if len(args) >= 2 {
					if uid, ok := args[1].(string); ok {
						switch uid {
						case "alice":
							return memberFixture("g_1", "alice", RoleOwner)
						case "bob":
							return memberFixture("g_1", "bob", RoleMember)
						}
					}
				}
				return memberFixture("g_1", "alice", RoleOwner)
			}
			return &cannedRow{}
		},
	}
	s := svcWithFake(fake)
	defer func() { _ = recover() }()
	err := s.SetRole(context.Background(), "g_1", "alice", "bob", RoleOwner)
	require.NoError(t, err)

	sawGroupsOwner := false
	for _, c := range fake.ExecCalls {
		if strings.Contains(c.SQL, "UPDATE groups SET owner_id") &&
			len(c.Args) >= 2 && c.Args[1] == "bob" {
			sawGroupsOwner = true
		}
	}
	require.True(t, sawGroupsOwner,
		"SetRole(owner) must delegate to TransferOwner and update groups.owner_id")
}
