package call

import (
	"errors"
	"strings"
)

// Config holds LiveKit SFU server connection and authentication parameters.
type Config struct {
	LiveKitURL    string
	LiveKitAPIKey string
	LiveKitSecret string
}

// Validate validates that all required LiveKit configuration variables are present.
//
// Business Logic:
// Enforces Fail-Fast configuration checking. Returns descriptive error if
// LiveKitURL, LiveKitAPIKey or LiveKitSecret are empty or whitespace,
// strictly rejecting implicit fallback defaults.
func (c *Config) Validate() error {
	if strings.TrimSpace(c.LiveKitURL) == "" {
		return errors.New("livekit configuration error: LIVEKIT_URL is required and cannot be empty")
	}
	if strings.TrimSpace(c.LiveKitAPIKey) == "" {
		return errors.New("livekit configuration error: LIVEKIT_API_KEY is required and cannot be empty")
	}
	if strings.TrimSpace(c.LiveKitSecret) == "" {
		return errors.New("livekit configuration error: LIVEKIT_API_SECRET is required and cannot be empty")
	}
	return nil
}
