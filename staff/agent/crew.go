package agent

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/aschepis/backscratcher/staff/config"
	"github.com/aschepis/backscratcher/staff/llm"
	llmanthropic "github.com/aschepis/backscratcher/staff/llm/anthropic"
	llmollama "github.com/aschepis/backscratcher/staff/llm/ollama"
	llmopenai "github.com/aschepis/backscratcher/staff/llm/openai"
	"github.com/aschepis/backscratcher/staff/mcp"
	"github.com/aschepis/backscratcher/staff/tools"
	"github.com/rs/zerolog"
	"github.com/samber/lo"
)

type Crew struct {
	Agents            map[string]*config.AgentConfig
	Runners           map[string]*AgentRunner
	ToolRegistry      *tools.Registry
	ToolProvider      *ToolProviderFromRegistry
	StateManager      *StateManager
	StatsManager      *StatsManager
	messagePersister  MessagePersister   // Optional message persister
	messageSummarizer *MessageSummarizer // Optional message summarizer

	MCPServers map[string]*config.MCPServerConfig
	MCPClients map[string]mcp.MCPClient

	logger zerolog.Logger

	apiKey      string
	clientCache map[string]llm.Client // Cache for LLM clients by ClientKey
	mu          sync.RWMutex
}

// CrewOption is a functional option for configuring a Crew.
type CrewOption func(*Crew)

// WithMessagePersister sets the message persister for the crew.
func WithMessagePersister(persister MessagePersister) CrewOption {
	return func(c *Crew) {
		c.messagePersister = persister
	}
}

// WithMessageSummarizer sets the message summarizer for the crew.
func WithMessageSummarizer(summarizer *MessageSummarizer) CrewOption {
	return func(c *Crew) {
		c.messageSummarizer = summarizer
	}
}

func NewCrew(logger zerolog.Logger, apiKey string, db *sql.DB, opts ...CrewOption) *Crew {
	if db == nil {
		panic("database connection is required for Crew")
	}
	reg := tools.NewRegistry(logger)
	provider := NewToolProvider(reg, logger)
	stateManager := NewStateManager(logger, db)
	statsManager := NewStatsManager(logger, db)

	c := &Crew{
		Agents:       make(map[string]*config.AgentConfig),
		Runners:      make(map[string]*AgentRunner),
		ToolRegistry: reg,
		ToolProvider: provider,
		StateManager: stateManager,
		StatsManager: statsManager,
		apiKey:       apiKey,
		clientCache:  make(map[string]llm.Client),
		MCPServers:   make(map[string]*config.MCPServerConfig),
		MCPClients:   make(map[string]mcp.MCPClient),
		logger:       logger.With().Str("component", "crew").Logger(),
	}

	// Apply options
	for _, opt := range opts {
		opt(c)
	}

	return c
}

// GetToolProvider returns the tool provider for this crew
func (c *Crew) GetToolProvider() *ToolProviderFromRegistry {
	return c.ToolProvider
}

// LoadCrewConfig loads crew configuration from the unified config.
func (c *Crew) LoadCrewConfig(cfg *config.Config) error {
	// Load agents
	for id, agentCfg := range cfg.Agents {
		if agentCfg.ID == "" {
			agentCfg.ID = id
		}
		// AgentConfig is now a type alias to config.AgentConfig, so we can use it directly
		c.Agents[id] = agentCfg
	}

	// Store MCP server configs
	c.mu.Lock()
	defer c.mu.Unlock()
	for name, serverCfg := range cfg.MCPServers {
		c.MCPServers[name] = serverCfg
	}
	return nil
}

func (c *Crew) InitializeAgents(registry *llm.ProviderRegistry) error {
	c.logger.Info().Msg("Initializing agents")

	// Get a copy of agents to iterate over (to avoid holding lock during client creation)
	c.mu.RLock()
	agentsCopy := make(map[string]*config.AgentConfig)
	for id, cfg := range c.Agents {
		agentsCopy[id] = cfg
	}
	c.mu.RUnlock()

	for id, cfg := range agentsCopy {
		c.logger.Info().Msgf("Initializing agent %s", id)
		// Skip disabled agents - they don't need runners or state initialization
		if cfg.Disabled {
			c.logger.Info().Msgf("Agent %s: disabled, skipping initialization", id)
			continue
		}

		// Convert agent config to registry format
		agentLLMConfig := llm.AgentLLMConfig{
			LLMPreferences: make([]llm.LLMPreference, len(cfg.LLM)),
		}
		for i, pref := range cfg.LLM {
			agentLLMConfig.LLMPreferences[i] = llm.LLMPreference{
				Provider:    pref.Provider,
				Model:       pref.Model,
				Temperature: pref.Temperature,
				APIKeyRef:   pref.APIKeyRef,
			}
		}

		// Resolve LLM configuration using preference-based selection
		c.logger.Info().Msgf("Resolving LLM configuration for agent %s", id)
		clientKey, err := registry.ResolveAgentLLMConfig(id, agentLLMConfig)
		if err != nil {
			return fmt.Errorf("failed to resolve LLM config for agent %s: %w", id, err)
		}

		// Get or create LLM client (with caching) - this may take time, so don't hold lock
		c.logger.Debug().Msgf("Getting or creating LLM client for agent %s", id)
		llmClient, err := c.getOrCreateClient(clientKey, id, cfg)
		if err != nil {
			return fmt.Errorf("failed to create LLM client for agent %s: %w", id, err)
		}

		c.logger.Info().Msgf("Creating agent runner for agent %s", id)
		runner, err := NewAgentRunner(c.logger, llmClient, NewAgent(id, cfg), clientKey.Model, clientKey.Provider, c.ToolRegistry, c.ToolProvider, c.StateManager, c.StatsManager, c.messagePersister, c.messageSummarizer)
		if err != nil {
			return fmt.Errorf("failed to create runner for agent %s: %w", id, err)
		}

		// Now acquire lock only to store the runner
		c.mu.Lock()
		c.Runners[id] = runner
		c.mu.Unlock()
		// Initialize agent state to idle if not exists
		exists, err := c.StateManager.StateExists(id)
		if err != nil {
			return fmt.Errorf("failed to check agent state for %s: %w", id, err)
		}
		c.logger.Debug().Msgf("Agent %s: state exists=%v, startup_delay=%v", id, exists, cfg.StartupDelay)
		if !exists {
			now := time.Now()
			var nextWake *time.Time
			var hasWakeTime bool

			// Check for startup delay first (one-time delay after app launch)
			if cfg.StartupDelay != "" {
				delay, err := time.ParseDuration(cfg.StartupDelay)
				if err != nil {
					return fmt.Errorf("failed to parse startup_delay for agent %s: %w", id, err)
				}
				wakeTime := now.Add(delay)
				nextWake = &wakeTime
				hasWakeTime = true
				c.logger.Debug().Msgf("Agent %s: configured with startup_delay of %v, will wake at %d (%s)", id, delay, wakeTime.Unix(), wakeTime.Format("2006-01-02 15:04:05"))
			}

			// Check if agent has a schedule and is not disabled
			// Default Disabled to false (agent is enabled by default)
			hasSchedule := cfg.Schedule != ""
			// Agent is enabled by default (Disabled defaults to false)
			enabled := hasSchedule && !cfg.Disabled

			if enabled {
				// Agent has a schedule and is enabled, compute initial next_wake
				scheduledNextWake, err := ComputeNextWake(cfg.Schedule, now)
				if err != nil {
					return fmt.Errorf("failed to compute next wake for agent %s: %w", id, err)
				}

				// If we have a startup delay, use whichever comes first
				if hasWakeTime {
					if scheduledNextWake.Before(*nextWake) {
						nextWake = &scheduledNextWake
					}
				} else {
					nextWake = &scheduledNextWake
					hasWakeTime = true
				}
			}

			if hasWakeTime {
				// Agent has a wake time (from startup delay or schedule), set state to waiting_external
				c.logger.Info().Msgf("Agent %s: setting state to waiting_external with next_wake=%d (%s)", id, nextWake.Unix(), nextWake.Format("2006-01-02 15:04:05"))
				if err := c.StateManager.SetStateWithNextWake(id, StateWaitingExternal, nextWake); err != nil {
					return fmt.Errorf("failed to initialize agent state with wake time for %s: %w", id, err)
				}
			} else {
				// Agent has no wake time, initialize to idle
				c.logger.Info().Msgf("Agent %s: no wake time configured, setting state to idle", id)
				if err := c.StateManager.SetState(id, StateIdle); err != nil {
					return fmt.Errorf("failed to initialize agent state for %s: %w", id, err)
				}
			}
		} else if cfg.StartupDelay != "" {
			// Agent state already exists - check if we need to apply startup delay
			// Startup delay should apply on every app startup if the agent is idle or doesn't have a next_wake set
			currentState, err := c.StateManager.GetState(id)
			if err != nil {
				return fmt.Errorf("failed to get state for agent %s: %w", id, err)
			}
			currentNextWake, err := c.StateManager.GetNextWake(id)
			if err != nil {
				return fmt.Errorf("failed to get next_wake for agent %s: %w", id, err)
			}

			// Apply startup delay if agent is idle and has no next_wake, or if next_wake is in the past
			shouldApplyDelay := (currentState == StateIdle && currentNextWake == nil) ||
				(currentNextWake != nil && currentNextWake.Before(time.Now()))

			if shouldApplyDelay {
				delay, err := time.ParseDuration(cfg.StartupDelay)
				if err != nil {
					return fmt.Errorf("failed to parse startup_delay for agent %s: %w", id, err)
				}
				now := time.Now()
				wakeTime := now.Add(delay)
				c.logger.Info().Msgf("Agent %s: applying startup_delay of %v (existing state=%s), will wake at %d (%s)", id, delay, currentState, wakeTime.Unix(), wakeTime.Format("2006-01-02 15:04:05"))
				if err := c.StateManager.SetStateWithNextWake(id, StateWaitingExternal, &wakeTime); err != nil {
					return fmt.Errorf("failed to apply startup_delay for agent %s: %w", id, err)
				}
			} else {
				var nextWakeStr string
				if currentNextWake != nil {
					nextWakeStr = fmt.Sprintf("%d (%s)", currentNextWake.Unix(), currentNextWake.Format("2006-01-02 15:04:05"))
				} else {
					nextWakeStr = "nil"
				}
				c.logger.Debug().Msgf("Agent %s: state exists, skipping startup_delay (state=%s, next_wake=%s)", id, currentState, nextWakeStr)
			}
		}
	}
	return nil
}

// Run executes a single turn for an agent with the given history.
// History is provided as provider-neutral llm.Message types to avoid leaking SDK types.
func (c *Crew) Run(
	ctx context.Context,
	agentID string,
	threadID string,
	userMessage string,
	history []llm.Message,
) (string, error) {
	c.mu.RLock()
	agent := c.Agents[agentID]
	runner := c.Runners[agentID]
	c.mu.RUnlock()

	if agent == nil || runner == nil {
		return "", fmt.Errorf("agent %q not found or not initialized", agentID)
	}

	return runner.RunAgent(ctx, threadID, userMessage, history)
}

// StreamCallback is called for each text delta received from the streaming API
type StreamCallback func(text string) error

// DebugCallback is called for debug information (tool invocations, API calls, etc.)
type DebugCallback func(message string)

// RunStream executes a single turn for an agent with streaming support.
// debugCallback should be added to context using WithDebugCallback if needed.
func (c *Crew) RunStream(
	ctx context.Context,
	agentID string,
	threadID string,
	userMessage string,
	history []llm.Message,
	callback StreamCallback,
) (string, error) {
	c.mu.RLock()
	runner := c.Runners[agentID]
	c.mu.RUnlock()

	if runner == nil {
		return "", fmt.Errorf("agent %q not found or not initialized", agentID)
	}

	return runner.RunAgentStream(ctx, threadID, userMessage, history, callback)
}

func (c *Crew) Stats() map[string]any {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return map[string]any{
		"agent_count": len(c.Runners),
	}
}

func (c *Crew) ListAgents() []*Agent {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return lo.Map(lo.Values(c.Runners), func(runner *AgentRunner, _ int) *Agent {
		return runner.agent
	})
}

// IsAgentDisabled checks if an agent is disabled
func (c *Crew) IsAgentDisabled(agentID string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	agent, ok := c.Agents[agentID]
	if !ok {
		return true // If agent doesn't exist, consider it disabled
	}
	return agent.Disabled
}

// GetAgents returns a copy of all agent configs
func (c *Crew) GetAgents() map[string]*config.AgentConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make(map[string]*config.AgentConfig)
	for id, cfg := range c.Agents {
		result[id] = cfg
	}
	return result
}

// GetMCPServers returns a copy of all MCP server configs
func (c *Crew) GetMCPServers() map[string]*config.MCPServerConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make(map[string]*config.MCPServerConfig)
	for name, cfg := range c.MCPServers {
		result[name] = cfg
	}
	return result
}

// GetMCPClients returns a copy of all MCP clients
func (c *Crew) GetMCPClients() map[string]mcp.MCPClient {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make(map[string]mcp.MCPClient)
	for name, client := range c.MCPClients {
		result[name] = client
	}
	return result
}

// GetRunner returns the runner for a specific agent ID.
func (c *Crew) GetRunner(agentID string) *AgentRunner {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Runners[agentID]
}

// GetResolvedLLMInfo returns the resolved provider and model for an agent.
// This is the authoritative source for LLM information - it uses the runner if available,
// otherwise falls back to config-based resolution.
func (c *Crew) GetResolvedLLMInfo(agentID string) ResolvedLLMInfo {
	// Try to get from runner first (most accurate)
	runner := c.GetRunner(agentID)
	if runner != nil {
		return ResolvedLLMInfo{
			Provider: runner.GetResolvedProvider(),
			Model:    runner.GetResolvedModel(),
		}
	}

	// Runner not available (e.g., agent disabled or not initialized)
	// Fall back to config-based resolution
	c.mu.RLock()
	cfg, ok := c.Agents[agentID]
	c.mu.RUnlock()

	if !ok {
		// Agent not found, return defaults
		return ResolvedLLMInfo{
			Provider: llm.ProviderAnthropic,
			Model:    "",
		}
	}

	return ResolveLLMFromConfig(cfg)
}

// GetAgentInfos returns complete information for all agents.
// This is the authoritative source for agent information, combining config with resolved LLM info.
func (c *Crew) GetAgentInfos() []*AgentInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()

	infos := lo.Map(lo.Values(c.Runners), func(runner *AgentRunner, _ int) *AgentInfo {
		ag := runner.agent
		return c.buildAgentInfoLocked(ag)
	})
	return infos
}

// GetAgentInfo returns complete information for a specific agent.
// Returns nil if the agent is not found.
func (c *Crew) GetAgentInfo(agentID string) (*AgentInfo, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	runner, ok := c.Runners[agentID]
	if !ok {
		return nil, fmt.Errorf("agent %q not found or not initialized", agentID)
	}

	return c.buildAgentInfoLocked(runner.agent), nil
}

// buildAgentInfoLocked builds an AgentInfo from an Agent.
// Caller must hold the read lock.
func (c *Crew) buildAgentInfoLocked(ag *Agent) *AgentInfo {
	cfg := ag.Config

	// Get resolved LLM info - try runner first, then config
	// TODO: do we really need a fallback to config or will there always be
	// a runner? If a runner is not guaranteed, why not just use the config only?
	var provider, model string
	if runner, ok := c.Runners[ag.ID]; ok {
		provider = runner.GetResolvedProvider()
		model = runner.GetResolvedModel()
	} else {
		llmInfo := ResolveLLMFromConfig(cfg)
		provider = llmInfo.Provider
		model = llmInfo.Model
	}

	return &AgentInfo{
		ID:           ag.ID,
		Name:         cfg.Name,
		Model:        model,
		Provider:     provider,
		Tools:        cfg.Tools,
		Schedule:     cfg.Schedule,
		Disabled:     cfg.Disabled,
		SystemPrompt: cfg.System,
		MaxTokens:    cfg.MaxTokens,
	}
}

// getOrCreateClient gets or creates an LLM client for the given ClientKey with caching.
// Clients are cached by ClientKey string representation to avoid creating duplicate clients.
func (c *Crew) getOrCreateClient(key *llm.ClientKey, agentID string, agentConfig *config.AgentConfig) (llm.Client, error) {
	// Create cache key from ClientKey
	keyStr := fmt.Sprintf("%s:%s:%s:%s:%s:%s", key.Provider, key.Model, key.APIKey, key.Host, key.BaseURL, key.Organization)

	// Check cache first with read lock
	c.logger.Info().Msgf("Checking cache for client %s", keyStr)
	c.mu.RLock()
	if client, ok := c.clientCache[keyStr]; ok {
		c.mu.RUnlock()
		// Client found in cache, but we still need to wrap with agent-specific middleware
		return c.wrapClientWithMiddleware(client, agentID, agentConfig), nil
	}
	c.mu.RUnlock()

	// Not in cache - create new base client (no lock held during creation)
	var baseClient llm.Client
	var err error

	switch key.Provider {
	case llm.ProviderAnthropic:
		if key.APIKey == "" {
			return nil, fmt.Errorf("anthropic API key is required")
		}
		baseClient, err = llmanthropic.NewAnthropicClient(key.APIKey, c.logger)
		if err != nil {
			return nil, fmt.Errorf("failed to create anthropic client: %w", err)
		}

	case llm.ProviderOllama:
		baseClient, err = llmollama.NewOllamaClient(key.Host, key.Model)
		if err != nil {
			return nil, fmt.Errorf("failed to create ollama client: %w", err)
		}

	case llm.ProviderOpenAI:
		if key.APIKey == "" {
			return nil, fmt.Errorf("openai API key is required")
		}
		baseClient, err = llmopenai.NewOpenAIClient(key.APIKey, key.BaseURL, key.Model, key.Organization)
		if err != nil {
			return nil, fmt.Errorf("failed to create openai client: %w", err)
		}

	default:
		return nil, fmt.Errorf("unknown provider: %s", key.Provider)
	}

	// Cache the base client using double-checked locking pattern
	c.mu.Lock()
	// Double-check: another goroutine might have created it while we were creating
	if existingClient, ok := c.clientCache[keyStr]; ok {
		c.mu.Unlock()
		// Use the existing client instead
		return c.wrapClientWithMiddleware(existingClient, agentID, agentConfig), nil
	}
	c.clientCache[keyStr] = baseClient
	c.mu.Unlock()

	// Wrap with agent-specific middleware
	return c.wrapClientWithMiddleware(baseClient, agentID, agentConfig), nil
}

// wrapClientWithMiddleware wraps a base client with agent-specific middleware.
func (c *Crew) wrapClientWithMiddleware(baseClient llm.Client, agentID string, agentConfig *config.AgentConfig) llm.Client {
	// Create middleware
	var middleware []llm.Middleware

	// Add rate limit middleware
	rateLimitHandler := NewRateLimitHandler(c.logger, c.StateManager, func(agentID string, retryAfter time.Duration, attempt int) error {
		c.logger.Info().Msgf("Rate limit callback: agent %s will retry after %v (attempt %d)", agentID, retryAfter, attempt)
		return nil
	})
	rateLimitMw := NewRateLimitMiddleware(c.logger, rateLimitHandler, agentID, agentConfig)
	middleware = append(middleware, rateLimitMw)

	// Add compression middleware if dependencies are provided
	if c.messagePersister != nil && c.messageSummarizer != nil {
		compressionMw := NewCompressionMiddleware(
			c.logger,
			c.messagePersister,
			c.messageSummarizer,
			agentID,
			agentConfig.System,
		)
		middleware = append(middleware, compressionMw)
	}

	// Wrap client with middleware
	if len(middleware) > 0 {
		return llm.WrapWithMiddleware(baseClient, middleware...)
	}

	return baseClient
}
