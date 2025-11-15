package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// SearchMemory executes keyword / embedding / hybrid search over memory_items.
func (s *Store) SearchMemory(ctx context.Context, q SearchQuery) ([]SearchResult, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 20
	}

	var byKeyword []SearchResult
	var byVector []SearchResult
	var err error

	if strings.TrimSpace(q.QueryText) != "" {
		byKeyword, err = s.searchByKeyword(ctx, q, limit*3)
		if err != nil {
			return nil, err
		}
	}
	if q.QueryEmbedding != nil {
		byVector, err = s.searchByVector(ctx, q, limit*3)
		if err != nil {
			return nil, err
		}
	}

	if !q.UseHybrid || q.QueryEmbedding == nil || strings.TrimSpace(q.QueryText) == "" {
		if len(byVector) > 0 {
			if len(byVector) > limit {
				byVector = byVector[:limit]
			}
			return byVector, nil
		}
		if len(byKeyword) > 0 {
			if len(byKeyword) > limit {
				byKeyword = byKeyword[:limit]
			}
			return byKeyword, nil
		}
		return nil, nil
	}

	results := make(map[int64]SearchResult)

	for _, r := range byVector {
		results[r.Item.ID] = r
	}
	for _, r := range byKeyword {
		if existing, ok := results[r.Item.ID]; ok {
			existing.Score = existing.Score*0.7 + r.Score*0.6
			results[r.Item.ID] = existing
		} else {
			r.Score *= 0.8
			results[r.Item.ID] = r
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
	return merged, nil
}

func (s *Store) searchByKeyword(ctx context.Context, q SearchQuery, limit int) ([]SearchResult, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT rowid
FROM memory_items_fts
WHERE memory_items_fts MATCH ?
LIMIT ?
`, q.QueryText, limit)
	if err != nil {
		return nil, fmt.Errorf("fts query: %w", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}

	items, err := s.loadItemsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}

	results := make([]SearchResult, 0, len(items))
	for _, it := range items {
		if !applyFilters(it, q) {
			continue
		}
		results = append(results, SearchResult{
			Item:  it,
			Score: 1.0,
		})
	}
	return results, nil
}

func (s *Store) searchByVector(ctx context.Context, q SearchQuery, limit int) ([]SearchResult, error) {
	const candidateLimit = 500

	where, args := buildFilterWhere(q)
	query := `
SELECT id, agent_id, thread_id, scope, type, content,
       embedding, metadata, created_at, updated_at, importance
FROM memory_items
` + where + `
ORDER BY created_at DESC
LIMIT ?
`
	args = append(args, candidateLimit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("vector query: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
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
		)
		if err := rows.Scan(&id, &agentIDStr, &threadIDStr, &scopeStr, &typStr, &content,
			&embBlob, &metaJSON, &createdAt, &updatedAt, &importance); err != nil {
			return nil, err
		}
		vec, err := DecodeEmbedding(embBlob)
		if err != nil {
			return nil, err
		}
		score := CosineSimilarity(q.QueryEmbedding, vec)
		if score <= 0 {
			continue
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

		item := MemoryItem{
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

		if !applyFilters(item, q) {
			continue
		}

		results = append(results, SearchResult{
			Item:  item,
			Score: score,
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

func (s *Store) loadItemsByIDs(ctx context.Context, ids []int64) ([]MemoryItem, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	query := `
SELECT id, agent_id, thread_id, scope, type, content,
       embedding, metadata, created_at, updated_at, importance
FROM memory_items
WHERE id IN (` + strings.Join(placeholders, ",") + `)
`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []MemoryItem
	for rows.Next() {
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
		)
		if err := rows.Scan(&id, &agentIDStr, &threadIDStr, &scopeStr, &typStr, &content,
			&embBlob, &metaJSON, &createdAt, &updatedAt, &importance); err != nil {
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
		items = append(items, MemoryItem{
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
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func buildFilterWhere(q SearchQuery) (string, []interface{}) {
	var clauses []string
	var args []interface{}

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
		return "WHERE 1=1", nil
	}
	return "WHERE " + strings.Join(clauses, " AND "), args
}

func applyFilters(item MemoryItem, q SearchQuery) bool {
	if q.AgentID != nil {
		if q.IncludeGlobal {
			if item.Scope == ScopeAgent {
				if item.AgentID == nil || *item.AgentID != *q.AgentID {
					return false
				}
			} else if item.Scope != ScopeGlobal {
				return false
			}
		} else {
			if item.Scope != ScopeAgent {
				return false
			}
			if item.AgentID == nil || *item.AgentID != *q.AgentID {
				return false
			}
		}
	} else {
		if q.IncludeGlobal {
			if item.Scope != ScopeGlobal {
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
			return false
		}
	}

	if q.MinImportance > 0 && item.Importance < q.MinImportance {
		return false
	}

	if q.After != nil && item.CreatedAt.Before(*q.After) {
		return false
	}
	if q.Before != nil && item.CreatedAt.After(*q.Before) {
		return false
	}

	return true
}
