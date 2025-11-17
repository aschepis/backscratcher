package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"
)

// ReflectThread takes recent episode memories for a given agent + thread and
// asks the Summarizer to produce a summary, which we store as a GLOBAL fact.
func (s *Store) ReflectThread(
	ctx context.Context,
	agentID string,
	threadID string,
	summarizer Summarizer,
) (MemoryItem, error) {
	if agentID == "" {
		return MemoryItem{}, fmt.Errorf("agentID is empty")
	}
	if threadID == "" {
		return MemoryItem{}, fmt.Errorf("threadID is empty")
	}

	cutoff := time.Now().Add(-7 * 24 * time.Hour).Unix()

	query := StatementBuilder().
		Select("id", "agent_id", "thread_id", "scope", "type", "content",
			"embedding", "metadata", "created_at", "updated_at", "importance").
		From("memory_items").
		Where(sq.Eq{"type": string(MemoryTypeEpisode)}).
		Where(sq.Eq{"scope": string(ScopeAgent)}).
		Where(sq.Eq{"agent_id": agentID}).
		Where(sq.Eq{"thread_id": threadID}).
		Where(sq.GtOrEq{"created_at": cutoff}).
		OrderBy("created_at ASC")

	queryStr, args, err := query.ToSql()
	if err != nil {
		return MemoryItem{}, fmt.Errorf("build query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, queryStr, args...)
	if err != nil {
		return MemoryItem{}, err
	}
	defer rows.Close() //nolint:errcheck // No remedy for rows close errors

	var episodes []MemoryItem
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
			return MemoryItem{}, err
		}
		var meta map[string]interface{}
		if metaJSON.Valid && metaJSON.String != "" {
			_ = json.Unmarshal([]byte(metaJSON.String), &meta)
		}
		vec, _ := DecodeEmbedding(embBlob)

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

		episodes = append(episodes, MemoryItem{
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
		return MemoryItem{}, err
	}
	if len(episodes) == 0 {
		return MemoryItem{}, fmt.Errorf("no episodes found for agent %q thread %q", agentID, threadID)
	}

	summary, err := summarizer.SummarizeEpisodes(episodes)
	if err != nil {
		return MemoryItem{}, fmt.Errorf("summarize episodes: %w", err)
	}

	meta := map[string]interface{}{
		"thread_id": threadID,
		"agent_id":  agentID,
		"source":    "reflection",
	}

	return s.RememberGlobalFact(ctx, summary, 0.7, meta)
}
