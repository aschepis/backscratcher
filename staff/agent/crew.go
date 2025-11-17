package agent

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"

	"github.com/aschepis/backscratcher/staff/logger"
	"github.com/aschepis/backscratcher/staff/mcp"
	"github.com/aschepis/backscratcher/staff/tools"
)

type Crew struct {
	Agents           map[string]*AgentConfig
	Runners          map[string]*AgentRunner
	ToolRegistry     *tools.Registry
	ToolProvider     *ToolProviderFromRegistry
	stateManager     *StateManager
	statsManager     *StatsManager
	messagePersister MessagePersister // Optional message persister

	MCPServers map[string]*MCPServerConfig
	MCPClients map[string]mcp.MCPClient

	apiKey string
	mu     sync.RWMutex
}

type CrewConfig struct {
	Agents     map[string]*AgentConfig     `yaml:"agents" json:"agents"`
	MCPServers map[string]*MCPServerConfig `yaml:"mcp_servers,omitempty" json:"mcp_servers,omitempty"`
}

func NewCrew(apiKey string, db *sql.DB) *Crew {
	if db == nil {
		panic("database connection is required for Crew")
	}
	reg := tools.NewRegistry()
	provider := NewToolProvider(reg)
	stateManager := NewStateManager(db)
	statsManager := NewStatsManager(db)

	return &Crew{
		Agents:       make(map[string]*AgentConfig),
		Runners:      make(map[string]*AgentRunner),
		ToolRegistry: reg,
		ToolProvider: provider,
		stateManager: stateManager,
		statsManager: statsManager,
		apiKey:       apiKey,
		MCPServers:   make(map[string]*MCPServerConfig),
		MCPClients:   make(map[string]mcp.MCPClient),
	}
}

// StateManager returns the state manager for this crew
func (c *Crew) StateManager() *StateManager {
	return c.stateManager
}

// StatsManager returns the stats manager for this crew
func (c *Crew) StatsManager() *StatsManager {
	return c.statsManager
}

// SetMessagePersister sets the message persister for this crew.
// All runners will use this persister to save conversation messages.
func (c *Crew) SetMessagePersister(persister MessagePersister) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.messagePersister = persister
	// Update existing runners
	for _, runner := range c.Runners {
		runner.messagePersister = persister
	}
}

func (c *Crew) LoadCrewConfig(cfg CrewConfig) error {
	for id, agentCfg := range cfg.Agents {
		if agentCfg.ID == "" {
			agentCfg.ID = id
		}
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

func (c *Crew) InitializeAgents() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for id, cfg := range c.Agents {
		// Skip disabled agents - they don't need runners or state initialization
		if cfg.Disabled {
			logger.Info("Agent %s: disabled, skipping initialization", id)
			continue
		}

		runner, err := NewAgentRunnerWithPersister(c.apiKey, NewAgent(id, cfg), c.ToolRegistry, c.ToolProvider, c.stateManager, c.statsManager, c.messagePersister)
		if err != nil {
			return fmt.Errorf("failed to create runner for agent %s: %w", id, err)
		}
		c.Runners[id] = runner
		// Initialize agent state to idle if not exists
		exists, err := c.stateManager.StateExists(id)
		if err != nil {
			return fmt.Errorf("failed to check agent state for %s: %w", id, err)
		}
		logger.Info("Agent %s: state exists=%v, startup_delay=%v", id, exists, cfg.StartupDelay)
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
				logger.Info("Agent %s: configured with startup_delay of %v, will wake at %v", id, delay, wakeTime.Format(time.RFC3339))
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
				logger.Info("Agent %s: setting state to waiting_external with next_wake=%v", id, nextWake.Format(time.RFC3339))
				if err := c.stateManager.SetStateWithNextWake(id, StateWaitingExternal, nextWake); err != nil {
					return fmt.Errorf("failed to initialize agent state with wake time for %s: %w", id, err)
				}
			} else {
				// Agent has no wake time, initialize to idle
				logger.Info("Agent %s: no wake time configured, setting state to idle", id)
				if err := c.stateManager.SetState(id, StateIdle); err != nil {
					return fmt.Errorf("failed to initialize agent state for %s: %w", id, err)
				}
			}
		} else {
			// Agent state already exists - check if we need to apply startup delay
			// Startup delay should apply on every app startup if the agent is idle or doesn't have a next_wake set
			if cfg.StartupDelay != "" {
				currentState, err := c.stateManager.GetState(id)
				if err != nil {
					return fmt.Errorf("failed to get state for agent %s: %w", id, err)
				}
				currentNextWake, err := c.stateManager.GetNextWake(id)
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
					logger.Info("Agent %s: applying startup_delay of %v (existing state=%s), will wake at %v", id, delay, currentState, wakeTime.Format(time.RFC3339))
					if err := c.stateManager.SetStateWithNextWake(id, StateWaitingExternal, &wakeTime); err != nil {
						return fmt.Errorf("failed to apply startup_delay for agent %s: %w", id, err)
					}
				} else {
					logger.Info("Agent %s: state exists, skipping startup_delay (state=%s, next_wake=%v)", id, currentState, currentNextWake)
				}
			}
		}
	}
	return nil
}

func (c *Crew) Run(
	ctx context.Context,
	agentID string,
	threadID string,
	userMessage string,
	history []anthropic.MessageParam,
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
	history []anthropic.MessageParam,
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

	out := make([]*Agent, 0, len(c.Runners))
	for _, runner := range c.Runners {
		out = append(out, runner.agent)
	}
	return out
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
func (c *Crew) GetAgents() map[string]*AgentConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make(map[string]*AgentConfig)
	for id, cfg := range c.Agents {
		result[id] = cfg
	}
	return result
}

// GetMCPServers returns a copy of all MCP server configs
func (c *Crew) GetMCPServers() map[string]*MCPServerConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make(map[string]*MCPServerConfig)
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
