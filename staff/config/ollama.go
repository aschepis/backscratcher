package config

import (
	"os"

	llmollama "github.com/aschepis/backscratcher/staff/llm/ollama"
)

// LoadOllamaConfig loads Ollama configuration from the main config.
// It returns the host and model to use for creating an Ollama client.
func LoadOllamaConfig(cfg *Config) (host, model string) {
	if cfg == nil {
		// Return defaults
		host = getOllamaHostFromEnv()
		model = getOllamaModelFromEnv()
		return
	}

	host = cfg.Ollama.Host
	model = cfg.Ollama.Model

	// Apply environment variable overrides
	if envHost := getOllamaHostFromEnv(); envHost != "" {
		host = envHost
	}
	if envModel := getOllamaModelFromEnv(); envModel != "" {
		model = envModel
	}

	// Set defaults if still empty
	if host == "" {
		host = "http://localhost:11434"
	}

	return host, model
}

// NewOllamaClient creates a new Ollama LLM client from the configuration.
func NewOllamaClient(cfg *Config) (*llmollama.OllamaClient, error) {
	host, model := LoadOllamaConfig(cfg)
	return llmollama.NewOllamaClient(host, model)
}

// getOllamaHostFromEnv gets the Ollama host from environment variable.
func getOllamaHostFromEnv() string {
	return os.Getenv("OLLAMA_HOST")
}

// getOllamaModelFromEnv gets the Ollama model from environment variable.
func getOllamaModelFromEnv() string {
	return os.Getenv("OLLAMA_MODEL")
}
