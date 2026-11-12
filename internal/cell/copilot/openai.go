package copilot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// OpenAIProvider implements AIProvider for OpenAI and compatible endpoints.
type OpenAIProvider struct {
	apiKey     string
	model      string
	baseURL    string
	httpClient *http.Client
}

// NewOpenAIProvider constructs an OpenAIProvider.
func NewOpenAIProvider(apiKey, model, baseURL string) *OpenAIProvider {
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	return &OpenAIProvider{
		apiKey:  apiKey,
		model:   model,
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIRequest struct {
	Model    string          `json:"model"`
	Messages []openAIMessage `json:"messages"`
}

type openAIResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// SummarizeThread summarizes messages via OpenAI Chat Completion.
func (o *OpenAIProvider) SummarizeThread(ctx context.Context, messages []ThreadMessage) (string, error) {
	if len(messages) == 0 {
		return "", errors.New("cannot summarize empty message list")
	}

	var sb strings.Builder
	for _, m := range messages {
		sb.WriteString(fmt.Sprintf("%s: %s\n", m.SenderName, m.Text))
	}

	systemPrompt := "You are a helpful assistant. Summarize the chat conversation into key points and action items."
	return o.complete(ctx, systemPrompt, sb.String())
}

// GenerateSmartReplies suggests 3 short quick-replies.
func (o *OpenAIProvider) GenerateSmartReplies(ctx context.Context, messages []ThreadMessage) ([]string, error) {
	if len(messages) == 0 {
		return nil, errors.New("cannot generate smart replies for empty message list")
	}

	var sb strings.Builder
	for _, m := range messages {
		sb.WriteString(fmt.Sprintf("%s: %s\n", m.SenderName, m.Text))
	}

	systemPrompt := "Suggest 3 concise, friendly reply options for the recipient. Return only the 3 options, one per line."
	text, err := o.complete(ctx, systemPrompt, sb.String())
	if err != nil {
		return nil, err
	}

	var replies []string
	for _, line := range strings.Split(text, "\n") {
		clean := strings.TrimSpace(strings.TrimLeft(line, "1234567890.-*• "))
		if clean != "" {
			replies = append(replies, clean)
		}
		if len(replies) == 3 {
			break
		}
	}
	return replies, nil
}

func (o *OpenAIProvider) complete(ctx context.Context, systemPrompt, userContent string) (string, error) {
	url := fmt.Sprintf("%s/v1/chat/completions", o.baseURL)

	reqBody := openAIRequest{
		Model: o.model,
		Messages: []openAIMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userContent},
		},
	}
	bytesBody, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bytesBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.apiKey)

	res, err := o.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("openai request failed: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("openai api returned status %d", res.StatusCode)
	}

	var parsed openAIResponse
	if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
		return "", err
	}

	if len(parsed.Choices) == 0 {
		return "", errors.New("openai returned no choices")
	}

	return parsed.Choices[0].Message.Content, nil
}
