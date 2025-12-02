package openai

import (
	"encoding/json"
	"fmt"

	"github.com/aschepis/backscratcher/staff/llm"
	openai "github.com/sashabaranov/go-openai"
	// Note: Using loops instead of lo.Map due to error handling requirements
)

// ToOpenAIMessages converts llm.Messages to OpenAI chat message format.
func ToOpenAIMessages(msgs []llm.Message) ([]openai.ChatCompletionMessage, error) {
	result := make([]openai.ChatCompletionMessage, 0, len(msgs))
	for _, msg := range msgs {
		openaiMsg, err := ToOpenAIMessage(msg)
		if err != nil {
			return nil, fmt.Errorf("failed to convert message: %w", err)
		}
		result = append(result, openaiMsg)
	}
	return result, nil
}

// ToOpenAIMessage converts a single llm.Message to OpenAI format.
func ToOpenAIMessage(msg llm.Message) (openai.ChatCompletionMessage, error) {
	// Convert role
	var role string
	switch msg.Role {
	case llm.RoleUser:
		role = openai.ChatMessageRoleUser
	case llm.RoleAssistant:
		role = openai.ChatMessageRoleAssistant
	case llm.RoleSystem:
		role = openai.ChatMessageRoleSystem
	default:
		role = openai.ChatMessageRoleUser // Default fallback
	}

	// Convert content blocks
	// OpenAI messages can have text content or tool calls
	var content string
	var toolCalls []openai.ToolCall

	for _, block := range msg.Content {
		switch block.Type {
		case llm.ContentBlockTypeText:
			if content != "" {
				content += "\n"
			}
			content += block.Text
		case llm.ContentBlockTypeToolUse:
			if block.ToolUse != nil {
				// Convert tool use to OpenAI tool call format
				// OpenAI uses function calling with name and arguments (JSON string)
				argsJSON, err := json.Marshal(block.ToolUse.Input)
				if err != nil {
					return openai.ChatCompletionMessage{}, fmt.Errorf("failed to marshal tool input: %w", err)
				}
				toolCall := openai.ToolCall{
					ID:   block.ToolUse.ID,
					Type: openai.ToolTypeFunction,
					Function: openai.FunctionCall{
						Name:      block.ToolUse.Name,
						Arguments: string(argsJSON),
					},
				}
				toolCalls = append(toolCalls, toolCall)
			}
		case llm.ContentBlockTypeToolResult:
			// Tool results are typically sent as separate user messages in OpenAI
			// We'll handle this at a higher level if needed
			if block.ToolResult != nil {
				if content != "" {
					content += "\n"
				}
				content += block.ToolResult.Content
			}
		}
	}

	openaiMsg := openai.ChatCompletionMessage{
		Role: role,
	}

	// Set content or tool calls based on what we have
	if len(toolCalls) > 0 {
		openaiMsg.ToolCalls = toolCalls
		// If we have both content and tool calls, include content as well
		if content != "" {
			openaiMsg.Content = content
		}
	} else {
		openaiMsg.Content = content
	}

	return openaiMsg, nil
}

// ToOpenAITools converts llm.ToolSpecs to OpenAI function format.
// OpenAI uses a JSON schema format for function definitions.
func ToOpenAITools(specs []llm.ToolSpec) ([]openai.Tool, error) {
	result := make([]openai.Tool, 0, len(specs))
	for i := range specs {
		tool, err := ToOpenAITool(&specs[i])
		if err != nil {
			return nil, fmt.Errorf("failed to convert tool %s: %w", specs[i].Name, err)
		}
		result = append(result, tool)
	}
	return result, nil
}

// ToOpenAITool converts a single llm.ToolSpec to OpenAI Tool format.
func ToOpenAITool(spec *llm.ToolSpec) (openai.Tool, error) {
	// Build JSON schema for the function parameters
	// Convert Properties from map[string]interface{} to proper JSON schema
	properties := make(map[string]interface{})
	if spec.Schema.Properties != nil {
		for k, v := range spec.Schema.Properties {
			properties[k] = v
		}
	}

	// Build the parameters schema
	parameters := map[string]interface{}{
		"type":       spec.Schema.Type,
		"properties": properties,
	}
	if len(spec.Schema.Required) > 0 {
		parameters["required"] = spec.Schema.Required
	}

	// Add any extra fields
	if spec.Schema.ExtraFields != nil {
		for k, v := range spec.Schema.ExtraFields {
			parameters[k] = v
		}
	}

	// Create OpenAI function definition
	function := openai.FunctionDefinition{
		Name:        spec.Name,
		Description: spec.Description,
		Parameters:  parameters,
	}

	return openai.Tool{
		Type:     openai.ToolTypeFunction,
		Function: &function,
	}, nil
}

// FromOpenAIToolCall converts an OpenAI tool call response to llm.ToolUseBlock.
func FromOpenAIToolCall(toolCall openai.ToolCall) (*llm.ToolUseBlock, error) {
	// Parse arguments JSON string
	var input map[string]interface{}
	if toolCall.Function.Arguments != "" {
		if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &input); err != nil {
			// If parsing fails, create empty map
			input = make(map[string]interface{})
		}
	} else {
		input = make(map[string]interface{})
	}

	return &llm.ToolUseBlock{
		ID:    toolCall.ID,
		Name:  toolCall.Function.Name,
		Input: input,
	}, nil
}
