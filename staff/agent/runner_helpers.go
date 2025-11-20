package agent

import (
	"context"
	"encoding/json"
	"strings"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/aschepis/backscratcher/staff/llm"
	"github.com/aschepis/backscratcher/staff/logger"
)

// prepareLLMRequest converts agent config, history, and tools to an llm.Request.
func prepareLLMRequest(
	agent *Agent,
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
		Model:     agent.Config.Model,
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

	for {
		// Create request with current conversation history
		currentReq := &llm.Request{
			Model:     req.Model,
			Messages:  conversationHistory,
			System:    req.System,
			Tools:     req.Tools,
			MaxTokens: req.MaxTokens,
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

				// Execute tool
				result, callErr := toolExec.Handle(ctx, toolUse.Name, agentID, raw)
				if callErr != nil {
					// Return error payload to the model
					result = map[string]any{"error": callErr.Error()}
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
				toolResults = append(toolResults, llm.ToolResultBlock{
					ID:      toolUse.ID,
					Content: string(b),
					IsError: isError,
				})
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
		toolResultContent := make([]llm.ContentBlock, 0, len(toolResults))
		for _, tr := range toolResults {
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
