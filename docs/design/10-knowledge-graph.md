# Knowledge Graph (L3)

The knowledge graph is Glyphoxa's structured memory layer. It stores entities (NPCs, locations, items, factions) and typed relationships between them, forming the backbone of NPC identity, scene context, and cross-session continuity.

This document covers the storage schema, Go interfaces, query patterns, and migration strategy. For how the knowledge graph fits into the broader memory architecture, see [Memory](03-memory.md).

## Technology Choice: PostgreSQL

All three storage layers share a single PostgreSQL instance:

| Layer | Role | PostgreSQL Feature |
|---|---|---|
| L1 — Session Log | Full verbatim transcripts | Tables + full-text index (`tsvector`) |
| L2 — Semantic Index | Chunked embeddings for RAG | `pgvector` extension |
| **L3 — Knowledge Graph** | Entities, relationships, provenance | Adjacency tables + recursive CTEs |

**Why PostgreSQL for L3:**

- **One database engine.** No SQLite/PostgreSQL split to maintain. Self-hosted deployments use PostgreSQL via Docker Compose.
- **GraphRAG in a single query.** Combining graph traversal (L3) with vector similarity search (L2) is a single SQL query when both live in the same database. Impossible with a separate SQLite L3.
- **Recursive CTEs** handle multi-hop traversal and path finding. Sub-millisecond at Glyphoxa's scale (~100–1000 entities).
- **JSONB** provides flexible attribute storage without schema migration for every new field.

**Go libraries:** `jackc/pgx` v5 for the PostgreSQL driver (binary protocol, built-in connection pooling via `pgxpool`, native type support). `pgvector/pgvector-go` with the `pgxvec` subpackage for vector operations — registers pgvector types directly with pgx connections. Both L2 (pgvector similarity) and L3 (adjacency tables) queries run through the same pgx pool.

**Alternatives evaluated:** Cayley, Dgraph, Ent, EliasDB, SQLite, and Neo4j were all evaluated and rejected or deferred in favor of PostgreSQL unification.

## Schema

```sql
CREATE TABLE entities (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    type        TEXT NOT NULL,       -- 'npc', 'player', 'location', 'item', 'faction', 'event', 'quest', 'concept'
    name        TEXT NOT NULL,
    attributes  JSONB DEFAULT '{}',  -- personality, appearance, stats, etc.
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE relationships (
    source_id   UUID NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    target_id   UUID NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    rel_type    TEXT NOT NULL,        -- 'KNOWS', 'LOCATED_AT', 'OWNS', 'MEMBER_OF', 'ALLIED_WITH', etc.
    attributes  JSONB DEFAULT '{}',  -- sentiment, since, conditions, etc.
    provenance  JSONB DEFAULT '{}',  -- session_id, timestamp, confidence, source ('stated'|'inferred'), dm_confirmed
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (source_id, target_id, rel_type)
);

-- Fast lookups
CREATE INDEX idx_entities_type ON entities(type);
CREATE INDEX idx_entities_name ON entities(name);
CREATE INDEX idx_rel_source ON relationships(source_id);
CREATE INDEX idx_rel_target ON relationships(target_id);
CREATE INDEX idx_rel_type ON relationships(rel_type);
CREATE INDEX idx_rel_provenance_confidence ON relationships((provenance->>'confidence'));
```

### Entity Types

| Type | Examples | Typical Attributes |
|---|---|---|
| `npc` | Grimjaw the blacksmith, Elara the mage | personality, appearance, occupation, emotional_state, speaking_style |
| `player` | Thorin (Bard), Lyra (Ranger) | class, race, notable_traits |
| `location` | The Rusty Tankard, Ironhold, Dark Forest | description, atmosphere, notable_features |
| `item` | Sword of Dawn, Healing Potion | type, properties, magical |
| `faction` | Thieves Guild, Royal Guard | alignment, goals, influence |
| `event` | The Great Fire, Missing Shipment | date, consequences, participants |
| `quest` | Find the Lost Artifact | status, objectives, reward |
| `concept` | The Old Prophecy, Blood Magic | lore_text, common_knowledge |

### Relationship Types

| Relationship | Example | Directional? |
|---|---|---|
| `KNOWS` | NPC → NPC | Yes (asymmetric knowledge) |
| `LOCATED_AT` | NPC → Location | Yes |
| `OWNS` | NPC → Item | Yes |
| `MEMBER_OF` | NPC → Faction | Yes |
| `ALLIED_WITH` | Faction → Faction | Bidirectional (store both directions) |
| `HOSTILE_TO` | Faction → Faction | Bidirectional |
| `PARTICIPATED_IN` | NPC → Event | Yes |
| `QUEST_GIVER` | NPC → Quest | Yes |
| `CHILD_OF` | NPC → NPC | Yes |
| `EMPLOYED_BY` | NPC → NPC/Faction | Yes |

Custom relationship types are supported — `rel_type` is a free text field, not an enum. The entity extraction pipeline may introduce new types as the story evolves.

### Fact Provenance

Every relationship carries provenance metadata in the `provenance` JSONB column:

```json
{
    "session_id": "550e8400-e29b-41d4-a716-446655440000",
    "timestamp": "2026-02-20T19:45:00Z",
    "confidence": 0.85,
    "source": "inferred",
    "dm_confirmed": false
}
```

- **`confidence`**: 0.0–1.0. Facts above a configurable threshold (default 0.7) are auto-accepted. Below threshold, they're queued for [DM review](03-memory.md#dm-override-and-corrections).
- **`source`**: `"stated"` (NPC or player explicitly said it) or `"inferred"` (LLM entity extraction deduced it).
- **`dm_confirmed`**: Whether the DM has explicitly validated or overridden this fact.

## Go Interfaces

### KnowledgeGraph (Base)

Every implementation must satisfy this interface. It covers entity and relationship CRUD, traversal, and path finding.

```go
type KnowledgeGraph interface {
    // Entity CRUD
    AddEntity(ctx context.Context, entity Entity) error
    GetEntity(ctx context.Context, id string) (*Entity, error)
    UpdateEntity(ctx context.Context, id string, attrs map[string]any) error
    DeleteEntity(ctx context.Context, id string) error
    FindEntities(ctx context.Context, filter EntityFilter) ([]Entity, error)

    // Relationship CRUD
    AddRelationship(ctx context.Context, rel Relationship) error
    GetRelationships(ctx context.Context, entityID string, opts ...RelQueryOpt) ([]Relationship, error)
    DeleteRelationship(ctx context.Context, sourceID, targetID, relType string) error

    // Traversal
    Neighbors(ctx context.Context, entityID string, depth int, opts ...TraversalOpt) ([]Entity, error)
    FindPath(ctx context.Context, fromID, toID string, maxDepth int) ([]Entity, error)

    // Scoped visibility: returns the subgraph visible to a specific NPC
    VisibleSubgraph(ctx context.Context, npcID string) ([]Entity, []Relationship, error)

    // Snapshot for hot context assembly
    IdentitySnapshot(ctx context.Context, npcID string) (*NPCIdentity, error)
}
```

### GraphRAGQuerier (Optional)

Implementations with access to both the knowledge graph (L3) and the semantic index (L2) can implement this interface for combined queries. The PostgreSQL implementation satisfies this natively since pgvector and the adjacency tables share the same database.

```go
type GraphRAGQuerier interface {
    KnowledgeGraph

    // Combines graph traversal with vector similarity search.
    // graphScope limits the entity IDs considered. query is matched against L2 embeddings.
    // Returns merged results ranked by combined graph relevance + semantic similarity.
    QueryWithContext(ctx context.Context, query string, graphScope []string) ([]ContextResult, error)
}
```

Callers use Go type assertion to check for GraphRAG support:

```go
if ragQuerier, ok := graph.(GraphRAGQuerier); ok {
    // Combined L2+L3 query in a single round-trip
    results, err = ragQuerier.QueryWithContext(ctx, query, scope)
} else {
    // Fallback: graph traversal (L3) + separate vector search (L2), merged in application code
    entities, _ = graph.Neighbors(ctx, npcID, 2)
    embeddings, _ = vectorIndex.Search(ctx, query, topK)
    results = mergeResults(entities, embeddings)
}
```

## Query Patterns

### Direct Lookup: NPC Identity for Hot Context

```sql
-- Fetch an NPC's identity snapshot (personality, relationships, location, inventory)
SELECT e.name, e.attributes,
       json_agg(json_build_object(
           'rel_type', r.rel_type,
           'target_name', t.name,
           'target_type', t.type,
           'attributes', r.attributes
       )) AS relationships
FROM entities e
LEFT JOIN relationships r ON r.source_id = e.id
LEFT JOIN entities t ON t.id = r.target_id
WHERE e.id = $1
GROUP BY e.id;
```

Target: < 5ms. This runs on every LLM call as part of hot context assembly.

### Multi-Hop Traversal: "Who does the blacksmith's guild know?"

```sql
WITH RECURSIVE reachable AS (
    -- Base case: start entity
    SELECT e.id, e.name, e.type, 0 AS depth
    FROM entities e WHERE e.id = $1

    UNION ALL

    -- Recursive step: follow relationships up to N hops
    SELECT t.id, t.name, t.type, r.depth + 1
    FROM reachable r
    JOIN relationships rel ON rel.source_id = r.id
    JOIN entities t ON t.id = rel.target_id
    WHERE r.depth < $2  -- max depth parameter
)
SELECT DISTINCT id, name, type, depth FROM reachable;
```

Target: < 10ms for depth 3 at ~1000 entities.

### GraphRAG: Combined Graph + Vector Search

```sql
-- Find entities related to an NPC that are semantically relevant to a query
WITH npc_scope AS (
    -- Get entities within 2 hops of the NPC
    SELECT DISTINCT t.id
    FROM relationships r
    JOIN entities t ON t.id = r.target_id
    WHERE r.source_id = $1  -- npcID
    UNION
    SELECT $1
),
semantic_matches AS (
    -- Vector search over L2 chunks, scoped to entities in the graph neighborhood
    SELECT c.entity_id, c.content, c.embedding <=> $2 AS distance
    FROM chunks c
    WHERE c.entity_id IN (SELECT id FROM npc_scope)
    ORDER BY distance
    LIMIT 10
)
SELECT e.name, e.type, e.attributes, sm.content, sm.distance
FROM semantic_matches sm
JOIN entities e ON e.id = sm.entity_id
ORDER BY sm.distance;
```

This query is only possible because L2 and L3 share the same PostgreSQL instance. It's what `GraphRAGQuerier.QueryWithContext` executes under the hood.

### Scoped Visibility: "What does this NPC know?"

```sql
-- Return all entities reachable from an NPC via any relationship path
-- Used to enforce "NPCs only know what they would logically know"
WITH RECURSIVE visible AS (
    SELECT target_id AS id FROM relationships WHERE source_id = $1
    UNION
    SELECT r.target_id FROM relationships r
    JOIN visible v ON v.id = r.source_id
    WHERE r.rel_type IN ('KNOWS', 'LOCATED_AT', 'MEMBER_OF', 'ALLIED_WITH')
)
SELECT e.* FROM entities e
JOIN visible v ON v.id = e.id;
```

## Performance

At Glyphoxa's scale (~100–1000 entities, ~1000–5000 relationships):

| Operation | Expected Latency | Notes |
|---|---|---|
| Identity snapshot (single NPC) | < 5ms | Single JOIN, indexed |
| Neighbor query (depth 1) | < 2ms | Index scan on `source_id` |
| Multi-hop traversal (depth 3) | < 10ms | Recursive CTE, small dataset |
| GraphRAG combined query | < 20ms | CTE + pgvector similarity |
| Entity upsert | < 2ms | Single INSERT ON CONFLICT |
| Relationship upsert | < 2ms | Single INSERT ON CONFLICT |

These are well within the [hot memory assembly budget](00-overview.md#performance-targets) of < 50ms (target) / < 150ms (hard limit).

## Migration Strategy

1. **Now:** PostgreSQL adjacency tables with recursive CTEs. Covers all current requirements.
2. **If schema stabilizes:** Evaluate [Ent (entgo.io)](https://entgo.io) for type-safe code generation over the same PostgreSQL backend. Ent generates Go code from schema definitions and provides a graph traversal API. It runs on PostgreSQL — no database migration needed, only a code-level change.
3. **If scale demands it:** Migrate to Neo4j. This would mean moving L3 to an external process with Cypher queries. The `KnowledgeGraph` Go interface ensures the rest of the system is unaffected. Unlikely to be needed for TTRPG sessions (~1000 entities), but the interface makes it possible.

## Relation to Other Docs

- **[Memory](03-memory.md):** The knowledge graph is layer L3. Hot context assembly pulls NPC identity snapshots from it. Entity extraction writes to it.
- **[Architecture](01-architecture.md):** The `KnowledgeGraph` interface is listed in the Memory Subsystem layer.
- **[NPC Agents](06-npc-agents.md):** `knowledge_scope` and `secret_knowledge` properties map to graph visibility queries.
- **[MCP Tools](04-mcp-tools.md):** `memory.query_entities` executes graph queries via the `KnowledgeGraph` interface.

---

**See also:** [Memory](03-memory.md) · [Architecture](01-architecture.md) · [Technology](07-technology.md)
