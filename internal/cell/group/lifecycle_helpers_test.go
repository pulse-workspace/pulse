
// unit tests (create / invite / join / quit / kick / dismiss / role).
//
// Helpers:
//   - rowHandler   : alias for the FakeTx QueryRowFn signature.
//   - callCounter  : discriminate successive QueryRow calls.
//   - groupFixture : canned Group projection.
//   - memberFixture: canned Member projection.
//   - notMember    : pgx.Row whose Scan returns pgx.ErrNoRows.
//
// Kept in its own file so the per-method test files stay under the
// 200 LOC development-rules limit.
package group

import (
	"time"

	"github.com/jackc/pgx/v5"
)

// callCounter lets tests discriminate successive QueryRow calls (e.g.
// "caller" vs "target" in Invite). The SUT issues them in a known order;
// we count and switch on parity.
type callCounter struct {
	n int
}

// groupFixture builds a canned Group projection used by tests that
// need s.Get to succeed.
func groupFixture(id, owner string, memberCount, maxMembers int, gt GroupType) *cannedRow {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return &cannedRow{values: []any{
		id, "Team " + id, "", owner, gt, memberCount, maxMembers,
		"", "", map[string]any{}, now, now,
	}}
}

// memberFixture builds a canned Member projection.
func memberFixture(groupID, userID string, role Role) *cannedRow {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return &cannedRow{values: []any{
		groupID, userID, role, now, (*time.Time)(nil), "",
	}}
}

// notMember returns a Row whose Scan returns pgx.ErrNoRows. Used when
// the SUT does a "does this user already exist?" probe and we want
// the "not present" answer.
func notMember() pgx.Row { return &fakeRow{err: pgx.ErrNoRows} }
