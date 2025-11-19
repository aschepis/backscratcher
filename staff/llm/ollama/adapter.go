package ollama

import (
	"fmt"

	"github.com/aschepis/backscratcher/staff/llm"
	"github.com/ollama/ollama/api"
)

// ToOllamaMessages converts llm.Messages to Ollama chat message format.
// Ollama expects messages in a specific format with role and content.
func ToOllamaMessages(msgs []llm.Message) ([]api.Message, error) {
	result := make([]api.Message, 0, len(msgs))
	for _, msg := range msgs {
		ollamaMsg, err := ToOllamaMessage(msg)
		if err != nil {
			return nil, fmt.Errorf("failed to convert message: %w", err)
		}
		result = append(result, ollamaMsg)
	}
	return result, nil
}

// ToOllamaMessage converts a single llm.Message to Ollama format.
func ToOllamaMessage(msg llm.Message) (api.Message, error) {
	// Convert role
	var role string
	switch msg.Role {
	case llm.RoleUser:
		role = "user"
	case llm.RoleAssistant:
		role = "assistant"
	case llm.RoleSystem:
		role = "system"
	default:
		role = "user" // Default fallback
	}

	// Convert content blocks to Ollama format
	// Ollama messages can have text content or tool calls
	var content string
	var toolCalls []api.ToolCall

	for _, block := range msg.Content {
		switch block.Type {
		case llm.ContentBlockTypeText:
			if content != "" {
				content += "\n"
			}
			content += block.Text
		case llm.ContentBlockTypeToolUse:
			if block.ToolUse != nil {
				// Convert tool use to Ollama tool call format
				// ToolCallFunctionArguments is a map[string]any
				args := make(api.ToolCallFunctionArguments)
				if block.ToolUse.Input != nil {
					for k, v := range block.ToolUse.Input {
						args[k] = v
					}
				}
				toolCall := api.ToolCall{
					Function: api.ToolCallFunction{
						Name:      block.ToolUse.Name,
						Arguments: args,
					},
				}
				toolCalls = append(toolCalls, toolCall)
			}
		case llm.ContentBlockTypeToolResult:
			// Tool results are typically sent as separate user messages in Ollama
			// We'll handle this at a higher level if needed
			if block.ToolResult != nil {
				if content != "" {
					content += "\n"
				}
				content += block.ToolResult.Content
			}
		}
	}

	ollamaMsg := api.Message{
		Role: role,
	}

	// Set content or tool calls based on what we have
	if len(toolCalls) > 0 {
		ollamaMsg.ToolCalls = toolCalls
		// If we have both content and tool calls, include content as well
		if content != "" {
			ollamaMsg.Content = content
		}
	} else {
		ollamaMsg.Content = content
	}

	return ollamaMsg, nil
}

// FromOllamaMessage converts an Ollama message to llm.Message.
func FromOllamaMessage(msg api.Message) (llm.Message, error) {
	var role llm.MessageRole
	switch msg.Role {
	case "user":
		role = llm.RoleUser
	case "assistant":
		role = llm.RoleAssistant
	case "system":
		role = llm.RoleSystem
	default:
		role = llm.RoleUser // Default fallback
	}

	content := make([]llm.ContentBlock, 0)

	// Add text content if present
	if msg.Content != "" {
		content = append(content, llm.ContentBlock{
			Type: llm.ContentBlockTypeText,
			Text: msg.Content,
		})
	}

	// Add tool calls if present
	for _, toolCall := range msg.ToolCalls {
		// Arguments is already a map[string]any (ToolCallFunctionArguments)
		input := make(map[string]interface{})
		if toolCall.Function.Arguments != nil {
			for k, v := range toolCall.Function.Arguments {
				input[k] = v
			}
		}

		// Generate a tool use ID (Ollama doesn't provide one, so we'll use the function name + index)
		// In practice, we might need to track this differently
		toolUseID := fmt.Sprintf("call_%s_%d", toolCall.Function.Name, len(content))

		content = append(content, llm.ContentBlock{
			Type: llm.ContentBlockTypeToolUse,
			ToolUse: &llm.ToolUseBlock{
				ID:    toolUseID,
				Name:  toolCall.Function.Name,
				Input: input,
			},
		})
	}

	return llm.Message{
		Role:    role,
		Content: content,
	}, nil
}

// ToOllamaTools converts llm.ToolSpecs to Ollama function format.
// Ollama uses a JSON schema format for function definitions.
func ToOllamaTools(specs []llm.ToolSpec) ([]api.Tool, error) {
	result := make([]api.Tool, 0, len(specs))
	for _, spec := range specs {
		tool, err := ToOllamaTool(spec)
		if err != nil {
			return nil, fmt.Errorf("failed to convert tool %s: %w", spec.Name, err)
		}
		result = append(result, tool)
	}
	return result, nil
}

// ToOllamaTool converts a single llm.ToolSpec to Ollama Tool format.
func ToOllamaTool(spec llm.ToolSpec) (api.Tool, error) {
	// Build JSON schema for the function parameters
	// ToolFunctionParameters is a struct with specific fields
	// Convert Properties from map[string]interface{} to map[string]ToolProperty
	properties := make(map[string]api.ToolProperty)
	if spec.Schema.Properties != nil {
		for k, v := range spec.Schema.Properties {
			// Convert interface{} to ToolProperty
			// ToolProperty likely has a Type field and other schema fields
			// For now, we'll create a basic ToolProperty
			// This is a simplified conversion - full schema conversion would be more complex
			if propMap, ok := v.(map[string]interface{}); ok {
				toolProp := api.ToolProperty{}
				if propType, ok := propMap["type"].(string); ok {
					toolProp.Type = []string{propType}
				}
				// Copy other fields as needed
				properties[k] = toolProp
			} else {
				// Fallback: create a basic property
				properties[k] = api.ToolProperty{
					Type: []string{"string"}, // Default type
				}
			}
		}
	}

	parameters := api.ToolFunctionParameters{
		Type:       spec.Schema.Type,
		Properties: properties,
		Required:   spec.Schema.Required,
	}

	// Create Ollama function definition
	function := api.ToolFunction{
		Name:        spec.Name,
		Description: spec.Description,
		Parameters:  parameters,
	}

	return api.Tool{
		Type:     "function",
		Function: function,
	}, nil
}

// FromOllamaToolCall converts an Ollama tool call response to llm.ToolUseBlock.
func FromOllamaToolCall(toolCall api.ToolCall) (*llm.ToolUseBlock, error) {
	// Arguments is already a map[string]any (ToolCallFunctionArguments)
	input := make(map[string]interface{})
	if toolCall.Function.Arguments != nil {
		for k, v := range toolCall.Function.Arguments {
			input[k] = v
		}
	}

	// Generate ID (Ollama doesn't provide one in the response)
	// We'll use a combination of name and a hash or timestamp
	toolUseID := fmt.Sprintf("tool_%s", toolCall.Function.Name)

	return &llm.ToolUseBlock{
		ID:    toolUseID,
		Name:  toolCall.Function.Name,
		Input: input,
	}, nil
}

