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

// MessageSummarization represents configuration for message summarization using Ollama.
type MessageSummarization struct {
	Enabled       bool   `yaml:"enabled,omitempty"`         // Enable/disable message summarization
	Model         string `yaml:"model,omitempty"`           // Ollama model name (default: "llama3.2:3b")
	MaxChars      int    `yaml:"max_chars,omitempty"`       // Maximum characters before summarization (default: 2000)
	MaxLines      int    `yaml:"max_lines,omitempty"`       // Maximum lines before summarization (default: 50)
	MaxLineBreaks int    `yaml:"max_line_breaks,omitempty"` // Maximum line breaks before summarization (default: 10)
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
	LLMProvider          string                      `yaml:"llm_provider,omitempty"` // LLM provider selection: "anthropic", "ollama", or "openai" (default: "anthropic")
	Anthropic            AnthropicConfig             `yaml:"anthropic,omitempty"`    // Anthropic LLM provider configuration
	Theme                string                      `yaml:"theme,omitempty"`
	MCPServers           map[string]MCPServerSecrets `yaml:"mcp_servers,omitempty"`
	ClaudeMCP            ClaudeMCPConfig             `yaml:"claude_mcp,omitempty"`
	ChatTimeout          int                         `yaml:"chat_timeout,omitempty"`          // Timeout in seconds for chat operations (default: 60)
	MessageSummarization MessageSummarization        `yaml:"message_summarization,omitempty"` // Message summarization configuration
	Ollama               OllamaConfig                `yaml:"ollama,omitempty"`                // Ollama LLM provider configuration
	OpenAI               OpenAIConfig                `yaml:"openai,omitempty"`                // OpenAI LLM provider configuration
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
		cfg := &Config{
			LLMProvider: "anthropic", // Default to Anthropic for backward compatibility
			MCPServers:  make(map[string]MCPServerSecrets),
			ChatTimeout: 60, // Default timeout
			MessageSummarization: MessageSummarization{
				Model:         "llama3.2:3b",
				MaxChars:      2000,
				MaxLines:      50,
				MaxLineBreaks: 10,
			},
		}
		// Apply environment variable defaults
		applyLLMProviderEnvDefaults(cfg)
		applyAnthropicEnvDefaults(cfg)
		applyOllamaEnvDefaults(cfg)
		applyOpenAIEnvDefaults(cfg)
		return cfg, nil
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

	// Set default LLM provider if not specified (anthropic for backward compatibility)
	if cfg.LLMProvider == "" {
		cfg.LLMProvider = "anthropic"
	}

	// Set default chat timeout if not specified (60 seconds)
	if cfg.ChatTimeout == 0 {
		cfg.ChatTimeout = 60
	}

	// Set default message summarization values if not specified
	if cfg.MessageSummarization.Model == "" {
		cfg.MessageSummarization.Model = "llama3.2:3b"
	}
	if cfg.MessageSummarization.MaxChars == 0 {
		cfg.MessageSummarization.MaxChars = 2000
	}
	if cfg.MessageSummarization.MaxLines == 0 {
		cfg.MessageSummarization.MaxLines = 50
	}
	if cfg.MessageSummarization.MaxLineBreaks == 0 {
		cfg.MessageSummarization.MaxLineBreaks = 10
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

	// Apply environment variable overrides
	applyLLMProviderEnvDefaults(&cfg)
	applyAnthropicEnvDefaults(&cfg)
	applyOllamaEnvDefaults(&cfg)
	applyOpenAIEnvDefaults(&cfg)

	return &cfg, nil
}

// applyLLMProviderEnvDefaults applies environment variable defaults and overrides for LLM provider selection.
func applyLLMProviderEnvDefaults(cfg *Config) {
	// Environment variable overrides config file
	if envProvider := os.Getenv("STAFF_LLM_PROVIDER"); envProvider != "" {
		cfg.LLMProvider = envProvider
	}
}

// applyAnthropicEnvDefaults applies environment variable defaults and overrides for Anthropic config.
func applyAnthropicEnvDefaults(cfg *Config) {
	// Set API key from environment if not specified in config
	if cfg.Anthropic.APIKey == "" {
		if envAPIKey := os.Getenv("ANTHROPIC_API_KEY"); envAPIKey != "" {
			cfg.Anthropic.APIKey = envAPIKey
		}
	} else if envAPIKey := os.Getenv("ANTHROPIC_API_KEY"); envAPIKey != "" {
		// Environment variable overrides config file
		cfg.Anthropic.APIKey = envAPIKey
	}
}

// applyOllamaEnvDefaults applies environment variable defaults and overrides for Ollama config.
func applyOllamaEnvDefaults(cfg *Config) {
	// Set default host if not specified
	if cfg.Ollama.Host == "" {
		if envHost := os.Getenv("OLLAMA_HOST"); envHost != "" {
			cfg.Ollama.Host = envHost
		} else {
			cfg.Ollama.Host = "http://localhost:11434" // Default Ollama host
		}
	} else if envHost := os.Getenv("OLLAMA_HOST"); envHost != "" {
		// Environment variable overrides config file
		cfg.Ollama.Host = envHost
	}

	// Set default model if not specified
	if cfg.Ollama.Model == "" {
		if envModel := os.Getenv("OLLAMA_MODEL"); envModel != "" {
			cfg.Ollama.Model = envModel
		}
	} else if envModel := os.Getenv("OLLAMA_MODEL"); envModel != "" {
		// Environment variable overrides config file
		cfg.Ollama.Model = envModel
	}
}

// applyOpenAIEnvDefaults applies environment variable defaults and overrides for OpenAI config.
func applyOpenAIEnvDefaults(cfg *Config) {
	// Set API key from environment if not specified in config
	if cfg.OpenAI.APIKey == "" {
		if envAPIKey := os.Getenv("OPENAI_API_KEY"); envAPIKey != "" {
			cfg.OpenAI.APIKey = envAPIKey
		}
	} else if envAPIKey := os.Getenv("OPENAI_API_KEY"); envAPIKey != "" {
		// Environment variable overrides config file
		cfg.OpenAI.APIKey = envAPIKey
	}

	// Set base URL from environment if not specified in config
	if cfg.OpenAI.BaseURL == "" {
		if envBaseURL := os.Getenv("OPENAI_BASE_URL"); envBaseURL != "" {
			cfg.OpenAI.BaseURL = envBaseURL
		}
	} else if envBaseURL := os.Getenv("OPENAI_BASE_URL"); envBaseURL != "" {
		// Environment variable overrides config file
		cfg.OpenAI.BaseURL = envBaseURL
	}

	// Set model from environment if not specified in config
	if cfg.OpenAI.Model == "" {
		if envModel := os.Getenv("OPENAI_MODEL"); envModel != "" {
			cfg.OpenAI.Model = envModel
		}
	} else if envModel := os.Getenv("OPENAI_MODEL"); envModel != "" {
		// Environment variable overrides config file
		cfg.OpenAI.Model = envModel
	}

	// Set organization from environment if not specified in config
	if cfg.OpenAI.Organization == "" {
		if envOrg := os.Getenv("OPENAI_ORG_ID"); envOrg != "" {
			cfg.OpenAI.Organization = envOrg
		}
	} else if envOrg := os.Getenv("OPENAI_ORG_ID"); envOrg != "" {
		// Environment variable overrides config file
		cfg.OpenAI.Organization = envOrg
	}
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
