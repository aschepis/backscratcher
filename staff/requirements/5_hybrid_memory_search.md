# Hybrid Memory Search (`memory_search_personal`)\*\*

_(Enhance your existing memory search to be robust.)_

### Goals

Improve memory recall via hybrid retrieval:

- embeddings
- tag match
- optional FTS fallback
- merge & rank results

### Deliverables

- New tool: `memory_search_personal`
- Upgraded search logic inside `Store`:

  - vector similarity
  - tag intersection
  - optional FTS if enabled
  - final ranking

### Required Changes

- Extend memory search implementation
- Update ToolProvider to register new tool schema

### Tests

- Query “what do you know about me” returns correct memories
- Queries with synonyms hit via normalized_text
