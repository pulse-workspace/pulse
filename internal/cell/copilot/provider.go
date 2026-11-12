package copilot

import "context"

// ThreadMessage represents an individual message passed into AI context.
type ThreadMessage struct {
	SenderName string `json:"senderName"`
	Text       string `json:"text"`
	Timestamp  int64  `json:"timestamp"`
}

// AIProvider defines the contract for LLM backends (Gemini, OpenAI).
type AIProvider interface {
	// SummarizeThread analyzes thread messages and produces an executive summary.
	SummarizeThread(ctx context.Context, messages []ThreadMessage) (string, error)

	// GenerateSmartReplies suggests 3 short quick-reply options for the latest message.
	GenerateSmartReplies(ctx context.Context, messages []ThreadMessage) ([]string, error)
}
