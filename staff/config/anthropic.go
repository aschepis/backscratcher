package config

import (
	llmanthropic "github.com/aschepis/backscratcher/staff/llm/anthropic"
)

// LoadAnthropicConfig loads Anthropic configuration from the main config.
// It returns the API key to use for creating an Anthropic client.
func LoadAnthropicConfig(cfg *Config) (apiKey string) {
	if cfg == nil {
		return ""
	}

	return cfg.Anthropic.APIKey
}

// NewAnthropicClient creates a new Anthropic LLM client from the configuration.
func NewAnthropicClient(cfg *Config) (*llmanthropic.AnthropicClient, error) {
	apiKey := LoadAnthropicConfig(cfg)
	return llmanthropic.NewAnthropicClient(apiKey)
}
