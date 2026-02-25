> *This document is derived from the Glyphoxa Design Document v0.2*

# Overview: Vision and Goals

Glyphoxa is a real-time voice AI framework that brings AI-driven talking personas into live voice chat sessions. Its primary use case is tabletop role-playing games (TTRPGs), where it serves as a persistent AI co-pilot for the Dungeon Master — voicing NPCs, documenting sessions, and answering questions — without ever replacing the human storyteller.

## Product Principles

**Augment, never replace.** The DM remains the storyteller. Glyphoxa voices NPCs, transcribes sessions, and answers rules questions on the DM's behalf.

**Voice-first, text-compatible.** The primary interface is spoken conversation in a live voice channel. Text fallback exists but voice is the default.

**Provider-agnostic by design.** Every external dependency — LLM, STT, TTS, audio platform — sits behind an abstraction layer. Swapping ElevenLabs for a local Coqui instance or OpenAI for Anthropic is a config change, not a rewrite.

**Memory is sacred.** Every session is transcribed, indexed, and queryable. NPCs remember. The world has continuity.

**Extensible via MCP.** Agents use tools — image generation, web search, dice rolling, memory retrieval — through a plug-and-play Model Context Protocol layer. Tools are self-describing, independently deployable, and performance-budgeted.

**Performance is non-negotiable.** Written in Go for native concurrency, compiled speed, and minimal runtime overhead. Every architectural decision is evaluated against the latency budget first.

## Core Capabilities

| Capability | Description | Priority |
|---|---|---|
| Voice NPC Personas | AI-controlled NPCs with distinct voices, personalities, and backstories that speak in real-time during voice chat sessions | P0 |
| Live Session Transcription | Continuous STT of the entire voice channel, identifying speakers, for session logging and future lookup | P0 |
| Hybrid Semantic Memory | Hot layer (always-injected NPC identity + recent context) plus cold layer (on-demand deep history via MCP tools). Cross-session knowledge graph. | P0 |
| LLM Question Answering | Answer rules questions, lore lookups, and general queries mid-session using LLM + RAG over session history and rulebooks | P1 |
| MCP Tool Integration | Plug-and-play tools with declared latency budgets. Image gen, dice, web search, file I/O, and custom extensions via MCP. | P1 |
| Multi-NPC Conversations | Multiple NPCs in the same group conversation with turn-taking and distinct voice profiles | P2 |

## Performance Targets

**Hard Constraint: 1–2 Second Round-Trip Latency**

From the moment a player finishes speaking to the moment the NPC's voice begins playing back, the total latency must not exceed 2 seconds. This budget covers STT processing, LLM inference, TTS generation, and audio streaming. Code-level optimization is always the first lever — faster providers are the fallback, not the default.

| Metric | Target | Hard Limit |
|---|---|---|
| Mouth-to-ear latency | < 1.2 seconds | 2.0 seconds |
| STT time-to-first-token | < 300ms | 500ms |
| LLM time-to-first-token | < 400ms | 800ms |
| TTS time-to-first-byte | < 200ms | 500ms |
| Concurrent NPC voices | ≥ 3 | ≥ 1 |
| Session transcript accuracy | > 92% WER | > 85% WER |
| Entity extraction F1 (known entities) | > 85% | > 75% |
| Hot memory assembly | < 50ms | < 150ms |
| Cold memory query (MCP) | < 300ms | < 800ms |

---

**See also:** [Architecture](01-architecture.md) · [Providers](02-providers.md) · [Memory](03-memory.md) · [Technology](07-technology.md)
