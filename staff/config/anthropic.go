package config

import (
	llmanthropic "github.com/aschepis/backscratcher/staff/llm/anthropic"
	"github.com/rs/zerolog"
)

// LoadAnthropicConfig loads Anthropic configuration from server config.
// It returns the API key to use for creating an Anthropic client.
func LoadAnthropicConfig(cfg *ServerConfig) (apiKey string) {
	if cfg == nil {
		return ""
	}

	return cfg.Anthropic.APIKey
}

// NewAnthropicClient creates a new Anthropic LLM client from the configuration.
func NewAnthropicClient(cfg *ServerConfig, logger zerolog.Logger) (*llmanthropic.AnthropicClient, error) {
	apiKey := LoadAnthropicConfig(cfg)
	return llmanthropic.NewAnthropicClient(apiKey, logger)
}
