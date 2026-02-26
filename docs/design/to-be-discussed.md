# Design Decisions — Resolved

All items from the Phase 2 implementation review have been addressed.

---

## 1. ~~`GraphRAGQuerier.QueryWithContext` — FTS vs Vector Similarity~~ ✅

**Decision:** Option 3 — dual methods. `QueryWithContext` uses FTS (no embedding needed), `QueryWithEmbedding` uses pgvector cosine similarity (true GraphRAG).

FTS remains useful as a fallback when no embedding is available (e.g., entity extraction, budget-constrained contexts).

## 2. ~~`CompletionRequest` Lacks a `Model` Field~~ ✅

**Decision:** One provider instance per model (Go-idiomatic). No `Model` field on `CompletionRequest`. Callers construct separate `llm.Provider` instances for different models (e.g., one for fast correction, one for strong dialogue). Removed the `WithModel` hack from `llmcorrect.Corrector`.

## 3. ~~`chunks.npc_id` vs Generic `entity_id`~~ ✅

**Decision:** Renamed `npc_id` → `entity_id` in the chunks table schema and all Go code (`Chunk.NPCID` → `Chunk.EntityID`, `ChunkFilter.NPCID` → `ChunkFilter.EntityID`). Chunks can now be associated with any entity type, not just NPCs.

Note: `TranscriptEntry.NPCID` and `session_entries.npc_id` remain unchanged — those genuinely mean "which NPC spoke this entry."

## 4. ~~Entity IDs: TEXT vs UUID~~ ✅

**Decision:** Keep `TEXT PRIMARY KEY` as-is. More flexible than UUID (supports UUIDs, ULIDs, slugs). Callers generate IDs explicitly.

## 5. ~~Neighbors/FindPath — Outgoing-Only Traversal~~ ✅

**Decision:** Default to bidirectional. Both `Neighbors` and `FindPath` CTEs now follow edges in both directions (outgoing + incoming via UNION ALL). Better for knowledge graph exploration — "who knows Grimjaw?" needs incoming edges.

---

*Resolved 2026-02-26.*
