> *This document is derived from the Glyphoxa Design Document v0.2*

# Open Questions

These are unresolved design questions that will be addressed through prototyping and team discussion.

## ~~Knowledge graph: in-process vs. Neo4j?~~ RESOLVED

Resolved by the [Knowledge Graph design](10-knowledge-graph.md). PostgreSQL adjacency tables with recursive CTEs, sharing the same instance as L1 and L2 (pgvector). No separate graph database. The `KnowledgeGraph` Go interface provides a migration path to Ent or Neo4j if scale ever demands it, but at TTRPG scale (~1000 entities) PostgreSQL is sub-millisecond for all graph operations.

## ~~Speech-to-speech models as an optional fast path?~~ RESOLVED

Resolved by the [Providers: VoiceEngine](02-providers.md#voiceengine-interface) design. S2S is a per-NPC engine choice (`engine: s2s`), not an automatic fast path. Both `CascadedEngine` and `S2SEngine` implement the same `VoiceEngine` interface. MCP tools bridge through S2S native tool calling with orchestrator-enforced budgets.

## ~~Entity extraction accuracy from noisy voice transcripts?~~ RESOLVED

Resolved by the [Transcript Correction Pipeline](03-memory.md#transcript-correction-pipeline). A multi-stage correction pipeline (STT keyword boosting from the knowledge graph, inline phonetic entity matching, background LLM transcript correction) brings entity recognition to ~85–90% F1 on known entities. Facts are auto-accepted above a 0.7 confidence threshold; below that, they're queued for DM review. See [Memory: DM Override](03-memory.md#dm-override-and-corrections).

## ~~Concurrent NPC audio on Discord?~~ RESOLVED

A single bot can only send one audio stream per guild. The design uses sequential NPC speech with a priority queue. Multiple bot accounts are deferred as a future consideration.

## Pricing model?

Community research shows $5–15/month is the sweet spot. Session length varies 2–8 hours. A hybrid model (base subscription with included hours + overage) is likely optimal. What are the right thresholds? Needs real usage data from alpha testing.

## Self-hosted vs. cloud-only?

Go's single-binary deployment makes self-hosting feasible. An open-source core with a managed cloud offering for convenience could serve both audiences. But this widens the testing matrix significantly.

## DM control interface design?

Voice commands, Discord slash commands, companion web dashboard, or all three? What about "puppet mode" where the DM speaks as the NPC with a voice filter rather than letting the AI generate dialogue? Needs UX prototyping.

## Game system licensing?

D&D 5e has a free SRD. Pathfinder 2e uses the ORC license. Many systems do not have permissive licenses. Should Glyphoxa ship pre-indexed SRDs, let DMs upload their own rulebooks, or both?

## S2S session lifecycle for long sessions?

A 4-hour TTRPG session may exceed S2S context limits. OpenAI Realtime has a 32k token window; Gemini Live has 128k with automatic compression. When should the orchestrator intervene? Options: periodic summarization + re-injection, automatic switch to cascaded when context is exhausted, or relying on Gemini's built-in compression.

## Voice consistency when switching engines?

If an NPC switches from S2S to cascaded mid-session (for slow tool calls), the voice will change — S2S preset voices don't match ElevenLabs cloned voices. Is this acceptable, or should the orchestrator avoid mixing engines within a single NPC's session?

---

**See also:** [Roadmap](09-roadmap.md) · [MCP Tools: Open Questions](04-mcp-tools.md#open-questions-for-tool-budgets)
