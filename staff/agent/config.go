package agent

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// LoadCrewConfigFromFile loads a CrewConfig from a YAML file.
func LoadCrewConfigFromFile(path string) (*CrewConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %q: %w", path, err)
	}

	var cfg CrewConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file %q: %w", path, err)
	}

	// Ensure IDs are set if missing (use map key)
	for id, agentCfg := range cfg.Agents {
		if agentCfg.ID == "" {
			agentCfg.ID = id
		}
	}

	return &cfg, nil
}
