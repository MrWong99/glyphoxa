# Design Decisions to Discuss

Issues found during Phase 2 implementation that warrant design-level discussion before proceeding.

---

## 1. `GraphRAGQuerier.QueryWithContext` — FTS vs Vector Similarity

**Design says:** GraphRAG should combine graph traversal (L3) with **pgvector** cosine similarity (L2) in a single SQL query. The example SQL uses `embedding <=> $2 AS distance`.

**Implementation does:** Full-text search (`ts_rank` + `plainto_tsquery`) instead of vector similarity, because `QueryWithContext` accepts `query string` — not a pre-computed embedding vector.

**The problem:** To do proper vector similarity, someone needs to convert the text query into an embedding first. The current `QueryWithContext(ctx, query string, graphScope []string)` signature doesn't provide that capability.

**Options:**
1. **Change the signature** to `QueryWithContext(ctx, embedding []float32, graphScope []string)` — caller is responsible for embedding. Cleaner separation but pushes work upstream.
2. **Inject an `embeddings.Provider`** into the postgres Store so it can embed internally. Couples the store to an embedding provider.
3. **Add a second method** — keep FTS-based `QueryWithContext` as a fallback and add `QueryWithEmbedding(ctx, embedding []float32, graphScope []string)` for true GraphRAG.
4. **Keep FTS for now** — Phase 2 roadmap says "GraphRAG combined query" but the FTS approach is pragmatic and works without an embeddings provider. Upgrade to vector search later.

**Recommendation:** Option 3 — dual methods. FTS is a useful fallback when no embedding is available (e.g., during entity extraction where you have a text query but no embedding budget).

---

## 2. `CompletionRequest` Lacks a `Model` Field

**Current state:** `llm.CompletionRequest` has no `Model` field. There's no way to select a per-request model.

**Impact:**
- The **transcript corrector** (`llmcorrect.Corrector`) has a `WithModel` option, but it prepends a `[model:xxx]` directive to the system prompt — this is a hacky workaround that no LLM provider will actually parse.
- The **sentence cascade** (Phase 3) needs to dispatch to a fast model AND a strong model from the same pipeline.
- The **hot context assembler** might want to use a cheaper model for entity extraction vs. the main dialogue model.

**Options:**
1. Add `Model string` to `CompletionRequest`. Simple, direct. Provider implementations route to the right model.
2. Make the provider itself model-aware (one provider instance per model). More Go-idiomatic but requires multiple provider instances.
3. Both — `Model` field in request overrides the provider's default, but provider defaults work for the common case.

**Recommendation:** Option 3 — it's backward-compatible (empty `Model` = use provider default).

---

## 3. `chunks.npc_id` vs Generic `entity_id`

**Design says:** The GraphRAG SQL example uses `c.entity_id` — chunks can be associated with any entity type (NPCs, locations, items, events).

**Implementation has:** `npc_id TEXT` in the chunks table. This is narrower than the design's intent. If a chunk describes a location or an item, it can't be properly associated.

**Impact:** GraphRAG queries that scope by entity ID will only work for NPC-associated chunks. You can't do "find chunks about the Rusty Tankard tavern" because the tavern is a location, not an NPC.

**Options:**
1. **Rename `npc_id` → `entity_id`** in schema + all code. Breaking change, but we're pre-alpha.
2. **Add `entity_id`** alongside `npc_id` as a more general association field. Redundant but backward-compatible.
3. **Keep `npc_id`** — chunks only represent conversation context, which is always tied to an NPC. Non-NPC entity search uses L3 graph queries.

**Recommendation:** Option 1 — rename to `entity_id` now while the schema is still fresh. Pre-alpha is the time for breaking changes.

---

## 4. Entity IDs: TEXT vs UUID

**Design says:** `id UUID PRIMARY KEY DEFAULT gen_random_uuid()` with auto-generated UUIDs.

**Implementation uses:** `id TEXT PRIMARY KEY` — caller must provide IDs.

**Impact:** Caller has to generate IDs themselves. This is more flexible (supports UUIDs, ULIDs, slug-based IDs) but means IDs aren't auto-generated.

**This is probably fine** — Go code typically generates IDs explicitly (e.g., `uuid.New().String()`). The TEXT type is strictly more flexible than UUID. Keeping it as-is unless there's a strong preference for auto-generated UUIDs.

---

## 5. Neighbors/FindPath — Outgoing-Only Traversal

**Current state:** Both `Neighbors` and `FindPath` recursive CTEs only follow outgoing edges (`source_id → target_id`).

**Design intent:** The interface says "following directed edges" which matches outgoing-only. `GetRelationships` supports bidirectional via `WithIncoming()`/`WithOutgoing()` options. `VisibleSubgraph` queries both directions.

**Potential issue:** "Who knows Grimjaw?" requires incoming edge traversal. A player asking "who is the blacksmith's ally?" needs outgoing. But "who considers the blacksmith an ally?" needs incoming. `Neighbors` only does outgoing.

**Options:**
1. Add `WithBidirectional()` to `TraversalOpt`. The CTE would UNION outgoing + incoming edges.
2. Keep outgoing-only for `Neighbors` — callers who need incoming use `GetRelationships(id, WithIncoming())`.
3. Default to bidirectional — better for knowledge graph exploration, which is the primary use case.

**Recommendation:** Option 1 — add the option, default to outgoing-only for backward compatibility. Bidirectional is opt-in.

---

*Created during Phase 2 implementation review, 2026-02-26.*
