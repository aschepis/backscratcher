package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aschepis/backscratcher/staff/logger"
)

// SearchMemory executes keyword / embedding / tag / hybrid search over memory_items.
func (s *Store) SearchMemory(ctx context.Context, q *SearchQuery) ([]SearchResult, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 20
	}

	// Default UseFTS to true for backward compatibility when query text is provided
	// The UseFTS field allows disabling FTS if needed, but defaults to true when query text exists
	queryText := strings.TrimSpace(q.QueryText)
	useFTS := queryText != "" // default to true when query text exists
	if q.UseFTS != nil {
		// Explicitly set - use the provided value
		useFTS = *q.UseFTS && queryText != ""
	}

	logger.Info("SearchMemory: queryText=%q, useFTS=%v, hasEmbedding=%v, hasTags=%v, agentID=%v, includeGlobal=%v, useHybrid=%v, limit=%d, types=%v, memoryTypes=%v",
		queryText, useFTS, q.QueryEmbedding != nil, len(q.Tags) > 0, q.AgentID, q.IncludeGlobal, q.UseHybrid, limit, q.Types, q.MemoryTypes)

	var byKeyword []SearchResult
	var byVector []SearchResult
	var byTags []SearchResult
	var err error

	// FTS search (optional)
	if useFTS && strings.TrimSpace(q.QueryText) != "" {
		logger.Debug("SearchMemory: executing FTS search")
		byKeyword, err = s.searchByKeyword(ctx, q, limit*3)
		if err != nil {
			logger.Error("SearchMemory: FTS search failed: %v", err)
			return nil, err
		}
		logger.Info("SearchMemory: FTS search returned %d results", len(byKeyword))
	} else {
		logger.Debug("SearchMemory: skipping FTS search (useFTS=%v, queryText=%q)", useFTS, queryText)
	}

	// Vector search
	if q.QueryEmbedding != nil {
		logger.Debug("SearchMemory: executing vector search")
		byVector, err = s.searchByVector(ctx, q, limit*3)
		if err != nil {
			logger.Error("SearchMemory: vector search failed: %v", err)
			return nil, err
		}
		logger.Info("SearchMemory: vector search returned %d results", len(byVector))
	} else {
		logger.Debug("SearchMemory: skipping vector search (no embedding provided)")
	}

	// Tag search
	if len(q.Tags) > 0 {
		logger.Debug("SearchMemory: executing tag search with tags=%v", q.Tags)
		byTags, err = s.searchByTags(ctx, q, limit*3)
		if err != nil {
			logger.Error("SearchMemory: tag search failed: %v", err)
			return nil, err
		}
		logger.Info("SearchMemory: tag search returned %d results", len(byTags))
	} else {
		logger.Debug("SearchMemory: skipping tag search (no tags provided)")
	}

	// If not using hybrid mode, return the best single result set
	if !q.UseHybrid {
		logger.Debug("SearchMemory: non-hybrid mode, selecting best result set")
		// Priority: vector > tags > keyword
		if len(byVector) > 0 {
			if len(byVector) > limit {
				byVector = byVector[:limit]
			}
			logger.Info("SearchMemory: returning %d vector results", len(byVector))
			return byVector, nil
		}
		if len(byTags) > 0 {
			if len(byTags) > limit {
				byTags = byTags[:limit]
			}
			logger.Info("SearchMemory: returning %d tag results", len(byTags))
			return byTags, nil
		}
		if len(byKeyword) > 0 {
			if len(byKeyword) > limit {
				byKeyword = byKeyword[:limit]
			}
			logger.Info("SearchMemory: returning %d keyword results", len(byKeyword))
			return byKeyword, nil
		}
		logger.Warn("SearchMemory: no results found from any search method")
		return nil, nil
	}

	// Hybrid mode: merge all result sets with weighted scoring
	results := make(map[int64]SearchResult)

	// Weight constants for different search methods
	const vectorWeight = 0.5
	const tagWeight = 0.3
	const ftsWeight = 0.2

	// Add vector results
	for _, r := range byVector {
		results[r.Item.ID] = SearchResult{
			Item:  r.Item,
			Score: r.Score * vectorWeight,
		}
	}

	// Merge tag results
	for _, r := range byTags {
		if existing, ok := results[r.Item.ID]; ok {
			// Combine scores: weighted average
			existing.Score += r.Score * tagWeight
			results[r.Item.ID] = existing
		} else {
			results[r.Item.ID] = SearchResult{
				Item:  r.Item,
				Score: r.Score * tagWeight,
			}
		}
	}

	// Merge FTS results
	for _, r := range byKeyword {
		if existing, ok := results[r.Item.ID]; ok {
			// Combine scores: weighted average
			existing.Score += r.Score * ftsWeight
			results[r.Item.ID] = existing
		} else {
			results[r.Item.ID] = SearchResult{
				Item:  r.Item,
				Score: r.Score * ftsWeight,
			}
		}
	}

	// Convert map to slice and sort by score
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
	logger.Info("SearchMemory: hybrid mode merged %d unique results (from %d vector + %d tags + %d keyword), returning %d",
		len(results), len(byVector), len(byTags), len(byKeyword), len(merged))
	return merged, nil
}

func (s *Store) searchByKeyword(ctx context.Context, q *SearchQuery, limit int) ([]SearchResult, error) {
	logger.Debug("searchByKeyword: query=%q, limit=%d", q.QueryText, limit)
	rows, err := s.db.QueryContext(ctx, `
SELECT rowid
FROM memory_items_fts
WHERE memory_items_fts MATCH ?
LIMIT ?
`, q.QueryText, limit)
	if err != nil {
		logger.Error("searchByKeyword: FTS query failed: %v", err)
		return nil, fmt.Errorf("fts query: %w", err)
	}
	defer rows.Close() //nolint:errcheck // No remedy for rows close errors

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			logger.Error("searchByKeyword: failed to scan rowid: %v", err)
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		logger.Error("searchByKeyword: row iteration error: %v", err)
		return nil, err
	}
	logger.Info("searchByKeyword: FTS query returned %d rowids: %v", len(ids), ids)
	if len(ids) == 0 {
		logger.Warn("searchByKeyword: no rowids found from FTS query")
		return nil, nil
	}

	items, err := s.loadItemsByIDs(ctx, ids)
	if err != nil {
		logger.Error("searchByKeyword: failed to load items: %v", err)
		return nil, err
	}
	logger.Info("searchByKeyword: loaded %d items from database", len(items))

	results := make([]SearchResult, 0, len(items))
	filteredCount := 0
	for _, it := range items {
		if !applyFilters(it, q) {
			filteredCount++
			logger.Debug("searchByKeyword: item id=%d filtered out (scope=%s, agentID=%v, type=%s)",
				it.ID, it.Scope, it.AgentID, it.Type)
			continue
		}
		results = append(results, SearchResult{
			Item:  it,
			Score: 1.0,
		})
	}
	logger.Info("searchByKeyword: %d items passed filters, %d filtered out, returning %d results",
		len(results), filteredCount, len(results))
	return results, nil
}

// loadMemoryItemFromRow scans a database row and returns a MemoryItem.
// It expects the SELECT to include: id, agent_id, thread_id, scope, type, content,
// embedding, metadata, created_at, updated_at, importance, raw_content, memory_type, tags_json
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
			// If tags_json is invalid, just leave tags empty
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

	where, args := buildFilterWhere(q)
	var query strings.Builder
	query.WriteString(`
SELECT id, agent_id, thread_id, scope, type, content,
       embedding, metadata, created_at, updated_at, importance,
       raw_content, memory_type, tags_json
FROM memory_items
WHERE `)
	query.WriteString(where)
	query.WriteString(`
ORDER BY created_at DESC
LIMIT ?
`)
	args = append(args, candidateLimit)
	queryStr := query.String()
	logger.Debug("searchByVector: query=%q, args=%v", queryStr, args)

	rows, err := s.db.QueryContext(ctx, queryStr, args...)
	if err != nil {
		logger.Error("searchByVector: query failed: %v", err)
		return nil, fmt.Errorf("vector query: %w", err)
	}
	defer rows.Close() //nolint:errcheck // No remedy for rows close errors

	var results []SearchResult
	scannedCount := 0
	zeroScoreCount := 0
	filteredCount := 0
	for rows.Next() {
		item, err := loadMemoryItemFromRow(rows)
		if err != nil {
			logger.Error("searchByVector: failed to load row: %v", err)
			return nil, err
		}
		scannedCount++

		if len(item.Embedding) == 0 {
			logger.Debug("searchByVector: item id=%d has no embedding, skipping", item.ID)
			continue
		}

		score := CosineSimilarity(q.QueryEmbedding, item.Embedding)
		if score <= 0 {
			zeroScoreCount++
			continue
		}

		if !applyFilters(item, q) {
			filteredCount++
			logger.Debug("searchByVector: item id=%d filtered out (scope=%s, agentID=%v, type=%s)",
				item.ID, item.Scope, item.AgentID, item.Type)
			continue
		}

		results = append(results, SearchResult{
			Item:  item,
			Score: score,
		})
	}
	if err := rows.Err(); err != nil {
		logger.Error("searchByVector: row iteration error: %v", err)
		return nil, err
	}

	logger.Info("searchByVector: scanned %d items, %d had zero/negative score, %d filtered out, %d with valid scores",
		scannedCount, zeroScoreCount, filteredCount, len(results))

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	if len(results) > limit {
		results = results[:limit]
	}
	logger.Info("searchByVector: returning %d results", len(results))
	return results, nil
}

// searchByTags searches memories by tag intersection.
// Returns results with scores based on the ratio of matching tags.
func (s *Store) searchByTags(ctx context.Context, q *SearchQuery, limit int) ([]SearchResult, error) {
	if len(q.Tags) == 0 {
		return nil, nil
	}

	where, args := buildFilterWhere(q)
	// We need to load all candidate memories and filter by tags in Go
	// since SQLite JSON extraction can be tricky for array intersection
	var query strings.Builder
	query.WriteString(`
SELECT id, agent_id, thread_id, scope, type, content,
       embedding, metadata, created_at, updated_at, importance,
       raw_content, memory_type, tags_json
FROM memory_items
WHERE `)
	query.WriteString(where)
	query.WriteString(`
ORDER BY created_at DESC
LIMIT ?
`)
	// Use a higher limit to get candidates, then filter by tags
	const candidateLimit = 500
	args = append(args, candidateLimit)
	queryStr := query.String()

	rows, err := s.db.QueryContext(ctx, queryStr, args...)
	if err != nil {
		return nil, fmt.Errorf("tag query: %w", err)
	}
	defer rows.Close() //nolint:errcheck // No remedy for rows close errors

	// Build a set of query tags for fast lookup
	queryTagSet := make(map[string]bool)
	for _, tag := range q.Tags {
		queryTagSet[strings.ToLower(strings.TrimSpace(tag))] = true
	}

	var results []SearchResult
	for rows.Next() {
		item, err := loadMemoryItemFromRow(rows)
		if err != nil {
			return nil, err
		}

		if !applyFilters(item, q) {
			continue
		}

		// Calculate tag intersection score
		if len(item.Tags) == 0 {
			continue // Skip items with no tags
		}

		// Count matching tags
		matchCount := 0
		for _, tag := range item.Tags {
			if queryTagSet[strings.ToLower(strings.TrimSpace(tag))] {
				matchCount++
			}
		}

		if matchCount == 0 {
			continue // No tag matches
		}

		// Score is the ratio of matching tags to query tags
		// This favors memories that match more of the requested tags
		score := float64(matchCount) / float64(len(q.Tags))
		// Also consider how many of the memory's tags matched (Jaccard-like)
		// This helps when a memory has many tags but only a few match
		jaccard := float64(matchCount) / float64(len(item.Tags)+len(q.Tags)-matchCount)
		// Combine both metrics
		finalScore := (score*0.7 + jaccard*0.3)

		results = append(results, SearchResult{
			Item:  item,
			Score: finalScore,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	if len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

func (s *Store) loadItemsByIDs(ctx context.Context, ids []int64) ([]*MemoryItem, error) {
	if len(ids) == 0 {
		logger.Debug("loadItemsByIDs: no IDs provided")
		return nil, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	var query strings.Builder
	query.WriteString(`
SELECT id, agent_id, thread_id, scope, type, content,
       embedding, metadata, created_at, updated_at, importance,
       raw_content, memory_type, tags_json
FROM memory_items
WHERE id IN (`)
	query.WriteString(strings.Join(placeholders, ","))
	query.WriteString(`)
`)
	queryStr := query.String()
	logger.Debug("loadItemsByIDs: loading %d items with IDs: %v", len(ids), ids)
	rows, err := s.db.QueryContext(ctx, queryStr, args...)
	if err != nil {
		logger.Error("loadItemsByIDs: query failed: %v", err)
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // No remedy for rows close errors

	var items []*MemoryItem
	for rows.Next() {
		item, err := loadMemoryItemFromRow(rows)
		if err != nil {
			logger.Error("loadItemsByIDs: failed to load row: %v", err)
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		logger.Error("loadItemsByIDs: row iteration error: %v", err)
		return nil, err
	}
	logger.Info("loadItemsByIDs: requested %d IDs, loaded %d items", len(ids), len(items))
	if len(items) < len(ids) {
		logger.Warn("loadItemsByIDs: some IDs were not found in database (requested %d, got %d)", len(ids), len(items))
	}
	return items, nil
}

func buildFilterWhere(q *SearchQuery) (string, []any) {
	var clauses []string
	var args []any

	if q.AgentID != nil {
		if q.IncludeGlobal {
			clauses = append(clauses, "((scope = 'agent' AND agent_id = ?) OR scope = 'global')")
			args = append(args, *q.AgentID)
		} else {
			clauses = append(clauses, "scope = 'agent' AND agent_id = ?")
			args = append(args, *q.AgentID)
		}
	} else {
		if q.IncludeGlobal {
			clauses = append(clauses, "scope = 'global'")
		}
	}

	if len(q.Types) > 0 {
		typePlaceholders := make([]string, len(q.Types))
		for i, t := range q.Types {
			typePlaceholders[i] = "?"
			args = append(args, string(t))
		}
		clauses = append(clauses, "type IN ("+strings.Join(typePlaceholders, ",")+")")
	}
	if q.MinImportance > 0 {
		clauses = append(clauses, "importance >= ?")
		args = append(args, q.MinImportance)
	}
	if q.After != nil {
		clauses = append(clauses, "created_at >= ?")
		args = append(args, q.After.Unix())
	}
	if q.Before != nil {
		clauses = append(clauses, "created_at <= ?")
		args = append(args, q.Before.Unix())
	}
	if len(clauses) == 0 {
		logger.Debug("buildFilterWhere: no filters, returning WHERE 1=1")
		return "1=1", nil
	}
	whereClause := strings.Join(clauses, " AND ")
	logger.Debug("buildFilterWhere: built WHERE clause: %q with args: %v", whereClause, args)
	return whereClause, args
}

func applyFilters(item *MemoryItem, q *SearchQuery) bool {
	if q.AgentID != nil {
		if q.IncludeGlobal {
			if item.Scope == ScopeAgent {
				if item.AgentID == nil || *item.AgentID != *q.AgentID {
					logger.Debug("applyFilters: item id=%d filtered (agent scope but agentID mismatch: item=%v, query=%v)",
						item.ID, item.AgentID, q.AgentID)
					return false
				}
			} else if item.Scope != ScopeGlobal {
				logger.Debug("applyFilters: item id=%d filtered (not agent or global scope: %s)", item.ID, item.Scope)
				return false
			}
		} else {
			if item.Scope != ScopeAgent {
				logger.Debug("applyFilters: item id=%d filtered (not agent scope: %s)", item.ID, item.Scope)
				return false
			}
			if item.AgentID == nil || *item.AgentID != *q.AgentID {
				logger.Debug("applyFilters: item id=%d filtered (agentID mismatch: item=%v, query=%v)",
					item.ID, item.AgentID, q.AgentID)
				return false
			}
		}
	} else {
		if q.IncludeGlobal {
			if item.Scope != ScopeGlobal {
				logger.Debug("applyFilters: item id=%d filtered (not global scope: %s)", item.ID, item.Scope)
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
			logger.Debug("applyFilters: item id=%d filtered (type mismatch: item=%s, query=%v)", item.ID, item.Type, q.Types)
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
			logger.Debug("applyFilters: item id=%d filtered (memoryType mismatch: item=%s, query=%v)", item.ID, item.MemoryType, q.MemoryTypes)
			return false
		}
	}

	if q.MinImportance > 0 && item.Importance < q.MinImportance {
		logger.Debug("applyFilters: item id=%d filtered (importance too low: item=%.2f, min=%.2f)", item.ID, item.Importance, q.MinImportance)
		return false
	}

	if q.After != nil && item.CreatedAt.Before(*q.After) {
		logger.Debug("applyFilters: item id=%d filtered (created before: item=%v, after=%v)", item.ID, item.CreatedAt, *q.After)
		return false
	}
	if q.Before != nil && item.CreatedAt.After(*q.Before) {
		logger.Debug("applyFilters: item id=%d filtered (created after: item=%v, before=%v)", item.ID, item.CreatedAt, *q.Before)
		return false
	}

	return true
}
