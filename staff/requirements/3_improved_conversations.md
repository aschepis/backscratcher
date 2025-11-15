# Upgrade Conversation Log for Tool Calls\*\*

_(Enhancement to existing `conversations` table.)_

### Goals

Support correct Anthropic tool replay by expanding conversation schema.

### Deliverables

Add columns:

```
tool_name TEXT NULL,   -- only for role=tool or assistant tool-call
```

the content column value should be stored as JSON for tool messages

### Required Changes

- Update transcript builder to reconstruct Anthropic message structures:

  - `assistant` → normal content
  - `assistant` tool call → JSON payload + tool_name
  - `tool` → raw tool result

- Implement helper functions:

  - `AppendAssistantMessage`
  - `AppendUserMessage`
  - `AppendToolCall`
  - `AppendToolResult`
  - `LoadThread(threadID) → []anthropic.MessageParam`

### Tests

- Tool call → stored properly
- Tool result → stored properly
- Full replay → correct message param list
