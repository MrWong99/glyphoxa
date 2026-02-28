# :brain: Memory System

Glyphoxa uses a **3-layer hybrid memory architecture** backed by a single PostgreSQL instance. The design ensures NPCs maintain identity, recall past conversations, and build persistent world knowledge across sessions -- all while meeting the sub-150ms latency budget for real-time voice interaction.

For the full design rationale (hot/cold layer tradeoffs, speculative pre-fetch, S2S engine considerations), see [`design/03-memory.md`](design/03-memory.md).

---

## :bricks: Architecture Overview

Every NPC prompt is assembled from two retrieval paths:

- **Hot layer** (always injected, ~50ms): NPC identity snapshot (L3), recent transcript (L1), scene context (L3). Covers ~80% of interactions with zero tool calls.
- **Cold layer** (MCP tools, on-demand): Semantic search over past sessions (L2), structured graph queries (L3), session summaries. Covers the remaining ~20%.

All three storage layers share a single PostgreSQL connection pool (`pgxpool.Pool`), enabling GraphRAG queries that combine graph traversal with vector similarity in a single SQL round-trip.

| Layer | Name | What It Stores | PostgreSQL Feature | Go Interface |
|---|---|---|---|---|
| **L1** | Session Log | Timestamped transcript entries with speaker labels, raw + corrected text | Tables + GIN full-text index (`tsvector`) | `memory.SessionStore` |
| **L2** | Semantic Index | Chunked, embedded transcript content with metadata tags | `pgvector` extension (HNSW index, cosine distance) | `memory.SemanticIndex` |
| **L3** | Knowledge Graph | Entities, typed relationships, provenance, NPC identity snapshots | Adjacency tables + recursive CTEs + JSONB | `memory.KnowledgeGraph` / `memory.GraphRAGQuerier` |

---

## :scroll: Layer 1: Session Log

The session log is the hot, append-only transcript record. Every utterance -- player speech and NPC response -- is written here with full provenance.

### What It Stores

Each entry is a `memory.TranscriptEntry`:

| Field | Type | Description |
|---|---|---|
| `SpeakerID` | `string` | Player user ID or NPC identifier |
| `SpeakerName` | `string` | Human-readable speaker name |
| `Text` | `string` | Corrected transcript text (post-pipeline) |
| `RawText` | `string` | Original uncorrected STT output (preserved for debugging) |
| `NPCID` | `string` | NPC agent ID (empty for player entries) |
| `Timestamp` | `time.Time` | When the entry was recorded |
| `Duration` | `time.Duration` | Length of the utterance |

### How It Works

- **Write path:** `SessionStore.WriteEntry` appends to `session_entries` with all metadata.
- **Recency window:** `SessionStore.GetRecent(sessionID, duration)` returns entries from the last N minutes for hot context assembly. Typically called with a 5-minute window.
- **Full-text search:** `SessionStore.Search(query, opts)` uses PostgreSQL `plainto_tsquery` against a GIN index on the `text` column. Supports filtering by session, time range, and speaker.

### Schema

```sql
CREATE TABLE session_entries (
    id           BIGSERIAL    PRIMARY KEY,
    session_id   TEXT         NOT NULL,
    speaker_id   TEXT         NOT NULL DEFAULT '',
    speaker_name TEXT         NOT NULL DEFAULT '',
    text         TEXT         NOT NULL,
    raw_text     TEXT         NOT NULL DEFAULT '',
    npc_id       TEXT         NOT NULL DEFAULT '',
    timestamp    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    duration_ns  BIGINT       NOT NULL DEFAULT 0
);

-- Indexes for recency queries and full-text search
CREATE INDEX idx_session_entries_session_id         ON session_entries (session_id);
CREATE INDEX idx_session_entries_timestamp           ON session_entries (timestamp);
CREATE INDEX idx_session_entries_session_timestamp    ON session_entries (session_id, timestamp);
CREATE INDEX idx_session_entries_fts                  ON session_entries USING GIN (to_tsvector('english', text));
```

---

## :mag: Layer 2: Semantic Index

The semantic index enables embedding-based similarity search over chunked transcript content. This is the primary retrieval layer for "do you remember when..." queries.

### Chunk Structure

Each `memory.Chunk` carries pre-computed embeddings:

| Field | Type | Description |
|---|---|---|
| `ID` | `string` | Unique chunk identifier (UUID) |
| `SessionID` | `string` | Source session |
| `Content` | `string` | Raw text of the chunk (sentence, paragraph, or utterance) |
| `Embedding` | `[]float32` | Vector representation -- dimension must match index config |
| `SpeakerID` | `string` | Who produced this chunk |
| `EntityID` | `string` | Associated knowledge graph entity (for GraphRAG scoping) |
| `Topic` | `string` | Coarse topic label (e.g., "quest", "trade", "lore") |
| `Timestamp` | `time.Time` | When the chunk was recorded |

### How Chunks Are Created and Indexed

1. **Transcript correction** produces clean text (see [Transcript Correction](#pencil2-transcript-correction) below).
2. **Chunking** splits the corrected transcript by topic shift, scene break, or fixed-size windows with overlap.
3. **Embedding** produces a vector for each chunk using the configured embedding provider (e.g., OpenAI `text-embedding-3-small` at 1536 dimensions, or `nomic-embed-text` at 768).
4. **Indexing** upserts the chunk via `SemanticIndex.IndexChunk`, which stores it in the `chunks` table with an HNSW index for fast approximate nearest-neighbour search.

### Similarity Queries

`SemanticIndex.Search(embedding, topK, filter)` finds the closest chunks by **cosine distance** (`<=>` operator). Results are returned as `ChunkResult` pairs with the chunk and its distance score. Lower distance means higher similarity.

Filters narrow results by session, speaker, entity, or time range -- all applied as SQL `WHERE` conditions before the vector scan.

### Schema

```sql
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE chunks (
    id          TEXT         PRIMARY KEY,
    session_id  TEXT         NOT NULL,
    content     TEXT         NOT NULL,
    embedding   vector(1536),  -- dimension set at migration time
    speaker_id  TEXT         NOT NULL DEFAULT '',
    entity_id   TEXT         NOT NULL DEFAULT '',
    topic       TEXT         NOT NULL DEFAULT '',
    timestamp   TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX idx_chunks_session_id ON chunks (session_id);
CREATE INDEX idx_chunks_embedding  ON chunks USING hnsw (embedding vector_cosine_ops);
```

The vector dimension (e.g., `1536`) is baked into the column type at schema creation time via the `embeddingDimensions` parameter passed to `NewStore`.

---

## :spider_web: Layer 3: Knowledge Graph

The knowledge graph stores named entities and typed relationships, forming the structural backbone of NPC identity, scene context, and cross-session continuity.

For the full schema reference, query patterns, and migration strategy, see [`design/10-knowledge-graph.md`](design/10-knowledge-graph.md).

### Entity Types

| Type | Examples | Typical Attributes |
|---|---|---|
| `npc` | Grimjaw the blacksmith, Elara the mage | `personality`, `appearance`, `occupation`, `emotional_state`, `speaking_style` |
| `player` | Thorin (Bard), Lyra (Ranger) | `class`, `race`, `notable_traits` |
| `location` | The Rusty Tankard, Ironhold | `description`, `atmosphere`, `notable_features` |
| `item` | Sword of Dawn, Healing Potion | `type`, `properties`, `magical` |
| `faction` | Thieves Guild, Royal Guard | `alignment`, `goals`, `influence` |
| `event` | The Great Fire, Missing Shipment | `date`, `consequences`, `participants` |
| `quest` | Find the Lost Artifact | `status`, `objectives`, `reward` |
| `concept` | The Old Prophecy, Blood Magic | `lore_text`, `common_knowledge` |

Entity attributes are stored as JSONB, so arbitrary fields are supported without schema migration.

### Relationship Types

| Relationship | Example | Directional? |
|---|---|---|
| `KNOWS` | NPC -> NPC | Yes (asymmetric knowledge) |
| `LOCATED_AT` | NPC -> Location | Yes |
| `OWNS` | NPC -> Item | Yes |
| `MEMBER_OF` | NPC -> Faction | Yes |
| `ALLIED_WITH` | Faction -> Faction | Bidirectional (store both directions) |
| `HOSTILE_TO` | Faction -> Faction | Bidirectional |
| `PARTICIPATED_IN` | NPC -> Event | Yes |
| `QUEST_GIVER` | NPC -> Quest | Yes |
| `CHILD_OF` | NPC -> NPC | Yes |
| `EMPLOYED_BY` | NPC -> NPC/Faction | Yes |

Custom types are supported -- `rel_type` is a free text field.

### Fact Provenance

Every relationship carries provenance metadata in its `provenance` JSONB column:

```json
{
    "session_id": "550e8400-e29b-41d4-a716-446655440000",
    "timestamp": "2026-02-20T19:45:00Z",
    "confidence": 0.85,
    "source": "inferred",
    "dm_confirmed": false
}
```

- **`confidence`** (0.0--1.0): Facts above the configurable threshold (default 0.7) are auto-accepted. Below threshold, they are queued for DM review.
- **`source`**: `"stated"` (explicitly spoken) or `"inferred"` (LLM entity extraction deduced it).
- **`dm_confirmed`**: Whether the DM has validated this fact.

### Scoped Visibility

NPCs only see what they would logically know. `VisibleSubgraph(npcID)` returns the NPC entity plus all directly related entities and relationships. `IdentitySnapshot(npcID)` assembles a compact `NPCIdentity` struct for hot context injection, containing the NPC node, all its relationships, and the connected entities.

### GraphRAG Queries

The `GraphRAGQuerier` interface extends `KnowledgeGraph` with two combined retrieval methods:

| Method | Search Strategy | Use Case |
|---|---|---|
| `QueryWithContext(query, graphScope)` | Full-text search (ts_rank) scoped to graph neighbourhood | Fallback when no embedding vector is available |
| `QueryWithEmbedding(embedding, topK, graphScope)` | pgvector cosine similarity scoped to graph neighbourhood | Primary GraphRAG path -- highest quality results |

Both execute in a single SQL round-trip because L2 and L3 share the same PostgreSQL instance. Callers check for GraphRAG support via Go type assertion:

```go
if ragQuerier, ok := graph.(memory.GraphRAGQuerier); ok {
    results, err = ragQuerier.QueryWithEmbedding(ctx, embedding, 10, scope)
}
```

### Schema

```sql
CREATE TABLE entities (
    id          TEXT         PRIMARY KEY,
    type        TEXT         NOT NULL,
    name        TEXT         NOT NULL,
    attributes  JSONB        NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE TABLE relationships (
    source_id   TEXT         NOT NULL REFERENCES entities (id) ON DELETE CASCADE,
    target_id   TEXT         NOT NULL REFERENCES entities (id) ON DELETE CASCADE,
    rel_type    TEXT         NOT NULL,
    attributes  JSONB        NOT NULL DEFAULT '{}',
    provenance  JSONB        NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    PRIMARY KEY (source_id, target_id, rel_type)
);

-- Indexes
CREATE INDEX idx_entities_type ON entities (type);
CREATE INDEX idx_entities_name ON entities (name);
CREATE INDEX idx_rel_source    ON relationships (source_id);
CREATE INDEX idx_rel_target    ON relationships (target_id);
CREATE INDEX idx_rel_type      ON relationships (rel_type);
CREATE INDEX idx_rel_provenance_confidence ON relationships ((provenance->>'confidence'));
```

---

## :elephant: PostgreSQL Setup

### Required Extensions

The only required extension is **pgvector** (`CREATE EXTENSION IF NOT EXISTS vector`). It is installed automatically by the migration.

### Schema Creation

`postgres.Migrate(ctx, pool, embeddingDimensions)` creates all tables, indexes, and extensions idempotently using `CREATE TABLE IF NOT EXISTS` / `CREATE INDEX IF NOT EXISTS`. It runs three DDL batches in order:

1. **L1** -- `session_entries` table + GIN full-text index
2. **L2** -- `vector` extension + `chunks` table + HNSW index (dimension baked into column type)
3. **L3** -- `entities` + `relationships` tables + all graph indexes

### Automatic vs Manual Migration

- **Automatic:** `Migrate` runs on every `NewStore` call. Safe to call on every application start.
- **Manual required:** Changing `embeddingDimensions` after the first migration requires manually altering the `chunks.embedding` column type. The DDL uses `CREATE TABLE IF NOT EXISTS`, so a dimension change will not take effect automatically.

### Initialisation

```go
store, err := postgres.NewStore(ctx, dsn, 1536) // 1536 for OpenAI text-embedding-3-small
if err != nil { /* ... */ }
defer store.Close()

l1 := store.L1()  // memory.SessionStore
l2 := store.L2()  // memory.SemanticIndex
// store itself implements memory.KnowledgeGraph + memory.GraphRAGQuerier
```

---

## :gear: Configuration

Memory-related fields live in the `memory` section of the YAML config:

```yaml
memory:
  postgres_dsn: "postgres://user:pass@localhost:5432/glyphoxa?sslmode=disable"
  embedding_dimensions: 1536
```

| Field | YAML Key | Type | Default | Description |
|---|---|---|---|---|
| PostgreSQL DSN | `memory.postgres_dsn` | `string` | (none) | Connection string. When empty, long-term memory is unavailable. |
| Embedding dimensions | `memory.embedding_dimensions` | `int` | 1536 (warned if unset) | Must match the embedding model output. Common values: 1536 (OpenAI `text-embedding-3-small`), 768 (`nomic-embed-text`). |

### Transcript Correction Thresholds

These are set programmatically when constructing the pipeline:

| Parameter | Default | Description |
|---|---|---|
| Phonetic threshold | 0.70 | Minimum Jaro-Winkler score for a phonetic match to be accepted |
| Fuzzy threshold | 0.85 | Minimum Jaro-Winkler score for the fuzzy fallback (no phonetic code overlap) |
| LLM low-confidence threshold | 0.50 | STT word confidence below which a word is flagged for LLM correction |
| LLM temperature | 0.1 | Sampling temperature for the LLM correction model |

### Session Lifecycle Parameters

| Parameter | Default | Description |
|---|---|---|
| Context window threshold | 0.75 | Fraction of `MaxTokens` at which summarisation triggers |
| Consolidation interval | 30 minutes | How often the consolidator flushes messages to L1 |
| Reconnect max retries | 10 | Maximum reconnection attempts before giving up |
| Reconnect initial backoff | 1 second | Backoff between retries (doubles each attempt) |
| Reconnect max backoff | 30 seconds | Upper limit on exponential backoff |

---

## :mag_right: Inspecting Memory State

### Count Entries Per Session

```sql
SELECT session_id, count(*) AS entry_count
FROM session_entries
GROUP BY session_id
ORDER BY entry_count DESC;
```

### Recent Entries for a Session

```sql
SELECT speaker_name, text, timestamp
FROM session_entries
WHERE session_id = 'your-session-id'
ORDER BY timestamp DESC
LIMIT 20;
```

### Compare Raw vs Corrected Text

```sql
SELECT raw_text, text AS corrected_text, timestamp
FROM session_entries
WHERE session_id = 'your-session-id'
  AND raw_text != text
ORDER BY timestamp DESC;
```

### Check Embedding Coverage

```sql
-- Total chunks and how many have embeddings
SELECT count(*) AS total_chunks,
       count(embedding) AS with_embedding
FROM chunks;

-- Chunks per session
SELECT session_id, count(*) AS chunk_count
FROM chunks
GROUP BY session_id
ORDER BY chunk_count DESC;
```

### Find Nearest Chunks to a Known Embedding

```sql
-- Replace $1 with an actual embedding vector
SELECT id, content, embedding <=> $1 AS distance
FROM chunks
ORDER BY distance
LIMIT 10;
```

### List All Entities by Type

```sql
SELECT type, count(*) AS entity_count
FROM entities
GROUP BY type
ORDER BY entity_count DESC;
```

### Inspect an NPC's Relationships

```sql
SELECT r.rel_type,
       t.name AS target_name,
       t.type AS target_type,
       r.provenance->>'confidence' AS confidence,
       r.provenance->>'source' AS source
FROM relationships r
JOIN entities t ON t.id = r.target_id
WHERE r.source_id = 'your-npc-id'
ORDER BY r.rel_type, t.name;
```

### Find Low-Confidence Facts Pending DM Review

```sql
SELECT e1.name AS source, r.rel_type, e2.name AS target,
       r.provenance->>'confidence' AS confidence,
       r.provenance->>'source' AS source_type
FROM relationships r
JOIN entities e1 ON e1.id = r.source_id
JOIN entities e2 ON e2.id = r.target_id
WHERE (r.provenance->>'confidence')::float < 0.7
  AND (r.provenance->>'dm_confirmed')::boolean IS NOT TRUE
ORDER BY (r.provenance->>'confidence')::float ASC;
```

### Multi-Hop Graph Traversal

```sql
WITH RECURSIVE reachable AS (
    SELECT id, ARRAY[id] AS visited, 0 AS depth
    FROM entities WHERE id = 'start-entity-id'

    UNION ALL

    SELECT e.id, r.visited || e.id, r.depth + 1
    FROM reachable r
    JOIN relationships rel ON rel.source_id = r.id
    JOIN entities e ON e.id = rel.target_id
    WHERE r.depth < 3
      AND NOT (e.id = ANY(r.visited))
)
SELECT DISTINCT e.id, e.name, e.type, rc.depth
FROM reachable rc
JOIN entities e ON e.id = rc.id
WHERE rc.id != 'start-entity-id'
ORDER BY rc.depth, e.name;
```

---

## :pencil2: Transcript Correction

Raw STT output is notoriously poor with fantasy proper nouns -- "Eldrinax" becomes "elder nacks", "Ironhold" becomes "iron hold". Glyphoxa applies a two-stage correction pipeline before memory storage.

### Pipeline Overview

```
Raw STT Text
    |
    v
[Stage 1: Phonetic Entity Matching]   <-- in-process, < 1ms
    |
    v
[Stage 2: LLM Correction]             <-- background only, ~100-200ms
    |
    v
Corrected Text -> L1 (session log) + L2 (chunking/embedding) + L3 (entity extraction)
```

The pipeline is configured via functional options:

```go
pipeline := transcript.NewPipeline(
    transcript.WithPhoneticMatcher(phonetic.New(
        phonetic.WithPhoneticThreshold(0.70),
        phonetic.WithFuzzyThreshold(0.85),
    )),
    transcript.WithLLMCorrector(llmcorrect.New(llmProvider)),
    transcript.WithLLMOnLowConfidence(0.5),
)
```

### Stage 1: Phonetic Entity Matching

The `phonetic.Matcher` uses **Double Metaphone** encoding combined with **Jaro-Winkler** string similarity:

1. **Phonetic candidate filtering:** Double Metaphone codes are computed for each word in the transcript and each known entity. If any code overlaps, the entity becomes a candidate.
2. **Jaro-Winkler ranking:** Among phonetic candidates, the entity with the highest Jaro-Winkler similarity (case-insensitive) is selected -- provided it exceeds the phonetic threshold (default 0.70).
3. **Fuzzy fallback:** When no phonetic candidate exists, a pure Jaro-Winkler pass runs against all entities with a higher threshold (default 0.85).

Multi-word entities are supported. The pipeline tests n-gram windows (longest first) so that "Tower of Whispers" matches before "Tower" alone. Three scoring strategies are used:

- Full-string comparison ("elder nacks" vs "eldrinax")
- Space-stripped comparison ("eldernacks" vs "eldrinax")
- Best pairwise word comparison (handles partial token alignment)

**Library:** `antzucaro/matchr` -- provides Double Metaphone, Jaro-Winkler, and other distance metrics. All functions are stateless and goroutine-safe.

**Performance:** Entity data can be precomputed once via `phonetic.PrepareEntities(entities)` and reused across calls via `Matcher.MatchPrepared`, avoiding redundant phonetic code generation.

### Stage 2: LLM Correction

The `llmcorrect.Corrector` handles residual errors the phonetic stage missed:

- Sends transcript text + known entity list to a fast LLM (e.g., GPT-4o-mini, Gemini Flash) with a conservative system prompt.
- Only words with STT confidence below the threshold (default 0.5) AND not already corrected by the phonetic stage are flagged as candidates.
- When no per-word confidence data is available (e.g., S2S transcripts), all entity-like spans are submitted.
- The LLM returns structured JSON with corrected text and itemised substitutions.

**Safety:** The `verifyCorrectedText` function cross-references actual token-level changes against the LLM's declared corrections list. Undeclared changes are reverted -- the LLM cannot silently alter non-entity words. Unparseable responses fall back to the original text (graceful degradation).

### Per-Engine Behaviour

| Correction Stage | Cascaded Path | S2S Path |
|---|---|---|
| STT keyword boosting | Inline (Deepgram keyword boost from KG) | Not available (S2S handles own STT) |
| Phonetic entity matching | Inline (< 1ms, before LLM prompt) | Background (before entity extraction) |
| LLM transcript correction | Background (low-confidence spans only) | Background (all entity-like spans) |
| Word-level confidence data | Available (Deepgram provides it) | Not available |

### Positive Feedback Loop

The knowledge graph serves as the canonical entity list for all correction stages. As more entities are extracted and confirmed, future transcriptions become more accurate -- the system improves with use.

---

## :arrows_counterclockwise: Session Lifecycle

Long TTRPG sessions (4+ hours) require active management of context windows, memory durability, and connection stability.

### Context Window Management

`ContextManager` tracks token usage using a 1-token-per-4-characters heuristic and triggers automatic summarisation when the estimated count exceeds `thresholdRatio * maxTokens` (default: 75% of the context window).

When triggered:
1. The **oldest half** of messages is extracted.
2. An `LLMSummariser` compresses them into a concise summary preserving key decisions, revealed information, emotional states, promises, and game-mechanical outcomes.
3. The summary replaces the old messages as a system-level `[Previous conversation summary]` prefix.
4. Multiple summaries accumulate over a long session -- they are prepended to the message list in order.

### Memory Consolidation

`Consolidator` runs a background goroutine that periodically (default: every 30 minutes) flushes conversation messages from the `ContextManager` to the L1 session store. This ensures:

- Long-running sessions persist even if the process crashes.
- Messages pruned by context window summarisation are durably stored in L1 before being removed from the working set.
- Synthetic summary messages (prefixed with `[`) are skipped -- only real conversation entries are written.

The consolidator tracks its write cursor (`lastIndex`) to avoid duplicates. `ConsolidateNow` forces an immediate flush (used during graceful shutdown).

### Memory Guard

`MemoryGuard` wraps a `SessionStore` and makes all operations non-fatal. If PostgreSQL is temporarily unavailable (restart, network partition), operations return defaults (empty slices, zero counts) and log warnings instead of propagating errors. The voice engine continues operating in degraded mode.

```go
guard := session.NewMemoryGuard(store.L1())
// guard.IsDegraded() reports current health
```

### Reconnection Handling

`Reconnector` monitors the audio connection and automatically reconnects on disconnection:

- **Exponential backoff:** Starts at 1 second, doubles each attempt, caps at 30 seconds.
- **Max retries:** 10 attempts before giving up.
- **State preservation:** The `OnReconnect` callback receives the new connection, allowing the session to resume with NPC state intact. Old connections are cleaned up.
- **Signal-based:** The monitor goroutine waits for `NotifyDisconnect()` rather than polling.

### Lifecycle Summary

```
Session Start
    |
    +-- ContextManager created (maxTokens, thresholdRatio, summariser)
    +-- Consolidator started (30-min periodic flush to L1)
    +-- MemoryGuard wraps L1 store (non-fatal operations)
    +-- Reconnector monitors audio connection
    |
    v
Session Active (hours)
    |
    +-- Messages accumulate in ContextManager
    +-- At 75% token capacity -> oldest half summarised
    +-- Every 30 min -> new messages flushed to L1
    +-- On audio disconnect -> exponential backoff reconnect
    +-- On memory failure -> degraded mode (continues operating)
    |
    v
Session End
    |
    +-- ConsolidateNow() flushes remaining messages
    +-- Consolidator.Stop()
    +-- Reconnector.Stop()
    +-- Background processing: chunking, embedding (L2), entity extraction (L3)
```

---

## :link: See also

- [`architecture.md`](architecture.md) -- System architecture and component overview
- [`configuration.md`](configuration.md) -- Full configuration reference
- [`npc-agents.md`](npc-agents.md) -- NPC agent system and identity management
- [`design/03-memory.md`](design/03-memory.md) -- Design rationale for the hybrid memory architecture
- [`design/10-knowledge-graph.md`](design/10-knowledge-graph.md) -- Knowledge graph schema, query patterns, and migration strategy
