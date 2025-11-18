# Staff Daemon Architecture Plan

This document outlines the plan to extract the staff functionality into a standalone daemon with a gRPC API, enabling multiple interfaces (TUI, web, mobile) to connect to the same backend.

## Goals

1. **Separation of concerns** - Decouple UI from agent execution
2. **Multiple interfaces** - Enable TUI, web UI, and other clients
3. **Consistent state** - Single source of truth for agent state
4. **Streaming support** - Real-time agent response streaming via gRPC
5. **Future extensibility** - gRPC-gateway for REST/JSON API later

---

## Architecture Overview

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│   TUI       │     │   Web UI    │     │   Mobile    │
│  (tview)    │     │  (future)   │     │  (future)   │
└──────┬──────┘     └──────┬──────┘     └──────┬──────┘
       │                   │                   │
       │              gRPC (HTTP/2)            │
       └───────────────────┼───────────────────┘
                           │
                    ┌──────▼──────┐
                    │   staffd    │
                    │   daemon    │
                    ├─────────────┤
                    │  gRPC Server│
                    │  - Chat     │
                    │  - Agents   │
                    │  - Memory   │
                    │  - Inbox    │
                    ├─────────────┤
                    │   Crew      │
                    │   Runners   │
                    │   Tools     │
                    │   MCP       │
                    │   Scheduler │
                    ├─────────────┤
                    │   SQLite    │
                    └─────────────┘
```

---

## Phase 1: Project Restructuring

### New Directory Structure

```
staff/
├── cmd/
│   ├── staffd/           # Daemon binary
│   │   └── main.go
│   └── staff/            # CLI/TUI client binary
│       └── main.go
│
├── api/
│   └── proto/
│       ├── staff.proto   # gRPC service definitions
│       └── buf.yaml      # Buf configuration
│
├── pkg/
│   ├── server/           # gRPC server implementation
│   │   ├── server.go
│   │   ├── chat.go       # ChatService implementation
│   │   ├── agents.go     # AgentService implementation
│   │   ├── memory.go     # MemoryService implementation
│   │   └── inbox.go      # InboxService implementation
│   │
│   ├── client/           # gRPC client library
│   │   ├── client.go     # Client wrapper
│   │   └── stream.go     # Streaming helpers
│   │
│   └── daemon/           # Daemon lifecycle
│       └── daemon.go
│
├── internal/             # Existing packages (renamed)
│   ├── agent/
│   ├── config/
│   ├── contextkeys/
│   ├── logger/
│   ├── mcp/
│   ├── memory/
│   ├── migrations/
│   ├── runtime/
│   └── tools/
│
└── ui/
    └── tui/              # TUI (now uses gRPC client)
        ├── app.go
        ├── chat.go
        ├── menu.go
        └── settings.go
```

### Tasks

- [ ] Create `cmd/staffd/` and `cmd/staff/` directories
- [ ] Move existing packages to `internal/`
- [ ] Create `pkg/server/`, `pkg/client/`, `pkg/daemon/`
- [ ] Create `api/proto/` directory
- [ ] Update import paths throughout codebase

---

## Phase 2: gRPC Service Definitions

### Proto File: `api/proto/staff.proto`

```protobuf
syntax = "proto3";

package staff.v1;

option go_package = "github.com/yourusername/backscratcher/staff/api/staffpb";

import "google/protobuf/timestamp.proto";
import "google/protobuf/struct.proto";

// =============================================================================
// Chat Service - Core agent interaction
// =============================================================================

service ChatService {
  // Send a message and receive streaming response
  rpc SendMessage(SendMessageRequest) returns (stream ChatEvent);

  // Send a message and wait for complete response (non-streaming)
  rpc SendMessageSync(SendMessageRequest) returns (SendMessageResponse);

  // Get or create a thread for an agent
  rpc GetOrCreateThread(GetOrCreateThreadRequest) returns (GetOrCreateThreadResponse);

  // Load conversation history for a thread
  rpc LoadThread(LoadThreadRequest) returns (LoadThreadResponse);
}

message SendMessageRequest {
  string agent_id = 1;
  string thread_id = 2;
  string message = 3;
  repeated MessageParam history = 4;  // Optional override
}

message ChatEvent {
  oneof event {
    TextDelta text_delta = 1;
    ToolUse tool_use = 2;
    ToolResult tool_result = 3;
    DebugMessage debug = 4;
    MessageComplete complete = 5;
    ErrorEvent error = 6;
  }
}

message TextDelta {
  string text = 1;
}

message ToolUse {
  string tool_id = 1;
  string tool_name = 2;
  string input_json = 3;
}

message ToolResult {
  string tool_id = 1;
  string tool_name = 2;
  string result_json = 3;
  bool is_error = 4;
}

message DebugMessage {
  string message = 1;
}

message MessageComplete {
  string full_response = 1;
  string stop_reason = 2;
}

message ErrorEvent {
  string message = 1;
  string code = 2;
}

message SendMessageResponse {
  string response = 1;
  string stop_reason = 2;
}

message MessageParam {
  string role = 1;  // user, assistant, tool
  string content = 2;
  string tool_id = 3;
  string tool_name = 4;
}

message GetOrCreateThreadRequest {
  string agent_id = 1;
}

message GetOrCreateThreadResponse {
  string thread_id = 1;
  bool created = 2;
}

message LoadThreadRequest {
  string agent_id = 1;
  string thread_id = 2;
}

message LoadThreadResponse {
  repeated MessageParam messages = 1;
}

// =============================================================================
// Agent Service - Agent management and introspection
// =============================================================================

service AgentService {
  // List all configured agents
  rpc ListAgents(ListAgentsRequest) returns (ListAgentsResponse);

  // Get details for a specific agent
  rpc GetAgent(GetAgentRequest) returns (GetAgentResponse);

  // Get current state of an agent
  rpc GetAgentState(GetAgentStateRequest) returns (GetAgentStateResponse);

  // Get statistics for an agent
  rpc GetAgentStats(GetAgentStatsRequest) returns (GetAgentStatsResponse);

  // Stream agent state changes (for live updates)
  rpc WatchAgentStates(WatchAgentStatesRequest) returns (stream AgentStateChange);
}

message ListAgentsRequest {}

message ListAgentsResponse {
  repeated AgentInfo agents = 1;
}

message AgentInfo {
  string id = 1;
  string name = 2;
  string model = 3;
  string state = 4;
  repeated string tools = 5;
  string schedule = 6;
  bool disabled = 7;
}

message GetAgentRequest {
  string agent_id = 1;
}

message GetAgentResponse {
  AgentInfo agent = 1;
  string system_prompt = 2;
  int64 max_tokens = 3;
}

message GetAgentStateRequest {
  string agent_id = 1;
}

message GetAgentStateResponse {
  string state = 1;
  google.protobuf.Timestamp next_wake = 2;
  google.protobuf.Timestamp updated_at = 3;
}

message GetAgentStatsRequest {
  string agent_id = 1;
}

message GetAgentStatsResponse {
  int64 execution_count = 1;
  int64 failure_count = 2;
  int64 wakeup_count = 3;
  google.protobuf.Timestamp last_execution = 4;
  google.protobuf.Timestamp last_failure = 5;
  string last_failure_message = 6;
}

message WatchAgentStatesRequest {
  repeated string agent_ids = 1;  // Empty = all agents
}

message AgentStateChange {
  string agent_id = 1;
  string old_state = 2;
  string new_state = 3;
  google.protobuf.Timestamp timestamp = 4;
}

// =============================================================================
// Inbox Service - Notifications from agents
// =============================================================================

service InboxService {
  // List inbox items
  rpc ListInboxItems(ListInboxItemsRequest) returns (ListInboxItemsResponse);

  // Archive an inbox item
  rpc ArchiveInboxItem(ArchiveInboxItemRequest) returns (ArchiveInboxItemResponse);

  // Stream new inbox items as they arrive
  rpc WatchInbox(WatchInboxRequest) returns (stream InboxItem);
}

message ListInboxItemsRequest {
  bool include_archived = 1;
}

message ListInboxItemsResponse {
  repeated InboxItem items = 1;
}

message InboxItem {
  int64 id = 1;
  string agent_id = 2;
  string title = 3;
  string body = 4;
  string priority = 5;
  bool archived = 6;
  google.protobuf.Timestamp created_at = 7;
}

message ArchiveInboxItemRequest {
  int64 inbox_id = 1;
}

message ArchiveInboxItemResponse {
  bool success = 1;
}

message WatchInboxRequest {}

// =============================================================================
// Memory Service - Memory search and management
// =============================================================================

service MemoryService {
  // Search memories
  rpc SearchMemory(SearchMemoryRequest) returns (SearchMemoryResponse);

  // Store a memory item
  rpc StoreMemory(StoreMemoryRequest) returns (StoreMemoryResponse);

  // List artifacts
  rpc ListArtifacts(ListArtifactsRequest) returns (ListArtifactsResponse);
}

message SearchMemoryRequest {
  string query = 1;
  string scope = 2;         // "agent" or "global"
  string agent_id = 3;      // Required if scope = "agent"
  repeated string types = 4; // fact, episode, profile, doc_ref
  int32 limit = 5;
}

message SearchMemoryResponse {
  repeated MemoryItem items = 1;
}

message MemoryItem {
  int64 id = 1;
  string agent_id = 2;
  string scope = 3;
  string type = 4;
  string content = 5;
  double importance = 6;
  google.protobuf.Struct metadata = 7;
  google.protobuf.Timestamp created_at = 8;
}

message StoreMemoryRequest {
  string agent_id = 1;
  string scope = 2;
  string type = 3;
  string content = 4;
  double importance = 5;
  google.protobuf.Struct metadata = 6;
}

message StoreMemoryResponse {
  int64 id = 1;
}

message ListArtifactsRequest {
  string agent_id = 1;  // Optional filter
}

message ListArtifactsResponse {
  repeated Artifact artifacts = 1;
}

message Artifact {
  int64 id = 1;
  string agent_id = 2;
  string name = 3;
  string type = 4;
  string content = 5;
  google.protobuf.Timestamp created_at = 6;
  google.protobuf.Timestamp updated_at = 7;
}

// =============================================================================
// System Service - Daemon information and control
// =============================================================================

service SystemService {
  // Get daemon status and version
  rpc GetStatus(GetStatusRequest) returns (GetStatusResponse);

  // List available tools
  rpc ListTools(ListToolsRequest) returns (ListToolsResponse);

  // List MCP servers
  rpc ListMCPServers(ListMCPServersRequest) returns (ListMCPServersResponse);

  // Reload configuration (hot reload)
  rpc ReloadConfig(ReloadConfigRequest) returns (ReloadConfigResponse);
}

message GetStatusRequest {}

message GetStatusResponse {
  string version = 1;
  string status = 2;  // "running", "degraded"
  google.protobuf.Timestamp started_at = 3;
  int32 active_agents = 4;
  int32 connected_clients = 5;
}

message ListToolsRequest {
  string agent_id = 1;  // Optional: filter to tools available to this agent
}

message ListToolsResponse {
  repeated ToolInfo tools = 1;
}

message ToolInfo {
  string name = 1;
  string description = 2;
  string source = 3;  // "builtin", "mcp:{server}"
}

message ListMCPServersRequest {}

message ListMCPServersResponse {
  repeated MCPServerInfo servers = 1;
}

message MCPServerInfo {
  string name = 1;
  string transport = 2;  // "stdio", "http"
  string status = 3;     // "connected", "disconnected", "error"
  int32 tool_count = 4;
}

message ReloadConfigRequest {}

message ReloadConfigResponse {
  bool success = 1;
  string message = 2;
}
```

### Tasks

- [ ] Create `api/proto/staff.proto` with service definitions
- [ ] Set up buf.yaml for proto management
- [ ] Generate Go code with `buf generate`
- [ ] Create `api/staffpb/` package with generated code

---

## Phase 3: gRPC Server Implementation

### Server Structure

```go
// pkg/server/server.go
package server

type StaffServer struct {
    crew         *agent.Crew
    db           *sql.DB
    memoryRouter *memory.MemoryRouter

    // State change notifications
    stateChanges chan AgentStateChange
    inboxUpdates chan *InboxItem

    // Client tracking
    clients   map[string]*clientConn
    clientsMu sync.RWMutex
}

func NewStaffServer(crew *agent.Crew, db *sql.DB, router *memory.MemoryRouter) *StaffServer

func (s *StaffServer) Serve(addr string) error
func (s *StaffServer) Shutdown(ctx context.Context) error
```

### Service Implementations

#### ChatService

```go
// pkg/server/chat.go
func (s *StaffServer) SendMessage(req *pb.SendMessageRequest, stream pb.ChatService_SendMessageServer) error {
    ctx := stream.Context()

    // Convert history from proto to internal format
    history := convertHistory(req.History)

    // Stream callback sends events to client
    streamCallback := func(text string) error {
        return stream.Send(&pb.ChatEvent{
            Event: &pb.ChatEvent_TextDelta{
                TextDelta: &pb.TextDelta{Text: text},
            },
        })
    }

    // Debug callback
    debugCallback := func(msg string) {
        stream.Send(&pb.ChatEvent{
            Event: &pb.ChatEvent_Debug{
                Debug: &pb.DebugMessage{Message: msg},
            },
        })
    }

    // Run agent with streaming
    response, err := s.crew.RunStream(ctx, req.AgentId, req.ThreadId, req.Message, history, streamCallback)
    if err != nil {
        stream.Send(&pb.ChatEvent{
            Event: &pb.ChatEvent_Error{
                Error: &pb.ErrorEvent{Message: err.Error()},
            },
        })
        return err
    }

    // Send completion
    return stream.Send(&pb.ChatEvent{
        Event: &pb.ChatEvent_Complete{
            Complete: &pb.MessageComplete{FullResponse: response},
        },
    })
}
```

#### AgentService with State Watching

```go
// pkg/server/agents.go
func (s *StaffServer) WatchAgentStates(req *pb.WatchAgentStatesRequest, stream pb.AgentService_WatchAgentStatesServer) error {
    // Subscribe to state changes
    ch := s.subscribeStateChanges(req.AgentIds)
    defer s.unsubscribeStateChanges(ch)

    for {
        select {
        case <-stream.Context().Done():
            return stream.Context().Err()
        case change := <-ch:
            if err := stream.Send(change); err != nil {
                return err
            }
        }
    }
}
```

### State Change Notifications

The daemon needs to broadcast state changes to watching clients:

```go
// pkg/server/notifications.go
type notificationHub struct {
    stateSubscribers map[chan *pb.AgentStateChange][]string
    inboxSubscribers map[chan *pb.InboxItem]struct{}
    mu               sync.RWMutex
}

func (h *notificationHub) broadcastStateChange(change *pb.AgentStateChange) {
    h.mu.RLock()
    defer h.mu.RUnlock()

    for ch, agentFilter := range h.stateSubscribers {
        if len(agentFilter) == 0 || contains(agentFilter, change.AgentId) {
            select {
            case ch <- change:
            default:
                // Channel full, skip
            }
        }
    }
}
```

### Tasks

- [ ] Implement `StaffServer` with all service handlers
- [ ] Implement `ChatService` with streaming support
- [ ] Implement `AgentService` with state watching
- [ ] Implement `InboxService` with live updates
- [ ] Implement `MemoryService`
- [ ] Implement `SystemService`
- [ ] Add notification hub for real-time updates
- [ ] Add graceful shutdown handling

---

## Phase 4: Client Library

### Client Wrapper

```go
// pkg/client/client.go
package client

type StaffClient struct {
    conn   *grpc.ClientConn
    chat   pb.ChatServiceClient
    agents pb.AgentServiceClient
    inbox  pb.InboxServiceClient
    memory pb.MemoryServiceClient
    system pb.SystemServiceClient
}

func Connect(addr string, opts ...grpc.DialOption) (*StaffClient, error) {
    conn, err := grpc.Dial(addr, opts...)
    if err != nil {
        return nil, err
    }

    return &StaffClient{
        conn:   conn,
        chat:   pb.NewChatServiceClient(conn),
        agents: pb.NewAgentServiceClient(conn),
        inbox:  pb.NewInboxServiceClient(conn),
        memory: pb.NewMemoryServiceClient(conn),
        system: pb.NewSystemServiceClient(conn),
    }, nil
}

func (c *StaffClient) Close() error {
    return c.conn.Close()
}
```

### Streaming Helpers

```go
// pkg/client/stream.go
package client

// ChatStream wraps the gRPC stream with convenience methods
type ChatStream struct {
    stream pb.ChatService_SendMessageClient
}

func (s *ChatStream) Next() (*pb.ChatEvent, error) {
    return s.stream.Recv()
}

// SendMessage returns a stream for receiving events
func (c *StaffClient) SendMessage(ctx context.Context, agentID, threadID, message string) (*ChatStream, error) {
    stream, err := c.chat.SendMessage(ctx, &pb.SendMessageRequest{
        AgentId:  agentID,
        ThreadId: threadID,
        Message:  message,
    })
    if err != nil {
        return nil, err
    }
    return &ChatStream{stream: stream}, nil
}

// SendMessageCallback sends a message and calls the callback for each event
func (c *StaffClient) SendMessageCallback(ctx context.Context, agentID, threadID, message string,
    onText func(string), onComplete func(string)) error {

    stream, err := c.SendMessage(ctx, agentID, threadID, message)
    if err != nil {
        return err
    }

    for {
        event, err := stream.Next()
        if err == io.EOF {
            return nil
        }
        if err != nil {
            return err
        }

        switch e := event.Event.(type) {
        case *pb.ChatEvent_TextDelta:
            if onText != nil {
                onText(e.TextDelta.Text)
            }
        case *pb.ChatEvent_Complete:
            if onComplete != nil {
                onComplete(e.Complete.FullResponse)
            }
        case *pb.ChatEvent_Error:
            return fmt.Errorf("agent error: %s", e.Error.Message)
        }
    }
}
```

### Tasks

- [ ] Create `StaffClient` wrapper with all service clients
- [ ] Add `ChatStream` helper for streaming
- [ ] Add callback-based streaming method
- [ ] Add state watching helpers
- [ ] Add inbox watching helpers
- [ ] Add connection retry logic
- [ ] Add keepalive configuration

---

## Phase 5: Daemon Binary

### Main Entry Point

```go
// cmd/staffd/main.go
package main

func main() {
    // Parse flags
    var (
        configPath = flag.String("config", "", "config file path")
        listenAddr = flag.String("listen", "localhost:50051", "gRPC listen address")
        dbPath     = flag.String("db", "", "database path")
    )
    flag.Parse()

    // Initialize logger
    logger.Init()

    // Load configuration
    cfg := config.Load(*configPath)

    // Open database
    db := openDatabase(*dbPath, cfg)
    defer db.Close()

    // Run migrations
    migrations.Run(db)

    // Create components
    embedder := createEmbedder(cfg)
    memStore := memory.NewStore(db, embedder)
    memRouter := memory.NewRouter(memStore, cfg.APIKey)

    crew := agent.NewCrew(cfg.APIKey, db)
    registerAllTools(crew, memRouter, cfg)
    loadAgents(crew, cfg)
    registerMCPServers(crew, cfg)
    crew.InitializeAgents()

    // Start scheduler
    scheduler := runtime.NewScheduler(crew)
    scheduler.Start()
    defer scheduler.Stop()

    // Create and start gRPC server
    srv := server.NewStaffServer(crew, db, memRouter)

    // Handle shutdown gracefully
    ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer cancel()

    go func() {
        if err := srv.Serve(*listenAddr); err != nil {
            logger.Fatal("server error: %v", err)
        }
    }()

    logger.Info("staffd listening on %s", *listenAddr)

    <-ctx.Done()
    logger.Info("shutting down...")

    shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer shutdownCancel()

    srv.Shutdown(shutdownCtx)
}
```

### Daemon Features

- [ ] Signal handling (SIGINT, SIGTERM)
- [ ] Graceful shutdown with timeout
- [ ] PID file management
- [ ] Systemd integration (notify, watchdog)
- [ ] Health check endpoint
- [ ] Metrics endpoint (Prometheus)

### Tasks

- [ ] Create `cmd/staffd/main.go`
- [ ] Add configuration loading
- [ ] Add database initialization
- [ ] Add component wiring
- [ ] Add graceful shutdown
- [ ] Add logging configuration
- [ ] Create systemd unit file

---

## Phase 6: TUI Client Updates

### Refactored TUI

The TUI will now use the gRPC client instead of direct `Crew` access:

```go
// ui/tui/app.go
type App struct {
    client      *client.StaffClient
    app         *tview.Application
    // ... existing fields
}

func NewApp(addr string) (*App, error) {
    // Connect to daemon
    staffClient, err := client.Connect(addr,
        grpc.WithInsecure(), // TODO: TLS
        grpc.WithKeepaliveParams(keepalive.ClientParameters{
            Time:    30 * time.Second,
            Timeout: 10 * time.Second,
        }),
    )
    if err != nil {
        return nil, fmt.Errorf("failed to connect to staffd: %w", err)
    }

    return &App{
        client: staffClient,
        // ...
    }, nil
}
```

### Updated Chat Handler

```go
// ui/tui/chat.go
func (a *App) sendMessage(message string) {
    agentID := a.currentAgent
    threadID := a.currentThread

    // Send message via gRPC streaming
    ctx := context.Background()

    go func() {
        err := a.client.SendMessageCallback(ctx, agentID, threadID, message,
            // On text delta
            func(text string) {
                a.app.QueueUpdateDraw(func() {
                    a.appendToResponse(text)
                })
            },
            // On complete
            func(response string) {
                a.app.QueueUpdateDraw(func() {
                    a.finalizeResponse()
                })
            },
        )

        if err != nil {
            a.app.QueueUpdateDraw(func() {
                a.showError(err)
            })
        }
    }()
}
```

### State Watching

```go
// ui/tui/app.go
func (a *App) watchAgentStates() {
    ctx := context.Background()

    stream, err := a.client.Agents().WatchAgentStates(ctx, &pb.WatchAgentStatesRequest{})
    if err != nil {
        logger.Error("failed to watch states: %v", err)
        return
    }

    for {
        change, err := stream.Recv()
        if err != nil {
            logger.Error("state watch error: %v", err)
            return
        }

        a.app.QueueUpdateDraw(func() {
            a.updateAgentState(change.AgentId, change.NewState)
        })
    }
}
```

### Tasks

- [ ] Update `App` to use `StaffClient`
- [ ] Update chat to use gRPC streaming
- [ ] Update agent list to use gRPC
- [ ] Update inbox to use gRPC with watching
- [ ] Add connection status indicator
- [ ] Add reconnection logic
- [ ] Add daemon address configuration

---

## Phase 7: CLI Client Binary

### Client Entry Point

```go
// cmd/staff/main.go
package main

func main() {
    var (
        daemonAddr = flag.String("addr", "localhost:50051", "daemon address")
    )
    flag.Parse()

    // Connect to daemon
    staffClient, err := client.Connect(*daemonAddr)
    if err != nil {
        fmt.Fprintf(os.Stderr, "Failed to connect to staffd at %s: %v\n", *daemonAddr, err)
        fmt.Fprintf(os.Stderr, "Make sure staffd is running.\n")
        os.Exit(1)
    }
    defer staffClient.Close()

    // Start TUI
    app, err := tui.NewApp(staffClient)
    if err != nil {
        logger.Fatal("failed to create app: %v", err)
    }

    if err := app.Run(); err != nil {
        logger.Fatal("app error: %v", err)
    }
}
```

### Tasks

- [ ] Create `cmd/staff/main.go`
- [ ] Add daemon connection
- [ ] Add connection error handling
- [ ] Add daemon auto-start option (optional)

---

## Phase 8: Internal Package Updates

### StateManager Notifications

Add hooks for state change notifications:

```go
// internal/agent/agent_state.go
type StateChangeCallback func(agentID string, oldState, newState State)

type StateManager struct {
    db        *sql.DB
    callbacks []StateChangeCallback
    mu        sync.RWMutex
}

func (sm *StateManager) OnStateChange(cb StateChangeCallback) {
    sm.mu.Lock()
    defer sm.mu.Unlock()
    sm.callbacks = append(sm.callbacks, cb)
}

func (sm *StateManager) SetState(agentID string, state State) error {
    oldState, _ := sm.GetState(agentID)

    // Update database
    _, err := sm.db.Exec(...)
    if err != nil {
        return err
    }

    // Notify callbacks
    sm.mu.RLock()
    defer sm.mu.RUnlock()
    for _, cb := range sm.callbacks {
        go cb(agentID, oldState, state)
    }

    return nil
}
```

### Tool Results for gRPC Events

Enhance `AgentRunner` to emit tool use/result events:

```go
// internal/agent/runner.go
type ToolEventCallback func(event ToolEvent)

type ToolEvent struct {
    Type      string // "tool_use" or "tool_result"
    ToolID    string
    ToolName  string
    Input     json.RawMessage
    Result    json.RawMessage
    IsError   bool
}
```

### Tasks

- [ ] Add state change callbacks to `StateManager`
- [ ] Add tool event callbacks to `AgentRunner`
- [ ] Add inbox notification callbacks
- [ ] Ensure thread-safety for all callbacks
- [ ] Update `Crew` to expose notification subscriptions

---

## Phase 9: Authentication & Security

### Initial Implementation (Unix Socket)

For local use, start with Unix domain sockets:

```go
// pkg/server/server.go
func (s *StaffServer) ServeUnix(socketPath string) error {
    listener, err := net.Listen("unix", socketPath)
    if err != nil {
        return err
    }

    // Set permissions
    os.Chmod(socketPath, 0600)

    return s.grpcServer.Serve(listener)
}
```

### Future: TLS + API Keys

```protobuf
// Add to staff.proto
service AuthService {
  rpc Authenticate(AuthRequest) returns (AuthResponse);
}

message AuthRequest {
  string api_key = 1;
}

message AuthResponse {
  string token = 1;
  google.protobuf.Timestamp expires_at = 2;
}
```

### Tasks

- [ ] Support Unix socket transport
- [ ] Add file permissions for socket
- [ ] Add TLS configuration (optional)
- [ ] Add API key authentication (future)
- [ ] Add per-client rate limiting (future)

---

## Phase 10: Testing

### Unit Tests

```go
// pkg/server/chat_test.go
func TestSendMessage(t *testing.T) {
    // Create mock crew
    crew := agent.NewTestCrew(...)

    // Create server
    srv := server.NewStaffServer(crew, db, router)

    // Create in-process client
    conn := bufconn.Listen(1024 * 1024)
    go srv.ServeListener(conn)

    client := pb.NewChatServiceClient(conn)

    // Test streaming
    stream, err := client.SendMessage(ctx, &pb.SendMessageRequest{...})
    // ...
}
```

### Integration Tests

```go
// test/integration/daemon_test.go
func TestDaemonIntegration(t *testing.T) {
    // Start daemon in test mode
    daemon := startTestDaemon(t)
    defer daemon.Stop()

    // Connect client
    client, err := client.Connect(daemon.Addr())
    require.NoError(t, err)
    defer client.Close()

    // Test full flow
    // ...
}
```

### Tasks

- [ ] Unit tests for each service
- [ ] Integration tests for client-server
- [ ] Streaming tests
- [ ] Reconnection tests
- [ ] Load tests for concurrent clients

---

## Implementation Order

### Milestone 1: Core Infrastructure (Week 1)
1. Project restructuring
2. Proto definitions
3. Generated code
4. Basic server skeleton

### Milestone 2: Chat Flow (Week 2)
1. ChatService implementation
2. Streaming support
3. Basic client library
4. TUI updates for chat

### Milestone 3: Agent Management (Week 3)
1. AgentService implementation
2. State watching
3. TUI agent list updates

### Milestone 4: Supporting Services (Week 4)
1. InboxService
2. MemoryService
3. SystemService
4. TUI full integration

### Milestone 5: Polish & Testing (Week 5)
1. Error handling
2. Reconnection logic
3. Tests
4. Documentation

---

## Migration Path

### Step 1: Parallel Operation
Run both old monolith and new daemon during development.

### Step 2: Feature Parity
Ensure all existing functionality works via gRPC.

### Step 3: Switch Default
Make daemon the default, keep monolith for fallback.

### Step 4: Remove Monolith
Delete old `main.go` and direct `ui/service.go`.

---

## Future Enhancements

### REST/JSON API
Use grpc-gateway to generate REST API from proto:

```yaml
# buf.gen.yaml
plugins:
  - plugin: grpc-gateway
    out: api/staffpb
    opt: logtostderr=true
```

### Web UI
- Connect to gRPC-Web or REST API
- Real-time updates via WebSocket or SSE

### Mobile App
- Use gRPC mobile libraries
- Or REST API

### Multi-User Support
- User authentication
- Agent permissions
- Workspace isolation

---

## Dependencies to Add

```go
// go.mod additions
require (
    google.golang.org/grpc v1.60.0
    google.golang.org/protobuf v1.32.0
    github.com/grpc-ecosystem/go-grpc-middleware v1.4.0
    github.com/grpc-ecosystem/grpc-gateway/v2 v2.19.0  // future
)
```

---

## Configuration Changes

### Daemon Config

```yaml
# ~/.staffd/config.yaml
daemon:
  listen: "localhost:50051"
  socket: "/tmp/staffd.sock"  # Unix socket path

  tls:
    enabled: false
    cert: ""
    key: ""

  auth:
    enabled: false
    api_keys: []

# Existing config remains the same
anthropic_api_key: "..."
```

### Client Config

```yaml
# ~/.staff/config.yaml
daemon:
  address: "localhost:50051"
  socket: "/tmp/staffd.sock"  # Prefer socket if available

  tls:
    enabled: false
    ca_cert: ""
```

---

## Summary

This plan transforms the staff application from a monolithic TUI into a client-server architecture:

1. **Daemon (`staffd`)** - Runs agents, manages state, exposes gRPC API
2. **Client (`staff`)** - TUI that connects to daemon
3. **Client Library** - Reusable Go package for building other interfaces

The gRPC API provides:
- Streaming responses for real-time chat
- State change subscriptions for live updates
- Full agent management and introspection
- Memory search and storage
- Inbox management

This architecture enables:
- Multiple simultaneous interfaces
- Consistent state across clients
- Future REST/JSON API via grpc-gateway
- Better separation of concerns
- Easier testing and development
