package ui

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"

	"github.com/aschepis/backscratcher/staff/agent"
)

const (
	roleAssistant = "assistant"
)

// chatService implements ChatService by wrapping an agent.Crew
type chatService struct {
	crew *agent.Crew
	db   *sql.DB
}

// NewChatService creates a new ChatService that wraps the given crew and database.
func NewChatService(crew *agent.Crew, db *sql.DB) ChatService {
	cs := &chatService{
		crew: crew,
		db:   db,
	}
	// Register chatService as the message persister for the crew
	crew.SetMessagePersister(cs)
	return cs
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

// ListInboxItems returns a list of inbox items, optionally filtered by archived status.
func (s *chatService) ListInboxItems(ctx context.Context, includeArchived bool) ([]*InboxItem, error) {
	var query string
	if includeArchived {
		query = `SELECT id, agent_id, thread_id, message, requires_response, response, 
		                response_at, archived_at, created_at, updated_at
		         FROM inbox
		         ORDER BY created_at DESC`
	} else {
		query = `SELECT id, agent_id, thread_id, message, requires_response, response, 
		                response_at, archived_at, created_at, updated_at
		         FROM inbox
		         WHERE archived_at IS NULL
		         ORDER BY created_at DESC`
	}

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // No remedy for rows close errors

	var items []*InboxItem
	for rows.Next() {
		var item InboxItem
		var agentID, threadID sql.NullString
		var response sql.NullString
		var responseAt, archivedAt, createdAt, updatedAt sql.NullInt64

		err := rows.Scan(
			&item.ID,
			&agentID,
			&threadID,
			&item.Message,
			&item.RequiresResponse,
			&response,
			&responseAt,
			&archivedAt,
			&createdAt,
			&updatedAt,
		)
		if err != nil {
			return nil, err
		}

		if agentID.Valid {
			item.AgentID = agentID.String
		}
		if threadID.Valid {
			item.ThreadID = threadID.String
		}
		if response.Valid {
			item.Response = response.String
		}
		if responseAt.Valid {
			t := time.Unix(responseAt.Int64, 0)
			item.ResponseAt = &t
		}
		if archivedAt.Valid {
			t := time.Unix(archivedAt.Int64, 0)
			item.ArchivedAt = &t
		}
		if createdAt.Valid {
			item.CreatedAt = time.Unix(createdAt.Int64, 0)
		}
		if updatedAt.Valid {
			item.UpdatedAt = time.Unix(updatedAt.Int64, 0)
		}

		items = append(items, &item)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return items, nil
}

// ArchiveInboxItem marks an inbox item as archived.
func (s *chatService) ArchiveInboxItem(ctx context.Context, inboxID int64) error {
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx,
		`UPDATE inbox SET archived_at = ?, updated_at = ? WHERE id = ?`,
		now, now, inboxID,
	)
	return err
}

// GetOrCreateThreadID gets an existing thread ID for an agent, or creates a new one if none exists.
func (s *chatService) GetOrCreateThreadID(ctx context.Context, agentID string) (string, error) {
	// Check if there's an existing thread for this agent
	var existingThreadID sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT DISTINCT thread_id FROM conversations WHERE agent_id = ? ORDER BY created_at DESC LIMIT 1`,
		agentID,
	).Scan(&existingThreadID)

	if err == nil && existingThreadID.Valid && existingThreadID.String != "" {
		return existingThreadID.String, nil
	}

	// No existing thread found, create a new one
	threadID := fmt.Sprintf("chat-%s-%d", agentID, time.Now().Unix())
	return threadID, nil
}

// LoadConversationHistory loads conversation history for a given agent and thread ID.
// Also available as LoadThread for API consistency.
func (s *chatService) LoadConversationHistory(ctx context.Context, agentID, threadID string) ([]anthropic.MessageParam, error) {
	return s.LoadThread(ctx, agentID, threadID)
}

// LoadThread loads conversation history for a given agent and thread ID.
// Reconstructs proper Anthropic message structures from database rows.
func (s *chatService) LoadThread(ctx context.Context, agentID, threadID string) ([]anthropic.MessageParam, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT role, content, tool_name, created_at FROM conversations 
		 WHERE agent_id = ? AND thread_id = ? 
		 ORDER BY created_at ASC`,
		agentID, threadID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // No remedy for rows close errors

	var messages []anthropic.MessageParam
	var currentUserTextBlocks []string
	var currentAssistantTextBlocks []string
	var currentAssistantToolBlocks []anthropic.ContentBlockParamUnion
	var currentToolResultBlocks []anthropic.ContentBlockParamUnion
	var lastRole string
	// Track tool_use IDs to prevent duplicates within the same message
	seenToolUseIDs := make(map[string]bool)
	seenToolResultIDs := make(map[string]bool)

	for rows.Next() {
		var role string
		var content string
		var toolName sql.NullString
		var createdAt int64

		if err := rows.Scan(&role, &content, &toolName, &createdAt); err != nil {
			return nil, err
		}

		// Handle different message types
		switch role {
		case "user":
			// User text message
			if lastRole == "user" {
				currentUserTextBlocks = append(currentUserTextBlocks, content)
			} else {
				// Role changed, commit previous messages
				s.commitPendingMessages(&messages, currentUserTextBlocks, currentAssistantTextBlocks,
					currentAssistantToolBlocks, currentToolResultBlocks)

				currentUserTextBlocks = []string{content}
				currentAssistantTextBlocks = nil
				currentAssistantToolBlocks = nil
				currentToolResultBlocks = nil
				// Reset seen IDs when role changes
				seenToolUseIDs = make(map[string]bool)
				seenToolResultIDs = make(map[string]bool)
			}

		case roleAssistant:
			if toolName.Valid && toolName.String != "" {
				// Assistant message with tool call
				// Parse the JSON content to extract tool use block information
				var toolUseData map[string]interface{}
				if err := json.Unmarshal([]byte(content), &toolUseData); err != nil {
					// If JSON parsing fails, skip this message or log error
					continue
				}

				// Extract tool use block fields
				toolID, _ := toolUseData["id"].(string)
				if toolID == "" {
					// Skip if no tool ID
					continue
				}

				// Check for duplicate tool_use ID
				if seenToolUseIDs[toolID] {
					// Skip duplicate tool_use ID
					continue
				}
				seenToolUseIDs[toolID] = true

				toolInput := toolUseData["input"]
				toolNameStr := toolName.String

				// Create tool use block
				toolUseBlock := anthropic.NewToolUseBlock(toolID, toolInput, toolNameStr)
				currentAssistantToolBlocks = append(currentAssistantToolBlocks, toolUseBlock)

				// Commit if role changed
				if lastRole != roleAssistant && lastRole != "" {
					s.commitPendingMessages(&messages, currentUserTextBlocks, currentAssistantTextBlocks,
						currentAssistantToolBlocks, currentToolResultBlocks)
					currentUserTextBlocks = nil
					currentAssistantTextBlocks = nil
					currentAssistantToolBlocks = nil
					currentToolResultBlocks = nil
					// Reset seen IDs when role changes
					seenToolUseIDs = make(map[string]bool)
					seenToolResultIDs = make(map[string]bool)
				}
			} else {
				// Assistant text message
				if lastRole == roleAssistant && len(currentAssistantToolBlocks) == 0 {
					currentAssistantTextBlocks = append(currentAssistantTextBlocks, content)
				} else {
					// Role changed or we have tool blocks, commit previous messages
					s.commitPendingMessages(&messages, currentUserTextBlocks, currentAssistantTextBlocks,
						currentAssistantToolBlocks, currentToolResultBlocks)

					currentUserTextBlocks = nil
					currentAssistantTextBlocks = []string{content}
					currentAssistantToolBlocks = nil
					currentToolResultBlocks = nil
					// Reset seen IDs when role changes
					seenToolUseIDs = make(map[string]bool)
					seenToolResultIDs = make(map[string]bool)
				}
			}

		case "tool":
			// Tool result message - these are sent as user messages with ToolResultBlock
			if toolName.Valid && toolName.String != "" {
				// Parse the JSON content to extract tool result information
				var toolResultData map[string]interface{}
				if err := json.Unmarshal([]byte(content), &toolResultData); err != nil {
					// If JSON parsing fails, skip this message or log error
					continue
				}

				// Extract tool result block fields
				toolID, _ := toolResultData["id"].(string)
				if toolID == "" {
					// Skip if no tool ID
					continue
				}

				// Check for duplicate tool result ID
				if seenToolResultIDs[toolID] {
					// Skip duplicate tool result ID
					continue
				}
				seenToolResultIDs[toolID] = true

				resultStr, _ := toolResultData["result"].(string)
				isError, _ := toolResultData["is_error"].(bool)

				// If result is not a string, marshal it back to JSON
				if resultStr == "" {
					if resultBytes, err := json.Marshal(toolResultData["result"]); err == nil {
						resultStr = string(resultBytes)
					}
				}

				// Create tool result block
				toolResultBlock := anthropic.NewToolResultBlock(toolID, resultStr, isError)
				currentToolResultBlocks = append(currentToolResultBlocks, toolResultBlock)

				// Commit if role changed
				if lastRole != "tool" && lastRole != "" {
					s.commitPendingMessages(&messages, currentUserTextBlocks, currentAssistantTextBlocks,
						currentAssistantToolBlocks, currentToolResultBlocks)
					currentUserTextBlocks = nil
					currentAssistantTextBlocks = nil
					currentAssistantToolBlocks = nil
					currentToolResultBlocks = nil
					// Reset seen IDs when role changes
					seenToolUseIDs = make(map[string]bool)
					seenToolResultIDs = make(map[string]bool)
				}
			}
		}

		lastRole = role
	}

	// Commit any remaining messages
	s.commitPendingMessages(&messages, currentUserTextBlocks, currentAssistantTextBlocks,
		currentAssistantToolBlocks, currentToolResultBlocks)

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return messages, nil
}

// commitPendingMessages commits any pending message groups to the messages slice.
func (s *chatService) commitPendingMessages(
	messages *[]anthropic.MessageParam,
	userTextBlocks []string,
	assistantTextBlocks []string,
	assistantToolBlocks []anthropic.ContentBlockParamUnion,
	toolResultBlocks []anthropic.ContentBlockParamUnion,
) {
	// Commit user text messages
	if len(userTextBlocks) > 0 {
		*messages = append(*messages, anthropic.NewUserMessage(
			anthropic.NewTextBlock(strings.Join(userTextBlocks, "\n")),
		))
	}

	// Commit assistant messages (text or tool calls)
	if len(assistantTextBlocks) > 0 {
		*messages = append(*messages, anthropic.NewAssistantMessage(
			anthropic.NewTextBlock(strings.Join(assistantTextBlocks, "\n")),
		))
	}
	if len(assistantToolBlocks) > 0 {
		*messages = append(*messages, anthropic.NewAssistantMessage(assistantToolBlocks...))
	}

	// Commit tool result messages as user messages
	if len(toolResultBlocks) > 0 {
		*messages = append(*messages, anthropic.NewUserMessage(toolResultBlocks...))
	}
}

// SaveMessage saves a user or assistant message to the conversation history.
func (s *chatService) SaveMessage(ctx context.Context, agentID, threadID, role, content string) error {
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO conversations (agent_id, thread_id, role, content, tool_name, created_at)
		 VALUES (?, ?, ?, ?, NULL, ?)`,
		agentID, threadID, role, content, now,
	)
	return err
}

// AppendUserMessage saves a user text message to the conversation history.
func (s *chatService) AppendUserMessage(ctx context.Context, agentID, threadID, content string) error {
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO conversations (agent_id, thread_id, role, content, tool_name, created_at)
		 VALUES (?, ?, 'user', ?, NULL, ?)`,
		agentID, threadID, content, now,
	)
	return err
}

// AppendAssistantMessage saves an assistant text-only message to the conversation history.
func (s *chatService) AppendAssistantMessage(ctx context.Context, agentID, threadID, content string) error {
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO conversations (agent_id, thread_id, role, content, tool_name, created_at)
		 VALUES (?, ?, 'assistant', ?, NULL, ?)`,
		agentID, threadID, content, now,
	)
	return err
}

// AppendToolCall saves an assistant message with tool use blocks to the conversation history.
// toolID is the unique ID for this tool call.
// toolName is the name of the tool being called.
// toolInput is the input parameters for the tool (will be JSON-marshaled).
// Uses INSERT OR IGNORE to prevent duplicate tool_use IDs in case of crashes/restarts.
func (s *chatService) AppendToolCall(ctx context.Context, agentID, threadID, toolID, toolName string, toolInput any) error {
	// Create a JSON object with id, input, and name fields
	toolUseData := map[string]interface{}{
		"id":    toolID,
		"input": toolInput,
		"name":  toolName,
	}
	contentJSON, err := json.Marshal(toolUseData)
	if err != nil {
		return fmt.Errorf("marshal tool use data: %w", err)
	}

	now := time.Now().Unix()
	// Use INSERT OR IGNORE to prevent duplicates based on unique index on (agent_id, thread_id, tool_id)
	_, err = s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO conversations (agent_id, thread_id, role, content, tool_name, tool_id, created_at)
		 VALUES (?, ?, 'assistant', ?, ?, ?, ?)`,
		agentID, threadID, string(contentJSON), toolName, toolID, now,
	)
	return err
}

// AppendToolResult saves a tool result message to the conversation history.
// toolID is the unique ID for the tool call that produced this result.
// toolName is the name of the tool that produced the result.
// result is the tool result (will be JSON-marshaled).
// isError indicates if the result represents an error.
// Uses INSERT OR IGNORE to prevent duplicate tool results in case of crashes/restarts.
func (s *chatService) AppendToolResult(ctx context.Context, agentID, threadID, toolID, toolName string, result any, isError bool) error {
	// Marshal the result to JSON string
	var resultStr string
	if resultBytes, err := json.Marshal(result); err == nil {
		resultStr = string(resultBytes)
	} else {
		resultStr = fmt.Sprintf("%v", result)
	}

	// Create a JSON object with id, result, and is_error fields
	toolResultData := map[string]interface{}{
		"id":       toolID,
		"result":   resultStr,
		"is_error": isError,
	}
	contentJSON, err := json.Marshal(toolResultData)
	if err != nil {
		return fmt.Errorf("marshal tool result data: %w", err)
	}

	now := time.Now().Unix()
	// Use INSERT OR IGNORE to prevent duplicates based on unique index on (agent_id, thread_id, tool_id, role)
	// The unique index allows one 'assistant' row and one 'tool' row per tool_id, preventing duplicate results
	_, err = s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO conversations (agent_id, thread_id, role, content, tool_name, tool_id, created_at)
		 VALUES (?, ?, 'tool', ?, ?, ?, ?)`,
		agentID, threadID, string(contentJSON), toolName, toolID, now,
	)
	return err
}
