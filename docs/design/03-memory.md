> *This document is derived from the Glyphoxa Design Document v0.2*

# Hybrid Memory System

Session memory is Glyphoxa's single biggest differentiator. The memory system uses a hybrid architecture: a "hot" layer that is always pre-woven into every NPC prompt (cheap, always relevant), and a "cold" layer that the LLM can query on demand via [MCP tools](04-mcp-tools.md) (deep, expensive, only when needed). A knowledge graph provides the structural backbone for both.

## Why Hybrid: The Tradeoff

Three approaches were evaluated during design:

| Approach | How It Works | Latency | Token Cost | Accuracy |
|---|---|---|---|---|
| Always Pre-woven | Every prompt gets full memory context injected. Pipeline always queries L1/L2/L3 and injects results. | No extra round-trip | High (bloated prompts even for "pass the salt") | High (LLM never lacks context) |
| Memory as MCP Only | LLM gets only recent transcript. Decides when to call memory tools. | Extra LLM round-trip (2x inference passes, ~800ms added) | Low (retrieves only what's needed) | Low (small models hallucinate rather than asking) |
| **Hybrid (chosen)** | Hot layer always injected. Cold layer available via MCP. Speculative pre-fetch bridges the gap. | No extra round-trip for common case. Cold queries overlap with pipeline. | Medium (identity always included, deep history only when needed) | High (identity is reliable; deep queries are explicit) |

## Hot Layer: Always-Available NPC Context

The hot layer is assembled by the Agent Orchestrator before every LLM call. It costs ~50ms and requires no LLM round-trip. It includes:

- **NPC Identity Graph Snapshot:** Pulled from the knowledge graph (L3). Contains the NPC's name, personality, relationships, current emotional state, known facts, and behavioral rules. Typically 200–500 tokens.
- **Recent Session Transcript:** The last ~5 minutes of the current session, with speaker labels. Provides immediate conversational context. Typically 500–1500 tokens.
- **Scene Context:** Current location, time of day, other NPCs present, active quests involving this NPC. Pulled from the knowledge graph. Typically 100–300 tokens.

The hot layer ensures the NPC is always in character, knows who it is, and can respond to immediate conversation without any tool calls. This covers ~80% of interactions.

## Cold Layer: MCP-Invocable Deep Memory

For the remaining ~20% of interactions — when a player references something from 3 sessions ago, asks about a distant location, or wants to recall a specific conversation — the LLM has access to memory tools via MCP:

| MCP Tool | Query Type | Estimated Latency | Example |
|---|---|---|---|
| `memory.search_sessions` | Semantic search over all session transcripts (L2 vector index) | 200–400ms | "What did the blacksmith say about the missing shipment?" |
| `memory.query_entities` | Structured query over the knowledge graph (L3) | 50–150ms | "Who is the mayor allied with?" |
| `memory.get_session_summary` | Retrieve the AI-generated summary of a specific past session | 30–80ms | "Summarize what happened last session." |
| `memory.search_facts` | Find specific facts/events with optional time filtering | 100–300ms | "When did the party first encounter the goblins?" |

These tools are subject to the [orchestrator-enforced budget system](04-mcp-tools.md#orchestrator-enforced-budget-tiers). In FAST mode, only `memory.query_entities` is declared to the LLM — slower memory tools are not visible. In STANDARD and DEEP modes, all memory tools are available.

## Speculative Pre-Fetch: Bridging the Gap

The hybrid approach includes an optimization that eliminates most cold-layer latency in practice. As STT partial transcripts arrive, a lightweight keyword extractor runs in parallel:

- **Entity Detection:** If the partial transcript contains a known NPC name, location, or item from the knowledge graph, the system pre-fetches that entity's full context from L3.
- **Temporal References:** If the partial contains phrases like "last time," "previously," "do you remember," the system fires a vector search over past sessions for the likely topic.
- **Results Ready Before Prompt:** Pre-fetch results are injected into the prompt alongside the hot layer. By the time the LLM sees the prompt, the relevant cold-layer context is already there — no tool call needed.

This means the LLM's memory MCP tools are a fallback for cases the pre-fetch didn't anticipate, not the primary retrieval mechanism. In testing, speculative pre-fetch should cover 60–80% of cold-layer queries at zero additional latency.

## Three Storage Layers

| Layer | What It Stores | Technology |
|---|---|---|
| Session Log (L1) | Full verbatim transcripts with timestamps, speaker labels, and NPC responses | PostgreSQL + full-text index (`tsvector`) |
| Semantic Index (L2) | Chunked, embedded session content. Each chunk tagged with session, speaker, NPC, topic. | pgvector (PostgreSQL extension) |
| Knowledge Graph (L3) | Entities (NPCs, locations, items, factions, players), typed relationships, and sourced facts | PostgreSQL adjacency tables + recursive CTEs. See [Knowledge Graph](10-knowledge-graph.md). |

All three layers share a single PostgreSQL instance. Self-hosted deployments run PostgreSQL via Docker Compose. This enables [GraphRAG queries](10-knowledge-graph.md#graphrag-combined-graph--vector-search) that combine graph traversal (L3) with vector similarity (L2) in a single SQL round-trip.

## Knowledge Graph Design

**Entity types:** NPC, Player, Location, Item, Faction, Event, Quest, Concept.

**Relationship types:** `KNOWS`, `LOCATED_AT`, `OWNS`, `MEMBER_OF`, `ALLIED_WITH`, `HOSTILE_TO`, `PARTICIPATED_IN`, `QUEST_GIVER`, `CHILD_OF`, `EMPLOYED_BY`, and custom user-defined relations.

**Fact provenance:** Each edge carries metadata — the session and timestamp where the fact was established, confidence level, whether it was stated by an NPC or inferred by the LLM, and whether the DM has confirmed or overridden it. Low-confidence facts are flagged for DM review rather than treated as ground truth.

**Scoped visibility:** NPCs only know what they would logically know. The knowledge graph supports per-NPC visibility scopes — an NPC in one city should not reference events in another unless connected via a relationship path.

### GraphRAG: Combining the Best of Both Worlds

Microsoft Research's GraphRAG approach demonstrates that combining knowledge graphs with vector retrieval dramatically outperforms pure RAG for complex multi-hop queries. Glyphoxa adopts this hybrid: use the knowledge graph (L3) for structured entity lookups and relationship traversal, use vector search (L2) for fuzzy semantic recall, and merge results before prompt injection.

## Transcript Correction Pipeline

Voice transcripts of TTRPG sessions are inherently noisy — fantasy names like "Eldrinax" frequently become "elder nacks" in STT output. Research shows NER F1 drops from 90% to 60% when WER exceeds 25%, and named entities are disproportionately affected because proper nouns are out-of-vocabulary. Glyphoxa uses a multi-stage correction pipeline to mitigate this before entity extraction.

### Stage 1: STT Keyword Boosting (Cascaded Only, Real-Time)

The STT provider (Deepgram Nova-3) receives a keyword boost list populated from the knowledge graph (L3). All known entity names — NPCs, locations, items, factions — are injected as boosted keywords. This alone reduces entity WER by 30–50% (per Deepgram benchmarks). The list is refreshed whenever the KG is updated.

This stage applies **only to the cascaded path** — S2S engines handle speech recognition internally, so keyword boosting is not available.

### Stage 2: Phonetic Entity Match (Cascaded: Inline, S2S: Background)

A phonetic matching step compares transcript words against the known entity list using a phonetic algorithm (Double Metaphone). Misheard spans that phonetically match a known entity are corrected: "elder nacks" → Metaphone: "ELTR NKS" ≈ "Eldrinax" → corrected. Candidates are ranked by Jaro-Winkler similarity on the normalized strings to handle near-matches.

**Go library:** `antzucaro/matchr` — provides Double Metaphone, Soundex, NYSIIS, Levenshtein, Damerau-Levenshtein, Jaro-Winkler, and Smith-Waterman in a single dependency. All functions are stateless and goroutine-safe. The default Double Metaphone max code length of 4 may need extending for longer fantasy names (fork or supplement with `vividvilla/metaphone` which supports configurable lengths up to 32).

On the **cascaded path**, this runs inline (< 1ms) between STT and LLM prompt composition. The LLM sees a cleaner transcript, and speculative pre-fetch triggers more accurately. On the **S2S path**, phonetic matching runs in background as part of session processing — there is no inline STT step to hook into.

### Stage 3: LLM Transcript Correction (Background Only)

A fast, cheap LLM (GPT-4o-mini, Gemini Flash) corrects remaining entity errors using the known entity list as context. This adds ~100–200ms and runs exclusively in the background session processing goroutine — never on the real-time voice path.

On the **cascaded path**, LLM correction triggers only for spans flagged as low-confidence by Deepgram's word-level confidence scores. On the **S2S path**, LLM correction runs on all entity-like spans — S2S transcripts do not include word-level confidence data.

### Per-Engine Summary

| Mitigation | Cascaded | S2S |
|---|---|---|
| Keyword boosting on STT | ✅ Deepgram keyword boost from KG | ❌ S2S handles own STT |
| Phonetic entity match | ✅ Inline (< 1ms, before LLM) | ✅ Background (before extraction) |
| LLM transcript correction | ✅ Background (low-confidence only) | ✅ Background (always — no confidence data) |
| Word-level confidence | ✅ Deepgram provides it | ❌ S2S transcripts lack it |

### KG as Entity List Source (Positive Feedback Loop)

The knowledge graph serves as the canonical entity list for all correction stages: keyword boosting, phonetic matching, and LLM correction context. As more entities are extracted and confirmed, future transcriptions become more accurate — the system improves with use.

Before the first session (or a new campaign), the DM registers key entity names via a pre-session setup step. This populates the initial KG and bootstraps the correction pipeline.

### Pre-session Entity Registration

The DM populates the knowledge graph before play begins. Three input methods, from quick to bulk:

| Method | Use Case | Example |
|---|---|---|
| **Discord slash commands** | Quick additions during prep or mid-session | `/entity add "Eldrinax" npc --personality "paranoid wizard"` |
| **Campaign config file** (YAML) | Bulk setup for a new campaign or arc. Loaded on startup or via `/campaign load`. | A YAML file listing entities with types, attributes, and relationships |
| **VTT import** | Migrating from an existing virtual tabletop with established world data | Import from Foundry VTT (JSON export), Roll20 (JSON), or a generic CSV format |

**Discord commands** are the primary interface — the DM is already in Discord running the session. Minimal commands:

- `/entity add <name> <type>` — register a new entity (optional `--attributes` JSON)
- `/entity list [type]` — list registered entities, optionally filtered by type
- `/entity remove <name>` — delete an entity
- `/campaign load <file>` — bulk-load entities from a YAML config file or VTT export

**Campaign config file** follows a declarative format:

```yaml
entities:
  - name: Eldrinax
    type: npc
    attributes:
      personality: paranoid wizard, speaks in riddles
      location: Tower of Whispers
  - name: Ironhold
    type: location
    attributes:
      description: dwarven mining city
relationships:
  - source: Eldrinax
    target: Ironhold
    type: LOCATED_AT
```

**VTT import** parses actor/item/scene data from Foundry VTT's `world.json` or Roll20's JSON export. Entity names and types are extracted; attributes are mapped best-effort. The DM reviews and confirms imported entities before they enter the knowledge graph.

## Session Processing Pipeline

After each exchange (or continuously on a background goroutine), the memory system processes the corrected transcript:

1. **Transcript Correction:** Phonetic match + LLM correction (stages 2–3 above) produce a corrected transcript from the raw STT/S2S output. The raw transcript is preserved in L1; the corrected version is used for steps 2–4.
2. **Chunking:** The corrected transcript is split into semantic chunks (by topic shift, scene break, or fixed-size windows with overlap).
3. **Embedding:** Each chunk is embedded and stored in the vector index (L2) with metadata tags.
4. **Entity Extraction:** An LLM pass extracts entities and relationships from each corrected chunk and upserts them into the knowledge graph (L3). Uses structured output schema for consistency.
5. **Summarization:** A session summary is generated and stored as a high-level "episode" node in the knowledge graph, linked to all entities that participated.

## DM Override and Corrections

- **Manual corrections:** The DM can edit entities, retcon facts, or delete incorrect extractions via a management interface (web dashboard or Discord commands).
- **Confidence scoring:** Facts extracted by the LLM carry a confidence score. High-confidence facts are auto-accepted. Low-confidence facts are queued for DM review.
- **Secret knowledge:** The DM can mark facts as secret (visible only to specific NPCs or hidden from all until revealed).

## S2S Engine Memory Trade-offs

When an NPC uses the `S2SEngine` (speech-to-speech), the memory integration differs from the cascaded pipeline:

**Hot context injection works the same.** The orchestrator calls `InjectTextContext` on the S2S session with the NPC identity snapshot, recent transcript, and scene context. Both OpenAI Realtime (`conversation.item.create`) and Gemini Live (`send_client_content`) support text context injection. This covers ~80% of interactions.

**No speculative pre-fetch.** S2S engines handle their own speech recognition internally — there are no STT partial transcripts to trigger pre-fetch. The LLM must use memory MCP tools explicitly (via the S2S tool calling bridge) when it needs deep context. This is acceptable because S2S sessions maintain longer context windows (128k for Gemini, 32k for OpenAI) and the hot layer covers most interactions.

**Cold-layer MCP tools are bridged.** Memory tools (`memory.search_sessions`, `memory.query_entities`, etc.) are declared as S2S function definitions and executed through the same `ToolCallHandler` as cascaded. They are subject to the orchestrator's budget tier — in FAST mode, only `memory.query_entities` is declared.

**Context window management differs.** Long TTRPG sessions may exceed S2S context limits. Gemini handles this automatically with context window compression. OpenAI's 32k limit requires the orchestrator to periodically summarize and re-inject context, or switch the NPC to cascaded for the remainder of the session.

See [Providers: VoiceEngine](02-providers.md#voiceengine-interface) for the VoiceEngine interface specification.

---

**See also:** [Architecture](01-architecture.md) · [MCP Tools](04-mcp-tools.md) · [NPC Agents](06-npc-agents.md) · [Knowledge Graph](10-knowledge-graph.md)
