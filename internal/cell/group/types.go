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
	"errors"
	"time"
)

// GroupType is the public group joinability enum.
type GroupType string

const (
	TypeNormal GroupType = "Normal" // invite-only
	TypeAllow  GroupType = "Allow"  // joinable via link / apply
)

// Role is the per-member role inside a group.
type Role string

const (
	RoleOwner  Role = "owner"
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
)

// Group is the projection for a group.
type Group struct {
	GroupID      string         `json:"group_id"`
	Name         string         `json:"name"`
	Avatar       string         `json:"avatar,omitempty"`
	OwnerID      string         `json:"owner_id"`
	GroupType    GroupType      `json:"group_type"`
	MemberCount  int            `json:"member_count"`
	MaxMembers   int            `json:"max_members"`
	Introduction string         `json:"introduction,omitempty"`
	Notification string         `json:"notification,omitempty"`
	MutedUntil   time.Time      `json:"muted_until,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	Settings     map[string]any `json:"settings,omitempty"`
}

// Member is a row in the group_members projection.
type Member struct {
	GroupID    string    `json:"group_id"`
	UserID     string    `json:"user_id"`
	Role       Role      `json:"role"`
	JoinedAt   time.Time `json:"joined_at"`
	MutedUntil time.Time `json:"muted_until,omitempty"`
	Nickname   string    `json:"nickname,omitempty"`
}

// Sentinel errors.
var (
	ErrNotFound       = errors.New("group not found")
	ErrMemberExists   = errors.New("user already a member")
	ErrMemberNotFound = errors.New("member not found")
	ErrForbidden      = errors.New("forbidden")
	ErrGroupFull      = errors.New("group full")
	ErrLastOwner      = errors.New("cannot remove last owner")
)

// CreateInput is the parameter for Service.Create.
type CreateInput struct {
	GroupID        string
	OwnerID        string
	Name           string
	GroupType      GroupType
	Avatar         string
	Introduction   string
	Notification   string
	MaxMembers     int
	Settings       map[string]any
	InitialAdmin   []string
	InitialMembers []string
}
