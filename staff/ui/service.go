package ui

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/rs/zerolog"

	"github.com/aschepis/backscratcher/staff/agent"
	"github.com/aschepis/backscratcher/staff/config"
	"github.com/aschepis/backscratcher/staff/llm"
)

const (
	roleAssistant = "assistant"
	roleUser      = "user"
	roleTool      = "tool"
	roleSystem    = "system"

	providerAnthropic = "anthropic"
	providerOllama    = "ollama"
)

// chatService implements ChatService by wrapping an agent.Crew
type chatService struct {
	crew    *agent.Crew
	db      *sql.DB
	timeout time.Duration // Timeout for chat operations
	config  *config.Config
	logger  zerolog.Logger
}

// NewChatService creates a new ChatService that wraps the given crew and database.
// timeoutSeconds is the timeout in seconds for chat operations (default: 60 if 0).
func NewChatService(logger zerolog.Logger, crew *agent.Crew, db *sql.DB, timeoutSeconds int, appConfig *config.Config) ChatService {
	if timeoutSeconds <= 0 {
		timeoutSeconds = 60 // Default timeout
	}
	cs := &chatService{
		crew:    crew,
		db:      db,
		timeout: time.Duration(timeoutSeconds) * time.Second,
		config:  appConfig,
		logger:  logger.With().Str("component", "chatService").Logger(),
	}
	// Register chatService as the message persister for the crew
	crew.SetMessagePersister(cs) // TODO: fix this.. it is weird.
	return cs
}

// SendMessage sends a message to an agent and returns the response.
// History is provided as provider-neutral llm.Message types.
func (s *chatService) SendMessage(ctx context.Context, agentID, threadID, message string, history []llm.Message) (string, error) {
	return s.crew.Run(ctx, agentID, threadID, message, history)
}

// SendMessageStream sends a message to an agent with streaming support.
// History is provided as provider-neutral llm.Message types.
func (s *chatService) SendMessageStream(ctx context.Context, agentID, threadID, message string, history []llm.Message, streamCallback StreamCallback) (string, error) {
	return s.crew.RunStream(ctx, agentID, threadID, message, history, agent.StreamCallback(streamCallback))
}

// ListAgents returns a list of available agents.
func (s *chatService) ListAgents() []AgentInfo {
	agents := s.crew.ListAgents()
	info := make([]AgentInfo, 0, len(agents))
	for _, ag := range agents {
		agentInfo := AgentInfo{
			ID:   ag.ID,
			Name: ag.Config.Name,
		}

		// Get the actual resolved provider and model from the runner
		// This ensures we show the model that's actually being used, not just the first preference
		runner := s.crew.GetRunner(ag.ID)
		if runner != nil {
			agentInfo.Provider = runner.GetResolvedProvider()
			agentInfo.Model = runner.GetResolvedModel()
		} else {
			// Runner not available (e.g., agent disabled or not initialized)
			// Fall back to config-based inference
			if len(ag.Config.LLM) > 0 {
				pref := ag.Config.LLM[0]
				agentInfo.Provider = pref.Provider
				if pref.Model != "" {
					agentInfo.Model = pref.Model
				} else {
					agentInfo.Model = ag.Config.Model // Fallback to legacy model field
				}
			} else {
				// No preferences - use legacy model field and infer provider from config
				agentInfo.Model = ag.Config.Model
				// Try to infer provider from model name (heuristic)
				switch {
				case strings.HasPrefix(ag.Config.Model, "claude-"):
					agentInfo.Provider = providerAnthropic
				case strings.Contains(ag.Config.Model, ":"):
					agentInfo.Provider = providerOllama
				default:
					agentInfo.Provider = providerAnthropic // Default
				}
			}
		}

		info = append(info, agentInfo)
	}
	return info
}

// ListInboxItems returns a list of inbox items, optionally filtered by archived status.
func (s *chatService) ListInboxItems(ctx context.Context, includeArchived bool) ([]*InboxItem, error) {
	query := sq.Select("id", "agent_id", "thread_id", "message", "requires_response", "response",
		"response_at", "archived_at", "created_at", "updated_at").
		From("inbox")

	if !includeArchived {
		query = query.Where(sq.Eq{"archived_at": nil})
	}

	query = query.OrderBy("created_at DESC")

	queryStr, args, err := query.ToSql()
	if err != nil {
		return nil, fmt.Errorf("build query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, queryStr, args...)
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
	query := sq.Update("inbox").
		Set("archived_at", now).
		Set("updated_at", now).
		Where(sq.Eq{"id": inboxID})

	queryStr, args, err := query.ToSql()
	if err != nil {
		return fmt.Errorf("build query: %w", err)
	}

	_, err = s.db.ExecContext(ctx, queryStr, args...)
	return err
}

// GetOrCreateThreadID gets an existing thread ID for an agent, or creates a new one if none exists.
func (s *chatService) GetOrCreateThreadID(ctx context.Context, agentID string) (string, error) {
	// Check if there's an existing thread for this agent
	query := sq.Select("DISTINCT thread_id").
		From("conversations").
		Where(sq.Eq{"agent_id": agentID}).
		OrderBy("created_at DESC").
		Limit(1)

	queryStr, args, err := query.ToSql()
	if err != nil {
		return "", fmt.Errorf("build query: %w", err)
	}

	var existingThreadID sql.NullString
	err = s.db.QueryRowContext(ctx, queryStr, args...).Scan(&existingThreadID)

	if err == nil && existingThreadID.Valid && existingThreadID.String != "" {
		return existingThreadID.String, nil
	}

	// No existing thread found, create a new one
	threadID := fmt.Sprintf("chat-%s-%d", agentID, time.Now().Unix())
	return threadID, nil
}

// LoadConversationHistory loads conversation history for a given agent and thread ID.
// Also available as LoadThread for API consistency.
// Returns provider-neutral llm.Message types.
func (s *chatService) LoadConversationHistory(ctx context.Context, agentID, threadID string) ([]llm.Message, error) {
	return s.LoadThread(ctx, agentID, threadID)
}

// LoadAllMessagesWithTimestamps loads ALL regular (non-system) messages with their timestamps.
// This is used for display purposes to show the full conversation history.
// Returns provider-neutral llm.Message types.
func (s *chatService) LoadAllMessagesWithTimestamps(ctx context.Context, agentID, threadID string) ([]MessageWithTimestamp, error) {
	query := sq.Select("role", "content", "tool_name", "created_at").
		From("conversations").
		Where(sq.Eq{"agent_id": agentID}).
		Where(sq.Eq{"thread_id": threadID}).
		Where(sq.NotEq{"role": "system"}).
		OrderBy("created_at ASC")

	queryStr, args, err := query.ToSql()
	if err != nil {
		return nil, fmt.Errorf("build query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, queryStr, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // No remedy for rows close errors

	var messages []MessageWithTimestamp
	var currentUserTextBlocks []string
	var currentAssistantTextBlocks []string
	var currentAssistantToolBlocks []llm.ContentBlock
	var currentToolResultBlocks []llm.ContentBlock
	var lastRole string
	var currentUserTimestamp int64
	var currentAssistantTimestamp int64
	var currentToolTimestamp int64
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

		// Handle different message types (same logic as LoadThread, but we track timestamps)
		switch role {
		case roleUser:
			if lastRole == roleUser {
				currentUserTextBlocks = append(currentUserTextBlocks, content)
				// Keep the earliest timestamp for this message group
				if currentUserTimestamp == 0 || createdAt < currentUserTimestamp {
					currentUserTimestamp = createdAt
				}
			} else {
				// Role changed, commit previous messages
				s.commitPendingMessagesWithTimestamp(&messages, currentUserTextBlocks, currentAssistantTextBlocks,
					currentAssistantToolBlocks, currentToolResultBlocks, currentUserTimestamp, currentAssistantTimestamp, currentToolTimestamp)

				currentUserTextBlocks = []string{content}
				currentUserTimestamp = createdAt
				currentAssistantTextBlocks = nil
				currentAssistantToolBlocks = nil
				currentToolResultBlocks = nil
				currentAssistantTimestamp = 0
				currentToolTimestamp = 0
				seenToolUseIDs = make(map[string]bool)
				seenToolResultIDs = make(map[string]bool)
			}

		case roleAssistant:
			if toolName.Valid && toolName.String != "" {
				// Assistant message with tool call
				var toolUseData map[string]interface{}
				if err := json.Unmarshal([]byte(content), &toolUseData); err != nil {
					continue
				}

				toolID, _ := toolUseData["id"].(string)
				if toolID == "" || seenToolUseIDs[toolID] {
					continue
				}
				seenToolUseIDs[toolID] = true

				toolInput, _ := toolUseData["input"].(map[string]interface{})
				if toolInput == nil {
					toolInput = make(map[string]interface{})
				}
				toolNameStr := toolName.String

				toolUseBlock := llm.ContentBlock{
					Type: llm.ContentBlockTypeToolUse,
					ToolUse: &llm.ToolUseBlock{
						ID:    toolID,
						Name:  toolNameStr,
						Input: toolInput,
					},
				}
				currentAssistantToolBlocks = append(currentAssistantToolBlocks, toolUseBlock)
				// Keep the earliest timestamp for this message group
				if currentAssistantTimestamp == 0 || createdAt < currentAssistantTimestamp {
					currentAssistantTimestamp = createdAt
				}

				if lastRole != roleAssistant && lastRole != "" {
					s.commitPendingMessagesWithTimestamp(&messages, currentUserTextBlocks, currentAssistantTextBlocks,
						currentAssistantToolBlocks, currentToolResultBlocks, currentUserTimestamp, currentAssistantTimestamp, currentToolTimestamp)
					currentUserTextBlocks = nil
					currentAssistantTextBlocks = nil
					currentAssistantToolBlocks = nil
					currentToolResultBlocks = nil
					currentUserTimestamp = 0
					currentAssistantTimestamp = 0
					currentToolTimestamp = 0
					seenToolUseIDs = make(map[string]bool)
					seenToolResultIDs = make(map[string]bool)
				}
			} else {
				if lastRole == roleAssistant && len(currentAssistantToolBlocks) == 0 {
					currentAssistantTextBlocks = append(currentAssistantTextBlocks, content)
					// Keep the earliest timestamp for this message group
					if currentAssistantTimestamp == 0 || createdAt < currentAssistantTimestamp {
						currentAssistantTimestamp = createdAt
					}
				} else {
					s.commitPendingMessagesWithTimestamp(&messages, currentUserTextBlocks, currentAssistantTextBlocks,
						currentAssistantToolBlocks, currentToolResultBlocks, currentUserTimestamp, currentAssistantTimestamp, currentToolTimestamp)

					currentUserTextBlocks = nil
					currentAssistantTextBlocks = []string{content}
					currentAssistantTimestamp = createdAt
					currentAssistantToolBlocks = nil
					currentToolResultBlocks = nil
					currentUserTimestamp = 0
					currentToolTimestamp = 0
					seenToolUseIDs = make(map[string]bool)
					seenToolResultIDs = make(map[string]bool)
				}
			}

		case roleTool:
			if toolName.Valid && toolName.String != "" {
				var toolResultData map[string]interface{}
				if err := json.Unmarshal([]byte(content), &toolResultData); err != nil {
					continue
				}

				toolID, _ := toolResultData["id"].(string)
				if toolID == "" || seenToolResultIDs[toolID] {
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
				toolResultBlock := llm.ContentBlock{
					Type: llm.ContentBlockTypeToolResult,
					ToolResult: &llm.ToolResultBlock{
						ID:      toolID,
						Content: resultStr,
						IsError: isError,
					},
				}
				currentToolResultBlocks = append(currentToolResultBlocks, toolResultBlock)

				// Keep the earliest timestamp for this message group
				if currentToolTimestamp == 0 || createdAt < currentToolTimestamp {
					currentToolTimestamp = createdAt
				}

				// Commit if role changed
				if lastRole != roleTool && lastRole != "" {
					s.commitPendingMessagesWithTimestamp(&messages, currentUserTextBlocks, currentAssistantTextBlocks,
						currentAssistantToolBlocks, currentToolResultBlocks, currentUserTimestamp, currentAssistantTimestamp, currentToolTimestamp)
					currentUserTextBlocks = nil
					currentAssistantTextBlocks = nil
					currentAssistantToolBlocks = nil
					currentToolResultBlocks = nil
					currentUserTimestamp = 0
					currentAssistantTimestamp = 0
					currentToolTimestamp = 0
					seenToolUseIDs = make(map[string]bool)
					seenToolResultIDs = make(map[string]bool)
				}
			}
		}

		lastRole = role
	}

	// Commit any remaining messages
	s.commitPendingMessagesWithTimestamp(&messages, currentUserTextBlocks, currentAssistantTextBlocks,
		currentAssistantToolBlocks, currentToolResultBlocks, currentUserTimestamp, currentAssistantTimestamp, currentToolTimestamp)

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return messages, nil
}

// LoadMessagesWithTimestamps loads regular (non-system) messages with their timestamps.
// Only loads messages after the most recent reset or compression break (if any).
// This is used for LLM context - only messages after the break are sent to the model.
// Returns provider-neutral llm.Message types.
func (s *chatService) LoadMessagesWithTimestamps(ctx context.Context, agentID, threadID string) ([]MessageWithTimestamp, error) {
	// First, find the most recent context break (system message with type="reset" or "compress")
	var breakTimestamp sql.NullInt64
	breakQuery := sq.Select("content", "created_at").
		From("conversations").
		Where(sq.Eq{"agent_id": agentID}).
		Where(sq.Eq{"thread_id": threadID}).
		Where(sq.Eq{"role": roleSystem}).
		OrderBy("created_at DESC")

	breakQueryStr, breakArgs, err := breakQuery.ToSql()
	if err == nil {
		rows, err := s.db.QueryContext(ctx, breakQueryStr, breakArgs...)
		if err == nil {
			for rows.Next() {
				var content string
				var createdAt int64
				if err := rows.Scan(&content, &createdAt); err == nil {
					// Parse JSON to check if it's a reset or compress message
					var msgData map[string]interface{}
					if err := json.Unmarshal([]byte(content), &msgData); err == nil {
						if msgType, ok := msgData["type"].(string); ok && (msgType == "reset" || msgType == "compress") {
							breakTimestamp = sql.NullInt64{Int64: createdAt, Valid: true}
							break
						}
					}
				}
			}
			_ = rows.Close()
		}
	}

	// Build main query - only load messages after the break (if any)
	query := sq.Select("role", "content", "tool_name", "created_at").
		From("conversations").
		Where(sq.Eq{"agent_id": agentID}).
		Where(sq.Eq{"thread_id": threadID}).
		Where(sq.NotEq{"role": "system"}).
		OrderBy("created_at ASC")

	// If we found a break, only load messages after it
	if breakTimestamp.Valid {
		query = query.Where(sq.Gt{"created_at": breakTimestamp.Int64})
	}

	queryStr, args, err := query.ToSql()
	if err != nil {
		return nil, fmt.Errorf("build query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, queryStr, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // No remedy for rows close errors

	var messages []MessageWithTimestamp
	var currentUserTextBlocks []string
	var currentAssistantTextBlocks []string
	var currentAssistantToolBlocks []llm.ContentBlock
	var currentToolResultBlocks []llm.ContentBlock
	var lastRole string
	var currentUserTimestamp int64
	var currentAssistantTimestamp int64
	var currentToolTimestamp int64
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

		// Handle different message types (same logic as LoadThread, but we track timestamps)
		switch role {
		case roleUser:
			if lastRole == roleUser {
				currentUserTextBlocks = append(currentUserTextBlocks, content)
				// Keep the earliest timestamp for this message group
				if currentUserTimestamp == 0 || createdAt < currentUserTimestamp {
					currentUserTimestamp = createdAt
				}
			} else {
				// Role changed, commit previous messages
				s.commitPendingMessagesWithTimestamp(&messages, currentUserTextBlocks, currentAssistantTextBlocks,
					currentAssistantToolBlocks, currentToolResultBlocks, currentUserTimestamp, currentAssistantTimestamp, currentToolTimestamp)

				currentUserTextBlocks = []string{content}
				currentUserTimestamp = createdAt
				currentAssistantTextBlocks = nil
				currentAssistantToolBlocks = nil
				currentToolResultBlocks = nil
				currentAssistantTimestamp = 0
				currentToolTimestamp = 0
				seenToolUseIDs = make(map[string]bool)
				seenToolResultIDs = make(map[string]bool)
			}

		case roleAssistant:
			if toolName.Valid && toolName.String != "" {
				// Assistant message with tool call
				var toolUseData map[string]interface{}
				if err := json.Unmarshal([]byte(content), &toolUseData); err != nil {
					continue
				}

				toolID, _ := toolUseData["id"].(string)
				if toolID == "" || seenToolUseIDs[toolID] {
					continue
				}
				seenToolUseIDs[toolID] = true

				toolInput, _ := toolUseData["input"].(map[string]interface{})
				if toolInput == nil {
					toolInput = make(map[string]interface{})
				}
				toolNameStr := toolName.String

				toolUseBlock := llm.ContentBlock{
					Type: llm.ContentBlockTypeToolUse,
					ToolUse: &llm.ToolUseBlock{
						ID:    toolID,
						Name:  toolNameStr,
						Input: toolInput,
					},
				}
				currentAssistantToolBlocks = append(currentAssistantToolBlocks, toolUseBlock)
				// Keep the earliest timestamp for this message group
				if currentAssistantTimestamp == 0 || createdAt < currentAssistantTimestamp {
					currentAssistantTimestamp = createdAt
				}

				if lastRole != roleAssistant && lastRole != "" {
					s.commitPendingMessagesWithTimestamp(&messages, currentUserTextBlocks, currentAssistantTextBlocks,
						currentAssistantToolBlocks, currentToolResultBlocks, currentUserTimestamp, currentAssistantTimestamp, currentToolTimestamp)
					currentUserTextBlocks = nil
					currentAssistantTextBlocks = nil
					currentAssistantToolBlocks = nil
					currentToolResultBlocks = nil
					currentUserTimestamp = 0
					currentAssistantTimestamp = 0
					currentToolTimestamp = 0
					seenToolUseIDs = make(map[string]bool)
					seenToolResultIDs = make(map[string]bool)
				}
			} else {
				if lastRole == roleAssistant && len(currentAssistantToolBlocks) == 0 {
					currentAssistantTextBlocks = append(currentAssistantTextBlocks, content)
					// Keep the earliest timestamp for this message group
					if currentAssistantTimestamp == 0 || createdAt < currentAssistantTimestamp {
						currentAssistantTimestamp = createdAt
					}
				} else {
					s.commitPendingMessagesWithTimestamp(&messages, currentUserTextBlocks, currentAssistantTextBlocks,
						currentAssistantToolBlocks, currentToolResultBlocks, currentUserTimestamp, currentAssistantTimestamp, currentToolTimestamp)

					currentUserTextBlocks = nil
					currentAssistantTextBlocks = []string{content}
					currentAssistantTimestamp = createdAt
					currentAssistantToolBlocks = nil
					currentToolResultBlocks = nil
					currentUserTimestamp = 0
					currentToolTimestamp = 0
					seenToolUseIDs = make(map[string]bool)
					seenToolResultIDs = make(map[string]bool)
				}
			}

		case roleTool:
			if toolName.Valid && toolName.String != "" {
				var toolResultData map[string]interface{}
				if err := json.Unmarshal([]byte(content), &toolResultData); err != nil {
					continue
				}

				toolID, _ := toolResultData["id"].(string)
				if toolID == "" || seenToolResultIDs[toolID] {
					continue
				}
				seenToolResultIDs[toolID] = true

				resultStr, _ := toolResultData["result"].(string)
				isError, _ := toolResultData["is_error"].(bool)

				if resultStr == "" {
					if resultBytes, err := json.Marshal(toolResultData["result"]); err == nil {
						resultStr = string(resultBytes)
					}
				}

				toolResultBlock := llm.ContentBlock{
					Type: llm.ContentBlockTypeToolResult,
					ToolResult: &llm.ToolResultBlock{
						ID:      toolID,
						Content: resultStr,
						IsError: isError,
					},
				}
				currentToolResultBlocks = append(currentToolResultBlocks, toolResultBlock)
				// Keep the earliest timestamp for this message group
				if currentToolTimestamp == 0 || createdAt < currentToolTimestamp {
					currentToolTimestamp = createdAt
				}

				if lastRole != roleTool && lastRole != "" {
					s.commitPendingMessagesWithTimestamp(&messages, currentUserTextBlocks, currentAssistantTextBlocks,
						currentAssistantToolBlocks, currentToolResultBlocks, currentUserTimestamp, currentAssistantTimestamp, currentToolTimestamp)
					currentUserTextBlocks = nil
					currentAssistantTextBlocks = nil
					currentAssistantToolBlocks = nil
					currentToolResultBlocks = nil
					currentUserTimestamp = 0
					currentAssistantTimestamp = 0
					currentToolTimestamp = 0
					seenToolUseIDs = make(map[string]bool)
					seenToolResultIDs = make(map[string]bool)
				}
			}
		}

		lastRole = role
	}

	// Commit any remaining messages
	s.commitPendingMessagesWithTimestamp(&messages, currentUserTextBlocks, currentAssistantTextBlocks,
		currentAssistantToolBlocks, currentToolResultBlocks, currentUserTimestamp, currentAssistantTimestamp, currentToolTimestamp)

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return messages, nil
}

// commitPendingMessagesWithTimestamp commits pending messages with their respective timestamps.
// Uses provider-neutral llm.Message types.
func (s *chatService) commitPendingMessagesWithTimestamp(
	messages *[]MessageWithTimestamp,
	userTextBlocks []string,
	assistantTextBlocks []string,
	assistantToolBlocks []llm.ContentBlock,
	toolResultBlocks []llm.ContentBlock,
	userTimestamp int64,
	assistantTimestamp int64,
	toolTimestamp int64,
) {
	// Commit user text messages
	if len(userTextBlocks) > 0 && userTimestamp > 0 {
		*messages = append(*messages, MessageWithTimestamp{
			Message:   llm.NewTextMessage(llm.RoleUser, strings.Join(userTextBlocks, "\n")),
			Timestamp: userTimestamp,
		})
	}

	// Commit assistant messages (text or tool calls)
	if len(assistantTextBlocks) > 0 && assistantTimestamp > 0 {
		*messages = append(*messages, MessageWithTimestamp{
			Message:   llm.NewTextMessage(llm.RoleAssistant, strings.Join(assistantTextBlocks, "\n")),
			Timestamp: assistantTimestamp,
		})
	}
	if len(assistantToolBlocks) > 0 && assistantTimestamp > 0 {
		*messages = append(*messages, MessageWithTimestamp{
			Message: llm.Message{
				Role:    llm.RoleAssistant,
				Content: assistantToolBlocks,
			},
			Timestamp: assistantTimestamp,
		})
	}

	// Commit tool result messages as user messages
	if len(toolResultBlocks) > 0 && toolTimestamp > 0 {
		*messages = append(*messages, MessageWithTimestamp{
			Message: llm.Message{
				Role:    llm.RoleUser,
				Content: toolResultBlocks,
			},
			Timestamp: toolTimestamp,
		})
	}
}

// LoadThread loads conversation history for a given agent and thread ID.
// Reconstructs proper message structures from database rows.
// Only loads messages after the most recent reset or compression break (if any).
// Returns provider-neutral llm.Message types.
func (s *chatService) LoadThread(ctx context.Context, agentID, threadID string) ([]llm.Message, error) {
	// First, find the most recent context break (system message with type="reset" or "compress")
	var breakTimestamp sql.NullInt64
	breakQuery := sq.Select("content", "created_at").
		From("conversations").
		Where(sq.Eq{"agent_id": agentID}).
		Where(sq.Eq{"thread_id": threadID}).
		Where(sq.Eq{"role": roleSystem}).
		OrderBy("created_at DESC")

	breakQueryStr, breakArgs, err := breakQuery.ToSql()
	if err == nil {
		rows, err := s.db.QueryContext(ctx, breakQueryStr, breakArgs...)
		if err == nil {
			for rows.Next() {
				var content string
				var createdAt int64
				if err := rows.Scan(&content, &createdAt); err == nil {
					// Parse JSON to check if it's a reset or compress message
					var msgData map[string]interface{}
					if err := json.Unmarshal([]byte(content), &msgData); err == nil {
						if msgType, ok := msgData["type"].(string); ok && (msgType == "reset" || msgType == "compress") {
							breakTimestamp = sql.NullInt64{Int64: createdAt, Valid: true}
							break
						}
					}
				}
			}
			_ = rows.Close()
		}
	}

	// Build main query - only load messages after the break (if any)
	query := sq.Select("role", "content", "tool_name", "created_at").
		From("conversations").
		Where(sq.Eq{"agent_id": agentID}).
		Where(sq.Eq{"thread_id": threadID}).
		OrderBy("created_at ASC")

	// If we found a break, only load messages after it
	if breakTimestamp.Valid {
		query = query.Where(sq.Gt{"created_at": breakTimestamp.Int64})
	}

	queryStr, args, err := query.ToSql()
	if err != nil {
		return nil, fmt.Errorf("build query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, queryStr, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // No remedy for rows close errors

	var messages []llm.Message
	var currentUserTextBlocks []string
	var currentAssistantTextBlocks []string
	var currentAssistantToolBlocks []llm.ContentBlock
	var currentToolResultBlocks []llm.ContentBlock
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
		case roleUser:
			// User text message
			if lastRole == roleUser {
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

				toolInput, _ := toolUseData["input"].(map[string]interface{})
				if toolInput == nil {
					toolInput = make(map[string]interface{})
				}
				toolNameStr := toolName.String

				// Create tool use block
				toolUseBlock := llm.ContentBlock{
					Type: llm.ContentBlockTypeToolUse,
					ToolUse: &llm.ToolUseBlock{
						ID:    toolID,
						Name:  toolNameStr,
						Input: toolInput,
					},
				}
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

		case roleSystem:
			// System messages (context breaks) are not sent to LLM API
			// They are stored for UI display purposes only
			// Skip them in the message list for API calls
			continue

		case roleTool:
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
				toolResultBlock := llm.ContentBlock{
					Type: llm.ContentBlockTypeToolResult,
					ToolResult: &llm.ToolResultBlock{
						ID:      toolID,
						Content: resultStr,
						IsError: isError,
					},
				}
				currentToolResultBlocks = append(currentToolResultBlocks, toolResultBlock)

				// Commit if role changed
				if lastRole != roleTool && lastRole != "" {
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
// Uses provider-neutral llm.Message types.
func (s *chatService) commitPendingMessages(
	messages *[]llm.Message,
	userTextBlocks []string,
	assistantTextBlocks []string,
	assistantToolBlocks []llm.ContentBlock,
	toolResultBlocks []llm.ContentBlock,
) {
	// Commit user text messages
	if len(userTextBlocks) > 0 {
		*messages = append(*messages, llm.NewTextMessage(llm.RoleUser, strings.Join(userTextBlocks, "\n")))
	}

	// Commit assistant messages (text or tool calls)
	if len(assistantTextBlocks) > 0 {
		*messages = append(*messages, llm.NewTextMessage(llm.RoleAssistant, strings.Join(assistantTextBlocks, "\n")))
	}
	if len(assistantToolBlocks) > 0 {
		*messages = append(*messages, llm.Message{
			Role:    llm.RoleAssistant,
			Content: assistantToolBlocks,
		})
	}

	// Commit tool result messages as user messages
	if len(toolResultBlocks) > 0 {
		*messages = append(*messages, llm.Message{
			Role:    llm.RoleUser,
			Content: toolResultBlocks,
		})
	}
}

// SaveMessage saves a user or assistant message to the conversation history.
func (s *chatService) SaveMessage(ctx context.Context, agentID, threadID, role, content string) error {
	now := time.Now().Unix()
	query := sq.Insert("conversations").
		Columns("agent_id", "thread_id", "role", "content", "tool_name", "created_at").
		Values(agentID, threadID, role, content, nil, now)

	queryStr, args, err := query.ToSql()
	if err != nil {
		return fmt.Errorf("build query: %w", err)
	}

	_, err = s.db.ExecContext(ctx, queryStr, args...)
	return err
}

// AppendUserMessage saves a user text message to the conversation history.
func (s *chatService) AppendUserMessage(ctx context.Context, agentID, threadID, content string) error {
	now := time.Now().Unix()
	query := sq.Insert("conversations").
		Columns("agent_id", "thread_id", "role", "content", "tool_name", "created_at").
		Values(agentID, threadID, "user", content, nil, now)

	queryStr, args, err := query.ToSql()
	if err != nil {
		return fmt.Errorf("build query: %w", err)
	}

	_, err = s.db.ExecContext(ctx, queryStr, args...)
	return err
}

// AppendAssistantMessage saves an assistant text-only message to the conversation history.
func (s *chatService) AppendAssistantMessage(ctx context.Context, agentID, threadID, content string) error {
	now := time.Now().Unix()
	query := sq.Insert("conversations").
		Columns("agent_id", "thread_id", "role", "content", "tool_name", "created_at").
		Values(agentID, threadID, "assistant", content, nil, now)

	queryStr, args, err := query.ToSql()
	if err != nil {
		return fmt.Errorf("build query: %w", err)
	}

	_, err = s.db.ExecContext(ctx, queryStr, args...)
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
	query := sq.Insert("conversations").
		Columns("agent_id", "thread_id", "role", "content", "tool_name", "tool_id", "created_at").
		Values(agentID, threadID, "assistant", string(contentJSON), toolName, toolID, now)

	queryStr, args, err := query.ToSql()
	if err != nil {
		return fmt.Errorf("build query: %w", err)
	}

	// SQLite requires "OR IGNORE" to come after "INSERT", so we replace "INSERT INTO" with "INSERT OR IGNORE INTO"
	queryStr = strings.Replace(queryStr, "INSERT INTO", "INSERT OR IGNORE INTO", 1)

	_, err = s.db.ExecContext(ctx, queryStr, args...)
	return err
}

// GetChatTimeout returns the timeout duration for chat operations.
func (s *chatService) GetChatTimeout() time.Duration {
	return s.timeout
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
	query := sq.Insert("conversations").
		Columns("agent_id", "thread_id", "role", "content", "tool_name", "tool_id", "created_at").
		Values(agentID, threadID, "tool", string(contentJSON), toolName, toolID, now)

	queryStr, args, err := query.ToSql()
	if err != nil {
		return fmt.Errorf("build query: %w", err)
	}

	// SQLite requires "OR IGNORE" to come after "INSERT", so we replace "INSERT INTO" with "INSERT OR IGNORE INTO"
	queryStr = strings.Replace(queryStr, "INSERT INTO", "INSERT OR IGNORE INTO", 1)

	_, err = s.db.ExecContext(ctx, queryStr, args...)
	return err
}

// AppendSystemMessage saves a system message to the conversation history.
// breakType should be "reset" or "compress".
// content should be a JSON string containing the system message data.
func (s *chatService) AppendSystemMessage(ctx context.Context, agentID, threadID, content, breakType string) error {
	now := time.Now().Unix()
	query := sq.Insert("conversations").
		Columns("agent_id", "thread_id", "role", "content", "tool_name", "created_at").
		Values(agentID, threadID, roleSystem, content, nil, now)

	queryStr, args, err := query.ToSql()
	if err != nil {
		return fmt.Errorf("build query: %w", err)
	}

	_, err = s.db.ExecContext(ctx, queryStr, args...)
	return err
}

// ResetContext clears the context by inserting a system message marking the reset.
func (s *chatService) ResetContext(ctx context.Context, agentID, threadID string) error {
	// Create system message content
	systemMsg := map[string]interface{}{
		"type":      "reset",
		"message":   "Context was reset",
		"timestamp": time.Now().Unix(),
	}

	contentJSON, err := json.Marshal(systemMsg)
	if err != nil {
		return fmt.Errorf("failed to marshal system message: %w", err)
	}

	return s.AppendSystemMessage(ctx, agentID, threadID, string(contentJSON), "reset")
}

// LoadSystemMessages loads system messages (context breaks) for a given agent and thread ID.
// Returns a slice of system message data with type, message, timestamp, and size information.
func (s *chatService) LoadSystemMessages(ctx context.Context, agentID, threadID string) ([]map[string]interface{}, error) {
	query := sq.Select("role", "content", "created_at").
		From("conversations").
		Where(sq.Eq{"agent_id": agentID}).
		Where(sq.Eq{"thread_id": threadID}).
		Where(sq.Eq{"role": roleSystem}).
		OrderBy("created_at ASC")

	queryStr, args, err := query.ToSql()
	if err != nil {
		return nil, fmt.Errorf("build query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, queryStr, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // No remedy for rows close errors

	var systemMessages []map[string]interface{}
	for rows.Next() {
		var role string
		var content string
		var createdAt int64

		if err := rows.Scan(&role, &content, &createdAt); err != nil {
			return nil, err
		}

		// Parse JSON content
		var msgData map[string]interface{}
		if err := json.Unmarshal([]byte(content), &msgData); err != nil {
			// If JSON parsing fails, create a basic message
			msgData = map[string]interface{}{
				"type":      "unknown",
				"message":   content,
				"timestamp": createdAt,
			}
		} else {
			msgData["timestamp"] = createdAt
		}

		systemMessages = append(systemMessages, msgData)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return systemMessages, nil
}

// CompressContext summarizes the context and inserts a system message marking the compression.
func (s *chatService) CompressContext(ctx context.Context, agentID, threadID string) error {
	// Load current conversation history
	history, err := s.LoadThread(ctx, agentID, threadID)
	if err != nil {
		return fmt.Errorf("failed to load conversation history: %w", err)
	}

	// Get the agent to access system prompt
	agents := s.crew.ListAgents()
	var agentConfig *config.AgentConfig
	for _, ag := range agents {
		if ag.ID == agentID {
			agentConfig = ag.Config
			break
		}
	}
	if agentConfig == nil {
		return fmt.Errorf("agent %s not found", agentID)
	}

	// Get the runner to access summarizer
	runner := s.crew.GetRunner(agentID)
	if runner == nil {
		return fmt.Errorf("runner for agent %s not found", agentID)
	}

	// Get the summarizer
	summarizer := runner.GetMessageSummarizer()
	if summarizer == nil {
		return fmt.Errorf("summarizer not available for agent %s", agentID)
	}

	// Use ContextManager to compress context
	cm := agent.NewContextManager(s)
	_, err = cm.CompressContext(ctx, agentID, threadID, agentConfig.System, history, summarizer)
	return err
}

// GetSystemInfo returns information about the system configuration.
func (s *chatService) GetSystemInfo(ctx context.Context) (*SystemInfo, error) {
	info := &SystemInfo{
		MCPServers: make([]MCPServerInfo, 0),
		Tools:      make([]ToolInfo, 0),
	}

	// Get LLM provider
	if s.config != nil {
		info.LLMProvider = strings.Join(s.config.LLMProviders, ", ")
	} else {
		info.LLMProvider = "anthropic" // Default
	}

	// Get MCP servers
	mcpServers := s.crew.GetMCPServers()
	mcpClients := s.crew.GetMCPClients()
	for name, serverCfg := range mcpServers {
		serverInfo := MCPServerInfo{
			Name:    name,
			Enabled: serverCfg != nil,
			Tools:   make([]string, 0),
		}

		// Get tools from MCP client if available
		if client, ok := mcpClients[name]; ok && client != nil {
			mcpTools, err := client.ListTools(ctx)
			if err == nil {
				for _, tool := range mcpTools {
					serverInfo.Tools = append(serverInfo.Tools, tool.Name)
				}
			}
		}

		info.MCPServers = append(info.MCPServers, serverInfo)
	}

	// Get native tools from tool provider schemas
	toolProvider := s.crew.GetToolProvider()
	if toolProvider != nil {
		schemas := toolProvider.GetAllSchemas()
		for toolName, schema := range schemas {
			info.Tools = append(info.Tools, ToolInfo{
				Name:        toolName,
				Description: schema.Description,
				Server:      schema.ServerName,
			})
		}
	}

	// MCP tools are already included in the schemas above, but let's also
	// add any tools from MCP clients that might not be in schemas yet
	for _, serverInfo := range info.MCPServers {
		for _, toolName := range serverInfo.Tools {
			// Check if we already have this tool
			found := false
			for _, existingTool := range info.Tools {
				if existingTool.Name == toolName && existingTool.Server == serverInfo.Name {
					found = true
					break
				}
			}
			if !found {
				info.Tools = append(info.Tools, ToolInfo{
					Name:        toolName,
					Description: "", // Description not available from MCP client directly
					Server:      serverInfo.Name,
				})
			}
		}
	}

	return info, nil
}

// DumpMemory writes all memory items to a file as JSON
func (s *chatService) DumpMemory(ctx context.Context, filePath string) error {
	query := sq.Select("id", "agent_id", "thread_id", "scope", "type", "content",
		"metadata", "created_at", "updated_at", "importance", "raw_content", "memory_type", "tags_json").
		From("memory_items").
		OrderBy("created_at ASC")

	queryStr, args, err := query.ToSql()
	if err != nil {
		return fmt.Errorf("build query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, queryStr, args...)
	if err != nil {
		return err
	}
	defer rows.Close() //nolint:errcheck // No remedy for rows close errors

	var items []map[string]interface{}
	for rows.Next() {
		var id int64
		var agentID, threadID sql.NullString
		var scope, typ, content string
		var metadata sql.NullString
		var createdAt, updatedAt int64
		var importance float64
		var rawContent, memoryType, tagsJSON sql.NullString

		if err := rows.Scan(&id, &agentID, &threadID, &scope, &typ, &content,
			&metadata, &createdAt, &updatedAt, &importance, &rawContent, &memoryType, &tagsJSON); err != nil {
			return err
		}

		item := map[string]interface{}{
			"id":         id,
			"scope":      scope,
			"type":       typ,
			"content":    content,
			"created_at": createdAt,
			"updated_at": updatedAt,
			"importance": importance,
		}

		if agentID.Valid {
			item["agent_id"] = agentID.String
		}
		if threadID.Valid {
			item["thread_id"] = threadID.String
		}
		if metadata.Valid {
			var meta map[string]interface{}
			if err := json.Unmarshal([]byte(metadata.String), &meta); err == nil {
				item["metadata"] = meta
			} else {
				item["metadata"] = metadata.String
			}
		}
		if rawContent.Valid {
			item["raw_content"] = rawContent.String
		}
		if memoryType.Valid {
			item["memory_type"] = memoryType.String
		}
		if tagsJSON.Valid {
			var tags []string
			if err := json.Unmarshal([]byte(tagsJSON.String), &tags); err == nil {
				item["tags"] = tags
			} else {
				item["tags"] = tagsJSON.String
			}
		}

		items = append(items, item)
	}

	if err := rows.Err(); err != nil {
		return err
	}

	// Write to file as JSON
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal memory items: %w", err)
	}

	return os.WriteFile(filePath, data, 0o600)
}

// ClearMemory deletes all memory items from the database
func (s *chatService) ClearMemory(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // No remedy for rollback errors

	// Delete from FTS table first (if it exists)
	_, err = tx.ExecContext(ctx, "DELETE FROM memory_items_fts")
	if err != nil {
		// FTS table might not exist, continue
		s.logger.Warn().Err(err).Msg("FTS table might not exist, continuing")
	}

	// Delete from main table
	_, err = tx.ExecContext(ctx, "DELETE FROM memory_items")
	if err != nil {
		return fmt.Errorf("delete memory items: %w", err)
	}

	return tx.Commit()
}

// DumpConversations writes conversations grouped by agent to files (one file per agent)
func (s *chatService) DumpConversations(ctx context.Context, outputDir string) error {
	// Get all unique agent IDs
	query := sq.Select("DISTINCT agent_id").From("conversations")
	queryStr, args, err := query.ToSql()
	if err != nil {
		return fmt.Errorf("build query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, queryStr, args...)
	if err != nil {
		return err
	}
	defer rows.Close() //nolint:errcheck // No remedy for rows close errors

	var agentIDs []string
	for rows.Next() {
		var agentID string
		if err := rows.Scan(&agentID); err != nil {
			return err
		}
		agentIDs = append(agentIDs, agentID)
	}

	if err := rows.Err(); err != nil {
		return err
	}

	// Create output directory if it doesn't exist
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	// For each agent, dump their conversations
	for _, agentID := range agentIDs {
		convQuery := sq.Select("id", "agent_id", "thread_id", "role", "content", "tool_name", "tool_id", "created_at").
			From("conversations").
			Where(sq.Eq{"agent_id": agentID}).
			OrderBy("created_at ASC")

		convQueryStr, convArgs, err := convQuery.ToSql()
		if err != nil {
			return fmt.Errorf("build query for agent %s: %w", agentID, err)
		}

		convRows, err := s.db.QueryContext(ctx, convQueryStr, convArgs...)
		if err != nil {
			return fmt.Errorf("query conversations for agent %s: %w", agentID, err)
		}

		var conversations []map[string]interface{}
		for convRows.Next() {
			var id int64
			var agentID, threadID, role, content string
			var toolName, toolID sql.NullString
			var createdAt int64

			if err := convRows.Scan(&id, &agentID, &threadID, &role, &content, &toolName, &toolID, &createdAt); err != nil {
				_ = convRows.Close() //nolint:errcheck // No remedy for rows close errors
				return err
			}

			conv := map[string]interface{}{
				"id":         id,
				"agent_id":   agentID,
				"thread_id":  threadID,
				"role":       role,
				"content":    content,
				"created_at": createdAt,
			}

			if toolName.Valid {
				conv["tool_name"] = toolName.String
			}
			if toolID.Valid {
				conv["tool_id"] = toolID.String
			}

			conversations = append(conversations, conv)
		}
		_ = convRows.Close() //nolint:errcheck // No remedy for rows close errors

		if err := convRows.Err(); err != nil {
			return fmt.Errorf("scan conversations for agent %s: %w", agentID, err)
		}

		// Write to file
		// Sanitize agentID for filename
		safeAgentID := strings.ReplaceAll(agentID, "/", "_")
		safeAgentID = strings.ReplaceAll(safeAgentID, "\\", "_")
		filePath := filepath.Join(outputDir, fmt.Sprintf("conversations_%s.json", safeAgentID))

		data, err := json.MarshalIndent(conversations, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal conversations for agent %s: %w", agentID, err)
		}

		if err := os.WriteFile(filePath, data, 0o600); err != nil {
			return fmt.Errorf("write conversations for agent %s: %w", agentID, err)
		}
	}

	return nil
}

// ClearConversations deletes all conversations from the database
func (s *chatService) ClearConversations(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM conversations")
	return err
}

// ResetStats resets all agent stats
func (s *chatService) ResetStats(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE agent_stats 
		SET execution_count = 0, 
		    failure_count = 0, 
		    wakeup_count = 0, 
		    last_execution = NULL, 
		    last_failure = NULL, 
		    last_failure_message = NULL
	`)
	return err
}

// DumpInbox writes all inbox items to a file as JSON
func (s *chatService) DumpInbox(ctx context.Context, filePath string) error {
	query := sq.Select("id", "agent_id", "thread_id", "message", "requires_response",
		"response", "response_at", "archived_at", "created_at", "updated_at").
		From("inbox").
		OrderBy("created_at ASC")

	queryStr, args, err := query.ToSql()
	if err != nil {
		return fmt.Errorf("build query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, queryStr, args...)
	if err != nil {
		return err
	}
	defer rows.Close() //nolint:errcheck // No remedy for rows close errors

	var items []map[string]interface{}
	for rows.Next() {
		var id int64
		var agentID, threadID sql.NullString
		var message string
		var requiresResponse bool
		var response sql.NullString
		var responseAt, archivedAt, createdAt, updatedAt sql.NullInt64

		if err := rows.Scan(&id, &agentID, &threadID, &message, &requiresResponse,
			&response, &responseAt, &archivedAt, &createdAt, &updatedAt); err != nil {
			return err
		}

		item := map[string]interface{}{
			"id":                id,
			"message":           message,
			"requires_response": requiresResponse,
		}

		if agentID.Valid {
			item["agent_id"] = agentID.String
		}
		if threadID.Valid {
			item["thread_id"] = threadID.String
		}
		if response.Valid {
			item["response"] = response.String
		}
		if responseAt.Valid {
			item["response_at"] = responseAt.Int64
		}
		if archivedAt.Valid {
			item["archived_at"] = archivedAt.Int64
		}
		if createdAt.Valid {
			item["created_at"] = createdAt.Int64
		}
		if updatedAt.Valid {
			item["updated_at"] = updatedAt.Int64
		}

		items = append(items, item)
	}

	if err := rows.Err(); err != nil {
		return err
	}

	// Write to file as JSON
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal inbox items: %w", err)
	}

	return os.WriteFile(filePath, data, 0o600)
}

// ClearInbox deletes all inbox items from the database
func (s *chatService) ClearInbox(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM inbox")
	return err
}
