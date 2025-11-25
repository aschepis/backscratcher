package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/aschepis/backscratcher/staff/config"
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
	resolvedModel     string // Model resolved from LLM preferences (not the legacy agent.Config.Model)
	resolvedProvider  string // Provider resolved from LLM preferences
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

// NewAgentRunner creates a new AgentRunner with all required dependencies.
func NewAgentRunner(
	llmClient llm.Client,
	agent *Agent,
	resolvedModel string, // Model resolved from LLM preferences
	resolvedProvider string, // Provider resolved from LLM preferences
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
		resolvedModel:     resolvedModel,
		resolvedProvider:  resolvedProvider,
		toolExec:          toolExec,
		toolProvider:      toolProvider,
		stateManager:      stateManager,
		statsManager:      statsManager,
		messagePersister:  messagePersister,
		messageSummarizer: messageSummarizer,
		rateLimitHandler:  rateLimitHandler,
	}, nil
}

// GetResolvedModel returns the model resolved from LLM preferences.
func (r *AgentRunner) GetResolvedModel() string {
	return r.resolvedModel
}

// GetResolvedProvider returns the provider resolved from LLM preferences.
func (r *AgentRunner) GetResolvedProvider() string {
	return r.resolvedProvider
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
// AgentConfig and LLMPreference are now in the config package
// These type aliases are kept for backward compatibility
type AgentConfig = config.AgentConfig
type LLMPreference = config.LLMPreference

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
	if r.resolvedModel == "" {
		return "", errors.New("resolved model is required")
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

	// Convert history from Anthropic types to llm types
	llmHistory := convertAnthropicMessagesToLLM(history)

	// Prepare LLM request
	req := prepareLLMRequest(r.agent, r.resolvedModel, userMsg, llmHistory, r.toolProvider)

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
func (r *AgentRunner) RunAgentStream(
	ctx context.Context,
	threadID string,
	userMsg string,
	history []anthropicsdk.MessageParam,
	callback StreamCallback,
) (string, error) {
	if r.agent == nil {
		return "", errors.New("agent is nil")
	}
	if r.resolvedModel == "" {
		return "", errors.New("resolved model is required")
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

	// Convert history from Anthropic types to llm types
	llmHistory := convertAnthropicMessagesToLLM(history)

	// Prepare LLM request
	req := prepareLLMRequest(r.agent, r.resolvedModel, userMsg, llmHistory, r.toolProvider)

	// Execute tool loop with streaming
	result, err := executeToolLoopStream(
		ctx,
		r.llmClient,
		req,
		r.agent.ID,
		threadID,
		r.toolExec,
		r.messagePersister,
		r.messageSummarizer,
		callback,
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
				// IsError is a param.Opt[bool], access Value directly
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
