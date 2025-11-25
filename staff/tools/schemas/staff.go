package schemas

// StaffSchemas returns schemas for staff introspection tools.
func StaffSchemas() map[string]ToolSchema {
	return map[string]ToolSchema{
		"list_agents": {
			Description: "List all configured agents with their configuration details.",
			Schema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
				"required":   []string{},
			},
		},
		"get_agent_state": {
			Description: "Get the current state and next_wake time for one or all agents.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent_id": map[string]any{
						"type":        "string",
						"description": "Optional agent ID. If omitted, returns states for all agents.",
					},
				},
				"required": []string{},
			},
		},
		"get_agent_stats": {
			Description: "Get execution statistics (execution_count, failure_count, wakeup_count, last_execution, last_failure) for one or all agents.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent_id": map[string]any{
						"type":        "string",
						"description": "Optional agent ID. If omitted, returns stats for all agents.",
					},
				},
				"required": []string{},
			},
		},
		"list_tools": {
			Description: "List all registered tools with their descriptions.",
			Schema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
				"required":   []string{},
			},
		},
		"list_mcp_servers": {
			Description: "List all configured MCP servers with their configuration details.",
			Schema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
				"required":   []string{},
			},
		},
		"mcp_tools_discover": {
			Description: "Discover tools available from MCP servers. Returns tool definitions including name, description, and input schema.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"server_name": map[string]any{
						"type":        "string",
						"description": "Optional MCP server name. If omitted, discovers tools from all configured MCP servers.",
					},
				},
				"required": []string{},
			},
		},
	}
}

