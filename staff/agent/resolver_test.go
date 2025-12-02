package agent

import (
	"testing"

	"github.com/aschepis/backscratcher/staff/config"
	"github.com/aschepis/backscratcher/staff/llm"
	"github.com/samber/lo"
)

func TestResolveLLMFromConfig(t *testing.T) {
	tests := []struct {
		name     string
		cfg      *config.AgentConfig
		expected ResolvedLLMInfo
	}{
		{
			name: "single LLM preference with anthropic provider",
			cfg: &config.AgentConfig{
				LLM: []config.LLMPreference{
					{Provider: llm.ProviderAnthropic, Model: "claude-sonnet-4-20250514"},
				},
			},
			expected: ResolvedLLMInfo{
				Provider: llm.ProviderAnthropic,
				Model:    "claude-sonnet-4-20250514",
			},
		},
		{
			name: "single LLM preference with ollama provider",
			cfg: &config.AgentConfig{
				LLM: []config.LLMPreference{
					{Provider: llm.ProviderOllama, Model: "mistral:20b"},
				},
			},
			expected: ResolvedLLMInfo{
				Provider: llm.ProviderOllama,
				Model:    "mistral:20b",
			},
		},
		{
			name: "single LLM preference with openai provider",
			cfg: &config.AgentConfig{
				LLM: []config.LLMPreference{
					{Provider: llm.ProviderOpenAI, Model: "gpt-4"},
				},
			},
			expected: ResolvedLLMInfo{
				Provider: llm.ProviderOpenAI,
				Model:    "gpt-4",
			},
		},
		{
			name: "single LLM preference with empty model",
			cfg: &config.AgentConfig{
				LLM: []config.LLMPreference{
					{Provider: llm.ProviderAnthropic, Model: ""},
				},
			},
			expected: ResolvedLLMInfo{
				Provider: llm.ProviderAnthropic,
				Model:    "",
			},
		},
		{
			name: "multiple LLM preferences - uses first one",
			cfg: &config.AgentConfig{
				LLM: []config.LLMPreference{
					{Provider: llm.ProviderAnthropic, Model: "claude-sonnet-4-20250514"},
					{Provider: llm.ProviderOllama, Model: "mistral:20b"},
					{Provider: llm.ProviderOpenAI, Model: "gpt-4"},
				},
			},
			expected: ResolvedLLMInfo{
				Provider: llm.ProviderAnthropic,
				Model:    "claude-sonnet-4-20250514",
			},
		},
		{
			name: "empty LLM preferences slice - returns default",
			cfg: &config.AgentConfig{
				LLM: []config.LLMPreference{},
			},
			expected: ResolvedLLMInfo{
				Provider: llm.ProviderAnthropic,
				Model:    "",
			},
		},
		{
			name: "nil LLM preferences - returns default",
			cfg: &config.AgentConfig{
				LLM: nil,
			},
			expected: ResolvedLLMInfo{
				Provider: llm.ProviderAnthropic,
				Model:    "",
			},
		},
		{
			name: "config with other fields but no LLM preferences",
			cfg: &config.AgentConfig{
				ID:       "test-agent",
				Name:     "Test Agent",
				Disabled: false,
				LLM:      nil,
			},
			expected: ResolvedLLMInfo{
				Provider: llm.ProviderAnthropic,
				Model:    "",
			},
		},
		{
			name: "LLM preference with temperature and APIKeyRef (should be ignored)",
			cfg: &config.AgentConfig{
				LLM: []config.LLMPreference{
					{
						Provider:    llm.ProviderOllama,
						Model:       "llama3.2:3b",
						Temperature: lo.ToPtr(0.7),
						APIKeyRef:   "key-ref-1",
					},
				},
			},
			expected: ResolvedLLMInfo{
				Provider: llm.ProviderOllama,
				Model:    "llama3.2:3b",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ResolveLLMFromConfig(tt.cfg)

			if result.Provider != tt.expected.Provider {
				t.Errorf("Provider: got %q, want %q", result.Provider, tt.expected.Provider)
			}

			if result.Model != tt.expected.Model {
				t.Errorf("Model: got %q, want %q", result.Model, tt.expected.Model)
			}
		})
	}
}
