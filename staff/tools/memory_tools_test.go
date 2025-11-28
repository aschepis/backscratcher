package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aschepis/backscratcher/staff/memory"
	"github.com/aschepis/backscratcher/staff/migrations"
	"github.com/rs/zerolog"

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

	// Run migrations to create the necessary tables
	// Try to find migrations directory relative to common test run locations
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	var migrationsPath string
	// Try relative to tools directory first
	if testPath := filepath.Join(cwd, "..", "migrations"); fileExists(filepath.Join(testPath, "000001_initial_schema.up.sql")) {
		migrationsPath = testPath
	} else if testPath := filepath.Join(cwd, "staff", "migrations"); fileExists(filepath.Join(testPath, "000001_initial_schema.up.sql")) {
		// Try relative to module root
		migrationsPath = testPath
	} else {
		// Fallback to relative path
		migrationsPath = filepath.Join("..", "migrations")
	}

	if err := migrations.RunMigrations(db, migrationsPath, zerolog.Nop()); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	store, err := memory.NewStore(db, nil, zerolog.Nop())
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	router := memory.NewMemoryRouter(store, memory.Config{}, zerolog.Nop())

	reg := NewRegistry(zerolog.Nop())
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

// fileExists checks if a file exists
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
