PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS memory_items (
    id INTEGER PRIMARY KEY,
    agent_id TEXT,
    thread_id TEXT,
    scope TEXT NOT NULL CHECK(scope IN ('agent','global')),
    type TEXT NOT NULL CHECK(type IN ('fact','episode','profile','doc_ref')),
    content TEXT NOT NULL,
    embedding BLOB,
    metadata TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    importance REAL NOT NULL DEFAULT 0.0,
    -- Normalization fields for personal memories
    raw_content TEXT,      -- original user/agent statement
    memory_type TEXT,      -- normalized memory type: preference, biographical, habit, goal, value, project, other
    tags_json TEXT         -- JSON array of tags: ["music","triathlon","age"]
);

CREATE TABLE IF NOT EXISTS artifacts (
    id INTEGER PRIMARY KEY,
    agent_id TEXT,
    thread_id TEXT,
    scope TEXT NOT NULL CHECK(scope IN ('agent','global')),
    title TEXT,
    body TEXT NOT NULL,
    metadata TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS inbox (
    id INTEGER PRIMARY KEY,
    agent_id TEXT,
    thread_id TEXT,
    message TEXT NOT NULL,
    requires_response BOOLEAN NOT NULL DEFAULT FALSE,
    response TEXT,
    response_at INTEGER,
    archived_at INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS conversations (
    id INTEGER PRIMARY KEY,
    agent_id TEXT NOT NULL,
    thread_id TEXT NOT NULL,
    role TEXT NOT NULL CHECK(role IN ('user', 'assistant', 'tool')),
    content TEXT NOT NULL,
    tool_name TEXT NULL,
    tool_id TEXT NULL,
    created_at INTEGER NOT NULL,
    UNIQUE(agent_id, thread_id, role, content, created_at)
);

CREATE INDEX IF NOT EXISTS idx_conversations_agent_thread ON conversations(agent_id, thread_id, created_at);

CREATE TABLE IF NOT EXISTS agent_states (
    agent_id TEXT PRIMARY KEY,
    state TEXT NOT NULL CHECK(state IN ('idle','running','waiting_human','waiting_external','sleeping')),
    updated_at INTEGER NOT NULL,
    next_wake INTEGER
);

CREATE INDEX IF NOT EXISTS idx_agent_states_state ON agent_states(state);
CREATE INDEX IF NOT EXISTS idx_agent_states_next_wake ON agent_states(next_wake);

CREATE TABLE IF NOT EXISTS agent_stats (
    agent_id TEXT PRIMARY KEY,
    execution_count INTEGER NOT NULL DEFAULT 0,
    failure_count INTEGER NOT NULL DEFAULT 0,
    wakeup_count INTEGER NOT NULL DEFAULT 0,
    last_execution INTEGER,
    last_failure INTEGER,
    last_failure_message TEXT,
    FOREIGN KEY(agent_id) REFERENCES agent_states(agent_id)
);

CREATE VIRTUAL TABLE IF NOT EXISTS memory_items_fts USING fts5(
    content,
    content_rowid='id'
);

-- Unique index on tool_id for tool calls/results
-- This allows multiple NULLs but prevents duplicate non-NULL tool_ids per thread and role
-- This ensures each tool_id appears at most once per role (one 'assistant' tool call, one 'tool' result)
CREATE UNIQUE INDEX IF NOT EXISTS idx_conversations_tool_id 
ON conversations(agent_id, thread_id, tool_id, role) 
WHERE tool_id IS NOT NULL;

