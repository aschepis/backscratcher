package agent

import (
	"context"
	"database/sql"
	"fmt"
	"sync"

	anthropic "github.com/anthropics/anthropic-sdk-go"

	"github.com/aschepis/backscratcher/staff/tools"
)

type Crew struct {
	Agents       map[string]*AgentConfig
	Runners      map[string]*AgentRunner
	ToolRegistry *tools.Registry
	ToolProvider *ToolProviderFromRegistry
	stateManager *StateManager

	apiKey string
	mu     sync.RWMutex
}

type CrewConfig struct {
	Agents map[string]*AgentConfig `yaml:"agents" json:"agents"`
}

func NewCrew(apiKey string, db *sql.DB) *Crew {
	if db == nil {
		panic("database connection is required for Crew")
	}
	reg := tools.NewRegistry()
	provider := NewToolProvider(reg)
	stateManager := NewStateManager(db)

	return &Crew{
		Agents:       make(map[string]*AgentConfig),
		Runners:      make(map[string]*AgentRunner),
		ToolRegistry: reg,
		ToolProvider: provider,
		stateManager: stateManager,
		apiKey:       apiKey,
	}
}

// StateManager returns the state manager for this crew
func (c *Crew) StateManager() *StateManager {
	return c.stateManager
}

func (c *Crew) LoadCrewConfig(cfg CrewConfig) error {
	for id, agentCfg := range cfg.Agents {
		if agentCfg.ID == "" {
			agentCfg.ID = id
		}
		c.Agents[id] = agentCfg
	}
	return nil
}

func (c *Crew) InitializeAgents() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for id, cfg := range c.Agents {
		runner := NewAgentRunner(c.apiKey, NewAgent(id, cfg), c.ToolRegistry, c.ToolProvider, c.stateManager)
		c.Runners[id] = runner
		// Initialize agent state to idle if not exists
		exists, err := c.stateManager.StateExists(id)
		if err != nil {
			return fmt.Errorf("failed to check agent state for %s: %w", id, err)
		}
		if !exists {
			// Agent has no persisted state, initialize to idle
			if err := c.stateManager.SetState(id, StateIdle); err != nil {
				return fmt.Errorf("failed to initialize agent state for %s: %w", id, err)
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
