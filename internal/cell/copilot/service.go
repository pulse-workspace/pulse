package copilot

import (
	"context"
	"errors"
	"strings"

	"github.com/pulse-workspace/pulse/pkg/shared/logger"
)

// Service provides AI thread summarization and smart reply assistance.
type Service struct {
	provider AIProvider
	log      logger.Logger
}

// NewService constructs and validates a Copilot Service instance.
//
// Business Logic:
// Validates Config with fail-fast rules, constructs either GeminiProvider or
// OpenAIProvider based on Provider setting, and initializes the service.
func NewService(cfg *Config, log logger.Logger) (*Service, error) {
	if cfg == nil {
		return nil, errors.New("copilot configuration cannot be nil")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	var provider AIProvider
	switch strings.ToLower(cfg.Provider) {
	case "gemini":
		provider = NewGeminiProvider(cfg.APIKey, cfg.Model, cfg.BaseURL)
	case "openai":
		provider = NewOpenAIProvider(cfg.APIKey, cfg.Model, cfg.BaseURL)
	default:
		return nil, errors.New("unsupported copilot provider")
	}

	return &Service{
		provider: provider,
		log:      log.With().Str("component", "copilot_service").Logger(),
	}, nil
}

// Summarize produces an executive summary of a message list.
func (s *Service) Summarize(ctx context.Context, messages []ThreadMessage) (string, error) {
	return s.provider.SummarizeThread(ctx, messages)
}

// SmartReplies returns 3 suggested quick replies for the conversation.
func (s *Service) SmartReplies(ctx context.Context, messages []ThreadMessage) ([]string, error) {
	return s.provider.GenerateSmartReplies(ctx, messages)
}
