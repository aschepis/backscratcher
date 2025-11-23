package ollama

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	ctxpkg "github.com/aschepis/backscratcher/staff/context"
	"github.com/aschepis/backscratcher/staff/llm"
	"github.com/aschepis/backscratcher/staff/ui/tui/debug"
	"github.com/ollama/ollama/api"
)

// validateAndConvertToolArguments validates required parameters and converts
// argument values to their proper types based on the tool schema.
func validateAndConvertToolArguments(ctx context.Context, toolName string, args map[string]interface{}, schema llm.ToolSchema) (api.ToolCallFunctionArguments, error) {
	result := make(api.ToolCallFunctionArguments)

	// Output what we received from LLM
	debug.ChatMessage(ctx, fmt.Sprintf("📥 LLM provided arguments for %s:\n%v", toolName, args))

	// Validate required parameters
	requiredSet := make(map[string]bool)
	for _, req := range schema.Required {
		requiredSet[req] = true
	}

	// Check that all required parameters are present and non-empty
	for _, reqParam := range schema.Required {
		val, exists := args[reqParam]
		if !exists {
			// Build helpful error message showing what was provided
			providedKeys := make([]string, 0, len(args))
			for k := range args {
				providedKeys = append(providedKeys, k)
			}
			return nil, fmt.Errorf("missing required parameter '%s' for tool '%s' (provided: %v)", reqParam, toolName, providedKeys)
		}
		// Check if value is empty (nil, empty string, etc.)
		if isEmptyValue(val) {
			return nil, fmt.Errorf("required parameter '%s' for tool '%s' cannot be empty", reqParam, toolName)
		}
	}

	// Convert arguments based on schema types
	properties := schema.Properties
	if properties == nil {
		properties = make(map[string]interface{})
	}

	for k, v := range args {
		// Get the property schema for this parameter
		propSchema, exists := properties[k]
		if !exists {
			// Parameter not in schema, pass through as-is
			result[k] = v
			continue
		}

		// Extract type from property schema
		propType := getPropertyType(propSchema)

		// Convert value to the correct type
		converted, err := convertValueToType(v, propType, k)
		if err != nil {
			return nil, fmt.Errorf("failed to convert parameter '%s' for tool '%s': %w", k, toolName, err)
		}

		result[k] = converted

		// Mark as processed if it was required
		delete(requiredSet, k)
	}

	// After conversion, output what was converted
	debug.ChatMessage(ctx, fmt.Sprintf("✅ Converted/validated arguments for %s:\n%v", toolName, result))

	return result, nil
}

// isEmptyValue checks if a value is considered empty (nil, empty string, empty array, etc.)
func isEmptyValue(v interface{}) bool {
	if v == nil {
		return true
	}

	switch val := v.(type) {
	case string:
		return val == ""
	case []interface{}:
		return len(val) == 0
	case []string:
		return len(val) == 0
	case map[string]interface{}:
		return len(val) == 0
	}

	return false
}

// getPropertyType extracts the type from a property schema definition
func getPropertyType(propSchema interface{}) string {
	if propMap, ok := propSchema.(map[string]interface{}); ok {
		if propType, ok := propMap["type"].(string); ok {
			return propType
		}
	}
	return "string" // Default type
}

// convertValueToType converts a value to the specified type
func convertValueToType(v interface{}, targetType, paramName string) (interface{}, error) {
	// If already the correct type, return as-is
	switch targetType {
	case "integer", "int":
		return convertToInteger(v, paramName)
	case "number", "float":
		return convertToNumber(v, paramName)
	case "boolean", "bool":
		return convertToBoolean(v, paramName)
	case "string":
		return convertToString(v), nil
	case "array":
		// Arrays are typically passed through, but we could validate
		return v, nil
	case "object":
		// Objects are typically passed through
		return v, nil
	default:
		// Unknown type, pass through
		return v, nil
	}
}

// convertToInteger converts a value to an integer
func convertToInteger(v interface{}, paramName string) (interface{}, error) {
	switch val := v.(type) {
	case int:
		return val, nil
	case int64:
		return int(val), nil
	case float64:
		return int(val), nil
	case string:
		// Try to parse string as integer
		var i int
		if _, err := fmt.Sscanf(val, "%d", &i); err != nil {
			return nil, fmt.Errorf("parameter '%s': cannot convert '%s' to integer", paramName, val)
		}
		return i, nil
	default:
		return nil, fmt.Errorf("parameter '%s': cannot convert %T to integer", paramName, v)
	}
}

// convertToNumber converts a value to a float64
func convertToNumber(v interface{}, paramName string) (interface{}, error) {
	switch val := v.(type) {
	case float64:
		return val, nil
	case int:
		return float64(val), nil
	case int64:
		return float64(val), nil
	case string:
		var f float64
		if _, err := fmt.Sscanf(val, "%f", &f); err != nil {
			return nil, fmt.Errorf("parameter '%s': cannot convert '%s' to number", paramName, val)
		}
		return f, nil
	default:
		return nil, fmt.Errorf("parameter '%s': cannot convert %T to number", paramName, v)
	}
}

// convertToBoolean converts a value to a boolean
func convertToBoolean(v interface{}, paramName string) (interface{}, error) {
	switch val := v.(type) {
	case bool:
		return val, nil
	case string:
		switch strings.ToLower(val) {
		case "true", "1", "yes", "on":
			return true, nil
		case "false", "0", "no", "off":
			return false, nil
		default:
			return nil, fmt.Errorf("parameter '%s': cannot convert '%s' to boolean", paramName, val)
		}
	case int:
		return val != 0, nil
	default:
		return nil, fmt.Errorf("parameter '%s': cannot convert %T to boolean", paramName, v)
	}
}

// convertToString converts a value to a string
func convertToString(v interface{}) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

// ToOllamaMessages converts llm.Messages to Ollama chat message format.
// It optionally accepts tool specs for validating and converting tool arguments.
func ToOllamaMessages(ctx context.Context, msgs []llm.Message, toolSpecs ...[]llm.ToolSpec) ([]api.Message, error) {
	var toolSpecMap map[string]llm.ToolSpec
	if len(toolSpecs) > 0 && len(toolSpecs[0]) > 0 {
		toolSpecMap = make(map[string]llm.ToolSpec)
		for _, spec := range toolSpecs[0] {
			toolSpecMap[spec.Name] = spec
		}
	}

	result := make([]api.Message, 0, len(msgs))
	for _, msg := range msgs {
		ollamaMsg, err := ToOllamaMessage(ctx, msg, toolSpecMap)
		if err != nil {
			return nil, fmt.Errorf("failed to convert message: %w", err)
		}
		result = append(result, ollamaMsg)
	}
	return result, nil
}

// ToOllamaMessage converts a single llm.Message to Ollama format.
// toolSpecMap is optional and used for validating/converting tool arguments.
func ToOllamaMessage(ctx context.Context, msg llm.Message, toolSpecMap map[string]llm.ToolSpec) (api.Message, error) {
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
				var args api.ToolCallFunctionArguments

				if toolSpecMap != nil {
					// Validate and convert arguments using schema
					if spec, ok := toolSpecMap[block.ToolUse.Name]; ok {
						convertedArgs, err := validateAndConvertToolArguments(
							ctx,
							block.ToolUse.Name,
							block.ToolUse.Input,
							spec.Schema,
						)
						if err != nil {
							return api.Message{}, fmt.Errorf("tool argument validation failed: %w", err)
						}
						args = convertedArgs
					} else {
						// No schema found, pass through as-is (for backward compatibility)
						args = make(api.ToolCallFunctionArguments)
						if block.ToolUse.Input != nil {
							for k, v := range block.ToolUse.Input {
								args[k] = v
							}
						}
					}
				} else {
					// No tool specs provided, pass through as-is (for backward compatibility)
					args = make(api.ToolCallFunctionArguments)
					if block.ToolUse.Input != nil {
						for k, v := range block.ToolUse.Input {
							args[k] = v
						}
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
		Role:      string(msg.Role),
		Content:   content,
		ToolCalls: toolCalls,
	}

	return ollamaMsg, nil
}

// FromOllamaMessage converts an Ollama message to llm.Message.
func FromOllamaMessage(ctx context.Context, msg *api.Message) (llm.Message, error) {
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

		// Attempt to retrieve debugCallback from context and log the raw arguments if available
		if cb, ok := ctxpkg.GetDebugCallback(ctx); ok && cb != nil {
			var pretty string
			if b, err := json.MarshalIndent(input, "", "  "); err == nil {
				pretty = string(b)
			} else {
				pretty = fmt.Sprintf("%v", input)
			}
			cb(fmt.Sprintf("🔍 [FromOllamaMessage] Raw tool call arguments for function '%s':\n%s", toolCall.Function.Name, pretty))
		}

		// Generate a tool use ID (Ollama doesn't provide one, so we'll use the function name + index)
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
		tool, err := ToOllamaTool(&spec)
		if err != nil {
			return nil, fmt.Errorf("failed to convert tool %s: %w", spec.Name, err)
		}
		result = append(result, tool)
	}
	return result, nil
}

// ToOllamaTool converts a single llm.ToolSpec to Ollama Tool format.
func ToOllamaTool(spec *llm.ToolSpec) (api.Tool, error) {
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
