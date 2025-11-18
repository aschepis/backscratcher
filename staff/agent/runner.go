package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/aschepis/backscratcher/staff/logger"
	"github.com/cenkalti/backoff/v4"
)

// ToolExecutor is whatever you already had for running tools.
// Kept here just for clarity.
// debugCallback is retrieved from context if needed.
type ToolExecutor interface {
	Handle(ctx context.Context, toolName, agentID string, inputJSON []byte) (any, error)
}

// MessagePersister provides an interface for persisting conversation messages.
type MessagePersister interface {
	// AppendUserMessage saves a user text message to the conversation history.
	AppendUserMessage(ctx context.Context, agentID, threadID, content string) error

	// AppendAssistantMessage saves an assistant text-only message to the conversation history.
	AppendAssistantMessage(ctx context.Context, agentID, threadID, content string) error

	// AppendToolCall saves an assistant message with tool use blocks to the conversation history.
	AppendToolCall(ctx context.Context, agentID, threadID, toolID, toolName string, toolInput any) error

	// AppendToolResult saves a tool result message to the conversation history.
	AppendToolResult(ctx context.Context, agentID, threadID, toolID, toolName string, result any, isError bool) error
}

type AgentRunner struct {
	client            *anthropic.Client
	agent             *Agent
	toolExec          ToolExecutor
	toolProvider      ToolProvider
	stateManager      *StateManager
	statsManager      *StatsManager
	messagePersister  MessagePersister   // Optional message persister
	messageSummarizer *MessageSummarizer // Optional message summarizer
	rateLimitHandler  *RateLimitHandler  // Rate limit handler
}

// toolResultData holds the result data for a tool call
type toolResultData struct {
	result  any
	isError bool
}

// summarizeToolResult summarizes the content of a tool result if it exceeds thresholds.
// It extracts text from the result (handling strings, maps, slices) and summarizes if needed.
func (r *AgentRunner) summarizeToolResult(ctx context.Context, result any) (any, error) {
	if r.messageSummarizer == nil {
		return result, nil
	}

	// Extract text content from result
	var textContent string
	switch v := result.(type) {
	case string:
		textContent = v
	case []byte:
		textContent = string(v)
	case map[string]any:
		// Try to find common text fields
		if content, ok := v["content"].(string); ok {
			textContent = content
		} else if text, ok := v["text"].(string); ok {
			textContent = text
		} else if message, ok := v["message"].(string); ok {
			textContent = message
		} else {
			// Marshal to JSON and use that as text
			if b, err := json.Marshal(v); err == nil {
				textContent = string(b)
			} else {
				return result, nil // Can't extract, return as-is
			}
		}
	case []any:
		// For slices, try to extract text from each element
		var parts []string
		for _, item := range v {
			if str, ok := item.(string); ok {
				parts = append(parts, str)
			} else if b, err := json.Marshal(item); err == nil {
				parts = append(parts, string(b))
			}
		}
		textContent = strings.Join(parts, "\n")
	default:
		// For other types, marshal to JSON
		if b, err := json.Marshal(result); err == nil {
			textContent = string(b)
		} else {
			return result, nil // Can't extract, return as-is
		}
	}

	// Check if summarization is needed
	if !r.messageSummarizer.ShouldSummarize(textContent) {
		return result, nil
	}

	// Summarize the text
	summary, err := r.messageSummarizer.Summarize(ctx, textContent)
	if err != nil {
		// If summarization fails, return original
		return result, err
	}

	// Replace the text content with summary
	switch v := result.(type) {
	case string:
		return summary, nil
	case []byte:
		return []byte(summary), nil
	case map[string]any:
		// Replace content/text/message fields with summary
		resultCopy := make(map[string]any)
		for k, val := range v {
			resultCopy[k] = val
		}
		if _, ok := resultCopy["content"]; ok {
			resultCopy["content"] = summary
		} else if _, ok := resultCopy["text"]; ok {
			resultCopy["text"] = summary
		} else if _, ok := resultCopy["message"]; ok {
			resultCopy["message"] = summary
		} else {
			// Add as "summary" field
			resultCopy["summary"] = summary
		}
		return resultCopy, nil
	case []any:
		// Replace slice with single summary string
		return []any{summary}, nil
	default:
		// For other types, return as string
		return summary, nil
	}
}

// NewAgentRunner creates a new AgentRunner with all required dependencies.
func NewAgentRunner(
	apiKey string,
	agent *Agent,
	toolExec ToolExecutor,
	toolProvider ToolProvider,
	stateManager *StateManager,
	statsManager *StatsManager,
	messagePersister MessagePersister,
	messageSummarizer *MessageSummarizer,
) (*AgentRunner, error) {
	if stateManager == nil {
		return nil, fmt.Errorf("stateManager is required for AgentRunner")
	}
	if statsManager == nil {
		return nil, fmt.Errorf("statsManager is required for AgentRunner")
	}
	if messagePersister == nil {
		return nil, fmt.Errorf("messagePersister is required for AgentRunner")
	}
	if messageSummarizer == nil {
		return nil, fmt.Errorf("messageSummarizer is required for AgentRunner")
	}
	client := anthropic.NewClient(option.WithAPIKey(apiKey))
	rateLimitHandler := NewRateLimitHandler(stateManager)

	// Set callback to notify users about rate limits
	rateLimitHandler.SetOnRateLimitCallback(func(agentID string, retryAfter time.Duration, attempt int) error {
		logger.Info("Rate limit callback: agent %s will retry after %v (attempt %d)", agentID, retryAfter, attempt)
		return nil
	})

	return &AgentRunner{
		client:            &client,
		agent:             agent,
		toolExec:          toolExec,
		toolProvider:      toolProvider,
		stateManager:      stateManager,
		statsManager:      statsManager,
		messagePersister:  messagePersister,
		messageSummarizer: messageSummarizer,
		rateLimitHandler:  rateLimitHandler,
	}, nil
}

// buildSystemBlocks creates system text blocks with prompt caching enabled if appropriate.
// According to Anthropic's prompt caching documentation, placing cache_control on the system block
// caches the full prefix: tools, system, and messages (in that order) up to and including the
// block designated with cache_control. This means tools are automatically cached along with the system prompt.
//
// Prompt caching is enabled when the combined size of tools + system is at least 4000 characters
// (roughly equivalent to ~1000 tokens, meeting Anthropic's 1024 token minimum requirement).
// This helps reduce costs and latency for repeated requests with the same tools and system prompt.
func buildSystemBlocks(systemPrompt string, tools []anthropic.ToolUnionParam) []anthropic.TextBlockParam {
	// Anthropic requires minimum 1,024 tokens for caching, which is roughly 4,000 characters
	// (using a rough estimate of 1 token ≈ 4 characters). We use 4000 characters as a safe
	// threshold to ensure we meet the minimum token requirement across different tokenization methods.
	const minCacheableLength = 4000

	blocks := []anthropic.TextBlockParam{
		{Text: systemPrompt},
	}

	// Calculate approximate size of tools by marshaling to JSON
	toolsSize := 0
	if len(tools) > 0 {
		if toolsJSON, err := json.Marshal(tools); err == nil {
			toolsSize = len(toolsJSON)
		}
	}

	// Combined size of tools + system prompt
	combinedSize := toolsSize + len(systemPrompt)

	// Enable ephemeral caching if the combined static content (tools + system) is large enough
	// When cache_control is placed on the system block, it caches both tools and system together
	if combinedSize >= minCacheableLength {
		// Create cache control with default 5-minute TTL
		cacheControl := anthropic.NewCacheControlEphemeralParam()
		blocks[0].CacheControl = cacheControl
	}

	return blocks
}

// logDetailedError logs detailed error information from Anthropic API errors
func logDetailedError(agentID string, err error, debugCallback DebugCallback, context string) {
	if err == nil {
		return
	}

	// Get the full error string
	errStr := err.Error()

	// Try to extract structured error information if available
	// The Anthropic SDK may wrap errors with additional context
	errDetails := fmt.Sprintf("Anthropic API error for agent %s (%s): %s", agentID, context, errStr)

	// Log the detailed error
	logger.Error("%s", errDetails)

	// Also send to debug callback if available
	if debugCallback != nil {
		debugCallback(fmt.Sprintf("ERROR: %s", errDetails))
	}

	// Try to extract additional error context using type assertions
	// Check if error has additional fields we can log
	if errStr != "" {
		// Log the full error message which may contain API response details
		logger.Debug("Full error details for agent %s: %+v", agentID, err)
	}
}

// trackExecutionStats records execution statistics (success or failure) for the agent
func (r *AgentRunner) trackExecutionStats(successful bool, errorMsg string) {
	if !successful {
		// Track failure if execution was not successful
		if errorMsg != "" {
			if updateErr := r.statsManager.IncrementFailureCount(r.agent.ID, errorMsg); updateErr != nil {
				logger.Warn("failed to update failure stats: %v", updateErr)
			}
		}
	} else {
		// Track successful execution
		if updateErr := r.statsManager.IncrementExecutionCount(r.agent.ID); updateErr != nil {
			logger.Warn("failed to update execution stats: %v", updateErr)
		}
	}
}

// updateAgentStateAfterExecution updates the agent state after execution completes,
// handling scheduled agents by computing next wake time or setting to idle
func (r *AgentRunner) updateAgentStateAfterExecution(executionSuccessful bool, executionError string) {
	// Track execution completion or failure
	r.trackExecutionStats(executionSuccessful, executionError)

	// Check if agent has a schedule - if so, compute next wake and set to waiting_external
	// Otherwise, set to idle
	if r.agent.Config.Schedule != "" && !r.agent.Config.Disabled {
		// Agent is scheduled, compute next wake time
		now := time.Now()
		nextWake, err := ComputeNextWake(r.agent.Config.Schedule, now)
		if err != nil {
			logger.Warn("failed to compute next wake for agent %s: %v", r.agent.ID, err)
			// Fall back to idle on error
			if err := r.stateManager.SetState(r.agent.ID, StateIdle); err != nil {
				logger.Warn("failed to set agent state to idle: %v", err)
			}
			return
		}
		// Set state to waiting_external with next_wake
		if err := r.stateManager.SetStateWithNextWake(r.agent.ID, StateWaitingExternal, &nextWake); err != nil {
			logger.Warn("failed to set agent state to waiting_external: %v", err)
		}
	} else {
		// Agent is not scheduled, set to idle
		if err := r.stateManager.SetState(r.agent.ID, StateIdle); err != nil {
			logger.Warn("failed to set agent state to idle: %v", err)
		}
	}
}

// AgentConfig is the per-agent config you already have.
type AgentConfig struct {
	ID           string   `yaml:"id" json:"id"`
	Name         string   `yaml:"name" json:"name"`
	System       string   `yaml:"system_prompt" json:"system"`
	Model        string   `yaml:"model" json:"model"`
	MaxTokens    int64    `yaml:"max_tokens" json:"max_tokens"`
	Tools        []string `yaml:"tools" json:"tools"`
	Schedule     string   `yaml:"schedule" json:"schedule"`           // e.g., "15m", "2h", "0 */15 * * * *" (cron)
	Disabled     bool     `yaml:"disabled" json:"disabled"`           // default: false (agent is enabled by default)
	StartupDelay string   `yaml:"startup_delay" json:"startup_delay"` // e.g., "5m", "30s", "1h" - one-time delay after app launch
}

// calculateInputTextLength calculates the total length of input text being sent to Anthropic.
// This includes the system prompt and all message content (text blocks, tool use blocks, and tool result blocks).
func (r *AgentRunner) calculateInputTextLength(msgs []anthropic.MessageParam) int {
	totalLength := len(r.agent.Config.System)

	for _, msg := range msgs {
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

// RunAgent executes a single turn for an agent, with optional history.
// debugCallback is retrieved from context if available.
func (r *AgentRunner) RunAgent(
	ctx context.Context,
	threadID string,
	userMsg string,
	history []anthropic.MessageParam,
) (string, error) {
	if r.agent == nil {
		return "", errors.New("agent is nil")
	}
	if r.agent.Config.Model == "" {
		return "", errors.New("agent.Model is required")
	}

	// Set state to running at start of execution
	if err := r.stateManager.SetState(r.agent.ID, StateRunning); err != nil {
		// Log error but don't fail execution
		logger.Warn("failed to set agent state to running: %v", err)
	}

	// Track if execution was successful
	executionSuccessful := false
	executionError := ""

	// Ensure state is updated when execution completes (normal or error)
	defer func() {
		r.updateAgentStateAfterExecution(executionSuccessful, executionError)
	}()

	// Get debug callback from context
	debugCallback, _ := GetDebugCallback(ctx)

	// Conversation so far
	msgs := append([]anthropic.MessageParam{}, history...)
	msgs = append(msgs,
		anthropic.NewUserMessage(anthropic.NewTextBlock(userMsg)),
	)

	tools := r.toolProvider.SpecsFor(r.agent.Config)

	for {
		// Call API with retry logic and rate limit handling
		// Compression is handled inside callAnthropicAPIWithRetry
		messagePtr, err := r.callAnthropicAPIWithRetry(ctx, threadID, msgs, tools, debugCallback, 5)
		if err != nil {
			// Check if this is a rate limit error that was scheduled for retry
			if IsRateLimitError(err) && strings.Contains(err.Error(), "will retry at scheduled time") {
				// This is expected - agent will retry later via scheduler
				executionError = err.Error()
				return "", err
			}
			executionError = err.Error()
			return "", err
		}
		message := *messagePtr

		// Accumulate tool uses + any plain text
		var (
			finalText     strings.Builder
			toolResults   []anthropic.ContentBlockParamUnion
			toolNameMap   = make(map[string]string)         // Map tool ID to tool name for persistence
			toolResultMap = make(map[string]toolResultData) // Map tool ID to result data for persistence
		)

		for _, blockUnion := range message.Content {
			switch block := blockUnion.AsAny().(type) {

			case anthropic.TextBlock:
				finalText.WriteString(block.Text)
				finalText.WriteRune('\n')

			case anthropic.ToolUseBlock:
				// Get raw JSON input the way the official example does.
				raw := []byte(block.JSON.Input.Raw())

				// Track tool name for persistence
				toolNameMap[block.ID] = block.Name

				// Execute tool (debug callback is retrieved from context if needed)
				result, callErr := r.toolExec.Handle(ctx, block.Name, r.agent.ID, raw)
				if callErr != nil {
					// Return error payload to the model
					result = map[string]any{"error": callErr.Error()}
				}

				// Summarize result if needed (before marshaling to JSON)
				summarizedResult, summarizeErr := r.summarizeToolResult(ctx, result)
				if summarizeErr != nil {
					logger.Warn("Failed to summarize tool result, using original: %v", summarizeErr)
					summarizedResult = result
				}

				// Track original result for persistence (before summarization)
				toolResultMap[block.ID] = toolResultData{result: result, isError: callErr != nil}

				b, _ := json.Marshal(summarizedResult)
				isError := callErr != nil
				toolResults = append(
					toolResults,
					anthropic.NewToolResultBlock(block.ID, string(b), isError),
				)
			}
		}

		// Add the assistant message to history
		assistantMsg := message.ToParam()
		msgs = append(msgs, assistantMsg)

		// Persist assistant message if we have tool calls
		if len(toolResults) > 0 && r.messagePersister != nil {
			// Save assistant message with tool calls
			for _, blockUnion := range message.Content {
				if toolUse, ok := blockUnion.AsAny().(anthropic.ToolUseBlock); ok {
					var toolInput any
					raw := []byte(toolUse.JSON.Input.Raw())
					if err := json.Unmarshal(raw, &toolInput); err != nil {
						// JSON parsing failed - this is an error condition
						logger.Warn("failed to parse tool input JSON for persistence (tool %s, id: %s): %v, raw JSON: %s", toolUse.Name, toolUse.ID, err, string(raw))
						// Use empty object as last resort
						toolInput = map[string]any{}
					}
					if err := r.messagePersister.AppendToolCall(ctx, r.agent.ID, threadID, toolUse.ID, toolUse.Name, toolInput); err != nil {
						// Log error but don't fail execution
						logger.Warn("failed to persist tool call: %v", err)
					}
				}
			}
		}

		// If no tool calls, we're done.
		if len(toolResults) == 0 {
			// Summarize final text if needed
			finalTextStr := strings.TrimSpace(finalText.String())
			if r.messageSummarizer != nil && r.messageSummarizer.ShouldSummarize(finalTextStr) {
				summarized, err := r.messageSummarizer.Summarize(ctx, finalTextStr)
				if err != nil {
					logger.Warn("Failed to summarize assistant message, using original: %v", err)
				} else {
					finalTextStr = summarized
				}
			}

			// Persist assistant text message if we have a persister
			if r.messagePersister != nil && finalTextStr != "" {
				if err := r.messagePersister.AppendAssistantMessage(ctx, r.agent.ID, threadID, finalTextStr); err != nil {
					// Log error but don't fail execution
					logger.Warn("failed to persist assistant message: %v", err)
				}
			}
			executionSuccessful = true
			return finalTextStr, nil
		}

		// Persist tool results
		if r.messagePersister != nil {
			for toolID, resultData := range toolResultMap {
				toolName := toolNameMap[toolID]
				if err := r.messagePersister.AppendToolResult(ctx, r.agent.ID, threadID, toolID, toolName, resultData.result, resultData.isError); err != nil {
					// Log error but don't fail execution
					logger.Warn("failed to persist tool result: %v", err)
				}
			}
		}

		// Otherwise, send tool results back as a user message and loop.
		msgs = append(msgs, anthropic.NewUserMessage(toolResults...))
	}
}

// RunAgentStream executes a single turn for an agent with streaming support.
// It calls the callback function for each text delta received.
// debugCallback is retrieved from context if available.
func (r *AgentRunner) RunAgentStream(
	ctx context.Context,
	threadID string,
	userMsg string,
	history []anthropic.MessageParam,
	callback StreamCallback,
) (string, error) {
	if r.agent == nil {
		return "", errors.New("agent is nil")
	}
	if r.agent.Config.Model == "" {
		return "", errors.New("agent.Model is required")
	}

	// Set state to running at start of execution
	if err := r.stateManager.SetState(r.agent.ID, StateRunning); err != nil {
		// Log error but don't fail execution
		logger.Warn("failed to set agent state to running: %v", err)
	}

	// Track if execution was successful
	executionSuccessful := false
	executionError := ""

	// Ensure state is updated when execution completes (normal or error)
	defer func() {
		r.updateAgentStateAfterExecution(executionSuccessful, executionError)
	}()

	// Get debug callback from context
	debugCallback, _ := GetDebugCallback(ctx)

	// Conversation so far
	msgs := append([]anthropic.MessageParam{}, history...)
	msgs = append(msgs,
		anthropic.NewUserMessage(anthropic.NewTextBlock(userMsg)),
	)

	tools := r.toolProvider.SpecsFor(r.agent.Config)

	var fullResponse strings.Builder

	for {
		// Calculate input text length
		inputLength := r.calculateInputTextLength(msgs)

		// Check if automatic compression is needed (before API call)
		if ShouldAutoCompress(r.agent.Config.System, msgs) {
			if debugCallback != nil {
				debugCallback("AUTOMATIC COMPRESSION: Context size exceeds 1,000,000 characters, compressing...")
			}
			logger.Info("Automatic compression triggered for agent %s: context size exceeds 1,000,000 characters", r.agent.ID)

			compressedMsgs, compressErr := r.compressContext(ctx, threadID, msgs, debugCallback)
			if compressErr != nil {
				logger.Warn("Failed to compress context automatically: %v", compressErr)
				// Continue with original messages if compression fails
			} else {
				msgs = compressedMsgs
				inputLength = r.calculateInputTextLength(msgs)
				if debugCallback != nil {
					debugCallback(fmt.Sprintf("Context compressed: %d chars", inputLength))
				}
			}
		}

		// Debug: Show API call about to be made
		if debugCallback != nil {
			debugCallback(fmt.Sprintf("Calling Anthropic API (model: %s, %d messages in history, input length: %d chars)", r.agent.Config.Model, len(msgs), inputLength))
		}

		// Log API call with input length
		logger.Info("Calling Anthropic API for agent %s (model: %s, %d messages in history, input length: %d chars)", r.agent.ID, r.agent.Config.Model, len(msgs), inputLength)

		// Create streaming request
		stream := r.client.Messages.NewStreaming(ctx, anthropic.MessageNewParams{
			Model:     anthropic.Model(r.agent.Config.Model),
			MaxTokens: r.agent.Config.MaxTokens,
			Messages:  msgs,
			System:    buildSystemBlocks(r.agent.Config.System, tools),
			Tools:     tools,
		})

		// Process stream
		var (
			textBuilder    strings.Builder
			hasText        bool
			toolUseBlocks  []anthropic.ToolUseBlock
			currentToolUse *anthropic.ToolUseBlock
			toolInputs     map[string]strings.Builder // Map tool ID to input builder
		)

		toolInputs = make(map[string]strings.Builder)

		streamComplete := false
		for stream.Next() {
			event := stream.Current()

			// Use type switch to handle different event types
			switch evt := event.AsAny().(type) {
			case anthropic.MessageStartEvent:
				// Message started - reset state
				textBuilder.Reset()
				hasText = false
				toolUseBlocks = nil
				currentToolUse = nil
				toolInputs = make(map[string]strings.Builder)

			case anthropic.ContentBlockStartEvent:
				// Content block started - check if it's tool use or text
				contentBlock := evt.ContentBlock
				if toolUse, ok := contentBlock.AsAny().(anthropic.ToolUseBlock); ok {
					// Store the tool use block
					toolUseBlocks = append(toolUseBlocks, toolUse)
					currentToolUse = &toolUse
					toolInputs[toolUse.ID] = strings.Builder{}
					// Debug: Show tool invocation starting
					if debugCallback != nil {
						debugCallback(fmt.Sprintf("Invoking tool: %s (id: %s)", toolUse.Name, toolUse.ID))
					}
				}

			case anthropic.ContentBlockDeltaEvent:
				// Content delta - could be text or tool input JSON
				delta := evt.Delta
				switch deltaType := delta.AsAny().(type) {
				case anthropic.TextDelta:
					// Text delta
					if deltaType.Text != "" {
						hasText = true
						textBuilder.WriteString(deltaType.Text)
						fullResponse.WriteString(deltaType.Text)
						// Call callback with text delta (non-blocking)
						if callback != nil {
							if err := callback(deltaType.Text); err != nil {
								executionError = err.Error()
								return "", fmt.Errorf("callback error: %w", err)
							}
						}
					}
				case anthropic.InputJSONDelta:
					// Tool input JSON delta
					if deltaType.PartialJSON != "" && currentToolUse != nil {
						builder := toolInputs[currentToolUse.ID]
						builder.WriteString(deltaType.PartialJSON)
						toolInputs[currentToolUse.ID] = builder
					}
				}

			case anthropic.ContentBlockStopEvent:
				// Content block finished
				if currentToolUse != nil {
					// Tool block completed - show tool arguments
					if debugCallback != nil {
						if builder, ok := toolInputs[currentToolUse.ID]; ok {
							toolInputStr := builder.String()
							if toolInputStr != "" {
								// Try to pretty-print JSON if possible
								var prettyJSON interface{}
								if err := json.Unmarshal([]byte(toolInputStr), &prettyJSON); err == nil {
									if prettyBytes, err := json.MarshalIndent(prettyJSON, "", "  "); err == nil {
										toolInputStr = string(prettyBytes)
									}
								}
								debugCallback(fmt.Sprintf("Tool arguments: %s", toolInputStr))
							}
						}
					}
					currentToolUse = nil
				}

			case anthropic.MessageDeltaEvent:
				// Message delta - can contain stop reason, but we don't need to handle it here
				_ = evt.Delta.StopReason
				// Note: Usage information is not available in MessageDeltaEvent in the current SDK version
				// Cache stats will be logged for non-streaming API calls

			case anthropic.MessageStopEvent:
				// Message finished - stream is complete
				streamComplete = true
			}
		}

		// Note: Cache performance metrics are not available from streaming responses in the current SDK version
		// Cache stats are logged for non-streaming API calls (see callAnthropicAPIWithRetry)

		// Check for stream errors
		if err := stream.Err(); err != nil {
			// Log detailed error information
			logDetailedError(r.agent.ID, err, debugCallback, "streaming API call")

			// Check for 413 error (request too large)
			if r.is413Error(err) {
				if debugCallback != nil {
					debugCallback("AUTOMATIC COMPRESSION: Anthropic API returned 413 request_too_large error, compressing context...")
				}
				logger.Info("Automatic compression triggered for agent %s: Anthropic API returned 413 request_too_large", r.agent.ID)

				compressedMsgs, compressErr := r.compressContext(ctx, threadID, msgs, debugCallback)
				if compressErr != nil {
					logDetailedError(r.agent.ID, compressErr, debugCallback, "compression after 413 error in streaming")
					executionError = fmt.Sprintf("failed to compress context after 413 error: %v", compressErr)
					return "", fmt.Errorf("stream error (413) and compression failed: %w", compressErr)
				}

				// Retry with compressed messages
				msgs = compressedMsgs
				inputLength = r.calculateInputTextLength(msgs)
				if debugCallback != nil {
					debugCallback(fmt.Sprintf("Retrying API call with compressed context (%d chars)", inputLength))
				}
				continue // Loop back to retry with compressed context
			}

			// Check for rate limit error
			if IsRateLimitError(err) {
				if r.rateLimitHandler != nil {
					delay, shouldRetry, handlerErr := r.rateLimitHandler.HandleRateLimit(ctx, r.agent.ID, err, 0, nil)
					if handlerErr != nil {
						logDetailedError(r.agent.ID, handlerErr, debugCallback, "rate limit handler")
						executionError = fmt.Sprintf("rate limit handler error: %v", handlerErr)
						return "", fmt.Errorf("rate limit handler error: %w", handlerErr)
					}

					if !shouldRetry {
						// Max retries exceeded - schedule retry using next_wake for scheduled agents
						if r.agent.Config.Schedule != "" {
							if scheduleErr := r.rateLimitHandler.ScheduleRetryWithNextWake(r.agent.ID, delay); scheduleErr != nil {
								logDetailedError(r.agent.ID, scheduleErr, debugCallback, "scheduling retry via next_wake")
								logger.Warn("Failed to schedule retry via next_wake: %v", scheduleErr)
							} else {
								if debugCallback != nil {
									debugCallback(fmt.Sprintf("Rate limit exceeded. Agent will retry at scheduled time (in %v)", delay))
								}
								logger.Info("Rate limit exceeded for agent %s (streaming). Scheduled retry via next_wake in %v", r.agent.ID, delay)
								executionError = "rate limit exceeded: agent will retry at scheduled time"
								return "", fmt.Errorf("rate limit exceeded: agent will retry at scheduled time: %w", err)
							}
						}
						executionError = "rate limit: max retries exceeded"
						return "", fmt.Errorf("rate limit: max retries exceeded: %w", err)
					}

					// Wait for retry delay
					if debugCallback != nil {
						debugCallback(fmt.Sprintf("Rate limit encountered. Waiting %v before retry...", delay))
					}
					logger.Info("Rate limit encountered for agent %s (streaming). Waiting %v before retry", r.agent.ID, delay)

					if waitErr := r.rateLimitHandler.WaitForRetry(ctx, delay); waitErr != nil {
						logDetailedError(r.agent.ID, waitErr, debugCallback, "waiting for rate limit retry")
						executionError = fmt.Sprintf("context cancelled while waiting for rate limit retry: %v", waitErr)
						return "", fmt.Errorf("context cancelled while waiting for rate limit retry: %w", waitErr)
					}
					continue // Loop back to retry after delay
				}
			}

			executionError = err.Error()
			return "", fmt.Errorf("stream error: %w", err)
		}

		// Check if context was cancelled
		if ctx.Err() != nil {
			executionError = ctx.Err().Error()
			return "", fmt.Errorf("context cancelled: %w", ctx.Err())
		}

		// If we have tool use, execute tools and continue the conversation
		if len(toolUseBlocks) > 0 {
			if debugCallback != nil {
				debugCallback(fmt.Sprintf("Tool use detected, executing %d tool(s)...", len(toolUseBlocks)))
			}

			// Build assistant message content with tool uses and execute tools
			var assistantContent []anthropic.ContentBlockParamUnion
			var toolResults []anthropic.ContentBlockParamUnion
			toolNameMap := make(map[string]string)    // Map tool ID to tool name for persistence
			toolResultMap := make(map[string]struct { // Map tool ID to result data for persistence
				result  any
				isError bool
			})

			for _, toolUse := range toolUseBlocks {
				// Track tool name for persistence
				toolNameMap[toolUse.ID] = toolUse.Name
				// Get the input JSON for this tool
				// Prefer the tool use block's raw JSON as it should be complete and valid
				var inputJSON any
				rawJSON := toolUse.JSON.Input.Raw()
				if err := json.Unmarshal([]byte(rawJSON), &inputJSON); err != nil {
					// JSON parsing failed - this is an error condition
					logger.Warn("failed to parse tool input JSON for tool %s (id: %s): %v, raw JSON: %s", toolUse.Name, toolUse.ID, err, rawJSON)
					// Use empty object as last resort to ensure it's always a dictionary
					inputJSON = map[string]any{}
				}

				// Ensure inputJSON is always a map (dictionary) for the API
				if _, ok := inputJSON.(map[string]any); !ok {
					// If it's not a map, this is unexpected - log and use empty map
					logger.Warn("tool input JSON for tool %s (id: %s) is not a map, got type %T, using empty map", toolUse.Name, toolUse.ID, inputJSON)
					inputJSON = map[string]any{}
				}

				// Create tool use block for assistant message
				assistantContent = append(assistantContent, anthropic.NewToolUseBlock(toolUse.ID, inputJSON, toolUse.Name))

				// Execute tool
				var raw []byte
				if builder, ok := toolInputs[toolUse.ID]; ok {
					raw = []byte(builder.String())
				} else {
					raw = []byte(toolUse.JSON.Input.Raw())
				}

				result, callErr := r.toolExec.Handle(ctx, toolUse.Name, r.agent.ID, raw)
				if callErr != nil {
					result = map[string]any{"error": callErr.Error()}
				}

				// Summarize result if needed (before marshaling to JSON)
				summarizedResult, summarizeErr := r.summarizeToolResult(ctx, result)
				if summarizeErr != nil {
					logger.Warn("Failed to summarize tool result, using original: %v", summarizeErr)
					summarizedResult = result
				}

				// Track original result for persistence (before summarization)
				isError := callErr != nil
				toolResultMap[toolUse.ID] = toolResultData{result: result, isError: isError}

				b, _ := json.Marshal(summarizedResult)
				toolResults = append(toolResults, anthropic.NewToolResultBlock(toolUse.ID, string(b), isError))
			}

			// Persist assistant message with tool calls
			if r.messagePersister != nil {
				for _, toolUse := range toolUseBlocks {
					var toolInput any
					// Prefer the tool use block's raw JSON as it should be complete and valid
					rawJSON := toolUse.JSON.Input.Raw()
					if err := json.Unmarshal([]byte(rawJSON), &toolInput); err != nil {
						// JSON parsing failed - this is an error condition
						logger.Warn("failed to parse tool input JSON for persistence (tool %s, id: %s): %v, raw JSON: %s", toolUse.Name, toolUse.ID, err, rawJSON)
						// Use empty object as last resort
						toolInput = map[string]any{}
					}
					if err := r.messagePersister.AppendToolCall(ctx, r.agent.ID, threadID, toolUse.ID, toolUse.Name, toolInput); err != nil {
						// Log error but don't fail execution
						logger.Warn("failed to persist tool call: %v", err)
					}
				}
			}

			// Add assistant message with tool uses to history
			assistantMsg := anthropic.NewAssistantMessage(assistantContent...)
			msgs = append(msgs, assistantMsg)

			// Persist tool results
			if r.messagePersister != nil {
				for toolID, resultData := range toolResultMap {
					toolName := toolNameMap[toolID]
					if err := r.messagePersister.AppendToolResult(ctx, r.agent.ID, threadID, toolID, toolName, resultData.result, resultData.isError); err != nil {
						// Log error but don't fail execution
						logger.Warn("failed to persist tool result: %v", err)
					}
				}
			}

			// Add tool results as user message and continue loop
			msgs = append(msgs, anthropic.NewUserMessage(toolResults...))
			continue // Loop back to get the next response
		}

		// If we have text, return it
		if hasText && textBuilder.Len() > 0 {
			text := strings.TrimSpace(textBuilder.String())
			// Summarize text if needed
			if r.messageSummarizer != nil && r.messageSummarizer.ShouldSummarize(text) {
				summarized, err := r.messageSummarizer.Summarize(ctx, text)
				if err != nil {
					logger.Warn("Failed to summarize assistant message, using original: %v", err)
				} else {
					text = summarized
				}
			}
			// Persist assistant text message if we have a persister
			if r.messagePersister != nil {
				if err := r.messagePersister.AppendAssistantMessage(ctx, r.agent.ID, threadID, text); err != nil {
					// Log error but don't fail execution
					logger.Warn("failed to persist assistant message: %v", err)
				}
			}
			executionSuccessful = true
			return text, nil
		}

		// If stream completed without content, it might be an empty response
		// This is valid - some responses might be empty
		if streamComplete {
			executionSuccessful = true
			return "", nil
		}

		// If we get here without text or tool use, something went wrong
		executionError = "unexpected stream completion: no text or tool use detected"
		return "", fmt.Errorf("%s", executionError)
	}
}

// compressContext compresses the context by summarizing it and replacing messages with a summary.
// Returns the new compressed message list and any error.
func (r *AgentRunner) compressContext(
	ctx context.Context,
	threadID string,
	msgs []anthropic.MessageParam,
	debugCallback DebugCallback,
) ([]anthropic.MessageParam, error) {
	if r.messageSummarizer == nil {
		return nil, fmt.Errorf("summarizer not available")
	}

	if r.messagePersister == nil {
		return nil, fmt.Errorf("message persister not available")
	}

	// Calculate original size
	originalSize := GetContextSize(r.agent.Config.System, msgs)

	// Summarize the context
	summary, err := r.messageSummarizer.SummarizeContext(ctx, r.agent.Config.System, msgs)
	if err != nil {
		return nil, fmt.Errorf("failed to summarize context: %w", err)
	}

	// Calculate compressed size
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
		return nil, fmt.Errorf("failed to marshal system message: %w", err)
	}

	// Save the system message
	if systemPersister, ok := r.messagePersister.(interface {
		AppendSystemMessage(ctx context.Context, agentID, threadID, content string, breakType string) error
	}); ok {
		if err := systemPersister.AppendSystemMessage(ctx, r.agent.ID, threadID, string(contentJSON), "compress"); err != nil {
			return nil, fmt.Errorf("failed to save system message: %w", err)
		}
	} else {
		return nil, fmt.Errorf("message persister does not support system messages")
	}

	// Log compression
	logger.Info("Context compressed for agent %s, thread %s: %d chars -> %d chars", r.agent.ID, threadID, originalSize, compressedSize)
	if debugCallback != nil {
		debugCallback(fmt.Sprintf("Context compressed: %d chars -> %d chars. Summary: %s", originalSize, compressedSize, summary))
	}

	// Return a new message list with just the summary as a user message
	// This effectively replaces all previous messages with the summary
	return []anthropic.MessageParam{
		anthropic.NewUserMessage(anthropic.NewTextBlock(fmt.Sprintf("Previous conversation summary: %s", summary))),
	}, nil
}

// is413Error checks if an error is a 413 request_too_large error from Anthropic.
func (r *AgentRunner) is413Error(err error) bool {
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

// callAnthropicAPIWithRetry calls the Anthropic API with rate limit handling and retry logic using the backoff library
func (r *AgentRunner) callAnthropicAPIWithRetry(
	ctx context.Context,
	threadID string,
	msgs []anthropic.MessageParam,
	tools []anthropic.ToolUnionParam,
	debugCallback DebugCallback,
	maxRetries int64,
) (*anthropic.Message, error) {
	// Create backoff configuration
	eb := backoff.NewExponentialBackOff()
	eb.InitialInterval = 1 * time.Second
	eb.Multiplier = 2.0
	eb.MaxInterval = 5 * time.Minute
	eb.MaxElapsedTime = 5 * time.Minute
	eb.RandomizationFactor = 0.2 // 20% jitter
	eb.Reset()

	// Limit max retries - handle potential overflow
	var maxRetriesUint uint64
	if maxRetries < 0 {
		maxRetriesUint = 0
	} else {
		maxRetriesUint = uint64(maxRetries)
	}
	backoffConfig := backoff.WithMaxRetries(eb, maxRetriesUint)

	var result *anthropic.Message
	var retryAfter time.Duration
	attemptCount := 0

	// Use a pointer to msgs so modifications persist across retries (for 413 compression)
	currentMsgs := msgs

	operation := func() error {
		attemptCount++

		// Calculate input text length
		inputLength := r.calculateInputTextLength(currentMsgs)

		// Check if automatic compression is needed (before API call)
		if ShouldAutoCompress(r.agent.Config.System, currentMsgs) {
			if debugCallback != nil {
				debugCallback("AUTOMATIC COMPRESSION: Context size exceeds 1,000,000 characters, compressing...")
			}
			logger.Info("Automatic compression triggered for agent %s: context size exceeds 1,000,000 characters", r.agent.ID)

			compressedMsgs, compressErr := r.compressContext(ctx, threadID, currentMsgs, debugCallback)
			if compressErr != nil {
				logger.Warn("Failed to compress context automatically: %v", compressErr)
				// Continue with original messages if compression fails
			} else {
				currentMsgs = compressedMsgs
				inputLength = r.calculateInputTextLength(currentMsgs)
				if debugCallback != nil {
					debugCallback(fmt.Sprintf("Context compressed: %d chars -> %d chars", inputLength, r.calculateInputTextLength(currentMsgs)))
				}
			}
		}

		// Debug: Show API call about to be made
		if debugCallback != nil {
			debugCallback(fmt.Sprintf("Calling Anthropic API (model: %s, %d messages in history, input length: %d chars, attempt %d)", r.agent.Config.Model, len(currentMsgs), inputLength, attemptCount))
		}

		// Log API call with input length
		logger.Info("Calling Anthropic API for agent %s (model: %s, %d messages in history, input length: %d chars, attempt %d)", r.agent.ID, r.agent.Config.Model, len(currentMsgs), inputLength, attemptCount)

		// Build API params with prompt caching support
		params := anthropic.MessageNewParams{
			Model:     anthropic.Model(r.agent.Config.Model),
			MaxTokens: r.agent.Config.MaxTokens,
			Messages:  currentMsgs,
			System:    buildSystemBlocks(r.agent.Config.System, tools),
			Tools:     tools,
		}

		message, err := r.client.Messages.New(ctx, params)
		if err != nil {
			// Log detailed error information
			logDetailedError(r.agent.ID, err, debugCallback, fmt.Sprintf("non-streaming API call, attempt %d", attemptCount))

			// Check for 413 error (request too large) - retry immediately with compression
			if r.is413Error(err) {
				if debugCallback != nil {
					debugCallback("AUTOMATIC COMPRESSION: Anthropic API returned 413 request_too_large error, compressing context...")
				}
				logger.Info("Automatic compression triggered for agent %s: Anthropic API returned 413 request_too_large", r.agent.ID)

				compressedMsgs, compressErr := r.compressContext(ctx, threadID, currentMsgs, debugCallback)
				if compressErr != nil {
					logDetailedError(r.agent.ID, compressErr, debugCallback, "compression after 413 error")
					return backoff.Permanent(fmt.Errorf("anthropic Messages.New (413 error) and compression failed: %w", compressErr))
				}

				// Update currentMsgs for next attempt and reset backoff for immediate retry
				currentMsgs = compressedMsgs
				eb.Reset() // Reset backoff for immediate retry
				return fmt.Errorf("anthropic Messages.New (413 error), retrying with compressed context: %w", err)
			}

			// Check for rate limit error
			if IsRateLimitError(err) {
				if r.rateLimitHandler != nil {
					// Extract retry-after if available
					retryAfter = ExtractRetryAfter(err, nil)

					// Adjust backoff based on retry-after
					if retryAfter > 0 {
						eb.Reset()
						eb.InitialInterval = retryAfter
						eb.Multiplier = 1.5
						eb.RandomizationFactor = 0.1
						eb.Reset()
					}

					if debugCallback != nil {
						debugCallback(fmt.Sprintf("Rate limit encountered. Retrying with backoff (attempt %d)...", attemptCount))
					}
					logger.Info("Rate limit encountered for agent %s (attempt %d), retrying with backoff", r.agent.ID, attemptCount)

					// Return error to trigger backoff retry
					return fmt.Errorf("rate limit: %w", err)
				}
			}

			// For other errors, don't retry
			return backoff.Permanent(fmt.Errorf("anthropic Messages.New: %w", err))
		}

		// Success
		result = message
		// Log cache performance metrics if available
		usage := message.Usage
		cacheMsg := fmt.Sprintf("Cache stats - Creation: %d tokens, Read: %d tokens, Input: %d tokens, Output: %d tokens",
			usage.CacheCreationInputTokens,
			usage.CacheReadInputTokens,
			usage.InputTokens,
			usage.OutputTokens)
		logger.Info("API call completed for agent %s - %s", r.agent.ID, cacheMsg)
		if debugCallback != nil {
			debugCallback(cacheMsg)
		}
		return nil
	}

	err := backoff.Retry(operation, backoff.WithContext(backoffConfig, ctx))
	if err != nil {
		// Check if this is a rate limit error that exceeded max retries
		if IsRateLimitError(err) && r.rateLimitHandler != nil {
			// Max retries exceeded - schedule retry using next_wake for scheduled agents
			if r.agent.Config.Schedule != "" {
				delay := retryAfter
				if delay == 0 {
					delay = 60 * time.Second // Default delay
				}
				if scheduleErr := r.rateLimitHandler.ScheduleRetryWithNextWake(r.agent.ID, delay); scheduleErr != nil {
					logger.Warn("Failed to schedule retry via next_wake: %v", scheduleErr)
				} else {
					if debugCallback != nil {
						debugCallback(fmt.Sprintf("Rate limit exceeded. Agent will retry at scheduled time (in %v)", delay))
					}
					logger.Info("Rate limit exceeded for agent %s. Scheduled retry via next_wake in %v", r.agent.ID, delay)
					return nil, fmt.Errorf("rate limit exceeded: agent will retry at scheduled time: %w", err)
				}
			}
		}
		return nil, err
	}

	return result, nil
}

// GetMessageSummarizer returns the message summarizer for this runner.
func (r *AgentRunner) GetMessageSummarizer() *MessageSummarizer {
	return r.messageSummarizer
}
