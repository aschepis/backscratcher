package schemas

// NotificationSchemas returns schemas for notification-related tools.
func NotificationSchemas() map[string]ToolSchema {
	return map[string]ToolSchema{
		"send_user_notification": {
			Description: "Send a notification to the user. Inserts the notification into the inbox table and attempts to display a desktop notification. Use this when you need to alert the user about something important, request their attention, or notify them of completed tasks.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"message": map[string]any{
						"type":        "string",
						"description": "The notification message to send to the user",
					},
					"title": map[string]any{
						"type":        "string",
						"description": "Optional title for the notification (default: 'Staff Notification')",
					},
					"thread_id": map[string]any{
						"type":        "string",
						"description": "Optional thread ID to associate the notification with a conversation",
					},
					"requires_response": map[string]any{
						"type":        "boolean",
						"description": "Whether this notification requires a response from the user",
					},
				},
				"required": []string{"message"},
			},
		},
	}
}

