package config

import (
	"os"

	llmanthropic "github.com/aschepis/backscratcher/staff/llm/anthropic"
)

// LoadAnthropicConfig loads Anthropic configuration from the main config.
// It returns the API key to use for creating an Anthropic client.
func LoadAnthropicConfig(cfg *Config) (apiKey string) {
	if cfg == nil {
		// Return default from environment
		apiKey = getAnthropicAPIKeyFromEnv()
		return
	}

	apiKey = cfg.Anthropic.APIKey

	// Apply environment variable overrides
	if envAPIKey := getAnthropicAPIKeyFromEnv(); envAPIKey != "" {
		apiKey = envAPIKey
	}

	return apiKey
}

// NewAnthropicClient creates a new Anthropic LLM client from the configuration.
func NewAnthropicClient(cfg *Config) (*llmanthropic.AnthropicClient, error) {
	apiKey := LoadAnthropicConfig(cfg)
	return llmanthropic.NewAnthropicClient(apiKey)
}

// getAnthropicAPIKeyFromEnv gets the Anthropic API key from environment variable.
func getAnthropicAPIKeyFromEnv() string {
	return os.Getenv("ANTHROPIC_API_KEY")
}
