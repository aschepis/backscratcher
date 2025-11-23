package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	ctxpkg "github.com/aschepis/backscratcher/staff/context"
	"github.com/aschepis/backscratcher/staff/logger"
	"github.com/aschepis/backscratcher/staff/memory"
)

// ToolHandler handles a tool call for a specific agent.
type ToolHandler func(ctx context.Context, agentID string, args json.RawMessage) (any, error)

// Registry maps tool names to handlers.
type Registry struct {
	handlers map[string]ToolHandler
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	logger.Info("Creating new tool Registry")
	return &Registry{
		handlers: make(map[string]ToolHandler),
	}
}

// Register registers a handler for a tool name.
func (r *Registry) Register(name string, h ToolHandler) {
	logger.Debug("Registering tool handler: %s", name)
	r.handlers[name] = h
}

// Handle dispatches a tool call.
// debugCallback is retrieved from context if available.
func (r *Registry) Handle(ctx context.Context, toolName, agentID string, argsStr []byte) (any, error) {
	logger.Info("Handling tool call: tool=%s agentID=%s", toolName, agentID)
	// Get debug callback from context using the shared context key
	dbg, _ := ctxpkg.GetDebugCallback(ctx)
	args := json.RawMessage(argsStr)
	h, ok := r.handlers[toolName]
	if !ok {
		logger.Error("Unknown tool requested: %s", toolName)
		return nil, fmt.Errorf("unknown tool: %s", toolName)
	}

	// Show tool execution start and log
	if dbg != nil {
		dbg(fmt.Sprintf("Executing tool: %s", toolName))
	}
	logger.Info("Executing tool '%s' for agent '%s'", toolName, agentID)
	// Show/log arguments (pretty-printed if possible)
	var prettyArgs interface{}
	if err := json.Unmarshal(argsStr, &prettyArgs); err == nil {
		if prettyBytes, err := json.MarshalIndent(prettyArgs, "", "  "); err == nil {
			argStr := string(prettyBytes)
			if dbg != nil {
				dbg(fmt.Sprintf("Tool arguments: %s", argStr))
			}
			logger.Debug("Tool '%s' called with arguments:\n%s", toolName, argStr)
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
		logger.Warn("Tool '%s' (agentID=%s) returned error: %v", toolName, agentID, err)
	} else {
		strResult := ""
		if resultBytes, e := json.MarshalIndent(result, "", "  "); e == nil {
			strResult = string(resultBytes)
			if len(strResult) > 500 {
				strResult = strResult[:500] + "... (truncated)"
			}
			logger.Info("Tool '%s' (agentID=%s) returned result: %s", toolName, agentID, strResult)
		} else {
			logger.Info("Tool '%s' (agentID=%s) returned result (non-jsonable): %v", toolName, agentID, result)
		}
	}

	return result, err
}

// RegisterMemoryTools registers memory-related tools backed by a MemoryRouter.
// Note: Tool names must match pattern ^[a-zA-Z0-9_-]{1,128}$ (no dots allowed)
// apiKey is used for the normalizer; if empty, falls back to ANTHROPIC_API_KEY environment variable.
func (r *Registry) RegisterMemoryTools(router *memory.MemoryRouter, apiKey string) {
	logger.Info("Registering memory tools in registry")

	// Normalizer instance shared by memory tools that only transform text.
	normalizer := memory.NewNormalizer("claude-3.5-haiku-latest", apiKey, 256)

	r.Register("memory_remember_episode", func(ctx context.Context, agentID string, args json.RawMessage) (any, error) {
		var payload struct {
			ThreadID string                 `json:"thread_id"`
			Content  string                 `json:"content"`
			Metadata map[string]interface{} `json:"metadata"`
		}
		logger.Debug("Received call to memory_remember_episode from agent=%s", agentID)
		if err := json.Unmarshal(args, &payload); err != nil {
			logger.Warn("Failed to decode arguments for memory_remember_episode: %v", err)
			return nil, err
		}
		logger.Info("Adding episode to memory: agentID=%s threadID=%s content=%.100q", agentID, payload.ThreadID, payload.Content)
		item, err := router.AddEpisode(ctx, agentID, payload.ThreadID, payload.Content, payload.Metadata)
		if err != nil {
			logger.Error("Error adding episode to memory: %v", err)
			return nil, err
		}
		logger.Debug("memory_remember_episode succeeded; returning id=%v", item.ID)
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
		logger.Debug("Received call to memory_remember_fact from agent=%s", agentID)
		if err := json.Unmarshal(args, &payload); err != nil {
			logger.Warn("Failed to decode arguments for memory_remember_fact: %v", err)
			return nil, fmt.Errorf("failed to unmarshal arguments: %w", err)
		}

		// Validate fact is not empty
		if strings.TrimSpace(payload.Fact) == "" {
			logger.Warn("Empty fact passed to memory_remember_fact for agent=%s", agentID)
			return nil, fmt.Errorf("fact cannot be empty")
		}

		if payload.Importance == 0 {
			payload.Importance = 0.9
			logger.Debug("Defaulting fact importance to 0.9 for agent=%s", agentID)
		}

		logger.Info("Adding global fact: fact=%.100q", payload.Fact)
		item, err := router.AddGlobalFact(ctx, payload.Fact, payload.Metadata)
		if err != nil {
			logger.Error("Failed to save global fact for agent %s: %v", agentID, err)
			return nil, fmt.Errorf("failed to save fact to database: %w", err)
		}

		logger.Debug("memory_remember_fact succeeded; returning id=%v", item.ID)
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
		logger.Debug("Received call to memory_search from agent=%s", agentID)
		if err := json.Unmarshal(args, &payload); err != nil {
			logger.Warn("Failed to decode arguments for memory_search: %v", err)
			return nil, err
		}
		if payload.Limit == 0 {
			payload.Limit = 10
			logger.Debug("Defaulting memory_search limit to 10 for agent=%s", agentID)
		}
		logger.Info("memory_search: Querying memory: agentID=%s query=%.80q global=%v limit=%d", agentID, payload.Query, payload.IncludeGlobal, payload.Limit)
		results, err := router.QueryAgentMemory(ctx, agentID, payload.Query, nil, payload.IncludeGlobal, payload.Limit, nil)
		if err != nil {
			logger.Error("memory_search failed for agent=%s: %v", agentID, err)
			return nil, err
		}
		logger.Info("memory_search: returned %d results for agent=%s", len(results), agentID)
		if len(results) == 0 {
			logger.Warn("memory_search: WARNING - no results found for query=%q, agentID=%s, includeGlobal=%v", payload.Query, agentID, payload.IncludeGlobal)
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
		logger.Debug("Received call to memory_search_personal from agent=%s", agentID)
		if err := json.Unmarshal(args, &payload); err != nil {
			logger.Warn("Failed to decode arguments for memory_search_personal: %v", err)
			return nil, err
		}
		if payload.Limit == 0 {
			payload.Limit = 10
			logger.Debug("Defaulting memory_search_personal limit to 10 for agent=%s", agentID)
		}

		var memoryTypes []string
		if payload.MemoryType != "" {
			memoryTypes = []string{payload.MemoryType}
		}

		logger.Info("Querying personal memory: agentID=%s query=%.80q tags=%v limit=%d", agentID, payload.Query, payload.Tags, payload.Limit)
		results, err := router.QueryPersonalMemory(ctx, agentID, payload.Query, payload.Tags, payload.Limit, memoryTypes)
		if err != nil {
			logger.Error("memory_search_personal failed for agent=%s: %v", agentID, err)
			return nil, err
		}
		logger.Debug("memory_search_personal returned %d results for agent=%s", len(results), agentID)
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

		logger.Debug("Received call to memory_store_personal from agent=%s", agentID)
		if err := json.Unmarshal(args, &payload); err != nil {
			logger.Warn("Failed to decode arguments for memory_store_personal: %v", err)
			return nil, fmt.Errorf("failed to unmarshal arguments: %w", err)
		}

		effectiveAgentID := agentID
		if payload.AgentID != nil && strings.TrimSpace(*payload.AgentID) != "" {
			effectiveAgentID = strings.TrimSpace(*payload.AgentID)
		}

		rawText := strings.TrimSpace(payload.Text)
		normalized := strings.TrimSpace(payload.Normalized)
		if rawText == "" && normalized == "" {
			logger.Warn("Empty text and normalized passed to memory_store_personal for agent=%s", effectiveAgentID)
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
			logger.Error("memory_store_personal failed for agent=%s: %v", effectiveAgentID, err)
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
		logger.Debug("Received call to memory_normalize from agent=%s", agentID)
		if err := json.Unmarshal(args, &payload); err != nil {
			logger.Warn("Failed to decode arguments for memory_normalize: %v", err)
			return nil, fmt.Errorf("failed to unmarshal arguments: %w", err)
		}
		payload.Text = strings.TrimSpace(payload.Text)
		if payload.Text == "" {
			logger.Warn("Empty text passed to memory_normalize for agent=%s", agentID)
			return nil, fmt.Errorf("text cannot be empty")
		}

		normalized, memType, tags, err := normalizer.Normalize(ctx, payload.Text)
		if err != nil {
			logger.Error("memory_normalize failed for agent=%s: %v", agentID, err)
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
	logger.Info("Registering remote tool: %s", name)
	r.Register(name, func(ctx context.Context, agentID string, args json.RawMessage) (any, error) {
		logger.Info("Calling remote tool: %s for agent=%s", name, agentID)
		resp, err := caller.Call(ctx, name, args)
		if err != nil {
			logger.Error("Remote tool '%s' call failed: %v", name, err)
			return nil, err
		}
		if len(resp) == 0 {
			logger.Warn("Remote tool '%s' returned empty response", name)
			return nil, nil
		}
		var out any
		if err := json.Unmarshal(resp, &out); err != nil {
			// If it's not valid JSON for some reason, return raw string.
			logger.Warn("Remote tool '%s' returned non-JSON; returning raw: %v", name, err)
			return string(resp), nil
		}
		logger.Debug("Remote tool '%s' returned response: %v", name, out)
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
	logger.Info("Registering MCP tool: safeName=%s originalName=%s", safeName, originalName)
	r.Register(safeName, func(ctx context.Context, agentID string, args json.RawMessage) (any, error) {
		logger.Info("Calling MCP tool: safeName=%s originalName=%s agent=%s", safeName, originalName, agentID)

		// Unmarshal args to map[string]interface{}
		var input map[string]interface{}
		if err := json.Unmarshal(args, &input); err != nil {
			logger.Error("Failed to unmarshal MCP tool args: %v", err)
			return nil, fmt.Errorf("failed to unmarshal tool arguments: %w", err)
		}

		// Invoke the tool using the original name
		result, err := invoker.InvokeTool(ctx, originalName, input)
		if err != nil {
			logger.Error("MCP tool '%s' (original: %s) call failed: %v", safeName, originalName, err)
			return nil, err
		}

		logger.Debug("MCP tool '%s' (original: %s) returned result: %v", safeName, originalName, result)
		return result, nil
	})
}
