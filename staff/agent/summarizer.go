package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/aschepis/backscratcher/staff/logger"
	"github.com/aschepis/backscratcher/staff/memory/ollama"
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
}

// NewMessageSummarizer creates a new MessageSummarizer with the given config.
// If summarization is disabled or summarizer creation fails, returns nil.
func NewMessageSummarizer(cfg MessageSummarizerConfig) (*MessageSummarizer, error) {
	summarizer, err := ollama.NewSummarizer(cfg.Model)
	if err != nil {
		return nil, fmt.Errorf("failed to create ollama summarizer: %w", err)
	}

	return &MessageSummarizer{
		config:     cfg,
		summarizer: summarizer,
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
		logger.Warn("Failed to summarize text, using original: %v", err)
		return text, err
	}

	logger.Debug("Summarized text: %d chars -> %d chars", len(text), len(summary))
	return summary, nil
}

// SummarizeContext summarizes an entire conversation context, including system prompt and message history.
// It preserves both the information and the flow of the conversation.
func (m *MessageSummarizer) SummarizeContext(
	ctx context.Context,
	systemPrompt string,
	messages []anthropic.MessageParam,
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
		case "user":
			conversationText.WriteString("User: ")
			for _, blockUnion := range msg.Content {
				if blockUnion.OfText != nil {
					conversationText.WriteString(blockUnion.OfText.Text)
				}
			}
			conversationText.WriteString("\n\n")
		case "assistant":
			conversationText.WriteString("Assistant: ")
			for _, blockUnion := range msg.Content {
				if blockUnion.OfText != nil {
					conversationText.WriteString(blockUnion.OfText.Text)
				} else if blockUnion.OfToolUse != nil {
					conversationText.WriteString(fmt.Sprintf("[Tool: %s]", blockUnion.OfToolUse.Name))
					if blockUnion.OfToolUse.Input != nil {
						if inputBytes, err := json.Marshal(blockUnion.OfToolUse.Input); err == nil {
							conversationText.WriteString(fmt.Sprintf(" %s", string(inputBytes)))
						}
					}
				}
			}
			conversationText.WriteString("\n\n")
		case "tool":
			conversationText.WriteString("Tool Result: ")
			for _, blockUnion := range msg.Content {
				if blockUnion.OfToolResult != nil {
					// Content is a slice of ToolResultBlockParamContentUnion
					for _, contentUnion := range blockUnion.OfToolResult.Content {
						if contentUnion.OfText != nil {
							conversationText.WriteString(contentUnion.OfText.Text)
						}
					}
				}
			}
			conversationText.WriteString("\n\n")
		}
	}

	// Use context-specific summarization
	summary, err := m.summarizer.SummarizeContext(ctx, conversationText.String())
	if err != nil {
		logger.Warn("Failed to summarize context, using original: %v", err)
		return "", fmt.Errorf("failed to summarize context: %w", err)
	}

	logger.Debug("Summarized context: %d chars -> %d chars", conversationText.Len(), len(summary))
	return summary, nil
}
