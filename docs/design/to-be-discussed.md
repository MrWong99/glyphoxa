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

---

# Open Items — Phase 5

Items surfaced during the Phase 5 verification pass. Not bugs — design decisions
that need discussion before action.

## 6. OpenAI Realtime: Server `error` Events Silently Dropped

**Package:** `pkg/provider/s2s/openai/`

The OpenAI Realtime API emits `{"type":"error","error":{...}}` for non-fatal issues
(e.g., unintelligible audio, rate limits). These currently fall through the
`handleServerEvent` switch unhandled — the session continues but the caller has no
visibility into the error.

**Options:**
- A. Add a dedicated `Errors() <-chan error` to `s2s.SessionHandle` (interface change)
- B. Add an `OnError(func(error))` callback (mirror `OnToolCall` pattern)
- C. Surface errors via the existing `Err()` method (only available after channel close — not useful for transient errors)
- D. Log-and-ignore (acceptable for alpha, revisit for beta)

**Recommendation:** Option B — minimal interface change, consistent with `OnToolCall` callback pattern.

## 7. WebRTC: `OutputStream()` Channel Not Closed on `Disconnect()`

**Package:** `pkg/audio/webrtc/`

The `audio.Connection` interface doc says "all channels returned by Connection
methods are closed automatically when the connection terminates." The write-only
`outputCh` returned by `OutputStream()` is never closed on `Disconnect()`.

Closing it from `Disconnect()` would panic any caller still writing after
disconnect. This is a channel ownership question: write-only channels are
conventionally closed by the writer (the caller), not the reader (the platform).

**Options:**
- A. Close `outputCh` in `Disconnect()` — simple but panics on late writes
- B. Use a wrapper that converts writes-after-close to a no-op (recover from panic or check `disconnected` flag)
- C. Update the interface doc to clarify that write-only channels are caller-owned and not closed by the platform
- D. Return a struct with `Send(frame)` + `Close()` methods instead of a bare channel

**Recommendation:** Option C for now (doc fix), Option D for v1 (richer API).
