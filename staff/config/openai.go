package config

import (
	"os"

	llmopenai "github.com/aschepis/backscratcher/staff/llm/openai"
)

// LoadOpenAIConfig loads OpenAI configuration from server config.
// It returns the API key, base URL, model, and organization to use for creating an OpenAI client.
func LoadOpenAIConfig(cfg *ServerConfig) (apiKey, baseURL, model, organization string) {
	if cfg == nil {
		// Return defaults from environment
		apiKey = getOpenAIAPIKeyFromEnv()
		baseURL = getOpenAIBaseURLFromEnv()
		model = getOpenAIModelFromEnv()
		organization = getOpenAIOrgFromEnv()
		return
	}

	apiKey = cfg.OpenAI.APIKey
	baseURL = cfg.OpenAI.BaseURL
	model = cfg.OpenAI.Model
	organization = cfg.OpenAI.Organization

	// Apply environment variable overrides
	if envAPIKey := getOpenAIAPIKeyFromEnv(); envAPIKey != "" {
		apiKey = envAPIKey
	}
	if envBaseURL := getOpenAIBaseURLFromEnv(); envBaseURL != "" {
		baseURL = envBaseURL
	}
	if envModel := getOpenAIModelFromEnv(); envModel != "" {
		model = envModel
	}
	if envOrg := getOpenAIOrgFromEnv(); envOrg != "" {
		organization = envOrg
	}

	return apiKey, baseURL, model, organization
}

// NewOpenAIClient creates a new OpenAI LLM client from the configuration.
func NewOpenAIClient(cfg *ServerConfig) (*llmopenai.OpenAIClient, error) {
	apiKey, baseURL, model, organization := LoadOpenAIConfig(cfg)
	return llmopenai.NewOpenAIClient(apiKey, baseURL, model, organization)
}

// getOpenAIAPIKeyFromEnv gets the OpenAI API key from environment variable.
func getOpenAIAPIKeyFromEnv() string {
	return os.Getenv("OPENAI_API_KEY")
}

// getOpenAIBaseURLFromEnv gets the OpenAI base URL from environment variable.
func getOpenAIBaseURLFromEnv() string {
	return os.Getenv("OPENAI_BASE_URL")
}

// getOpenAIModelFromEnv gets the OpenAI model from environment variable.
func getOpenAIModelFromEnv() string {
	return os.Getenv("OPENAI_MODEL")
}

// getOpenAIOrgFromEnv gets the OpenAI organization ID from environment variable.
func getOpenAIOrgFromEnv() string {
	return os.Getenv("OPENAI_ORG_ID")
}
