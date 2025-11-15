# MCP Integration Layer

### Goals

Add ability to call external APIs (Gmail/Calendar/etc.) through MCP servers, not native code.

### Deliverables

- New `staff/mcp/` package:

  - `client.go`: connect to MCP server
  - `tools.go`: call tool with JSON payload
  - `adapter.go`: convert MCP tool signatures → local tool handlers

### Required Changes

- Update ToolProvider:

  - Allow registration of MCP-exposed tools from config

  - Register tool names that do **not** contain `.` by namespacing them, e.g.:

    ```
    gmail_messages_list
    gmail_messages_get
    gmail_messages_modify
    googlecalendar_events_list
    ```

  - Mapping from config names such as `"gmail.messages.list"` → `"gmail_messages_list"`

- Update agents.yaml:

  ```
  tools:
    - gmail_messages_list
    - gmail_messages_get
  ```

### Tests

- Call MCP tool → data returned
- Tool result stored in conversation logs
- Agent workflow (email triage) uses MCP tools
