// Package friend — persistence.go: SQL helpers for the friendships table.
//
// All multi-row writes (sendRequest, acceptRequest, removeEdge) run
// inside s.cell.DB.WithTx so both edges of a friendship stay in
// lockstep. The single-row reads (edgeExists, listByStatus, scanRow)
// go straight through DBTX so unit tests can substitute a fake.
//
// WithTx is on the concrete *db.DB, not on the DBTX test-seam interface.
// The service holds *cell.Cell which carries *db.DB.
package friend

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// rowColumns is the column list used by every SELECT. Locked in one
// place so the struct <-> column mapping never drifts.
const rowColumns = `user_id, friend_id, status, created_at`

// scanRow accepts a pgx.Row and decodes it into a Friendship. Used by
// both QueryRow + Query so the decode lives in exactly one place.
func scanRow(row pgx.Row) (*Friendship, error) {
	var f Friendship
	if err := row.Scan(&f.UserID, &f.FriendID, &f.Status, &f.CreatedAt); err != nil {
		return nil, err
	}
	return &f, nil
}

// edgeExists answers "does ANY row exist between userA and userB?" —
// used to short-circuit SendRequest when a duplicate row pair exists.
func (s *Service) edgeExists(ctx context.Context, userA, userB string) (bool, error) {
	var n int
	if err := s.db.QueryRow(ctx, `
		SELECT (
			SELECT COUNT(*) FROM friendships
			WHERE (user_id = $1 AND friend_id = $2)
			   OR (user_id = $2 AND friend_id = $1)
			LIMIT 1)`,
		userA, userB,
	).Scan(&n); err != nil {
		return false, fmt.Errorf("friend: edge-exists check: %w", err)
	}
	return n > 0, nil
}

// sendRequest writes the two edges of a new pending request inside a
// transaction. Both writes succeed or both rollback. The service
// checks edgeExists BEFORE calling this — the row-count check here
// closes the race when two concurrent calls both pass edgeExists: the
// loser's ON CONFLICT DO NOTHING makes both INSERTs no-ops, so
// RowsAffected is 0 for BOTH edges and we surface ErrRequestExists.
//
// Returns:
//   - ErrRequestExists — the relationship is already present
//     (caught via 0 rows-affected from either INSERT)
//   - wrapped DB error — transactional failure (the whole tx is
//     rolled back; neither edge is written)
func (s *Service) sendRequest(ctx context.Context, userA, userB string) error {
	return s.runTx(ctx, func(tx pgx.Tx) error {
		ct, err := tx.Exec(ctx, `
			INSERT INTO friendships (user_id, friend_id, status)
			VALUES ($1, $2, 'pending_out')
			ON CONFLICT (user_id, friend_id) DO NOTHING`,
			userA, userB)
		if err != nil {
			return fmt.Errorf("friend: insert pending_out: %w", err)
		}
		if ct.RowsAffected() == 0 {
			// Race loser: edgeExists passed but the row was inserted
			// concurrently. Surface ErrRequestExists via the tx
			// rollback; the caller treats it identically to the
			// edgeExists short-circuit path.
			return ErrRequestExists
		}
		ct, err = tx.Exec(ctx, `
			INSERT INTO friendships (user_id, friend_id, status)
			VALUES ($1, $2, 'pending_in')
			ON CONFLICT (user_id, friend_id) DO NOTHING`,
			userB, userA)
		if err != nil {
			return fmt.Errorf("friend: insert pending_in: %w", err)
		}
		if ct.RowsAffected() == 0 {
			// Same race on the reverse edge — same outcome.
			return ErrRequestExists
		}
		return nil
	})
}

// acceptRequest flips BOTH edges of an existing pending pair to
// 'accepted'. Returns ErrNotPending if no row pair is in pending
// state (caller may have raced a concurrent accept or decline).
func (s *Service) acceptRequest(ctx context.Context, userA, fromB string) error {
	var n int64
	err := s.runTx(ctx, func(tx pgx.Tx) error {
		// The acceptance caller is fromB → fromB owns the pending_out
		// row; userA owns the pending_in row. UPDATE both.
		ct, err := tx.Exec(ctx, `
			UPDATE friendships
			SET status = 'accepted'
			WHERE (user_id = $1 AND friend_id = $2 AND status = 'pending_out')
			   OR (user_id = $2 AND friend_id = $1 AND status = 'pending_in')`,
			fromB, userA)
		if err != nil {
			return fmt.Errorf("friend: accept update: %w", err)
		}
		n = ct.RowsAffected()
		return nil
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotPending
	}
	return nil
}

// removeEdge deletes both rows of a friendship (or pending pair) in
// one transaction. Used for Decline + Remove — both are "tear down
// the row pair" operations, just under different UX labels.
//
// `requiredStatus` filters which rows to delete; pass "" to delete
// regardless of current status. Returns ErrNotPending (when called
// from Decline on a missing pair) or ErrNotFriend (when called from
// Remove on a non-accepted pair).
func (s *Service) removeEdge(ctx context.Context, userA, userB, requiredStatus string) error {
	var n int64
	err := s.runTx(ctx, func(tx pgx.Tx) error {
		var ct pgconn.CommandTag
		var err error
		if requiredStatus == "" {
			ct, err = tx.Exec(ctx, `
				DELETE FROM friendships
				WHERE (user_id = $1 AND friend_id = $2)
				   OR (user_id = $2 AND friend_id = $1)`,
				userA, userB)
		} else {
			ct, err = tx.Exec(ctx, `
				DELETE FROM friendships
				WHERE ((user_id = $1 AND friend_id = $2)
				    OR (user_id = $2 AND friend_id = $1))
				  AND status = $3`,
				userA, userB, requiredStatus)
		}
		if err != nil {
			return fmt.Errorf("friend: remove: %w", err)
		}
		n = ct.RowsAffected()
		return nil
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFriend
	}
	return nil
}

// listByStatus returns rows where user_id = $1 AND status = $2,
// ordered by created_at DESC. Used by all three List* methods.
func (s *Service) listByStatus(ctx context.Context, userID string, status Status) ([]*Friendship, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+rowColumns+`
		FROM friendships
		WHERE user_id = $1 AND status = $2
		ORDER BY created_at DESC`,
		userID, string(status))
	if err != nil {
		return nil, fmt.Errorf("friend: list %s: %w", status, err)
	}
	defer rows.Close()

	var out []*Friendship
	for rows.Next() {
		f, err := scanRow(rows)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			return nil, fmt.Errorf("friend: scan: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// runTx wraps s.cell.DB.WithTx so callers don't have to spell the
// s.cell.DB.WithTx(...) chain at every site. Cell carries *db.DB
// directly; friend.Service gets it via s.cell.
func (s *Service) runTx(ctx context.Context, fn func(pgx.Tx) error) error {
	return s.cell.DB.WithTx(ctx, fn)
}
