package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aschepis/backscratcher/staff/llm"
	"github.com/aschepis/backscratcher/staff/memory/ollama"
	"github.com/rs/zerolog"
)

// MessageSummarizerConfig holds configuration for message summarization.
type MessageSummarizerConfig struct {
	Model         string
	MaxChars      int
	MaxLines      int
	MaxLineBreaks int
}

// MessageSummarizer wraps an Ollama summarizer and provides methods to check
// if text should be summarized and to perform summarization.
type MessageSummarizer struct {
	config     MessageSummarizerConfig
	summarizer *ollama.Summarizer
	logger     zerolog.Logger
}

// NewMessageSummarizer creates a new MessageSummarizer with the given config.
// If summarization is disabled or summarizer creation fails, returns nil.
func NewMessageSummarizer(cfg MessageSummarizerConfig, logger zerolog.Logger) (*MessageSummarizer, error) {
	summarizer, err := ollama.NewSummarizer(cfg.Model)
	if err != nil {
		return nil, fmt.Errorf("failed to create ollama summarizer: %w", err)
	}

	return &MessageSummarizer{
		config:     cfg,
		summarizer: summarizer,
		logger:     logger.With().Str("component", "messageSummarizer").Logger(),
	}, nil
}

// ShouldSummarize checks if the given text should be summarized based on
// the configured heuristics (character count, line count, line breaks).
// Returns true if any threshold is exceeded.
func (m *MessageSummarizer) ShouldSummarize(text string) bool {
	if text == "" {
		return false
	}

	// Check character count
	if len(text) > m.config.MaxChars {
		return true
	}

	// Check line count
	lines := strings.Split(text, "\n")
	if len(lines) > m.config.MaxLines {
		return true
	}

	// Check line break count
	lineBreaks := strings.Count(text, "\n")
	return lineBreaks > m.config.MaxLineBreaks
}

// Summarize summarizes the given text using the configured Ollama model.
// Returns the original text if summarization fails or is disabled.
func (m *MessageSummarizer) Summarize(ctx context.Context, text string) (string, error) {
	if text == "" {
		return text, nil
	}

	summary, err := m.summarizer.SummarizeText(ctx, text)
	if err != nil {
		m.logger.Warn().Err(err).Msg("Failed to summarize text, using original")
		return text, err
	}

	m.logger.Debug().Int("original_chars", len(text)).Int("summary_chars", len(summary)).Msg("Summarized text")
	return summary, nil
}

// SummarizeContext summarizes an entire conversation context, including system prompt and message history.
// It preserves both the information and the flow of the conversation.
// Uses provider-neutral llm.Message types.
func (m *MessageSummarizer) SummarizeContext(
	ctx context.Context,
	systemPrompt string,
	messages []llm.Message,
) (string, error) {
	// Convert conversation to text format for summarization
	var conversationText strings.Builder

	// Add system prompt
	if systemPrompt != "" {
		conversationText.WriteString("System: ")
		conversationText.WriteString(systemPrompt)
		conversationText.WriteString("\n\n")
	}

	// Add messages
	for _, msg := range messages {
		switch msg.Role {
		case llm.RoleUser:
			conversationText.WriteString("User: ")
			for _, block := range msg.Content {
				if block.Type == llm.ContentBlockTypeText {
					conversationText.WriteString(block.Text)
				} else if block.Type == llm.ContentBlockTypeToolResult && block.ToolResult != nil {
					conversationText.WriteString(block.ToolResult.Content)
				}
			}
			conversationText.WriteString("\n\n")
		case llm.RoleAssistant:
			conversationText.WriteString("Assistant: ")
			for _, block := range msg.Content {
				if block.Type == llm.ContentBlockTypeText {
					conversationText.WriteString(block.Text)
				} else if block.Type == llm.ContentBlockTypeToolUse && block.ToolUse != nil {
					conversationText.WriteString(fmt.Sprintf("[Tool: %s]", block.ToolUse.Name))
					if block.ToolUse.Input != nil {
						if inputBytes, err := json.Marshal(block.ToolUse.Input); err == nil {
							conversationText.WriteString(fmt.Sprintf(" %s", string(inputBytes)))
						}
					}
				}
			}
			conversationText.WriteString("\n\n")
		default:
			// Handle any other roles (e.g., tool results sent as separate messages)
			for _, block := range msg.Content {
				if block.Type == llm.ContentBlockTypeText {
					conversationText.WriteString(block.Text)
				} else if block.Type == llm.ContentBlockTypeToolResult && block.ToolResult != nil {
					conversationText.WriteString("Tool Result: ")
					conversationText.WriteString(block.ToolResult.Content)
				}
			}
			conversationText.WriteString("\n\n")
		}
	}

	// Use context-specific summarization
	summary, err := m.summarizer.SummarizeContext(ctx, conversationText.String())
	if err != nil {
		m.logger.Warn().Err(err).Msg("Failed to summarize context, using original")
		return "", fmt.Errorf("failed to summarize context: %w", err)
	}

	m.logger.Debug().
		Int("original_chars", conversationText.Len()).
		Int("summary_chars", len(summary)).
		Msg("Summarized context")
	return summary, nil
}
