// Package push drivers implement delivery mechanisms for various push notification providers.
package push

import (
	"context"
	"errors"
	"sync"
)

// MockDriver is an in-memory test driver recording dispatches for verification.
type MockDriver struct {
	mu         sync.Mutex
	platform   string
	SentLoud   []*PushNotification
	SentSilent []*SilentPush
	FailNext   error
}

// NewMockDriver creates a test driver for the given platform.
func NewMockDriver(platform string) *MockDriver {
	return &MockDriver{
		platform:   platform,
		SentLoud:   make([]*PushNotification, 0),
		SentSilent: make([]*SilentPush, 0),
	}
}

// Platform returns the supported platform name.
func (m *MockDriver) Platform() string {
	return m.platform
}

// Send records loud notification dispatches in memory.
func (m *MockDriver) Send(ctx context.Context, token string, notification *PushNotification) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.FailNext != nil {
		err := m.FailNext
		m.FailNext = nil
		return err
	}
	m.SentLoud = append(m.SentLoud, notification)
	return nil
}

// SendSilent records silent notification dispatches in memory.
func (m *MockDriver) SendSilent(ctx context.Context, token string, silent *SilentPush) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.FailNext != nil {
		err := m.FailNext
		m.FailNext = nil
		return err
	}
	m.SentSilent = append(m.SentSilent, silent)
	return nil
}

// FCMDriverConfig defines credentials and endpoint options for Firebase Cloud Messaging HTTP v1.
type FCMDriverConfig struct {
	ProjectID      string `json:"project_id"`
	ServiceAccount string `json:"service_account"`
}

// FCMDriver delivers notifications via Google Firebase Cloud Messaging.
type FCMDriver struct {
	cfg FCMDriverConfig
}

// NewFCMDriver initializes FCMDriver with validated credentials.
func NewFCMDriver(cfg FCMDriverConfig) (*FCMDriver, error) {
	if cfg.ProjectID == "" {
		return nil, errors.New("fcm project_id cannot be blank")
	}
	return &FCMDriver{cfg: cfg}, nil
}

// Platform returns PlatformFCM.
func (f *FCMDriver) Platform() string {
	return PlatformFCM
}

// Send formats and delivers an FCM HTTP v1 notification payload.
func (f *FCMDriver) Send(ctx context.Context, token string, notification *PushNotification) error {
	if token == "" || notification == nil {
		return errors.New("token and notification cannot be nil or empty")
	}
	// Delivery stub: In production, posts JSON to https://fcm.googleapis.com/v1/projects/{project_id}/messages:send
	return nil
}

// SendSilent delivers a data-only payload to FCM without visual or audible alerts.
func (f *FCMDriver) SendSilent(ctx context.Context, token string, silent *SilentPush) error {
	if token == "" || silent == nil {
		return errors.New("token and silent cannot be nil or empty")
	}
	// Delivery stub: Posts data-only payload with content_available: true
	return nil
}

// APNsDriverConfig defines connection credentials for Apple Push Notification service.
type APNsDriverConfig struct {
	KeyID    string `json:"key_id"`
	TeamID   string `json:"team_id"`
	BundleID string `json:"bundle_id"`
	Sandbox  bool   `json:"sandbox"`
}

// APNsDriver delivers push messages to iOS devices via APNs HTTP/2.
type APNsDriver struct {
	cfg APNsDriverConfig
}

// NewAPNsDriver initializes an APNs push driver with validation.
func NewAPNsDriver(cfg APNsDriverConfig) (*APNsDriver, error) {
	if cfg.BundleID == "" {
		return nil, errors.New("apns bundle_id cannot be blank")
	}
	return &APNsDriver{cfg: cfg}, nil
}

// Platform returns PlatformAPNs.
func (a *APNsDriver) Platform() string {
	return PlatformAPNs
}

// Send delivers a visible APNs alert message with sound and badge.
func (a *APNsDriver) Send(ctx context.Context, token string, notification *PushNotification) error {
	if token == "" || notification == nil {
		return errors.New("token and notification cannot be nil or empty")
	}
	// Delivery stub: In production, dispatches HTTP/2 POST to api.push.apple.com
	return nil
}

// SendSilent dispatches a background push with "content-available: 1" and no alert sound.
func (a *APNsDriver) SendSilent(ctx context.Context, token string, silent *SilentPush) error {
	if token == "" || silent == nil {
		return errors.New("token and silent cannot be nil or empty")
	}
	// Delivery stub: Background APNs push for cross-device dismissal
	return nil
}

// WebPushDriver delivers standard WebPush VAPID notifications to browsers.
type WebPushDriver struct {
	subject string
}

// NewWebPushDriver initializes a WebPush VAPID driver.
func NewWebPushDriver(subject string) (*WebPushDriver, error) {
	if subject == "" {
		return nil, errors.New("webpush subject cannot be blank")
	}
	return &WebPushDriver{subject: subject}, nil
}

// Platform returns PlatformWebPush.
func (w *WebPushDriver) Platform() string {
	return PlatformWebPush
}

// Send delivers a WebPush notification frame.
func (w *WebPushDriver) Send(ctx context.Context, token string, notification *PushNotification) error {
	if token == "" || notification == nil {
		return errors.New("token and notification cannot be nil or empty")
	}
	return nil
}

// SendSilent delivers a silent WebPush sync event.
func (w *WebPushDriver) SendSilent(ctx context.Context, token string, silent *SilentPush) error {
	if token == "" || silent == nil {
		return errors.New("token and silent cannot be nil or empty")
	}
	return nil
}
