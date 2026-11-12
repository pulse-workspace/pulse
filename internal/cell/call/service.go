package call

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/pulse-workspace/pulse/pkg/shared/logger"
	"github.com/pulse-workspace/pulse/pkg/shared/nats"
)

// VideoGrant defines LiveKit video room permissions for participant access token.
type VideoGrant struct {
	Room           string `json:"room"`
	RoomJoin       bool   `json:"roomJoin"`
	CanPublish     bool   `json:"canPublish"`
	CanSubscribe   bool   `json:"canSubscribe"`
	CanPublishData bool   `json:"canPublishData"`
}

// LiveKitClaims defines standard JWT claims structure for LiveKit authentication.
type LiveKitClaims struct {
	Video VideoGrant `json:"video"`
	Name  string     `json:"name,omitempty"`
	jwt.RegisteredClaims
}

// SignalPayload represents a WebRTC call signaling message payload.
type SignalPayload struct {
	SignalType string         `json:"signalType"` // Ring, Accept, Reject, Hangup, Candidate
	SenderID   string         `json:"senderID"`
	TargetID   string         `json:"targetID"`
	RoomName   string         `json:"roomName"`
	Data       map[string]any `json:"data,omitempty"`
	CreatedAt  int64          `json:"createdAt"`
}

// Service manages LiveKit room tokens and real-time WebRTC signaling dispatches.
type Service struct {
	cfg  *Config
	nats *nats.Client
	log  logger.Logger
}

// NewService constructs a validated Call Service instance.
//
// Business Logic:
// Validates Config parameters against Fail-Fast rules before initializing the service.
func NewService(cfg *Config, natsClient *nats.Client, log logger.Logger) (*Service, error) {
	if cfg == nil {
		return nil, errors.New("call service: configuration cannot be nil")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &Service{
		cfg:  cfg,
		nats: natsClient,
		log:  log.With().Str("component", "call_service").Logger(),
	}, nil
}

// GenerateToken creates a signed LiveKit room access JWT token for a participant.
//
// Business Logic:
// 1. Constructs standard LiveKit claims granting roomJoin, canPublish, canSubscribe, canPublishData.
// 2. Sets expiration to 6 hours from generation.
// 3. Signs JWT using HMAC-SHA256 with LiveKit Secret key.
func (s *Service) GenerateToken(roomName, participantID, participantName string) (string, error) {
	if roomName == "" || participantID == "" {
		return "", errors.New("roomName and participantID are required to generate livekit token")
	}

	now := time.Now()
	claims := LiveKitClaims{
		Name: participantName,
		Video: VideoGrant{
			Room:           roomName,
			RoomJoin:       true,
			CanPublish:     true,
			CanSubscribe:   true,
			CanPublishData: true,
		},
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    s.cfg.LiveKitAPIKey,
			Subject:   participantID,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(6 * time.Hour)),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(s.cfg.LiveKitSecret))
	if err != nil {
		s.log.Error().Err(err).Str("room", roomName).Str("participant", participantID).Msg("failed to sign livekit token")
		return "", fmt.Errorf("failed to sign livekit token: %w", err)
	}

	return signed, nil
}

// DispatchSignal publishes a WebRTC signaling event to target participant via NATS.
//
// Business Logic:
// Serializes SignalPayload into JSON and dispatches to NATS topic pulse.signals.{targetID}.
// If target is connected on any gateway instance, message is relayed in real-time.
func (s *Service) DispatchSignal(
	ctx context.Context,
	signalType string,
	senderID string,
	targetID string,
	roomName string,
	data map[string]any,
) error {
	if signalType == "" || senderID == "" || targetID == "" {
		return errors.New("signalType, senderID, and targetID are mandatory")
	}

	payload := SignalPayload{
		SignalType: signalType,
		SenderID:   senderID,
		TargetID:   targetID,
		RoomName:   roomName,
		Data:       data,
		CreatedAt:  time.Now().UnixMilli(),
	}

	bytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal signal payload: %w", err)
	}

	if s.nats != nil {
		topic := fmt.Sprintf("pulse.signals.%s", targetID)
		if err := s.nats.Publish(ctx, topic, bytes); err != nil {
			s.log.Error().Err(err).Str("topic", topic).Msg("failed to publish signaling payload to NATS")
			return err
		}
	}

	return nil
}

// GetLiveKitURL returns the configured LiveKit endpoint URL.
func (s *Service) GetLiveKitURL() string {
	return s.cfg.LiveKitURL
}
