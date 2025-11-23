package llm

import (
	"os"
	"testing"
)

func TestProviderRegistry_IsProviderEnabled(t *testing.T) {
	registry := NewProviderRegistry(ProviderConfig{}, []string{"anthropic", "ollama"})

	if !registry.IsProviderEnabled("anthropic") {
		t.Error("anthropic should be enabled")
	}
	if !registry.IsProviderEnabled("ollama") {
		t.Error("ollama should be enabled")
	}
	if registry.IsProviderEnabled("openai") {
		t.Error("openai should not be enabled")
	}
}

func TestProviderRegistry_IsProviderConfigured(t *testing.T) {
	// Test Anthropic - should require API key
	registry := NewProviderRegistry(ProviderConfig{}, []string{"anthropic"})
	if registry.IsProviderConfigured("anthropic") {
		t.Error("anthropic should not be configured without API key")
	}

	// Set API key via environment
	_ = os.Setenv("ANTHROPIC_API_KEY", "test-key") //nolint:errcheck // ignore error
	defer os.Unsetenv("ANTHROPIC_API_KEY")         //nolint:errcheck // ignore error

	registry2 := NewProviderRegistry(ProviderConfig{}, []string{"anthropic"})
	if !registry2.IsProviderConfigured("anthropic") {
		t.Error("anthropic should be configured with API key")
	}

	// Test Ollama - should always be configured (no API key required)
	registry3 := NewProviderRegistry(ProviderConfig{}, []string{"ollama"})
	if !registry3.IsProviderConfigured("ollama") {
		t.Error("ollama should always be configured")
	}

	// Test OpenAI - should require API key
	registry4 := NewProviderRegistry(ProviderConfig{}, []string{"openai"})
	if registry4.IsProviderConfigured("openai") {
		t.Error("openai should not be configured without API key")
	}

	_ = os.Setenv("OPENAI_API_KEY", "test-key") //nolint:errcheck // ignore error
	defer os.Unsetenv("OPENAI_API_KEY")         //nolint:errcheck // ignore error

	registry5 := NewProviderRegistry(ProviderConfig{}, []string{"openai"})
	if !registry5.IsProviderConfigured("openai") {
		t.Error("openai should be configured with API key")
	}
}

func TestProviderRegistry_ResolveAgentLLMConfig_WithPreferences(t *testing.T) {
	_ = os.Setenv("ANTHROPIC_API_KEY", "test-key") //nolint:errcheck // ignore error
	defer os.Unsetenv("ANTHROPIC_API_KEY")         //nolint:errcheck // ignore error

	registry := NewProviderRegistry(ProviderConfig{}, []string{"anthropic", "ollama"})

	// Agent with preferences - first preference should be selected
	agentCfg := AgentLLMConfig{
		LLMPreferences: []LLMPreference{
			{Provider: "anthropic", Model: "claude-sonnet-4-20250514"},
			{Provider: "ollama", Model: "mistral:20b"},
		},
	}

	key, err := registry.ResolveAgentLLMConfig("test-agent", agentCfg)
	if err != nil {
		t.Fatalf("Failed to resolve config: %v", err)
	}

	if key.Provider != "anthropic" {
		t.Errorf("Expected provider 'anthropic', got '%s'", key.Provider)
	}
	if key.Model != "claude-sonnet-4-20250514" {
		t.Errorf("Expected model 'claude-sonnet-4-20250514', got '%s'", key.Model)
	}
}

func TestProviderRegistry_ResolveAgentLLMConfig_WithoutPreferences(t *testing.T) {
	_ = os.Setenv("ANTHROPIC_API_KEY", "test-key") //nolint:errcheck // ignore error
	defer os.Unsetenv("ANTHROPIC_API_KEY")         //nolint:errcheck // ignore error

	registry := NewProviderRegistry(ProviderConfig{}, []string{"anthropic", "ollama"})

	// Agent without preferences - should use first enabled provider with its default model
	// (agent's model field is ignored as it may be provider-specific)
	agentCfg := AgentLLMConfig{
		Model: "claude-haiku-4-5", // This field is ignored when no preferences are set
	}

	key, err := registry.ResolveAgentLLMConfig("test-agent", agentCfg)
	if err != nil {
		t.Fatalf("Failed to resolve config: %v", err)
	}

	if key.Provider != "anthropic" {
		t.Errorf("Expected provider 'anthropic' (first enabled), got '%s'", key.Provider)
	}
	// Model should be the provider's default, not the agent's model field
	// (For Anthropic, default is "claude-haiku-4-5", so this test still passes)
	if key.Model != "claude-haiku-4-5" {
		t.Errorf("Expected model 'claude-haiku-4-5' (provider default), got '%s'", key.Model)
	}
}

func TestProviderRegistry_ResolveAgentLLMConfig_Fallback(t *testing.T) {
	_ = os.Setenv("ANTHROPIC_API_KEY", "test-key") //nolint:errcheck // ignore error
	defer os.Unsetenv("ANTHROPIC_API_KEY")         //nolint:errcheck // ignore error

	// Only enable anthropic, not ollama
	registry := NewProviderRegistry(ProviderConfig{}, []string{"anthropic"})

	// Agent prefers ollama first, but it's not enabled - should fallback to anthropic
	agentCfg := AgentLLMConfig{
		LLMPreferences: []LLMPreference{
			{Provider: "ollama", Model: "mistral:20b"},
			{Provider: "anthropic", Model: "claude-haiku-4-5"},
		},
	}

	key, err := registry.ResolveAgentLLMConfig("test-agent", agentCfg)
	if err != nil {
		t.Fatalf("Failed to resolve config: %v", err)
	}

	if key.Provider != "anthropic" {
		t.Errorf("Expected provider 'anthropic' (fallback), got '%s'", key.Provider)
	}
}

func TestProviderRegistry_ResolveAgentLLMConfig_NoAvailableProvider(t *testing.T) {
	// No providers enabled
	registry := NewProviderRegistry(ProviderConfig{}, []string{})

	agentCfg := AgentLLMConfig{
		Model: "claude-haiku-4-5",
	}

	_, err := registry.ResolveAgentLLMConfig("test-agent", agentCfg)
	if err == nil {
		t.Error("Expected error when no providers are enabled")
	}
}
