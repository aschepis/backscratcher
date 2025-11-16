# Memory Normalization Tool (`memory_normalize`)\*\*

### Goals

Add ability for agents to convert raw user statements into structured memories.

### Deliverables

- New MCP-safe tool name: `memory_normalize`
- Anthropic-backed Normalizer that returns:

  - normalized text
  - memory type
  - tags

- Integration with `StorePersonalMemory`

## NOTES:

- there is no need for database migrations. just add any schema changes directly to the schema creation code. The
  database will be created from scratch after this phase is complete.

## Database schema changes

memory_type – TEXT
e.g. "preference", "biographical", "habit", "goal", "value", "project", "other"

normalized_text – TEXT
the output from memory_normalize.normalized

tags_json – TEXT
JSON array of string tags: ["music","triathlon","age"]

## 1. Purpose & Goals

**Memory normalization** is the layer that converts messy, raw user statements into **consistent, structured, retrievable long-term memories**.

It should:

- Turn “I’m 43 and I love concept albums” into a **normalized**, third-person statement like:

  > “The user is 43 years old and loves concept albums.”

- Classify the memory into a **type**: `preference`, `biographical`, `habit`, `goal`, `value`, `project`, or `other`.
- Extract a short list of **tags/keywords** to support hybrid retrieval.
- Provide a stable, predictable shape of data that:

  - can be **embedded** (for vector search),
  - can be **stored** as a personal memory,
  - and can be **reliably retrieved** later.

---

## 2. Scope

Memory normalization covers:

- User statements that are candidates for **long-term memory**:

  - preferences, habits, values, biography, recurring patterns, goals, projects, etc.

- Agent-generated summaries that should become compact memories.
- Potentially “meta” info about the user’s working style, tools, and environment.

It **does not** cover:

- Transient small-talk and one-off context.
- Raw tools output or logs (those stay in conversation/threads or artifacts).
- Secrets (API keys, passwords, tokens) – those should never be stored as “memories”.

---

## 3. High-Level Behavior

1. An agent (e.g., your interviewer agent) decides:
   “This user statement is memory-worthy.”
2. It calls the `memory_normalize` tool with the raw statement.
3. The tool uses Anthropic to:

   - normalize the statement,
   - assign a type,
   - generate tags.

4. The agent then calls a **separate store tool** to insert it into long-term memory:

   - e.g., `memory_store_personal`

5. The enriched memory entry is then available to `memory_search_personal` and other retrieval tools.

---

## 4. Tool: `memory_normalize`

### 4.1. Name & Role

- MCP/Anthropic-safe name: **`memory_normalize`**
- Purpose: Convert a single raw statement into a structured memory triple:

  - normalized text
  - memory type
  - tags

This tool **does not persist** memory by itself. It only transforms.

### 4.2. Input Schema (requirements)

The tool input MUST:

- Accept a single object with:

  - `text` (string):
    The raw user statement (or agent summary) to normalize.

  - Optional future fields (might plan for now):

    - `source` (string): `"user" | "agent" | "system"`
    - `context` (string): short optional note about where this came from.

For now, keep it minimal:

- Required:

  - `text: string`

### 4.3. Output Schema (requirements)

The tool output MUST be a JSON object with:

- `normalized` (string, required)

  - Third-person, clearly phrased, and self-contained.
  - Should typically start with **“The user …”**.

- `type` (string, required)

  - One of the controlled vocabulary:

    - `"preference"`
    - `"biographical"`
    - `"habit"`
    - `"goal"`
    - `"value"`
    - `"project"`
    - `"other"`

- `tags` (array of string, required)

  - 3–8 lowercase tokens (no spaces; snake_case or hyphenated is fine), e.g.:

    - `"music"`, `"running"`, `"programming_languages"`, `"sleep_schedule"`

  - Must not include secrets, full sentences, or freeform text.

Constraints:

- If `normalized` is accidentally empty, fall back to the raw text.
- If `type` is unrecognized, set it to `"other"`.
- If `tags` are missing or empty, generate at least one generic tag like `"misc"`.

---

## 5. Normalizer Component (`memory/normalizer.go`)

### 5.1. Responsibilities

- Host the Anthropic client.
- Construct the appropriate `system` prompt and `user` message.
- Parse out the JSON from model responses.
- Enforce the output contract described above.
- Handle timeouts, errors, and retries gracefully.

### 5.2. Behavioral Requirements

- The normalizer must be **stateless** except for its Anthropic client configuration.
- It must:

  - Accept `rawText` and return `normalized`, `type`, `tags`.
  - NEVER store data on its own.
  - Be deterministic in structure (even if content can vary slightly).

- It should be **robust to messy inputs**, such as:

  - Multi-sentence statements.
  - Mixed preferences and facts.
  - “I used to…” vs “I now…” – treat the present state as the key.

### 5.3. System Prompt Requirements

The `system` prompt used for Anthropic should:

- Clearly describe the expected JSON output shape.
- Explicitly forbid non-JSON chatter.
- Define the type taxonomy and examples.
- Emphasize that secrets (tokens, passwords, API keys) must **not** be turned into memories.

Something along the lines of (high-level, not literal):

- “You are a memory normalization module…”
- “You must output valid JSON with keys: normalized, type, tags.”
- “Types: …”
- “Do not output anything other than JSON.”
- “Do not store sensitive credentials as tags or normalized text.”

(Your coding agent can generate the exact wording later.)

---

## 6. Integration with Store & Personal Memories

### 6.1. Data Model Requirements

Your existing store must support an enriched memory row with at least:

- `id`
- `agent_id` (e.g., `"global"` or specific agent)
- `memory_type` (matches `type` from normalize)
- `raw_text`
- `normalized_text`
- `tags_json`
- `embedding`
- `created_at`

The plan is:

1. `memory_normalize` returns `(normalized, type, tags)`.
2. A separate function (or tool) writes:

   - `raw_text` = original text
   - `normalized_text` = normalized
   - `memory_type` = type
   - `tags_json` = tags
   - `embedding` = embed(normalized_text)

### 6.2. Tool Integration

- Tools:

  - `memory_normalize`
  - `memory_store_personal` (future tool)

- Flow:

  1. Agent calls `memory_normalize`.
  2. Agent calls `memory_store_personal` with the returned values.

---

## 7. Agent Behavior & Usage Requirements

### 7.1. When should agents normalize?

Agents should **normalize and store** when they detect:

- User statements about:

  - age, background, education, past experience
  - preferences (tools, languages, music, food, schedule)
  - recurring habits (work schedule, training patterns)
  - long-term goals or projects
  - strongly held values (“I care a lot about X”)

### 7.2. Prompt Requirements for Agents

Each agent that is “allowed” to write memories should have instructions like:

> Whenever the user shares something about their long-term preferences, personal background, habits, values, or goals, call `memory_normalize` on that statement, then store the normalized memory in long-term memory.

Also:

- Avoid repeatedly storing the same fact — if it clearly duplicates an existing memory, you can skip it.
- Don’t store one-off, highly local things (“I’m cold right now”).

---

## 8. Testing Requirements

You already wrote some high-level tests. Let’s flesh them out into categories.

### 8.1. Unit Tests: Normalizer

- Input: `"I'm 43 and I love concept albums and triathlons."`
  Output:

  - `normalized` includes: “The user is 43”, “loves concept albums”, “likes triathlons”
  - `type` is `"preference"` or `"biographical"` (depending on your spec)
  - `tags` include words like `"music"`, `"triathlon"`, `"age"`.

- Input: `"I go running most mornings before work."`
  → type `"habit"`; tags `"running"`, `"exercise"`, `"morning"`.

- Input: non-English or mixed text → still valid JSON, still third-person.

- Input: empty or near-empty string → either return an error or fallback to safe default (define expected behavior).

### 8.2. Integration Tests: Store + Normalize

- Given a raw statement and calling the Go helper / tool pipeline:

  - A new `personal_memories` row is created.
  - `normalized_text` is non-empty.
  - `tags_json` is valid JSON array.
  - `embedding` is populated.

### 8.3. Behavioral Tests: Agent

- Conversation where the user shares multiple “about me” statements:

  - Agent calls `memory_normalize` a few times.

- Later, agent uses `memory_search_personal` to answer “What do you know about me?” and uses the normalized memories in its answer.

---

## 9. Non-Goals / Constraints

- The normalizer must **not**:

  - Persist anything by itself.
  - Expose secrets as tags or normalized text.
  - Store transient, ephemeral context.

- It’s okay if normalization is **best effort**; perfect classification is not required, but structure is.

---

## 10. Summary of Concrete Deliverables

**Tools / Components:**

1. `memory_normalize`

   - MCP-safe name
   - Input: `{ text: string }`
   - Output: `{ normalized: string, type: string, tags: string[] }`

2. `memory/normalizer.go`

   - Anthropic client
   - System prompt & parsing
   - Error handling & retries

3. Integration with `StorePersonalMemory`

   - Via a future `memory_store_personal` tool.

4. Tool schema registration in `ToolProvider`

   - For `memory_normalize`
   - Possibly for `memory_store_personal` later.

5. Tests

   - Unit: normalization correctness & shape.
   - Integration: storage pipeline.
   - Behavioral: agent flow.
