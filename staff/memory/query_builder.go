package memory

import (
	sq "github.com/Masterminds/squirrel"
)

// StatementBuilder returns a Squirrel StatementBuilder configured for SQLite.
// SQLite uses '?' as placeholders, which is Squirrel's default.
func StatementBuilder() sq.StatementBuilderType {
	return sq.StatementBuilder
}

// SelectMemoryItemsColumns returns the standard column list for memory_items SELECT queries.
func SelectMemoryItemsColumns() []string {
	return []string{
		"id", "agent_id", "thread_id", "scope", "type", "content",
		"embedding", "metadata", "created_at", "updated_at", "importance",
		"raw_content", "memory_type", "tags_json",
	}
}
