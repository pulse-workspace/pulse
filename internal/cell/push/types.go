// Package push manages mobile device token registration, presence-aware push dispatch,
// and silent push notifications for cross-device notification dismissal.
package push

import (
	"context"
	"errors"
	"strings"
	"time"
)

// Supported mobile and web push platforms.
const (
	PlatformFCM     = "fcm"
	PlatformAPNs    = "apns"
	PlatformWebPush = "webpush"
)

// Common silent push action identifiers.
const (
	ActionDismissNotification = "dismiss_notification"
	ActionSyncMessages        = "sync_messages"
)

// DeviceToken represents a registered push token bound to an authenticated user.
type DeviceToken struct {
	ID          string    `json:"id" db:"id"`
	UserID      string    `json:"user_id" db:"user_id"`
	Platform    string    `json:"platform" db:"platform"`
	Token       string    `json:"token" db:"token"`
	AppBundleID string    `json:"app_bundle_id" db:"app_bundle_id"`
	IsActive    bool      `json:"is_active" db:"is_active"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time `json:"updated_at" db:"updated_at"`
}

// Validate ensures device token registration parameters meet required criteria without fallback.
func (d *DeviceToken) Validate() error {
	if strings.TrimSpace(d.UserID) == "" {
		return errors.New("user_id cannot be blank")
	}
	platform := strings.ToLower(strings.TrimSpace(d.Platform))
	if platform != PlatformFCM && platform != PlatformAPNs && platform != PlatformWebPush {
		return errors.New("platform must be one of: fcm, apns, webpush")
	}
	if strings.TrimSpace(d.Token) == "" {
		return errors.New("token cannot be blank")
	}
	if strings.TrimSpace(d.AppBundleID) == "" {
		return errors.New("app_bundle_id cannot be blank")
	}
	return nil
}

// PushNotification carries the payload for loud foreground/lockscreen notifications.
type PushNotification struct {
	RecipientID string            `json:"recipient_id"`
	SenderID    string            `json:"sender_id"`
	SenderName  string            `json:"sender_name"`
	Title       string            `json:"title"`
	Body        string            `json:"body"`
	ConvID      string            `json:"conv_id"`
	MessageID   string            `json:"message_id"`
	Badge       int               `json:"badge"`
	Data        map[string]string `json:"data,omitempty"`
}

// SilentPush carries background synchronization instructions without alerting the user visually or audibly.
type SilentPush struct {
	RecipientID       string            `json:"recipient_id"`
	Action            string            `json:"action"`
	ConvID            string            `json:"conv_id"`
	ReadSeq           int64             `json:"read_seq"`
	TargetMessageIDs  []string          `json:"target_message_ids,omitempty"`
	Data              map[string]string `json:"data,omitempty"`
}

// Driver abstracts delivery through external push providers (Firebase, Apple, WebPush).
type Driver interface {
	// Platform returns the supported platform identifier.
	Platform() string
	// Send delivers a visible push notification to a registered token.
	Send(ctx context.Context, token string, notification *PushNotification) error
	// SendSilent delivers a silent background sync or dismissal payload to a registered token.
	SendSilent(ctx context.Context, token string, silent *SilentPush) error
}
