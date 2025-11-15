# MCP Integration Layer

### Goals

Add ability to call external APIs (Gmail/Calendar/etc.) through MCP servers, not native code.

# 🧩 **MCP SUPPORT REQUIREMENTS (for Go Staff System)**

## _STDIO & HTTP MCP with minimum code written by you_

---

# 1️⃣ **General Requirements**

### R1 — Support both MCP Transports

- **STDIO (child process execution)**
- **HTTP-based server (REST + JSON spec discovery)**

### R2 — Automatically discover MCP tools

- Via MCP STDIO handshake, which includes tool definitions
- Or via HTTP `.well-known/mcp.json`

### R3 — Schema-Driven Tool Registration

Every MCP tool (method) must be mapped into your in-process `ToolProvider` with:

- Name
- Description
- Input schema
- Output shapes (if provided)

### R4 — Dynamic Discovery

The Staff system must dynamically discover:

- MCP servers present in config
- Their supported tools
- Their required configuration fields
- Their optional features
- Their transport mechanism (HTTP or STDIO)

### R5 — Integration with Config Agent

The Config Agent must read the MCP tool metadata and config schema, and determine:

- Missing fields
- Required user prompts
- Files to write
- Auth flows needed

---

# 2️⃣ **STDIO MCP (Child Processes) — Requirements**

### R6 — STDIO MCP Execution Wrapper

The Staff system must be able to:

- Spawn an MCP server as a subprocess
- Redirect `stdin`, `stdout`, `stderr`
- Send JSON-RPC messages over STDIN
- Receive responses over STDOUT

### R7 — STDIO Handshake Support

Send:

```
client.handshake
```

Receive:

```
server.handshake
```

Parse:

- tools
- config_schema
- capabilities
- version

### R8 — JSON-RPC Protocol Support

You must implement (or reuse a library that implements):

- Request IDs
- Notifications
- Request/Response pattern
- Error formats

---

# 3️⃣ **HTTP MCP (Remote Servers) — Requirements**

### R9 — HTTP Discovery

Fetch:

```
GET /.well-known/mcp.json
```

Parse:

- tools
- config_schema
- auth
- capabilities

### R10 — HTTP Tool Invocation

Each tool invocation must be performed according to the MCP server’s HTTP spec:

- POST to appropriate endpoint
- JSON body matching schema
- Handle streaming if supported

(If server conforms to MCP spec, there's usually a `/mcp` or `/invoke` endpoint.)

---

# 4️⃣ **Tool Invocation Abstraction Layer**

### R11 — Unified MCP Client Interface

Define an interface:

```go
type MCPClient interface {
    ListTools() ([]ToolDefinition, error)
    InvokeTool(name string, input map[string]any) (map[string]any, error)
    Close() error
}
```

### R12 — Two concrete implementations:

- `StdioMCPClient`
- `HttpMCPClient`

---

# 5️⃣ **ToolProvider Integration Requirements**

### R13 — Register MCP tools under tool-safe names

Reminder: Anthropic cannot accept dots, so:

```
gmail.messages.list → gmail_messages_list
calendar.events.get → calendar_events_get
google.drive.copy → google_drive_copy
```

Must store a mapping table:

```
original_name → safe_name
safe_name → original_name
```

### R14 — Register schemas from MCP

`ToolProvider.RegisterSchema` must accept schemas parsed from MCP tool definitions.

### R15 — Register MCP invocation handler

Each tool in ToolProvider must direct to `MCPClient.InvokeTool` internally.

---

# 6️⃣ **Configuration Requirements**

### R16 — MCP Server Definition in YAML Config

Support both transport types, e.g.:

```yaml
mcp_servers:
  google:
    command: "/usr/local/bin/mcp-google" # STDIO
    config_file: "~/.staff/mcp/google.yaml"
  jira:
    url: "https://jira.mycompany.com/mcp" # HTTP
    config_file: "~/.staff/mcp/jira.yaml"
```

### R17 — Load and validate config schemas

The Config Agent must check:

- Required fields
- Missing fields
- User prompts
- Validation (via MCP ping or schema checks)

### R18 — Write config files using tools

Prefer:

- `filesystem_write_file`
- `notification_send`
- (Optional) `open_browser` tool for OAuth2

---

# 7️⃣ **Conversation Log Requirements**

### R19 — Support storing MCP tool calls

Conversation log must store:

- safe tool name
- original MCP method name
- input JSON
- output JSON
- streaming results if any

### R20 — Replay requirements

AgentRunner must reconstruct tool messages exactly.

---

# 8️⃣ **Error Handling Requirements**

### R21 — Detect MCP server failure

Handle cases where:

- process fails to start
- HTTP connection fails
- handshake errors
- malformed schemas

### R22 — Automatic restart for STDIO clients

Should restart child processes when needed.

### R23 — Rate limiting

Honor MCP-specific rate limits (if provided in metadata).

---

# 9️⃣ **Security Requirements**

### R24 — Store secrets securely

Config Agent writes credential files to:

```
~/.staff/mcp/SERVERNAME.yaml
```

Permissions:

```
chmod 600
```

### R25 — Never send secrets to agents accidentally

Config Agent must store secrets in:

- config files
- NOT long-term memory

---

# 1️⃣0️⃣ **Testing Requirements**

### R26 — Provide a “fake MCP server” for tests

A mock MCP server that:

- prints handshake
- supports sample tools
- returns known JSON

### R27 — Separate unit tests for STDIO & HTTP clients

### R28 — End-to-end test:

Agent → ToolProvider → MCP → tool result → conversation log.

---

# 🧰 **Recommended Existing Go Libraries (To Reduce Your Code)**

These libraries will massively cut the implementation workload:

---

## ⭐ 1. **JSON-RPC libraries**

### **github.com/sourcegraph/jsonrpc2**

Best for streams (STDIO):

- Bidirectional message passing
- Supports cancelation
- Well-tested

Used widely in LSP servers.

### **github.com/vmihailenco/msgpack/v5 + your own framing**

If you want speed.

---

## ⭐ 2. **Process Control / IPC**

### **os/exec (standard library)**

Perfectly adequate; use:

```go
cmd := exec.Command(...)
stdin, _ := cmd.StdinPipe()
stdout, _ := cmd.StdoutPipe()
cmd.Start()
```

---

## ⭐ 3. **Schema Handling**

### **github.com/santhosh-tekuri/jsonschema/v5**

Full JSON Schema validator; you can validate inputs/outputs automatically.

---

## ⭐ 4. **HTTP Client + JSON**

Standard library suffices:

- `net/http`
- `encoding/json`

Use `json.Decoder` to stream.

---

## ⭐ 5. **Cron-like parsing**

For schedule parsing:

### **github.com/robfig/cron/v3**

Already excellent, well-tested.

---

## ⭐ 6. **YAML config**

### **gopkg.in/yaml.v3**

---

## ⭐ 7. **SQLite**

### **modernc.org/sqlite**

or

### **github.com/mattn/go-sqlite3**

You are already using SQLite, so you're set.

---

## ⭐ 8. **Structured Logging (Optional)**

### `zap`

### `zerolog`

Better than standard library for debugging MCP interactions.

---

# 🚀 **Summary: What You Actually Need to Build**

Given the above tools, your custom code needed is _surprisingly small_:

### You must implement:

1. A thin `MCPClient` interface
2. Two small implementations:

   - `StdioMCPClient` (JSON-RPC via pipes)
   - `HttpMCPClient` (HTTP POST)

3. Discovery logic
4. Config Agent (agent + tool definitions)
5. Dynamic tool registration
6. Minimal glue code around JSON schema validation

**Everything else is either already done in your project or available via high-quality Go libraries.**

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
