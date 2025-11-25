package ui

import (
	"context"
	"time"

	"github.com/aschepis/backscratcher/staff/llm"
)

// StreamCallback is called for each text delta received from the streaming API
type StreamCallback func(text string) error

// DebugCallback is called for debug information (tool invocations, API calls, etc.)
type DebugCallback func(message string)

// ChatService provides an interface for UI components to interact with agents
// without directly coupling to the agent implementation.
// All message types use the provider-neutral llm.Message type.
type ChatService interface {
	// SendMessage sends a message to an agent and returns the response.
	// This is a non-streaming call.
	SendMessage(ctx context.Context, agentID, threadID, message string, history []llm.Message) (string, error)

	// SendMessageStream sends a message to an agent with streaming support.
	// The streamCallback is called for each text delta received.
	SendMessageStream(ctx context.Context, agentID, threadID, message string, history []llm.Message, streamCallback StreamCallback) (string, error)

	// GetChatTimeout returns the timeout duration for chat operations.
	GetChatTimeout() time.Duration

	// ListAgents returns a list of available agents.
	ListAgents() []AgentInfo

	// ListInboxItems returns a list of inbox items, optionally filtered by archived status.
	ListInboxItems(ctx context.Context, includeArchived bool) ([]*InboxItem, error)

	// ArchiveInboxItem marks an inbox item as archived.
	ArchiveInboxItem(ctx context.Context, inboxID int64) error

	// GetOrCreateThreadID gets an existing thread ID for an agent, or creates a new one if none exists.
	GetOrCreateThreadID(ctx context.Context, agentID string) (string, error)

	// LoadConversationHistory loads conversation history for a given agent and thread ID.
	LoadConversationHistory(ctx context.Context, agentID, threadID string) ([]llm.Message, error)

	// LoadThread loads conversation history for a given agent and thread ID.
	// Reconstructs proper message structures from database rows.
	LoadThread(ctx context.Context, agentID, threadID string) ([]llm.Message, error)

	// SaveMessage saves a user or assistant message to the conversation history.
	SaveMessage(ctx context.Context, agentID, threadID, role, content string) error

	// AppendUserMessage saves a user text message to the conversation history.
	AppendUserMessage(ctx context.Context, agentID, threadID, content string) error

	// AppendAssistantMessage saves an assistant text-only message to the conversation history.
	AppendAssistantMessage(ctx context.Context, agentID, threadID, content string) error

	// AppendToolCall saves an assistant message with tool use blocks to the conversation history.
	// toolID is the unique ID for this tool call.
	// toolName is the name of the tool being called.
	// toolInput is the input parameters for the tool (will be JSON-marshaled).
	AppendToolCall(ctx context.Context, agentID, threadID, toolID, toolName string, toolInput any) error

	// AppendToolResult saves a tool result message to the conversation history.
	// toolID is the unique ID for the tool call that produced this result.
	// toolName is the name of the tool that produced the result.
	// result is the tool result (will be JSON-marshaled).
	// isError indicates if the result represents an error.
	AppendToolResult(ctx context.Context, agentID, threadID, toolID, toolName string, result any, isError bool) error

	// ResetContext clears the context by inserting a system message marking the reset.
	ResetContext(ctx context.Context, agentID, threadID string) error

	// CompressContext summarizes the context and inserts a system message marking the compression.
	CompressContext(ctx context.Context, agentID, threadID string) error

	// LoadSystemMessages loads system messages (context breaks) for a given agent and thread ID.
	LoadSystemMessages(ctx context.Context, agentID, threadID string) ([]map[string]interface{}, error)

	// LoadMessagesWithTimestamps loads regular (non-system) messages with their timestamps.
	// Only loads messages after the most recent reset or compression break (if any).
	// This is used for LLM context - only messages after the break are sent to the model.
	LoadMessagesWithTimestamps(ctx context.Context, agentID, threadID string) ([]MessageWithTimestamp, error)

	// LoadAllMessagesWithTimestamps loads ALL regular (non-system) messages with their timestamps.
	// This is used for display purposes to show the full conversation history.
	LoadAllMessagesWithTimestamps(ctx context.Context, agentID, threadID string) ([]MessageWithTimestamp, error)

	// GetSystemInfo returns information about the system configuration.
	GetSystemInfo(ctx context.Context) (*SystemInfo, error)
}

// MessageWithTimestamp represents a message with its database timestamp.
// Uses the provider-neutral llm.Message type.
type MessageWithTimestamp struct {
	Message   llm.Message
	Timestamp int64
}

// AgentInfo provides basic information about an agent for UI display.
type AgentInfo struct {
	ID       string
	Name     string
	Provider string // e.g., "anthropic", "ollama", "openai"
	Model    string // e.g., "claude-sonnet-4-20250514"
}

// InboxItem represents an inbox notification item.
type InboxItem struct {
	ID               int64
	AgentID          string
	ThreadID         string
	Message          string
	RequiresResponse bool
	Response         string
	ResponseAt       *time.Time
	ArchivedAt       *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// SystemInfo provides information about the system configuration.
type SystemInfo struct {
	LLMProvider string
	MCPServers  []MCPServerInfo
	Tools       []ToolInfo
}

// MCPServerInfo provides information about an MCP server.
type MCPServerInfo struct {
	Name    string
	Tools   []string
	Enabled bool
}

// ToolInfo provides information about a tool.
type ToolInfo struct {
	Name        string
	Description string
	Server      string // MCP server name if from MCP, empty for native tools
}
