package schemas

// FilesystemSchemas returns schemas for filesystem-related tools.
func FilesystemSchemas() map[string]ToolSchema {
	return map[string]ToolSchema{
		"read_file": {
			Description: "Read the contents of a file. Returns the file content, size, and path.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "Path to the file to read (relative to workspace)",
					},
					"encoding": map[string]any{
						"type":        "string",
						"description": "File encoding (default: utf-8)",
					},
					"max_bytes": map[string]any{
						"type":        "number",
						"description": "Maximum number of bytes to read (0 = read entire file)",
					},
				},
				"required": []string{"path"},
			},
		},
		"write_file": {
			Description: "Write content to a file. Creates the file if it doesn't exist, overwrites if it does.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "Path to the file to write (relative to workspace)",
					},
					"content": map[string]any{
						"type":        "string",
						"description": "Content to write to the file",
					},
					"create_dirs": map[string]any{
						"type":        "boolean",
						"description": "Create parent directories if they don't exist",
					},
				},
				"required": []string{"path", "content"},
			},
		},
		"list_directory": {
			Description: "List files and directories in a path. Can list recursively and optionally include hidden files.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "Path to the directory to list (relative to workspace, default: '.')",
					},
					"recursive": map[string]any{
						"type":        "boolean",
						"description": "Whether to list recursively",
					},
					"include_hidden": map[string]any{
						"type":        "boolean",
						"description": "Whether to include hidden files (starting with '.')",
					},
				},
				"required": []string{},
			},
		},
		"file_search": {
			Description: "Search for files using glob patterns (e.g., '*.go', '**/*.test.go').",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern": map[string]any{
						"type":        "string",
						"description": "Glob pattern to match files (e.g., '*.go', '**/*.test.go')",
					},
					"root": map[string]any{
						"type":        "string",
						"description": "Root directory to search from (relative to workspace, default: '.')",
					},
					"limit": map[string]any{
						"type":        "number",
						"description": "Maximum number of matches to return (default: 100)",
					},
				},
				"required": []string{"pattern"},
			},
		},
		"file_info": {
			Description: "Get metadata about a file or directory (size, mode, modification time, etc.).",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "Path to the file or directory (relative to workspace)",
					},
				},
				"required": []string{"path"},
			},
		},
		"create_directory": {
			Description: "Create a directory. Can create parent directories if needed.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "Path to the directory to create (relative to workspace)",
					},
					"parents": map[string]any{
						"type":        "boolean",
						"description": "Whether to create parent directories if they don't exist",
					},
				},
				"required": []string{"path"},
			},
		},
		"grep_search": {
			Description: "Search file contents using regex patterns. Returns matching lines with line numbers and optional context.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern": map[string]any{
						"type":        "string",
						"description": "Regex pattern to search for",
					},
					"path": map[string]any{
						"type":        "string",
						"description": "Path to file or directory to search in (relative to workspace)",
					},
					"case_sensitive": map[string]any{
						"type":        "boolean",
						"description": "Whether the search should be case-sensitive (default: false)",
					},
					"context_lines": map[string]any{
						"type":        "number",
						"description": "Number of context lines to include around each match (0-5, default: 0)",
					},
				},
				"required": []string{"pattern", "path"},
			},
		},
	}
}
