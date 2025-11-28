package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/gen2brain/beeep"
)

// SetStateFunc is a function that sets an agent's state.
// This avoids import cycles by not importing the agent package.
type SetStateFunc func(agentID string, state string) error

// RegisterNotificationTools registers notification-related tools
func (r *Registry) RegisterNotificationTools(db *sql.DB, setState SetStateFunc) {
	r.logger.Info().Msg("Registering notification tools in registry")

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
		query := sq.Insert("inbox").
			Columns("agent_id", "thread_id", "message", "requires_response", "created_at", "updated_at").
			Values(agentID, payload.ThreadID, payload.Message, payload.RequiresResponse, now, now)

		queryStr, queryArgs, err := query.ToSql()
		if err != nil {
			r.logger.Error().Err(err).Msg("Failed to build insert query")
			return nil, fmt.Errorf("build insert query: %w", err)
		}

		result, err := db.ExecContext(ctx, queryStr, queryArgs...)
		if err != nil {
			r.logger.Error().Err(err).Msg("Failed to insert notification into inbox")
			return nil, fmt.Errorf("failed to insert notification into inbox: %w", err)
		}

		inboxID, err := result.LastInsertId()
		if err != nil {
			r.logger.Warn().Err(err).Msg("Failed to get last insert ID for inbox")
		}

		r.logger.Info().Int64("id", inboxID).Str("agentID", agentID).Str("message", payload.Message).Msg("Inserted notification into inbox")

		// Attempt to send desktop notification using beeep (uses modern UserNotifications framework)
		notificationTitle := payload.Title
		if notificationTitle == "" {
			notificationTitle = "Staff Notification"
		}

		// Build notification message
		notificationMessage := payload.Message
		if payload.RequiresResponse {
			notificationMessage += " (Response required)"
		}

		// Send desktop notification using beeep
		// beeep uses the modern UserNotifications framework on macOS
		notifErr := beeep.Notify(notificationTitle, notificationMessage, "")

		if notifErr != nil {
			// Log error but don't fail the tool - the inbox insert succeeded
			// Common causes: notification permissions not granted, or notification center disabled
			r.logger.Warn().Err(notifErr).Msg("Failed to send desktop notification (notification still saved to inbox)")
			r.logger.Info().Msg("Note: If notifications aren't appearing, check macOS System Settings > Notifications > Staff")
		} else {
			r.logger.Info().Msg("Desktop notification sent successfully")
		}

		// If notification requires response, set agent state to waiting_human
		if payload.RequiresResponse && setState != nil {
			if err := setState(agentID, "waiting_human"); err != nil {
				r.logger.Warn().Err(err).Msg("Failed to set agent state to waiting_human")
				// Don't fail the tool - notification was successfully sent
			} else {
				r.logger.Info().Str("agentID", agentID).Msg("Agent state set to waiting_human")
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
