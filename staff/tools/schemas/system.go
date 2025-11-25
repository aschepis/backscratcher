package schemas

// SystemSchemas returns schemas for system-related tools.
func SystemSchemas() map[string]ToolSchema {
	return map[string]ToolSchema{
		"execute_command": {
			Description: "Execute a shell command in the workspace directory. WARNING: This tool blocks dangerous commands that could damage the system, delete files, format disks, or execute arbitrary code from the internet. Please use safe commands only and avoid any operations that could modify or delete files, format storage devices, or download and execute code. Commands that attempt file deletion, disk formatting, or piping from remote sources will be automatically blocked for safety.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{
						"type":        "string",
						"description": "Command to execute (e.g., 'ls', 'grep', 'git')",
					},
					"args": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Command arguments",
					},
					"timeout": map[string]any{
						"type":        "number",
						"description": "Timeout in seconds (default: 30, max: 300)",
					},
					"working_dir": map[string]any{
						"type":        "string",
						"description": "Working directory relative to workspace (default: workspace root)",
					},
					"stdin": map[string]any{
						"type":        "string",
						"description": "Standard input to pipe to the command",
					},
				},
				"required": []string{"command"},
			},
		},
	}
}

