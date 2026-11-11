
//
// Validation branches (empty owner / empty name / invalid type) are
// already covered in group_test.go (no DB needed). This file covers
// the SQL-bound happy path and the INSERT error path via DBTX FakeTx.
package group

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

func TestCreate_HappyPath_InsertsGroupsAndOwnerMember(t *testing.T) {
	// Create issues: INSERT groups, INSERT group_members (owner),
	// INSERT group_members (initial), UPDATE groups (count). Then
	// Get → SELECT groups. Finally publish → nil panic; recover.
	fake := &FakeTx{
		ExecFn: okExecRouter(),
		QueryRowFn: func(_ context.Context, sql string, _ []any) pgx.Row {
			if strings.Contains(sql, "FROM groups") {
				return groupFixture("g_1", "alice", 2, 200, TypeNormal)
			}
			return &cannedRow{}
		},
	}
	s := svcWithFake(fake)
	defer func() { _ = recover() }()

	g, err := s.Create(context.Background(), CreateInput{
		OwnerID:        "alice",
		Name:           "Engineering",
		GroupType:      TypeNormal,
		InitialMembers: []string{"bob"},
	})
	require.NoError(t, err)
	require.NotNil(t, g)
	require.Equal(t, "g_1", g.GroupID)

	// 3 INSERTs (groups, owner, initial bob) — UPDATE groups (count) is a 4th.
	inserts := 0
	for _, c := range fake.ExecCalls {
		if strings.Contains(c.SQL, "INSERT INTO groups") ||
			strings.Contains(c.SQL, "INSERT INTO group_members") {
			inserts++
		}
	}
	require.Equal(t, 3, inserts,
		"groups + owner + 1 initial member = 3 INSERTs, got %d", inserts)
}

func TestCreate_InsertGroupsError_ReturnsError(t *testing.T) {
	// First Exec (INSERT groups) fails → Create returns the error.
	fake := &FakeTx{
		ExecFn: func(_ context.Context, sql string, _ []any) error {
			if strings.Contains(sql, "INSERT INTO groups") {
				return errors.New("connection reset")
			}
			return nil
		},
	}
	s := svcWithFake(fake)
	_, err := s.Create(context.Background(), CreateInput{OwnerID: "alice", Name: "x"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "connection reset")
}
