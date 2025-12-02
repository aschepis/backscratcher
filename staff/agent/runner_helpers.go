package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aschepis/backscratcher/staff/llm"
	"github.com/aschepis/backscratcher/staff/ui/tui/debug"
	"github.com/rs/zerolog"
	"github.com/samber/lo"
)

// Constants for loop safeguards
const (
	maxIterations       = 20
	maxRepeatedFailures = 3
)

// toolCallKey is used to track repeated identical failing tool calls.
type toolCallKey struct {
	toolName string
	input    string // JSON string of input
}

// toolExecutionResult holds the result of executing a single tool.
type toolExecutionResult struct {
	ToolID          string
	ToolName        string
	Result          any    // Original result for persistence
	SummarizedJSON  string // JSON-serialized summarized result for LLM
	IsError         bool
	RepeatedFailure bool // True if this tool has failed too many times
}

// toolLoopContext holds shared context for tool loop execution.
type toolLoopContext struct {
	ctx               context.Context
	agentID           string
	threadID          string
	toolExec          ToolExecutor
	messagePersister  MessagePersister
	messageSummarizer *MessageSummarizer
	repeatedFailures  map[toolCallKey]int
	logger            zerolog.Logger
}

// newToolLoopContext creates a new tool loop context.
func newToolLoopContext(
	ctx context.Context,
	agentID, threadID string,
	toolExec ToolExecutor,
	messagePersister MessagePersister,
	messageSummarizer *MessageSummarizer,
	logger zerolog.Logger,
) *toolLoopContext {
	return &toolLoopContext{
		ctx:               ctx,
		agentID:           agentID,
		threadID:          threadID,
		toolExec:          toolExec,
		messagePersister:  messagePersister,
		messageSummarizer: messageSummarizer,
		repeatedFailures:  make(map[toolCallKey]int),
		logger:            logger.With().Str("component", "toolLoopContext").Logger(),
	}
}

// executeSingleTool executes a single tool and returns the result.
// It handles failure tracking, summarization, and result formatting.
func (tlc *toolLoopContext) executeSingleTool(toolUse *llm.ToolUseBlock) (*toolExecutionResult, error) {
	if toolUse == nil {
		return nil, fmt.Errorf("toolUse is nil")
	}

	// Marshal tool input to JSON for execution
	raw, err := json.Marshal(toolUse.Input)
	if err != nil {
		tlc.logger.Warn().Err(err).Str("toolName", toolUse.Name).Str("toolID", toolUse.ID).Msg("failed to marshal tool input")
		raw = []byte("{}")
	}

	// Output debug info about tool call
	debug.ChatMessage(tlc.ctx, fmt.Sprintf("🔧 Tool call detected: %s\nArguments: %s", toolUse.Name, string(raw)))

	// Execute tool
	result, callErr := tlc.toolExec.Handle(tlc.ctx, toolUse.Name, tlc.agentID, raw)

	callKey := toolCallKey{
		toolName: toolUse.Name,
		input:    string(raw),
	}

	if callErr != nil {
		// Check for repeated identical failing tool calls
		tlc.repeatedFailures[callKey]++
		if tlc.repeatedFailures[callKey] >= maxRepeatedFailures {
			tlc.logger.Warn().
				Str("toolName", toolUse.Name).
				Str("input", string(raw)).
				Int("failures", tlc.repeatedFailures[callKey]).
				Msg("Tool has failed too many times. Breaking loop to prevent infinite retry")
			return &toolExecutionResult{
					ToolID:          toolUse.ID,
					ToolName:        toolUse.Name,
					RepeatedFailure: true,
				}, fmt.Errorf("tool '%s' repeatedly failed with same input after %d attempts: %v",
					toolUse.Name, maxRepeatedFailures, callErr)
		}
		// Return error payload to the model
		result = map[string]any{"error": callErr.Error()}
	} else {
		// Reset failure count on success
		delete(tlc.repeatedFailures, callKey)
	}

	// Summarize result if needed (before marshaling to JSON)
	summarizedResult, summarizeErr := summarizeToolResult(tlc.ctx, tlc.messageSummarizer, result)
	if summarizeErr != nil {
		tlc.logger.Warn().Err(summarizeErr).Msg("Failed to summarize tool result, using original")
		summarizedResult = result
	}

	// Marshal result to JSON string
	summarizedJSON, _ := json.Marshal(summarizedResult)
	isError := callErr != nil

	// Output debug info about tool result
	if isError {
		debug.ChatMessage(tlc.ctx, fmt.Sprintf("❌ Tool result for %s: %v", toolUse.Name, callErr))
	} else {
		resultStr := string(summarizedJSON)
		if len(resultStr) > 500 {
			resultStr = resultStr[:500] + "... (truncated)"
		}
		debug.ChatMessage(tlc.ctx, fmt.Sprintf("✅ Tool result for %s: %s", toolUse.Name, resultStr))
	}

	return &toolExecutionResult{
		ToolID:         toolUse.ID,
		ToolName:       toolUse.Name,
		Result:         result,
		SummarizedJSON: string(summarizedJSON),
		IsError:        isError,
	}, nil
}

// persistToolCalls persists tool calls to storage.
func (tlc *toolLoopContext) persistToolCalls(contentBlocks []llm.ContentBlock) {
	if tlc.messagePersister == nil {
		return
	}

	for _, block := range contentBlocks {
		if block.Type == llm.ContentBlockTypeToolUse && block.ToolUse != nil {
			toolUse := block.ToolUse
			if err := tlc.messagePersister.AppendToolCall(tlc.ctx, tlc.agentID, tlc.threadID,
				toolUse.ID, toolUse.Name, toolUse.Input); err != nil {
				tlc.logger.Warn().Err(err).Msg("failed to persist tool call")
			}
		}
	}
}

// persistToolResults persists tool results to storage.
func (tlc *toolLoopContext) persistToolResults(results []*toolExecutionResult) {
	if tlc.messagePersister == nil {
		return
	}

	for _, result := range results {
		if err := tlc.messagePersister.AppendToolResult(tlc.ctx, tlc.agentID, tlc.threadID,
			result.ToolID, result.ToolName, result.Result, result.IsError); err != nil {
			tlc.logger.Warn().Err(err).Msg("failed to persist tool result")
		}
	}
}

// persistFinalMessage persists the final assistant message.
func (tlc *toolLoopContext) persistFinalMessage(text string) {
	if tlc.messagePersister == nil || text == "" {
		return
	}

	if err := tlc.messagePersister.AppendAssistantMessage(tlc.ctx, tlc.agentID, tlc.threadID, text); err != nil {
		tlc.logger.Warn().Err(err).Msg("failed to persist assistant message")
	}
}

// buildToolResultBlocks builds deduplicated tool result content blocks.
func buildToolResultBlocks(results []*toolExecutionResult) []llm.ContentBlock {
	seenIDs := make(map[string]bool)
	return lo.FilterMap(results, func(result *toolExecutionResult, _ int) (llm.ContentBlock, bool) {
		if seenIDs[result.ToolID] {
			return llm.ContentBlock{}, false
		}
		seenIDs[result.ToolID] = true
		return llm.ContentBlock{
			Type: llm.ContentBlockTypeToolResult,
			ToolResult: &llm.ToolResultBlock{
				ID:      result.ToolID,
				Content: result.SummarizedJSON,
				IsError: result.IsError,
			},
		}, true
	})
}

// buildToolResultMessage creates a user message containing tool results.
func buildToolResultMessage(results []*toolExecutionResult) llm.Message {
	return llm.Message{
		Role:    llm.RoleUser,
		Content: buildToolResultBlocks(results),
	}
}

// summarizeFinalText summarizes the final assistant text if needed.
func summarizeFinalText(ctx context.Context, summarizer *MessageSummarizer, text string, logger zerolog.Logger) string {
	if summarizer == nil || !summarizer.ShouldSummarize(text) {
		return text
	}

	summarized, err := summarizer.Summarize(ctx, text)
	if err != nil {
		logger.Warn().Err(err).Msg("Failed to summarize assistant message, using original")
		return text
	}
	return summarized
}

// prepareLLMRequest converts agent config, history, and tools to an llm.Request.
func prepareLLMRequest(
	agent *Agent,
	resolvedModel string,
	userMsg string,
	history []llm.Message,
	toolProvider ToolProvider,
) *llm.Request {
	toolSpecs := toolProvider.SpecsFor(agent.Config)

	messages := make([]llm.Message, 0, len(history)+1)
	messages = append(messages, history...)
	messages = append(messages, llm.NewTextMessage(llm.RoleUser, userMsg))

	return &llm.Request{
		Model:     resolvedModel,
		Messages:  messages,
		System:    agent.Config.System,
		Tools:     toolSpecs,
		MaxTokens: agent.Config.MaxTokens,
	}
}

// executeToolLoop executes a tool execution loop using the LLM client.
func executeToolLoop(
	ctx context.Context,
	client llm.Client,
	req *llm.Request,
	agentID string,
	threadID string,
	toolExec ToolExecutor,
	messagePersister MessagePersister,
	messageSummarizer *MessageSummarizer,
	logger zerolog.Logger,
) (string, error) {
	tlc := newToolLoopContext(ctx, agentID, threadID, toolExec, messagePersister, messageSummarizer, logger)
	conversationHistory := req.Messages

	for iterationCount := 1; iterationCount <= maxIterations; iterationCount++ {
		currentReq := &llm.Request{
			Model:     req.Model,
			Messages:  conversationHistory,
			System:    req.System,
			Tools:     req.Tools,
			MaxTokens: req.MaxTokens,
		}

		debug.ChatMessage(ctx, fmt.Sprintf("🤖 Calling LLM (model: %s, messages: %d, tools: %d)",
			req.Model, len(conversationHistory), len(req.Tools)))

		resp, err := client.Synchronous(ctx, currentReq)
		if err != nil {
			return "", err
		}

		// Process response: collect text and execute tools
		var finalText strings.Builder
		var toolResults []*toolExecutionResult

		for _, block := range resp.Content {
			switch block.Type {
			case llm.ContentBlockTypeText:
				finalText.WriteString(block.Text)
				finalText.WriteRune('\n')

			case llm.ContentBlockTypeToolUse:
				if block.ToolUse == nil {
					continue
				}
				result, err := tlc.executeSingleTool(block.ToolUse)
				if err != nil && result != nil && result.RepeatedFailure {
					return "", err
				}
				if result != nil {
					toolResults = append(toolResults, result)
				}
			}
		}

		// Add assistant message to conversation history
		conversationHistory = append(conversationHistory, llm.Message{
			Role:    llm.RoleAssistant,
			Content: resp.Content,
		})

		// If no tool calls, we're done
		if len(toolResults) == 0 {
			finalTextStr := strings.TrimSpace(finalText.String())
			finalTextStr = summarizeFinalText(ctx, messageSummarizer, finalTextStr, logger)
			tlc.persistFinalMessage(finalTextStr)
			return finalTextStr, nil
		}

		// Persist tool calls and results
		tlc.persistToolCalls(resp.Content)
		tlc.persistToolResults(toolResults)

		// Add tool results and continue loop
		conversationHistory = append(conversationHistory, buildToolResultMessage(toolResults))
	}

	return "", fmt.Errorf("tool loop exceeded maximum iterations (%d). Possible infinite loop detected", maxIterations)
}

// executeToolLoopStream executes a tool execution loop with streaming support.
func executeToolLoopStream(
	ctx context.Context,
	client llm.Client,
	req *llm.Request,
	agentID string,
	threadID string,
	toolExec ToolExecutor,
	messagePersister MessagePersister,
	messageSummarizer *MessageSummarizer,
	streamCallback StreamCallback,
	logger zerolog.Logger,
) (string, error) {
	tlc := newToolLoopContext(ctx, agentID, threadID, toolExec, messagePersister, messageSummarizer, logger)
	conversationHistory := req.Messages

	for iterationCount := 1; iterationCount <= maxIterations; iterationCount++ {
		currentReq := &llm.Request{
			Model:     req.Model,
			Messages:  conversationHistory,
			System:    req.System,
			Tools:     req.Tools,
			MaxTokens: req.MaxTokens,
		}

		debug.ChatMessage(ctx, fmt.Sprintf("🤖 Calling LLM stream (model: %s, messages: %d, tools: %d)",
			req.Model, len(conversationHistory), len(req.Tools)))

		stream, err := client.Stream(ctx, currentReq)
		if err != nil {
			return "", err
		}

		// Collect streaming results
		var finalText strings.Builder
		toolUses := make(map[string]*llm.ToolUseBlock)         // Deduplicated by ID
		toolInputBuilders := make(map[string]*strings.Builder) // Accumulate JSON input per tool ID
		var currentToolID string                               // Track which tool is currently receiving input

		// Process stream events
		for stream.Next() {
			event := stream.Event()
			if event == nil {
				continue
			}

			switch event.Type {
			case llm.StreamEventTypeContentDelta, llm.StreamEventTypeContentBlock:
				if event.Delta == nil {
					continue
				}
				switch event.Delta.Type {
				case llm.StreamDeltaTypeText:
					if event.Delta.Text != "" {
						finalText.WriteString(event.Delta.Text)
						if streamCallback != nil {
							if err := streamCallback(event.Delta.Text); err != nil {
								_ = stream.Close()
								return "", fmt.Errorf("stream callback error: %w", err)
							}
						}
					}
				case llm.StreamDeltaTypeToolUse:
					if tu := event.Delta.ToolUse; tu != nil {
						if _, ok := toolUses[tu.ID]; !ok {
							// Copy and store (Input will be populated later from accumulated JSON)
							toolCopy := *tu
							toolCopy.Input = make(map[string]interface{})
							toolUses[tu.ID] = &toolCopy
							// Initialize input builder for this tool
							toolInputBuilders[tu.ID] = &strings.Builder{}
						}
						// Track current tool for subsequent input deltas
						currentToolID = tu.ID
					}
				case llm.StreamDeltaTypeToolInput:
					// Accumulate tool input JSON delta
					if currentToolID != "" {
						if builder, ok := toolInputBuilders[currentToolID]; ok {
							builder.WriteString(event.Delta.ToolInput)
						}
					}
				}

			case llm.StreamEventTypeStop:
				goto streamDone
			}
		}
	streamDone:

		// Parse accumulated JSON input for each tool
		for id, builder := range toolInputBuilders {
			if tu, ok := toolUses[id]; ok && builder.Len() > 0 {
				var input map[string]interface{}
				if err := json.Unmarshal([]byte(builder.String()), &input); err == nil {
					tu.Input = input
				}
			}
		}

		if err := stream.Err(); err != nil {
			_ = stream.Close()
			return "", err
		}
		_ = stream.Close()

		// Execute collected tools
		var toolResults []*toolExecutionResult
		var toolUsesSlice []*llm.ToolUseBlock

		for _, tu := range toolUses {
			toolUsesSlice = append(toolUsesSlice, tu)
			result, err := tlc.executeSingleTool(tu)
			if err != nil && result != nil && result.RepeatedFailure {
				return "", err
			}
			if result != nil {
				toolResults = append(toolResults, result)
			}
		}

		// If no tool calls, we're done
		if len(toolResults) == 0 {
			text := strings.TrimSpace(finalText.String())
			if text == "" {
				return "", fmt.Errorf("received empty response from LLM")
			}
			tlc.persistFinalMessage(text)
			return text, nil
		}

		// Build and persist assistant message with tool uses
		assistantBlocks := lo.Map(toolUsesSlice, func(tu *llm.ToolUseBlock, _ int) llm.ContentBlock {
			return llm.ContentBlock{
				Type:    llm.ContentBlockTypeToolUse,
				ToolUse: tu,
			}
		})

		// Persist tool calls using the content blocks
		tlc.persistToolCalls(assistantBlocks)
		tlc.persistToolResults(toolResults)

		conversationHistory = append(conversationHistory,
			llm.Message{Role: llm.RoleAssistant, Content: assistantBlocks},
			buildToolResultMessage(toolResults),
		)
	}

	return "", fmt.Errorf("tool loop exceeded maximum iterations (%d). Possible infinite loop detected", maxIterations)
}

// summarizeToolResult summarizes the content of a tool result if it exceeds thresholds.
func summarizeToolResult(ctx context.Context, messageSummarizer *MessageSummarizer, result any) (any, error) {
	if messageSummarizer == nil {
		return result, nil
	}

	textContent := extractTextContent(result)
	if textContent == "" || !messageSummarizer.ShouldSummarize(textContent) {
		return result, nil
	}

	summary, err := messageSummarizer.Summarize(ctx, textContent)
	if err != nil {
		return result, err
	}

	return replaceTextContent(result, summary), nil
}

// extractTextContent extracts text content from various result types.
func extractTextContent(result any) string {
	switch v := result.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	case map[string]any:
		if content, ok := v["content"].(string); ok {
			return content
		}
		if text, ok := v["text"].(string); ok {
			return text
		}
		if message, ok := v["message"].(string); ok {
			return message
		}
		if b, err := json.Marshal(v); err == nil {
			return string(b)
		}
	case []any:
		var parts []string
		for _, item := range v {
			if str, ok := item.(string); ok {
				parts = append(parts, str)
			} else if b, err := json.Marshal(item); err == nil {
				parts = append(parts, string(b))
			}
		}
		return strings.Join(parts, "\n")
	default:
		if b, err := json.Marshal(result); err == nil {
			return string(b)
		}
	}
	return ""
}

// replaceTextContent replaces text content in result with summary.
func replaceTextContent(result any, summary string) any {
	switch v := result.(type) {
	case string:
		return summary
	case []byte:
		return []byte(summary)
	case map[string]any:
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
			resultCopy["summary"] = summary
		}
		return resultCopy
	case []any:
		return []any{summary}
	default:
		return summary
	}
}
