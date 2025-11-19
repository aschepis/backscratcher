# LLM Provider Abstraction Plan

## Phase 1 – Introduce LLM Abstraction Layer

- Create a new `staff/llm` package that defines provider-neutral request/response structs (messages, tool calls, streaming deltas) plus interfaces such as `Client`, `Streamer`, and `ToolCodec`.
- Add adapters that turn existing `agent.ToolProvider` specs and history (currently `anthropic.MessageParam`) into the new `llm` structures, and update `staff/ui/service.go` + `staff/ui/tui/*.go` to depend only on the shared types.
- Provide shared error types and pluggable middleware (logging, retry, rate-limit hooks) that future LLM drivers can plug into without forcing `agent.AgentRunner` to depend on provider SDKs directly.

## Phase 2 – Port Anthropics Flow to the Abstraction

- Move the logic in `staff/agent/runner.go` that prepares messages, handles tool executions, and manages retries/rate limits into provider-agnostic helpers; the runner should call an injected `llm.Client` for both non-streaming and streaming paths.
- Implement `llm/anthropic` using the existing `anthropic-sdk-go` calls, translating between shared types and Anthropic-specific structs, and reusing rate-limit handling + prompt compression.
- Update `staff/main.go`, `staff/agent/crew.go`, and `staff/ui/service.go` to construct Anthropics clients through a factory (e.g., `llm.NewProviderRegistry`) so current behavior is unchanged when no other providers are configured.

## Phase 3 – Add Ollama LLM Implementation

- Build `llm/ollama` that can call the local Ollama HTTP API (both `/api/chat` and `/api/generate` as needed), mapping tool/function calls to JSON schemas supported by Ollama.
- Support streaming by consuming Ollama’s SSE/line-delimited responses and emitting the shared streaming events expected by `AgentRunner.RunStream`.
- Expose configuration in `staff/config` (host, model defaults, authentication if any) and wire environment variables (e.g., `OLLAMA_HOST`) plus per-agent overrides.

## Phase 4 – Add OpenAI LLM Implementation

- Implement `llm/openai` using the Chat Completions (or Responses) API with tools/function calling, including streaming delta handling and JSON tool args validation.
- Handle provider-specific constraints (e.g., `response_format`, token limits) within the driver while keeping the abstraction consistent with Anthropics and Ollama.
- Extend configuration loading to accept OpenAI credentials (env vars + config file), ensure retries/backoff align with OpenAI error semantics, and document the required scopes.

## Phase 5 – Per-Agent Provider & Model Selection

- Extend `agent.AgentConfig` and `agents.yaml` to include a provider reference (e.g., `llm:` block with `provider`, `model`, `temperature`, optional `api_key_ref`), defaulting to Anthropic if omitted.
- Modify `agent.Crew` to resolve each agent’s LLM config into a concrete client instance (or client+pool) using the provider registry, caching shared clients when possible, and update `NewAgentRunner` to receive the provider-specific client directly.
- Update persistence, scheduling, and UI layers (`staff/ui/service.go`, `staff/ui/tui/*.go`) to display the active provider/model, and add regression tests covering mixed-provider crews plus documentation updates in `staff/README.md`.
