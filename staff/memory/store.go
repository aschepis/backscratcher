package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

// Store manages all memory & artifact persistence.
type Store struct {
	db       *sql.DB
	embedder Embedder
	logger   zerolog.Logger
}

// NewStore creates and returns a Store.
func NewStore(db *sql.DB, embedder Embedder, logger zerolog.Logger) (*Store, error) {
	logger = logger.With().Str("component", "memory_store").Logger()
	logger.Info().Msg("Initializing new Store with DB and Embedder")
	s := &Store{db: db, embedder: embedder, logger: logger}
	return s, nil
}

// EmbedText generates an embedding for the given text.
// Returns an error if no embedder is configured.
func (s *Store) EmbedText(ctx context.Context, text string) ([]float32, error) {
	if s.embedder == nil {
		return nil, fmt.Errorf("no embedder configured")
	}
	return s.embedder.Embed(ctx, text)
}

func now() int64 { return time.Now().Unix() }

// RememberGlobalFact stores a long-term shared fact.
func (s *Store) RememberGlobalFact(
	ctx context.Context,
	content string,
	importance float64,
	metadata map[string]interface{},
) (MemoryItem, error) {
	s.logger.Debug().
		Str("method", "RememberGlobalFact").
		Str("content", truncateString(content, 40)).
		Float64("importance", importance).
		Interface("metadata", metadata).
		Msg("called")
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
	s.logger.Debug().
		Str("method", "RememberAgentFact").
		Str("agent_id", agentID).
		Str("content", truncateString(content, 40)).
		Float64("importance", importance).
		Interface("metadata", metadata).
		Msg("called")
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
	s.logger.Debug().
		Str("method", "RememberAgentEpisode").
		Str("agent_id", agentID).
		Str("thread_id", threadID).
		Str("content", truncateString(content, 40)).
		Float64("importance", importance).
		Interface("metadata", metadata).
		Msg("called")
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
	s.logger.Debug().
		Str("method", "RememberGeneric").
		Str("type", string(typ)).
		Str("scope", string(scope)).
		Str("agent_id", derefString(agentID)).
		Str("thread_id", derefString(threadID)).
		Str("content", truncateString(content, 40)).
		Float64("importance", importance).
		Interface("metadata", metadata).
		Msg("called")
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
	s.logger.Debug().
		Str("method", "remember").
		Str("type", string(typ)).
		Str("scope", string(scope)).
		Str("agent_id", derefString(agentID)).
		Str("thread_id", derefString(threadID)).
		Str("content", truncateString(content, 40)).
		Float64("importance", importance).
		Interface("metadata", metadata).
		Msg("called")
	if strings.TrimSpace(content) == "" {
		s.logger.Warn().
			Str("method", "remember").
			Msg("Attempted to remember empty content")
		return MemoryItem{}, errors.New("content is empty")
	}
	if scope != ScopeAgent && scope != ScopeGlobal {
		s.logger.Error().
			Str("method", "remember").
			Str("invalid_scope", string(scope)).
			Msg("Invalid scope provided")
		return MemoryItem{}, fmt.Errorf("invalid scope: %q", scope)
	}

	var metaJSON []byte
	var err error
	if metadata != nil {
		metaJSON, err = json.Marshal(metadata)
		if err != nil {
			s.logger.Error().
				Str("method", "remember").
				Err(err).
				Msg("Failed to marshal metadata")
			return MemoryItem{}, fmt.Errorf("marshal metadata: %w", err)
		}
	}

	var embedding []float32
	if s.embedder != nil {
		embedding, err = s.embedder.Embed(ctx, content)
		if err != nil {
			s.logger.Error().
				Str("method", "remember").
				Err(err).
				Msg("Embedding failed. Saving anyway without embedding.")
			embedding = nil
		}
	}

	nowUnix := now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		s.logger.Error().
			Str("method", "remember").
			Err(err).
			Msg("Failed to begin transaction")
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

	query := StatementBuilder().
		Insert("memory_items").
		Columns("agent_id", "thread_id", "scope", "type", "content",
			"embedding", "metadata", "created_at", "updated_at", "importance").
		Values(agentVal, threadVal, string(scope), string(typ), content,
			EncodeEmbedding(embedding), metaJSON, nowUnix, nowUnix, importance)

	queryStr, args, err := query.ToSql()
	if err != nil {
		s.logger.Error().
			Str("method", "remember").
			Err(err).
			Msg("Failed to build insert query")
		return MemoryItem{}, fmt.Errorf("build insert query: %w", err)
	}

	res, err := tx.ExecContext(ctx, queryStr, args...)
	if err != nil {
		s.logger.Error().
			Str("method", "remember").
			Err(err).
			Msg("Failed to insert memory_item")
		return MemoryItem{}, fmt.Errorf("insert memory_item: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		s.logger.Error().
			Str("method", "remember").
			Err(err).
			Msg("Failed to retrieve LastInsertId for memory_items")
		return MemoryItem{}, err
	}

	if _, err := tx.ExecContext(ctx, `
INSERT INTO memory_items_fts (rowid, content) VALUES (?, ?)
`, id, content); err != nil {
		s.logger.Error().
			Str("method", "remember").
			Err(err).
			Msg("Failed to insert memory_items_fts row")
		return MemoryItem{}, fmt.Errorf("insert fts: %w", err)
	}

	if err := tx.Commit(); err != nil {
		s.logger.Error().
			Str("method", "remember").
			Err(err).
			Msg("Transaction commit failed for remembering memory_item")
		return MemoryItem{}, err
	}

	s.logger.Info().
		Str("method", "remember").
		Str("type", string(typ)).
		Str("scope", string(scope)).
		Str("agent_id", derefString(agentID)).
		Str("thread_id", derefString(threadID)).
		Str("content", truncateString(content, 40)).
		Int64("id", id).
		Msg("MemoryItem remembered")

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

// StorePersonalMemory writes an enriched, normalized personal memory for a specific agent.
// It is intended to be used together with the memory_normalize tool.
func (s *Store) StorePersonalMemory(
	ctx context.Context,
	agentID string,
	rawText string,
	normalized string,
	memoryType string,
	tags []string,
	threadID *string,
	importance float64,
	metadata map[string]interface{},
) (MemoryItem, error) {
	s.logger.Debug().
		Str("method", "StorePersonalMemory").
		Str("agent_id", agentID).
		Str("raw", truncateString(rawText, 60)).
		Str("normalized", truncateString(normalized, 60)).
		Str("memory_type", memoryType).
		Strs("tags", tags).
		Str("thread_id", derefString(threadID)).
		Float64("importance", importance).
		Msg("called")

	rawText = strings.TrimSpace(rawText)
	normalized = strings.TrimSpace(normalized)
	if rawText == "" && normalized == "" {
		s.logger.Warn().
			Str("method", "StorePersonalMemory").
			Msg("Attempted to store personal memory with empty raw and normalized text")
		return MemoryItem{}, errors.New("raw and normalized text cannot both be empty")
	}
	if normalized == "" {
		normalized = rawText
	}
	if importance == 0 {
		importance = 0.8
	}

	// Encode metadata and tags.
	var (
		metaJSON []byte
		err      error
	)
	if metadata != nil {
		metaJSON, err = json.Marshal(metadata)
		if err != nil {
			s.logger.Error().
				Str("method", "StorePersonalMemory").
				Err(err).
				Msg("failed to marshal metadata")
			return MemoryItem{}, fmt.Errorf("marshal metadata: %w", err)
		}
	}
	var tagsJSON []byte
	if tags != nil {
		tagsJSON, err = json.Marshal(tags)
		if err != nil {
			s.logger.Error().
				Str("method", "StorePersonalMemory").
				Err(err).
				Msg("failed to marshal tags")
			return MemoryItem{}, fmt.Errorf("marshal tags: %w", err)
		}
	}

	// Embed normalized text for vector search.
	var embedding []float32
	if s.embedder != nil {
		embedding, err = s.embedder.Embed(ctx, normalized)
		if err != nil {
			s.logger.Error().
				Str("method", "StorePersonalMemory").
				Err(err).
				Msg("embedding failed: saving without embedding")
			embedding = nil
		}
	}

	nowUnix := now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		s.logger.Error().
			Str("method", "StorePersonalMemory").
			Err(err).
			Msg("failed to begin transaction")
		return MemoryItem{}, err
	}
	defer func() { _ = tx.Rollback() }()

	agentVal := interface{}(agentID)
	var threadVal interface{}
	if threadID != nil {
		threadVal = *threadID
	}

	query := StatementBuilder().
		Insert("memory_items").
		Columns("agent_id", "thread_id", "scope", "type", "content",
			"embedding", "metadata", "created_at", "updated_at", "importance",
			"raw_content", "memory_type", "tags_json").
		Values(agentVal, threadVal, string(ScopeAgent), string(MemoryTypeProfile), normalized,
			EncodeEmbedding(embedding), metaJSON, nowUnix, nowUnix, importance,
			rawText, memoryType, tagsJSON)

	queryStr, args, err := query.ToSql()
	if err != nil {
		s.logger.Error().
			Str("method", "StorePersonalMemory").
			Err(err).
			Msg("failed to build insert query")
		return MemoryItem{}, fmt.Errorf("build insert query: %w", err)
	}

	res, err := tx.ExecContext(ctx, queryStr, args...)
	if err != nil {
		s.logger.Error().
			Str("method", "StorePersonalMemory").
			Err(err).
			Msg("failed to insert memory_item")
		return MemoryItem{}, fmt.Errorf("insert personal memory_item: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		s.logger.Error().
			Str("method", "StorePersonalMemory").
			Err(err).
			Msg("failed to retrieve LastInsertId")
		return MemoryItem{}, err
	}

	// Index normalized text in the FTS table to support hybrid search.
	if _, err := tx.ExecContext(ctx, `
INSERT INTO memory_items_fts (rowid, content) VALUES (?, ?)
`, id, normalized); err != nil {
		s.logger.Error().
			Str("method", "StorePersonalMemory").
			Err(err).
			Msg("failed to insert memory_items_fts row")
		return MemoryItem{}, fmt.Errorf("insert personal fts: %w", err)
	}

	if err := tx.Commit(); err != nil {
		s.logger.Error().
			Str("method", "StorePersonalMemory").
			Err(err).
			Msg("transaction commit failed")
		return MemoryItem{}, err
	}

	s.logger.Info().
		Str("method", "StorePersonalMemory").
		Str("agent_id", agentID).
		Int64("id", id).
		Msg("stored personal memory")

	item := MemoryItem{
		ID:         id,
		AgentID:    &agentID,
		ThreadID:   threadID,
		Scope:      ScopeAgent,
		Type:       MemoryTypeProfile,
		Content:    normalized,
		Embedding:  embedding,
		Metadata:   metadata,
		CreatedAt:  time.Unix(nowUnix, 0),
		UpdatedAt:  time.Unix(nowUnix, 0),
		Importance: importance,
		RawContent: rawText,
		MemoryType: memoryType,
		Tags:       append([]string(nil), tags...),
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
	s.logger.Debug().
		Str("method", "CreateArtifact").
		Str("scope", string(scope)).
		Str("agent_id", derefString(agentID)).
		Str("thread_id", derefString(threadID)).
		Str("title", truncateString(title, 40)).
		Str("body", truncateString(body, 40)).
		Interface("metadata", metadata).
		Msg("called")

	if strings.TrimSpace(body) == "" {
		s.logger.Warn().
			Str("method", "CreateArtifact").
			Msg("Attempted to create artifact with empty body")
		return Artifact{}, errors.New("body is empty")
	}
	if scope != ScopeAgent && scope != ScopeGlobal {
		s.logger.Error().
			Str("method", "CreateArtifact").
			Str("invalid_scope", string(scope)).
			Msg("Invalid scope for artifact")
		return Artifact{}, fmt.Errorf("invalid scope: %q", scope)
	}
	var metaJSON []byte
	var err error
	if metadata != nil {
		metaJSON, err = json.Marshal(metadata)
		if err != nil {
			s.logger.Error().
				Str("method", "CreateArtifact").
				Err(err).
				Msg("Failed to marshal artifact metadata")
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

	query := StatementBuilder().
		Insert("artifacts").
		Columns("agent_id", "thread_id", "scope", "title", "body", "metadata", "created_at", "updated_at").
		Values(agentVal, threadVal, string(scope), title, body, metaJSON, nowUnix, nowUnix)

	queryStr, args, err := query.ToSql()
	if err != nil {
		s.logger.Error().
			Str("method", "CreateArtifact").
			Err(err).
			Msg("Failed to build insert query")
		return Artifact{}, fmt.Errorf("build insert query: %w", err)
	}

	res, err := s.db.ExecContext(ctx, queryStr, args...)
	if err != nil {
		s.logger.Error().
			Str("method", "CreateArtifact").
			Err(err).
			Msg("Failed to insert artifact")
		return Artifact{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		s.logger.Error().
			Str("method", "CreateArtifact").
			Err(err).
			Msg("Failed to retrieve LastInsertId for artifact")
		return Artifact{}, err
	}
	s.logger.Info().
		Str("method", "CreateArtifact").
		Int64("id", id).
		Str("title", truncateString(title, 40)).
		Str("scope", string(scope)).
		Str("agent_id", derefString(agentID)).
		Str("thread_id", derefString(threadID)).
		Msg("Artifact created")
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

// Helper function to safely dereference *string for structured logs.
// TODO: extract this into a ptr module that can ref/deref any type
func derefString(ptr *string) string {
	if ptr == nil {
		return ""
	}
	return *ptr
}

// Helper function to safely truncate strings (for log safety).
// TODO: add this to a helper module for non-standard library string operations
func truncateString(s string, n int) string {
	rs := []rune(s)
	if len(rs) > n {
		return string(rs[:n]) + "..."
	}
	return s
}
