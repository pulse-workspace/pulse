package copilot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pulse-workspace/pulse/pkg/shared/logger"
)

func TestConfig_Validate(t *testing.T) {
	// Invalid provider
	cfg := &Config{Provider: "invalid", APIKey: "key", Model: "model"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error on invalid provider")
	}

	// Missing APIKey
	cfg = &Config{Provider: "gemini", APIKey: "", Model: "gemini-1.5-flash"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error on missing APIKey")
	}

	// Missing Model
	cfg = &Config{Provider: "gemini", APIKey: "key", Model: ""}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error on missing Model")
	}

	// Valid Gemini
	cfg = &Config{Provider: "gemini", APIKey: "key", Model: "gemini-1.5-flash"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected nil on valid config, got %v", err)
	}
}

func TestGeminiProvider(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		res := geminiResponse{
			Candidates: []struct {
				Content struct {
					Parts []struct {
						Text string `json:"text"`
					} `json:"parts"`
				} `json:"content"`
			}{
				{
					Content: struct {
						Parts []struct {
							Text string `json:"text"`
						} `json:"parts"`
					}{
						Parts: []struct {
							Text string `json:"text"`
						}{
							{Text: "1. Sounds good!\n2. I will check that now.\n3. Let us schedule a call."},
						},
					},
				},
			},
		}
		json.NewEncoder(w).Encode(res)
	}))
	defer mockServer.Close()

	provider := NewGeminiProvider("mock_key", "gemini-1.5-flash", mockServer.URL)

	messages := []ThreadMessage{
		{SenderName: "Alice", Text: "Can we review the deployment at 3 PM?", Timestamp: 1000},
	}

	summary, err := provider.SummarizeThread(context.Background(), messages)
	if err != nil {
		t.Fatalf("SummarizeThread failed: %v", err)
	}
	if summary == "" {
		t.Fatal("expected non-empty summary")
	}

	replies, err := provider.GenerateSmartReplies(context.Background(), messages)
	if err != nil {
		t.Fatalf("GenerateSmartReplies failed: %v", err)
	}
	if len(replies) != 3 {
		t.Fatalf("expected 3 replies, got %d", len(replies))
	}
	if replies[0] != "Sounds good!" {
		t.Errorf("expected 'Sounds good!', got '%s'", replies[0])
	}
}

func TestOpenAIProvider(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		res := openAIResponse{
			Choices: []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			}{
				{
					Message: struct {
						Content string `json:"content"`
					}{
						Content: "Summary: Team discussed release schedule.",
					},
				},
			},
		}
		json.NewEncoder(w).Encode(res)
	}))
	defer mockServer.Close()

	log := logger.New(logger.Config{Level: "debug"})
	cfg := &Config{
		Provider: "openai",
		APIKey:   "sk_mock_key",
		Model:    "gpt-4o",
		BaseURL:  mockServer.URL,
	}

	svc, err := NewService(cfg, log)
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}

	messages := []ThreadMessage{
		{SenderName: "Bob", Text: "Release is ready on staging.", Timestamp: 2000},
	}

	summary, err := svc.Summarize(context.Background(), messages)
	if err != nil {
		t.Fatalf("Summarize failed: %v", err)
	}
	if summary != "Summary: Team discussed release schedule." {
		t.Fatalf("unexpected summary: %s", summary)
	}
}

