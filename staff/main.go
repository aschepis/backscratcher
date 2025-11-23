package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"

	"github.com/aschepis/backscratcher/staff/agent"
	"github.com/aschepis/backscratcher/staff/config"
	"github.com/aschepis/backscratcher/staff/llm"
	"github.com/aschepis/backscratcher/staff/logger"
	"github.com/aschepis/backscratcher/staff/mcp"
	"github.com/aschepis/backscratcher/staff/memory"
	"github.com/aschepis/backscratcher/staff/memory/ollama"
	"github.com/aschepis/backscratcher/staff/migrations"
	"github.com/aschepis/backscratcher/staff/runtime"
	"github.com/aschepis/backscratcher/staff/tools"
	"github.com/aschepis/backscratcher/staff/ui"
	"github.com/aschepis/backscratcher/staff/ui/tui"
	_ "github.com/mattn/go-sqlite3"
)

// mcpClientAdapter adapts mcp.MCPClient to tools.MCPClientData
type mcpClientAdapter struct {
	client mcp.MCPClient
}

func (a *mcpClientAdapter) ListTools(ctx context.Context) ([]tools.MCPToolDefinition, error) {
	mcpTools, err := a.client.ListTools(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]tools.MCPToolDefinition, len(mcpTools))
	for i, tool := range mcpTools {
		result[i] = tools.MCPToolDefinition{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: tool.InputSchema,
		}
	}
	return result, nil
}

// registerAllTools registers all tool handlers and schemas with the crew.
// This centralizes all tool registration logic.
func registerAllTools(crew *agent.Crew, memoryRouter *memory.MemoryRouter, workspacePath string, db *sql.DB, stateManager *agent.StateManager, apiKey string) {
	// Register tool handlers
	crew.ToolRegistry.RegisterMemoryTools(memoryRouter, apiKey)
	crew.ToolRegistry.RegisterFilesystemTools(workspacePath)
	crew.ToolRegistry.RegisterSystemTools(workspacePath)
	// TODO: it is a bit weird that we pass a state change func that uses statemanager to
	// update the datababase when we have the database right there. This exists because
	// the agents themselves will not have access to the database. NOTE: this may eventually
	// cause issues if we want to have multi-query transactions.
	crew.ToolRegistry.RegisterNotificationTools(db, func(agentID string, state string) error {
		return stateManager.SetState(agentID, agent.State(state))
	})

	// Register staff tools with data accessors
	staffData := tools.StaffToolsData{
		GetAgents: func() map[string]tools.AgentConfigData {
			result := make(map[string]tools.AgentConfigData)
			agents := crew.GetAgents()
			for id, cfg := range agents {
				result[id] = tools.AgentConfigData{
					ID:           cfg.ID,
					Name:         cfg.Name,
					System:       cfg.System,
					Model:        cfg.Model,
					MaxTokens:    cfg.MaxTokens,
					Tools:        cfg.Tools,
					Schedule:     cfg.Schedule,
					Disabled:     cfg.Disabled,
					StartupDelay: cfg.StartupDelay,
				}
			}
			return result
		},
		GetAgentState: func(agentID string) (string, *int64, error) {
			state, err := crew.StateManager().GetState(agentID)
			if err != nil {
				return "", nil, err
			}
			nextWake, err := crew.StateManager().GetNextWake(agentID)
			if err != nil {
				return "", nil, err
			}
			var nextWakeUnix *int64
			if nextWake != nil {
				unix := nextWake.Unix()
				nextWakeUnix = &unix
			}
			return string(state), nextWakeUnix, nil
		},
		GetAllStates: func() (map[string]string, error) {
			states, err := crew.StateManager().GetAllStates()
			if err != nil {
				return nil, err
			}
			result := make(map[string]string)
			for id, state := range states {
				result[id] = string(state)
			}
			return result, nil
		},
		GetNextWake: func(agentID string) (*int64, error) {
			nextWake, err := crew.StateManager().GetNextWake(agentID)
			if err != nil {
				return nil, err
			}
			if nextWake != nil {
				unix := nextWake.Unix()
				return &unix, nil
			}
			return nil, nil
		},
		GetStats: func(agentID string) (map[string]interface{}, error) {
			return crew.StatsManager().GetStats(agentID)
		},
		GetAllStats: func() ([]map[string]interface{}, error) {
			return crew.StatsManager().GetAllStats()
		},
		GetAllToolSchemas: func() map[string]tools.ToolSchemaData {
			schemas := crew.ToolProvider.GetAllSchemas()
			result := make(map[string]tools.ToolSchemaData)
			for name, schema := range schemas {
				result[name] = tools.ToolSchemaData{
					Description: schema.Description,
				}
			}
			return result
		},
		GetMCPServers: func() map[string]tools.MCPServerData {
			result := make(map[string]tools.MCPServerData)
			servers := crew.GetMCPServers()
			for name, cfg := range servers {
				result[name] = tools.MCPServerData{
					Name:       name,
					Command:    cfg.Command,
					URL:        cfg.URL,
					ConfigFile: cfg.ConfigFile,
					Args:       cfg.Args,
					Env:        cfg.Env,
				}
			}
			return result
		},
		GetMCPClients: func() map[string]tools.MCPClientData {
			result := make(map[string]tools.MCPClientData)
			clients := crew.GetMCPClients()
			for name, client := range clients {
				result[name] = &mcpClientAdapter{client: client}
			}
			return result
		},
	}
	crew.ToolRegistry.RegisterStaffTools(staffData, workspacePath, db)

	// Register schemas for memory tools
	// Note: Tool names must match pattern ^[a-zA-Z0-9_-]{1,128}$ (no dots allowed)
	crew.ToolProvider.RegisterSchema("memory_search", agent.ToolSchema{
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
	})

	crew.ToolProvider.RegisterSchema("memory_search_personal", agent.ToolSchema{
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
	})

	crew.ToolProvider.RegisterSchema("memory_remember_fact", agent.ToolSchema{
		Description: "Store a global factual memory about the user.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"fact": map[string]any{"type": "string"},
			},
			"required": []string{"fact"},
		},
	})

	crew.ToolProvider.RegisterSchema("memory_normalize", agent.ToolSchema{
		Description: "Normalize a raw user or agent statement into a structured personal memory triple: normalized text, type, and tags.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text": map[string]any{"type": "string", "description": "Raw user or agent statement to normalize into a long-term memory."},
			},
			"required": []string{"text"},
		},
	})

	crew.ToolProvider.RegisterSchema("memory_store_personal", agent.ToolSchema{
		Description: "Store a normalized personal memory about the user, using the output from memory_normalize.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"agent_id":   map[string]any{"type": "string", "description": "Optional agent ID on whose behalf the memory is stored (defaults to calling agent)."},
				"text":       map[string]any{"type": "string", "description": "Original raw statement, if available."},
				"normalized": map[string]any{"type": "string", "description": "Normalized third-person text from memory_normalize."},
				"type":       map[string]any{"type": "string", "description": "Normalized memory type from memory_normalize (preference, biographical, habit, goal, value, project, other)."},
				"tags": map[string]any{
					"type":        "array",
					"description": "Tags returned by memory_normalize.",
					"items":       map[string]any{"type": "string"},
				},
				"thread_id":  map[string]any{"type": "string", "description": "Optional thread or conversation identifier."},
				"importance": map[string]any{"type": "number", "description": "Optional importance score; if omitted, a reasonable default is used."},
				"metadata":   map[string]any{"type": "object", "description": "Optional additional metadata to associate with this memory."},
			},
			"required": []string{"normalized", "type", "tags"},
		},
	})

	// Register schemas for filesystem tools
	crew.ToolProvider.RegisterSchema("read_file", agent.ToolSchema{
		Description: "Read the contents of a file. Returns the file content, size, and path.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":      map[string]any{"type": "string", "description": "Path to the file to read (relative to workspace)"},
				"encoding":  map[string]any{"type": "string", "description": "File encoding (default: utf-8)"},
				"max_bytes": map[string]any{"type": "number", "description": "Maximum number of bytes to read (0 = read entire file)"},
			},
			"required": []string{"path"},
		},
	})

	crew.ToolProvider.RegisterSchema("write_file", agent.ToolSchema{
		Description: "Write content to a file. Creates the file if it doesn't exist, overwrites if it does.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":        map[string]any{"type": "string", "description": "Path to the file to write (relative to workspace)"},
				"content":     map[string]any{"type": "string", "description": "Content to write to the file"},
				"create_dirs": map[string]any{"type": "boolean", "description": "Create parent directories if they don't exist"},
			},
			"required": []string{"path", "content"},
		},
	})

	crew.ToolProvider.RegisterSchema("list_directory", agent.ToolSchema{
		Description: "List files and directories in a path. Can list recursively and optionally include hidden files.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":           map[string]any{"type": "string", "description": "Path to the directory to list (relative to workspace, default: '.')"},
				"recursive":      map[string]any{"type": "boolean", "description": "Whether to list recursively"},
				"include_hidden": map[string]any{"type": "boolean", "description": "Whether to include hidden files (starting with '.')"},
			},
			"required": []string{},
		},
	})

	crew.ToolProvider.RegisterSchema("file_search", agent.ToolSchema{
		Description: "Search for files using glob patterns (e.g., '*.go', '**/*.test.go').",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{"type": "string", "description": "Glob pattern to match files (e.g., '*.go', '**/*.test.go')"},
				"root":    map[string]any{"type": "string", "description": "Root directory to search from (relative to workspace, default: '.')"},
				"limit":   map[string]any{"type": "number", "description": "Maximum number of matches to return (default: 100)"},
			},
			"required": []string{"pattern"},
		},
	})

	crew.ToolProvider.RegisterSchema("file_info", agent.ToolSchema{
		Description: "Get metadata about a file or directory (size, mode, modification time, etc.).",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "description": "Path to the file or directory (relative to workspace)"},
			},
			"required": []string{"path"},
		},
	})

	crew.ToolProvider.RegisterSchema("create_directory", agent.ToolSchema{
		Description: "Create a directory. Can create parent directories if needed.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string", "description": "Path to the directory to create (relative to workspace)"},
				"parents": map[string]any{"type": "boolean", "description": "Whether to create parent directories if they don't exist"},
			},
			"required": []string{"path"},
		},
	})

	crew.ToolProvider.RegisterSchema("grep_search", agent.ToolSchema{
		Description: "Search file contents using regex patterns. Returns matching lines with line numbers and optional context.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern":        map[string]any{"type": "string", "description": "Regex pattern to search for"},
				"path":           map[string]any{"type": "string", "description": "Path to file or directory to search in (relative to workspace)"},
				"case_sensitive": map[string]any{"type": "boolean", "description": "Whether the search should be case-sensitive (default: false)"},
				"context_lines":  map[string]any{"type": "number", "description": "Number of context lines to include around each match (0-5, default: 0)"},
			},
			"required": []string{"pattern", "path"},
		},
	})

	// Register schemas for system tools
	crew.ToolProvider.RegisterSchema("execute_command", agent.ToolSchema{
		Description: "Execute a shell command in the workspace directory. WARNING: This tool blocks dangerous commands that could damage the system, delete files, format disks, or execute arbitrary code from the internet. Please use safe commands only and avoid any operations that could modify or delete files, format storage devices, or download and execute code. Commands that attempt file deletion, disk formatting, or piping from remote sources will be automatically blocked for safety.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command":     map[string]any{"type": "string", "description": "Command to execute (e.g., 'ls', 'grep', 'git')"},
				"args":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Command arguments"},
				"timeout":     map[string]any{"type": "number", "description": "Timeout in seconds (default: 30, max: 300)"},
				"working_dir": map[string]any{"type": "string", "description": "Working directory relative to workspace (default: workspace root)"},
				"stdin":       map[string]any{"type": "string", "description": "Standard input to pipe to the command"},
			},
			"required": []string{"command"},
		},
	})

	// Register schemas for notification tools
	crew.ToolProvider.RegisterSchema("send_user_notification", agent.ToolSchema{
		Description: "Send a notification to the user. Inserts the notification into the inbox table and attempts to display a desktop notification. Use this when you need to alert the user about something important, request their attention, or notify them of completed tasks.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"message":           map[string]any{"type": "string", "description": "The notification message to send to the user"},
				"title":             map[string]any{"type": "string", "description": "Optional title for the notification (default: 'Staff Notification')"},
				"thread_id":         map[string]any{"type": "string", "description": "Optional thread ID to associate the notification with a conversation"},
				"requires_response": map[string]any{"type": "boolean", "description": "Whether this notification requires a response from the user"},
			},
			"required": []string{"message"},
		},
	})

	// Register schemas for staff tools
	crew.ToolProvider.RegisterSchema("list_agents", agent.ToolSchema{
		Description: "List all configured agents with their configuration details.",
		Schema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
			"required":   []string{},
		},
	})

	crew.ToolProvider.RegisterSchema("get_agent_state", agent.ToolSchema{
		Description: "Get the current state and next_wake time for one or all agents.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"agent_id": map[string]any{"type": "string", "description": "Optional agent ID. If omitted, returns states for all agents."},
			},
			"required": []string{},
		},
	})

	crew.ToolProvider.RegisterSchema("get_agent_stats", agent.ToolSchema{
		Description: "Get execution statistics (execution_count, failure_count, wakeup_count, last_execution, last_failure) for one or all agents.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"agent_id": map[string]any{"type": "string", "description": "Optional agent ID. If omitted, returns stats for all agents."},
			},
			"required": []string{},
		},
	})

	crew.ToolProvider.RegisterSchema("list_tools", agent.ToolSchema{
		Description: "List all registered tools with their descriptions.",
		Schema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
			"required":   []string{},
		},
	})

	crew.ToolProvider.RegisterSchema("list_mcp_servers", agent.ToolSchema{
		Description: "List all configured MCP servers with their configuration details.",
		Schema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
			"required":   []string{},
		},
	})

	crew.ToolProvider.RegisterSchema("mcp_tools_discover", agent.ToolSchema{
		Description: "Discover tools available from MCP servers. Returns tool definitions including name, description, and input schema.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"server_name": map[string]any{"type": "string", "description": "Optional MCP server name. If omitted, discovers tools from all configured MCP servers."},
			},
			"required": []string{},
		},
	})
}

// registerMCPServers discovers and registers tools from MCP servers.
func registerMCPServers(crew *agent.Crew, servers map[string]*agent.MCPServerConfig) {
	logger.Info("registerMCPServers: starting registration for %d MCP server(s)", len(servers))
	if len(servers) == 0 {
		logger.Info("No MCP servers configured")
		return
	}

	// Create a context with timeout for MCP server registration
	// Use a longer timeout (60 seconds) to allow slow-starting servers
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	adapter := mcp.NewNameAdapter()
	logger.Info("registerMCPServers: created context with 60s timeout and name adapter, beginning server registration loop")

	for serverName, serverConfig := range servers {
		if serverConfig == nil {
			logger.Warn("MCP server %s has nil config, skipping", serverName)
			continue
		}

		// Determine if this is a Claude MCP server
		source := "agents.yaml"
		if strings.HasPrefix(serverName, "claude_") {
			source = "Claude config"
		}
		logger.Info("Registering MCP server: %s (source: %s, command=%s url=%s)", serverName, source, serverConfig.Command, serverConfig.URL)

		var mcpClient mcp.MCPClient
		var err error

		// Create client based on transport type
		switch {
		case serverConfig.Command != "":
			// STDIO transport
			logger.Info("Creating STDIO MCP client for %s: command=%s args=%v env=%v", serverName, serverConfig.Command, serverConfig.Args, serverConfig.Env)
			mcpClient, err = mcp.NewStdioMCPClient(serverConfig.Command, serverConfig.ConfigFile, serverConfig.Args, serverConfig.Env)
			if err != nil {
				logger.Error("Failed to create STDIO MCP client for %s: %v", serverName, err)
				continue
			}
			logger.Info("Successfully created STDIO MCP client for %s", serverName)
		case serverConfig.URL != "":
			// HTTP transport
			logger.Info("Creating HTTP MCP client for %s: url=%s", serverName, serverConfig.URL)
			mcpClient, err = mcp.NewHttpMCPClient(serverConfig.URL, serverConfig.ConfigFile)
			if err != nil {
				logger.Error("Failed to create HTTP MCP client for %s: %v", serverName, err)
				continue
			}
			logger.Info("Successfully created HTTP MCP client for %s", serverName)
		default:
			logger.Warn("MCP server %s has neither command nor url, skipping", serverName)
			continue
		}

		// Start the client
		logger.Info("Starting MCP client for %s", serverName)
		if err := mcpClient.Start(ctx); err != nil {
			logger.Error("Failed to start MCP client for %s: %v", serverName, err)
			_ = mcpClient.Close() //nolint:errcheck // Cleanup on error
			continue
		}
		logger.Info("MCP client started successfully for %s", serverName)

		// Discover tools
		logger.Info("Discovering tools from MCP server %s", serverName)
		tools, err := mcpClient.ListTools(ctx)
		if err != nil {
			logger.Error("Failed to list tools from MCP server %s: %v", serverName, err)
			_ = mcpClient.Close() //nolint:errcheck // Cleanup on error
			continue
		}

		logger.Info("Discovered %d tools from MCP server %s", len(tools), serverName)

		// Register each tool
		for _, tool := range tools {
			originalName := tool.Name
			safeName := adapter.GetSafeName(originalName)

			// Register the tool handler
			crew.ToolRegistry.RegisterMCPTool(safeName, originalName, mcpClient)

			// Register the tool schema
			// Convert inputSchema to the format expected by ToolProvider
			var schema map[string]any
			if tool.InputSchema != nil {
				schema = tool.InputSchema
			} else {
				// Default schema if none provided
				schema = map[string]any{
					"type":       "object",
					"properties": make(map[string]any),
				}
			}

			// Log the raw schema for debugging
			if schemaBytes, err := json.MarshalIndent(schema, "", "  "); err == nil {
				logger.Debug("Registering MCP tool schema for %s (original: %s) from server %s:\n%s", safeName, originalName, serverName, string(schemaBytes))
			}

			crew.ToolProvider.RegisterSchemaWithServer(safeName, agent.ToolSchema{
				Description: tool.Description,
				Schema:      schema,
			}, serverName)

			logger.Info("Registered MCP tool: safeName=%s originalName=%s server=%s", safeName, originalName, serverName)
		}

		// Store MCP server config and client in Crew
		crew.MCPServers[serverName] = serverConfig
		crew.MCPClients[serverName] = mcpClient
		logger.Info("Completed registration for MCP server: %s", serverName)
	}
	logger.Info("registerMCPServers: completed registration for all MCP servers")
}

func main() {
	// Initialize logger
	if err := logger.Init(); err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}
	defer logger.Close() //nolint:errcheck // No remedy for logger close errors

	logger.Info("Starting Staff application")

	// Load dotenv
	_ = godotenv.Load("../.env")
	_ = godotenv.Load()

	// Load configuration file
	configPath := config.GetConfigPath()
	appConfig, err := config.LoadConfig(configPath)
	if err != nil {
		logger.Warn("Failed to load config file %q: %v (using environment variables)", configPath, err)
		appConfig = &config.Config{MCPServers: make(map[string]config.MCPServerSecrets)}
	} else {
		logger.Info("Loaded configuration from %q", configPath)
	}

	// Get Anthropic API key: env var takes precedence, then config file
	anthropicAPIKey := os.Getenv("ANTHROPIC_API_KEY")
	if anthropicAPIKey == "" {
		anthropicAPIKey = appConfig.Anthropic.APIKey
	}
	if anthropicAPIKey == "" {
		logger.Error("Missing ANTHROPIC_API_KEY (not found in environment or config file)")
		_ = logger.Close()                      //nolint:errcheck // Closing before fatal exit
		log.Fatalf("Missing ANTHROPIC_API_KEY") //nolint:gocritic // Fatal exit
	}

	// ---------------------------
	// 1. Open SQLite + Memory Store
	// ---------------------------

	logger.Info("Initializing database and memory store")
	db, err := sql.Open("sqlite3", "staff_memory.db")
	if err != nil {
		logger.Error("Failed to open database: %v", err)
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close() //nolint:errcheck // No remedy for db close errors

	// Run database migrations
	if err := migrations.RunMigrations(db, "./migrations"); err != nil {
		logger.Error("Failed to run migrations: %v", err)
		log.Fatalf("Failed to run migrations: %v", err)
	}

	embedder, err := ollama.NewEmbedder(ollama.ModelMXBAI)
	if err != nil {
		logger.Error("Failed to create ollama embedder: %v", err)
		log.Fatalf("Failed to create ollama embedder: %v", err)
	}

	store, err := memory.NewStore(db, embedder)
	if err != nil {
		logger.Error("Failed to create memory store: %v", err)
		log.Fatalf("Failed to create memory store: %v", err)
	}

	memoryRouter := memory.NewMemoryRouter(store, memory.Config{
		Summarizer: memory.NewAnthropicSummarizer("claude-3.5-haiku-latest", anthropicAPIKey, 256),
	})

	// ---------------------------
	// 2. Create Crew + Shared Tools
	// ---------------------------

	logger.Info("Creating crew and registering tools")
	crew := agent.NewCrew(anthropicAPIKey, db)

	// Get workspace path (default to current directory, or staff directory)
	workspacePath, err := os.Getwd()
	if err != nil {
		workspacePath = "."
		logger.Warn("Failed to get current directory, using '.' as workspace")
	}

	// Register all tools (handlers and schemas)
	registerAllTools(crew, memoryRouter, workspacePath, db, crew.StateManager(), anthropicAPIKey)

	// ---------------------------
	// 3. Load Agents from YAML
	// ---------------------------

	logger.Info("Loading agent configuration")
	agentsConfigPath := "agents.yaml"
	if envPath := os.Getenv("AGENTS_CONFIG"); envPath != "" {
		agentsConfigPath = envPath
	}

	// Read agents.yaml as bytes for merging
	agentsYAML, err := os.ReadFile(agentsConfigPath)
	if err != nil {
		logger.Error("Failed to read agent config from %q: %v", agentsConfigPath, err)
		log.Fatalf("Failed to read agent config from %q: %v", agentsConfigPath, err)
	}

	// Merge MCP server configs from config file with agents.yaml
	mergedYAML, err := config.MergeMCPServerConfigs(agentsYAML, appConfig.MCPServers)
	if err != nil {
		logger.Warn("Failed to merge MCP server configs: %v (using agents.yaml as-is)", err)
		mergedYAML = agentsYAML
	} else {
		logger.Info("Merged MCP server configurations from config file with agents.yaml")
	}

	// Parse the merged YAML
	var cfg agent.CrewConfig
	if err := yaml.Unmarshal(mergedYAML, &cfg); err != nil {
		logger.Error("Failed to parse merged agent config: %v", err)
		log.Fatalf("Failed to parse merged agent config: %v", err)
	}

	// Ensure IDs are set if missing (use map key) and apply smart defaults
	for id, agentCfg := range cfg.Agents {
		if agentCfg.ID == "" {
			agentCfg.ID = id
		}
		// Set smart defaults for agent values
		if agentCfg.Name == "" {
			agentCfg.Name = agentCfg.ID
		}
		if agentCfg.MaxTokens == 0 {
			agentCfg.MaxTokens = 2048
		}
		if agentCfg.Model == "" {
			agentCfg.Model = "claude-haiku-4-5"
		}
	}

	if err := crew.LoadCrewConfig(cfg); err != nil {
		logger.Error("Failed to load crew config: %v", err)
		log.Fatalf("Failed to load crew config: %v", err)
	}

	// Load and merge Claude MCP servers if enabled
	if appConfig.ClaudeMCP.Enabled {
		logger.Info("Claude MCP integration is enabled, loading Claude MCP servers")

		// Determine Claude config path
		claudeConfigPath := appConfig.ClaudeMCP.ConfigPath
		if claudeConfigPath == "" {
			// Default to ~/.claude.json
			homeDir, err := os.UserHomeDir()
			if err != nil {
				logger.Warn("Failed to get home directory for Claude config, skipping: %v", err)
			} else {
				claudeConfigPath = filepath.Join(homeDir, ".claude.json")
			}
		}

		if claudeConfigPath != "" {
			// Check if config file exists and get modification time for change detection
			var configModified time.Time
			if stat, err := os.Stat(claudeConfigPath); err == nil {
				configModified = stat.ModTime()
			}

			// Load Claude config
			logger.Info("Loading Claude MCP config from: %q", claudeConfigPath)
			claudeConfig, err := config.LoadClaudeConfig(claudeConfigPath)
			if err != nil {
				logger.Warn("Failed to load Claude config from %q: %v (skipping Claude MCP servers)", claudeConfigPath, err)
			} else {
				logger.Info("Claude config loaded successfully, extracting MCP servers from projects (filter: %v)", appConfig.ClaudeMCP.Projects)
				// Extract MCP servers from projects
				claudeServers, projectToServers := config.ExtractMCPServersFromProjects(claudeConfig, appConfig.ClaudeMCP.Projects)

				if len(claudeServers) > 0 {
					logger.Info("Found %d Claude MCP server(s) to process", len(claudeServers))
					// Map to our format
					mappedServers := config.MapClaudeToMCPServerConfig(claudeServers)

					// Merge with existing servers (agents.yaml takes precedence)
					addedCount := 0
					skippedCount := 0
					for name, serverCfg := range mappedServers {
						if _, exists := cfg.MCPServers[name]; !exists {
							cfg.MCPServers[name] = serverCfg
							logger.Info("Added Claude MCP server: %s (from project config)", name)
							addedCount++
						} else {
							logger.Info("Skipping Claude MCP server %s (already exists in agents.yaml, agents.yaml takes precedence)", name)
							skippedCount++
						}
					}
					logger.Info("Claude MCP server merge complete: %d added, %d skipped (already in agents.yaml)", addedCount, skippedCount)

					// Log summary
					projectList := make([]string, 0, len(projectToServers))
					hasGlobal := false
					projectCount := 0
					for projectPath := range projectToServers {
						if projectPath == "Global" {
							hasGlobal = true
						} else {
							projectCount++
						}
						projectList = append(projectList, projectPath)
					}
					if hasGlobal {
						logger.Info("Loaded %d Claude MCP server(s) from Global and %d project(s): %v", addedCount, projectCount, projectList)
					} else {
						logger.Info("Loaded %d Claude MCP server(s) from %d project(s): %v", addedCount, projectCount, projectList)
					}

					// Log restart notification if config was recently modified (within last minute)
					if !configModified.IsZero() && time.Since(configModified) < time.Minute {
						logger.Info("Claude MCP configuration file was recently modified. Please restart the application for changes to take effect.")
					}
				} else {
					if len(appConfig.ClaudeMCP.Projects) > 0 {
						logger.Info("No Claude MCP servers found in specified projects")
					} else {
						logger.Info("No Claude MCP servers found in any projects")
					}
				}
			}
		}
	} else {
		logger.Info("Claude MCP integration is disabled")
	}

	// Register MCP servers and their tools
	logger.Info("Starting MCP server registration for %d total server(s) (from agents.yaml and Claude config)", len(cfg.MCPServers))
	registerMCPServers(crew, cfg.MCPServers)

	// Initialize message summarizer if enabled
	if !appConfig.MessageSummarization.Disabled {
		logger.Info("Message summarization is enabled, initializing Ollama summarizer (model: %s)", appConfig.MessageSummarization.Model)
		// Convert config.MessageSummarization to agent.MessageSummarizationConfig to avoid import cycle
		summarizerConfig := agent.MessageSummarizerConfig{
			Model:         appConfig.MessageSummarization.Model,
			MaxChars:      appConfig.MessageSummarization.MaxChars,
			MaxLines:      appConfig.MessageSummarization.MaxLines,
			MaxLineBreaks: appConfig.MessageSummarization.MaxLineBreaks,
		}
		messageSummarizer, err := agent.NewMessageSummarizer(summarizerConfig)
		if err != nil {
			logger.Error("Failed to create message summarizer: %v", err)
			log.Fatalf("Failed to create message summarizer: %v", err)
		}
		if messageSummarizer != nil {
			crew.SetMessageSummarizer(messageSummarizer)
			logger.Info("Message summarizer initialized successfully")
		}
	} else {
		logger.Info("Message summarization is disabled")
	}

	// ---------------------------
	// 4. Create Chat Service (must be before InitializeAgents)
	// ---------------------------

	logger.Info("Creating chat service")
	// Get chat timeout: env var takes precedence, then config file, then default (60)
	chatTimeout := 60 // default
	if envTimeout := os.Getenv("STAFF_CHAT_TIMEOUT"); envTimeout != "" {
		if parsed, err := strconv.Atoi(envTimeout); err == nil && parsed > 0 {
			chatTimeout = parsed
		}
	} else if appConfig.ChatTimeout > 0 {
		chatTimeout = appConfig.ChatTimeout
	}
	chatService := ui.NewChatService(crew, db, chatTimeout, appConfig)

	// Initialize AgentRunners (after message persister is set)
	// Extract enabled providers from config (support both array and legacy string)
	logger.Info("Extracting enabled providers from config")
	enabledProviders := appConfig.LLMProviders
	if len(enabledProviders) == 0 {
		// TODO: remove default. this should be configured and enforced by the config file.
		enabledProviders = []string{"anthropic"} // Default
	}

	// Also check agents.yaml for llm_providers (takes precedence if present)
	if len(cfg.LLMProviders) > 0 {
		logger.Info("Using LLM providers from agents.yaml: %v", cfg.LLMProviders)
		enabledProviders = cfg.LLMProviders
	}

	// Validate at least one provider is enabled
	if len(enabledProviders) == 0 {
		log.Fatalf("No LLM providers enabled. Please configure llm_providers in config file or agents.yaml")
	}

	// Create provider config from appConfig
	providerConfig := llm.ProviderConfig{
		AnthropicAPIKey: config.LoadAnthropicConfig(appConfig),
	}
	ollamaHost, ollamaModel := config.LoadOllamaConfig(appConfig)
	providerConfig.OllamaHost = ollamaHost
	providerConfig.OllamaModel = ollamaModel
	openaiAPIKey, openaiBaseURL, openaiModel, openaiOrg := config.LoadOpenAIConfig(appConfig)
	providerConfig.OpenAIAPIKey = openaiAPIKey
	providerConfig.OpenAIBaseURL = openaiBaseURL
	providerConfig.OpenAIModel = openaiModel
	providerConfig.OpenAIOrg = openaiOrg

	// Create provider registry
	logger.Info("Creating provider registry with enabled providers: %v", enabledProviders)
	registry := llm.NewProviderRegistry(&providerConfig, enabledProviders)

	// Initialize agents with registry
	if err := crew.InitializeAgents(registry); err != nil {
		logger.Error("Failed to initialize agents: %v", err)
		log.Fatalf("Failed to initialize agents: %v", err)
	}

	logger.Info("Agents initialized successfully")

	// ---------------------------
	// 5. Start Background Scheduler
	// ---------------------------

	logger.Info("Starting background scheduler")
	// Create context for graceful shutdown
	schedulerCtx, cancelScheduler := context.WithCancel(context.Background())
	defer cancelScheduler()

	// Create scheduler with 15 second poll interval
	scheduler, err := runtime.NewScheduler(crew, crew.StateManager(), crew.StatsManager(), 15*time.Second)
	if err != nil {
		logger.Error("Failed to create scheduler: %v", err)
		log.Fatalf("Failed to create scheduler: %v", err)
	}

	// Start scheduler in background goroutine
	go scheduler.Start(schedulerCtx)
	logger.Info("Background scheduler goroutine started")

	// ---------------------------
	// 6. Create UI Service and TUI
	// ---------------------------

	logger.Info("Initializing UI")
	// Suppress console output to avoid interfering with TUI rendering
	logger.SetSuppressConsole(true)
	// Get theme: env var takes precedence, then config file, then default
	theme := os.Getenv("STAFF_THEME")
	if theme == "" {
		theme = appConfig.Theme
	}
	if theme == "" {
		theme = "solarized"
	}
	app := tui.NewAppWithTheme(chatService, theme)
	app.SetConfigPath(configPath)

	logger.Info("Starting terminal UI")
	if err := app.Run(); err != nil {
		logger.Error("Error running application: %v", err)
		fmt.Fprintf(os.Stderr, "Error running application: %v\n", err)
		os.Exit(1)
	}

	logger.Info("Application shutdown")
}
