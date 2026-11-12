package copilot

import (
	"errors"
	"strings"
)

// Config holds AI model and credentials configuration for Copilot service.
type Config struct {
	Provider string // "gemini" or "openai"
	APIKey   string
	Model    string
	BaseURL  string
}

// Validate validates that mandatory AI configuration parameters are present.
//
// Business Logic:
// Enforces Fail-Fast configuration checking. Throws error if Provider, APIKey,
// or Model are empty or invalid, strictly preventing silent fallback defaults.
func (c *Config) Validate() error {
	p := strings.ToLower(strings.TrimSpace(c.Provider))
	if p != "gemini" && p != "openai" {
		return errors.New("copilot configuration error: Provider must be either 'gemini' or 'openai'")
	}
	if strings.TrimSpace(c.APIKey) == "" {
		return errors.New("copilot configuration error: APIKey is required and cannot be empty")
	}
	if strings.TrimSpace(c.Model) == "" {
		return errors.New("copilot configuration error: Model is required and cannot be empty")
	}
	return nil
}

