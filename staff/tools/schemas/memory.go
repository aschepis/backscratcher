package schemas

// MemorySchemas returns schemas for memory-related tools.
func MemorySchemas() map[string]ToolSchema {
	return map[string]ToolSchema{
		"memory_search": {
			Description: "Search the agent or global memory store.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query":          map[string]any{"type": "string"},
					"include_global": map[string]any{"type": "boolean"},
					"limit":          map[string]any{"type": "number"},
				},
				"required": []string{"query"},
			},
		},
		"memory_search_personal": {
			Description: "Search personal memories (type='profile') for the agent using hybrid retrieval (embeddings, tag matching, and FTS). Returns memories with raw_content, memory_type, and tags.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "Text query to search for in personal memories. Uses hybrid search with embeddings, tags, and FTS.",
					},
					"tags": map[string]any{
						"type":        "array",
						"description": "Optional tags to match against memory tags (intersection matching).",
						"items":       map[string]any{"type": "string"},
					},
					"limit": map[string]any{
						"type":        "number",
						"description": "Maximum number of results to return (default: 10).",
					},
					"memory_type": map[string]any{
						"type":        "string",
						"description": "Optional filter by normalized memory type (preference, biographical, habit, goal, value, project, other).",
					},
				},
				"required": []string{},
			},
		},
		"memory_remember_fact": {
			Description: "Store a global factual memory about the user.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"fact": map[string]any{"type": "string"},
				},
				"required": []string{"fact"},
			},
		},
		"memory_normalize": {
			Description: "Normalize a raw user or agent statement into a structured personal memory triple: normalized text, type, and tags.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"text": map[string]any{
						"type":        "string",
						"description": "Raw user or agent statement to normalize into a long-term memory.",
					},
				},
				"required": []string{"text"},
			},
		},
		"memory_store_personal": {
			Description: "Store a normalized personal memory about the user, using the output from memory_normalize.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent_id": map[string]any{
						"type":        "string",
						"description": "Optional agent ID on whose behalf the memory is stored (defaults to calling agent).",
					},
					"text": map[string]any{
						"type":        "string",
						"description": "Original raw statement, if available.",
					},
					"normalized": map[string]any{
						"type":        "string",
						"description": "Normalized third-person text from memory_normalize.",
					},
					"type": map[string]any{
						"type":        "string",
						"description": "Normalized memory type from memory_normalize (preference, biographical, habit, goal, value, project, other).",
					},
					"tags": map[string]any{
						"type":        "array",
						"description": "Tags returned by memory_normalize.",
						"items":       map[string]any{"type": "string"},
					},
					"thread_id": map[string]any{
						"type":        "string",
						"description": "Optional thread or conversation identifier.",
					},
					"importance": map[string]any{
						"type":        "number",
						"description": "Optional importance score; if omitted, a reasonable default is used.",
					},
					"metadata": map[string]any{
						"type":        "object",
						"description": "Optional additional metadata to associate with this memory.",
					},
				},
				"required": []string{"normalized", "type", "tags"},
			},
		},
	}
}
