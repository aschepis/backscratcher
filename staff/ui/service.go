package ui

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"

	"github.com/aschepis/backscratcher/staff/agent"
)

// chatService implements ChatService by wrapping an agent.Crew
type chatService struct {
	crew *agent.Crew
	db   *sql.DB
}

// NewChatService creates a new ChatService that wraps the given crew and database.
func NewChatService(crew *agent.Crew, db *sql.DB) ChatService {
	return &chatService{
		crew: crew,
		db:   db,
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

// ListInboxItems returns a list of inbox items, optionally filtered by archived status.
func (s *chatService) ListInboxItems(ctx context.Context, includeArchived bool) ([]InboxItem, error) {
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
	defer rows.Close()

	var items []InboxItem
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

		items = append(items, item)
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
func (s *chatService) LoadConversationHistory(ctx context.Context, agentID, threadID string) ([]anthropic.MessageParam, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT role, content, created_at FROM conversations 
		 WHERE agent_id = ? AND thread_id = ? 
		 ORDER BY created_at ASC`,
		agentID, threadID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []anthropic.MessageParam
	var currentUserMessages []string
	var currentAssistantMessages []string
	var lastRole string

	for rows.Next() {
		var role string
		var content string
		var createdAt int64

		if err := rows.Scan(&role, &content, &createdAt); err != nil {
			return nil, err
		}

		// Group consecutive messages from the same role
		if lastRole == "" || lastRole == role {
			if role == "user" {
				currentUserMessages = append(currentUserMessages, content)
			} else {
				currentAssistantMessages = append(currentAssistantMessages, content)
			}
		} else {
			// Role changed, commit the previous messages
			if len(currentUserMessages) > 0 {
				messages = append(messages, anthropic.NewUserMessage(
					anthropic.NewTextBlock(strings.Join(currentUserMessages, "\n")),
				))
				currentUserMessages = nil
			}
			if len(currentAssistantMessages) > 0 {
				messages = append(messages, anthropic.NewAssistantMessage(
					anthropic.NewTextBlock(strings.Join(currentAssistantMessages, "\n")),
				))
				currentAssistantMessages = nil
			}

			// Start new message group
			if role == "user" {
				currentUserMessages = append(currentUserMessages, content)
			} else {
				currentAssistantMessages = append(currentAssistantMessages, content)
			}
		}

		lastRole = role
	}

	// Commit any remaining messages
	if len(currentUserMessages) > 0 {
		messages = append(messages, anthropic.NewUserMessage(
			anthropic.NewTextBlock(strings.Join(currentUserMessages, "\n")),
		))
	}
	if len(currentAssistantMessages) > 0 {
		messages = append(messages, anthropic.NewAssistantMessage(
			anthropic.NewTextBlock(strings.Join(currentAssistantMessages, "\n")),
		))
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return messages, nil
}

// SaveMessage saves a user or assistant message to the conversation history.
func (s *chatService) SaveMessage(ctx context.Context, agentID, threadID, role, content string) error {
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO conversations (agent_id, thread_id, role, content, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		agentID, threadID, role, content, now,
	)
	return err
}
