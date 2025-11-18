package ollama

import (
	"context"
	"fmt"
	"strings"

	"github.com/ollama/ollama/api"
)

type Summarizer struct {
	client *api.Client
	model  string
}

// NewSummarizer creates a new Ollama summarizer with the specified model.
func NewSummarizer(model string) (*Summarizer, error) {
	if model == "" {
		model = "llama3.2:3b"
	}
	cli, err := api.ClientFromEnvironment()
	if err != nil {
		return nil, fmt.Errorf("failed to create ollama client: %w", err)
	}
	return &Summarizer{
		client: cli,
		model:  model,
	}, nil
}

// SummarizeText summarizes the given text using the configured Ollama model.
// The summary will be concise sentences/fragments without losing information,
// and lists will be converted to comma-separated values.
func (s *Summarizer) SummarizeText(ctx context.Context, text string) (string, error) {
	if text == "" {
		return "", nil
	}

	systemPrompt := `You are a text summarization assistant. Your task is to summarize long text into concise sentences or fragments without losing any important information.

Rules:
- Produce concise sentences or fragments
- Do NOT preserve list formatting - convert lists to comma-separated values
- Preserve all key information - do not omit important details
- Keep the summary factual and accurate
- Use plain text only (no markdown, no bullet points, no numbered lists)`

	userPrompt := fmt.Sprintf(`Please summarize the following text. Convert any lists to comma-separated values and produce concise sentences or fragments without losing information:

%s`, text)

	var responseBuilder strings.Builder
	stream := false
	req := &api.GenerateRequest{
		Model:  s.model,
		Prompt: userPrompt,
		System: systemPrompt,
		Stream: &stream,
		Options: map[string]any{
			"temperature": 0.3, // Lower temperature for more consistent summarization
		},
	}

	err := s.client.Generate(ctx, req, func(resp api.GenerateResponse) error {
		responseBuilder.WriteString(resp.Response)
		return nil
	})

	if err != nil {
		return "", fmt.Errorf("failed to generate summary: %w", err)
	}

	summary := strings.TrimSpace(responseBuilder.String())
	if summary == "" {
		return "", fmt.Errorf("received empty summary from model")
	}

	return summary, nil
}

// SummarizeContext summarizes a conversation context, preserving both information and flow.
// This uses a different prompt than SummarizeText to focus on conversation flow and context preservation.
func (s *Summarizer) SummarizeContext(ctx context.Context, conversationText string) (string, error) {
	if conversationText == "" {
		return "", nil
	}

	systemPrompt := `You are a conversation summarization assistant. Your task is to summarize conversation history while preserving both the information exchanged and the flow of the conversation.

Rules:
- Preserve all key decisions, facts, and important context
- Maintain conversation flow and user preferences
- Capture the progression and flow of the conversation (how topics evolved, what was discussed in what order, transitions between topics)
- Focus on actionable information
- Preserve the conversational context needed for the conversation to continue naturally
- Use plain text only (no markdown, no bullet points, no numbered lists)
- Produce concise sentences or fragments that maintain the full context`

	userPrompt := fmt.Sprintf(`Summarize this conversation history, preserving all important facts, decisions, user preferences, and the flow of the conversation. Include how topics evolved, what was discussed in what order, and any transitions between topics. Ensure the summary maintains the context needed for the conversation to continue naturally with full awareness of both the information exchanged and the conversational flow.

%s`, conversationText)

	var responseBuilder strings.Builder
	stream := false
	req := &api.GenerateRequest{
		Model:  s.model,
		Prompt: userPrompt,
		System: systemPrompt,
		Stream: &stream,
		Options: map[string]any{
			"temperature": 0.3, // Lower temperature for more consistent summarization
		},
	}

	err := s.client.Generate(ctx, req, func(resp api.GenerateResponse) error {
		responseBuilder.WriteString(resp.Response)
		return nil
	})

	if err != nil {
		return "", fmt.Errorf("failed to generate context summary: %w", err)
	}

	summary := strings.TrimSpace(responseBuilder.String())
	if summary == "" {
		return "", fmt.Errorf("received empty context summary from model")
	}

	return summary, nil
}
