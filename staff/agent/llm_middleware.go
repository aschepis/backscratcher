package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/aschepis/backscratcher/staff/config"
	"github.com/aschepis/backscratcher/staff/llm"
	"github.com/aschepis/backscratcher/staff/logger"
	"github.com/rs/zerolog"
)

// RateLimitMiddleware handles rate limit errors and retries.
type RateLimitMiddleware struct {
	logger           zerolog.Logger
	rateLimitHandler *RateLimitHandler
	agentID          string
	agentConfig      *config.AgentConfig
}

// NewRateLimitMiddleware creates a new RateLimitMiddleware.
func NewRateLimitMiddleware(logger zerolog.Logger, rateLimitHandler *RateLimitHandler, agentID string, agentConfig *config.AgentConfig) *RateLimitMiddleware {
	return &RateLimitMiddleware{
		logger:           logger.With().Str("component", "rateLimitMiddleware").Logger(),
		rateLimitHandler: rateLimitHandler,
		agentID:          agentID,
		agentConfig:      agentConfig,
	}
}

// BeforeRequest implements llm.Middleware.BeforeRequest.
func (m *RateLimitMiddleware) BeforeRequest(ctx context.Context, req *llm.Request) (*llm.Request, error) {
	return req, nil
}

// AfterResponse implements llm.Middleware.AfterResponse.
func (m *RateLimitMiddleware) AfterResponse(ctx context.Context, req *llm.Request, resp *llm.Response) (*llm.Response, error) {
	return resp, nil
}

// OnError implements llm.Middleware.OnError.
func (m *RateLimitMiddleware) OnError(ctx context.Context, req *llm.Request, err error) error {
	if err == nil {
		return nil
	}

	// Check if this is a rate limit error
	if !isAnthropicRateLimitError(err) {
		return err
	}

	if m.rateLimitHandler == nil {
		return err
	}

	// Extract retry-after from error
	retryAfter := extractRetryAfterFromError(err)

	// Handle rate limit
	delay, shouldRetry, handlerErr := m.rateLimitHandler.HandleRateLimit(ctx, m.agentID, err, 0, nil)
	if handlerErr != nil {
		return fmt.Errorf("rate limit handler error: %w", handlerErr)
	}

	if !shouldRetry {
		// Max retries exceeded - schedule retry using next_wake for scheduled agents
		if m.agentConfig != nil && m.agentConfig.Schedule != "" {
			if retryAfter == 0 {
				retryAfter = delay
			}
			if retryAfter == 0 {
				retryAfter = 60 // Default 60 seconds
			}
			if scheduleErr := m.rateLimitHandler.ScheduleRetryWithNextWake(m.agentID, retryAfter); scheduleErr != nil {
				m.logger.Warn().Err(scheduleErr).Msg("Failed to schedule retry via next_wake")
			} else {
				m.logger.Info().Str("agentID", m.agentID).Int64("retryAfter", retryAfter.Milliseconds()).Msg("Rate limit exceeded for agent. Scheduled retry via next_wake")
				return fmt.Errorf("rate limit exceeded: agent will retry at scheduled time: %w", err)
			}
		}
		return fmt.Errorf("rate limit: max retries exceeded: %w", err)
	}

	// Wait for retry delay
	if waitErr := m.rateLimitHandler.WaitForRetry(ctx, delay); waitErr != nil {
		return fmt.Errorf("context cancelled while waiting for rate limit retry: %w", waitErr)
	}

	// Return error to trigger retry
	return fmt.Errorf("rate limit: %w", err)
}

// BeforeStream implements llm.StreamMiddleware.BeforeStream.
func (m *RateLimitMiddleware) BeforeStream(ctx context.Context, req *llm.Request) (*llm.Request, error) {
	return req, nil
}

// OnStreamEvent implements llm.StreamMiddleware.OnStreamEvent.
func (m *RateLimitMiddleware) OnStreamEvent(ctx context.Context, req *llm.Request, event *llm.StreamEvent) (*llm.StreamEvent, error) {
	return event, nil
}

// OnStreamError implements llm.StreamMiddleware.OnStreamError.
func (m *RateLimitMiddleware) OnStreamError(ctx context.Context, req *llm.Request, err error) error {
	return m.OnError(ctx, req, err)
}

// CompressionMiddleware handles automatic context compression.
type CompressionMiddleware struct {
	logger            zerolog.Logger
	messagePersister  MessagePersister
	messageSummarizer *MessageSummarizer
	agentID           string
	systemPrompt      string
}

// NewCompressionMiddleware creates a new CompressionMiddleware.
func NewCompressionMiddleware(
	logger zerolog.Logger,
	messagePersister MessagePersister,
	messageSummarizer *MessageSummarizer,
	agentID string,
	systemPrompt string,
) *CompressionMiddleware {
	return &CompressionMiddleware{
		logger:            logger.With().Str("component", "compressionMiddleware").Logger(),
		messagePersister:  messagePersister,
		messageSummarizer: messageSummarizer,
		agentID:           agentID,
		systemPrompt:      systemPrompt,
	}
}

// BeforeRequest implements llm.Middleware.BeforeRequest.
func (m *CompressionMiddleware) BeforeRequest(ctx context.Context, req *llm.Request) (*llm.Request, error) {
	// Check if compression is needed using llm.Message types directly
	if shouldAutoCompress(m.systemPrompt, req.Messages) {
		m.logger.Info().Str("agentID", m.agentID).Msg("Automatic compression triggered for agent: context size exceeds 1,000,000 characters")

		// Compress context
		compressedMsgs, compressErr := m.compressContext(ctx, req.Messages)
		if compressErr != nil {
			m.logger.Warn().Err(compressErr).Msg("Failed to compress context automatically")
			return req, nil // Continue with original if compression fails
		}

		// Update request with compressed messages
		req.Messages = compressedMsgs
	}

	return req, nil
}

// AfterResponse implements llm.Middleware.AfterResponse.
func (m *CompressionMiddleware) AfterResponse(ctx context.Context, req *llm.Request, resp *llm.Response) (*llm.Response, error) {
	return resp, nil
}

// OnError implements llm.Middleware.OnError.
func (m *CompressionMiddleware) OnError(ctx context.Context, req *llm.Request, err error) error {
	if err == nil {
		return nil
	}

	// Check if this is a 413 error (request too large)
	if !is413Error(err) {
		return err
	}

	m.logger.Info().Str("agentID", m.agentID).Msg("Automatic compression triggered for agent: API returned 413 request_too_large")

	// Compress context using llm.Message types directly
	compressedMsgs, compressErr := m.compressContext(ctx, req.Messages)
	if compressErr != nil {
		return fmt.Errorf("compression after 413 error failed: %w", compressErr)
	}

	// Update request with compressed messages
	req.Messages = compressedMsgs

	// Return error to trigger retry with compressed context
	return fmt.Errorf("request too large, retrying with compressed context: %w", err)
}

// compressContext compresses the context by summarizing it.
// Uses provider-neutral llm.Message types.
// TODO: threadID should be passed through request context or metadata
func (m *CompressionMiddleware) compressContext(ctx context.Context, msgs []llm.Message) ([]llm.Message, error) {
	if m.messageSummarizer == nil {
		return nil, fmt.Errorf("summarizer not available")
	}

	if m.messagePersister == nil {
		return nil, fmt.Errorf("message persister not available")
	}

	// TODO: Get threadID from context or request metadata
	// For now, use empty string - compression will work but threadID won't be set correctly
	threadID := ""

	// Use ContextManager to compress
	cm := NewContextManager(logger.GetLogger(), m.messagePersister)
	summary, err := cm.CompressContext(ctx, m.agentID, threadID, m.systemPrompt, msgs, m.messageSummarizer)
	if err != nil {
		return nil, fmt.Errorf("failed to compress context: %w", err)
	}

	// Return a new message list with just the summary as a user message
	// This effectively replaces all previous messages with the summary
	summaryMsg := llm.NewTextMessage(llm.RoleUser, fmt.Sprintf("Previous conversation summary: %s", summary))
	return []llm.Message{summaryMsg}, nil
}

// Helper functions

// isAnthropicRateLimitError checks if an error is a rate limit error from Anthropic.
func isAnthropicRateLimitError(err error) bool {
	if err == nil {
		return false
	}

	errStr := err.Error()
	// Check for common 429 error indicators
	return strings.Contains(errStr, "429") ||
		strings.Contains(errStr, "rate_limit") ||
		strings.Contains(errStr, "rate limit") ||
		strings.Contains(errStr, "Too Many Requests") ||
		strings.Contains(errStr, "Rate limit exceeded")
}

// is413Error checks if an error is a 413 request_too_large error.
func is413Error(err error) bool {
	if err == nil {
		return false
	}

	errStr := err.Error()
	// Check for common 413 error indicators
	return strings.Contains(errStr, "413") ||
		strings.Contains(errStr, "request_too_large") ||
		strings.Contains(errStr, "Request Entity Too Large") ||
		strings.Contains(errStr, "payload too large")
}

// extractRetryAfterFromError extracts retry-after duration from an error.
func extractRetryAfterFromError(err error) time.Duration {
	// Try to extract from llm.Error
	if retryAfter := llm.ExtractRetryAfter(err); retryAfter != nil {
		return *retryAfter
	}

	// Default retry after duration if not specified
	return 60 * time.Second
}

// shouldAutoCompress checks if the context size exceeds 1,000,000 characters.
// Uses provider-neutral llm.Message types.
func shouldAutoCompress(systemPrompt string, messages []llm.Message) bool {
	size := getContextSize(systemPrompt, messages)
	return size >= 1000000
}

// getContextSize calculates the total character count of the conversation context.
// Uses provider-neutral llm.Message types.
func getContextSize(systemPrompt string, messages []llm.Message) int {
	totalLength := len(systemPrompt)

	for _, msg := range messages {
		for _, block := range msg.Content {
			switch block.Type {
			case llm.ContentBlockTypeText:
				totalLength += len(block.Text)
			case llm.ContentBlockTypeToolUse:
				if block.ToolUse != nil {
					// Include tool name
					totalLength += len(block.ToolUse.Name)
					// Include tool input JSON
					if block.ToolUse.Input != nil {
						if inputBytes, err := json.Marshal(block.ToolUse.Input); err == nil {
							totalLength += len(inputBytes)
						}
					}
				}
			case llm.ContentBlockTypeToolResult:
				if block.ToolResult != nil {
					// Include tool result content
					totalLength += len(block.ToolResult.Content)
				}
			}
		}
	}

	return totalLength
}
