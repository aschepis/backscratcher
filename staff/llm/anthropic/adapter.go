package anthropic

import (
	"encoding/json"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/aschepis/backscratcher/staff/llm"
	"github.com/samber/lo"
)

// FromMessageParam converts an Anthropic MessageParam to an llm.Message.
func FromMessageParam(msg anthropic.MessageParam) (llm.Message, error) {
	var role llm.MessageRole
	switch string(msg.Role) {
	case "user":
		role = llm.RoleUser
	case "assistant":
		role = llm.RoleAssistant
	default:
		role = llm.RoleUser // Default fallback
	}

	content := make([]llm.ContentBlock, 0, len(msg.Content))
	for _, blockUnion := range msg.Content {
		// Check for text blocks
		if blockUnion.OfText != nil {
			content = append(content, llm.ContentBlock{
				Type: llm.ContentBlockTypeText,
				Text: blockUnion.OfText.Text,
			})
		}
		// Check for tool use blocks
		if blockUnion.OfToolUse != nil {
			// Extract input as map[string]interface{}
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
		// Check for tool result blocks
		if blockUnion.OfToolResult != nil {
			// Extract content string from ContentUnion slice
			var contentStr string
			for _, contentUnion := range blockUnion.OfToolResult.Content {
				if contentUnion.OfText != nil {
					contentStr += contentUnion.OfText.Text
				}
			}
			// Extract isError - it's a param.Opt[bool], get the value if set
			isError := false
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

	return llm.Message{
		Role:    role,
		Content: content,
	}, nil
}

// ToMessageParam converts an llm.Message to an Anthropic MessageParam.
func ToMessageParam(msg llm.Message) (anthropic.MessageParam, error) {
	contentBlocks := make([]anthropic.ContentBlockParamUnion, 0, len(msg.Content))
	for _, block := range msg.Content {
		switch block.Type {
		case llm.ContentBlockTypeText:
			contentBlocks = append(contentBlocks, anthropic.NewTextBlock(block.Text))
		case llm.ContentBlockTypeToolUse:
			if block.ToolUse != nil {
				contentBlocks = append(contentBlocks, anthropic.NewToolUseBlock(
					block.ToolUse.ID,
					block.ToolUse.Input,
					block.ToolUse.Name,
				))
			}
		case llm.ContentBlockTypeToolResult:
			if block.ToolResult != nil {
				contentBlocks = append(contentBlocks, anthropic.NewToolResultBlock(
					block.ToolResult.ID,
					block.ToolResult.Content,
					block.ToolResult.IsError,
				))
			}
		}
	}

	switch msg.Role {
	case llm.RoleUser:
		return anthropic.NewUserMessage(contentBlocks...), nil
	case llm.RoleAssistant:
		return anthropic.NewAssistantMessage(contentBlocks...), nil
	default:
		return anthropic.NewUserMessage(contentBlocks...), nil
	}
}

// FromMessageParams converts a slice of Anthropic MessageParams to llm.Messages.
func FromMessageParams(msgs []anthropic.MessageParam) ([]llm.Message, error) {
	result := make([]llm.Message, 0, len(msgs))
	for _, msg := range msgs {
		llmMsg, err := FromMessageParam(msg)
		if err != nil {
			return nil, err
		}
		result = append(result, llmMsg)
	}
	return result, nil
	// Note: Using loop instead of lo.Map due to error handling requirement
}

// ToMessageParams converts a slice of llm.Messages to Anthropic MessageParams.
func ToMessageParams(msgs []llm.Message) ([]anthropic.MessageParam, error) {
	result := make([]anthropic.MessageParam, 0, len(msgs))
	for _, msg := range msgs {
		anthMsg, err := ToMessageParam(msg)
		if err != nil {
			return nil, err
		}
		result = append(result, anthMsg)
	}
	return result, nil
	// Note: Using loop instead of lo.Map due to error handling requirement
}

// FromToolUnionParam converts an Anthropic ToolUnionParam to an llm.ToolSpec.
func FromToolUnionParam(tool anthropic.ToolUnionParam) (llm.ToolSpec, error) {
	if tool.OfTool == nil {
		return llm.ToolSpec{}, nil
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

	return llm.ToolSpec{
		Name:        t.Name,
		Description: description,
		Schema:      schema,
	}, nil
}

// ToToolUnionParam converts an llm.ToolSpec to an Anthropic ToolUnionParam.
func ToToolUnionParam(spec *llm.ToolSpec) anthropic.ToolUnionParam {
	desc := anthropic.String(spec.Description)

	toolParam := anthropic.ToolParam{
		Name:        spec.Name,
		Description: desc,
		InputSchema: anthropic.ToolInputSchemaParam{
			Type:        "object",
			Properties:  spec.Schema.Properties,
			Required:    spec.Schema.Required,
			ExtraFields: spec.Schema.ExtraFields,
		},
	}

	return anthropic.ToolUnionParam{OfTool: &toolParam}
}

// FromToolUnionParams converts a slice of Anthropic ToolUnionParams to llm.ToolSpecs.
func FromToolUnionParams(tools []anthropic.ToolUnionParam) ([]llm.ToolSpec, error) {
	result := make([]llm.ToolSpec, 0, len(tools))
	for _, tool := range tools {
		spec, err := FromToolUnionParam(tool)
		if err != nil {
			return nil, err
		}
		result = append(result, spec)
	}
	return result, nil
	// Note: Using loop instead of lo.Map due to error handling requirement
}

// ToToolUnionParams converts a slice of llm.ToolSpecs to Anthropic ToolUnionParams.
func ToToolUnionParams(specs []llm.ToolSpec) []anthropic.ToolUnionParam {
	return lo.Map(specs, func(spec llm.ToolSpec, _ int) anthropic.ToolUnionParam {
		return ToToolUnionParam(&spec)
	})
}
