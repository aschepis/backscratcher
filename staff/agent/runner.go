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
	client           *anthropic.Client
	agent            *Agent
	toolExec         ToolExecutor
	toolProvider     ToolProvider
	stateManager     *StateManager
	statsManager     *StatsManager
	messagePersister MessagePersister // Optional message persister
}

// toolResultData holds the result data for a tool call
type toolResultData struct {
	result  any
	isError bool
}

func NewAgentRunner(
	apiKey string,
	agent *Agent,
	toolExec ToolExecutor,
	toolProvider ToolProvider,
	stateManager *StateManager,
	statsManager *StatsManager,
) (*AgentRunner, error) {
	return NewAgentRunnerWithPersister(apiKey, agent, toolExec, toolProvider, stateManager, statsManager, nil)
}

// NewAgentRunnerWithPersister creates a new AgentRunner with an optional message persister.
func NewAgentRunnerWithPersister(
	apiKey string,
	agent *Agent,
	toolExec ToolExecutor,
	toolProvider ToolProvider,
	stateManager *StateManager,
	statsManager *StatsManager,
	messagePersister MessagePersister,
) (*AgentRunner, error) {
	if stateManager == nil {
		return nil, fmt.Errorf("stateManager is required for AgentRunner")
	}
	if statsManager == nil {
		return nil, fmt.Errorf("statsManager is required for AgentRunner")
	}
	client := anthropic.NewClient(option.WithAPIKey(apiKey))
	return &AgentRunner{
		client:           &client,
		agent:            agent,
		toolExec:         toolExec,
		toolProvider:     toolProvider,
		stateManager:     stateManager,
		statsManager:     statsManager,
		messagePersister: messagePersister,
	}, nil
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
		// Calculate input text length
		inputLength := r.calculateInputTextLength(msgs)

		// Debug: Show API call about to be made
		if debugCallback != nil {
			debugCallback(fmt.Sprintf("Calling Anthropic API (model: %s, %d messages in history, input length: %d chars)", r.agent.Config.Model, len(msgs), inputLength))
		}

		// Log API call with input length
		logger.Info("Calling Anthropic API for agent %s (model: %s, %d messages in history, input length: %d chars)", r.agent.ID, r.agent.Config.Model, len(msgs), inputLength)

		message, err := r.client.Messages.New(ctx, anthropic.MessageNewParams{
			Model:     anthropic.Model(r.agent.Config.Model),
			MaxTokens: r.agent.Config.MaxTokens,
			Messages:  msgs,
			System: []anthropic.TextBlockParam{
				{Text: r.agent.Config.System},
			},
			Tools: tools,
		})
		if err != nil {
			executionError = err.Error()
			return "", fmt.Errorf("anthropic Messages.New: %w", err)
		}

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

				// Track result for persistence
				toolResultMap[block.ID] = toolResultData{result: result, isError: callErr != nil}

				b, _ := json.Marshal(result)
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
			// Persist assistant text message if we have a persister
			if r.messagePersister != nil && finalText.Len() > 0 {
				if err := r.messagePersister.AppendAssistantMessage(ctx, r.agent.ID, threadID, strings.TrimSpace(finalText.String())); err != nil {
					// Log error but don't fail execution
					logger.Warn("failed to persist assistant message: %v", err)
				}
			}
			executionSuccessful = true
			return strings.TrimSpace(finalText.String()), nil
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
			System: []anthropic.TextBlockParam{
				{Text: r.agent.Config.System},
			},
			Tools: tools,
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

			case anthropic.MessageStopEvent:
				// Message finished - stream is complete
				streamComplete = true
			}
		}

		// Check for stream errors
		if err := stream.Err(); err != nil {
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

				// Track result for persistence
				isError := callErr != nil
				toolResultMap[toolUse.ID] = toolResultData{result: result, isError: isError}

				b, _ := json.Marshal(result)
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
