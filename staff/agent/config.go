package agent

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// MCPServerConfig represents configuration for an MCP server.
type MCPServerConfig struct {
	Name       string   `yaml:"name,omitempty"`
	Homepage   string   `yaml:"homepage,omitempty"`
	Command    string   `yaml:"command,omitempty"`     // For STDIO transport
	URL        string   `yaml:"url,omitempty"`         // For HTTP transport
	ConfigFile string   `yaml:"config_file,omitempty"` // Path to server config YAML
	Args       []string `yaml:"args,omitempty"`        // Additional args for STDIO command
	Env        []string `yaml:"env,omitempty"`         // Environment variables for STDIO
}

// LoadCrewConfigFromFile loads a CrewConfig from a YAML file.
func LoadCrewConfigFromFile(path string) (*CrewConfig, error) {
	data, err := os.ReadFile(path) //#nosec 304 -- intentional file read for config
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %q: %w", path, err)
	}

	var cfg CrewConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file %q: %w", path, err)
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

	return &cfg, nil
}
