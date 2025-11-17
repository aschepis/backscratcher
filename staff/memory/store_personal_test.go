package memory

import (
	"context"
	"database/sql"
	"testing"
)

// TestStorePersonalMemory_Smoke verifies that StorePersonalMemory inserts a row and
// returns a populated MemoryItem. This uses an in-memory SQLite DB and a nil embedder.
func TestStorePersonalMemory_Smoke(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open in-memory sqlite: %v", err)
	}
	defer db.Close() //nolint:errcheck // Test cleanup

	store, err := NewStore(db, nil)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	ctx := context.Background()
	item, err := store.StorePersonalMemory(
		ctx,
		"agent-1",
		"I'm 43 and I love concept albums and triathlons.",
		"The user is 43 years old and loves concept albums and triathlons.",
		"preference",
		[]string{"music", "triathlon", "age"},
		nil,
		0,
		map[string]any{"source": "test"},
	)
	if err != nil {
		t.Fatalf("StorePersonalMemory returned error: %v", err)
	}
	if item.ID == 0 {
		t.Fatalf("expected non-zero ID")
	}
	if item.RawContent == "" || item.Content == "" {
		t.Fatalf("expected raw and normalized content to be populated")
	}
	if len(item.Tags) == 0 {
		t.Fatalf("expected tags to be populated")
	}
	if item.AgentID == nil || *item.AgentID != "agent-1" {
		t.Fatalf("expected AgentID to be agent-1, got %v", item.AgentID)
	}
}
