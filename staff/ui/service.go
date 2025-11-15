package ui

import (
	"context"

	anthropic "github.com/anthropics/anthropic-sdk-go"

	"github.com/aschepis/backscratcher/staff/agent"
)

// chatService implements ChatService by wrapping an agent.Crew
type chatService struct {
	crew *agent.Crew
}

// NewChatService creates a new ChatService that wraps the given crew.
func NewChatService(crew *agent.Crew) ChatService {
	return &chatService{
		crew: crew,
	}
}

// SendMessage sends a message to an agent and returns the response.
func (s *chatService) SendMessage(ctx context.Context, agentID, threadID, message string, history []anthropic.MessageParam) (string, error) {
	return s.crew.Run(ctx, agentID, threadID, message, history)
}

// SendMessageStream sends a message to an agent with streaming support.
func (s *chatService) SendMessageStream(ctx context.Context, agentID, threadID, message string, history []anthropic.MessageParam, streamCallback StreamCallback, debugCallback DebugCallback) (string, error) {
	// Add debug callback to context if provided
	if debugCallback != nil {
		ctx = agent.WithDebugCallback(ctx, agent.DebugCallback(debugCallback))
	}

	return s.crew.RunStream(ctx, agentID, threadID, message, history, agent.StreamCallback(streamCallback))
}

// ListAgents returns a list of available agents.
func (s *chatService) ListAgents() []AgentInfo {
	agents := s.crew.ListAgents()
	info := make([]AgentInfo, 0, len(agents))
	for _, ag := range agents {
		info = append(info, AgentInfo{
			ID:   ag.ID,
			Name: ag.Config.Name,
		})
	}
	return info
}

