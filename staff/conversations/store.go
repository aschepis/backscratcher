package conversations

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"
)

const roleSystem = "system"

// Store handles persistence of conversation messages.
// It implements agent.MessagePersister.
type Store struct {
	db *sql.DB
}

// NewStore creates a new ConversationStore.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// AppendUserMessage saves a user text message to the conversation history.
func (s *Store) AppendUserMessage(ctx context.Context, agentID, threadID, content string) error {
	// TODO: There is potential to DRY up this code. AppendUserMessage, AppendAssistantMessage,
	//       AppendSystemMessage all have the same pattern.
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
func (s *Store) AppendAssistantMessage(ctx context.Context, agentID, threadID, content string) error {
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
func (s *Store) AppendToolCall(ctx context.Context, agentID, threadID, toolID, toolName string, toolInput any) error {
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

// AppendToolResult saves a tool result message to the conversation history.
// toolID is the unique ID for the tool call that produced this result.
// toolName is the name of the tool that produced the result.
// result is the tool result (will be JSON-marshaled).
// isError indicates if the result represents an error.
// Uses INSERT OR IGNORE to prevent duplicate tool results in case of crashes/restarts.
func (s *Store) AppendToolResult(ctx context.Context, agentID, threadID, toolID, toolName string, result any, isError bool) error {
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
func (s *Store) AppendSystemMessage(ctx context.Context, agentID, threadID, content, breakType string) error {
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
