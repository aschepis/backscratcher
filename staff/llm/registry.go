package llm

import (
	"fmt"
	"os"
	"sync"
)

const (
	providerAnthropic = "anthropic"
	providerOllama    = "ollama"
	providerOpenAI    = "openai"
)

// AgentLLMConfig represents the LLM configuration portion of an agent config.
// This is used to avoid import cycles.
type AgentLLMConfig struct {
	LLMPreferences []LLMPreference
	Model          string // Legacy model field
}

// LLMPreference represents a single provider/model preference.
type LLMPreference struct {
	Provider    string
	Model       string
	Temperature *float64
	APIKeyRef   string
}

// ClientKey uniquely identifies an LLM client configuration.
type ClientKey struct {
	Provider     string
	Model        string
	APIKey       string // For credential-based providers
	Host         string // For Ollama
	BaseURL      string // For OpenAI
	Organization string // For OpenAI
}

// ProviderConfig holds the configuration needed for provider registry.
// This avoids import cycles by not importing the config package.
type ProviderConfig struct {
	AnthropicAPIKey string
	OllamaHost      string
	OllamaModel     string
	OpenAIAPIKey    string
	OpenAIBaseURL   string
	OpenAIModel     string
	OpenAIOrg       string
}

// ProviderRegistry manages LLM provider selection and configuration resolution.
// Client creation and caching is handled by the caller to avoid import cycles.
type ProviderRegistry struct {
	enabledProviders map[string]bool // Set of enabled providers
	mu               sync.RWMutex
	config           *ProviderConfig
}

// NewProviderRegistry creates a new ProviderRegistry with the given config and enabled providers.
func NewProviderRegistry(providerConfig *ProviderConfig, enabledProviders []string) *ProviderRegistry {
	enabledMap := make(map[string]bool)
	for _, p := range enabledProviders {
		enabledMap[p] = true
	}

	return &ProviderRegistry{
		enabledProviders: enabledMap,
		config:           providerConfig,
	}
}

// IsProviderEnabled checks if a provider is in the enabled providers list.
func (r *ProviderRegistry) IsProviderEnabled(provider string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.enabledProviders[provider]
}

// IsProviderConfigured checks if a provider has the required configuration (API keys, hosts, etc.).
func (r *ProviderRegistry) IsProviderConfigured(provider string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.isProviderConfiguredUnlocked(provider)
}

// ResolveAgentLLMConfig resolves an agent's LLM configuration using preference-based selection.
// It returns a ClientKey for the first available provider from the agent's preference list.
func (r *ProviderRegistry) ResolveAgentLLMConfig(agentID string, agentCfg AgentLLMConfig) (*ClientKey, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// If agent has LLM preferences, iterate through them
	if len(agentCfg.LLMPreferences) > 0 {
		var attemptedProviders []string
		for _, pref := range agentCfg.LLMPreferences {
			attemptedProviders = append(attemptedProviders, pref.Provider)

			// Check if provider is enabled
			if !r.enabledProviders[pref.Provider] {
				continue
			}

			// Check if provider is configured
			if !r.isProviderConfiguredUnlocked(pref.Provider) {
				continue
			}

			// Resolve provider-specific config
			key, err := r.resolveProviderConfig(pref.Provider, pref.Model)
			if err != nil {
				// Log warning and continue to next preference
				continue
			}

			return key, nil
		}

		return nil, fmt.Errorf("agent %s: no available provider from preferences %v (enabled: %v)", agentID, attemptedProviders, r.getEnabledProvidersList())
	}

	// Agent has no LLM preferences - use first enabled provider
	// Don't use agent's model field as it may be provider-specific (e.g., "claude-haiku-4-5" won't work with Ollama)
	if len(r.enabledProviders) == 0 {
		return nil, fmt.Errorf("no providers enabled")
	}

	// Get first enabled provider
	var firstProvider string
	for p := range r.enabledProviders {
		firstProvider = p
		break
	}

	if !r.isProviderConfiguredUnlocked(firstProvider) {
		return nil, fmt.Errorf("agent %s: first enabled provider %s is not configured", agentID, firstProvider)
	}

	// Don't use agent's model field - it may be provider-specific
	// Use provider's default model instead
	key, err := r.resolveProviderConfig(firstProvider, "")
	if err != nil {
		return nil, fmt.Errorf("agent %s: failed to resolve config for provider %s: %w", agentID, firstProvider, err)
	}

	return key, nil
}

// isProviderConfiguredUnlocked is the unlocked version of IsProviderConfigured.
// Must be called with r.mu already locked.
func (r *ProviderRegistry) isProviderConfiguredUnlocked(provider string) bool {
	switch provider {
	case providerAnthropic:
		// Check config only
		return r.config.AnthropicAPIKey != ""
	case providerOllama:
		// Ollama doesn't require API key, just needs host (which has a default)
		return true
	case providerOpenAI:
		// Check config first, then environment
		apiKey := r.config.OpenAIAPIKey
		if apiKey == "" {
			apiKey = os.Getenv("OPENAI_API_KEY")
		}
		return apiKey != ""
	default:
		return false
	}
}

// resolveProviderConfig resolves provider-specific configuration and returns a ClientKey.
func (r *ProviderRegistry) resolveProviderConfig(provider, modelOverride string) (*ClientKey, error) {
	key := &ClientKey{
		Provider: provider,
		Model:    modelOverride,
	}

	switch provider {
	case providerAnthropic:
		// Get API key from config
		if r.config.AnthropicAPIKey == "" {
			return nil, fmt.Errorf("anthropic API key not configured")
		}
		key.APIKey = r.config.AnthropicAPIKey
		// If model not specified, use a default (will be overridden by agent's model field if present)
		if key.Model == "" {
			key.Model = "claude-haiku-4-5" // Default Anthropic model
		}

	case providerOllama:
		// Get host from config or environment
		host := r.config.OllamaHost
		if host == "" {
			host = os.Getenv("OLLAMA_HOST")
		}
		if host == "" {
			host = "http://localhost:11434" // Default
		}
		key.Host = host

		// Get model from config or environment, or use override
		defaultModel := r.config.OllamaModel
		if defaultModel == "" {
			defaultModel = os.Getenv("OLLAMA_MODEL")
		}
		// Use model override if provided, otherwise use default from config
		// Note: modelOverride is already set in key.Model at the start of the function
		if key.Model == "" {
			key.Model = defaultModel
		}
		// Ensure we have a model - if still empty, this is an error
		if key.Model == "" {
			return nil, fmt.Errorf("ollama model not specified and no default configured")
		}

	case providerOpenAI:
		// Get API key from config or environment
		apiKey := r.config.OpenAIAPIKey
		if apiKey == "" {
			apiKey = os.Getenv("OPENAI_API_KEY")
		}
		if apiKey == "" {
			return nil, fmt.Errorf("openai API key not configured")
		}
		key.APIKey = apiKey

		// Get base URL from config or environment
		baseURL := r.config.OpenAIBaseURL
		if baseURL == "" {
			baseURL = os.Getenv("OPENAI_BASE_URL")
		}
		key.BaseURL = baseURL

		// Get organization from config or environment
		org := r.config.OpenAIOrg
		if org == "" {
			org = os.Getenv("OPENAI_ORG_ID")
		}
		key.Organization = org

		// Get model from config or environment, or use override
		defaultModel := r.config.OpenAIModel
		if defaultModel == "" {
			defaultModel = os.Getenv("OPENAI_MODEL")
		}
		// Use model override if provided, otherwise use default from config
		if key.Model == "" {
			key.Model = defaultModel
		}

	default:
		return nil, fmt.Errorf("unknown provider: %s", provider)
	}

	return key, nil
}

// getEnabledProvidersList returns a list of enabled providers (for error messages).
func (r *ProviderRegistry) getEnabledProvidersList() []string {
	var providers []string
	for p := range r.enabledProviders {
		providers = append(providers, p)
	}
	return providers
}
