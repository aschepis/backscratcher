package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aschepis/backscratcher/staff/llm"
	"github.com/rs/zerolog"
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
	logger            zerolog.Logger
}

// NewAgentRunner creates a new AgentRunner with all required dependencies.
func NewAgentRunner(
	logger zerolog.Logger,
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
		logger.Info().Msgf("Rate limit callback: agent %s will retry after %v (attempt %d)", agentID, retryAfter, attempt)
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
		logger:            logger.With().Str("component", "agentRunner").Logger(),
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
				r.logger.Warn().Err(updateErr).Msgf("failed to update failure stats for agent %s", r.agent.ID)
			}
		}
	} else {
		// Track successful execution
		if updateErr := r.statsManager.IncrementExecutionCount(r.agent.ID); updateErr != nil {
			r.logger.Warn().Err(updateErr).Msgf("failed to update execution stats for agent %s", r.agent.ID)
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
			r.logger.Warn().Err(err).Msgf("failed to compute next wake for agent %s", r.agent.ID)
			// Fall back to idle on error
			if err := r.stateManager.SetState(r.agent.ID, StateIdle); err != nil {
				r.logger.Warn().Err(err).Msgf("failed to set agent state to idle for agent %s", r.agent.ID)
			}
			return
		}
		// Set state to waiting_external with next_wake
		if err := r.stateManager.SetStateWithNextWake(r.agent.ID, StateWaitingExternal, &nextWake); err != nil {
			r.logger.Warn().Err(err).Msgf("failed to set agent state to waiting_external for agent %s", r.agent.ID)
		}
	} else {
		// Agent is not scheduled, set to idle
		if err := r.stateManager.SetState(r.agent.ID, StateIdle); err != nil {
			r.logger.Warn().Err(err).Msgf("failed to set agent state to idle for agent %s", r.agent.ID)
		}
	}
}

// RunAgent executes a single turn for an agent, with optional history.
// debugCallback is retrieved from context if available.
// History is provided as provider-neutral llm.Message types.
func (r *AgentRunner) RunAgent(
	ctx context.Context,
	threadID string,
	userMsg string,
	history []llm.Message,
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
		r.logger.Warn().Err(err).Msgf("failed to set agent state to running for agent %s", r.agent.ID)
	}

	// Track if execution was successful
	executionSuccessful := false
	executionError := ""

	// Ensure state is updated when execution completes (normal or error)
	defer func() {
		r.updateAgentStateAfterExecution(executionSuccessful, executionError)
	}()

	// Prepare LLM request (history is already in llm.Message format)
	req := prepareLLMRequest(r.agent, r.resolvedModel, userMsg, history, r.toolProvider)

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
	history []llm.Message,
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
		r.logger.Warn().Err(err).Msgf("failed to set agent state to running for agent %s", r.agent.ID)
	}

	// Track if execution was successful
	executionSuccessful := false
	executionError := ""

	// Ensure state is updated when execution completes (normal or error)
	defer func() {
		r.updateAgentStateAfterExecution(executionSuccessful, executionError)
	}()

	// Prepare LLM request (history is already in llm.Message format)
	req := prepareLLMRequest(r.agent, r.resolvedModel, userMsg, history, r.toolProvider)

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
