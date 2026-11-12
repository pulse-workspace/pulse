package call

import (
	"context"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/pulse-workspace/pulse/pkg/shared/logger"
)

func TestConfig_Validate(t *testing.T) {
	// Missing URL
	cfg := &Config{LiveKitURL: "", LiveKitAPIKey: "key", LiveKitSecret: "sec"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error on missing LiveKitURL, got nil")
	}

	// Missing Key
	cfg = &Config{LiveKitURL: "https://livekit.test", LiveKitAPIKey: "", LiveKitSecret: "sec"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error on missing LiveKitAPIKey, got nil")
	}

	// Missing Secret
	cfg = &Config{LiveKitURL: "https://livekit.test", LiveKitAPIKey: "key", LiveKitSecret: ""}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error on missing LiveKitSecret, got nil")
	}

	// Valid
	cfg = &Config{LiveKitURL: "https://livekit.test", LiveKitAPIKey: "key", LiveKitSecret: "sec"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected nil error on valid config, got %v", err)
	}
}

func TestService_GenerateToken(t *testing.T) {
	log := logger.New(logger.Config{Level: "debug"})
	cfg := &Config{
		LiveKitURL:    "https://livekit.pulse.internal",
		LiveKitAPIKey: "test_api_key",
		LiveKitSecret: "super_secret_signing_key_123",
	}

	svc, err := NewService(cfg, nil, log)
	if err != nil {
		t.Fatalf("failed to create call service: %v", err)
	}

	tokenStr, err := svc.GenerateToken("room_pulse_101", "usr_alice", "Alice")
	if err != nil {
		t.Fatalf("GenerateToken failed: %v", err)
	}
	if tokenStr == "" {
		t.Fatal("expected non-empty token string")
	}

	// Parse and verify claims
	var claims LiveKitClaims
	token, err := jwt.ParseWithClaims(tokenStr, &claims, func(token *jwt.Token) (any, error) {
		return []byte(cfg.LiveKitSecret), nil
	})
	if err != nil || !token.Valid {
		t.Fatalf("token validation failed: %v", err)
	}

	if claims.Issuer != "test_api_key" {
		t.Errorf("expected issuer 'test_api_key', got %s", claims.Issuer)
	}
	if claims.Subject != "usr_alice" {
		t.Errorf("expected subject 'usr_alice', got %s", claims.Subject)
	}
	if claims.Name != "Alice" {
		t.Errorf("expected name 'Alice', got %s", claims.Name)
	}
	if claims.Video.Room != "room_pulse_101" {
		t.Errorf("expected room 'room_pulse_101', got %s", claims.Video.Room)
	}
	if !claims.Video.RoomJoin || !claims.Video.CanPublish || !claims.Video.CanSubscribe {
		t.Errorf("expected video grants enabled, got %+v", claims.Video)
	}
}

func TestService_DispatchSignal(t *testing.T) {
	log := logger.New(logger.Config{Level: "debug"})
	cfg := &Config{
		LiveKitURL:    "https://livekit.pulse.internal",
		LiveKitAPIKey: "test_api_key",
		LiveKitSecret: "super_secret_signing_key_123",
	}

	svc, err := NewService(cfg, nil, log)
	if err != nil {
		t.Fatalf("failed to create call service: %v", err)
	}

	err = svc.DispatchSignal(
		context.Background(),
		"Ring",
		"usr_alice",
		"usr_bob",
		"room_pulse_101",
		map[string]any{"hasVideo": true},
	)
	if err != nil {
		t.Fatalf("DispatchSignal failed: %v", err)
	}
}
