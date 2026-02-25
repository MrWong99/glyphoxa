> *This document is derived from the Glyphoxa Design Document v0.2*

# Roadmap

## Phase 1: Foundation (Weeks 1–4)

1. **Scaffold the project.** Go module monorepo with packages for `core`, `providers`, `audio`, `memory`, `mcp-host`, and `discord-transport`. Set up CI, linting, and a basic test harness.
2. **Implement [provider interfaces](02-providers.md).** Define `VoiceEngine`, `LLMProvider`, `STTProvider`, `TTSProvider`, `S2SProvider`, `AudioPlatform` Go interfaces. Implement `CascadedEngine` with one concrete provider for STT/LLM/TTS (Deepgram, OpenAI, ElevenLabs). Implement `S2SEngine` as an interface stub (concrete S2S providers in Phase 3).
3. **Build the end-to-end voice pipeline.** Discord bot joins voice → captures audio → STT → LLM (single static persona) → TTS → plays back in channel. Measure latency at every stage with Go benchmarks.
4. **Optimize for the [latency target](00-overview.md#performance-targets).** Implement full streaming pipeline with sentence-level TTS streaming. Profile with pprof. Only upgrade providers if code optimization is insufficient.

## Phase 2: Memory and NPCs (Weeks 5–8)

1. **Implement session log (L1).** Continuous transcription stored in PostgreSQL with speaker labels and timestamps.
2. **Implement [semantic index (L2)](03-memory.md#three-storage-layers).** Chunk and embed session content in pgvector. Build RAG retrieval.
3. **Implement [knowledge graph (L3)](10-knowledge-graph.md).** PostgreSQL adjacency tables (`entities` + `relationships`) with recursive CTEs. Implement `KnowledgeGraph` and `GraphRAGQuerier` Go interfaces. Wire entity extraction from session transcripts. Validate extraction accuracy.
4. **Build [hot layer assembly](03-memory.md#hot-layer-always-available-npc-context).** NPC identity snapshot + recent transcript + scene context. Measure assembly latency (target: < 50ms).
5. **Implement [speculative pre-fetch](03-memory.md#speculative-pre-fetch-bridging-the-gap).** Keyword extraction on STT partials. Parallel cold-layer queries. Measure hit rate.
6. **Build [NPC agent schema and orchestrator](06-npc-agents.md).** Declarative NPC definitions in YAML. Multi-NPC turn-taking. DM control commands.

## Phase 3: MCP and Tools (Weeks 9–12)

1. **Implement [MCP host](04-mcp-tools.md).** Using `modelcontextprotocol/go-sdk`. Tool discovery, registry, and execution wired into LLM tool calling.
2. **Implement cold-layer memory as MCP tools.** `memory.search_sessions`, `memory.query_entities`, `memory.get_summary`, `memory.search_facts`.
3. **Build [core tool servers](04-mcp-tools.md#built-in-tool-servers).** Dice roller, rules lookup (D&D 5e SRD), file I/O. Ship as bundled Go MCP servers.
4. **Implement [orchestrator-enforced tool budgets](04-mcp-tools.md#orchestrator-enforced-budget-tiers).** Implement calibration protocol. Strip over-budget tools from function definitions per budget tier (FAST/STANDARD/DEEP). No prompt-based budget compliance — the LLM never sees over-budget tools.
5. **Implement [S2S providers](02-providers.md#s2s-provider-interface).** Concrete `GeminiLiveProvider` and `OpenAIRealtimeProvider` implementing `S2SProvider`. Wire MCP tool bridge through S2S native function calling. Test tool execution flow with both providers.
6. **DM control interface.** Discord slash commands + prototype web dashboard for NPC management and memory browsing.

## Phase 4: Experimental Features and Alpha (Weeks 13–16)

1. **Prototype [dual-model sentence cascade](05-sentence-cascade.md).** Implement with GPT-4o-mini + Claude Sonnet. Measure coherence, latency gains, and cost overhead. Determine when it outperforms single-model.
2. **Closed alpha with real play groups.** Recruit 3–5 DMs for real session testing. Gather feedback on latency, NPC quality, memory accuracy, and DM workflow.
3. **Add second audio platform target.** WebRTC-based browser sessions via Pion. Validates the [platform abstraction](02-providers.md#audio-platform-interface).
4. **Iterate based on alpha feedback.** Fix top pain points. Calibrate tool budgets with real usage data. Finalize pricing model.
5. **Public beta launch.** Launch on Product Hunt, r/dnd, r/FoundryVTT, and TTRPG Discord servers.

---

**See also:** [Overview](00-overview.md) · [Open Questions](08-open-questions.md)
