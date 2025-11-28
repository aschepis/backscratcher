package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/rs/zerolog"
)

// Normalizer converts raw user/agent statements into structured personal memories.
// It is stateless aside from its Anthropic client configuration.
type Normalizer struct {
	APIKey     string
	Model      string
	MaxTokens  int
	HTTPClient *http.Client
	logger     zerolog.Logger
}

// NewNormalizer constructs a Normalizer configured for the Anthropic Messages API.
func NewNormalizer(model, apiKey string, maxTokens int, logger zerolog.Logger) *Normalizer {
	if maxTokens <= 0 {
		maxTokens = 256
	}
	return &Normalizer{
		APIKey:    apiKey,
		Model:     model,
		MaxTokens: maxTokens,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		logger: logger.With().Str("component", "normalizer").Logger(),
	}
}

// Normalize takes raw free-form text and returns normalized text, memory type, and tags.
//
// Contract (from 4_memory_normalization.md):
//   - normalized: third-person, self-contained, typically starting with "The user ..."
//   - type: one of "preference","biographical","habit","goal","value","project","other"
//   - tags: 3–8 lowercase tokens, no spaces; if empty, falls back to ["misc"]
func (n *Normalizer) Normalize(ctx context.Context, rawText string) (string, string, []string, error) {
	rawText = strings.TrimSpace(rawText)
	if rawText == "" {
		return "", "", nil, fmt.Errorf("normalizer: raw text is empty")
	}
	if n.APIKey == "" {
		return "", "", nil, fmt.Errorf("normalizer: missing API key")
	}
	if n.Model == "" {
		return "", "", nil, fmt.Errorf("normalizer: model name is required")
	}

	systemPrompt := `You are a memory normalization module for a personal AI assistant.

You must convert a single raw user or agent statement into a structured memory JSON object.

Output MUST be valid JSON with this exact shape and no extra keys:
{
  "normalized": string,
  "type": string,
  "tags": string[]
}

Requirements:
- "normalized" must be a third-person, self-contained sentence or short paragraph.
- Prefer to begin with "The user ..." when describing the user.
- "type" must be exactly one of:
  "preference", "biographical", "habit", "goal", "value", "project", "other"
- "tags" must be 3-8 short, lowercase tokens without spaces.
  - Examples: "music", "running", "programming_languages", "sleep_schedule".
- Do NOT include secrets (API keys, passwords, tokens) in "normalized" or "tags".
- If the input is not suitable as a long-term memory, still respond with best-effort JSON.

You must output ONLY the JSON object. Do not include explanations, comments, or surrounding text.`

	userPrompt := fmt.Sprintf(`Normalize the following statement into a structured personal memory:

%s`, rawText)

	type message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	payload := map[string]interface{}{
		"model":       n.Model,
		"max_tokens":  n.MaxTokens,
		"temperature": 0.0,
		"messages": []message{
			{
				Role:    "system",
				Content: systemPrompt,
			},
			{
				Role:    "user",
				Content: userPrompt,
			},
		},
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return "", "", nil, fmt.Errorf("normalizer: marshal request: %w", err)
	}

	normalized, memType, tags, err := n.callAnthropic(ctx, bodyBytes)
	if err != nil {
		return "", "", nil, err
	}

	// Post-processing and contract enforcement.
	if strings.TrimSpace(normalized) == "" {
		normalized = rawText
	}
	memType = sanitizeMemoryType(memType)
	tags = sanitizeTags(tags)
	if len(tags) == 0 {
		tags = []string{"misc"}
	}
	normalized = stripSecrets(normalized)
	for i := range tags {
		tags[i] = stripSecrets(tags[i])
	}

	return normalized, memType, tags, nil
}

// callAnthropic handles the HTTP call and response parsing, including retry logic with exponential backoff for rate limits.
func (n *Normalizer) callAnthropic(ctx context.Context, body []byte) (string, string, []string, error) {
	const endpoint = "https://api.anthropic.com/v1/messages"

	var result struct {
		normalized string
		memType    string
		tags       []string
	}
	var retryAfter time.Duration

	// Create backoff configuration
	eb := backoff.NewExponentialBackOff()
	eb.InitialInterval = 1 * time.Second
	eb.Multiplier = 2.0
	eb.MaxInterval = 60 * time.Second
	eb.MaxElapsedTime = 5 * time.Minute
	eb.RandomizationFactor = 0.2 // 20% jitter
	eb.Reset()

	// Limit max retries
	b := backoff.WithMaxRetries(eb, 5)

	operation := func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return backoff.Permanent(fmt.Errorf("normalizer: create request: %w", err))
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", n.APIKey)
		req.Header.Set("anthropic-version", "2023-06-01")

		resp, err := n.HTTPClient.Do(req)
		if err != nil {
			return fmt.Errorf("normalizer: request failed: %w", err)
		}
		defer resp.Body.Close() //nolint:errcheck // Body close error can be ignored

		if resp.StatusCode >= 400 {
			var apiErr map[string]interface{}
			_ = json.NewDecoder(resp.Body).Decode(&apiErr)

			// Check for rate limit (429)
			if resp.StatusCode == 429 {
				retryAfter = extractRetryAfterFromResponseSummarizer(resp)
				if retryAfter > 0 {
					// Use retry-after as initial delay for next attempt
					eb.Reset()
					eb.InitialInterval = retryAfter
					eb.Multiplier = 1.5
					eb.RandomizationFactor = 0.1
					eb.Reset()
				}
				n.logger.Warn().Dur("retryAfter", retryAfter).Msg("Normalizer: Rate limit encountered, retrying")
				return fmt.Errorf("normalizer: rate limit: %s: %v", resp.Status, apiErr)
			}

			// Don't retry on 4xx errors (except 429)
			if resp.StatusCode < 500 {
				return backoff.Permanent(fmt.Errorf("normalizer: API error %s: %v", resp.Status, apiErr))
			}

			// Retry on 5xx errors
			n.logger.Warn().Str("status", resp.Status).Msg("Normalizer: Server error, retrying")
			return fmt.Errorf("normalizer: server error %s: %v", resp.Status, apiErr)
		}

		var msgResp struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&msgResp); err != nil {
			return fmt.Errorf("normalizer: decode response: %w", err)
		}

		if len(msgResp.Content) == 0 {
			return fmt.Errorf("normalizer: empty content in response")
		}

		rawJSON := strings.TrimSpace(msgResp.Content[0].Text)
		var out struct {
			Normalized string   `json:"normalized"`
			Type       string   `json:"type"`
			Tags       []string `json:"tags"`
		}
		if err := json.Unmarshal([]byte(rawJSON), &out); err != nil {
			return fmt.Errorf("normalizer: parse model JSON: %w", err)
		}

		result.normalized = out.Normalized
		result.memType = out.Type
		result.tags = out.Tags
		return nil
	}

	err := backoff.Retry(operation, backoff.WithContext(b, ctx))
	if err != nil {
		return "", "", nil, err
	}

	return result.normalized, result.memType, result.tags, nil
}

var (
	allowedTypes = map[string]struct{}{
		"preference":   {},
		"biographical": {},
		"habit":        {},
		"goal":         {},
		"value":        {},
		"project":      {},
		"other":        {},
	}

	tagSanitizer = regexp.MustCompile(`[^a-z0-9_-]+`)
	secretLike   = regexp.MustCompile(`(?i)(api[_-]?key|secret|token|password|sk-[a-z0-9]{10,})`)
)

func sanitizeMemoryType(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	if _, ok := allowedTypes[t]; ok {
		return t
	}
	return "other"
}

func sanitizeTags(tags []string) []string {
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.ToLower(strings.TrimSpace(tag))
		if tag == "" {
			continue
		}
		tag = tagSanitizer.ReplaceAllString(tag, "_")
		tag = strings.Trim(tag, "_-")
		if tag == "" {
			continue
		}
		out = append(out, tag)
	}
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

func stripSecrets(s string) string {
	return secretLike.ReplaceAllString(s, "[redacted]")
}
