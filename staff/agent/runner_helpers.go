package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/aschepis/backscratcher/staff/llm"
	"github.com/aschepis/backscratcher/staff/logger"
)

// prepareLLMRequest converts agent config, history, and tools to an llm.Request.
func prepareLLMRequest(
	agent *Agent,
	resolvedModel string, // Model resolved from LLM preferences
	userMsg string,
	history []llm.Message,
	toolProvider ToolProvider,
) *llm.Request {
	// Convert tool specs
	toolSpecs := toolProvider.SpecsFor(agent.Config)
	tools := convertAnthropicToolsToLLM(toolSpecs)

	// Build messages list
	messages := make([]llm.Message, 0, len(history)+1)
	messages = append(messages, history...)
	messages = append(messages, llm.NewTextMessage(llm.RoleUser, userMsg))

	return &llm.Request{
		Model:     resolvedModel, // Use resolved model, not legacy agent.Config.Model
		Messages:  messages,
		System:    agent.Config.System,
		Tools:     tools,
		MaxTokens: agent.Config.MaxTokens,
	}
}

// executeToolLoop executes a tool execution loop using the LLM client.
// It handles tool calls, execution, result formatting, and looping until final response.
func executeToolLoop(
	ctx context.Context,
	client llm.Client,
	req *llm.Request,
	agentID string,
	threadID string,
	toolExec ToolExecutor,
	messagePersister MessagePersister,
	messageSummarizer *MessageSummarizer,
	debugCallback DebugCallback,
) (string, error) {
	// Track conversation history in llm.Message format
	conversationHistory := req.Messages

	// Safeguards against infinite loops
	const maxIterations = 20
	iterationCount := 0
	// Track repeated identical failing tool calls
	type toolCallKey struct {
		toolName string
		input    string // JSON string of input
	}
	repeatedFailures := make(map[toolCallKey]int)
	const maxRepeatedFailures = 3

	for {
		iterationCount++
		if iterationCount > maxIterations {
			return "", fmt.Errorf("tool loop exceeded maximum iterations (%d). Possible infinite loop detected", maxIterations)
		}
		// Create request with current conversation history
		currentReq := &llm.Request{
			Model:     req.Model,
			Messages:  conversationHistory,
			System:    req.System,
			Tools:     req.Tools,
			MaxTokens: req.MaxTokens,
		}

		// Output debug info about LLM request
		if debugCallback != nil {
			toolCount := 0
			if req.Tools != nil {
				toolCount = len(req.Tools)
			}
			msgCount := len(conversationHistory)
			debugCallback(fmt.Sprintf("🤖 Calling LLM (model: %s, messages: %d, tools: %d)", req.Model, msgCount, toolCount))
		}

		// Call LLM
		resp, err := client.Synchronous(ctx, currentReq)
		if err != nil {
			return "", err
		}

		// Accumulate tool uses + any plain text
		var (
			finalText     strings.Builder
			toolResults   []llm.ToolResultBlock
			toolNameMap   = make(map[string]string)         // Map tool ID to tool name for persistence
			toolResultMap = make(map[string]toolResultData) // Map tool ID to result data for persistence
		)

		// Process response content
		for _, block := range resp.Content {
			switch block.Type {
			case llm.ContentBlockTypeText:
				finalText.WriteString(block.Text)
				finalText.WriteRune('\n')

			case llm.ContentBlockTypeToolUse:
				if block.ToolUse == nil {
					continue
				}

				toolUse := block.ToolUse
				// Track tool name for persistence
				toolNameMap[toolUse.ID] = toolUse.Name

				// Marshal tool input to JSON for execution
				raw, err := json.Marshal(toolUse.Input)
				if err != nil {
					logger.Warn("failed to marshal tool input for tool %s (id: %s): %v", toolUse.Name, toolUse.ID, err)
					raw = []byte("{}")
				}

				// Output debug info about tool call
				if debugCallback != nil {
					var argsStr string
					if prettyArgs, err := json.MarshalIndent(toolUse.Input, "", "  "); err == nil {
						argsStr = string(prettyArgs)
					} else {
						argsStr = string(raw)
					}
					debugCallback(fmt.Sprintf("🔧 Tool call detected: %s\nArguments: %s", toolUse.Name, argsStr))
				}

				// Execute tool
				result, callErr := toolExec.Handle(ctx, toolUse.Name, agentID, raw)
				if callErr != nil {
					// Check for repeated identical failing tool calls
					callKey := toolCallKey{
						toolName: toolUse.Name,
						input:    string(raw),
					}
					repeatedFailures[callKey]++
					if repeatedFailures[callKey] >= maxRepeatedFailures {
						logger.Warn("Tool '%s' with input '%s' has failed %d times. Breaking loop to prevent infinite retry", toolUse.Name, string(raw), repeatedFailures[callKey])
						return "", fmt.Errorf("tool '%s' repeatedly failed with same input after %d attempts: %v", toolUse.Name, maxRepeatedFailures, callErr)
					}
					// Return error payload to the model
					result = map[string]any{"error": callErr.Error()}
				} else {
					// Reset failure count on success
					callKey := toolCallKey{
						toolName: toolUse.Name,
						input:    string(raw),
					}
					delete(repeatedFailures, callKey)
				}

				// Summarize result if needed (before marshaling to JSON)
				summarizedResult, summarizeErr := summarizeToolResult(ctx, messageSummarizer, result)
				if summarizeErr != nil {
					logger.Warn("Failed to summarize tool result, using original: %v", summarizeErr)
					summarizedResult = result
				}

				// Track original result for persistence (before summarization)
				toolResultMap[toolUse.ID] = toolResultData{result: result, isError: callErr != nil}

				// Marshal result to JSON string
				b, _ := json.Marshal(summarizedResult)
				isError := callErr != nil

				// Output debug info about tool result
				if debugCallback != nil {
					var resultStr string
					if isError {
						resultStr = fmt.Sprintf("ERROR: %v", callErr)
					} else {
						// Pretty-print result if possible
						if prettyResult, err := json.MarshalIndent(summarizedResult, "", "  "); err == nil {
							resultStr = string(prettyResult)
							// Truncate very long results
							if len(resultStr) > 500 {
								resultStr = resultStr[:500] + "... (truncated)"
							}
						} else {
							resultStr = string(b)
							if len(resultStr) > 500 {
								resultStr = resultStr[:500] + "... (truncated)"
							}
						}
					}
					debugCallback(fmt.Sprintf("✅ Tool result for %s: %s", toolUse.Name, resultStr))
				}

				// Check if we already have a result for this tool ID (avoid duplicates)
				alreadyExists := false
				for _, tr := range toolResults {
					if tr.ID == toolUse.ID {
						alreadyExists = true
						break
					}
				}
				if !alreadyExists {
					toolResults = append(toolResults, llm.ToolResultBlock{
						ID:      toolUse.ID,
						Content: string(b),
						IsError: isError,
					})
				}
			}
		}

		// Add assistant message to conversation history
		assistantContent := make([]llm.ContentBlock, 0, len(resp.Content))
		for _, block := range resp.Content {
			assistantContent = append(assistantContent, block)
		}
		assistantMsg := llm.Message{
			Role:    llm.RoleAssistant,
			Content: assistantContent,
		}
		conversationHistory = append(conversationHistory, assistantMsg)

		// Persist assistant message if we have tool calls
		if len(toolResults) > 0 && messagePersister != nil {
			// Save assistant message with tool calls
			for _, block := range resp.Content {
				if block.Type == llm.ContentBlockTypeToolUse && block.ToolUse != nil {
					toolUse := block.ToolUse
					if err := messagePersister.AppendToolCall(ctx, agentID, threadID, toolUse.ID, toolUse.Name, toolUse.Input); err != nil {
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
			if messageSummarizer != nil && messageSummarizer.ShouldSummarize(finalTextStr) {
				summarized, err := messageSummarizer.Summarize(ctx, finalTextStr)
				if err != nil {
					logger.Warn("Failed to summarize assistant message, using original: %v", err)
				} else {
					finalTextStr = summarized
				}
			}

			// Persist assistant text message if we have a persister
			if messagePersister != nil && finalTextStr != "" {
				if err := messagePersister.AppendAssistantMessage(ctx, agentID, threadID, finalTextStr); err != nil {
					// Log error but don't fail execution
					logger.Warn("failed to persist assistant message: %v", err)
				}
			}
			return finalTextStr, nil
		}

		// Persist tool results
		if messagePersister != nil {
			for toolID, resultData := range toolResultMap {
				toolName := toolNameMap[toolID]
				if err := messagePersister.AppendToolResult(ctx, agentID, threadID, toolID, toolName, resultData.result, resultData.isError); err != nil {
					// Log error but don't fail execution
					logger.Warn("failed to persist tool result: %v", err)
				}
			}
		}

		// Otherwise, send tool results back as a user message and loop.
		// Deduplicate tool results by ID to ensure each tool_use has only one result
		seenResultIDs := make(map[string]bool)
		toolResultContent := make([]llm.ContentBlock, 0, len(toolResults))
		for _, tr := range toolResults {
			// Skip if we've already added a result for this tool ID
			if seenResultIDs[tr.ID] {
				continue
			}
			seenResultIDs[tr.ID] = true
			toolResultContent = append(toolResultContent, llm.ContentBlock{
				Type:       llm.ContentBlockTypeToolResult,
				ToolResult: &tr,
			})
		}
		toolResultMsg := llm.Message{
			Role:    llm.RoleUser,
			Content: toolResultContent,
		}
		conversationHistory = append(conversationHistory, toolResultMsg)
	}
}

// executeToolLoopStream executes a tool execution loop using the LLM client with streaming support.
// It handles tool calls, execution, result formatting, and looping until final response.
// The streamCallback is called for each text delta received.
func executeToolLoopStream(
	ctx context.Context,
	client llm.Client,
	req *llm.Request,
	agentID string,
	threadID string,
	toolExec ToolExecutor,
	messagePersister MessagePersister,
	messageSummarizer *MessageSummarizer,
	debugCallback DebugCallback,
	streamCallback StreamCallback,
) (string, error) {
	// Track conversation history in llm.Message format
	conversationHistory := req.Messages

	// Safeguards against infinite loops
	const maxIterations = 20
	iterationCount := 0
	// Track repeated identical failing tool calls
	type toolCallKey struct {
		toolName string
		input    string // JSON string of input
	}
	repeatedFailures := make(map[toolCallKey]int)
	const maxRepeatedFailures = 3

	for {
		iterationCount++
		if iterationCount > maxIterations {
			return "", fmt.Errorf("tool loop exceeded maximum iterations (%d). Possible infinite loop detected", maxIterations)
		}
		// Create request with current conversation history
		currentReq := &llm.Request{
			Model:     req.Model,
			Messages:  conversationHistory,
			System:    req.System,
			Tools:     req.Tools,
			MaxTokens: req.MaxTokens,
		}

		// Output debug info about LLM request
		if debugCallback != nil {
			toolCount := 0
			if req.Tools != nil {
				toolCount = len(req.Tools)
			}
			msgCount := len(conversationHistory)
			debugCallback(fmt.Sprintf("🤖 Calling LLM stream (model: %s, messages: %d, tools: %d)", req.Model, msgCount, toolCount))
		}

		// Start streaming
		stream, err := client.Stream(ctx, currentReq)
		if err != nil {
			return "", err
		}
		defer stream.Close()

		// Accumulate tool uses + any plain text
		var (
			finalText      strings.Builder
			toolUses       []*llm.ToolUseBlock // Track tool uses as they come in
			toolResults    []llm.ToolResultBlock
			toolNameMap    = make(map[string]string)         // Map tool ID to tool name for persistence
			toolResultMap  = make(map[string]toolResultData) // Map tool ID to result data for persistence
			currentToolUse *llm.ToolUseBlock
		)

		// Process stream events
		for stream.Next() {
			event := stream.Event()
			if event == nil {
				continue
			}

			switch event.Type {
			case llm.StreamEventTypeContentDelta:
				if event.Delta != nil {
					switch event.Delta.Type {
					case llm.StreamDeltaTypeText:
						// Text delta - append to final text and call callback
						if event.Delta.Text != "" {
							finalText.WriteString(event.Delta.Text)
							if streamCallback != nil {
								if err := streamCallback(event.Delta.Text); err != nil {
									return "", fmt.Errorf("stream callback error: %w", err)
								}
							}
						}
					case llm.StreamDeltaTypeToolUse:
						// Tool use start
						if event.Delta.ToolUse != nil {
							currentToolUse = event.Delta.ToolUse
							toolNameMap[currentToolUse.ID] = currentToolUse.Name
							// Check if we already have this tool use (avoid duplicates)
							found := false
							for _, tu := range toolUses {
								if tu != nil && tu.ID == currentToolUse.ID {
									found = true
									// Update the existing one with new input if needed
									if currentToolUse.Input != nil {
										for k, v := range currentToolUse.Input {
											tu.Input[k] = v
										}
									}
									break
								}
							}
							if !found {
								// Output debug info about tool call
								if debugCallback != nil {
									var argsStr string
									if currentToolUse.Input != nil {
										if prettyArgs, err := json.MarshalIndent(currentToolUse.Input, "", "  "); err == nil {
											argsStr = string(prettyArgs)
										} else {
											argsStr = fmt.Sprintf("%v", currentToolUse.Input)
										}
									} else {
										argsStr = "{}"
									}
									debugCallback(fmt.Sprintf("🔧 Tool call detected: %s\nArguments: %s", currentToolUse.Name, argsStr))
								}
								// Make a copy to track
								toolUseCopy := *currentToolUse
								toolUseCopy.Input = make(map[string]interface{})
								if currentToolUse.Input != nil {
									for k, v := range currentToolUse.Input {
										toolUseCopy.Input[k] = v
									}
								}
								toolUses = append(toolUses, &toolUseCopy)
							}
						}
					case llm.StreamDeltaTypeToolInput:
						// Tool input delta - accumulate for current tool use
						if currentToolUse != nil && event.Delta.ToolInput != "" {
							// Parse accumulated tool input
							var input map[string]interface{}
							if err := json.Unmarshal([]byte(event.Delta.ToolInput), &input); err != nil {
								// If parsing fails, try to merge with existing input
								if currentToolUse.Input == nil {
									currentToolUse.Input = make(map[string]interface{})
								}
							} else {
								currentToolUse.Input = input
							}
						}
					}
				}

			case llm.StreamEventTypeContentBlock:
				if event.Delta != nil {
					switch event.Delta.Type {
					case llm.StreamDeltaTypeText:
						// Complete text block
						if event.Delta.Text != "" {
							finalText.WriteString(event.Delta.Text)
							if streamCallback != nil {
								if err := streamCallback(event.Delta.Text); err != nil {
									return "", fmt.Errorf("stream callback error: %w", err)
								}
							}
						}
					case llm.StreamDeltaTypeToolUse:
						// Complete tool use block
						if event.Delta.ToolUse != nil {
							currentToolUse = event.Delta.ToolUse
							toolNameMap[currentToolUse.ID] = currentToolUse.Name
							// Check if we already have this tool use (avoid duplicates)
							found := false
							for _, tu := range toolUses {
								if tu != nil && tu.ID == currentToolUse.ID {
									found = true
									// Update the existing one with new input if needed
									if currentToolUse.Input != nil {
										for k, v := range currentToolUse.Input {
											tu.Input[k] = v
										}
									}
									break
								}
							}
							if !found {
								// Make a copy to track
								toolUseCopy := *currentToolUse
								toolUseCopy.Input = make(map[string]interface{})
								if currentToolUse.Input != nil {
									for k, v := range currentToolUse.Input {
										toolUseCopy.Input[k] = v
									}
								}
								toolUses = append(toolUses, &toolUseCopy)
							}
						}
					}
				}

			case llm.StreamEventTypeStop:
				// Stream is done - break out of the event loop
				goto streamDone
			}
		}
	streamDone:

		// Check for stream errors
		if err := stream.Err(); err != nil {
			return "", err
		}

		// Process any accumulated tool uses
		// First, process any tool uses we collected
		if len(toolUses) > 0 {
			for _, toolUse := range toolUses {

				// Marshal tool input to JSON for execution
				raw, err := json.Marshal(toolUse.Input)
				if err != nil {
					logger.Warn("failed to marshal tool input for tool %s (id: %s): %v", toolUse.Name, toolUse.ID, err)
					raw = []byte("{}")
				}

				// Execute tool
				result, callErr := toolExec.Handle(ctx, toolUse.Name, agentID, raw)
				if callErr != nil {
					// Check for repeated identical failing tool calls
					callKey := toolCallKey{
						toolName: toolUse.Name,
						input:    string(raw),
					}
					repeatedFailures[callKey]++
					if repeatedFailures[callKey] >= maxRepeatedFailures {
						logger.Warn("Tool '%s' with input '%s' has failed %d times. Breaking loop to prevent infinite retry", toolUse.Name, string(raw), repeatedFailures[callKey])
						return "", fmt.Errorf("tool '%s' repeatedly failed with same input after %d attempts: %v", toolUse.Name, maxRepeatedFailures, callErr)
					}
					// Return error payload to the model
					result = map[string]any{"error": callErr.Error()}
				} else {
					// Reset failure count on success
					callKey := toolCallKey{
						toolName: toolUse.Name,
						input:    string(raw),
					}
					delete(repeatedFailures, callKey)
				}

				// Summarize result if needed (before marshaling to JSON)
				summarizedResult, summarizeErr := summarizeToolResult(ctx, messageSummarizer, result)
				if summarizeErr != nil {
					logger.Warn("Failed to summarize tool result, using original: %v", summarizeErr)
					summarizedResult = result
				}

				// Track original result for persistence (before summarization)
				toolResultMap[toolUse.ID] = toolResultData{result: result, isError: callErr != nil}

				// Marshal result to JSON string
				b, _ := json.Marshal(summarizedResult)
				isError := callErr != nil

				// Output debug info about tool result
				if debugCallback != nil {
					var resultStr string
					if isError {
						resultStr = fmt.Sprintf("ERROR: %v", callErr)
					} else {
						// Pretty-print result if possible
						if prettyResult, err := json.MarshalIndent(summarizedResult, "", "  "); err == nil {
							resultStr = string(prettyResult)
							// Truncate very long results
							if len(resultStr) > 500 {
								resultStr = resultStr[:500] + "... (truncated)"
							}
						} else {
							resultStr = string(b)
							if len(resultStr) > 500 {
								resultStr = resultStr[:500] + "... (truncated)"
							}
						}
					}
					debugCallback(fmt.Sprintf("✅ Tool result for %s: %s", toolUse.Name, resultStr))
				}

				toolResults = append(toolResults, llm.ToolResultBlock{
					ID:      toolUse.ID,
					Content: string(b),
					IsError: isError,
				})
			}
		}

		// Also handle any currentToolUse that wasn't added to toolUses
		if currentToolUse != nil {
			toolUse := currentToolUse
			currentToolUse = nil

			// Marshal tool input to JSON for execution
			raw, err := json.Marshal(toolUse.Input)
			if err != nil {
				logger.Warn("failed to marshal tool input for tool %s (id: %s): %v", toolUse.Name, toolUse.ID, err)
				raw = []byte("{}")
			}

			// Output debug info about tool call
			if debugCallback != nil {
				var argsStr string
				if prettyArgs, err := json.MarshalIndent(toolUse.Input, "", "  "); err == nil {
					argsStr = string(prettyArgs)
				} else {
					argsStr = string(raw)
				}
				debugCallback(fmt.Sprintf("🔧 Tool call detected: %s\nArguments: %s", toolUse.Name, argsStr))
			}

			// Execute tool
			result, callErr := toolExec.Handle(ctx, toolUse.Name, agentID, raw)
			if callErr != nil {
				// Check for repeated identical failing tool calls
				callKey := toolCallKey{
					toolName: toolUse.Name,
					input:    string(raw),
				}
				repeatedFailures[callKey]++
				if repeatedFailures[callKey] >= maxRepeatedFailures {
					logger.Warn("Tool '%s' with input '%s' has failed %d times. Breaking loop to prevent infinite retry", toolUse.Name, string(raw), repeatedFailures[callKey])
					return "", fmt.Errorf("tool '%s' repeatedly failed with same input after %d attempts: %v", toolUse.Name, maxRepeatedFailures, callErr)
				}
				// Return error payload to the model
				result = map[string]any{"error": callErr.Error()}
			} else {
				// Reset failure count on success
				callKey := toolCallKey{
					toolName: toolUse.Name,
					input:    string(raw),
				}
				delete(repeatedFailures, callKey)
			}

			// Summarize result if needed (before marshaling to JSON)
			summarizedResult, summarizeErr := summarizeToolResult(ctx, messageSummarizer, result)
			if summarizeErr != nil {
				logger.Warn("Failed to summarize tool result, using original: %v", summarizeErr)
				summarizedResult = result
			}

			// Track original result for persistence (before summarization)
			toolResultMap[toolUse.ID] = toolResultData{result: result, isError: callErr != nil}

			// Marshal result to JSON string
			b, _ := json.Marshal(summarizedResult)
			isError := callErr != nil

			// Output debug info about tool result
			if debugCallback != nil {
				var resultStr string
				if isError {
					resultStr = fmt.Sprintf("ERROR: %v", callErr)
				} else {
					// Pretty-print result if possible
					if prettyResult, err := json.MarshalIndent(summarizedResult, "", "  "); err == nil {
						resultStr = string(prettyResult)
						// Truncate very long results
						if len(resultStr) > 500 {
							resultStr = resultStr[:500] + "... (truncated)"
						}
					} else {
						resultStr = string(b)
						if len(resultStr) > 500 {
							resultStr = resultStr[:500] + "... (truncated)"
						}
					}
				}
				debugCallback(fmt.Sprintf("✅ Tool result for %s: %s", toolUse.Name, resultStr))
			}

			// Check if we already have a result for this tool ID (avoid duplicates)
			alreadyExists := false
			for _, tr := range toolResults {
				if tr.ID == toolUse.ID {
					alreadyExists = true
					break
				}
			}
			if !alreadyExists {
				toolResults = append(toolResults, llm.ToolResultBlock{
					ID:      toolUse.ID,
					Content: string(b),
					IsError: isError,
				})
			}
		}

		// If we have tool results, we need to execute them and continue the loop
		if len(toolResults) > 0 {
			// Persist tool calls and results
			if messagePersister != nil {
				for _, toolResult := range toolResults {
					toolName := toolNameMap[toolResult.ID]
					toolData := toolResultMap[toolResult.ID]

					// Persist tool call
					if err := messagePersister.AppendToolCall(ctx, agentID, threadID, toolResult.ID, toolName, toolData.result); err != nil {
						logger.Warn("failed to persist tool call: %v", err)
					}

					// Persist tool result
					if err := messagePersister.AppendToolResult(ctx, agentID, threadID, toolResult.ID, toolName, toolData.result, toolData.isError); err != nil {
						logger.Warn("failed to persist tool result: %v", err)
					}
				}
			}

			// Build assistant message with tool uses
			// Find the tool uses that correspond to the tool results
			// Deduplicate by tool ID to ensure uniqueness
			seenToolIDs := make(map[string]bool)
			assistantToolBlocks := make([]llm.ContentBlock, 0, len(toolResults))
			for _, toolResult := range toolResults {
				// Skip if we've already added this tool use ID
				if seenToolIDs[toolResult.ID] {
					continue
				}
				seenToolIDs[toolResult.ID] = true

				// Find the corresponding tool use from toolUses
				var toolUse *llm.ToolUseBlock
				for _, tu := range toolUses {
					if tu != nil && tu.ID == toolResult.ID {
						toolUse = tu
						break
					}
				}
				// If not found, create a minimal tool use block (shouldn't happen, but be safe)
				if toolUse == nil {
					toolName := toolNameMap[toolResult.ID]
					toolUse = &llm.ToolUseBlock{
						ID:    toolResult.ID,
						Name:  toolName,
						Input: make(map[string]interface{}),
					}
				}
				assistantToolBlocks = append(assistantToolBlocks, llm.ContentBlock{
					Type:    llm.ContentBlockTypeToolUse,
					ToolUse: toolUse,
				})
			}

			// Clear tool uses after building message
			toolUses = nil

			assistantMsg := llm.Message{
				Role:    llm.RoleAssistant,
				Content: assistantToolBlocks,
			}
			conversationHistory = append(conversationHistory, assistantMsg)

			// Build tool result messages
			// Deduplicate tool results by ID to ensure each tool_use has only one result
			seenResultIDs := make(map[string]bool)
			toolResultBlocks := make([]llm.ContentBlock, 0, len(toolResults))
			for _, toolResult := range toolResults {
				// Skip if we've already added a result for this tool ID
				if seenResultIDs[toolResult.ID] {
					continue
				}
				seenResultIDs[toolResult.ID] = true
				toolResultBlocks = append(toolResultBlocks, llm.ContentBlock{
					Type:       llm.ContentBlockTypeToolResult,
					ToolResult: &toolResult,
				})
			}
			toolResultMsg := llm.Message{
				Role:    llm.RoleUser,
				Content: toolResultBlocks,
			}
			conversationHistory = append(conversationHistory, toolResultMsg)

			// Continue loop to process tool results
			continue
		}

		// No tool calls - we have a final text response
		text := strings.TrimSpace(finalText.String())
		if text == "" {
			// Empty response - this shouldn't happen, but handle it gracefully
			return "", fmt.Errorf("received empty response from LLM")
		}

		// Add assistant message to conversation history
		assistantContent := []llm.ContentBlock{
			{
				Type: llm.ContentBlockTypeText,
				Text: text,
			},
		}
		assistantMsg := llm.Message{
			Role:    llm.RoleAssistant,
			Content: assistantContent,
		}
		conversationHistory = append(conversationHistory, assistantMsg)

		// Persist assistant message if we have a message persister
		if messagePersister != nil {
			if err := messagePersister.AppendAssistantMessage(ctx, agentID, threadID, text); err != nil {
				logger.Warn("failed to persist assistant message: %v", err)
			}
		}

		// Return final text response
		return text, nil
	}
}

// summarizeToolResult summarizes the content of a tool result if it exceeds thresholds.
// It extracts text from the result (handling strings, maps, slices) and summarizes if needed.
func summarizeToolResult(ctx context.Context, messageSummarizer *MessageSummarizer, result any) (any, error) {
	if messageSummarizer == nil {
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
	if !messageSummarizer.ShouldSummarize(textContent) {
		return result, nil
	}

	// Summarize the text
	summary, err := messageSummarizer.Summarize(ctx, textContent)
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

// convertAnthropicToolsToLLM converts Anthropic ToolUnionParams to llm.ToolSpecs.
// This is a local conversion to avoid import cycles.
func convertAnthropicToolsToLLM(tools []anthropic.ToolUnionParam) []llm.ToolSpec {
	result := make([]llm.ToolSpec, 0, len(tools))
	for _, tool := range tools {
		if tool.OfTool == nil {
			continue
		}

		t := tool.OfTool
		schema := llm.ToolSchema{
			Type:        "object",
			Properties:  make(map[string]interface{}),
			Required:    t.InputSchema.Required,
			ExtraFields: make(map[string]interface{}),
		}

		// Copy properties
		if t.InputSchema.Properties != nil {
			if propsMap, ok := t.InputSchema.Properties.(map[string]interface{}); ok {
				for k, v := range propsMap {
					schema.Properties[k] = v
				}
			}
		}

		// Copy extra fields
		if t.InputSchema.ExtraFields != nil {
			for k, v := range t.InputSchema.ExtraFields {
				schema.ExtraFields[k] = v
			}
		}

		description := ""
		if t.Description.Value != "" {
			description = t.Description.Value
		}

		result = append(result, llm.ToolSpec{
			Name:        t.Name,
			Description: description,
			Schema:      schema,
		})
	}
	return result
}
