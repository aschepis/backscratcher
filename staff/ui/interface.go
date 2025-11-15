package ui

import (
	"context"

	anthropic "github.com/anthropics/anthropic-sdk-go"
)

// StreamCallback is called for each text delta received from the streaming API
type StreamCallback func(text string) error

// DebugCallback is called for debug information (tool invocations, API calls, etc.)
type DebugCallback func(message string)

// ChatService provides an interface for UI components to interact with agents
// without directly coupling to the agent implementation.
type ChatService interface {
	// SendMessage sends a message to an agent and returns the response.
	// This is a non-streaming call.
	SendMessage(ctx context.Context, agentID, threadID, message string, history []anthropic.MessageParam) (string, error)

	// SendMessageStream sends a message to an agent with streaming support.
	// The streamCallback is called for each text delta received.
	// The debugCallback is added to the context for debug information.
	SendMessageStream(ctx context.Context, agentID, threadID, message string, history []anthropic.MessageParam, streamCallback StreamCallback, debugCallback DebugCallback) (string, error)

	// ListAgents returns a list of available agents.
	ListAgents() []AgentInfo
}

// AgentInfo provides basic information about an agent for UI display.
type AgentInfo struct {
	ID   string
	Name string
}

