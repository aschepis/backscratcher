-- Migration to add 'system' role to conversations table CHECK constraint
-- SQLite doesn't support ALTER TABLE to modify CHECK constraints,
-- so we need to recreate the table

-- Step 1: Create new table with updated CHECK constraint
CREATE TABLE IF NOT EXISTS conversations_new (
    id INTEGER PRIMARY KEY,
    agent_id TEXT NOT NULL,
    thread_id TEXT NOT NULL,
    role TEXT NOT NULL CHECK(role IN ('user', 'assistant', 'tool', 'system')),
    content TEXT NOT NULL,
    tool_name TEXT NULL,
    tool_id TEXT NULL,
    created_at INTEGER NOT NULL,
    UNIQUE(agent_id, thread_id, role, content, created_at)
);

-- Step 2: Copy all data from old table to new table
INSERT INTO conversations_new 
SELECT * FROM conversations;

-- Step 3: Drop old table
DROP TABLE conversations;

-- Step 4: Rename new table to original name
ALTER TABLE conversations_new RENAME TO conversations;

-- Step 5: Recreate indexes
CREATE INDEX IF NOT EXISTS idx_conversations_agent_thread ON conversations(agent_id, thread_id, created_at);
CREATE UNIQUE INDEX IF NOT EXISTS idx_conversations_tool_id 
ON conversations(agent_id, thread_id, tool_id, role) 
WHERE tool_id IS NOT NULL;

