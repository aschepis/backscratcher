package memory

import (
	"context"
	"database/sql"
	"hash/fnv"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aschepis/backscratcher/staff/migrations"
	"github.com/rs/zerolog"

	_ "github.com/mattn/go-sqlite3"
)

type stubEmbedder struct{}

func (stubEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	return []float32{float32(len(text)), 1.0}, nil
}

// semanticEmbedder creates embeddings based on word content to simulate semantic similarity.
// Documents with overlapping words will have similar embeddings (high cosine similarity).
// This is deterministic and doesn't require external services, making it suitable for CI.
type semanticEmbedder struct {
	dimensions int
}

func newSemanticEmbedder(dimensions int) *semanticEmbedder {
	return &semanticEmbedder{dimensions: dimensions}
}

func (e *semanticEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	// Tokenize and normalize text
	words := strings.Fields(strings.ToLower(text))
	if len(words) == 0 {
		// Return zero vector for empty text
		return make([]float32, e.dimensions), nil
	}

	// Create embedding vector
	embedding := make([]float32, e.dimensions)

	// For each word, hash it to distribute across dimensions
	for _, word := range words {
		h := fnv.New32a()
		if _, err := h.Write([]byte(word)); err != nil {
			return nil, err
		}
		hash := h.Sum32()

		// Use hash to determine which dimensions this word influences
		// Each word contributes to multiple dimensions for better similarity detection
		for i := 0; i < 3; i++ { // Each word influences 3 dimensions
			dim := int((hash + uint32(i)*2654435761) % uint32(e.dimensions)) // nolint:gosec // Test code
			// Add contribution (using a sin function for varied values)
			embedding[dim] += float32(math.Sin(float64(hash+uint32(i))*0.1) + 1.0) // nolint:gosec // Test code
		}
	}

	// Normalize the vector (important for cosine similarity)
	var magnitude float32
	for _, val := range embedding {
		magnitude += val * val
	}
	magnitude = float32(math.Sqrt(float64(magnitude)))

	if magnitude > 0 {
		for i := range embedding {
			embedding[i] /= magnitude
		}
	}

	return embedding, nil
}

// setupTestDB creates an in-memory database and runs migrations
func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

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

	if err := migrations.RunMigrations(db, migrationsPath, zerolog.Nop()); err != nil {
		_ = db.Close() //nolint:errcheck // Cleanup on error
		t.Fatalf("failed to run migrations: %v", err)
	}

	return db
}

// fileExists checks if a file exists
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestStore_RememberGlobalFactAndSearch(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close() //nolint:errcheck // Test cleanup

	store, err := NewStore(db, stubEmbedder{}, zerolog.Nop())
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

	results, err := store.SearchMemory(ctx, &SearchQuery{
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
	db := setupTestDB(t)
	defer db.Close() //nolint:errcheck // Test cleanup

	store, err := NewStore(db, stubEmbedder{}, zerolog.Nop())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	router := NewMemoryRouter(store, Config{}, zerolog.Nop())

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

// TestSemanticEmbeddingSearch verifies that vector/embedding-based search
// correctly finds semantically similar content.
func TestSemanticEmbeddingSearch(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close() //nolint:errcheck // Test cleanup

	// Use semantic embedder that simulates real embeddings
	embedder := newSemanticEmbedder(128)
	store, err := NewStore(db, embedder, zerolog.Nop())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	ctx := context.Background()

	// Store several facts with different semantic content
	facts := []struct {
		content  string
		category string
	}{
		{"Adam loves programming in Go and building distributed systems", "technical"},
		{"Sarah enjoys Python and machine learning research", "technical"},
		{"Bob prefers hiking in the mountains and outdoor adventures", "outdoor"},
		{"Alice likes running marathons and training for triathlons", "sports"},
		{"Go is a great language for building scalable backend services", "technical"},
		{"Mountains provide excellent hiking trails and beautiful views", "outdoor"},
	}

	for _, fact := range facts {
		_, err := store.RememberGlobalFact(ctx, fact.content, 0.8, map[string]interface{}{
			"category": fact.category,
		})
		if err != nil {
			t.Fatalf("RememberGlobalFact: %v", err)
		}
	}

	// Test 1: Search for programming content
	t.Run("programming_query", func(t *testing.T) {
		queryEmb, err := embedder.Embed(ctx, "programming languages and software development")
		if err != nil {
			t.Fatalf("Embed query: %v", err)
		}

		results, err := store.SearchMemory(ctx, &SearchQuery{
			QueryEmbedding: queryEmb,
			Limit:          3,
			IncludeGlobal:  true,
			UseHybrid:      false, // Pure vector search
		})
		if err != nil {
			t.Fatalf("SearchMemory: %v", err)
		}

		if len(results) == 0 {
			t.Fatalf("expected results, got 0")
		}

		// The top result should be about programming/Go (semantic similarity)
		topResult := results[0].Item.Content
		if !strings.Contains(topResult, "programming") && !strings.Contains(topResult, "Go") && !strings.Contains(topResult, "language") {
			t.Logf("Top result: %s (score: %.3f)", topResult, results[0].Score)
			t.Errorf("expected programming-related content in top result")
		}

		// Verify scores are reasonable (> 0)
		for i, res := range results {
			if res.Score <= 0 {
				t.Errorf("result %d has non-positive score: %.3f", i, res.Score)
			}
			t.Logf("Result %d (score %.3f): %s", i, res.Score, res.Item.Content)
		}
	})

	// Test 2: Search for outdoor/mountain content
	t.Run("outdoor_query", func(t *testing.T) {
		queryEmb, err := embedder.Embed(ctx, "hiking trails and mountain exploration")
		if err != nil {
			t.Fatalf("Embed query: %v", err)
		}

		results, err := store.SearchMemory(ctx, &SearchQuery{
			QueryEmbedding: queryEmb,
			Limit:          3,
			IncludeGlobal:  true,
			UseHybrid:      false,
		})
		if err != nil {
			t.Fatalf("SearchMemory: %v", err)
		}

		if len(results) == 0 {
			t.Fatalf("expected results, got 0")
		}

		// Top results should be about outdoor/mountain activities
		foundOutdoor := false
		for i, res := range results {
			content := strings.ToLower(res.Item.Content)
			if strings.Contains(content, "hiking") || strings.Contains(content, "mountain") || strings.Contains(content, "outdoor") {
				foundOutdoor = true
			}
			t.Logf("Result %d (score %.3f): %s", i, res.Score, res.Item.Content)
		}

		if !foundOutdoor {
			t.Errorf("expected outdoor-related content in top results")
		}
	})

	// Test 3: Verify cosine similarity decreases for dissimilar content
	t.Run("similarity_comparison", func(t *testing.T) {
		// Create embeddings for similar and dissimilar queries
		programmingEmb, _ := embedder.Embed(ctx, "Go programming")

		programmingFactEmb, _ := embedder.Embed(ctx, facts[0].content) // Go programming fact
		hikingFactEmb, _ := embedder.Embed(ctx, facts[2].content)      // Hiking fact

		// Similar embeddings should have higher cosine similarity
		similarScore := CosineSimilarity(programmingEmb, programmingFactEmb)
		dissimilarScore := CosineSimilarity(programmingEmb, hikingFactEmb)

		t.Logf("Similar score (programming vs programming): %.3f", similarScore)
		t.Logf("Dissimilar score (programming vs hiking): %.3f", dissimilarScore)

		if similarScore <= dissimilarScore {
			t.Errorf("expected similar content to have higher cosine similarity: similar=%.3f, dissimilar=%.3f",
				similarScore, dissimilarScore)
		}
	})

	// Test 4: Hybrid search combining vector + FTS
	t.Run("hybrid_search", func(t *testing.T) {
		queryEmb, err := embedder.Embed(ctx, "Go programming")
		if err != nil {
			t.Fatalf("Embed query: %v", err)
		}

		results, err := store.SearchMemory(ctx, &SearchQuery{
			QueryText:      "Go programming",
			QueryEmbedding: queryEmb,
			Limit:          3,
			IncludeGlobal:  true,
			UseHybrid:      true, // Hybrid mode: vector + FTS
		})
		if err != nil {
			t.Fatalf("SearchMemory: %v", err)
		}

		if len(results) == 0 {
			t.Fatalf("expected results, got 0")
		}

		t.Logf("Hybrid search returned %d results", len(results))
		for i, res := range results {
			t.Logf("Result %d (score %.3f): %s", i, res.Score, res.Item.Content)
		}
	})
}
