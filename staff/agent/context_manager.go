package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/aschepis/backscratcher/staff/logger"
)

// ContextManager handles context management operations like reset and compression.
type ContextManager struct {
	messagePersister MessagePersister
}

// NewContextManager creates a new ContextManager.
func NewContextManager(messagePersister MessagePersister) *ContextManager {
	return &ContextManager{
		messagePersister: messagePersister,
	}
}

// GetContextSize calculates the total character count of the conversation context.
// This includes the system prompt and all message content (text blocks, tool use blocks, and tool result blocks).
func GetContextSize(systemPrompt string, messages []anthropic.MessageParam) int {
	totalLength := len(systemPrompt)

	for _, msg := range messages {
		for _, blockUnion := range msg.Content {
			// Check for text blocks
			if blockUnion.OfText != nil {
				totalLength += len(blockUnion.OfText.Text)
			}
			// Check for tool use blocks
			if blockUnion.OfToolUse != nil {
				// Include tool name
				totalLength += len(blockUnion.OfToolUse.Name)
				// Include tool input JSON
				if blockUnion.OfToolUse.Input != nil {
					if inputBytes, err := json.Marshal(blockUnion.OfToolUse.Input); err == nil {
						totalLength += len(inputBytes)
					}
				}
			}
			// Check for tool result blocks
			if blockUnion.OfToolResult != nil {
				// Include tool result content
				totalLength += len(blockUnion.OfToolResult.Content)
			}
		}
	}

	return totalLength
}

// ShouldAutoCompress checks if the context size exceeds 1,000,000 characters.
func ShouldAutoCompress(systemPrompt string, messages []anthropic.MessageParam) bool {
	size := GetContextSize(systemPrompt, messages)
	return size >= 1000000
}

// ResetContext clears the context by inserting a system message marking the reset.
// The history remains in the database but the context is effectively cleared for future messages.
func (cm *ContextManager) ResetContext(ctx context.Context, agentID, threadID string) error {
	if cm.messagePersister == nil {
		return fmt.Errorf("message persister is required for context reset")
	}

	// Create system message content
	systemMsg := map[string]interface{}{
		"type":      "reset",
		"message":   "Context was reset",
		"timestamp": time.Now().Unix(),
	}

	contentJSON, err := json.Marshal(systemMsg)
	if err != nil {
		return fmt.Errorf("failed to marshal system message: %w", err)
	}

	// Use the message persister to save the system message
	// We'll need to add AppendSystemMessage to the MessagePersister interface
	// For now, we'll use a workaround by checking if the persister has the method
	if systemPersister, ok := cm.messagePersister.(interface {
		AppendSystemMessage(ctx context.Context, agentID, threadID, content string, breakType string) error
	}); ok {
		return systemPersister.AppendSystemMessage(ctx, agentID, threadID, string(contentJSON), "reset")
	}

	return fmt.Errorf("message persister does not support system messages")
}

// CompressContext summarizes the entire context and inserts a system message marking the compression.
func (cm *ContextManager) CompressContext(
	ctx context.Context,
	agentID, threadID string,
	systemPrompt string,
	messages []anthropic.MessageParam,
	summarizer *MessageSummarizer,
) (string, error) {
	// Calculate original size
	originalSize := GetContextSize(systemPrompt, messages)

	// Summarize the context
	summary, err := summarizer.SummarizeContext(ctx, systemPrompt, messages)
	if err != nil {
		return "", fmt.Errorf("failed to summarize context: %w", err)
	}

	// Calculate compressed size (approximate - just the summary length)
	compressedSize := len(summary)

	// Create system message content
	systemMsg := map[string]interface{}{
		"type":            "compress",
		"message":         fmt.Sprintf("Context compressed: %s", summary),
		"timestamp":       time.Now().Unix(),
		"original_size":   originalSize,
		"compressed_size": compressedSize,
	}

	contentJSON, err := json.Marshal(systemMsg)
	if err != nil {
		return "", fmt.Errorf("failed to marshal system message: %w", err)
	}

	// Save the system message
	if systemPersister, ok := cm.messagePersister.(interface {
		AppendSystemMessage(ctx context.Context, agentID, threadID, content string, breakType string) error
	}); ok {
		if err := systemPersister.AppendSystemMessage(ctx, agentID, threadID, string(contentJSON), "compress"); err != nil {
			return "", fmt.Errorf("failed to save system message: %w", err)
		}
	} else {
		return "", fmt.Errorf("message persister does not support system messages")
	}

	logger.Info("Context compressed for agent %s, thread %s: %d chars -> %d chars", agentID, threadID, originalSize, compressedSize)

	return summary, nil
}
