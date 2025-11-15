package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aschepis/backscratcher/staff/logger"
	"github.com/gen2brain/beeep"
)

// SetStateFunc is a function that sets an agent's state.
// This avoids import cycles by not importing the agent package.
type SetStateFunc func(agentID string, state string) error

// RegisterNotificationTools registers notification-related tools
func (r *Registry) RegisterNotificationTools(db *sql.DB, setState SetStateFunc) {
	logger.Info("Registering notification tools in registry")

	r.Register("send_user_notification", func(ctx context.Context, agentID string, args json.RawMessage) (any, error) {
		var payload struct {
			Message          string `json:"message"`
			Title            string `json:"title"`
			ThreadID         string `json:"thread_id"`
			RequiresResponse bool   `json:"requires_response"`
		}
		if err := json.Unmarshal(args, &payload); err != nil {
			return nil, fmt.Errorf("failed to unmarshal arguments: %w", err)
		}

		if payload.Message == "" {
			return nil, fmt.Errorf("message cannot be empty")
		}

		now := time.Now().Unix()

		// Insert into inbox table
		result, err := db.ExecContext(ctx,
			`INSERT INTO inbox (agent_id, thread_id, message, requires_response, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			agentID,
			payload.ThreadID,
			payload.Message,
			payload.RequiresResponse,
			now,
			now,
		)
		if err != nil {
			logger.Error("Failed to insert notification into inbox: %v", err)
			return nil, fmt.Errorf("failed to insert notification into inbox: %w", err)
		}

		inboxID, err := result.LastInsertId()
		if err != nil {
			logger.Warn("Failed to get last insert ID for inbox: %v", err)
		}

		logger.Info("Inserted notification into inbox: id=%d agentID=%s message=%.100q", inboxID, agentID, payload.Message)

		// Attempt to send desktop notification using beeep (uses modern UserNotifications framework)
		notificationTitle := payload.Title
		if notificationTitle == "" {
			notificationTitle = "Staff Notification"
		}

		// Build notification message
		notificationMessage := payload.Message
		if payload.RequiresResponse {
			notificationMessage = notificationMessage + " (Response required)"
		}

		// Send desktop notification using beeep
		// beeep uses the modern UserNotifications framework on macOS
		notifErr := beeep.Notify(notificationTitle, notificationMessage, "")

		if notifErr != nil {
			// Log error but don't fail the tool - the inbox insert succeeded
			// Common causes: notification permissions not granted, or notification center disabled
			logger.Warn("Failed to send desktop notification (notification still saved to inbox): %v", notifErr)
			logger.Info("Note: If notifications aren't appearing, check macOS System Settings > Notifications > Staff")
		} else {
			logger.Info("Desktop notification sent successfully")
		}

		// If notification requires response, set agent state to waiting_human
		if payload.RequiresResponse && setState != nil {
			if err := setState(agentID, "waiting_human"); err != nil {
				logger.Warn("Failed to set agent state to waiting_human: %v", err)
				// Don't fail the tool - notification was successfully sent
			} else {
				logger.Info("Agent state set to waiting_human for agent %s", agentID)
			}
		}

		return map[string]any{
			"id":                inboxID,
			"message":           payload.Message,
			"title":             notificationTitle,
			"thread_id":         payload.ThreadID,
			"requires_response": payload.RequiresResponse,
			"created_at":        now,
			"notification_sent": notifErr == nil,
		}, nil
	})
}
