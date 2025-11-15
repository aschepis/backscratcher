package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// ToolExecutor is whatever you already had for running tools.
// Kept here just for clarity.
// debugCallback is retrieved from context if needed.
type ToolExecutor interface {
	Handle(ctx context.Context, toolName, agentID string, inputJSON []byte) (any, error)
}

type AgentRunner struct {
	client       *anthropic.Client
	agent        *Agent
	toolExec     ToolExecutor
	toolProvider ToolProvider
}

func NewAgentRunner(
	apiKey string,
	agent *Agent,
	toolExec ToolExecutor,
	toolProvider ToolProvider,
) *AgentRunner {
	client := anthropic.NewClient(option.WithAPIKey(apiKey))
	return &AgentRunner{
		client:       &client,
		agent:        agent,
		toolExec:     toolExec,
		toolProvider: toolProvider,
	}
}

// AgentConfig is the per-agent config you already have.
type AgentConfig struct {
	ID        string
	Name      string
	System    string
	Model     string
	MaxTokens int64
	Tools     []string
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

	// Get debug callback from context
	debugCallback, _ := GetDebugCallback(ctx)

	// Conversation so far
	msgs := append([]anthropic.MessageParam{}, history...)
	msgs = append(msgs,
		anthropic.NewUserMessage(anthropic.NewTextBlock(userMsg)),
	)

	tools := r.toolProvider.SpecsFor(r.agent.Config)

	for {
		// Debug: Show API call about to be made
		if debugCallback != nil {
			debugCallback(fmt.Sprintf("Calling Anthropic API (model: %s, %d messages in history)", r.agent.Config.Model, len(msgs)))
		}

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
			return "", fmt.Errorf("anthropic Messages.New: %w", err)
		}

		// Accumulate tool uses + any plain text
		var (
			finalText   strings.Builder
			toolResults []anthropic.ContentBlockParamUnion
		)

		for _, blockUnion := range message.Content {
			switch block := blockUnion.AsAny().(type) {

			case anthropic.TextBlock:
				finalText.WriteString(block.Text)
				finalText.WriteRune('\n')

			case anthropic.ToolUseBlock:
				// Get raw JSON input the way the official example does.
				raw := []byte(block.JSON.Input.Raw())

				// Execute tool (debug callback is retrieved from context if needed)
				result, callErr := r.toolExec.Handle(ctx, block.Name, r.agent.ID, raw)
				if callErr != nil {
					// Return error payload to the model
					result = map[string]any{"error": callErr.Error()}
				}

				b, _ := json.Marshal(result)
				toolResults = append(
					toolResults,
					anthropic.NewToolResultBlock(block.ID, string(b), false),
				)
			}
		}

		// Add the assistant message to history
		msgs = append(msgs, message.ToParam())

		// If no tool calls, we’re done.
		if len(toolResults) == 0 {
			return strings.TrimSpace(finalText.String()), nil
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
		// Debug: Show API call about to be made
		if debugCallback != nil {
			debugCallback(fmt.Sprintf("Calling Anthropic API (model: %s, %d messages in history)", r.agent.Config.Model, len(msgs)))
		}

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
			return "", fmt.Errorf("stream error: %w", err)
		}

		// Check if context was cancelled
		if ctx.Err() != nil {
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

			for _, toolUse := range toolUseBlocks {
				// Get the input JSON for this tool
				var inputJSON any
				if builder, ok := toolInputs[toolUse.ID]; ok {
					inputStr := builder.String()
					// Parse the JSON input so we can pass it as any
					var parsedInput any
					if err := json.Unmarshal([]byte(inputStr), &parsedInput); err == nil {
						inputJSON = parsedInput
					} else {
						// Fallback to raw string if parsing fails
						inputJSON = inputStr
					}
				} else {
					// Fallback: use the raw JSON from the tool use block if available
					var parsedInput any
					rawJSON := toolUse.JSON.Input.Raw()
					if err := json.Unmarshal([]byte(rawJSON), &parsedInput); err == nil {
						inputJSON = parsedInput
					} else {
						inputJSON = rawJSON
					}
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

				b, _ := json.Marshal(result)
				toolResults = append(toolResults, anthropic.NewToolResultBlock(toolUse.ID, string(b), false))
			}

			// Add assistant message with tool uses to history
			assistantMsg := anthropic.NewAssistantMessage(assistantContent...)
			msgs = append(msgs, assistantMsg)

			// Add tool results as user message and continue loop
			msgs = append(msgs, anthropic.NewUserMessage(toolResults...))
			continue // Loop back to get the next response
		}

		// If we have text, return it
		if hasText && textBuilder.Len() > 0 {
			text := strings.TrimSpace(textBuilder.String())
			return text, nil
		}

		// If stream completed without content, it might be an empty response
		// This is valid - some responses might be empty
		if streamComplete {
			return "", nil
		}

		// If we get here without text or tool use, something went wrong
		return "", fmt.Errorf("unexpected stream completion: no text or tool use detected")
	}
}
