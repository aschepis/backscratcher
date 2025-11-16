package memory

import "time"

// MemoryType describes the kind of memory item.
type MemoryType string

const (
	MemoryTypeFact    MemoryType = "fact"
	MemoryTypeEpisode MemoryType = "episode"
	MemoryTypeProfile MemoryType = "profile"
	MemoryTypeDocRef  MemoryType = "doc_ref"
)

// Scope indicates whether a memory is agent-local or globally shared.
type Scope string

const (
	ScopeAgent  Scope = "agent"
	ScopeGlobal Scope = "global"
)

// MemoryItem is a single unit of memory (fact, episode, etc.).
type MemoryItem struct {
	ID         int64                  `json:"id"`
	AgentID    *string                `json:"agent_id,omitempty"`  // nil for global
	ThreadID   *string                `json:"thread_id,omitempty"` // optional task/thread linkage
	Scope      Scope                  `json:"scope"`               // "agent" or "global"
	Type       MemoryType             `json:"type"`
	Content    string                 `json:"content"`
	Embedding  []float32              `json:"embedding,omitempty"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt  time.Time              `json:"created_at"`
	UpdatedAt  time.Time              `json:"updated_at"`
	Importance float64                `json:"importance"`
	// Normalization-enriched fields for personal memories
	RawContent string   `json:"raw_content,omitempty"` // original user/agent statement
	MemoryType string   `json:"memory_type,omitempty"` // preference, biographical, habit, goal, value, project, other
	Tags       []string `json:"tags,omitempty"`        // denormalized view of tags_json
}

// Artifact is a durable document / handoff object.
type Artifact struct {
	ID        int64                  `json:"id"`
	AgentID   *string                `json:"agent_id,omitempty"`  // creator, optional
	ThreadID  *string                `json:"thread_id,omitempty"` // optional
	Scope     Scope                  `json:"scope"`               // usually global
	Title     string                 `json:"title"`
	Body      string                 `json:"body"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt time.Time              `json:"created_at"`
	UpdatedAt time.Time              `json:"updated_at"`
}

// SearchQuery controls how we search memory.
type SearchQuery struct {
	QueryText      string
	QueryEmbedding []float32
	Types          []MemoryType
	MinImportance  float64
	After          *time.Time
	Before         *time.Time
	AgentID        *string
	IncludeGlobal  bool
	Limit          int
	UseHybrid      bool
	Tags           []string // Tags to match against memory tags (intersection)
	UseFTS         *bool    // Whether to use FTS (nil = default true when query text exists, false = explicitly disabled, true = explicitly enabled)
	MemoryTypes    []string // Filter by normalized memory types (preference, biographical, etc.)
}

// SearchResult includes a MemoryItem plus a relevance score.
type SearchResult struct {
	Item  MemoryItem
	Score float64
}

// Summarizer is used by the reflection pipeline.
type Summarizer interface {
	SummarizeEpisodes(episodes []MemoryItem) (string, error)
}
