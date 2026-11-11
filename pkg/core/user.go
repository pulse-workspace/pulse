package core

import (
	"context"
	"time"
)

// User represents the public user entity in Pulse Core.
type User struct {
	UserID    string         `json:"userID"`
	Nickname  string         `json:"nickname"`
	AvatarURL string         `json:"avatarURL"`
	Email     string         `json:"email,omitempty"`
	Phone     string         `json:"phone,omitempty"`
	Status    string         `json:"status"`
	Ext       map[string]any `json:"ext,omitempty"`
	CreatedAt time.Time      `json:"createdAt"`
	UpdatedAt time.Time      `json:"updatedAt"`
}

var (
	ErrNotFound = errorsNew("resource not found")
	ErrConflict = errorsNew("resource conflict")
)

func errorsNew(s string) error {
	return ErrResourceError(s)
}

type ErrResourceError string

func (e ErrResourceError) Error() string { return string(e) }

// UserManager defines the contract for managing user accounts.
type UserManager interface {
	GetUser(ctx context.Context, userID string) (*User, error)
	SearchUsers(ctx context.Context, keyword string, limit int) ([]*User, error)
	Register(ctx context.Context, userID, nickname, email, phone string, ext map[string]any) (*User, error)
	DeleteUser(ctx context.Context, userID string) error
	SetMFARequired(ctx context.Context, userID string, required bool) error
	GetMFARequired(ctx context.Context, userID string) (bool, error)
}

