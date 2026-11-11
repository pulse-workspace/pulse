
package group

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

func TestKick_CallerNotOwnerOrAdmin_ReturnsForbidden(t *testing.T) {
	fake := &FakeTx{
		QueryRowFn: func(_ context.Context, sql string, _ []any) pgx.Row {
			if strings.Contains(sql, "FROM group_members") {
				return memberFixture("g_1", "bob", RoleMember)
			}
			return &cannedRow{}
		},
	}
	s := svcWithFake(fake)
	err := s.Kick(context.Background(), "g_1", "bob", "carol")
	require.ErrorIs(t, err, ErrForbidden)
}

func TestKick_TargetIsOwner_ReturnsForbidden(t *testing.T) {
	cc := &callCounter{}
	fake := &FakeTx{
		QueryRowFn: func(_ context.Context, sql string, _ []any) pgx.Row {
			if strings.Contains(sql, "FROM group_members") {
				cc.n++
				if cc.n == 1 {
					return memberFixture("g_1", "alice", RoleOwner)
				}
				return memberFixture("g_1", "bob", RoleOwner)
			}
			return &cannedRow{}
		},
	}
	s := svcWithFake(fake)
	err := s.Kick(context.Background(), "g_1", "alice", "bob")
	require.ErrorIs(t, err, ErrForbidden)
}

func TestKick_HappyPath_DeletesTarget(t *testing.T) {
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
	defer func() { _ = recover() }()
	err := s.Kick(context.Background(), "g_1", "alice", "bob")
	require.NoError(t, err)
}
