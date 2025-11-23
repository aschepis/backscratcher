package config

import (
	_ "embed"
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

// Config represents the application configuration.
type Config struct {
	LLMProviders         []string                    `yaml:"llm_providers,omitempty"` // Array of enabled LLM providers: "anthropic", "ollama", "openai" (default: ["anthropic"])
	Anthropic            AnthropicConfig             `yaml:"anthropic,omitempty"`     // Anthropic LLM provider configuration
	Ollama               OllamaConfig                `yaml:"ollama,omitempty"`        // Ollama LLM provider configuration
	OpenAI               OpenAIConfig                `yaml:"openai,omitempty"`        // OpenAI LLM provider configuration
	Theme                string                      `yaml:"theme,omitempty"`
	MCPServers           map[string]MCPServerSecrets `yaml:"mcp_servers,omitempty"`
	ClaudeMCP            ClaudeMCPConfig             `yaml:"claude_mcp,omitempty"`
	ChatTimeout          int                         `yaml:"chat_timeout,omitempty"`          // Timeout in seconds for chat operations (default: 60)
	MessageSummarization MessageSummarization        `yaml:"message_summarization,omitempty"` // Message summarization configuration
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
// Configuration is merged on top of the default configuration.
func LoadConfig(path string) (*Config, error) {
	expandedPath := expandPath(path)

	// Check if file exists
	if _, err := os.Stat(expandedPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("config file does not exist at %q", expandedPath)
	}

	// Read file
	data, err := os.ReadFile(expandedPath) //#nosec 304 -- intentional file read for config
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %q: %w", expandedPath, err)
	}

	cfg := Config{
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
		MCPServers:  make(map[string]MCPServerSecrets),
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

	// Parse YAML
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file %q: %w", expandedPath, err)
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
