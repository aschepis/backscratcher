package config

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dario.cat/mergo"
	"gopkg.in/yaml.v3"
)

// MCPServerSecrets represents secrets and environment variables for an MCP server.
// This is used for merging secrets from user config file into agents.yaml config.
type MCPServerSecrets struct {
	Env []string `yaml:"env,omitempty"`
}

// ClaudeMCPConfig represents configuration for Claude MCP integration.
type ClaudeMCPConfig struct {
	Enabled    bool     `yaml:"enabled,omitempty"`     // Enable/disable Claude MCP loading
	Projects   []string `yaml:"projects,omitempty"`    // List of project paths to load from (empty = all projects)
	ConfigPath string   `yaml:"config_path,omitempty"` // Override default ~/.claude.json path
}

// MessageSummarization represents configuration for message summarization using Ollama.
type MessageSummarization struct {
	Disabled      bool   `yaml:"disabled,omitempty"`        // Disable message summarization (enabled by default)
	Model         string `yaml:"model,omitempty"`           // Ollama model name
	MaxChars      int    `yaml:"max_chars,omitempty"`       // Maximum characters before summarization
	MaxLines      int    `yaml:"max_lines,omitempty"`       // Maximum lines before summarization
	MaxLineBreaks int    `yaml:"max_line_breaks,omitempty"` // Maximum line breaks before summarization
}

// AnthropicConfig represents configuration for Anthropic LLM provider.
type AnthropicConfig struct {
	APIKey string `yaml:"api_key,omitempty"` // Anthropic API key
}

// OllamaConfig represents configuration for Ollama LLM provider.
type OllamaConfig struct {
	Host    string `yaml:"host,omitempty"`    // Ollama host (default: "http://localhost:11434")
	Model   string `yaml:"model,omitempty"`   // Default model name
	Timeout int    `yaml:"timeout,omitempty"` // Request timeout in seconds
}

// OpenAIConfig represents configuration for OpenAI LLM provider.
type OpenAIConfig struct {
	APIKey       string `yaml:"api_key,omitempty"`      // OpenAI API key
	BaseURL      string `yaml:"base_url,omitempty"`     // Custom base URL (default: official API)
	Model        string `yaml:"model,omitempty"`        // Default model name
	Organization string `yaml:"organization,omitempty"` // Organization ID
}

// LLMPreference represents a single LLM provider/model preference for an agent.
// Agents can specify multiple preferences in order, and the system will use
// the first available provider from the preference list.
type LLMPreference struct {
	Provider    string   `yaml:"provider" json:"provider"`                           // Required: "anthropic", "ollama", or "openai"
	Model       string   `yaml:"model,omitempty" json:"model,omitempty"`             // Optional: uses provider default if omitted
	Temperature *float64 `yaml:"temperature,omitempty" json:"temperature,omitempty"` // Optional temperature override
	APIKeyRef   string   `yaml:"api_key_ref,omitempty" json:"api_key_ref,omitempty"` // Future: reference to credential store
}

// AgentConfig represents the configuration for a single agent.
type AgentConfig struct {
	ID           string          `yaml:"id" json:"id"`
	Name         string          `yaml:"name" json:"name"`
	System       string          `yaml:"system_prompt" json:"system"`
	MaxTokens    int64           `yaml:"max_tokens" json:"max_tokens"`
	Tools        []string        `yaml:"tools" json:"tools"`
	Schedule     string          `yaml:"schedule" json:"schedule"`           // e.g., "15m", "2h", "0 */15 * * * *" (cron)
	Disabled     bool            `yaml:"disabled" json:"disabled"`           // default: false (agent is enabled by default)
	StartupDelay string          `yaml:"startup_delay" json:"startup_delay"` // e.g., "5m", "30s", "1h" - one-time delay after app launch
	LLM          []LLMPreference `yaml:"llm,omitempty" json:"llm,omitempty"` // Ordered list of provider/model preferences
}

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

// Config represents the unified application configuration.
type Config struct {
	// Application settings
	Anthropic            AnthropicConfig      `yaml:"anthropic,omitempty"` // Anthropic LLM provider configuration
	Ollama               OllamaConfig         `yaml:"ollama,omitempty"`    // Ollama LLM provider configuration
	OpenAI               OpenAIConfig         `yaml:"openai,omitempty"`    // OpenAI LLM provider configuration
	Theme                string               `yaml:"theme,omitempty"`
	ClaudeMCP            ClaudeMCPConfig      `yaml:"claude_mcp,omitempty"`
	ChatTimeout          int                  `yaml:"chat_timeout,omitempty"`          // Timeout in seconds for chat operations (default: 60)
	MessageSummarization MessageSummarization `yaml:"message_summarization,omitempty"` // Message summarization configuration

	// Agent/Crew configuration (from agents.yaml)
	LLMProviders []string                    `yaml:"llm_providers,omitempty"` // Array of enabled LLM providers: "anthropic", "ollama", "openai" (default: ["anthropic"])
	Agents       map[string]*AgentConfig     `yaml:"agents,omitempty"`        // Agent configurations
	MCPServers   map[string]*MCPServerConfig `yaml:"mcp_servers,omitempty"`   // MCP server configurations

	// Internal: used for merging secrets from user config file
	mcpServerSecrets map[string]MCPServerSecrets `yaml:"-"` // Not serialized, used only during merge
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

// LoadConfig loads the unified configuration by:
// 1. Setting defaults
// 2. Loading agents.yaml config
// 3. Merging user config file onto the result
// Returns a single Config with all configuration.
// Uses mergo to properly merge nested structures.
func LoadConfig(path string) (*Config, error) {
	// Step 1: Set defaults
	defaults := Config{
		LLMProviders: []string{"anthropic"},
		Anthropic: AnthropicConfig{
			APIKey: "",
		},
		Ollama: OllamaConfig{
			Host:    "http://localhost:11434",
			Model:   "gpt-oss:20b",
			Timeout: 60,
		},
		OpenAI: OpenAIConfig{
			APIKey:       "",
			BaseURL:      "https://api.openai.com/v1",
			Model:        "llama3.2:3b",
			Organization: "",
		},
		ChatTimeout: 60,
		Theme:       "random",
		Agents:      make(map[string]*AgentConfig),
		MCPServers:  make(map[string]*MCPServerConfig),
		ClaudeMCP: ClaudeMCPConfig{
			Enabled:    false,
			Projects:   []string{},
			ConfigPath: "~/.claude.json",
		},
		MessageSummarization: MessageSummarization{
			Disabled:      false,
			Model:         "llama3.2:3b",
			MaxChars:      2000,
			MaxLines:      50,
			MaxLineBreaks: 10,
		},
	}

	// Step 2: Load and merge agents.yaml config
	agentsConfigPath := "agents.yaml"
	if envPath := os.Getenv("AGENTS_CONFIG"); envPath != "" {
		agentsConfigPath = envPath
	}

	agentsYAML, err := os.ReadFile(agentsConfigPath) //#nosec 304 -- intentional file read for config
	if err != nil {
		return nil, fmt.Errorf("failed to read agents config from %q: %w", agentsConfigPath, err)
	}

	var agentsConfig Config
	if err := yaml.Unmarshal(agentsYAML, &agentsConfig); err != nil {
		return nil, fmt.Errorf("failed to parse agents config: %w", err)
	}

	// Merge agents config onto defaults (agents.yaml values take precedence over defaults)
	if err := mergo.Merge(&defaults, agentsConfig, mergo.WithOverride); err != nil {
		return nil, fmt.Errorf("failed to merge agents config: %w", err)
	}

	// Step 3: Merge user config file onto the result (if it exists)
	expandedPath := expandPath(path)
	var userConfigYAML []byte
	var userConfigSecrets struct {
		MCPServers map[string]MCPServerSecrets `yaml:"mcp_servers,omitempty"`
	}

	if _, err := os.Stat(expandedPath); err == nil {
		// File exists, read it
		userConfigYAML, err = os.ReadFile(expandedPath) //#nosec 304 -- intentional file read for config
		if err != nil {
			return nil, fmt.Errorf("failed to read user config file %q: %w", expandedPath, err)
		}

		var userConfig Config
		if err := yaml.Unmarshal(userConfigYAML, &userConfig); err != nil {
			return nil, fmt.Errorf("failed to parse user config: %w", err)
		}

		// Extract MCP server secrets separately (they use a different type)
		if err := yaml.Unmarshal(userConfigYAML, &userConfigSecrets); err == nil {
			defaults.mcpServerSecrets = userConfigSecrets.MCPServers
		}

		// Merge user config on top (user config takes precedence)
		// Note: For MCP servers, we'll handle the secrets merge separately below
		if err := mergo.Merge(&defaults, userConfig, mergo.WithOverride); err != nil {
			return nil, fmt.Errorf("failed to merge user config: %w", err)
		}
	}

	// Initialize maps if they're nil
	if defaults.Agents == nil {
		defaults.Agents = make(map[string]*AgentConfig)
	}
	if defaults.MCPServers == nil {
		defaults.MCPServers = make(map[string]*MCPServerConfig)
	}
	if defaults.mcpServerSecrets == nil {
		defaults.mcpServerSecrets = make(map[string]MCPServerSecrets)
	}

	// Handle MCP server secrets from user config (if user config file exists)
	// MCP server secrets in user config are MCPServerSecrets (only env vars),
	// but we need to merge them into MCPServerConfig (full config from agents.yaml)
	if len(userConfigYAML) > 0 && len(userConfigSecrets.MCPServers) > 0 {
		for name, secrets := range userConfigSecrets.MCPServers {
			if defaults.MCPServers[name] == nil {
				// Create new MCP server config if it doesn't exist
				defaults.MCPServers[name] = &MCPServerConfig{}
			}
			// Convert MCPServerSecrets to MCPServerConfig and merge using mergo
			// (user config takes precedence)
			overrideServerConfig := &MCPServerConfig{
				Env: secrets.Env,
			}
			if err := mergo.Merge(defaults.MCPServers[name], overrideServerConfig, mergo.WithOverride); err != nil {
				return nil, fmt.Errorf("failed to merge MCP server secrets for %q: %w", name, err)
			}
		}
	}

	// Apply smart defaults to agents
	for id, agentCfg := range defaults.Agents {
		if agentCfg.ID == "" {
			agentCfg.ID = id
		}
		if agentCfg.Name == "" {
			agentCfg.Name = agentCfg.ID
		}
		if agentCfg.MaxTokens == 0 {
			agentCfg.MaxTokens = 2048
		}
	}

	return &defaults, nil
}

// MergeMCPServerConfigs merges MCP server configurations from agents.yaml (base) with secrets from config file (overrides).
// Uses mergo to properly merge nested structures, with config file values taking precedence.
func MergeMCPServerConfigs(baseYAML []byte, configSecrets map[string]MCPServerSecrets) ([]byte, error) {
	// Unmarshal base YAML to Config struct
	var baseConfig Config
	if err := yaml.Unmarshal(baseYAML, &baseConfig); err != nil {
		return nil, fmt.Errorf("failed to parse base YAML: %w", err)
	}

	// Initialize maps if nil
	if baseConfig.MCPServers == nil {
		baseConfig.MCPServers = make(map[string]*MCPServerConfig)
	}

	// Create override Config from config secrets
	overrideConfig := Config{
		MCPServers: make(map[string]*MCPServerConfig),
	}
	for name, secrets := range configSecrets {
		overrideConfig.MCPServers[name] = &MCPServerConfig{
			Env: secrets.Env,
		}
	}

	// Merge override onto base using mergo (override takes precedence)
	if err := mergo.Merge(&baseConfig, overrideConfig, mergo.WithOverride); err != nil {
		return nil, fmt.Errorf("failed to merge MCP server configs: %w", err)
	}

	// Marshal merged config back to YAML
	mergedYAML, err := yaml.Marshal(&baseConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal merged config: %w", err)
	}

	return mergedYAML, nil
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
