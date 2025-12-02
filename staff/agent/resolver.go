package agent

import (
	"github.com/aschepis/backscratcher/staff/config"
	"github.com/aschepis/backscratcher/staff/llm"
)

// AgentInfo contains complete information about an agent.
// This is the authoritative domain struct for agent information,
// used by both the gRPC server and UI service.
type AgentInfo struct {
	ID           string
	Name         string
	Model        string
	Provider     string
	Tools        []string
	Schedule     string
	Disabled     bool
	SystemPrompt string
	MaxTokens    int64
}

// ResolvedLLMInfo contains the resolved provider and model for an agent.
type ResolvedLLMInfo struct {
	Provider string
	Model    string
}

// ResolveLLMFromConfig resolves the provider and model from agent configuration.
// This is used when a runner is not available (e.g., agent is disabled or not initialized).
func ResolveLLMFromConfig(cfg *config.AgentConfig) ResolvedLLMInfo {
	// If LLM preferences are specified, use the first one
	if len(cfg.LLM) > 0 {
		pref := cfg.LLM[0]
		return ResolvedLLMInfo{
			Provider: pref.Provider,
			Model:    pref.Model,
		}
	}

	// No LLM preferences specified - return empty (will use provider defaults)
	return ResolvedLLMInfo{
		Provider: llm.ProviderAnthropic,
		Model:    "",
	}
}
