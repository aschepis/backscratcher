package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/aschepis/backscratcher/staff/memory"

	_ "github.com/mattn/go-sqlite3"
)

// TestMemoryStorePersonalTool_Smoke exercises the wiring of the memory_store_personal
// tool end-to-end against an in-memory SQLite database. It does not call Anthropic.
func TestMemoryStorePersonalTool_Smoke(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open in-memory sqlite: %v", err)
	}
	defer db.Close() //nolint:errcheck // Test cleanup

	store, err := memory.NewStore(db, nil)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	router := memory.NewMemoryRouter(store, memory.Config{})

	reg := NewRegistry()
	reg.RegisterMemoryTools(router, "") // Empty API key will fall back to env var

	args := map[string]any{
		"text":       "I go running most mornings before work.",
		"normalized": "The user usually goes running most mornings before work.",
		"type":       "habit",
		"tags":       []string{"running", "exercise", "morning"},
	}
	argsBytes, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("failed to marshal args: %v", err)
	}

	result, err := reg.Handle(context.Background(), "memory_store_personal", "agent-test", argsBytes)
	if err != nil {
		t.Fatalf("memory_store_personal tool returned error: %v", err)
	}
	if result == nil {
		t.Fatalf("expected non-nil result")
	}
}
