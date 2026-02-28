# Roadmap

Glyphoxa is in early alpha. This roadmap prioritizes **interchangeable components**, **clean API boundaries**, and **usable interfaces** over feature completeness. Every phase starts with interface design and ends with at least two concrete implementations to prove the abstraction holds.

## Design Principles for Development

1. **Interface-first, implementation-second.** Define the Go interface, write tests against it with mocks, then build concrete providers. If the interface feels wrong during implementation, fix the interface before continuing.
2. **Every component is swappable.** A user should be able to replace any provider (LLM, STT, TTS, audio platform, memory backend, tool server) via configuration — never by editing application code.
3. **Clear package boundaries.** Each package owns one concern. No package imports from a sibling's internals. Communication between layers happens through interfaces and Go channels.
4. **Configuration-driven wiring.** Provider selection, NPC definitions, tool registration, and budget tiers are all declarative config (YAML). The application reads config and wires together the right implementations at startup.
5. **Test at the boundary.** Integration tests run against the interface, not the implementation. A test suite for `LLMProvider` must pass identically for OpenAI, Anthropic, Gemini, and Ollama.

## Phase 1: Core Interfaces and Project Scaffold ✅

**Goal:** Establish the package structure, define all primary Go interfaces, and prove end-to-end audio flow with one provider per slot.

### Package Structure

```
glyphoxa/
├── cmd/glyphoxa/          # CLI entrypoint, config loading, dependency wiring
├── config/                # Config schema, validation, provider registry
├── engine/                # VoiceEngine interface, CascadedEngine, S2SEngine
│   ├── cascaded/          # STT→LLM→TTS pipeline implementation
│   └── s2s/               # Speech-to-speech engine implementation
├── provider/              # Provider interfaces + concrete implementations
│   ├── llm/               # LLMProvider interface + openai/, anthropic/, gemini/, ollama/
│   ├── stt/               # STTProvider interface + deepgram/, assemblyai/, whisper/
│   ├── tts/               # TTSProvider interface + elevenlabs/, cartesia/, coqui/
│   ├── s2s/               # S2SProvider interface + geminilive/, openairealtime/
│   ├── embeddings/        # EmbeddingsProvider interface + openai/, voyage/, nomic/
│   └── vad/               # VADEngine interface + silero/
├── audio/                 # AudioPlatform interface, AudioMixer, frame types
│   ├── discord/           # Discord voice transport
│   └── webrtc/            # WebRTC transport (future)
├── agent/                 # NPCAgent, AgentRouter, orchestrator, turn-taking
├── memory/                # MemoryStore interface, hot layer, cold layer
│   ├── session/           # L1 session log (PostgreSQL)
│   ├── semantic/          # L2 vector index (pgvector)
│   └── graph/             # L3 knowledge graph (PostgreSQL adjacency tables)
├── mcp/                   # MCP host, tool registry, budget enforcer, calibration
├── transcript/            # Transcript correction pipeline (phonetic match, LLM correction)
└── internal/              # Shared utilities (logging, metrics, test helpers)
```

### Interface Definitions

Define and document all primary interfaces. Each interface gets:
- A Go interface type with full godoc
- A `mock/` subpackage with a test double
- An interface compliance test suite that any implementation must pass

**Priority interfaces (define first):**
1. `provider/llm.Provider` — streaming completions, tool calling, token counting, capabilities
2. `provider/stt.Provider` — streaming sessions, partials/finals channels, keyword boosting
3. `provider/tts.Provider` — streaming synthesis from text channel to audio channel, voice profiles
4. `audio.Platform` — connect, input/output streams, participant lifecycle
5. `engine.VoiceEngine` — the unifying abstraction over cascaded and S2S paths

**Secondary interfaces (define alongside first implementations):**
6. `provider/s2s.Provider` — audio-in/audio-out sessions, tool calling bridge, context injection
7. `provider/embeddings.Provider` — single/batch embedding, dimensionality, model ID
8. `provider/vad.Engine` — frame processing, speech/silence events
9. `memory.KnowledgeGraph` — entity/relationship CRUD, traversal, scoped visibility, identity snapshots
10. `memory.GraphRAGQuerier` — combined graph + vector search (optional extension of KnowledgeGraph)
11. `mcp.Host` — tool discovery, registry, execution, budget enforcement

### First Implementation Pass

Build one concrete provider per interface to prove the pipeline works end-to-end:
- **STT:** Deepgram Nova-3 (streaming WebSocket)
- **LLM:** OpenAI GPT-4o-mini via `any-llm-go` (streaming with tool calling)
- **TTS:** ElevenLabs Flash v2.5 (streaming WebSocket)
- **VAD:** Silero via `silero-vad-go`
- **Audio:** Discord via `discordgo`

**End-to-end milestone:** Discord bot joins voice → captures audio → VAD → STT → LLM (single static persona) → TTS → plays back. Measure and log latency at every stage boundary.

### Config and Wiring

- Define the YAML config schema for provider selection, credentials, and NPC definitions
- Build a provider registry that maps config strings to constructor functions
- Wire everything in `cmd/glyphoxa/` — config load → provider instantiation → engine assembly → run

## Phase 2: Provider Breadth and Memory Foundation ✅

**Goal:** Prove interchangeability by adding a second provider for each slot. Build the memory subsystem with clean separation between layers.

### Second Provider Pass

Add at least one alternative for every provider interface. Run the same compliance test suite against both:

| Interface | Primary | Secondary |
|---|---|---|
| LLM | OpenAI (GPT-4o-mini) | Anthropic (Claude Sonnet) |
| STT | Deepgram | whisper.cpp (local) |
| TTS | ElevenLabs | Coqui XTTS (local) |
| Embeddings | OpenAI text-embedding-3-small | nomic-embed-text (local) |

Switching between providers must be a single config change with zero code modifications. If it's not, the interface is wrong — fix it.

### Memory Subsystem

Build all three memory layers behind clean interfaces:

1. **L1 — Session Log:** PostgreSQL storage with full-text index. Continuous transcript write with speaker labels and timestamps. Interface: `memory/session.Store`.
2. **L2 — Semantic Index:** Chunk, embed, and store session content in pgvector. RAG retrieval with metadata filtering. Interface: `memory/semantic.Index`.
3. **L3 — Knowledge Graph:** PostgreSQL adjacency tables. `KnowledgeGraph` and `GraphRAGQuerier` interfaces. Entity extraction pipeline from corrected transcripts.

**Key design constraint:** L1, L2, and L3 share a single PostgreSQL instance but are accessed through separate interfaces. A future migration (e.g., swapping L3 to Neo4j or L2 to a standalone vector DB) should only require implementing the new backend behind the existing interface.

### Hot Layer Assembly

Build the orchestrator's hot context assembly:
- NPC identity snapshot from L3 (`IdentitySnapshot`)
- Recent session transcript from L1
- Scene context from L3

Target: < 50ms assembly time. This runs before every LLM call and must never require an LLM round-trip.

### Transcript Correction Pipeline

Implement the multi-stage correction pipeline as a composable chain:
1. **Phonetic entity match** (inline, < 1ms) — Double Metaphone + Jaro-Winkler against known entity list from L3
2. **LLM transcript correction** (background) — cheap LLM corrects remaining entity errors

Each stage is independently testable and skippable via config. The pipeline reads the entity list from the `KnowledgeGraph` interface — it does not directly access the database.

## Phase 3: MCP Tools and Budget Enforcement ✅

**Goal:** Build the tool execution layer with performance budgets that enforce latency guarantees by construction.

### MCP Host

Implement the MCP host using `modelcontextprotocol/go-sdk`:
- Tool discovery on server connection
- In-memory tool registry with schema and latency metadata
- Tool execution with timeout enforcement
- Support for both stdio (local) and HTTP/SSE (remote) transports

### Budget Enforcer

The budget enforcer is the core of Glyphoxa's tool strategy. It controls which tools the LLM can see based on the active latency tier:

- **FAST** (≤ 500ms): dice-roller, `memory.query_entities`, file-io, music-ambiance
- **STANDARD** (≤ 1500ms): FAST tools + `memory.search_sessions`, rules-lookup, session-manager
- **DEEP** (≤ 4000ms): All tools including image-gen, web-search

Implementation:
- Strip over-budget tools from function definitions before they reach the LLM
- Tier selection logic in the orchestrator (conversation state, keyword detection, DM commands)
- No prompt-based enforcement — the LLM never sees tools it can't afford to call

### Calibration Protocol

Build the calibration system:
- Synthetic probe on server connection
- Rolling window measurement (last 100 calls, p50 and p99)
- Health scoring with automatic tier demotion for degraded tools

### Built-in Tool Servers

Ship core tools as bundled Go MCP servers:
- **dice-roller** — `roll(expression)`, `roll_table(table_name)`
- **memory tools** — `search_sessions`, `query_entities`, `get_summary`, `search_facts` (backed by L1/L2/L3 interfaces)
- **rules-lookup** — `search_rules(query, system)` (D&D 5e SRD)
- **file-io** — `write_file`, `read_file`

### MCP Bridge for S2S

Wire MCP tools into S2S sessions:
- Convert `MCPToolDef` schemas to S2S-native function definitions
- Execute tools through the same `ToolCallHandler` as cascaded
- Respect budget tiers — only declare tier-appropriate tools

## Phase 4: NPC Agents and Orchestration ✅

**Goal:** Build the agent layer that brings NPCs to life with distinct personalities, memories, and voice profiles.

### NPC Agent Schema

Declarative NPC definitions in YAML:

```yaml
npcs:
  - name: Grimjaw
    engine: cascaded
    personality: "Gruff but kind. Speaks in short sentences."
    voice:
      provider: elevenlabs
      voice_id: "Antoni"
      pitch: -2
      speed: 0.9
    knowledge_scope: ["Ironhold", "Missing Shipment"]
    tools: ["dice-roller", "memory.*"]
    budget_tier: standard
```

The agent loader reads this config and wires together the correct `VoiceEngine`, provider instances, memory scopes, and tool sets. No NPC-specific code — everything is configuration.

### Agent Orchestrator

Build the orchestration layer:
- **Address detection:** Determine which NPC was spoken to (by name, conversational context, or DM command)
- **Turn-taking:** Priority queue for NPC speech output. Natural pacing with configurable silence gaps
- **Cross-NPC awareness:** Shared recent-utterance buffer. Each NPC's context includes what other NPCs just said
- **DM override:** Voice commands and Discord slash commands to mute, redirect, or puppet NPCs

### Audio Mixer

Build the output serializer:
- Priority queue for NPC audio segments
- Barge-in detection (player speech interrupts NPC output)
- Configurable gap between segments (200–500ms ± jitter)

### Speculative Pre-fetch

Wire keyword extraction on STT partials to trigger parallel cold-layer queries:
- Entity name detection against L3 entity list
- Temporal reference detection ("last time", "do you remember")
- Results injected into prompt alongside hot context

## Phase 5: S2S Engines, Entity Management, and Platform Breadth ✅

**Goal:** Complete the `S2SEngine` implementations and validate the full system with real play groups.

### S2S Providers

Implement concrete S2S providers:
- **Gemini Live** (`gemini-live-2.5-flash-native-audio`) — custom WebSocket client
- **OpenAI Realtime** (`gpt-realtime-mini`) — via `go-openai-realtime`

Both must satisfy the `S2SProvider` interface and pass the same compliance tests. Verify:
- Audio forwarding and playback
- Text context injection (hot layer)
- Tool calling bridge (MCP budget enforcement)
- Session lifecycle for long sessions (context window limits, summarization triggers)

### Pre-session Entity Registration

Build the DM's entity management interface:
- Discord slash commands: `/entity add`, `/entity list`, `/entity remove`
- Campaign config file loader (YAML bulk import)
- VTT import (Foundry VTT JSON, Roll20 JSON)

### Experimental: Dual-Model Sentence Cascade

Prototype the sentence cascade with controlled A/B testing:
- Fast model (GPT-4o-mini) generates opener → TTS starts immediately
- Strong model (Claude Sonnet) continues from forced prefix
- Measure coherence, latency gain, and cost overhead
- Compare against single-model baseline and Cisco-style single-model forced prefix

This is experimental and opt-in per NPC via `cascade_mode` config.

### Closed Alpha

Recruit 3–5 DMs for real session testing. Focus feedback on:
- Voice latency and naturalness
- NPC personality consistency
- Memory accuracy (does the NPC remember correctly?)
- DM workflow (is the control interface usable?)
- Provider switching (did it break anything?)

### Second Audio Platform

Add WebRTC support via Pion to validate the `AudioPlatform` interface:
- Browser-based voice sessions without Discord
- Same pipeline, different transport — no changes above the audio layer
- If the abstraction holds cleanly, the interface is correct

## Phase 6: Production Hardening and Observability

**Goal:** Make Glyphoxa reliable enough for multi-hour sessions with real play groups. Instrument everything, harden failure modes, and establish operational baselines.

**Status:** Next up.

### Structured Observability

- **OpenTelemetry integration:** Traces for the full voice pipeline (VAD → STT → LLM → TTS → playback) with span-per-stage latency
- **Prometheus metrics:** Endpoint exposing p50/p95/p99 latency per stage, active NPC count, memory query duration, tool execution times, error rates by provider
- **Structured logging:** Replace ad-hoc `log` calls with `slog` (structured, leveled). Correlation IDs linking a single utterance through the entire pipeline
- **Health endpoint:** `/healthz` and `/readyz` for container orchestration. Include provider connectivity checks

### Graceful Degradation

- **Provider failover:** If an STT/TTS/LLM provider returns errors or exceeds latency hard limits, automatically fall back to a configured secondary (e.g., ElevenLabs → Coqui, Deepgram → whisper.cpp)
- **Circuit breakers:** Per-provider circuit breaker (closed → open → half-open). Prevent cascading failures when a single external API goes down
- **S2S → cascaded fallback:** If an S2S session fails mid-conversation, seamlessly restart the NPC on cascaded engine without losing context
- **Memory layer isolation:** L1/L2/L3 failures should degrade gracefully (NPC continues without memory rather than crashing)

### Session Lifecycle

- **Context window management:** Automatic summarization when approaching provider context limits. For S2S sessions, trigger summary + re-injection before the window fills
- **Long-session support:** 4+ hour sessions need periodic memory consolidation — flush hot context to L1, re-summarize, prune stale entries
- **Reconnection:** If the audio platform disconnects (Discord voice timeout, WebRTC ICE failure), auto-reconnect and resume NPC state

### Configuration Validation

- **Startup validation:** Fail fast with clear error messages if config references unknown providers, missing credentials, or invalid NPC definitions
- **Config hot-reload:** Watch config file for changes and apply non-destructive updates (NPC personality, tool tiers, voice settings) without restarting the session

### Resolve Open Design Items

Address the two items from [to-be-discussed.md](to-be-discussed.md):
- **#6 — OpenAI Realtime error events:** Implement `OnError(func(error))` callback on `s2s.SessionHandle` (recommended Option B)
- **#7 — WebRTC `outputCh` ownership:** Update interface docs to clarify write-only channels are caller-owned (Option C), plan `Send(frame)` + `Close()` struct for v1 (Option D)

## Phase 7: DM Experience and Closed Alpha

**Goal:** Build the DM-facing control surface and run real play sessions to validate the product.

### Discord Bot Interface

- **Slash commands:** `/npc list`, `/npc mute <name>`, `/npc unmute <name>`, `/npc speak <name> <text>` (puppet mode), `/session start`, `/session stop`, `/session recap`
- **Entity management:** `/entity add`, `/entity list`, `/entity remove`, `/entity import <file>` (YAML or VTT JSON)
- **Campaign management:** `/campaign load <file>`, `/campaign info`, `/campaign switch`
- **Session dashboard:** Embed showing active NPCs, latency stats, memory usage, and session duration

### Voice Commands

- DM voice shortcuts: "Grimjaw, be quiet" → mute, "Everyone, stop" → mute all, "Grimjaw, say..." → puppet
- Keyword detection on STT partials with low-latency response

### Companion Web UI (Stretch)

- Real-time session view: active NPCs, who's speaking, transcript, latency gauges
- NPC editor: personality, voice preview, knowledge scope, tool permissions
- Campaign/entity browser with relationship graph visualization
- This is a stretch goal — Discord-first for alpha

### Closed Alpha Program

- Recruit 3–5 DMs for real session testing (2–4 hour sessions, multiple game systems)
- Focus feedback on:
  - Voice latency and naturalness
  - NPC personality consistency across long sessions
  - Memory accuracy ("does the NPC remember correctly?")
  - DM workflow ("is the control interface usable mid-session?")
  - Provider switching reliability
- Structured feedback forms after each session
- Telemetry dashboards for latency distribution, error rates, provider usage

## Phase 8: Polish, Performance, and Public Beta

**Goal:** Optimize based on alpha feedback, expand platform support, and prepare for public release.

### Performance Optimization

- **Latency profiling:** Identify and eliminate bottlenecks from alpha telemetry. Target consistent sub-1.2s mouth-to-ear
- **Connection pooling:** Reuse provider connections across NPC turns where possible (especially STT/TTS WebSocket sessions)
- **Speculative pre-fetch tuning:** Measure hit rate of keyword-based pre-fetch from alpha data. Tune thresholds to minimize wasted queries
- **Memory query optimization:** Index tuning on PostgreSQL (GIN for FTS, HNSW for pgvector) based on real query patterns

### Multi-Platform Audio

- **WebRTC production-ready:** Browser-based voice sessions without Discord dependency
- **Platform abstraction validation:** Run identical NPC sessions on Discord and WebRTC to confirm the `AudioPlatform` interface holds
- **Additional platforms:** Evaluate Mumble, TeamSpeak, or native desktop audio as community-requested

### Game System Expansion

- **D&D 5e SRD:** Pre-indexed and bundled (OGL/CC)
- **Pathfinder 2e:** Pre-indexed (ORC license)
- **User-uploaded rulebooks:** PDF/text ingestion into the rules-lookup tool with per-campaign scoping
- **System-agnostic mode:** Dice roller and memory without system-specific rules

### Deployment and Distribution

- **Container images:** Multi-arch Docker images (amd64, arm64) published to GHCR
- **Helm chart:** Kubernetes deployment with PostgreSQL, optional GPU node for local providers
- **Single-binary self-host:** `glyphoxa serve` with embedded migrations and SQLite fallback for memory (no PostgreSQL required for small deployments)
- **Managed cloud offering:** Evaluate feasibility based on alpha usage patterns and cost data

### Documentation

- **User guide:** Getting started, configuration reference, NPC authoring guide
- **DM handbook:** Best practices for NPC design, session management, memory tuning
- **Provider comparison:** Latency/cost/quality matrix for all supported providers
- **API reference:** Generated godoc + OpenAPI spec for any HTTP endpoints

## Open Items

These remain unresolved and will be addressed through prototyping and alpha feedback:

- **Pricing model:** $5–15/month range, hybrid subscription + overage. Needs real usage data from alpha
- **Self-hosted vs. cloud:** Go's single-binary deployment makes both feasible. Open-source core + managed cloud offering is likely. Alpha will inform the split
- **Voice consistency across engine switches:** S2S ↔ cascaded voice mismatch within a single NPC session. May require voice-cloning or accepting the tradeoff
- **Game system licensing:** D&D 5e SRD (free), Pathfinder 2e (ORC). User-uploaded rulebooks need careful scoping

---

**See also:** [Overview](00-overview.md) · [Architecture](01-architecture.md) · [Providers](02-providers.md) · [Open Questions](08-open-questions.md)
