package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/aschepis/backscratcher/staff/api/staffpb"
	"github.com/aschepis/backscratcher/staff/llm"
	"github.com/aschepis/backscratcher/staff/ui"
)

const roleSystem = "system"

// ServiceAdapter implements ui.ChatService by calling the gRPC daemon.
// It adapts all 5 gRPC services (Chat, Agent, Inbox, Memory, System) to the UI interface.
type ServiceAdapter struct {
	client      *Client
	chatTimeout time.Duration
}

// NewServiceAdapter creates a new ServiceAdapter that implements ui.ChatService.
// chatTimeout is the timeout duration for chat operations (default: 60s if 0).
func NewServiceAdapter(client *Client, chatTimeout time.Duration) ui.ChatService {
	if chatTimeout == 0 {
		chatTimeout = 60 * time.Second
	}
	return &ServiceAdapter{
		client:      client,
		chatTimeout: chatTimeout,
	}
}

// SendMessage sends a message to an agent and returns the response (non-streaming).
func (a *ServiceAdapter) SendMessage(ctx context.Context, agentID, threadID, message string, history []llm.Message) (string, error) {
	// Use streaming implementation and collect all text
	var fullResponse string
	callback := func(text string) error {
		fullResponse += text
		return nil
	}
	return a.SendMessageStream(ctx, agentID, threadID, message, history, callback)
}

// SendMessageStream sends a message to an agent with streaming support.
func (a *ServiceAdapter) SendMessageStream(ctx context.Context, agentID, threadID, message string, history []llm.Message, streamCallback ui.StreamCallback) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, a.chatTimeout)
	defer cancel()

	req := &staffpb.ChatRequest{
		AgentId:  agentID,
		ThreadId: threadID,
		Message:  message,
	}

	stream, err := a.client.Chat.Chat(ctx, req)
	if err != nil {
		return "", fmt.Errorf("failed to start chat stream: %w", err)
	}

	var fullResponse string
	for {
		event, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("stream error: %w", err)
		}

		switch e := event.Event.(type) {
		case *staffpb.ChatEvent_TextDelta:
			if e.TextDelta != nil {
				text := e.TextDelta.Text
				if err := streamCallback(text); err != nil {
					return "", fmt.Errorf("stream callback error: %w", err)
				}
				fullResponse += text
			}
		case *staffpb.ChatEvent_Complete:
			if e.Complete != nil {
				fullResponse = e.Complete.FullResponse
			}
		case *staffpb.ChatEvent_Error:
			if e.Error != nil {
				return "", fmt.Errorf("chat error: %s (code: %s)", e.Error.Message, e.Error.Code)
			}
		}
	}

	return fullResponse, nil
}

// GetChatTimeout returns the timeout duration for chat operations.
func (a *ServiceAdapter) GetChatTimeout() time.Duration {
	return a.chatTimeout
}

// ListAgents returns a list of available agents.
func (a *ServiceAdapter) ListAgents() []ui.AgentInfo {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := a.client.Agent.ListAgents(ctx, &staffpb.ListAgentsRequest{})
	if err != nil {
		// Return empty list on error - UI should handle gracefully
		return []ui.AgentInfo{}
	}

	agents := make([]ui.AgentInfo, 0, len(resp.Agents))
	for _, agent := range resp.Agents {
		agents = append(agents, ui.AgentInfo{
			ID:       agent.Id,
			Name:     agent.Name,
			Provider: agent.Provider,
			Model:    agent.Model,
		})
	}
	return agents
}

// ListInboxItems returns a list of inbox items.
func (a *ServiceAdapter) ListInboxItems(ctx context.Context, includeArchived bool) ([]*ui.InboxItem, error) {
	resp, err := a.client.Inbox.ListItems(ctx, &staffpb.ListInboxRequest{
		IncludeArchived: includeArchived,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list inbox items: %w", err)
	}

	items := make([]*ui.InboxItem, 0, len(resp.Items))
	for _, item := range resp.Items {
		uiItem := &ui.InboxItem{
			ID:               item.Id,
			AgentID:          item.AgentId,
			ThreadID:         item.ThreadId,
			Message:          item.Message,
			RequiresResponse: item.RequiresResponse,
			Response:         item.Response,
		}

		if item.ResponseAt != nil {
			responseAt := item.ResponseAt.AsTime()
			uiItem.ResponseAt = &responseAt
		}
		if item.ArchivedAt != nil {
			archivedAt := item.ArchivedAt.AsTime()
			uiItem.ArchivedAt = &archivedAt
		}
		if item.CreatedAt != nil {
			uiItem.CreatedAt = item.CreatedAt.AsTime()
		}
		if item.UpdatedAt != nil {
			uiItem.UpdatedAt = item.UpdatedAt.AsTime()
		}

		items = append(items, uiItem)
	}
	return items, nil
}

// ArchiveInboxItem marks an inbox item as archived.
func (a *ServiceAdapter) ArchiveInboxItem(ctx context.Context, inboxID int64) error {
	_, err := a.client.Inbox.Archive(ctx, &staffpb.ArchiveRequest{
		InboxId: inboxID,
	})
	return err
}

// GetOrCreateThreadID gets an existing thread ID for an agent, or creates a new one.
func (a *ServiceAdapter) GetOrCreateThreadID(ctx context.Context, agentID string) (string, error) {
	resp, err := a.client.Chat.GetOrCreateThread(ctx, &staffpb.GetThreadRequest{
		AgentId: agentID,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get/create thread: %w", err)
	}
	return resp.ThreadId, nil
}

// LoadConversationHistory loads conversation history for a given agent and thread ID.
func (a *ServiceAdapter) LoadConversationHistory(ctx context.Context, agentID, threadID string) ([]llm.Message, error) {
	return a.LoadThread(ctx, agentID, threadID)
}

// LoadThread loads conversation history for a given agent and thread ID.
func (a *ServiceAdapter) LoadThread(ctx context.Context, agentID, threadID string) ([]llm.Message, error) {
	resp, err := a.client.Chat.LoadHistory(ctx, &staffpb.LoadHistoryRequest{
		AgentId:    agentID,
		ThreadId:   threadID,
		IncludeAll: false, // Load active context only
	})
	if err != nil {
		return nil, fmt.Errorf("failed to load history: %w", err)
	}

	messages := make([]llm.Message, 0, len(resp.Messages))
	for _, msg := range resp.Messages {
		llmMsg := convertProtoToMessage(msg)
		messages = append(messages, llmMsg)
	}
	return messages, nil
}

// SaveMessage saves a user or assistant message to the conversation history.
// This is a no-op on the client side - messages are saved automatically by the server.
func (a *ServiceAdapter) SaveMessage(ctx context.Context, agentID, threadID, role, content string) error {
	// Messages are automatically persisted by the server during chat
	return nil
}

// AppendUserMessage is a no-op on the client side - messages are saved automatically by the server.
func (a *ServiceAdapter) AppendUserMessage(ctx context.Context, agentID, threadID, content string) error {
	// Messages are automatically persisted by the server during chat
	return nil
}

// AppendAssistantMessage is a no-op on the client side - messages are saved automatically by the server.
func (a *ServiceAdapter) AppendAssistantMessage(ctx context.Context, agentID, threadID, content string) error {
	// Messages are automatically persisted by the server during chat
	return nil
}

// AppendToolCall is a no-op on the client side - messages are saved automatically by the server.
func (a *ServiceAdapter) AppendToolCall(ctx context.Context, agentID, threadID, toolID, toolName string, toolInput any) error {
	// Messages are automatically persisted by the server during chat
	return nil
}

// AppendToolResult is a no-op on the client side - messages are saved automatically by the server.
func (a *ServiceAdapter) AppendToolResult(ctx context.Context, agentID, threadID, toolID, toolName string, result any, isError bool) error {
	// Messages are automatically persisted by the server during chat
	return nil
}

// ResetContext clears the context by inserting a system message marking the reset.
func (a *ServiceAdapter) ResetContext(ctx context.Context, agentID, threadID string) error {
	_, err := a.client.Chat.ResetContext(ctx, &staffpb.ContextRequest{
		AgentId:  agentID,
		ThreadId: threadID,
	})
	return err
}

// CompressContext summarizes the context and inserts a system message marking the compression.
func (a *ServiceAdapter) CompressContext(ctx context.Context, agentID, threadID string) error {
	_, err := a.client.Chat.CompressContext(ctx, &staffpb.ContextRequest{
		AgentId:  agentID,
		ThreadId: threadID,
	})
	return err
}

// LoadSystemMessages loads system messages (context breaks) for a given agent and thread ID.
func (a *ServiceAdapter) LoadSystemMessages(ctx context.Context, agentID, threadID string) ([]map[string]interface{}, error) {
	// Load all messages and filter for system messages
	resp, err := a.client.Chat.LoadHistory(ctx, &staffpb.LoadHistoryRequest{
		AgentId:    agentID,
		ThreadId:   threadID,
		IncludeAll: true, // Need all messages to find system ones
	})
	if err != nil {
		return nil, fmt.Errorf("failed to load history: %w", err)
	}

	var systemMessages []map[string]interface{}
	for _, msg := range resp.Messages {
		if msg.Role == roleSystem {
			// Parse JSON content
			var msgData map[string]interface{}
			if err := json.Unmarshal([]byte(msg.Content), &msgData); err == nil {
				systemMessages = append(systemMessages, msgData)
			}
		}
	}
	return systemMessages, nil
}

// LoadMessagesWithTimestamps loads regular (non-system) messages with their timestamps.
func (a *ServiceAdapter) LoadMessagesWithTimestamps(ctx context.Context, agentID, threadID string) ([]ui.MessageWithTimestamp, error) {
	resp, err := a.client.Chat.LoadHistory(ctx, &staffpb.LoadHistoryRequest{
		AgentId:    agentID,
		ThreadId:   threadID,
		IncludeAll: false, // Active context only
	})
	if err != nil {
		return nil, fmt.Errorf("failed to load history: %w", err)
	}

	messages := make([]ui.MessageWithTimestamp, 0, len(resp.Messages))
	for _, msg := range resp.Messages {
		if msg.Role != roleSystem {
			messages = append(messages, ui.MessageWithTimestamp{
				Message:   convertProtoToMessage(msg),
				Timestamp: msg.Timestamp,
			})
		}
	}
	return messages, nil
}

// LoadAllMessagesWithTimestamps loads ALL regular (non-system) messages with their timestamps.
func (a *ServiceAdapter) LoadAllMessagesWithTimestamps(ctx context.Context, agentID, threadID string) ([]ui.MessageWithTimestamp, error) {
	resp, err := a.client.Chat.LoadHistory(ctx, &staffpb.LoadHistoryRequest{
		AgentId:    agentID,
		ThreadId:   threadID,
		IncludeAll: true, // All messages
	})
	if err != nil {
		return nil, fmt.Errorf("failed to load history: %w", err)
	}

	messages := make([]ui.MessageWithTimestamp, 0, len(resp.Messages))
	for _, msg := range resp.Messages {
		if msg.Role != roleSystem {
			messages = append(messages, ui.MessageWithTimestamp{
				Message:   convertProtoToMessage(msg),
				Timestamp: msg.Timestamp,
			})
		}
	}
	return messages, nil
}

// GetSystemInfo returns information about the system configuration.
func (a *ServiceAdapter) GetSystemInfo(ctx context.Context) (*ui.SystemInfo, error) {
	infoResp, err := a.client.System.GetInfo(ctx, &staffpb.GetInfoRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to get system info: %w", err)
	}

	toolsResp, err := a.client.System.ListTools(ctx, &staffpb.ListToolsRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to list tools: %w", err)
	}

	mcpResp, err := a.client.System.ListMCPServers(ctx, &staffpb.ListMCPServersRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to list MCP servers: %w", err)
	}

	// Convert tools
	tools := make([]ui.ToolInfo, 0, len(toolsResp.Tools))
	for _, tool := range toolsResp.Tools {
		tools = append(tools, ui.ToolInfo{
			Name:        tool.Name,
			Description: tool.Description,
			Server:      tool.Server,
		})
	}

	// Convert MCP servers
	mcpServers := make([]ui.MCPServerInfo, 0, len(mcpResp.Servers))
	for _, server := range mcpResp.Servers {
		mcpServers = append(mcpServers, ui.MCPServerInfo{
			Name:    server.Name,
			Tools:   server.Tools,
			Enabled: server.Status == "connected",
		})
	}

	return &ui.SystemInfo{
		LLMProvider: infoResp.Version, // Using version as placeholder - adjust if needed
		MCPServers:  mcpServers,
		Tools:       tools,
	}, nil
}

// DumpMemory writes all memory items to a file.
func (a *ServiceAdapter) DumpMemory(ctx context.Context, filePath string) error {
	_, err := a.client.Memory.Dump(ctx, &staffpb.DumpMemoryRequest{
		FilePath: filePath,
	})
	return err
}

// ClearMemory deletes all memory items from the database.
func (a *ServiceAdapter) ClearMemory(ctx context.Context) error {
	_, err := a.client.Memory.Clear(ctx, &staffpb.ClearMemoryRequest{})
	return err
}

// DumpConversations writes conversations grouped by agent to files.
func (a *ServiceAdapter) DumpConversations(ctx context.Context, outputDir string) error {
	_, err := a.client.System.DumpConversations(ctx, &staffpb.DumpConversationsRequest{
		OutputDir: outputDir,
	})
	return err
}

// ClearConversations deletes all conversations from the database.
func (a *ServiceAdapter) ClearConversations(ctx context.Context) error {
	_, err := a.client.System.ClearConversations(ctx, &staffpb.ClearConversationsRequest{})
	return err
}

// ResetStats resets all agent stats.
func (a *ServiceAdapter) ResetStats(ctx context.Context) error {
	_, err := a.client.System.ResetStats(ctx, &staffpb.ResetStatsRequest{})
	return err
}

// DumpInbox writes all inbox items to a file.
func (a *ServiceAdapter) DumpInbox(ctx context.Context, filePath string) error {
	_, err := a.client.System.DumpInbox(ctx, &staffpb.DumpInboxRequest{
		FilePath: filePath,
	})
	return err
}

// ClearInbox deletes all inbox items from the database.
func (a *ServiceAdapter) ClearInbox(ctx context.Context) error {
	_, err := a.client.System.ClearInbox(ctx, &staffpb.ClearInboxRequest{})
	return err
}

// ListAllTools returns all registered tools with formatted names.
func (a *ServiceAdapter) ListAllTools(ctx context.Context) ([]string, error) {
	resp, err := a.client.System.ListTools(ctx, &staffpb.ListToolsRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to list tools: %w", err)
	}

	tools := make([]string, 0, len(resp.Tools))
	for _, tool := range resp.Tools {
		if tool.Server != "" {
			tools = append(tools, fmt.Sprintf("%s:%s", tool.Server, tool.Name))
		} else {
			tools = append(tools, tool.Name)
		}
	}
	return tools, nil
}

// DumpToolSchemas writes all tool schemas to a file as JSON.
func (a *ServiceAdapter) DumpToolSchemas(ctx context.Context, filePath string) error {
	_, err := a.client.System.DumpToolSchemas(ctx, &staffpb.DumpToolSchemasRequest{
		FilePath: filePath,
	})
	return err
}

// convertProtoToMessage converts a protobuf Message to llm.Message.
func convertProtoToMessage(msg *staffpb.Message) llm.Message {
	llmMsg := llm.Message{
		Role: llm.MessageRole(msg.Role),
	}

	// Build content blocks
	if msg.Content != "" {
		// Text content
		llmMsg.Content = []llm.ContentBlock{
			{
				Type: llm.ContentBlockTypeText,
				Text: msg.Content,
			},
		}
	}

	// Handle tool use/result if present
	if msg.ToolId != "" && msg.ToolName != "" {
		if msg.Role == "assistant" {
			// Tool use
			var toolInput map[string]interface{}
			if msg.Content != "" {
				_ = json.Unmarshal([]byte(msg.Content), &toolInput)
			}
			llmMsg.Content = []llm.ContentBlock{
				{
					Type: llm.ContentBlockTypeToolUse,
					ToolUse: &llm.ToolUseBlock{
						ID:    msg.ToolId,
						Name:  msg.ToolName,
						Input: toolInput,
					},
				},
			}
		} else if msg.Role == "tool" {
			// Tool result
			llmMsg.Content = []llm.ContentBlock{
				{
					Type: llm.ContentBlockTypeToolResult,
					ToolResult: &llm.ToolResultBlock{
						ID:      msg.ToolId,
						Content: msg.Content,
					},
				},
			}
		}
	}

	return llmMsg
}
