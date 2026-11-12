package push

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pulse-workspace/pulse/internal/cell"
	"github.com/pulse-workspace/pulse/internal/cell/presence"
	"github.com/pulse-workspace/pulse/pkg/shared/config"
	"github.com/pulse-workspace/pulse/pkg/shared/logger"
)

// fakePresence implements PresenceChecker for testing.
type fakePresence struct {
	entries map[string]*presence.Entry
}

func (f *fakePresence) Get(userID string) (*presence.Entry, bool) {
	e, ok := f.entries[userID]
	return e, ok
}

// cannedRows provides a stub implementation of pgx.Rows for tests.
type cannedRows struct {
	rows [][]any
	pos  int
}

func (r *cannedRows) Close() {}
func (r *cannedRows) Err() error { return nil }
func (r *cannedRows) CommandTag() pgconn.CommandTag { return pgconn.CommandTag{} }
func (r *cannedRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *cannedRows) Values() ([]any, error) { return nil, nil }
func (r *cannedRows) RawValues() [][]byte { return nil }
func (r *cannedRows) Conn() *pgx.Conn { return nil }

func (r *cannedRows) Next() bool {
	r.pos++
	return r.pos <= len(r.rows)
}

func (r *cannedRows) Scan(dest ...any) error {
	if r.pos < 1 || r.pos > len(r.rows) {
		return errors.New("scan past end")
	}
	row := r.rows[r.pos-1]
	for i, v := range row {
		switch d := dest[i].(type) {
		case *string:
			if s, ok := v.(string); ok {
				*d = s
			}
		case *bool:
			if b, ok := v.(bool); ok {
				*d = b
			}
		case *time.Time:
			if t, ok := v.(time.Time); ok {
				*d = t
			}
		}
	}
	return nil
}

// fakeDBTX implements db.DBTX in-memory for testing repository operations.
type fakeDBTX struct {
	execFn  func(ctx context.Context, sql string, args ...any) error
	queryFn func(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func (f *fakeDBTX) Exec(ctx context.Context, sql string, args ...any) error {
	if f.execFn != nil {
		return f.execFn(ctx, sql, args...)
	}
	return nil
}

func (f *fakeDBTX) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if f.queryFn != nil {
		return f.queryFn(ctx, sql, args...)
	}
	return nil, nil
}

func (f *fakeDBTX) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return nil
}

func createTestCell() *cell.Cell {
	cfg := &config.Config{}
	cfg.Cell.ID = "cell-test"
	log := logger.New(logger.Config{Level: "debug"})
	return cell.New(cfg, log, nil, nil, nil)
}

func TestDeviceTokenValidation(t *testing.T) {
	valid := &DeviceToken{
		UserID:      "usr_123",
		Platform:    "fcm",
		Token:       "tok_abc",
		AppBundleID: "com.pulse.im",
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("expected valid token, got error: %v", err)
	}

	invalidPlatform := &DeviceToken{
		UserID:      "usr_123",
		Platform:    "invalid",
		Token:       "tok_abc",
		AppBundleID: "com.pulse.im",
	}
	if err := invalidPlatform.Validate(); err == nil {
		t.Fatal("expected error on invalid platform, got nil")
	}

	missingToken := &DeviceToken{
		UserID:      "usr_123",
		Platform:    "apns",
		Token:       "",
		AppBundleID: "com.pulse.im",
	}
	if err := missingToken.Validate(); err == nil {
		t.Fatal("expected error on missing token, got nil")
	}
}

func TestPresenceAwarePushSuppression(t *testing.T) {
	c := createTestCell()
	db := &fakeDBTX{}
	repo := NewRepository(db)

	pres := &fakePresence{
		entries: map[string]*presence.Entry{
			"usr_alice": {
				UserID: "usr_alice",
				Status: presence.StatusOnline,
			},
			"usr_bob": {
				UserID: "usr_bob",
				Status: presence.StatusOffline,
			},
		},
	}

	svc := NewService(c, repo, pres)
	fcmMock := NewMockDriver(PlatformFCM)
	svc.RegisterDriver(fcmMock)

	ctx := context.Background()

	// 1. Dispatch to Alice who is ONLINE -> Should be SUPPRESSED
	err := svc.DispatchMessage(ctx, &PushNotification{
		RecipientID: "usr_alice",
		Title:       "New Message",
		Body:        "Hello Alice!",
		ConvID:      "conv_1",
	})
	if err != nil {
		t.Fatalf("unexpected dispatch error: %v", err)
	}
	if len(fcmMock.SentLoud) != 0 {
		t.Fatalf("expected loud push to be suppressed for online user, but got %d sent", len(fcmMock.SentLoud))
	}
}

func TestLoudPushDispatchedToOfflineUser(t *testing.T) {
	c := createTestCell()
	now := time.Now().UTC()
	db := &fakeDBTX{
		queryFn: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
			return &cannedRows{
				rows: [][]any{
					{"devtok_1", "usr_bob", "fcm", "fcm_token_bob_phone", "com.pulse.im", true, now, now},
				},
			}, nil
		},
	}
	repo := NewRepository(db)

	pres := &fakePresence{
		entries: map[string]*presence.Entry{
			"usr_bob": {
				UserID: "usr_bob",
				Status: presence.StatusOffline,
			},
		},
	}

	svc := NewService(c, repo, pres)
	fcmMock := NewMockDriver(PlatformFCM)
	svc.RegisterDriver(fcmMock)

	ctx := context.Background()

	err := svc.DispatchMessage(ctx, &PushNotification{
		RecipientID: "usr_bob",
		Title:       "New Message",
		Body:        "Hey Bob, are you free?",
		ConvID:      "conv_bob",
	})
	if err != nil {
		t.Fatalf("unexpected dispatch error: %v", err)
	}
	if len(fcmMock.SentLoud) != 1 {
		t.Fatalf("expected 1 loud notification dispatched to offline user, got %d", len(fcmMock.SentLoud))
	}
	if fcmMock.SentLoud[0].RecipientID != "usr_bob" {
		t.Errorf("expected recipient usr_bob, got %s", fcmMock.SentLoud[0].RecipientID)
	}
}

func TestSilentReadDismissal(t *testing.T) {
	c := createTestCell()
	now := time.Now().UTC()
	db := &fakeDBTX{
		queryFn: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
			return &cannedRows{
				rows: [][]any{
					{"devtok_desktop", "usr_bob", "apns", "apns_desktop_token", "com.pulse.desktop", true, now, now},
					{"devtok_mobile", "usr_bob", "apns", "apns_mobile_token", "com.pulse.mobile", true, now, now},
				},
			}, nil
		},
	}
	repo := NewRepository(db)

	svc := NewService(c, repo, nil)
	apnsMock := NewMockDriver(PlatformAPNs)
	svc.RegisterDriver(apnsMock)

	ctx := context.Background()

	// Bob reads on desktop, so dismiss notifications on mobile!
	err := svc.DispatchSilentReadDismissal(ctx, "usr_bob", "conv_1", 42, "apns_desktop_token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(apnsMock.SentSilent) != 1 {
		t.Fatalf("expected 1 silent dismissal sent to other device, got %d", len(apnsMock.SentSilent))
	}
	if apnsMock.SentSilent[0].Action != ActionDismissNotification {
		t.Errorf("expected action dismiss_notification, got %s", apnsMock.SentSilent[0].Action)
	}
}
