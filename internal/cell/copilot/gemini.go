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

// GeminiProvider implements AIProvider for Google Gemini API.
type GeminiProvider struct {
	apiKey     string
	model      string
	baseURL    string
	httpClient *http.Client
}

// NewGeminiProvider constructs a GeminiProvider.
func NewGeminiProvider(apiKey, model, baseURL string) *GeminiProvider {
	if baseURL == "" {
		baseURL = "https://generativelanguage.googleapis.com"
	}
	return &GeminiProvider{
		apiKey:  apiKey,
		model:   model,
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
}

type geminiRequest struct {
	Contents []geminiContent `json:"contents"`
}

type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
}

// SummarizeThread sends prompt to Gemini to summarize discussion.
//
// Business Logic:
// 1. Formats conversation transcript into chronological bullet points.
// 2. Dispatches prompt to Gemini API v1beta models.generateContent.
// 3. Extracts candidate text response.
func (g *GeminiProvider) SummarizeThread(ctx context.Context, messages []ThreadMessage) (string, error) {
	if len(messages) == 0 {
		return "", errors.New("cannot summarize empty message list")
	}

	var sb strings.Builder
	sb.WriteString("Summarize the following chat conversation into key points and action items:\n\n")
	for _, m := range messages {
		sb.WriteString(fmt.Sprintf("%s: %s\n", m.SenderName, m.Text))
	}

	return g.generate(ctx, sb.String())
}

// GenerateSmartReplies suggests 3 short quick-reply responses.
//
// Business Logic:
// Asks Gemini to output exactly 3 short reply suggestions separated by newlines.
func (g *GeminiProvider) GenerateSmartReplies(ctx context.Context, messages []ThreadMessage) ([]string, error) {
	if len(messages) == 0 {
		return nil, errors.New("cannot generate smart replies for empty message list")
	}

	var sb strings.Builder
	sb.WriteString("Suggest 3 concise, friendly reply options for the recipient of the last message. Return only the 3 options, one per line:\n\n")
	for _, m := range messages {
		sb.WriteString(fmt.Sprintf("%s: %s\n", m.SenderName, m.Text))
	}

	text, err := g.generate(ctx, sb.String())
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

func (g *GeminiProvider) generate(ctx context.Context, prompt string) (string, error) {
	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent?key=%s", g.baseURL, g.model, g.apiKey)

	reqBody := geminiRequest{
		Contents: []geminiContent{
			{Parts: []geminiPart{{Text: prompt}}},
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

	res, err := g.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("gemini request failed: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gemini api returned status %d", res.StatusCode)
	}

	var parsed geminiResponse
	if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
		return "", err
	}

	if len(parsed.Candidates) == 0 || len(parsed.Candidates[0].Content.Parts) == 0 {
		return "", errors.New("gemini returned no content candidates")
	}

	return parsed.Candidates[0].Content.Parts[0].Text, nil
}

