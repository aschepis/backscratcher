package llm

import (
	"encoding/json"
)

// MessageRole represents the role of a message in a conversation.
type MessageRole string

const (
	RoleUser      MessageRole = "user"
	RoleAssistant MessageRole = "assistant"
	RoleSystem    MessageRole = "system"
)

// Message represents a single message in a conversation.
// This is provider-neutral and can represent user, assistant, or system messages.
type Message struct {
	Role    MessageRole
	Content []ContentBlock
}

// ContentBlock represents a single content block within a message.
// It can be text, a tool use, or a tool result.
type ContentBlock struct {
	Type       ContentBlockType
	Text       string           // For text blocks
	ToolUse    *ToolUseBlock    // For tool use blocks
	ToolResult *ToolResultBlock // For tool result blocks
}

// ContentBlockType represents the type of content block.
type ContentBlockType string

const (
	ContentBlockTypeText       ContentBlockType = "text"
	ContentBlockTypeToolUse    ContentBlockType = "tool_use"
	ContentBlockTypeToolResult ContentBlockType = "tool_result"
)

// ToolUseBlock represents a tool invocation request from the assistant.
type ToolUseBlock struct {
	ID    string
	Name  string
	Input map[string]interface{} // JSON-serializable input parameters
}

// ToolResultBlock represents the result of a tool invocation.
type ToolResultBlock struct {
	ID      string
	Content string // JSON-serialized result
	IsError bool
}

// ToolSpec represents a tool definition that can be provided to an LLM.
type ToolSpec struct {
	Name        string
	Description string
	Schema      ToolSchema
}

// ToolSchema represents the JSON schema for a tool's input parameters.
type ToolSchema struct {
	Type        string
	Properties  map[string]interface{}
	Required    []string
	ExtraFields map[string]interface{} // For any additional schema fields
}

// Request represents a complete LLM API request.
type Request struct {
	Model       string
	Messages    []Message
	System      string
	Tools       []ToolSpec
	MaxTokens   int64
	Temperature *float64 // Optional temperature override
}

// Response represents a complete LLM API response.
type Response struct {
	Content    []ContentBlock
	Usage      *Usage
	StopReason string
}

// Usage represents token usage information from an LLM response.
type Usage struct {
	InputTokens  int64
	OutputTokens int64
	// Provider-specific usage fields can be added here
	CacheCreationInputTokens int64
	CacheReadInputTokens     int64
}

// StreamDelta represents a single delta in a streaming response.
type StreamDelta struct {
	Type      StreamDeltaType
	Text      string        // For text deltas
	ToolUse   *ToolUseBlock // For tool use start
	ToolInput string        // For tool input JSON deltas
}

// StreamDeltaType represents the type of streaming delta.
type StreamDeltaType string

const (
	StreamDeltaTypeText      StreamDeltaType = "text"
	StreamDeltaTypeToolUse   StreamDeltaType = "tool_use"
	StreamDeltaTypeToolInput StreamDeltaType = "tool_input"
)

// StreamEvent represents a complete streaming event.
type StreamEvent struct {
	Type  StreamEventType
	Delta *StreamDelta
	Usage *Usage
	Done  bool
}

// StreamEventType represents the type of streaming event.
type StreamEventType string

const (
	StreamEventTypeStart        StreamEventType = "start"
	StreamEventTypeContentBlock StreamEventType = "content_block"
	StreamEventTypeContentDelta StreamEventType = "content_delta"
	StreamEventTypeMessageDelta StreamEventType = "message_delta"
	StreamEventTypeStop         StreamEventType = "stop"
)

// NewTextMessage creates a new user message with text content.
func NewTextMessage(role MessageRole, text string) Message {
	return Message{
		Role: role,
		Content: []ContentBlock{
			{
				Type: ContentBlockTypeText,
				Text: text,
			},
		},
	}
}

// NewToolUseMessage creates a new assistant message with tool use blocks.
func NewToolUseMessage(toolUses []ToolUseBlock) Message {
	content := make([]ContentBlock, len(toolUses))
	for i, tu := range toolUses {
		content[i] = ContentBlock{
			Type:    ContentBlockTypeToolUse,
			ToolUse: &tu,
		}
	}
	return Message{
		Role:    RoleAssistant,
		Content: content,
	}
}

// NewToolResultMessage creates a new user message with tool result blocks.
func NewToolResultMessage(toolResults []ToolResultBlock) Message {
	content := make([]ContentBlock, len(toolResults))
	for i, tr := range toolResults {
		content[i] = ContentBlock{
			Type:       ContentBlockTypeToolResult,
			ToolResult: &tr,
		}
	}
	return Message{
		Role:    RoleUser,
		Content: content,
	}
}

// ToJSON marshals a message to JSON for debugging/logging purposes.
func (m Message) ToJSON() ([]byte, error) {
	return json.Marshal(m)
}
