# Memory Normalization Tool (`memory_normalize`)\*\*

### Goals

Add ability for agents to convert raw user statements into structured memories.

### Deliverables

- New MCP-safe tool name: `memory_normalize`
- Anthropic-backed Normalizer that returns:

  - normalized text
  - memory type
  - tags

- Integration with `StorePersonalMemory`

### Required Changes

- `memory/normalizer.go`
- `tools/memory_normalize.go`
- Add schema registration in ToolProvider

### Tests

- Given raw text → normalized output
- Saving normalized memory works
- Agent calls memory_normalize correctly
