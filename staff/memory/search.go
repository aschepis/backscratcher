package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/rs/zerolog"
	"github.com/samber/lo"
)

// SearchMemory executes keyword / embedding / tag / hybrid search over memory_items.
func (s *Store) SearchMemory(ctx context.Context, q *SearchQuery) ([]SearchResult, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 20
	}

	// TODO: look into whether FTS should be toggleable
	queryText := strings.TrimSpace(q.QueryText)
	useFTS := queryText != ""
	if q.UseFTS != nil {
		useFTS = *q.UseFTS && queryText != ""
	}

	s.logger.Info().
		Str("queryText", queryText).
		Bool("useFTS", useFTS).
		Bool("hasEmbedding", q.QueryEmbedding != nil).
		Bool("hasTags", len(q.Tags) > 0).
		Interface("agentID", q.AgentID).
		Bool("includeGlobal", q.IncludeGlobal).
		Bool("useHybrid", q.UseHybrid).
		Int("limit", limit).
		Interface("types", q.Types).
		Interface("memoryTypes", q.MemoryTypes).
		Msg("SearchMemory: Start")

	var byKeyword []SearchResult
	var byVector []SearchResult
	var byTags []SearchResult
	var err error

	if useFTS && strings.TrimSpace(q.QueryText) != "" {
		s.logger.Debug().Msg("SearchMemory: executing FTS search")
		byKeyword, err = s.searchByKeyword(ctx, q, limit*3)
		if err != nil {
			s.logger.Error().Err(err).Msg("SearchMemory: FTS search failed")
			return nil, err
		}
		s.logger.Info().
			Int("numKeywordResults", len(byKeyword)).
			Msg("SearchMemory: FTS search completed")
	} else {
		s.logger.Debug().
			Bool("useFTS", useFTS).
			Str("queryText", queryText).
			Msg("SearchMemory: skipping FTS search")
	}

	if q.QueryEmbedding != nil {
		s.logger.Debug().Msg("SearchMemory: executing vector search")
		byVector, err = s.searchByVector(ctx, q, limit*3)
		if err != nil {
			s.logger.Error().Err(err).Msg("SearchMemory: vector search failed")
			return nil, err
		}
		s.logger.Info().
			Int("numVectorResults", len(byVector)).
			Msg("SearchMemory: vector search completed")
	} else {
		s.logger.Debug().Msg("SearchMemory: skipping vector search (no embedding provided)")
	}

	if len(q.Tags) > 0 {
		s.logger.Debug().
			Interface("tags", q.Tags).
			Msg("SearchMemory: executing tag search")
		byTags, err = s.searchByTags(ctx, q, limit*3)
		if err != nil {
			s.logger.Error().Err(err).Msg("SearchMemory: tag search failed")
			return nil, err
		}
		s.logger.Info().
			Int("numTagResults", len(byTags)).
			Msg("SearchMemory: tag search completed")
	} else {
		s.logger.Debug().Msg("SearchMemory: skipping tag search (no tags provided)")
	}

	if !q.UseHybrid {
		s.logger.Debug().Msg("SearchMemory: non-hybrid mode, selecting best result set")
		if len(byVector) > 0 {
			if len(byVector) > limit {
				byVector = byVector[:limit]
			}
			s.logger.Info().
				Int("numVectorResults", len(byVector)).
				Msg("SearchMemory: returning vector results")
			return byVector, nil
		}
		if len(byTags) > 0 {
			if len(byTags) > limit {
				byTags = byTags[:limit]
			}
			s.logger.Info().
				Int("numTagResults", len(byTags)).
				Msg("SearchMemory: returning tag results")
			return byTags, nil
		}
		if len(byKeyword) > 0 {
			if len(byKeyword) > limit {
				byKeyword = byKeyword[:limit]
			}
			s.logger.Info().
				Int("numKeywordResults", len(byKeyword)).
				Msg("SearchMemory: returning keyword results")
			return byKeyword, nil
		}
		s.logger.Warn().Msg("SearchMemory: no results found from any search method")
		return nil, nil
	}

	results := make(map[int64]SearchResult)
	const vectorWeight = 0.5
	const tagWeight = 0.3
	const ftsWeight = 0.2

	for _, r := range byVector {
		results[r.Item.ID] = SearchResult{
			Item:  r.Item,
			Score: r.Score * vectorWeight,
		}
	}
	for _, r := range byTags {
		if existing, ok := results[r.Item.ID]; ok {
			existing.Score += r.Score * tagWeight
			results[r.Item.ID] = existing
		} else {
			results[r.Item.ID] = SearchResult{
				Item:  r.Item,
				Score: r.Score * tagWeight,
			}
		}
	}
	for _, r := range byKeyword {
		if existing, ok := results[r.Item.ID]; ok {
			existing.Score += r.Score * ftsWeight
			results[r.Item.ID] = existing
		} else {
			results[r.Item.ID] = SearchResult{
				Item:  r.Item,
				Score: r.Score * ftsWeight,
			}
		}
	}

	merged := make([]SearchResult, 0, len(results))
	for _, r := range results {
		merged = append(merged, r)
	}
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Score > merged[j].Score
	})
	if len(merged) > limit {
		merged = merged[:limit]
	}
	s.logger.Info().
		Int("uniqueResults", len(results)).
		Int("vectorResults", len(byVector)).
		Int("tagResults", len(byTags)).
		Int("keywordResults", len(byKeyword)).
		Int("returning", len(merged)).
		Msg("SearchMemory: hybrid mode merged results")
	return merged, nil
}

func (s *Store) searchByKeyword(ctx context.Context, q *SearchQuery, limit int) ([]SearchResult, error) {
	s.logger.Debug().
		Str("queryText", q.QueryText).
		Int("limit", limit).
		Msg("searchByKeyword: begin")
	rows, err := s.db.QueryContext(ctx, `
SELECT rowid
FROM memory_items_fts
WHERE memory_items_fts MATCH ?
LIMIT ?
`, q.QueryText, limit)
	if err != nil {
		s.logger.Error().Err(err).Msg("searchByKeyword: FTS query failed")
		return nil, fmt.Errorf("fts query: %w", err)
	}
	defer rows.Close() //nolint:errcheck // no remedy for rows close error

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			s.logger.Error().Err(err).Msg("searchByKeyword: failed to scan rowid")
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		s.logger.Error().Err(err).Msg("searchByKeyword: row iteration error")
		return nil, err
	}
	s.logger.Info().
		Int("numRowIDs", len(ids)).
		Ints64("rowIDs", ids).
		Msg("searchByKeyword: FTS query results")
	if len(ids) == 0 {
		s.logger.Warn().Msg("searchByKeyword: no rowids found from FTS query")
		return nil, nil
	}

	items, err := s.loadItemsByIDs(ctx, ids)
	if err != nil {
		s.logger.Error().Err(err).Msg("searchByKeyword: failed to load items")
		return nil, err
	}
	s.logger.Info().
		Int("numLoadedItems", len(items)).
		Msg("searchByKeyword: loaded items from DB")

	results := lo.FilterMap(items, func(it *MemoryItem, _ int) (SearchResult, bool) {
		if !applyFilters(it, q, s.logger) {
			s.logger.Debug().
				Int64("itemID", it.ID).
				Str("scope", string(it.Scope)).
				Interface("agentID", it.AgentID).
				Str("type", string(it.Type)).
				Msg("searchByKeyword: item filtered out")
			return SearchResult{}, false
		}
		return SearchResult{
			Item:  it,
			Score: 1.0,
		}, true
	})
	filteredCount := len(items) - len(results)
	s.logger.Info().
		Int("passed", len(results)).
		Int("filtered", filteredCount).
		Int("returning", len(results)).
		Msg("searchByKeyword: items after filtering")
	return results, nil
}

// loadMemoryItemFromRow has no usage of logger, so unchanged.
func loadMemoryItemFromRow(rows *sql.Rows) (*MemoryItem, error) {
	var (
		id          int64
		agentIDStr  sql.NullString
		threadIDStr sql.NullString
		scopeStr    string
		typStr      string
		content     string
		embBlob     []byte
		metaJSON    sql.NullString
		createdAt   int64
		updatedAt   int64
		importance  float64
		rawContent  sql.NullString
		memoryType  sql.NullString
		tagsJSON    sql.NullString
	)
	if err := rows.Scan(&id, &agentIDStr, &threadIDStr, &scopeStr, &typStr, &content,
		&embBlob, &metaJSON, &createdAt, &updatedAt, &importance,
		&rawContent, &memoryType, &tagsJSON); err != nil {
		return nil, err
	}

	vec, err := DecodeEmbedding(embBlob)
	if err != nil {
		return nil, err
	}

	var meta map[string]interface{}
	if metaJSON.Valid && metaJSON.String != "" {
		_ = json.Unmarshal([]byte(metaJSON.String), &meta)
	}

	var agentPtr *string
	if agentIDStr.Valid {
		v := agentIDStr.String
		agentPtr = &v
	}
	var threadPtr *string
	if threadIDStr.Valid {
		v := threadIDStr.String
		threadPtr = &v
	}

	var tags []string
	if tagsJSON.Valid && tagsJSON.String != "" {
		if err := json.Unmarshal([]byte(tagsJSON.String), &tags); err != nil {
			tags = nil
		}
	}

	item := &MemoryItem{
		ID:         id,
		AgentID:    agentPtr,
		ThreadID:   threadPtr,
		Scope:      Scope(scopeStr),
		Type:       MemoryType(typStr),
		Content:    content,
		Embedding:  vec,
		Metadata:   meta,
		CreatedAt:  time.Unix(createdAt, 0),
		UpdatedAt:  time.Unix(updatedAt, 0),
		Importance: importance,
	}
	if rawContent.Valid {
		item.RawContent = rawContent.String
	}
	if memoryType.Valid {
		item.MemoryType = memoryType.String
	}
	if tags != nil {
		item.Tags = tags
	}

	return item, nil
}

func (s *Store) searchByVector(ctx context.Context, q *SearchQuery, limit int) ([]SearchResult, error) {
	const candidateLimit = 500

	query := StatementBuilder().
		Select(SelectMemoryItemsColumns()...).
		From("memory_items").
		Where(buildFilterWhere(q, s.logger)).
		OrderBy("created_at DESC").
		Limit(uint64(candidateLimit))

	queryStr, args, err := query.ToSql()
	if err != nil {
		s.logger.Error().Err(err).Msg("searchByVector: failed to build query")
		return nil, fmt.Errorf("build query: %w", err)
	}
	s.logger.Debug().
		Str("query", queryStr).
		Interface("args", args).
		Msg("searchByVector: built query")

	rows, err := s.db.QueryContext(ctx, queryStr, args...)
	if err != nil {
		s.logger.Error().Err(err).Msg("searchByVector: query failed")
		return nil, fmt.Errorf("vector query: %w", err)
	}
	defer rows.Close() //nolint:errcheck // no remedy for rows close error

	var results []SearchResult
	scannedCount := 0
	zeroScoreCount := 0
	filteredCount := 0
	for rows.Next() {
		item, err := loadMemoryItemFromRow(rows)
		if err != nil {
			s.logger.Error().Err(err).Msg("searchByVector: failed to load row")
			return nil, err
		}
		scannedCount++

		if len(item.Embedding) == 0 {
			s.logger.Debug().
				Int64("itemID", item.ID).
				Msg("searchByVector: item has no embedding, skipping")
			continue
		}

		score := CosineSimilarity(q.QueryEmbedding, item.Embedding)
		if score <= 0 {
			zeroScoreCount++
			continue
		}

		if !applyFilters(item, q, s.logger) {
			filteredCount++
			s.logger.Debug().
				Int64("itemID", item.ID).
				Str("scope", string(item.Scope)).
				Interface("agentID", item.AgentID).
				Str("type", string(item.Type)).
				Msg("searchByVector: item filtered out")
			continue
		}

		results = append(results, SearchResult{
			Item:  item,
			Score: score,
		})
	}
	if err := rows.Err(); err != nil {
		s.logger.Error().Err(err).Msg("searchByVector: row iteration error")
		return nil, err
	}

	s.logger.Info().
		Int("scanned", scannedCount).
		Int("zeroOrNegativeScore", zeroScoreCount).
		Int("filtered", filteredCount).
		Int("validResults", len(results)).
		Msg("searchByVector: summary")

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	if len(results) > limit {
		results = results[:limit]
	}
	s.logger.Info().
		Int("numResults", len(results)).
		Msg("searchByVector: returning results")

	return results, nil
}

func (s *Store) searchByTags(ctx context.Context, q *SearchQuery, limit int) ([]SearchResult, error) {
	if len(q.Tags) == 0 {
		return nil, nil
	}

	const candidateLimit = 500
	query := StatementBuilder().
		Select(SelectMemoryItemsColumns()...).
		From("memory_items").
		Where(buildFilterWhere(q, s.logger)).
		OrderBy("created_at DESC").
		Limit(uint64(candidateLimit))

	queryStr, args, err := query.ToSql()
	if err != nil {
		s.logger.Error().Err(err).Msg("searchByTags: failed to build query")
		return nil, fmt.Errorf("build query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, queryStr, args...)
	if err != nil {
		s.logger.Error().Err(err).Msg("searchByTags: query failed")
		return nil, fmt.Errorf("tag query: %w", err)
	}
	defer rows.Close() //nolint:errcheck // no remedy for rows close error

	queryTagSet := make(map[string]bool)
	for _, tag := range q.Tags {
		queryTagSet[strings.ToLower(strings.TrimSpace(tag))] = true
	}

	var results []SearchResult
	for rows.Next() {
		item, err := loadMemoryItemFromRow(rows)
		if err != nil {
			s.logger.Error().Err(err).Msg("searchByTags: failed to load row")
			return nil, err
		}

		if !applyFilters(item, q, s.logger) {
			continue
		}

		if len(item.Tags) == 0 {
			continue
		}

		matchCount := 0
		for _, tag := range item.Tags {
			if queryTagSet[strings.ToLower(strings.TrimSpace(tag))] {
				matchCount++
			}
		}

		if matchCount == 0 {
			continue
		}

		score := float64(matchCount) / float64(len(q.Tags))
		jaccard := float64(matchCount) / float64(len(item.Tags)+len(q.Tags)-matchCount)
		finalScore := (score*0.7 + jaccard*0.3)

		results = append(results, SearchResult{
			Item:  item,
			Score: finalScore,
		})
	}
	if err := rows.Err(); err != nil {
		s.logger.Error().Err(err).Msg("searchByTags: row iteration error")
		return nil, err
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	if len(results) > limit {
		results = results[:limit]
	}
	s.logger.Info().
		Int("returningNum", len(results)).
		Msg("searchByTags: returning results")
	return results, nil
}

func (s *Store) loadItemsByIDs(ctx context.Context, ids []int64) ([]*MemoryItem, error) {
	if len(ids) == 0 {
		s.logger.Debug().Msg("loadItemsByIDs: no IDs provided")
		return nil, nil
	}

	idArgs := make([]interface{}, len(ids))
	for i, id := range ids {
		idArgs[i] = id
	}

	query := StatementBuilder().
		Select(SelectMemoryItemsColumns()...).
		From("memory_items").
		Where(sq.Eq{"id": idArgs})

	queryStr, args, err := query.ToSql()
	if err != nil {
		s.logger.Error().Err(err).Msg("loadItemsByIDs: failed to build query")
		return nil, fmt.Errorf("build query: %w", err)
	}
	s.logger.Debug().
		Int("numIDs", len(ids)).
		Ints64("IDs", ids).
		Msg("loadItemsByIDs: loading items")
	rows, err := s.db.QueryContext(ctx, queryStr, args...)
	if err != nil {
		s.logger.Error().Err(err).Msg("loadItemsByIDs: query failed")
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // no remedy for rows close error

	var items []*MemoryItem
	for rows.Next() {
		item, err := loadMemoryItemFromRow(rows)
		if err != nil {
			s.logger.Error().Err(err).Msg("loadItemsByIDs: failed to load row")
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		s.logger.Error().Err(err).Msg("loadItemsByIDs: row iteration error")
		return nil, err
	}
	s.logger.Info().
		Int("requested", len(ids)).
		Int("loaded", len(items)).
		Msg("loadItemsByIDs: items loaded")
	if len(items) < len(ids) {
		s.logger.Warn().
			Int("requested", len(ids)).
			Int("loaded", len(items)).
			Msg("loadItemsByIDs: some IDs were not found in the database")
	}
	return items, nil
}

// buildFilterWhere builds Squirrel WHERE conditions based on SearchQuery filters.
// Returns a sq.Sqlizer that can be used in Where() clauses.
func buildFilterWhere(q *SearchQuery, logger zerolog.Logger) sq.Sqlizer {
	var conditions []sq.Sqlizer

	// Build scope/agent_id filter
	if q.AgentID != nil {
		if q.IncludeGlobal {
			// (scope = 'agent' AND agent_id = ?) OR scope = 'global'
			conditions = append(conditions, sq.Or{
				sq.And{
					sq.Eq{"scope": string(ScopeAgent)},
					sq.Eq{"agent_id": *q.AgentID},
				},
				sq.Eq{"scope": string(ScopeGlobal)},
			})
		} else {
			// scope = 'agent' AND agent_id = ?
			conditions = append(conditions, sq.Eq{"scope": string(ScopeAgent)}, sq.Eq{"agent_id": *q.AgentID})
		}
	} else {
		if q.IncludeGlobal {
			// scope = 'global'
			conditions = append(conditions, sq.Eq{"scope": string(ScopeGlobal)})
		}
	}

	// Type filter
	if len(q.Types) > 0 {
		typeStrings := make([]interface{}, len(q.Types))
		for i, t := range q.Types {
			typeStrings[i] = string(t)
		}
		conditions = append(conditions, sq.Eq{"type": typeStrings})
	}

	// Importance filter
	if q.MinImportance > 0 {
		conditions = append(conditions, sq.GtOrEq{"importance": q.MinImportance})
	}

	// Time range filters
	if q.After != nil {
		conditions = append(conditions, sq.GtOrEq{"created_at": q.After.Unix()})
	}
	if q.Before != nil {
		conditions = append(conditions, sq.LtOrEq{"created_at": q.Before.Unix()})
	}

	// If no filters were applied, return a condition that matches all rows
	if len(conditions) == 0 {
		logger.Debug().Msg("buildFilterWhere: no filters, query will match all rows")
		return sq.Expr("1=1")
	}

	// Combine all conditions with AND
	if len(conditions) == 1 {
		return conditions[0]
	}
	return sq.And(conditions)
}

func applyFilters(item *MemoryItem, q *SearchQuery, logger zerolog.Logger) bool {
	if q.AgentID != nil {
		if q.IncludeGlobal {
			if item.Scope == ScopeAgent {
				if item.AgentID == nil || *item.AgentID != *q.AgentID {
					logger.Debug().
						Str("reason", "agent scope but agentID mismatch").
						Int64("item_id", item.ID).
						Interface("item_agent_id", item.AgentID).
						Interface("query_agent_id", q.AgentID).
						Msg("applyFilters: item filtered")
					return false
				}
			} else if item.Scope != ScopeGlobal {
				logger.Debug().
					Str("reason", "not agent or global scope").
					Int64("item_id", item.ID).
					Str("item_scope", string(item.Scope)).
					Msg("applyFilters: item filtered")
				return false
			}
		} else {
			if item.Scope != ScopeAgent {
				logger.Debug().
					Str("reason", "not agent scope").
					Int64("item_id", item.ID).
					Str("item_scope", string(item.Scope)).
					Msg("applyFilters: item filtered")
				return false
			}
			if item.AgentID == nil || *item.AgentID != *q.AgentID {
				logger.Debug().
					Str("reason", "agentID mismatch").
					Int64("item_id", item.ID).
					Interface("item_agent_id", item.AgentID).
					Interface("query_agent_id", q.AgentID).
					Msg("applyFilters: item filtered")
				return false
			}
		}
	} else {
		if q.IncludeGlobal {
			if item.Scope != ScopeGlobal {
				logger.Debug().
					Str("reason", "not global scope").
					Int64("item_id", item.ID).
					Str("item_scope", string(item.Scope)).
					Msg("applyFilters: item filtered")
				return false
			}
		}
	}

	if len(q.Types) > 0 {
		match := false
		for _, t := range q.Types {
			if item.Type == t {
				match = true
				break
			}
		}
		if !match {
			logger.Debug().
				Str("reason", "type mismatch").
				Int64("item_id", item.ID).
				Str("item_type", string(item.Type)).
				Interface("query_types", q.Types).
				Msg("applyFilters: item filtered")
			return false
		}
	}

	if len(q.MemoryTypes) > 0 {
		match := false
		for _, mt := range q.MemoryTypes {
			if item.MemoryType == mt {
				match = true
				break
			}
		}
		if !match {
			logger.Debug().
				Str("reason", "memoryType mismatch").
				Int64("item_id", item.ID).
				Str("item_memory_type", item.MemoryType).
				Interface("query_memory_types", q.MemoryTypes).
				Msg("applyFilters: item filtered")
			return false
		}
	}

	if q.MinImportance > 0 && item.Importance < q.MinImportance {
		logger.Debug().
			Str("reason", "importance too low").
			Int64("item_id", item.ID).
			Float64("item_importance", item.Importance).
			Float64("min_importance", q.MinImportance).
			Msg("applyFilters: item filtered")
		return false
	}

	if q.After != nil && item.CreatedAt.Before(*q.After) {
		logger.Debug().
			Str("reason", "created before").
			Int64("item_id", item.ID).
			Time("item_created_at", item.CreatedAt).
			Time("after", *q.After).
			Msg("applyFilters: item filtered")
		return false
	}
	if q.Before != nil && item.CreatedAt.After(*q.Before) {
		logger.Debug().
			Str("reason", "created after").
			Int64("item_id", item.ID).
			Time("item_created_at", item.CreatedAt).
			Time("before", *q.Before).
			Msg("applyFilters: item filtered")
		return false
	}

	return true
}
