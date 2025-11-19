package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/aschepis/backscratcher/staff/llm"
	"github.com/aschepis/backscratcher/staff/logger"
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
	llmClient         llm.Client
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
	llmClient llm.Client,
	agent *Agent,
	toolExec ToolExecutor,
	toolProvider ToolProvider,
	stateManager *StateManager,
	statsManager *StatsManager,
	messagePersister MessagePersister,
	messageSummarizer *MessageSummarizer,
) (*AgentRunner, error) {
	if llmClient == nil {
		return nil, fmt.Errorf("llmClient is required for AgentRunner")
	}
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
	rateLimitHandler := NewRateLimitHandler(stateManager)

	// Set callback to notify users about rate limits
	rateLimitHandler.SetOnRateLimitCallback(func(agentID string, retryAfter time.Duration, attempt int) error {
		logger.Info("Rate limit callback: agent %s will retry after %v (attempt %d)", agentID, retryAfter, attempt)
		return nil
	})

	return &AgentRunner{
		llmClient:         llmClient,
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

// buildSystemBlocks moved to llm/anthropic/client.go

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

// calculateInputTextLength moved to context_manager.go as GetContextSize

// RunAgent executes a single turn for an agent, with optional history.
// debugCallback is retrieved from context if available.
func (r *AgentRunner) RunAgent(
	ctx context.Context,
	threadID string,
	userMsg string,
	history []anthropicsdk.MessageParam,
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

	// Convert history from Anthropic types to llm types
	llmHistory := convertAnthropicMessagesToLLM(history)

	// Prepare LLM request
	req := prepareLLMRequest(r.agent, userMsg, llmHistory, r.toolProvider)

	// Execute tool loop
	result, err := executeToolLoop(
		ctx,
		r.llmClient,
		req,
		r.agent.ID,
		threadID,
		r.toolExec,
		r.messagePersister,
		r.messageSummarizer,
		debugCallback,
	)

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

	executionSuccessful = true
	return result, nil
}

// RunAgentStream executes a single turn for an agent with streaming support.
// It calls the callback function for each text delta received.
// debugCallback is retrieved from context if available.
// TODO: Refactor to use llm.Client.Stream() similar to RunAgent()
func (r *AgentRunner) RunAgentStream(
	ctx context.Context,
	threadID string,
	userMsg string,
	history []anthropicsdk.MessageParam,
	callback StreamCallback,
) (string, error) {
	// For now, fall back to non-streaming implementation
	// TODO: Implement proper streaming using r.llmClient.Stream()
	return r.RunAgent(ctx, threadID, userMsg, history)
}

// GetMessageSummarizer returns the message summarizer for this runner.
func (r *AgentRunner) GetMessageSummarizer() *MessageSummarizer {
	return r.messageSummarizer
}

// convertAnthropicMessagesToLLM converts Anthropic MessageParams to llm.Messages.
// This is a local conversion to avoid import cycles.
func convertAnthropicMessagesToLLM(msgs []anthropicsdk.MessageParam) []llm.Message {
	result := make([]llm.Message, 0, len(msgs))
	for _, msg := range msgs {
		var role llm.MessageRole
		switch string(msg.Role) {
		case "user":
			role = llm.RoleUser
		case "assistant":
			role = llm.RoleAssistant
		default:
			role = llm.RoleUser
		}

		content := make([]llm.ContentBlock, 0, len(msg.Content))
		for _, blockUnion := range msg.Content {
			if blockUnion.OfText != nil {
				content = append(content, llm.ContentBlock{
					Type: llm.ContentBlockTypeText,
					Text: blockUnion.OfText.Text,
				})
			}
			if blockUnion.OfToolUse != nil {
				var input map[string]interface{}
				if blockUnion.OfToolUse.Input != nil {
					if inputBytes, err := json.Marshal(blockUnion.OfToolUse.Input); err == nil {
						if err := json.Unmarshal(inputBytes, &input); err != nil {
							input = make(map[string]interface{})
						}
					} else {
						input = make(map[string]interface{})
					}
				} else {
					input = make(map[string]interface{})
				}
				content = append(content, llm.ContentBlock{
					Type: llm.ContentBlockTypeToolUse,
					ToolUse: &llm.ToolUseBlock{
						ID:    blockUnion.OfToolUse.ID,
						Name:  blockUnion.OfToolUse.Name,
						Input: input,
					},
				})
			}
			if blockUnion.OfToolResult != nil {
				var contentStr string
				for _, contentUnion := range blockUnion.OfToolResult.Content {
					if contentUnion.OfText != nil {
						contentStr += contentUnion.OfText.Text
					}
				}
				isError := false
				if blockUnion.OfToolResult.IsError.Value {
					isError = true
				}
				content = append(content, llm.ContentBlock{
					Type: llm.ContentBlockTypeToolResult,
					ToolResult: &llm.ToolResultBlock{
						ID:      blockUnion.OfToolResult.ToolUseID,
						Content: contentStr,
						IsError: isError,
					},
				})
			}
		}

		result = append(result, llm.Message{
			Role:    role,
			Content: content,
		})
	}
	return result
}
