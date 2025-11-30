package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/aschepis/backscratcher/staff/agent"
	"github.com/aschepis/backscratcher/staff/config"
	"github.com/aschepis/backscratcher/staff/conversations"
	"github.com/aschepis/backscratcher/staff/llm"
	stafflogger "github.com/aschepis/backscratcher/staff/logger"
	"github.com/aschepis/backscratcher/staff/mcp"
	"github.com/aschepis/backscratcher/staff/memory"
	"github.com/aschepis/backscratcher/staff/memory/ollama"
	"github.com/aschepis/backscratcher/staff/migrations"
	"github.com/aschepis/backscratcher/staff/runtime"
	"github.com/aschepis/backscratcher/staff/server"
	"github.com/aschepis/backscratcher/staff/tools"
	"github.com/aschepis/backscratcher/staff/tools/schemas"
	"github.com/aschepis/backscratcher/staff/ui"
	_ "github.com/mattn/go-sqlite3"
	"github.com/rs/zerolog"
)

const (
	defaultSocketPath = "/tmp/staffd.sock"
)

// TODO: this feels like it shouldn't exist. Look into refactoring this out.
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

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Parse command-line flags
	var (
		socketPath = flag.String("socket", defaultSocketPath, "Unix socket path for gRPC server")
		tcpAddress = flag.String("tcp", "", "TCP address to listen on (e.g., localhost:50051). If set, disables Unix socket")
		logFile    = flag.String("logfile", "", "Path to log file. If not set, logs to stdout/stderr")
		pretty     = flag.Bool("pretty", false, "Use pretty console output (only valid when logfile is not set)")
		dbPath     = flag.String("db", "staff_memory.db", "Path to SQLite database file")
	)
	flag.Parse()

	// Validate that --logfile and --pretty are mutually exclusive
	if *logFile != "" && *pretty {
		return fmt.Errorf("--logfile and --pretty are mutually exclusive")
	}

	// Initialize logger with options
	logger, err := stafflogger.InitWithOptions(*logFile, *pretty)
	if err != nil {
		return fmt.Errorf("failed to initialize logger: %w", err)
	}

	logger.Info().
		Str("socket", *socketPath).
		Str("tcp", *tcpAddress).
		Str("db", *dbPath).
		Msg("staffd starting")

	// Load unified configuration
	configPath := config.GetConfigPath()
	appConfig, err := config.LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}
	logger.Info().Msg("Loaded unified configuration")

	// Get Anthropic API key from config file
	anthropicAPIKey := appConfig.Anthropic.APIKey
	if anthropicAPIKey == "" {
		return fmt.Errorf("missing anthropic.api_key in config file")
	}

	// ---------------------------
	// 1. Open SQLite + Memory Store
	// ---------------------------

	logger.Info().Str("path", *dbPath).Msg("Initializing database and memory store")
	db, err := sql.Open("sqlite3", *dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer db.Close() //nolint:errcheck // No remedy for db close errors

	// Run database migrations
	if err := migrations.RunMigrations(db, "./migrations", logger); err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	embedder, err := ollama.NewEmbedder(ollama.ModelMXBAI)
	if err != nil {
		return fmt.Errorf("failed to create ollama embedder: %w", err)
	}

	memoryStore, err := memory.NewStore(db, embedder, logger)
	if err != nil {
		return fmt.Errorf("failed to create memory store: %w", err)
	}

	memoryRouter := memory.NewMemoryRouter(memoryStore, memory.Config{
		Summarizer: memory.NewAnthropicSummarizer("claude-3.5-haiku-latest", anthropicAPIKey, 256, logger),
	}, logger)

	// Create conversations store for message persistence
	conversationStore := conversations.NewStore(db)

	// ---------------------------
	// 2. Create Message Summarizer (if enabled)
	// ---------------------------

	var messageSummarizer *agent.MessageSummarizer
	if !appConfig.MessageSummarization.Disabled {
		logger.Info().
			Str("model", appConfig.MessageSummarization.Model).
			Msg("Message summarization is enabled, initializing Ollama summarizer")
		summarizerConfig := agent.MessageSummarizerConfig{
			Model:         appConfig.MessageSummarization.Model,
			MaxChars:      appConfig.MessageSummarization.MaxChars,
			MaxLines:      appConfig.MessageSummarization.MaxLines,
			MaxLineBreaks: appConfig.MessageSummarization.MaxLineBreaks,
		}
		messageSummarizer, err = agent.NewMessageSummarizer(summarizerConfig, logger)
		if err != nil {
			return fmt.Errorf("failed to create message summarizer: %w", err)
		}
		if messageSummarizer != nil {
			logger.Info().Msg("Message summarizer initialized successfully")
		}
	} else {
		logger.Info().Msg("Message summarization is disabled")
	}

	// ---------------------------
	// 3. Create Crew + Shared Tools
	// ---------------------------

	logger.Info().Msg("Creating crew and registering tools")
	crewOpts := []agent.CrewOption{
		agent.WithMessagePersister(conversationStore),
	}
	if messageSummarizer != nil {
		crewOpts = append(crewOpts, agent.WithMessageSummarizer(messageSummarizer))
	}
	crew := agent.NewCrew(logger, anthropicAPIKey, db, crewOpts...)

	// Get workspace path (default to current directory)
	workspacePath, err := os.Getwd()
	if err != nil {
		workspacePath = "."
		logger.Warn().Err(err).Msg("Failed to get current directory, using '.' as workspace")
	}

	// Register all tools (handlers and schemas)
	registerAllTools(logger, crew, memoryRouter, workspacePath, db, crew.StateManager, anthropicAPIKey)

	// Load crew config from unified config
	if err := crew.LoadCrewConfig(appConfig); err != nil {
		return fmt.Errorf("failed to load crew config: %w", err)
	}

	// Load and merge Claude MCP servers if enabled
	if appConfig.ClaudeMCP.Enabled {
		loadClaudeMCPServers(logger, appConfig)
	}

	// Register MCP servers and their tools
	logger.Info().
		Int("count", len(appConfig.MCPServers)).
		Msg("Starting MCP server registration")
	registerMCPServers(logger, crew, appConfig.MCPServers)

	// ---------------------------
	// 4. Create Chat Service
	// ---------------------------

	logger.Info().Msg("Creating chat service")
	chatTimeout := 60 // default
	if envTimeout := os.Getenv("STAFF_CHAT_TIMEOUT"); envTimeout != "" {
		if parsed, err := strconv.Atoi(envTimeout); err == nil && parsed > 0 {
			chatTimeout = parsed
		}
	} else if appConfig.ChatTimeout > 0 {
		chatTimeout = appConfig.ChatTimeout
	}
	chatService := ui.NewChatService(logger, crew, db, conversationStore, chatTimeout, appConfig)

	// ---------------------------
	// 5. Initialize Agent Runners
	// ---------------------------

	logger.Info().Msg("Initializing agent runners")
	enabledProviders := appConfig.LLMProviders
	if len(enabledProviders) == 0 {
		enabledProviders = []string{"anthropic"} // Default
	}

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

	registry := llm.NewProviderRegistry(&providerConfig, enabledProviders)
	if err := crew.InitializeAgents(registry); err != nil {
		return fmt.Errorf("failed to initialize agents: %w", err)
	}
	logger.Info().Msg("Agents initialized successfully")

	// ---------------------------
	// 6. Start Background Scheduler
	// ---------------------------

	schedulerCtx, cancelScheduler := context.WithCancel(context.Background())
	defer cancelScheduler()

	scheduler, err := runtime.NewScheduler(crew, crew.StateManager, crew.StatsManager, 15*time.Second, logger)
	if err != nil {
		return fmt.Errorf("failed to create scheduler: %w", err)
	}
	go scheduler.Start(schedulerCtx)
	logger.Info().Msg("Background scheduler started")

	// ---------------------------
	// 7. Create and Start gRPC Server
	// ---------------------------

	// Determine socket path to use
	listenPath := *socketPath
	if *tcpAddress != "" {
		listenPath = *tcpAddress
	}

	srv := server.New(server.Config{
		SocketPath: listenPath,
		Logger:     logger,
	}, crew, db, memoryRouter, memoryStore, chatService)

	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start server in goroutine
	serverErr := make(chan error, 1)
	go func() {
		var err error
		if *tcpAddress != "" {
			logger.Info().Str("address", *tcpAddress).Msg("Starting gRPC server on TCP")
			err = srv.ServeTCP(*tcpAddress)
		} else {
			// Remove existing socket file if it exists
			if err := os.Remove(*socketPath); err != nil && !os.IsNotExist(err) {
				logger.Warn().Err(err).Str("socket", *socketPath).Msg("Failed to remove existing socket file")
			}
			logger.Info().Str("socket", *socketPath).Msg("Starting gRPC server on Unix socket")
			err = srv.ServeUnix(*socketPath)
		}
		serverErr <- err
	}()

	// Wait for shutdown signal or server error
	select {
	case sig := <-sigChan:
		logger.Info().Str("signal", sig.String()).Msg("Received shutdown signal")
		cancelScheduler()
		srv.GracefulStop()
	case err := <-serverErr:
		if err != nil {
			return fmt.Errorf("server error: %w", err)
		}
	}

	// Cleanup socket file on shutdown
	if *tcpAddress == "" {
		if err := os.Remove(*socketPath); err != nil && !os.IsNotExist(err) {
			logger.Warn().Err(err).Str("socket", *socketPath).Msg("Failed to remove socket file on shutdown")
		}
	}

	logger.Info().Msg("staffd shutdown complete")
	return nil
}

// registerToolSchemas registers all tool schemas with the ToolProvider.
func registerToolSchemas(logger zerolog.Logger, crew *agent.Crew) {
	allSchemas := schemas.All()
	for name, schema := range allSchemas {
		crew.ToolProvider.RegisterSchema(name, agent.ToolSchema{
			Description: schema.Description,
			Schema:      schema.Schema,
		})
	}
	logger.Info().Int("count", len(allSchemas)).Msg("Registered tool schemas")
}

// registerToolHandlers registers all tool handlers with the ToolRegistry.
func registerToolHandlers(crew *agent.Crew, memoryRouter *memory.MemoryRouter, workspacePath string, db *sql.DB, stateManager *agent.StateManager, apiKey string) {
	crew.ToolRegistry.RegisterMemoryTools(memoryRouter, apiKey)
	crew.ToolRegistry.RegisterFilesystemTools(workspacePath)
	crew.ToolRegistry.RegisterSystemTools(workspacePath)
	crew.ToolRegistry.RegisterNotificationTools(db, func(agentID string, state string) error {
		return stateManager.SetState(agentID, agent.State(state))
	})

	staffData := tools.StaffToolsData{
		GetAgents: func() map[string]tools.AgentConfigData {
			result := make(map[string]tools.AgentConfigData)
			agents := crew.GetAgents()
			for id, cfg := range agents {
				result[id] = tools.AgentConfigData{
					ID:           cfg.ID,
					Name:         cfg.Name,
					System:       cfg.System,
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
			state, err := crew.StateManager.GetState(agentID)
			if err != nil {
				return "", nil, err
			}
			nextWake, err := crew.StateManager.GetNextWake(agentID)
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
			states, err := crew.StateManager.GetAllStates()
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
			nextWake, err := crew.StateManager.GetNextWake(agentID)
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
			return crew.StatsManager.GetStats(agentID)
		},
		GetAllStats: func() ([]map[string]interface{}, error) {
			return crew.StatsManager.GetAllStats()
		},
		GetAllToolSchemas: func() map[string]tools.ToolSchemaData {
			toolSchemas := crew.ToolProvider.GetAllSchemas()
			result := make(map[string]tools.ToolSchemaData)
			for name, schema := range toolSchemas {
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
}

// registerAllTools registers all tool handlers and schemas with the crew.
func registerAllTools(logger zerolog.Logger, crew *agent.Crew, memoryRouter *memory.MemoryRouter, workspacePath string, db *sql.DB, stateManager *agent.StateManager, apiKey string) {
	registerToolHandlers(crew, memoryRouter, workspacePath, db, stateManager, apiKey)
	registerToolSchemas(logger, crew)
}

// registerMCPServers discovers and registers tools from MCP servers.
func registerMCPServers(logger zerolog.Logger, crew *agent.Crew, servers map[string]*config.MCPServerConfig) {
	if len(servers) == 0 {
		logger.Info().Msg("No MCP servers configured")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	adapter := mcp.NewNameAdapter()

	for serverName, serverConfig := range servers {
		if serverConfig == nil {
			logger.Warn().Str("name", serverName).Msg("MCP server has nil config, skipping")
			continue
		}

		var mcpClient mcp.MCPClient
		var err error

		switch {
		case serverConfig.Command != "":
			mcpClient, err = mcp.NewStdioMCPClient(logger, serverConfig.Command, serverConfig.ConfigFile, serverConfig.Args, serverConfig.Env)
			if err != nil {
				logger.Error().Str("name", serverName).Err(err).Msg("Failed to create STDIO MCP client")
				continue
			}
		case serverConfig.URL != "":
			mcpClient, err = mcp.NewHttpMCPClient(logger, serverConfig.URL, serverConfig.ConfigFile)
			if err != nil {
				logger.Error().Str("name", serverName).Err(err).Msg("Failed to create HTTP MCP client")
				continue
			}
		default:
			logger.Warn().Str("name", serverName).Msg("MCP server has neither command nor url, skipping")
			continue
		}

		if err := mcpClient.Start(ctx); err != nil {
			logger.Error().Str("name", serverName).Err(err).Msg("Failed to start MCP client")
			_ = mcpClient.Close()
			continue
		}

		mcpTools, err := mcpClient.ListTools(ctx)
		if err != nil {
			logger.Error().Str("name", serverName).Err(err).Msg("Failed to list tools from MCP server")
			_ = mcpClient.Close()
			continue
		}

		logger.Info().Int("count", len(mcpTools)).Str("name", serverName).Msg("Discovered tools from MCP server")

		for _, tool := range mcpTools {
			originalName := tool.Name
			safeName := adapter.GetSafeName(originalName)

			crew.ToolRegistry.RegisterMCPTool(safeName, originalName, mcpClient)

			var schema map[string]any
			if tool.InputSchema != nil {
				schema = tool.InputSchema
			} else {
				schema = map[string]any{
					"type":       "object",
					"properties": make(map[string]any),
				}
			}

			crew.ToolProvider.RegisterSchemaWithServer(safeName, agent.ToolSchema{
				Description: tool.Description,
				Schema:      schema,
			}, serverName)
		}

		crew.MCPServers[serverName] = serverConfig
		crew.MCPClients[serverName] = mcpClient
		logger.Info().Str("name", serverName).Msg("Completed registration for MCP server")
	}
}

// loadClaudeMCPServers loads MCP servers from Claude configuration.
func loadClaudeMCPServers(logger zerolog.Logger, appConfig *config.Config) {
	logger.Info().Msg("Claude MCP integration is enabled, loading Claude MCP servers")

	claudeConfigPath := appConfig.ClaudeMCP.ConfigPath
	if claudeConfigPath == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			logger.Warn().Err(err).Msg("Failed to get home directory for Claude config, skipping")
			return
		}
		claudeConfigPath = filepath.Join(homeDir, ".claude.json")
	}

	claudeConfig, err := config.LoadClaudeConfig(logger, claudeConfigPath)
	if err != nil {
		logger.Warn().Str("path", claudeConfigPath).Err(err).Msg("Failed to load Claude config")
		return
	}

	claudeServers, _ := config.ExtractMCPServersFromProjects(logger, claudeConfig, appConfig.ClaudeMCP.Projects)
	if len(claudeServers) == 0 {
		logger.Info().Msg("No Claude MCP servers found")
		return
	}

	mappedServers := config.MapClaudeToMCPServerConfig(logger, claudeServers)
	addedCount := 0
	for name, serverCfg := range mappedServers {
		if _, exists := appConfig.MCPServers[name]; !exists {
			appConfig.MCPServers[name] = serverCfg
			addedCount++
		}
	}
	logger.Info().Int("added", addedCount).Msg("Merged Claude MCP servers")
}
