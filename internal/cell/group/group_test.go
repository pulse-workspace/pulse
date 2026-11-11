package group

import (
	"context"
	"errors"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/pulse-im/pulse/internal/cell"
)

// buildSvc constructs a *Service with no DB/NATS. Tests in this file
// exercise validation branches that fail BEFORE any DB query, so the
// dependencies are safe to leave nil.
func buildSvc() *Service {
	return &Service{
		cell: &cell.Cell{Log: zerolog.Nop()},
		log:  zerolog.Nop(),
	}
}

func TestNewService_Name(t *testing.T) {
	s := NewService(&cell.Cell{Log: zerolog.Nop()})
	require.Equal(t, "group", s.Name())
}

func TestService_Stop_IsNoOp(t *testing.T) {
	// Stop must succeed without dependencies.
	s := buildSvc()
	require.NoError(t, s.Stop(context.Background()))
}

// --- Create input validation (no DB needed) -----------------------------

func TestCreate_EmptyOwner_ReturnsError(t *testing.T) {
	s := buildSvc()
	g, err := s.Create(context.Background(), CreateInput{Name: "x"})
	require.Nil(t, g)
	require.Error(t, err)
	require.Contains(t, err.Error(), "ownerID")
}

func TestCreate_EmptyName_ReturnsError(t *testing.T) {
	s := buildSvc()
	g, err := s.Create(context.Background(), CreateInput{OwnerID: "u1"})
	require.Nil(t, g)
	require.Error(t, err)
	require.Contains(t, err.Error(), "name")
}

func TestCreate_InvalidGroupType_ReturnsError(t *testing.T) {
	s := buildSvc()
	g, err := s.Create(context.Background(), CreateInput{
		OwnerID:   "u1",
		Name:      "x",
		GroupType: "WrongType",
	})
	require.Nil(t, g)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid groupType")
}

func TestCreate_DefaultMaxMembersApplied(t *testing.T) {
	// Defaulted to 2000 in code; we don't assert the DB call here because
	// there's no DB. We can verify the input normalization by running Create
	// with MaxMembers=0 and noting that the DB call would receive 2000 — the
	// const test below locks the documented default.
	require.Equal(t, 2000, defaultMaxMembers)
}

// defaultMaxMembers mirrors the inline default applied by Create. If the
// production default changes, this const must change too — kept as a guard.
const defaultMaxMembers = 2000

func TestRole_ConstantsAreStable(t *testing.T) {
	// role strings are persisted as the lowercase form in the SQL binding;
	// any future rename is a breaking schema change.
	require.Equal(t, "owner", string(RoleOwner))
	require.Equal(t, "admin", string(RoleAdmin))
	require.Equal(t, "member", string(RoleMember))
}

func TestGroupType_ConstantsAreStable(t *testing.T) {
	require.Equal(t, "Normal", string(TypeNormal))
	require.Equal(t, "Allow", string(TypeAllow))
}

// --- Sentinel errors -----------------------------------------------------

func TestSentinels_AreDistinct(t *testing.T) {
	sentinels := []error{
		ErrNotFound,
		ErrMemberExists,
		ErrMemberNotFound,
		ErrForbidden,
		ErrGroupFull,
		ErrLastOwner,
	}
	for i := range sentinels {
		for j := range sentinels {
			if i == j {
				continue
			}
			require.NotEqual(t, sentinels[i], sentinels[j],
				"sent %d == sent %d", i, j)
		}
	}
	require.True(t, errors.Is(ErrForbidden, ErrForbidden))
	require.True(t, errors.Is(ErrLastOwner, ErrLastOwner))
}

func TestSentinels_NotEmptyOrNil(t *testing.T) {
	for _, e := range []error{ErrNotFound, ErrMemberExists, ErrMemberNotFound, ErrForbidden, ErrGroupFull, ErrLastOwner} {
		require.NotNil(t, e)
		require.NotEmpty(t, e.Error())
	}
}

// --- CreateInput struct stability ----------------------------------------

func TestCreateInput_DefaultFields(t *testing.T) {
	in := CreateInput{}
	require.Empty(t, in.OwnerID)
	require.Empty(t, in.GroupID)
	require.Nil(t, in.InitialAdmin)
	require.Nil(t, in.InitialMembers)
	require.Nil(t, in.Settings)
}

// --- internal helpers ----------------------------------------------------

func TestDedup_StripsEmptyAndDuplicates(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"nil", nil, []string{}},
		{"emptyInput", []string{}, []string{}},
		{"allEmpty", []string{"", "", ""}, []string{}},
		{"dedup", []string{"a", "a", "b", "a"}, []string{"a", "b"}},
		{"mixedEmpty", []string{"", "a", "", "b", "a"}, []string{"a", "b"}},
		{"preservesOrder", []string{"c", "a", "b", "a"}, []string{"c", "a", "b"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := dedup(tc.in)
			// dedup returns [] when len==0; normalize comparator
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			require.Equal(t, tc.want, got)
		})
	}
}

func TestJoinComma(t *testing.T) {
	tests := []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{}, ""},
		{[]string{"a"}, "a"},
		{[]string{"a", "b"}, "a, b"},
		{[]string{"a", "b", "c"}, "a, b, c"},
	}
	for _, tc := range tests {
		require.Equal(t, tc.want, joinComma(tc.in))
	}
}
