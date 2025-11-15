package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aschepis/backscratcher/staff/logger"
)

// Store manages all memory & artifact persistence.
type Store struct {
	db       *sql.DB
	embedder Embedder
}

// NewStore sets up the schema and returns a Store.
func NewStore(db *sql.DB, embedder Embedder) (*Store, error) {
	logger.Info("Initializing new Store with DB and Embedder")
	s := &Store{db: db, embedder: embedder}
	if err := s.migrate(); err != nil {
		logger.Error("Failed to migrate Store schema: %v", err)
		return nil, err
	}
	logger.Info("Store schema migration complete")
	return s, nil
}

func (s *Store) migrate() error {
	const schema = `
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
    importance REAL NOT NULL DEFAULT 0.0
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
    role TEXT NOT NULL CHECK(role IN ('user', 'assistant')),
    content TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    UNIQUE(agent_id, thread_id, role, content, created_at)
);

CREATE INDEX IF NOT EXISTS idx_conversations_agent_thread ON conversations(agent_id, thread_id, created_at);

CREATE TABLE IF NOT EXISTS agent_states (
    agent_id TEXT PRIMARY KEY,
    state TEXT NOT NULL CHECK(state IN ('idle','running','waiting_human','waiting_external','sleeping')),
    updated_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_agent_states_state ON agent_states(state);

CREATE VIRTUAL TABLE IF NOT EXISTS memory_items_fts USING fts5(
    content,
    content_rowid='id'
);
`
	logger.Info("Running schema migration for Store")
	_, err := s.db.Exec(schema)
	if err != nil {
		logger.Error("Error executing schema migration: %v", err)
		return err
	}
	logger.Info("Schema migration executed successfully")

	// Add next_wake column to agent_states if it doesn't exist
	// SQLite doesn't support IF NOT EXISTS for ALTER TABLE, so we check first
	var columnExists int
	checkColumnQuery := `
		SELECT COUNT(*) FROM pragma_table_info('agent_states')
		WHERE name = 'next_wake'
	`
	err = s.db.QueryRow(checkColumnQuery).Scan(&columnExists)
	if err != nil {
		logger.Error("Error checking if next_wake column exists: %v", err)
		return err
	}

	if columnExists == 0 {
		logger.Info("Adding next_wake column to agent_states table")
		_, err = s.db.Exec(`
			ALTER TABLE agent_states ADD COLUMN next_wake INTEGER;
		`)
		if err != nil {
			logger.Error("Error adding next_wake column: %v", err)
			return err
		}

		_, err = s.db.Exec(`
			CREATE INDEX IF NOT EXISTS idx_agent_states_next_wake ON agent_states(next_wake);
		`)
		if err != nil {
			logger.Error("Error creating next_wake index: %v", err)
			return err
		}
		logger.Info("Successfully added next_wake column and index")
	}

	return nil
}

func now() int64 { return time.Now().Unix() }

// RememberGlobalFact stores a long-term shared fact.
func (s *Store) RememberGlobalFact(
	ctx context.Context,
	content string,
	importance float64,
	metadata map[string]interface{},
) (MemoryItem, error) {
	logger.Debug("RememberGlobalFact called. Content: %.40q, Importance: %.2f, Metadata: %+v", content, importance, metadata)
	return s.remember(ctx, MemoryTypeFact, ScopeGlobal, nil, nil, content, importance, metadata)
}

// RememberAgentFact stores a fact scoped to a specific agent.
func (s *Store) RememberAgentFact(
	ctx context.Context,
	agentID string,
	content string,
	importance float64,
	metadata map[string]interface{},
) (MemoryItem, error) {
	logger.Debug("RememberAgentFact called. AgentID: %s, Content: %.40q, Importance: %.2f, Metadata: %+v", agentID, content, importance, metadata)
	return s.remember(ctx, MemoryTypeFact, ScopeAgent, &agentID, nil, content, importance, metadata)
}

// RememberAgentEpisode stores a short-term episode for a given agent and thread.
func (s *Store) RememberAgentEpisode(
	ctx context.Context,
	agentID string,
	threadID string,
	content string,
	importance float64,
	metadata map[string]interface{},
) (MemoryItem, error) {
	logger.Debug("RememberAgentEpisode called. AgentID: %s, ThreadID: %s, Content: %.40q, Importance: %.2f, Metadata: %+v", agentID, threadID, content, importance, metadata)
	return s.remember(ctx, MemoryTypeEpisode, ScopeAgent, &agentID, &threadID, content, importance, metadata)
}

// RememberGeneric lets you choose any MemoryType/Scope/agent/thread.
func (s *Store) RememberGeneric(
	ctx context.Context,
	typ MemoryType,
	scope Scope,
	agentID *string,
	threadID *string,
	content string,
	importance float64,
	metadata map[string]interface{},
) (MemoryItem, error) {
	logger.Debug("RememberGeneric called. Type: %s, Scope: %s, AgentID: %v, ThreadID: %v, Content: %.40q, Importance: %.2f, Metadata: %+v",
		typ, scope, agentID, threadID, content, importance, metadata)
	return s.remember(ctx, typ, scope, agentID, threadID, content, importance, metadata)
}

func (s *Store) remember(
	ctx context.Context,
	typ MemoryType,
	scope Scope,
	agentID *string,
	threadID *string,
	content string,
	importance float64,
	metadata map[string]interface{},
) (MemoryItem, error) {
	logger.Debug("remember called. Type: %s, Scope: %s, AgentID: %v, ThreadID: %v, Content: %.40q, Importance: %.2f, Metadata: %+v",
		typ, scope, agentID, threadID, content, importance, metadata)
	if strings.TrimSpace(content) == "" {
		logger.Warn("Attempted to remember empty content")
		return MemoryItem{}, errors.New("content is empty")
	}
	if scope != ScopeAgent && scope != ScopeGlobal {
		logger.Error("Invalid scope provided: %q", scope)
		return MemoryItem{}, fmt.Errorf("invalid scope: %q", scope)
	}

	var metaJSON []byte
	var err error
	if metadata != nil {
		metaJSON, err = json.Marshal(metadata)
		if err != nil {
			logger.Error("Failed to marshal metadata: %v", err)
			return MemoryItem{}, fmt.Errorf("marshal metadata: %w", err)
		}
	}

	var embedding []float32
	if s.embedder != nil {
		embedding, err = s.embedder.Embed(ctx, content)
		if err != nil {
			logger.Error("Embedding failed: %v. Saving anyway without embedding.", err)
			embedding = nil
		}
	}

	nowUnix := now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		logger.Error("Failed to begin transaction: %v", err)
		return MemoryItem{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var agentVal interface{}
	if agentID != nil {
		agentVal = *agentID
	}
	var threadVal interface{}
	if threadID != nil {
		threadVal = *threadID
	}

	res, err := tx.ExecContext(ctx, `
INSERT INTO memory_items (
    agent_id, thread_id, scope, type, content,
    embedding, metadata, created_at, updated_at, importance
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, agentVal, threadVal, string(scope), string(typ), content,
		EncodeEmbedding(embedding), metaJSON, nowUnix, nowUnix, importance)
	if err != nil {
		logger.Error("Failed to insert memory_item: %v", err)
		return MemoryItem{}, fmt.Errorf("insert memory_item: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		logger.Error("Failed to retrieve LastInsertId for memory_items: %v", err)
		return MemoryItem{}, err
	}

	if _, err := tx.ExecContext(ctx, `
INSERT INTO memory_items_fts (rowid, content) VALUES (?, ?)
`, id, content); err != nil {
		logger.Error("Failed to insert memory_items_fts row: %v", err)
		return MemoryItem{}, fmt.Errorf("insert fts: %w", err)
	}

	if err := tx.Commit(); err != nil {
		logger.Error("Transaction commit failed for remembering memory_item: %v", err)
		return MemoryItem{}, err
	}

	logger.Info("MemoryItem remembered. Type: %s, Scope: %s, AgentID: %v, ThreadID: %v, Content: %.40q, ID: %d",
		typ, scope, agentID, threadID, content, id)

	item := MemoryItem{
		ID:         id,
		AgentID:    agentID,
		ThreadID:   threadID,
		Scope:      scope,
		Type:       typ,
		Content:    content,
		Embedding:  embedding,
		Metadata:   metadata,
		CreatedAt:  time.Unix(nowUnix, 0),
		UpdatedAt:  time.Unix(nowUnix, 0),
		Importance: importance,
	}
	return item, nil
}

// CreateArtifact stores a durable document.
func (s *Store) CreateArtifact(
	ctx context.Context,
	scope Scope,
	agentID *string,
	threadID *string,
	title, body string,
	metadata map[string]interface{},
) (Artifact, error) {
	logger.Debug("CreateArtifact called. Scope: %s, AgentID: %v, ThreadID: %v, Title: %.40q, Body: %.40q, Metadata: %+v",
		scope, agentID, threadID, title, body, metadata)

	if strings.TrimSpace(body) == "" {
		logger.Warn("Attempted to create artifact with empty body")
		return Artifact{}, errors.New("body is empty")
	}
	if scope != ScopeAgent && scope != ScopeGlobal {
		logger.Error("Invalid scope for artifact: %q", scope)
		return Artifact{}, fmt.Errorf("invalid scope: %q", scope)
	}
	var metaJSON []byte
	var err error
	if metadata != nil {
		metaJSON, err = json.Marshal(metadata)
		if err != nil {
			logger.Error("Failed to marshal artifact metadata: %v", err)
			return Artifact{}, fmt.Errorf("marshal metadata: %w", err)
		}
	}
	nowUnix := now()

	var agentVal interface{}
	if agentID != nil {
		agentVal = *agentID
	}
	var threadVal interface{}
	if threadID != nil {
		threadVal = *threadID
	}

	res, err := s.db.ExecContext(ctx, `
INSERT INTO artifacts (agent_id, thread_id, scope, title, body, metadata, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
`, agentVal, threadVal, string(scope), title, body, metaJSON, nowUnix, nowUnix)
	if err != nil {
		logger.Error("Failed to insert artifact: %v", err)
		return Artifact{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		logger.Error("Failed to retrieve LastInsertId for artifact: %v", err)
		return Artifact{}, err
	}
	logger.Info("Artifact created. ID: %d, Title: %.40q, Scope: %s, AgentID: %v, ThreadID: %v", id, title, scope, agentID, threadID)
	return Artifact{
		ID:        id,
		AgentID:   agentID,
		ThreadID:  threadID,
		Scope:     scope,
		Title:     title,
		Body:      body,
		Metadata:  metadata,
		CreatedAt: time.Unix(nowUnix, 0),
		UpdatedAt: time.Unix(nowUnix, 0),
	}, nil
}
