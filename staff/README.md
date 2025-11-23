# Staff - Your personal executive staff

## LLM Provider Configuration

Staff supports multiple LLM providers (Anthropic, Ollama, OpenAI) with per-agent preference-based selection and automatic fallback.

### Global Provider Configuration

Enable providers globally using the `llm_providers` array in your config file (`~/.staffd/config.yaml`) or `agents.yaml`:

```yaml
llm_providers:
  - anthropic
  - ollama
  - openai
```

**Legacy Support**: The old `llm_provider` (singular) field is still supported and will be automatically converted to an array.

### Per-Agent LLM Preferences

Agents can specify their preferred provider/model combinations in order of preference. The system will use the first available provider from the agent's preference list that matches the enabled providers.

```yaml
agents:
  interviewer:
    llm:  # Ordered preferences - uses first available
      - provider: anthropic
        model: claude-sonnet-4-20250514
        temperature: 0.7
      - provider: ollama
        model: mistral:20b
  
  config_agent:
    llm:
      - provider: ollama
        model: llama3.2:3b
      - provider: anthropic  # Fallback if Ollama unavailable
        model: claude-haiku-4-5
```

### Agents Without Preferences

Agents without `llm:` preferences will use the first enabled provider from the global `llm_providers` list, combined with their `model` field:

```yaml
agents:
  simple_agent:
    model: claude-haiku-4-5  # Uses first enabled provider (anthropic) with this model
```

### Migration Guide

**From legacy single provider:**
- Old: `llm_provider: "anthropic"` 
- New: `llm_providers: ["anthropic"]` (or just remove it, defaults to anthropic)

**Adding per-agent preferences:**
- Agents without `llm:` block continue to work using first enabled provider + their `model` field
- Gradually add `llm:` preferences as needed for specific agents

### Provider Configuration

Each provider requires specific configuration:

**Anthropic:**
- Set `ANTHROPIC_API_KEY` environment variable or `anthropic.api_key` in config

**Ollama:**
- Set `OLLAMA_HOST` environment variable (default: `http://localhost:11434`) or `ollama.host` in config
- Set `OLLAMA_MODEL` environment variable or `ollama.model` in config

**OpenAI:**
- Set `OPENAI_API_KEY` environment variable or `openai.api_key` in config
- Optionally set `OPENAI_BASE_URL`, `OPENAI_MODEL`, `OPENAI_ORG_ID` or corresponding config fields
