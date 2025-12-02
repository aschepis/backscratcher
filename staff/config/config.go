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

// ServerConfig represents server-side configuration for staffd daemon.
type ServerConfig struct {
	// Server settings
	Server struct {
		Socket string `yaml:"socket,omitempty"` // Unix socket path (default: /tmp/staffd.sock)
		TCP    string `yaml:"tcp,omitempty"`    // TCP address (e.g., localhost:50051)
	} `yaml:"server,omitempty"`

	// LLM provider configurations
	Anthropic AnthropicConfig `yaml:"anthropic,omitempty"`
	Ollama    OllamaConfig    `yaml:"ollama,omitempty"`
	OpenAI    OpenAIConfig    `yaml:"openai,omitempty"`

	// Agent/Crew configuration
	LLMProviders []string                    `yaml:"llm_providers,omitempty"`
	Agents       map[string]*AgentConfig     `yaml:"agents,omitempty"`
	MCPServers   map[string]*MCPServerConfig `yaml:"mcp_servers,omitempty"`

	// Feature configurations
	ClaudeMCP            ClaudeMCPConfig      `yaml:"claude_mcp,omitempty"`
	ChatTimeout          int                  `yaml:"chat_timeout,omitempty"`
	MessageSummarization MessageSummarization `yaml:"message_summarization,omitempty"`

	// Internal: used for merging secrets from user config file
	mcpServerSecrets map[string]MCPServerSecrets `yaml:"-"` // Not serialized, used only during merge
}

type DaemonConfig struct {
	Socket string `yaml:"socket,omitempty"` // Unix socket path (default: /tmp/staffd.sock)
	TCP    string `yaml:"tcp,omitempty"`    // TCP address (e.g., localhost:50051)
}

// ClientConfig represents client-side configuration for staff CLI.
type ClientConfig struct {
	// Connection settings
	Daemon DaemonConfig `yaml:"daemon,omitempty"`

	// UI preferences
	Theme       string `yaml:"theme,omitempty"`        // UI theme (default: solarized)
	ChatTimeout int    `yaml:"chat_timeout,omitempty"` // Timeout in seconds for chat operations (default: 60)
}

// GetServerConfigPath returns the default server config file path.
// Can be overridden via STAFF_CONFIG_PATH environment variable.
func GetServerConfigPath() string {
	if envPath := os.Getenv("STAFF_CONFIG_PATH"); envPath != "" {
		return expandPath(envPath)
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "./.staffd/config.yaml"
	}
	return filepath.Join(homeDir, ".staffd", "config.yaml")
}

// GetClientConfigPath returns the default client config file path.
// Can be overridden via STAFF_CLIENT_CONFIG_PATH environment variable.
func GetClientConfigPath() string {
	if envPath := os.Getenv("STAFF_CLIENT_CONFIG_PATH"); envPath != "" {
		return expandPath(envPath)
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "./.staffd/cli.yaml"
	}
	return filepath.Join(homeDir, ".staffd", "cli.yaml")
}

// GetConfigPath returns the default config file path, expanding ~ to home directory.
// Can be overridden via STAFF_CONFIG_PATH environment variable.
// Deprecated: Use GetServerConfigPath() or GetClientConfigPath() instead.
// This is kept for backward compatibility with ui/tui/settings.go.
func GetConfigPath() string {
	return GetServerConfigPath()
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

// MergeMCPServerConfigs merges MCP server configurations from agents.yaml (base) with secrets from config file (overrides).
// Uses mergo to properly merge nested structures, with config file values taking precedence.
func MergeMCPServerConfigs(baseYAML []byte, configSecrets map[string]MCPServerSecrets) ([]byte, error) {
	// Unmarshal base YAML to ServerConfig struct
	var baseConfig ServerConfig
	if err := yaml.Unmarshal(baseYAML, &baseConfig); err != nil {
		return nil, fmt.Errorf("failed to parse base YAML: %w", err)
	}

	// Initialize maps if nil
	if baseConfig.MCPServers == nil {
		baseConfig.MCPServers = make(map[string]*MCPServerConfig)
	}

	// Create override ServerConfig from config secrets
	overrideConfig := ServerConfig{
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

// SaveServerConfig saves the server configuration to the specified path.
func SaveServerConfig(cfg *ServerConfig, path string) error {
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

// SaveClientConfig saves the client configuration to the specified path.
func SaveClientConfig(cfg *ClientConfig, path string) error {
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

// LoadServerConfig loads server-side configuration.
// Loads from agents.yaml and server config file, merging them together.
func LoadServerConfig(path string) (*ServerConfig, error) {
	// Step 1: Set defaults
	defaults := ServerConfig{
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
	defaults.Server.Socket = "/tmp/staffd.sock"

	// Step 2: Load and merge agents.yaml config
	agentsConfigPath := "agents.yaml"
	if envPath := os.Getenv("AGENTS_CONFIG"); envPath != "" {
		agentsConfigPath = envPath
	}

	agentsYAML, err := os.ReadFile(agentsConfigPath) //#nosec 304 -- intentional file read for config
	if err != nil {
		return nil, fmt.Errorf("failed to read agents config from %q: %w", agentsConfigPath, err)
	}

	var agentsConfig ServerConfig
	if err := yaml.Unmarshal(agentsYAML, &agentsConfig); err != nil {
		return nil, fmt.Errorf("failed to parse agents config: %w", err)
	}

	// Merge agents config onto defaults
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
		userConfigYAML, err = os.ReadFile(expandedPath) //#nosec 304 -- intentional file read for config
		if err != nil {
			return nil, fmt.Errorf("failed to read user config file %q: %w", expandedPath, err)
		}

		var userConfig ServerConfig
		if err := yaml.Unmarshal(userConfigYAML, &userConfig); err != nil {
			return nil, fmt.Errorf("failed to parse user config: %w", err)
		}

		// Extract MCP server secrets separately
		if err := yaml.Unmarshal(userConfigYAML, &userConfigSecrets); err == nil {
			defaults.mcpServerSecrets = userConfigSecrets.MCPServers
		}

		// Merge user config on top
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

	// Handle MCP server secrets from user config
	if len(userConfigYAML) > 0 && len(userConfigSecrets.MCPServers) > 0 {
		for name, secrets := range userConfigSecrets.MCPServers {
			if defaults.MCPServers[name] == nil {
				defaults.MCPServers[name] = &MCPServerConfig{}
			}
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

// LoadClientConfig loads client-side configuration.
// Returns defaults if config file doesn't exist.
func LoadClientConfig(path string) (*ClientConfig, error) {
	defaults := ClientConfig{
		Theme:       "solarized",
		ChatTimeout: 60,
	}
	defaults.Daemon.Socket = "/tmp/staffd.sock"

	// Load config file if it exists
	expandedPath := expandPath(path)
	if _, err := os.Stat(expandedPath); err != nil {
		// File doesn't exist, return defaults
		return &defaults, nil
	}

	configYAML, err := os.ReadFile(expandedPath) //#nosec 304 -- intentional file read for config
	if err != nil {
		return nil, fmt.Errorf("failed to read client config file %q: %w", expandedPath, err)
	}

	var config ClientConfig
	if err := yaml.Unmarshal(configYAML, &config); err != nil {
		return nil, fmt.Errorf("failed to parse client config: %w", err)
	}

	// Merge loaded config onto defaults
	if err := mergo.Merge(&defaults, config, mergo.WithOverride); err != nil {
		return nil, fmt.Errorf("failed to merge client config: %w", err)
	}

	return &defaults, nil
}
