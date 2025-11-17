package memory

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/aschepis/backscratcher/staff/migrations"
)

// TestStorePersonalMemory_Smoke verifies that StorePersonalMemory inserts a row and
// returns a populated MemoryItem. This uses an in-memory SQLite DB and a nil embedder.
func TestStorePersonalMemory_Smoke(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open in-memory sqlite: %v", err)
	}
	defer db.Close() //nolint:errcheck // Test cleanup

	// Run migrations to create the necessary tables
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	var migrationsPath string
	// Try relative to memory directory first
	if testPath := filepath.Join(cwd, "..", "migrations"); fileExists(filepath.Join(testPath, "000001_initial_schema.up.sql")) {
		migrationsPath = testPath
	} else if testPath := filepath.Join(cwd, "staff", "migrations"); fileExists(filepath.Join(testPath, "000001_initial_schema.up.sql")) {
		// Try relative to module root
		migrationsPath = testPath
	} else {
		// Fallback to relative path
		migrationsPath = filepath.Join("..", "migrations")
	}

	if err := migrations.RunMigrations(db, migrationsPath); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

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
