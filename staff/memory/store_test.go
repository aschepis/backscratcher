package memory

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

type stubEmbedder struct{}

func (stubEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	return []float32{float32(len(text)), 1.0}, nil
}

func TestStore_RememberGlobalFactAndSearch(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	store, err := NewStore(db, stubEmbedder{})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	ctx := context.Background()

	_, err = store.RememberGlobalFact(ctx,
		"Adam prefers Ruby over Python for core systems.",
		0.9,
		map[string]interface{}{"category": "preference"},
	)
	if err != nil {
		t.Fatalf("RememberGlobalFact: %v", err)
	}

	results, err := store.SearchMemory(ctx, SearchQuery{
		QueryText:     "Ruby core systems",
		Limit:         5,
		IncludeGlobal: true,
		UseHybrid:     false,
		Types:         []MemoryType{MemoryTypeFact},
	})
	if err != nil {
		t.Fatalf("SearchMemory: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("expected results, got 0")
	}
}

func TestRouter_AddEpisodeAndQuery(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	store, err := NewStore(db, stubEmbedder{})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	router := NewMemoryRouter(store, Config{})

	ctx := context.Background()
	agentID := "researcher"
	threadID := "thread-1"

	_, err = router.AddEpisode(ctx, agentID, threadID,
		"Investigated ICE partnerships.", nil)
	if err != nil {
		t.Fatalf("AddEpisode: %v", err)
	}

	res, err := router.QueryAgentMemory(ctx, agentID, "ICE partnerships", nil, false, 5, nil)
	if err != nil {
		t.Fatalf("QueryAgentMemory: %v", err)
	}
	if len(res) == 0 {
		t.Fatalf("expected at least one result")
	}
}
