package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/aschepis/backscratcher/staff/logger"
)

// StaffToolsData provides the data needed for staff tools without creating import cycles
type StaffToolsData struct {
	// Agent data
	GetAgents      func() map[string]AgentConfigData
	GetAgentState  func(agentID string) (string, *int64, error) // returns state, next_wake unix timestamp, error
	GetAllStates   func() (map[string]string, error)             // returns map[agentID]state
	GetNextWake    func(agentID string) (*int64, error)          // returns next_wake unix timestamp

	// Stats data
	GetStats      func(agentID string) (map[string]interface{}, error)
	GetAllStats   func() ([]map[string]interface{}, error)

	// Tool data
	GetAllToolSchemas func() map[string]ToolSchemaData

	// MCP data
	GetMCPServers func() map[string]MCPServerData
	GetMCPClients func() map[string]MCPClientData
}

type AgentConfigData struct {
	ID          string
	Name        string
	System      string
	Model       string
	MaxTokens   int64
	Tools       []string
	Schedule    string
	Disabled    bool
	StartupDelay string
}

type ToolSchemaData struct {
	Description string
}

type MCPServerData struct {
	Name      string
	Command   string
	URL       string
	ConfigFile string
	Args      []string
	Env       []string
}

type MCPClientData interface {
	ListTools(ctx context.Context) ([]MCPToolDefinition, error)
}

type MCPToolDefinition struct {
	Name        string
	Description string
	InputSchema map[string]interface{}
}


// RegisterStaffTools registers staff-specific introspection tools for the chief_of_staff agent
func (r *Registry) RegisterStaffTools(
	data StaffToolsData,
	workspacePath string,
	db *sql.DB,
) {
	logger.Info("Registering staff tools in registry")

	// list_agents - Returns config for all agents
	r.Register("list_agents", func(ctx context.Context, agentID string, args json.RawMessage) (any, error) {
		logger.Debug("Received call to list_agents from agent=%s", agentID)

		var agents []map[string]any
		agentConfigs := data.GetAgents()
		for id, cfg := range agentConfigs {
			agentMap := map[string]any{
				"id":        id,
				"name":      cfg.Name,
				"disabled":  cfg.Disabled,
				"schedule":  cfg.Schedule,
				"model":     cfg.Model,
				"max_tokens": cfg.MaxTokens,
				"tools":     cfg.Tools,
			}
			if cfg.System != "" {
				agentMap["system"] = cfg.System
			}
			if cfg.StartupDelay != "" {
				agentMap["startup_delay"] = cfg.StartupDelay
			}
			agents = append(agents, agentMap)
		}

		return map[string]any{
			"agents": agents,
			"count":  len(agents),
		}, nil
	})

	// get_agent_state - Returns state machine status for each agent
	r.Register("get_agent_state", func(ctx context.Context, agentID string, args json.RawMessage) (any, error) {
		var payload struct {
			AgentID string `json:"agent_id"`
		}
		logger.Debug("Received call to get_agent_state from agent=%s", agentID)
		if err := json.Unmarshal(args, &payload); err != nil {
			logger.Warn("Failed to decode arguments for get_agent_state: %v", err)
			return nil, fmt.Errorf("failed to unmarshal arguments: %w", err)
		}

		var results []map[string]any

		if payload.AgentID != "" {
			// Get state for specific agent
			state, nextWakeUnix, err := data.GetAgentState(payload.AgentID)
			if err != nil {
				return nil, fmt.Errorf("failed to get state for agent %s: %w", payload.AgentID, err)
			}

			result := map[string]any{
				"agent_id": payload.AgentID,
				"state":    state,
			}
			if nextWakeUnix != nil {
				result["next_wake"] = *nextWakeUnix
			} else {
				result["next_wake"] = nil
			}
			results = append(results, result)
		} else {
			// Get states for all agents
			states, err := data.GetAllStates()
			if err != nil {
				return nil, fmt.Errorf("failed to get all states: %w", err)
			}

			for agentID, state := range states {
				nextWakeUnix, err := data.GetNextWake(agentID)
				if err != nil {
					logger.Warn("Failed to get next_wake for agent %s: %v", agentID, err)
					continue
				}

				result := map[string]any{
					"agent_id": agentID,
					"state":    state,
				}
				if nextWakeUnix != nil {
					result["next_wake"] = *nextWakeUnix
				} else {
					result["next_wake"] = nil
				}
				results = append(results, result)
			}
		}

		return map[string]any{
			"states": results,
			"count":  len(results),
		}, nil
	})

	// get_agent_stats - Returns execution counts, failures, wakeups
	r.Register("get_agent_stats", func(ctx context.Context, agentID string, args json.RawMessage) (any, error) {
		var payload struct {
			AgentID string `json:"agent_id"`
		}
		logger.Debug("Received call to get_agent_stats from agent=%s", agentID)
		if err := json.Unmarshal(args, &payload); err != nil {
			logger.Warn("Failed to decode arguments for get_agent_stats: %v", err)
			return nil, fmt.Errorf("failed to unmarshal arguments: %w", err)
		}

		var results []map[string]any

		if payload.AgentID != "" {
			// Get stats for specific agent
			stats, err := data.GetStats(payload.AgentID)
			if err != nil {
				return nil, fmt.Errorf("failed to get stats for agent %s: %w", payload.AgentID, err)
			}
			results = append(results, stats)
		} else {
			// Get stats for all agents
			allStats, err := data.GetAllStats()
			if err != nil {
				return nil, fmt.Errorf("failed to get all stats: %w", err)
			}
			results = allStats
		}

		return map[string]any{
			"stats": results,
			"count": len(results),
		}, nil
	})

	// list_tools - Returns all registered tools
	r.Register("list_tools", func(ctx context.Context, agentID string, args json.RawMessage) (any, error) {
		logger.Debug("Received call to list_tools from agent=%s", agentID)

		schemas := data.GetAllToolSchemas()

		var tools []map[string]any
		for name, schema := range schemas {
			tools = append(tools, map[string]any{
				"name":        name,
				"description": schema.Description,
			})
		}

		return map[string]any{
			"tools": tools,
			"count": len(tools),
		}, nil
	})

	// list_mcp_servers - Returns configured MCP servers
	r.Register("list_mcp_servers", func(ctx context.Context, agentID string, args json.RawMessage) (any, error) {
		logger.Debug("Received call to list_mcp_servers from agent=%s", agentID)

		var servers []map[string]any
		mcpServers := data.GetMCPServers()
		for name, cfg := range mcpServers {
			serverMap := map[string]any{
				"name": name,
			}
			if cfg.Command != "" {
				serverMap["command"] = cfg.Command
				serverMap["transport"] = "stdio"
			} else if cfg.URL != "" {
				serverMap["url"] = cfg.URL
				serverMap["transport"] = "http"
			}
			if cfg.ConfigFile != "" {
				serverMap["config_file"] = cfg.ConfigFile
			}
			if len(cfg.Args) > 0 {
				serverMap["args"] = cfg.Args
			}
			if len(cfg.Env) > 0 {
				serverMap["env"] = cfg.Env
			}
			servers = append(servers, serverMap)
		}

		return map[string]any{
			"servers": servers,
			"count":   len(servers),
		}, nil
	})

	// mcp_tools_discover - Introspects MCP servers for tools + schemas
	r.Register("mcp_tools_discover", func(ctx context.Context, agentID string, args json.RawMessage) (any, error) {
		var payload struct {
			ServerName string `json:"server_name"`
		}
		logger.Debug("Received call to mcp_tools_discover from agent=%s", agentID)
		if err := json.Unmarshal(args, &payload); err != nil {
			logger.Warn("Failed to decode arguments for mcp_tools_discover: %v", err)
			return nil, fmt.Errorf("failed to unmarshal arguments: %w", err)
		}

		var allTools []map[string]any
		mcpClients := data.GetMCPClients()

		if payload.ServerName != "" {
			// Discover tools for specific server
			client, ok := mcpClients[payload.ServerName]
			if !ok {
				return nil, fmt.Errorf("MCP server %s not found", payload.ServerName)
			}

			tools, err := client.ListTools(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to list tools from server %s: %w", payload.ServerName, err)
			}

			for _, tool := range tools {
				allTools = append(allTools, map[string]any{
					"server":      payload.ServerName,
					"name":        tool.Name,
					"description": tool.Description,
					"input_schema": tool.InputSchema,
				})
			}
		} else {
			// Discover tools for all servers
			for serverName, client := range mcpClients {
				tools, err := client.ListTools(ctx)
				if err != nil {
					logger.Warn("Failed to list tools from server %s: %v", serverName, err)
					continue
				}

				for _, tool := range tools {
					allTools = append(allTools, map[string]any{
						"server":      serverName,
						"name":        tool.Name,
						"description": tool.Description,
						"input_schema": tool.InputSchema,
					})
				}
			}
		}

		return map[string]any{
			"tools": allTools,
			"count": len(allTools),
		}, nil
	})
}

