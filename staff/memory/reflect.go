package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
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

	rows, err := s.db.QueryContext(ctx, `
SELECT id, agent_id, thread_id, scope, type, content,
       embedding, metadata, created_at, updated_at, importance
FROM memory_items
WHERE type = 'episode'
  AND scope = 'agent'
  AND agent_id = ?
  AND thread_id = ?
  AND created_at >= ?
ORDER BY created_at ASC
`, agentID, threadID, cutoff)
	if err != nil {
		return MemoryItem{}, err
	}
	defer rows.Close()

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
