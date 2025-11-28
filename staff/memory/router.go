package memory

import (
	"context"
	"errors"
	"time"

	"github.com/rs/zerolog"
)

// MemoryRouter handles routing of memories between agent-private and global.
type MemoryRouter struct {
	store      *Store
	summarizer Summarizer
	logger     zerolog.Logger
}

// Config allows customizing MemoryRouter behavior.
type Config struct {
	Summarizer Summarizer
}

func NewMemoryRouter(store *Store, cfg Config, logger zerolog.Logger) *MemoryRouter {
	return &MemoryRouter{
		store:      store,
		summarizer: cfg.Summarizer,
		logger:     logger,
	}
}

// StorePersonalMemory stores a normalized personal memory for a specific agent.
// This is a thin wrapper around Store.StorePersonalMemory to keep tools decoupled
// from the underlying storage implementation.
func (r *MemoryRouter) StorePersonalMemory(
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
	return r.store.StorePersonalMemory(ctx, agentID, rawText, normalized, memoryType, tags, threadID, importance, metadata)
}

// AddEpisode stores a detailed agent-private episode.
func (r *MemoryRouter) AddEpisode(
	ctx context.Context,
	agentID string,
	threadID string,
	content string,
	metadata map[string]interface{},
) (MemoryItem, error) {
	return r.store.RememberAgentEpisode(
		ctx,
		agentID,
		threadID,
		content,
		0.3,
		metadata,
	)
}

// AddObservation is a synonym for AddEpisode.
func (r *MemoryRouter) AddObservation(
	ctx context.Context,
	agentID string,
	threadID string,
	content string,
	metadata map[string]interface{},
) (MemoryItem, error) {
	return r.AddEpisode(ctx, agentID, threadID, content, metadata)
}

// AddAgentFact stores an internal fact for this agent only.
func (r *MemoryRouter) AddAgentFact(
	ctx context.Context,
	agentID string,
	content string,
	metadata map[string]interface{},
) (MemoryItem, error) {
	return r.store.RememberAgentFact(
		ctx,
		agentID,
		content,
		0.7,
		metadata,
	)
}

// AddGlobalFact stores a shared fact that all agents should know.
func (r *MemoryRouter) AddGlobalFact(
	ctx context.Context,
	content string,
	metadata map[string]interface{},
) (MemoryItem, error) {
	return r.store.RememberGlobalFact(
		ctx,
		content,
		0.9,
		metadata,
	)
}

// AddArtifact stores a shared, durable document.
func (r *MemoryRouter) AddArtifact(
	ctx context.Context,
	agentID *string,
	title string,
	body string,
	metadata map[string]interface{},
) (Artifact, error) {
	return r.store.CreateArtifact(
		ctx,
		ScopeGlobal,
		agentID,
		nil,
		title,
		body,
		metadata,
	)
}

// Reflect consolidates an agent's recent episodes into a global fact.
func (r *MemoryRouter) Reflect(
	ctx context.Context,
	agentID string,
	threadID string,
) (MemoryItem, error) {
	if r.summarizer == nil {
		return MemoryItem{}, errors.New("MemoryRouter: no summarizer configured")
	}
	return r.store.ReflectThread(ctx, agentID, threadID, r.summarizer)
}

// AutoReflect performs time-based reflection.
func (r *MemoryRouter) AutoReflect(
	ctx context.Context,
	agentID string,
	threadID string,
	lastReflected *time.Time,
	minInterval time.Duration,
) (*MemoryItem, error) {
	if lastReflected != nil && time.Since(*lastReflected) < minInterval {
		return nil, nil
	}
	item, err := r.Reflect(ctx, agentID, threadID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if lastReflected != nil {
		*lastReflected = now
	}
	return &item, nil
}

// QueryAgentMemory returns agent-private memory plus optional global.
func (r *MemoryRouter) QueryAgentMemory(
	ctx context.Context,
	agentID string,
	text string,
	embedding []float32,
	includeGlobal bool,
	limit int,
	types []MemoryType,
) ([]SearchResult, error) {
	r.logger.Info().
		Str("method", "QueryAgentMemory").
		Str("agentID", agentID).
		Str("text", text).
		Bool("hasEmbedding", embedding != nil).
		Bool("includeGlobal", includeGlobal).
		Int("limit", limit).
		Interface("types", types).
		Msg("QueryAgentMemory started")

	results, err := r.store.SearchMemory(ctx, &SearchQuery{
		AgentID:        &agentID,
		IncludeGlobal:  includeGlobal,
		QueryText:      text,
		QueryEmbedding: embedding,
		Limit:          limit,
		UseHybrid:      true,
		Types:          types,
	})
	if err != nil {
		r.logger.Error().
			Str("method", "QueryAgentMemory").
			Err(err).
			Msg("search failed")
		return nil, err
	}
	r.logger.Info().
		Str("method", "QueryAgentMemory").
		Int("result_count", len(results)).
		Msg("QueryAgentMemory returning results")
	return results, nil
}

// QueryGlobalMemory searches only global memories.
func (r *MemoryRouter) QueryGlobalMemory(
	ctx context.Context,
	text string,
	embedding []float32,
	limit int,
	types []MemoryType,
) ([]SearchResult, error) {
	return r.store.SearchMemory(ctx, &SearchQuery{
		AgentID:        nil,
		IncludeGlobal:  true,
		QueryText:      text,
		QueryEmbedding: embedding,
		Limit:          limit,
		UseHybrid:      true,
		Types:          types,
	})
}

// QueryAllMemory searches everything regardless of scope.
func (r *MemoryRouter) QueryAllMemory(
	ctx context.Context,
	text string,
	embedding []float32,
	limit int,
	types []MemoryType,
) ([]SearchResult, error) {
	return r.store.SearchMemory(ctx, &SearchQuery{
		AgentID:        nil,
		IncludeGlobal:  false,
		QueryText:      text,
		QueryEmbedding: embedding,
		Limit:          limit,
		UseHybrid:      true,
		Types:          types,
	})
}

// QueryPersonalMemory searches for personal memories (type='profile') for a specific agent.
// It uses hybrid retrieval combining embeddings, tag matching, and optional FTS.
func (r *MemoryRouter) QueryPersonalMemory(
	ctx context.Context,
	agentID string,
	text string,
	tags []string,
	limit int,
	memoryTypes []string,
) ([]SearchResult, error) {
	// Generate embedding for query text if available
	var embedding []float32
	var err error
	if text != "" {
		embedding, err = r.store.EmbedText(ctx, text)
		if err != nil {
			// Log error but continue without embedding
			embedding = nil
		}
	}

	// Build query with personal memory filters
	query := &SearchQuery{
		AgentID:        &agentID,
		IncludeGlobal:  false, // Personal memories are agent-scoped
		QueryText:      text,
		QueryEmbedding: embedding,
		Tags:           tags,
		Limit:          limit,
		UseHybrid:      true,
		Types:          []MemoryType{MemoryTypeProfile}, // Only personal memories
		MemoryTypes:    memoryTypes,
		UseFTS:         nil, // nil = default to true when query text exists
	}

	return r.store.SearchMemory(ctx, query)
}
