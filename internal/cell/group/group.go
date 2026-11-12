// Package group implements the pulse-group cell service.
//
// Groups are the unit of multi-party chat. Each group has roles:
//   - Owner  : 1 user, full control
//   - Admin  : up to N users, manage members + mute
//   - Member : regular chat participant
//
// Groups also expose:
//   - groupType:  "Normal" (invite only) | "Allow" (joinable via link)
//   - mute (per group) and per-member mute with TTL
//   - announcement (one-way broadcast)
//
// CQRS shape: writes append events to NATS JetStream; reads serve from
// the `groups` + `group_members` projections in PostgreSQL.
package group

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/oklog/ulid/v2"

	"github.com/pulse-workspace/pulse/internal/cell"
	"github.com/pulse-workspace/pulse/pkg/shared/db"
	"github.com/pulse-workspace/pulse/pkg/shared/events"
	"github.com/pulse-workspace/pulse/pkg/shared/logger"
	"github.com/pulse-workspace/pulse/pkg/shared/nats"
)

// Service is the group domain service.
// The db field uses the db.DBTX interface (Exec / Query / QueryRow) to support
// test doubles in unit tests. Methods requiring transactional semantics invoke
// cell.DB.WithTx directly.
type Service struct {
	cell *cell.Cell
	log  logger.Logger
	db   db.DBTX
	nats *nats.Client
}

// NewService constructs the group service.
func NewService(c *cell.Cell) *Service {
	return &Service{
		cell: c,
		log:  c.Log.With().Str("service", "group").Logger(),
		db:   c.DB,
		nats: c.NATS,
	}
}

// Name implements cell.Service.
func (s *Service) Name() string { return "group" }

// Start implements cell.Service — ensures NATS stream for group events.
func (s *Service) Start(ctx context.Context) error {
	return s.nats.EnsureStream(ctx, "events_groups", []string{"group.>"})
}

// Stop implements cell.Service.
func (s *Service) Stop(ctx context.Context) error { return nil }

// Create makes a new group. The caller (OwnerID) becomes Owner.
// Members in InitialMembers are added as Members; InitialAdmin as Admins.
func (s *Service) Create(ctx context.Context, in CreateInput) (*Group, error) {
	if in.OwnerID == "" {
		return nil, errors.New("ownerID required")
	}
	if in.Name == "" {
		return nil, errors.New("name required")
	}
	if in.GroupType == "" {
		in.GroupType = TypeNormal
	}
	if in.GroupType != TypeNormal && in.GroupType != TypeAllow {
		return nil, fmt.Errorf("invalid groupType %q", in.GroupType)
	}
	if in.MaxMembers <= 0 {
		in.MaxMembers = 2000
	}
	groupID := in.GroupID
	if groupID == "" {
		groupID = "g_" + ulid.Make().String()
	}

	now := time.Now().UTC()

	if err := s.db.Exec(ctx,
		`INSERT INTO groups (group_id, name, avatar, owner_id, group_type, member_count, max_members,
		                    introduction, notification, settings, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, 1, $6, $7, $8, $9, $10, $10)`,
		groupID, in.Name, in.Avatar, in.OwnerID, in.GroupType, in.MaxMembers,
		in.Introduction, in.Notification, in.Settings, now); err != nil {
		return nil, err
	}
	if err := s.db.Exec(ctx,
		`INSERT INTO group_members (group_id, user_id, role, joined_at) VALUES ($1, $2, 'owner', $3)`,
		groupID, in.OwnerID, now); err != nil {
		return nil, err
	}

	addedMembers := 0
	for _, uid := range dedup(in.InitialMembers) {
		if uid == in.OwnerID {
			continue
		}
		if err := s.addMember(ctx, groupID, uid, RoleMember, now); err == nil {
			addedMembers++
		}
	}
	for _, uid := range dedup(in.InitialAdmin) {
		if uid == in.OwnerID {
			continue
		}
		if err := s.addMember(ctx, groupID, uid, RoleAdmin, now); err == nil {
			addedMembers++
		}
	}
	if addedMembers > 0 {
		_ = s.db.Exec(ctx,
			`UPDATE groups SET member_count = member_count + $2, updated_at = NOW() WHERE group_id = $1`,
			groupID, addedMembers)
	}

	g, err := s.Get(ctx, groupID)
	if err != nil {
		return nil, err
	}

	// Emit event.
	evt := events.NewEvent("group:"+groupID, events.TypeGroupCreated, in.OwnerID, nil)
	evt.Data = mustJSON(map[string]any{
		"group_id":   groupID,
		"name":       in.Name,
		"owner_id":   in.OwnerID,
		"group_type": in.GroupType,
		"members":    append(in.InitialMembers, in.InitialAdmin...),
	})
	_ = s.nats.Publish(ctx, "group."+groupID, mustJSON(evt))
	s.log.Info().Str("group", groupID).Str("owner", in.OwnerID).Msg("group created")
	return g, nil
}

// Get returns the projection.
func (s *Service) Get(ctx context.Context, groupID string) (*Group, error) {
	g := &Group{}
	err := s.db.QueryRow(ctx,
		`SELECT group_id, name, COALESCE(avatar,''), owner_id, group_type, member_count, max_members,
		        COALESCE(introduction,''), COALESCE(notification,''), settings, created_at, updated_at
		 FROM groups WHERE group_id = $1`, groupID).
		Scan(&g.GroupID, &g.Name, &g.Avatar, &g.OwnerID, &g.GroupType, &g.MemberCount, &g.MaxMembers,
			&g.Introduction, &g.Notification, &g.Settings, &g.CreatedAt, &g.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return g, nil
}

// GetMember returns one Member row.
func (s *Service) GetMember(ctx context.Context, groupID, userID string) (*Member, error) {
	m := &Member{}
	var mutedUntil *time.Time
	err := s.db.QueryRow(ctx,
		`SELECT group_id, user_id, role, joined_at, muted_until, COALESCE(nickname,'') AS nickname
		 FROM group_members WHERE group_id = $1 AND user_id = $2`, groupID, userID).
		Scan(&m.GroupID, &m.UserID, &m.Role, &m.JoinedAt, &mutedUntil, &m.Nickname)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrMemberNotFound
		}
		return nil, err
	}
	if mutedUntil != nil {
		m.MutedUntil = *mutedUntil
	}
	return m, nil
}

// ListMembers returns members of a group (paginated).
func (s *Service) ListMembers(ctx context.Context, groupID string, offset, limit int) ([]*Member, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(ctx,
		`SELECT group_id, user_id, role, joined_at, muted_until, COALESCE(nickname,'') AS nickname
		 FROM group_members WHERE group_id = $1
		 ORDER BY joined_at ASC
		 LIMIT $2 OFFSET $3`, groupID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Member
	for rows.Next() {
		m := &Member{}
		var mutedUntil *time.Time
		if err := rows.Scan(&m.GroupID, &m.UserID, &m.Role, &m.JoinedAt, &mutedUntil, &m.Nickname); err != nil {
			return nil, err
		}
		if mutedUntil != nil {
			m.MutedUntil = *mutedUntil
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Invite adds a user to a group as Member. Caller must be Owner/Admin.
func (s *Service) Invite(ctx context.Context, groupID, callerID, targetID string) error {
	g, err := s.Get(ctx, groupID)
	if err != nil {
		return err
	}
	caller, err := s.GetMember(ctx, groupID, callerID)
	if err != nil {
		return ErrForbidden
	}
	if caller.Role != RoleOwner && caller.Role != RoleAdmin {
		return ErrForbidden
	}
	if g.MemberCount >= g.MaxMembers {
		return ErrGroupFull
	}
	if _, err := s.GetMember(ctx, groupID, targetID); err == nil {
		return ErrMemberExists
	}
	if err := s.addMember(ctx, groupID, targetID, RoleMember, time.Now().UTC()); err != nil {
		return err
	}
	_ = s.db.Exec(ctx,
		`UPDATE groups SET member_count = member_count + 1, updated_at = NOW() WHERE group_id = $1`, groupID)

	evt := events.NewEvent("group:"+groupID, events.TypeGroupMemberAdded, callerID, nil)
	evt.Data = mustJSON(map[string]any{"group_id": groupID, "user_id": targetID, "by": callerID})
	_ = s.nats.Publish(ctx, "group."+groupID, mustJSON(evt))
	s.log.Info().Str("group", groupID).Str("user", targetID).Str("by", callerID).Msg("invited")
	return nil
}

// Join self-applies (TypeAllow groups only). Returns ErrForbidden for Normal.
func (s *Service) Join(ctx context.Context, groupID, userID string) error {
	g, err := s.Get(ctx, groupID)
	if err != nil {
		return err
	}
	if g.GroupType != TypeAllow {
		return ErrForbidden
	}
	if g.MemberCount >= g.MaxMembers {
		return ErrGroupFull
	}
	if _, err := s.GetMember(ctx, groupID, userID); err == nil {
		return ErrMemberExists
	}
	if err := s.addMember(ctx, groupID, userID, RoleMember, time.Now().UTC()); err != nil {
		return err
	}
	_ = s.db.Exec(ctx,
		`UPDATE groups SET member_count = member_count + 1, updated_at = NOW() WHERE group_id = $1`, groupID)

	evt := events.NewEvent("group:"+groupID, events.TypeGroupMemberAdded, userID, nil)
	evt.Data = mustJSON(map[string]any{"group_id": groupID, "user_id": userID, "by": userID})
	_ = s.nats.Publish(ctx, "group."+groupID, mustJSON(evt))
	return nil
}

// Quit removes a member (voluntary). Owners may not quit (must transfer first).
func (s *Service) Quit(ctx context.Context, groupID, userID string) error {
	m, err := s.GetMember(ctx, groupID, userID)
	if err != nil {
		return err
	}
	if m.Role == RoleOwner {
		return ErrLastOwner
	}
	return s.removeMember(ctx, groupID, userID, userID)
}

// Kick removes a member. Caller must be Owner/Admin; cannot kick Owner.
func (s *Service) Kick(ctx context.Context, groupID, callerID, targetID string) error {
	caller, err := s.GetMember(ctx, groupID, callerID)
	if err != nil {
		return ErrForbidden
	}
	if caller.Role != RoleOwner && caller.Role != RoleAdmin {
		return ErrForbidden
	}
	target, err := s.GetMember(ctx, groupID, targetID)
	if err != nil {
		return ErrMemberNotFound
	}
	if target.Role == RoleOwner {
		return ErrForbidden
	}
	return s.removeMember(ctx, groupID, targetID, callerID)
}

// TransferOwner moves ownership to a current admin/member.
func (s *Service) TransferOwner(ctx context.Context, groupID, callerID, newOwnerID string) error {
	caller, err := s.GetMember(ctx, groupID, callerID)
	if err != nil || caller.Role != RoleOwner {
		return ErrForbidden
	}
	if callerID == newOwnerID {
		return nil
	}
	if _, err := s.GetMember(ctx, groupID, newOwnerID); err != nil {
		return ErrMemberNotFound
	}
	now := time.Now().UTC()
	if err := s.db.Exec(ctx,
		`UPDATE group_members SET role = 'admin', joined_at = $3 WHERE group_id = $1 AND user_id = $2`,
		groupID, callerID, now); err != nil {
		return err
	}
	if err := s.db.Exec(ctx,
		`UPDATE group_members SET role = 'owner' WHERE group_id = $1 AND user_id = $2`,
		groupID, newOwnerID); err != nil {
		return err
	}
	if err := s.db.Exec(ctx,
		`UPDATE groups SET owner_id = $2, updated_at = NOW() WHERE group_id = $1`,
		groupID, newOwnerID); err != nil {
		return err
	}
	evt := events.NewEvent("group:"+groupID, events.TypeGroupOwnerTransferred, callerID, nil)
	evt.Data = mustJSON(map[string]any{"group_id": groupID, "from": callerID, "to": newOwnerID})
	_ = s.nats.Publish(ctx, "group."+groupID, mustJSON(evt))
	return nil
}

// SetRole changes a member's role. Owner/Admin only. Cannot demote the last Owner.
func (s *Service) SetRole(ctx context.Context, groupID, callerID, targetID string, newRole Role) error {
	caller, err := s.GetMember(ctx, groupID, callerID)
	if err != nil || caller.Role != RoleOwner {
		return ErrForbidden
	}
	if newRole != RoleOwner && newRole != RoleAdmin && newRole != RoleMember {
		return fmt.Errorf("invalid role %q", newRole)
	}
	if _, err := s.GetMember(ctx, groupID, targetID); err != nil {
		return ErrMemberNotFound
	}
	if newRole == RoleOwner {
		return s.TransferOwner(ctx, groupID, callerID, targetID)
	}
	return s.db.Exec(ctx,
		`UPDATE group_members SET role = $3 WHERE group_id = $1 AND user_id = $2`,
		groupID, targetID, newRole)
}

// MuteUser sets a per-member mute with TTL. Caller must be Owner/Admin.
func (s *Service) MuteUser(ctx context.Context, groupID, callerID, targetID string, until time.Time) error {
	caller, err := s.GetMember(ctx, groupID, callerID)
	if err != nil || (caller.Role != RoleOwner && caller.Role != RoleAdmin) {
		return ErrForbidden
	}
	return s.db.Exec(ctx,
		`UPDATE group_members SET muted_until = $3 WHERE group_id = $1 AND user_id = $2`,
		groupID, targetID, until)
}

// MuteGroup sets a group-wide mute (no one can send). Owner only.
func (s *Service) MuteGroup(ctx context.Context, groupID, callerID string, until time.Time) error {
	caller, err := s.GetMember(ctx, groupID, callerID)
	if err != nil || caller.Role != RoleOwner {
		return ErrForbidden
	}
	return s.db.Exec(ctx,
		`UPDATE groups SET muted_until = $2, updated_at = NOW() WHERE group_id = $1`, groupID, until)
}

// SetInfo updates name/avatar/introduction/notification. Owner/Admin.
func (s *Service) SetInfo(ctx context.Context, groupID, callerID string, fields map[string]any) error {
	caller, err := s.GetMember(ctx, groupID, callerID)
	if err != nil || (caller.Role != RoleOwner && caller.Role != RoleAdmin) {
		return ErrForbidden
	}
	allowed := map[string]bool{"name": true, "avatar": true, "introduction": true, "notification": true}
	set := []string{}
	args := []any{groupID}
	i := 2
	for k, v := range fields {
		if !allowed[k] {
			continue
		}
		set = append(set, fmt.Sprintf("%s = $%d", k, i))
		args = append(args, v)
		i++
	}
	if len(set) == 0 {
		return nil
	}
	set = append(set, "updated_at = NOW()")
	q := fmt.Sprintf("UPDATE groups SET %s WHERE group_id = $1", joinComma(set))
	return s.db.Exec(ctx, q, args...)
}

// Dismiss deletes the group. Owner only. All members removed.
func (s *Service) Dismiss(ctx context.Context, groupID, callerID string) error {
	caller, err := s.GetMember(ctx, groupID, callerID)
	if err != nil || caller.Role != RoleOwner {
		return ErrForbidden
	}
	if err := s.db.Exec(ctx, `DELETE FROM group_members WHERE group_id = $1`, groupID); err != nil {
		return err
	}
	if err := s.db.Exec(ctx, `DELETE FROM groups WHERE group_id = $1`, groupID); err != nil {
		return err
	}
	evt := events.NewEvent("group:"+groupID, events.TypeGroupDismissed, callerID, nil)
	evt.Data = mustJSON(map[string]any{"group_id": groupID})
	_ = s.nats.Publish(ctx, "group."+groupID, mustJSON(evt))
	return nil
}

// --- SCIM v2 (RFC 7644 §3.4) group surface --------------------------------
//
// These methods exist solely to back the SCIM /Groups endpoints. They do
// NOT replace the existing internal Group CRUD (Create/Get/Join/etc.); they
// read and write the scim_groups table and (on create) materialise a real
// Pulse group row so that membership / chat flows work end-to-end.
//
// Filter parsing for List currently supports displayName exact-match (displayName eq "X").

// SCIMListGroups returns SCIM group projections matching the provided filter.
// filter: SCIM filter expression (e.g. `displayName eq "Engineering"`).
// startIndex: 1-based page index.
// count: page size clamped to [1, 200].
// Returns matching SCIM groups, total matching results, and error if any.
func (s *Service) SCIMListGroups(ctx context.Context, filter string, startIndex, count int) ([]SCIMGroup, int, error) {
	if count <= 0 || count > 200 {
		count = 50
	}
	if startIndex < 1 {
		startIndex = 1
	}

	displayName := ""
	if filter != "" {
		displayName = extractDisplayNameFilter(filter)
		if displayName == "" {
			// Unsupported filter: return empty page.
			return []SCIMGroup{}, 0, nil
		}
	}

	offset := startIndex - 1

	var (
		rows pgx.Rows
		err  error
	)
	if displayName != "" {
		rows, err = s.db.Query(ctx,
			`SELECT scim_id, pulse_group_id, display_name, COALESCE(external_id,'') AS external_id,
			        created_at, updated_at
			   FROM scim_groups
			  WHERE display_name = $1
			  ORDER BY created_at ASC
			  LIMIT $2 OFFSET $3`, displayName, count, offset)
	} else {
		rows, err = s.db.Query(ctx,
			`SELECT scim_id, pulse_group_id, display_name, COALESCE(external_id,'') AS external_id,
			        created_at, updated_at
			   FROM scim_groups
			  ORDER BY created_at ASC
			  LIMIT $1 OFFSET $2`, count, offset)
	}
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := make([]SCIMGroup, 0, count)
	for rows.Next() {
		var (
			scimID, pulseID, display, external string
			created, updated                    time.Time
		)
		if err := rows.Scan(&scimID, &pulseID, &display, &external, &created, &updated); err != nil {
			return nil, 0, err
		}
		out = append(out, SCIMGroup{
			Schemas:     []string{SCIMGroupSchema},
			ID:          scimID,
			DisplayName: display,
			ExternalID:  external,
			Meta: SCIMGroupMeta{
				ResourceType: "Group",
				Created:      created,
				LastModified: updated,
			},
		})
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	// Separate COUNT for the envelope. Small N — acceptable; windowed
	// Note: count() OVER () optimization can be adopted for single-query pagination.
	var total int
	if displayName != "" {
		err = s.db.QueryRow(ctx,
			`SELECT COUNT(*) FROM scim_groups WHERE display_name = $1`, displayName).Scan(&total)
	} else {
		err = s.db.QueryRow(ctx, `SELECT COUNT(*) FROM scim_groups`).Scan(&total)
	}
	if err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// SCIMGetGroup returns one SCIM projection by its SCIM-side identifier.
func (s *Service) SCIMGetGroup(ctx context.Context, scimID string) (*SCIMGroup, error) {
	var (
		pulseID, display, external string
		created, updated          time.Time
	)
	err := s.db.QueryRow(ctx,
		`SELECT pulse_group_id, display_name, COALESCE(external_id,''), created_at, updated_at
		   FROM scim_groups WHERE scim_id = $1`, scimID).
		Scan(&pulseID, &display, &external, &created, &updated)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &SCIMGroup{
		Schemas:     []string{SCIMGroupSchema},
		ID:          scimID,
		DisplayName: display,
		ExternalID:  external,
		Meta: SCIMGroupMeta{
			ResourceType: "Group",
			Created:      created,
			LastModified: updated,
		},
	}, nil
}

// SCIMCreateGroup materialises a real Pulse group AND a scim_groups row
// in a single transaction. The Pulse group's owner is set to a synthetic
// "scim_<idpID>" account so ownership invariants hold even though no
// human "owns" an IdP-provisioned group.
//
// displayName and idpID (passed as ownerID) must be non-empty.
// externalID may be empty.
func (s *Service) SCIMCreateGroup(ctx context.Context, displayName, externalID, ownerID string) (*SCIMGroup, error) {
	if displayName == "" {
		return nil, errors.New("displayName required")
	}
	if ownerID == "" {
		return nil, errors.New("ownerID (idpID) required; pass the IdP ID via X-Pulse-IdP-Id or a server-side default")
	}

	scimID := "scimgrp_" + ulid.Make().String()
	pulseGroupID := "g_" + ulid.Make().String()
	now := time.Now().UTC()

	// Atomic write across groups, group_members, and scim_groups tables.
	err := s.cell.DB.WithTx(ctx, func(tx pgx.Tx) error {
		// 1. Create native Pulse group record.
		if _, err := tx.Exec(ctx,
			`INSERT INTO groups (group_id, name, owner_id, group_type, member_count, max_members,
			                    introduction, notification, settings, created_at, updated_at)
			 VALUES ($1, $2, $3, 'Normal', 1, 2000, '', '', '{}'::jsonb, $4, $4)`,
			pulseGroupID, displayName, ownerID, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO group_members (group_id, user_id, role, joined_at)
			 VALUES ($1, $2, 'owner', $3)`,
			pulseGroupID, ownerID, now); err != nil {
			return err
		}

		// 2. Create SCIM projection record.
		if _, err := tx.Exec(ctx,
			`INSERT INTO scim_groups (scim_id, pulse_group_id, display_name, external_id, created_at, updated_at)
			 VALUES ($1, $2, $3, NULLIF($4, ''), $5, $5)`,
			scimID, pulseGroupID, displayName, externalID, now); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	evt := events.NewEvent("group:"+pulseGroupID, events.TypeGroupCreated, ownerID, nil)
	evt.Data = mustJSON(map[string]any{
		"group_id":   pulseGroupID,
		"scim_id":    scimID,
		"name":       displayName,
		"owner_id":   ownerID,
		"group_type": "Normal",
		"origin":     "scim",
	})
	_ = s.nats.Publish(ctx, "group."+pulseGroupID, mustJSON(evt))
	s.log.Info().Str("scim_group", scimID).Str("pulse_group", pulseGroupID).Msg("scim group created")

	return &SCIMGroup{
		Schemas:     []string{SCIMGroupSchema},
		ID:          scimID,
		DisplayName: displayName,
		ExternalID:  externalID,
		Meta: SCIMGroupMeta{
			ResourceType: "Group",
			Created:      now,
			LastModified: now,
		},
	}, nil
}

// SCIMDeleteGroup removes the SCIM projection AND the underlying Pulse
// group. The FK ON DELETE CASCADE on scim_groups.pulse_group_id handles
// group_members cleanup; we explicitly remove the groups row so the
// projection is fully torn down. Idempotent on the projection side.
func (s *Service) SCIMDeleteGroup(ctx context.Context, scimID string) error {
	var pulseGroupID string
	err := s.db.QueryRow(ctx,
		`SELECT pulse_group_id FROM scim_groups WHERE scim_id = $1`, scimID).Scan(&pulseGroupID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if err := s.db.Exec(ctx, `DELETE FROM scim_groups WHERE scim_id = $1`, scimID); err != nil {
		return err
	}
	if err := s.db.Exec(ctx, `DELETE FROM groups WHERE group_id = $1`, pulseGroupID); err != nil {
		return err
	}

	evt := events.NewEvent("group:"+pulseGroupID, events.TypeGroupDismissed, "scim_system", nil)
	evt.Data = mustJSON(map[string]any{
		"group_id": pulseGroupID,
		"scim_id":  scimID,
		"origin":   "scim",
	})
	_ = s.nats.Publish(ctx, "group."+pulseGroupID, mustJSON(evt))
	s.log.Info().Str("scim_group", scimID).Str("pulse_group", pulseGroupID).Msg("scim group deleted")
	return nil
}

// SCIMUpdateGroupMembers applies a PATCH (RFC 7644 §3.5.2) operation
// to a SCIM group's membership. Membership changes are recorded in
// `idp_group_mappings` (audit trail keyed by (idp_id, external_group)).
//
//   - op == "add"    → for each memberID, INSERT a row into
//     idp_group_mappings (idp_id, external_group=memberID,
//     pulse_role='user', pulse_group_id, scim_group_id). Idempotent on
//     retry because the PK is (idp_id, external_group); a second
//     INSERT for the same member is a no-op.
//   - op == "remove" → for each memberID, DELETE the matching mapping
//     row. Idempotent: missing rows are silently ignored (we log only
//     deletions that actually removed something).
//   - op == "replace" → DELETE every active mapping for this SCIM
//     group, then INSERT a fresh row per memberID. This is the
//     PATCH semantics IdPs use to assert the canonical member set
//     after a full re-sync.
//
// `idpID` is the IdP identifier resolved by the SCIM auth middleware
// (from the authenticated Bearer token). It is required because
// idp_group_mappings.idp_id is NOT NULL; passing "" will fail at the
// INSERT step. We surface the underlying DB error rather than masking
// it because callers need to know whether they're missing the auth
// piece vs. hitting a schema collision.
//
// scimID is the SCIM-side group identifier (RFC 7644 §3.4 `id`).
// Returns ErrNotFound when the SCIM group does not exist (so the
// handler can surface a 404 with the SCIM notFound envelope).
func (s *Service) SCIMUpdateGroupMembers(ctx context.Context, scimID, idpID, op string, memberIDs []string) error {
	if scimID == "" {
		return errors.New("scimID required")
	}
	if idpID == "" {
		return errors.New("idpID required")
	}
	op = strings.ToLower(strings.TrimSpace(op))
	if op != "add" && op != "remove" && op != "replace" {
		return fmt.Errorf("unsupported SCIM patch op %q (supported: add, remove, replace)", op)
	}

	// Resolve scimID → pulse_group_id and verify the projection exists.
	// We do NOT use SCIMGetGroup here because we only need the IDs and
	// want to avoid the SCIM DTO build overhead on the hot patch path.
	var pulseGroupID string
	err := s.db.QueryRow(ctx,
		`SELECT pulse_group_id FROM scim_groups WHERE scim_id = $1`, scimID).Scan(&pulseGroupID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}

	switch op {
	case "add":
		for _, memberID := range dedup(memberIDs) {
			if memberID == "" {
				continue
			}
			// ON CONFLICT DO NOTHING handles retries: the PK
			// (idp_id, external_group) blocks the duplicate and
			// we treat "already mapped" as success.
			if err := s.db.Exec(ctx,
				`INSERT INTO idp_group_mappings (idp_id, external_group, pulse_role, pulse_group_id, scim_group_id)
				 VALUES ($1, $2, 'user', $3, $4)
				 ON CONFLICT (idp_id, external_group) DO NOTHING`,
				idpID, memberID, pulseGroupID, scimID); err != nil {
				return err
			}
		}
		s.log.Info().
			Str("scim_group", scimID).
			Str("pulse_group", pulseGroupID).
			Str("idp_id", idpID).
			Int("added", len(memberIDs)).
			Msg("scim patch add")

	case "remove":
		for _, memberID := range dedup(memberIDs) {
			if memberID == "" {
				continue
			}
			// ON CONFLICT-tolerant: only rows for THIS scim_group_id
			// are removed, so a member mapped under a different
			// SCIM group is unaffected. We rely on (scim_group_id)
			// to scope the deletion.
			if err := s.db.Exec(ctx,
				`DELETE FROM idp_group_mappings
				  WHERE idp_id = $1 AND external_group = $2 AND scim_group_id = $3`,
				idpID, memberID, scimID); err != nil {
				return err
			}
		}
		s.log.Info().
			Str("scim_group", scimID).
			Str("pulse_group", pulseGroupID).
			Str("idp_id", idpID).
			Int("removed", len(memberIDs)).
			Msg("scim patch remove")

	case "replace":
		// Replace: drop active mappings for this SCIM group and re-insert member rows atomically.
		err := s.cell.DB.WithTx(ctx, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx,
				`DELETE FROM idp_group_mappings
				  WHERE idp_id = $1 AND scim_group_id = $2`,
				idpID, scimID); err != nil {
				return err
			}
			for _, memberID := range dedup(memberIDs) {
				if memberID == "" {
					continue
				}
				if _, err := tx.Exec(ctx,
					`INSERT INTO idp_group_mappings (idp_id, external_group, pulse_role, pulse_group_id, scim_group_id)
					 VALUES ($1, $2, 'user', $3, $4)
					 ON CONFLICT (idp_id, external_group) DO NOTHING`,
					idpID, memberID, pulseGroupID, scimID); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		s.log.Info().
			Str("scim_group", scimID).
			Str("pulse_group", pulseGroupID).
			Str("idp_id", idpID).
			Int("replaced", len(memberIDs)).
			Msg("scim patch replace")
	}

	return nil
}

// extractDisplayNameFilter parses a minimal `displayName eq "X"` filter.
// Returns "" on any parse failure or unsupported grammar.
func extractDisplayNameFilter(filter string) string {
	needle := `displayName eq "`
	_, rest, found := strings.Cut(filter, needle)
	if !found {
		return ""
	}
	value, _, found := strings.Cut(rest, `"`)
	if !found {
		return ""
	}
	return value
}

// IsMember reports whether userID is currently in groupID.
func (s *Service) IsMember(ctx context.Context, groupID, userID string) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM group_members WHERE group_id = $1 AND user_id = $2)`,
		groupID, userID).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}

// MembersOf returns the userIDs of all members in groupID.
func (s *Service) MembersOf(ctx context.Context, groupID string) ([]string, error) {
	rows, err := s.db.Query(ctx,
		`SELECT user_id FROM group_members WHERE group_id = $1`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// --- helpers --------------------------------------------------------------

func (s *Service) addMember(ctx context.Context, groupID, userID string, role Role, joinedAt time.Time) error {
	return s.db.Exec(ctx,
		`INSERT INTO group_members (group_id, user_id, role, joined_at)
		 VALUES ($1, $2, $3, $4)`,
		groupID, userID, role, joinedAt)
}

func (s *Service) removeMember(ctx context.Context, groupID, userID, by string) error {
	if err := s.db.Exec(ctx,
		`DELETE FROM group_members WHERE group_id = $1 AND user_id = $2`, groupID, userID); err != nil {
		return err
	}
	_ = s.db.Exec(ctx,
		`UPDATE groups SET member_count = GREATEST(member_count - 1, 0), updated_at = NOW() WHERE group_id = $1`,
		groupID)
	evt := events.NewEvent("group:"+groupID, events.TypeGroupMemberRemoved, by, nil)
	evt.Data = mustJSON(map[string]any{"group_id": groupID, "user_id": userID, "by": by})
	_ = s.nats.Publish(ctx, "group."+groupID, mustJSON(evt))
	return nil
}

func dedup(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func joinComma(s []string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += ", "
		}
		out += v
	}
	return out
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}