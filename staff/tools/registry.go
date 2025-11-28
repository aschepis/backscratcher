package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	ctxpkg "github.com/aschepis/backscratcher/staff/context"
	"github.com/aschepis/backscratcher/staff/memory"
	"github.com/rs/zerolog"
)

// ToolHandler handles a tool call for a specific agent.
type ToolHandler func(ctx context.Context, agentID string, args json.RawMessage) (any, error)

// Registry maps tool names to handlers.
type Registry struct {
	handlers map[string]ToolHandler
	logger   zerolog.Logger
}

// NewRegistry creates an empty registry.
func NewRegistry(logger zerolog.Logger) *Registry {
	logger = logger.With().Str("component", "tool_registry").Logger()
	logger.Info().Msg("Creating new tool Registry")
	return &Registry{
		handlers: make(map[string]ToolHandler),
		logger:   logger,
	}
}

// Register registers a handler for a tool name.
func (r *Registry) Register(name string, h ToolHandler) {
	r.logger.Debug().Str("name", name).Msg("Registering tool handler")
	r.handlers[name] = h
}

// Handle dispatches a tool call.
// debugCallback is retrieved from context if available.
func (r *Registry) Handle(ctx context.Context, toolName, agentID string, argsStr []byte) (any, error) {
	r.logger.Info().Str("tool", toolName).Str("agentID", agentID).Msg("Handling tool call")
	// Get debug callback from context using the shared context key
	dbg, _ := ctxpkg.GetDebugCallback(ctx)
	args := json.RawMessage(argsStr)
	h, ok := r.handlers[toolName]
	if !ok {
		r.logger.Error().Str("tool", toolName).Msg("Unknown tool requested")
		return nil, fmt.Errorf("unknown tool: %s", toolName)
	}

	// Show tool execution start and log
	if dbg != nil {
		dbg(fmt.Sprintf("Executing tool: %s", toolName))
	}
	r.logger.Info().Str("tool", toolName).Str("agentID", agentID).Msg("Executing tool")
	// Show/log arguments (pretty-printed if possible)
	var prettyArgs interface{}
	if err := json.Unmarshal(argsStr, &prettyArgs); err == nil {
		if prettyBytes, err := json.MarshalIndent(prettyArgs, "", "  "); err == nil {
			argStr := string(prettyBytes)
			if dbg != nil {
				dbg(fmt.Sprintf("Tool arguments: %s", argStr))
			}
			r.logger.Debug().Str("tool", toolName).Str("args", argStr).Msg("Tool called with arguments")
		}
	}

	result, err := h(ctx, agentID, args)

	// Show tool result
	if dbg != nil {
		if err != nil {
			dbg(fmt.Sprintf("Tool error: %v", err))
		} else {
			// Pretty-print result if possible
			if resultBytes, err := json.MarshalIndent(result, "", "  "); err == nil {
				resultStr := string(resultBytes)
				// Truncate very long results
				if len(resultStr) > 500 {
					resultStr = resultStr[:500] + "... (truncated)"
				}
				dbg(fmt.Sprintf("Tool result: %s", resultStr))
			} else {
				dbg(fmt.Sprintf("Tool result: %v", result))
			}
		}
	}

	// Log result or error
	if err != nil {
		r.logger.Warn().Str("tool", toolName).Str("agentID", agentID).Err(err).Msg("Tool returned error")
	} else {
		strResult := ""
		if resultBytes, e := json.MarshalIndent(result, "", "  "); e == nil {
			strResult = string(resultBytes)
			if len(strResult) > 500 {
				strResult = strResult[:500] + "... (truncated)"
			}
			r.logger.Info().Str("tool", toolName).Str("agentID", agentID).Str("result", strResult).Msg("Tool returned result")
		} else {
			r.logger.Info().Str("tool", toolName).Str("agentID", agentID).Interface("result", result).Msg("Tool returned result (non-jsonable)")
		}
	}

	return result, err
}

// RegisterMemoryTools registers memory-related tools backed by a MemoryRouter.
// Note: Tool names must match pattern ^[a-zA-Z0-9_-]{1,128}$ (no dots allowed)
// apiKey is used for the normalizer and must be provided from config.
func (r *Registry) RegisterMemoryTools(router *memory.MemoryRouter, apiKey string) {
	r.logger.Info().Msg("Registering memory tools in registry")

	// Normalizer instance shared by memory tools that only transform text.
	normalizer := memory.NewNormalizer("claude-3.5-haiku-latest", apiKey, 256, r.logger)

	r.Register("memory_remember_episode", func(ctx context.Context, agentID string, args json.RawMessage) (any, error) {
		var payload struct {
			ThreadID string                 `json:"thread_id"`
			Content  string                 `json:"content"`
			Metadata map[string]interface{} `json:"metadata"`
		}
		r.logger.Debug().Str("agentID", agentID).Msg("Received call to memory_remember_episode")
		if err := json.Unmarshal(args, &payload); err != nil {
			r.logger.Warn().Err(err).Msg("Failed to decode arguments for memory_remember_episode")
			return nil, err
		}
		r.logger.Info().Str("agentID", agentID).Str("threadID", payload.ThreadID).Str("content", payload.Content).Msg("Adding episode to memory")
		item, err := router.AddEpisode(ctx, agentID, payload.ThreadID, payload.Content, payload.Metadata)
		if err != nil {
			r.logger.Error().Err(err).Msg("Error adding episode to memory")
			return nil, err
		}
		r.logger.Debug().Int64("id", item.ID).Msg("memory_remember_episode succeeded")
		return map[string]any{
			"id":      item.ID,
			"scope":   item.Scope,
			"type":    item.Type,
			"created": item.CreatedAt,
		}, nil
	})

	r.Register("memory_remember_fact", func(ctx context.Context, agentID string, args json.RawMessage) (any, error) {
		var payload struct {
			Fact       string                 `json:"fact"`
			Importance float64                `json:"importance"`
			Metadata   map[string]interface{} `json:"metadata"`
		}
		r.logger.Debug().Str("agentID", agentID).Msg("Received call to memory_remember_fact")
		if err := json.Unmarshal(args, &payload); err != nil {
			r.logger.Warn().Err(err).Msg("Failed to decode arguments for memory_remember_fact")
			return nil, fmt.Errorf("failed to unmarshal arguments: %w", err)
		}

		// Validate fact is not empty
		if strings.TrimSpace(payload.Fact) == "" {
			r.logger.Warn().Str("agentID", agentID).Msg("Empty fact passed to memory_remember_fact")
			return nil, fmt.Errorf("fact cannot be empty")
		}

		if payload.Importance == 0 {
			payload.Importance = 0.9
			r.logger.Debug().Str("agentID", agentID).Msg("Defaulting fact importance to 0.9")
		}

		r.logger.Info().Str("fact", payload.Fact).Msg("Adding global fact")
		item, err := router.AddGlobalFact(ctx, payload.Fact, payload.Metadata)
		if err != nil {
			r.logger.Error().Str("agentID", agentID).Err(err).Msg("Failed to save global fact")
			return nil, fmt.Errorf("failed to save fact to database: %w", err)
		}

		r.logger.Debug().Int64("id", item.ID).Msg("memory_remember_fact succeeded")
		return map[string]any{
			"id":      item.ID,
			"scope":   item.Scope,
			"type":    item.Type,
			"created": item.CreatedAt,
		}, nil
	})

	r.Register("memory_search", func(ctx context.Context, agentID string, args json.RawMessage) (any, error) {
		var payload struct {
			Query         string `json:"query"`
			IncludeGlobal bool   `json:"include_global"`
			Limit         int    `json:"limit"`
		}
		r.logger.Debug().Str("agentID", agentID).Msg("Received call to memory_search")
		if err := json.Unmarshal(args, &payload); err != nil {
			r.logger.Warn().Err(err).Msg("Failed to decode arguments for memory_search")
			return nil, err
		}
		if payload.Limit == 0 {
			payload.Limit = 10
			r.logger.Debug().Str("agentID", agentID).Msg("Defaulting memory_search limit to 10")
		}
		r.logger.Info().
			Str("agentID", agentID).
			Str("query", payload.Query).
			Bool("includeGlobal", payload.IncludeGlobal).
			Int("limit", payload.Limit).
			Msg("memory_search: Querying memory")
		results, err := router.QueryAgentMemory(ctx, agentID, payload.Query, nil, payload.IncludeGlobal, payload.Limit, nil)
		if err != nil {
			r.logger.Error().Err(err).Str("agentID", agentID).Msg("memory_search failed for agent")
			return nil, err
		}
		r.logger.Info().Int("result_count", len(results)).Str("agentID", agentID).Msg("memory_search: returned results")
		if len(results) == 0 {
			r.logger.Warn().Str("query", payload.Query).Str("agentID", agentID).Bool("includeGlobal", payload.IncludeGlobal).Msg("memory_search: WARNING - no results found")
		}
		out := make([]map[string]any, 0, len(results))
		for _, r := range results {
			out = append(out, map[string]any{
				"id":       r.Item.ID,
				"scope":    r.Item.Scope,
				"type":     r.Item.Type,
				"content":  r.Item.Content,
				"metadata": r.Item.Metadata,
				"score":    r.Score,
			})
		}
		return out, nil
	})

	r.Register("memory_search_personal", func(ctx context.Context, agentID string, args json.RawMessage) (any, error) {
		var payload struct {
			Query      string   `json:"query"`
			Tags       []string `json:"tags"`
			Limit      int      `json:"limit"`
			MemoryType string   `json:"memory_type"` // optional filter by normalized memory type
		}
		r.logger.Debug().Str("agentID", agentID).Msg("Received call to memory_search_personal")
		if err := json.Unmarshal(args, &payload); err != nil {
			r.logger.Warn().Err(err).Msg("Failed to decode arguments for memory_search_personal")
			return nil, err
		}
		if payload.Limit == 0 {
			payload.Limit = 10
			r.logger.Debug().Str("agentID", agentID).Msg("Defaulting memory_search_personal limit to 10")
		}

		var memoryTypes []string
		if payload.MemoryType != "" {
			memoryTypes = []string{payload.MemoryType}
		}

		r.logger.Info().Str("agentID", agentID).Str("query", payload.Query).Interface("tags", payload.Tags).Int("limit", payload.Limit).Msg("Querying personal memory")
		results, err := router.QueryPersonalMemory(ctx, agentID, payload.Query, payload.Tags, payload.Limit, memoryTypes)
		if err != nil {
			r.logger.Error().Str("agentID", agentID).Err(err).Msg("memory_search_personal failed")
			return nil, err
		}
		r.logger.Debug().Int("result_count", len(results)).Str("agentID", agentID).Msg("memory_search_personal returned results")
		out := make([]map[string]any, 0, len(results))
		for _, r := range results {
			resultMap := map[string]any{
				"id":          r.Item.ID,
				"scope":       r.Item.Scope,
				"type":        r.Item.Type,
				"content":     r.Item.Content,
				"metadata":    r.Item.Metadata,
				"score":       r.Score,
				"raw_content": r.Item.RawContent,
			}
			if r.Item.MemoryType != "" {
				resultMap["memory_type"] = r.Item.MemoryType
			}
			if len(r.Item.Tags) > 0 {
				resultMap["tags"] = r.Item.Tags
			}
			out = append(out, resultMap)
		}
		return out, nil
	})

	r.Register("memory_store_personal", func(ctx context.Context, agentID string, args json.RawMessage) (any, error) {
		var payload struct {
			AgentID    *string                `json:"agent_id,omitempty"`
			Text       string                 `json:"text"`       // original/raw text
			Normalized string                 `json:"normalized"` // normalized third-person text
			Type       string                 `json:"type"`       // normalized memory type
			Tags       []string               `json:"tags"`       // tags from memory_normalize
			ThreadID   string                 `json:"thread_id"`  // optional conversation/thread id
			Importance float64                `json:"importance"` // optional importance; default handled by store
			Metadata   map[string]interface{} `json:"metadata"`   // optional extra metadata
		}

		r.logger.Debug().Str("agentID", agentID).Msg("Received call to memory_store_personal")
		if err := json.Unmarshal(args, &payload); err != nil {
			r.logger.Warn().Err(err).Msg("Failed to decode arguments for memory_store_personal")
			return nil, fmt.Errorf("failed to unmarshal arguments: %w", err)
		}

		effectiveAgentID := agentID
		if payload.AgentID != nil && strings.TrimSpace(*payload.AgentID) != "" {
			effectiveAgentID = strings.TrimSpace(*payload.AgentID)
		}

		rawText := strings.TrimSpace(payload.Text)
		normalized := strings.TrimSpace(payload.Normalized)
		if rawText == "" && normalized == "" {
			r.logger.Warn().Str("agentID", effectiveAgentID).Msg("Empty text and normalized passed to memory_store_personal")
			return nil, fmt.Errorf("either text or normalized must be provided")
		}

		var threadPtr *string
		if strings.TrimSpace(payload.ThreadID) != "" {
			tid := strings.TrimSpace(payload.ThreadID)
			threadPtr = &tid
		}

		item, err := router.StorePersonalMemory(
			ctx,
			effectiveAgentID,
			rawText,
			normalized,
			payload.Type,
			payload.Tags,
			threadPtr,
			payload.Importance,
			payload.Metadata,
		)
		if err != nil {
			r.logger.Error().Str("agentID", effectiveAgentID).Err(err).Msg("memory_store_personal failed")
			return nil, err
		}

		return map[string]any{
			"id":              item.ID,
			"scope":           item.Scope,
			"type":            item.Type,
			"memory_type":     item.MemoryType,
			"created":         item.CreatedAt,
			"raw_content":     item.RawContent,
			"normalized_text": item.Content,
			"tags":            item.Tags,
		}, nil
	})

	r.Register("memory_normalize", func(ctx context.Context, agentID string, args json.RawMessage) (any, error) {
		var payload struct {
			Text string `json:"text"`
		}
		r.logger.Debug().Str("agentID", agentID).Msg("Received call to memory_normalize")
		if err := json.Unmarshal(args, &payload); err != nil {
			r.logger.Warn().Err(err).Msg("Failed to decode arguments for memory_normalize")
			return nil, fmt.Errorf("failed to unmarshal arguments: %w", err)
		}
		payload.Text = strings.TrimSpace(payload.Text)
		if payload.Text == "" {
			r.logger.Warn().Str("agentID", agentID).Msg("Empty text passed to memory_normalize")
			return nil, fmt.Errorf("text cannot be empty")
		}

		normalized, memType, tags, err := normalizer.Normalize(ctx, payload.Text)
		if err != nil {
			r.logger.Error().Str("agentID", agentID).Err(err).Msg("memory_normalize failed")
			return nil, err
		}

		return map[string]any{
			"normalized": normalized,
			"type":       memType,
			"tags":       tags,
		}, nil
	})
}

// RemoteCaller represents something that can call a remote tool backend.
type RemoteCaller interface {
	Call(ctx context.Context, toolName string, args json.RawMessage) (json.RawMessage, error)
}

// RegisterRemoteTool registers a tool whose implementation is provided by a RemoteCaller.
func (r *Registry) RegisterRemoteTool(name string, caller RemoteCaller) {
	r.logger.Info().Str("name", name).Msg("Registering remote tool")
	r.Register(name, func(ctx context.Context, agentID string, args json.RawMessage) (any, error) {
		r.logger.Info().Str("name", name).Str("agentID", agentID).Msg("Calling remote tool")
		resp, err := caller.Call(ctx, name, args)
		if err != nil {
			r.logger.Error().Str("name", name).Err(err).Msg("Remote tool call failed")
			return nil, err
		}
		if len(resp) == 0 {
			r.logger.Warn().Str("name", name).Msg("Remote tool returned empty response")
			return nil, nil
		}
		var out any
		if err := json.Unmarshal(resp, &out); err != nil {
			// If it's not valid JSON for some reason, return raw string.
			r.logger.Warn().Str("name", name).Err(err).Msg("Remote tool returned non-JSON; returning raw")
			return string(resp), nil
		}
		r.logger.Debug().Str("name", name).Interface("out", out).Msg("Remote tool returned response")
		return out, nil
	})
}

// MCPToolInvoker represents something that can invoke an MCP tool.
type MCPToolInvoker interface {
	InvokeTool(ctx context.Context, originalName string, input map[string]interface{}) (map[string]interface{}, error)
}

// RegisterMCPTool registers a tool whose implementation is provided by an MCP client.
// safeName is the tool name safe for Anthropic API (no dots).
// originalName is the original MCP tool name (may contain dots).
func (r *Registry) RegisterMCPTool(safeName, originalName string, invoker MCPToolInvoker) {
	r.logger.Debug().Str("safeName", safeName).Str("originalName", originalName).Msg("Registering MCP tool")
	// TODO: update all r.Register commands to provide a logger with the tool name and agentID already set
	r.Register(safeName, func(ctx context.Context, agentID string, args json.RawMessage) (any, error) {
		r.logger.Info().Str("safeName", safeName).Str("originalName", originalName).Str("agentID", agentID).Msg("Calling MCP tool")

		// Unmarshal args to map[string]interface{}
		var input map[string]interface{}
		if err := json.Unmarshal(args, &input); err != nil {
			r.logger.Error().Err(err).Msg("Failed to unmarshal MCP tool args")
			return nil, fmt.Errorf("failed to unmarshal tool arguments: %w", err)
		}

		// Invoke the tool using the original name
		result, err := invoker.InvokeTool(ctx, originalName, input)
		if err != nil {
			r.logger.Error().Str("safeName", safeName).Str("originalName", originalName).Err(err).Msg("MCP tool call failed")
			return nil, err
		}

		r.logger.Debug().Str("safeName", safeName).Str("originalName", originalName).Interface("result", result).Msg("MCP tool returned result")
		return result, nil
	})
}
