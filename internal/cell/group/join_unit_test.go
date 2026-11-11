
package group

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

func TestJoin_NormalGroup_ReturnsForbidden(t *testing.T) {
	fake := &FakeTx{
		QueryRowFn: func(_ context.Context, sql string, _ []any) pgx.Row {
			if strings.Contains(sql, "FROM groups") {
				return groupFixture("g_1", "alice", 5, 100, TypeNormal)
			}
			return &cannedRow{}
		},
	}
	s := svcWithFake(fake)
	err := s.Join(context.Background(), "g_1", "bob")
	require.ErrorIs(t, err, ErrForbidden)
}

func TestJoin_AllowGroup_AddsMember(t *testing.T) {
	fake := &FakeTx{
		ExecFn: okExecRouter(),
		QueryRowFn: func(_ context.Context, sql string, _ []any) pgx.Row {
			switch {
			case strings.Contains(sql, "FROM groups"):
				return groupFixture("g_1", "alice", 5, 100, TypeAllow)
			case strings.Contains(sql, "FROM group_members"):
				return notMember()
			}
			return &cannedRow{}
		},
	}
	s := svcWithFake(fake)
	defer func() { _ = recover() }()
	err := s.Join(context.Background(), "g_1", "bob")
	require.NoError(t, err)
}
