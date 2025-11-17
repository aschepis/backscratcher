package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/TwiN/deepmerge"
	"gopkg.in/yaml.v3"
)

// MCPServerSecrets represents secrets and environment variables for an MCP server.
type MCPServerSecrets struct {
	Env []string `yaml:"env,omitempty"`
}

// ClaudeMCPConfig represents configuration for Claude MCP integration.
type ClaudeMCPConfig struct {
	Enabled    bool     `yaml:"enabled,omitempty"`     // Enable/disable Claude MCP loading
	Projects   []string `yaml:"projects,omitempty"`    // List of project paths to load from (empty = all projects)
	ConfigPath string   `yaml:"config_path,omitempty"` // Override default ~/.claude.json path
}

// Config represents the application configuration.
type Config struct {
	AnthropicAPIKey string                      `yaml:"anthropic_api_key,omitempty"`
	Theme           string                      `yaml:"theme,omitempty"`
	MCPServers      map[string]MCPServerSecrets `yaml:"mcp_servers,omitempty"`
	ClaudeMCP       ClaudeMCPConfig             `yaml:"claude_mcp,omitempty"`
}

// GetConfigPath returns the default config file path, expanding ~ to home directory.
// Can be overridden via STAFF_CONFIG_PATH environment variable.
func GetConfigPath() string {
	if envPath := os.Getenv("STAFF_CONFIG_PATH"); envPath != "" {
		return expandPath(envPath)
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		// Fallback to current directory if home dir can't be determined
		return "./.staffd/config.yaml"
	}
	return filepath.Join(homeDir, ".staffd", "config.yaml")
}

// expandPath expands ~ to the user's home directory.
func expandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(homeDir, path[2:])
	}
	return path
}

// LoadConfig loads the configuration from the specified path.
// Returns a config with defaults if the file doesn't exist (non-fatal).
// Returns an error only if the file exists but cannot be parsed.
func LoadConfig(path string) (*Config, error) {
	expandedPath := expandPath(path)

	// Check if file exists
	if _, err := os.Stat(expandedPath); os.IsNotExist(err) {
		// File doesn't exist - return empty config (non-fatal)
		return &Config{
			MCPServers: make(map[string]MCPServerSecrets),
		}, nil
	}

	// Read file
	data, err := os.ReadFile(expandedPath) //#nosec 304 -- intentional file read for config
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %q: %w", expandedPath, err)
	}

	// Parse YAML
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file %q: %w", expandedPath, err)
	}

	// Initialize MCPServers map if nil
	if cfg.MCPServers == nil {
		cfg.MCPServers = make(map[string]MCPServerSecrets)
	}

	// Apply environment variable overrides for Claude MCP settings
	// Environment variables take precedence over config file values
	if envEnabled := os.Getenv("STAFF_CLAUDE_MCP_ENABLED"); envEnabled != "" {
		cfg.ClaudeMCP.Enabled = (envEnabled == "true" || envEnabled == "1")
	}
	if envProjects := os.Getenv("STAFF_CLAUDE_MCP_PROJECTS"); envProjects != "" {
		// Split comma-separated list
		projects := strings.Split(envProjects, ",")
		cfg.ClaudeMCP.Projects = make([]string, 0, len(projects))
		for _, p := range projects {
			if trimmed := strings.TrimSpace(p); trimmed != "" {
				cfg.ClaudeMCP.Projects = append(cfg.ClaudeMCP.Projects, trimmed)
			}
		}
	}
	if envConfigPath := os.Getenv("STAFF_CLAUDE_MCP_CONFIG_PATH"); envConfigPath != "" {
		cfg.ClaudeMCP.ConfigPath = envConfigPath
	}

	return &cfg, nil
}

// MergeMCPServerConfigs merges MCP server configurations from agents.yaml (base) with secrets from config file (overrides).
// Uses deepmerge to properly merge nested structures, with config file values taking precedence.
func MergeMCPServerConfigs(baseYAML []byte, configSecrets map[string]MCPServerSecrets) ([]byte, error) {
	// Parse base YAML to ensure mcp_servers is a map
	var baseConfig map[string]interface{}
	if err := yaml.Unmarshal(baseYAML, &baseConfig); err != nil {
		return nil, fmt.Errorf("failed to parse base YAML: %w", err)
	}

	// Ensure mcp_servers exists and is a map
	if baseConfig["mcp_servers"] == nil {
		baseConfig["mcp_servers"] = make(map[string]interface{})
	} else {
		// Verify it's actually a map, not a slice or other type
		if _, ok := baseConfig["mcp_servers"].(map[string]interface{}); !ok {
			// If it's not a map, replace it with an empty map
			baseConfig["mcp_servers"] = make(map[string]interface{})
		}
	}

	// Re-marshal base to ensure proper structure
	normalizedBaseYAML, err := yaml.Marshal(baseConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal normalized base config: %w", err)
	}

	// Create override YAML from config secrets
	override := map[string]interface{}{
		"mcp_servers": make(map[string]interface{}),
	}
	for name, secrets := range configSecrets {
		if override["mcp_servers"].(map[string]interface{})[name] == nil {
			override["mcp_servers"].(map[string]interface{})[name] = make(map[string]interface{})
		}
		serverCfg := override["mcp_servers"].(map[string]interface{})[name].(map[string]interface{})
		if len(secrets.Env) > 0 {
			serverCfg["env"] = secrets.Env
		}
	}

	// Marshal override to YAML
	overrideYAML, err := yaml.Marshal(override)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal override config: %w", err)
	}

	// Deep merge base with override
	merged, err := deepmerge.YAML(normalizedBaseYAML, overrideYAML)
	if err != nil {
		return nil, fmt.Errorf("failed to merge MCP server configs: %w", err)
	}

	return merged, nil
}

// SaveConfig saves the configuration to the specified path.
func SaveConfig(cfg *Config, path string) error {
	expandedPath := expandPath(path)

	// Ensure directory exists
	dir := filepath.Dir(expandedPath)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Marshal to YAML
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// Write file
	if err := os.WriteFile(expandedPath, data, 0o600); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}
