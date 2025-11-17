package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// AnthropicSummarizer implements Summarizer using Claude via the Messages API.
type AnthropicSummarizer struct {
	APIKey     string
	Model      string
	MaxTokens  int
	HTTPClient *http.Client
}

// NewAnthropicSummarizer returns a configured summarizer.
func NewAnthropicSummarizer(model, apiKey string, maxTokens int) *AnthropicSummarizer {
	if apiKey == "" {
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	if maxTokens <= 0 {
		maxTokens = 256
	}
	return &AnthropicSummarizer{
		APIKey:    apiKey,
		Model:     model,
		MaxTokens: maxTokens,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// SummarizeEpisodes turns episodes into concise durable facts.
func (s *AnthropicSummarizer) SummarizeEpisodes(episodes []MemoryItem) (string, error) {
	if s.APIKey == "" {
		return "", fmt.Errorf("AnthropicSummarizer: missing API key")
	}
	if s.Model == "" {
		return "", fmt.Errorf("AnthropicSummarizer: missing model name")
	}
	if len(episodes) == 0 {
		return "", fmt.Errorf("AnthropicSummarizer: no episodes provided")
	}

	var b strings.Builder
	for i, ep := range episodes {
		b.WriteString(fmt.Sprintf("Episode %d (%s):\n", i+1, ep.CreatedAt.Format(time.RFC3339)))
		b.WriteString(ep.Content)
		b.WriteString("\n\n")
	}
	transcript := b.String()

	systemPrompt := `You are an AI assistant that summarizes an agent's recent work into concise, durable facts suitable for long-term memory in a multi-agent system.

Your goals:
- Extract only stable, reusable information (decisions, conclusions, preferences, important facts, open questions).
- Ignore transient details, tool errors, or irrelevant side tracks.
- Write in third person, not as the agent or user.
- Be concise but specific.
- Prefer bullet points or short paragraphs.
- Do NOT mention that you are summarizing episodes; just state the distilled knowledge.`

	userPrompt := fmt.Sprintf(`Here are chronological notes and episodes from a single agent's recent work.

Please produce a concise summary of the key facts, decisions, and knowledge that should be stored as long-term memory for the whole system to use.

Episodes:
%s`, transcript)

	payload := map[string]interface{}{
		"model":       s.Model,
		"max_tokens":  s.MaxTokens,
		"temperature": 0.1,
		"messages": []map[string]interface{}{
			{
				"role":    "system",
				"content": systemPrompt,
			},
			{
				"role":    "user",
				"content": userPrompt,
			},
		},
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("AnthropicSummarizer: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		"https://api.anthropic.com/v1/messages",
		bytes.NewReader(bodyBytes),
	)
	if err != nil {
		return "", fmt.Errorf("AnthropicSummarizer: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", s.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := s.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("AnthropicSummarizer: request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // Body close error can be ignored

	if resp.StatusCode >= 400 {
		var apiErr map[string]interface{}
		_ = json.NewDecoder(resp.Body).Decode(&apiErr)
		return "", fmt.Errorf("AnthropicSummarizer: API error %s: %v", resp.Status, apiErr)
	}

	var msgResp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&msgResp); err != nil {
		return "", fmt.Errorf("AnthropicSummarizer: decode response: %w", err)
	}
	if len(msgResp.Content) == 0 {
		return "", fmt.Errorf("AnthropicSummarizer: empty content in response")
	}
	summary := strings.TrimSpace(msgResp.Content[0].Text)
	if summary == "" {
		return "", fmt.Errorf("AnthropicSummarizer: empty summary text")
	}
	return summary, nil
}
