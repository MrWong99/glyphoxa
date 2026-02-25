> *This document is derived from the Glyphoxa Design Document v0.2*

# Technology Decisions

## Why Go

Go was chosen as the primary language after evaluating TypeScript, Rust, Go, and Java against the project's core requirements: streaming concurrency, latency sensitivity, and ecosystem maturity.

| Requirement | Go's Advantage |
|---|---|
| Streaming concurrency | Goroutines (~2KB each) + channels are purpose-built for this workload. Each NPC agent is a goroutine. Each audio stream is a channel. Pipeline stages connect via channels. No callback hell, no async/await coloring. |
| Compiled performance | Native compilation, no JIT warmup, no GC pause concern for I/O-bound streaming. Opus encoding/decoding has native Go bindings (hraban/opus). |
| Ecosystem maturity | Official MCP Go SDK (`modelcontextprotocol/go-sdk`). `discordgo` with full voice support. `any-llm-go` (Mozilla, channel-based streaming over official provider SDKs). `coder/websocket` for Deepgram/ElevenLabs streaming clients. |
| Security | Go modules with `go.sum` for cryptographic dependency verification. Standard library covers HTTP, WebSocket, crypto, JSON, audio without external deps. Fraction of the dependency tree compared to npm. |
| Deployment simplicity | Single binary with two native shared libraries (libopus, ONNX Runtime). Docker images stay small via multi-stage builds. Cross-compilation requires a C toolchain for the target platform due to CGo. |

**Escape hatch:** If profiling reveals that audio mixing of multiple simultaneous NPC outputs becomes a CPU bottleneck, a Rust native addon for the audio mixer can be integrated via CGO. This is unlikely to be needed but available.

## Go Dependency Stack

Primary library choices with fallbacks. Every external service sits behind a Go interface ([Provider Abstraction](02-providers.md)), so swapping a library is a single-struct change.

| Component | Primary | Fallback | Pure Go? |
|---|---|---|---|
| LLM (multi-provider) | `mozilla-ai/any-llm-go` | `openai/openai-go`, `anthropics/anthropic-sdk-go` directly | Yes |
| MCP | `modelcontextprotocol/go-sdk` (official, v1.0.0) | `mark3labs/mcp-go` (8k+ stars, HTTP transport) | Yes |
| Discord | `bwmarrin/discordgo` | — | Yes |
| WebSocket | `coder/websocket` | `gorilla/websocket` | Yes |
| PostgreSQL | `jackc/pgx` v5 + `pgxpool` | `lib/pq` via database/sql | Yes |
| pgvector | `pgvector/pgvector-go` + `pgxvec` | — | Yes |
| Opus codec | `hraban/opus` v2 | — | **No** — CGo, requires libopus |
| VAD (Silero) | `streamer45/silero-vad-go` | `plandem/silero-go` | **No** — CGo, requires ONNX Runtime |
| OpenAI Realtime | `WqyJh/go-openai-realtime` | Custom WS client | Yes |
| Gemini Live | Custom WS client (`coder/websocket`) | — | Yes |
| ONNX Runtime | `yalue/onnxruntime_go` | — | **No** — CGo wrapper |
| Phonetic matching | `antzucaro/matchr` | — | Yes |

### CGo and Native Dependencies

Most of the stack is pure Go, but two components require CGo and system-level native libraries:

| Native Library | Required By | System Package | Notes |
|---|---|---|---|
| **libopus** | `hraban/opus` (Opus encode/decode) | `libopus-dev` (Debian/Ubuntu), `opus` (Arch/macOS Homebrew) | Essential for Discord voice. No viable pure-Go Opus encoder exists (`pion/opus` is decode-only). |
| **ONNX Runtime** | `streamer45/silero-vad-go` via `yalue/onnxruntime_go` | Shared library (`.so`/`.dylib`/`.dll`) from [onnxruntime releases](https://github.com/microsoft/onnxruntime/releases) | Required for Silero VAD inference. Must ship alongside the binary or install system-wide. Available for Linux/macOS/Windows on x86_64 and ARM64. |

**Build implications:** `CGO_ENABLED=1` is required. Cross-compilation needs a C toolchain for the target platform. Docker builds should use a multi-stage Dockerfile with the native libraries installed in the build stage and the shared libraries copied to the runtime image.

**Why accept CGo:** Opus encoding is non-negotiable for Discord voice — there is no pure-Go alternative. Silero VAD is the only viable local VAD with language-agnostic, sub-millisecond inference. The CGo cost is limited to two well-isolated components (audio codec and voice detection) while the rest of the stack remains pure Go.

## Default Provider Stack

| Component | Default Provider | Fallback | Local/Free Option |
|---|---|---|---|
| STT | Deepgram Nova-3 (streaming) | AssemblyAI Universal-2 | whisper.cpp via Go bindings |
| LLM (fast) | GPT-4o-mini (streaming) | Gemini 2.5 Flash | Ollama + Llama 3.x |
| LLM (strong) | Claude Sonnet | GPT-4o | Ollama + Llama 3.1 70B |
| TTS | ElevenLabs Flash v2.5 | Cartesia Sonic | Coqui XTTS (local) |
| S2S | Gemini Live (`gemini-live-2.5-flash-native-audio`) | OpenAI Realtime (`gpt-realtime-mini`) | — |
| Embeddings | OpenAI text-embedding-3-small | Voyage AI | nomic-embed-text (local) |
| Audio Platform | Discord (discordgo) | — | WebRTC (custom, Pion) |

Gemini Live is the recommended S2S default: 128k context window (vs OpenAI's 32k), 24-hour session resumption, lower cost (~$3/$12 per 1M audio tokens vs $10/$20 for OpenAI mini), and a free tier for development.

## Latency Budget Breakdown

| Stage | Budget (ms) | Technique |
|---|---|---|
| VAD + silence detection | 50–100 | Local Silero VAD. No network hop. |
| STT (streaming final) | 200–300 | Deepgram streaming. Transcript ready ~200ms after speech ends. |
| Speculative pre-fetch (parallel) | 0 (overlapped) | Start vector search + graph query as STT partials arrive. |
| Hot context assembly | 30–50 | In-memory graph traversal + recent transcript slice. |
| LLM time-to-first-token | 300–500 | GPT-4o-mini or Haiku streaming. |
| TTS time-to-first-byte | 75–150 | ElevenLabs Flash streaming. |
| Audio transport overhead | 20–50 | Opus encoding + Discord playback. |
| **Total (pipelined)** | **650–1100** | Pipelining overlaps STT tail with memory pre-fetch, LLM streaming with TTS streaming. |

### S2S Latency Comparison

| Engine | Latency (first audio) | Notes |
|---|---|---|
| Cascaded (pipelined) | 650–1100ms | Full pipeline, maximum flexibility |
| OpenAI Realtime (full) | 200–500ms | Single API, limited voices |
| OpenAI Realtime (mini) | 150–400ms | Cheaper, slightly lower quality |
| Gemini Live (flash) | 300–600ms | 128k context, session resumption |

S2S engines achieve lower latency by eliminating inter-stage overhead, but trade off voice variety and fine-grained control. See [Providers](02-providers.md) for the VoiceEngine interface and architectural trade-offs.

## Knowledge Graph Stack

All three memory layers (L1 session log, L2 semantic index, L3 knowledge graph) run on a single PostgreSQL instance. L3 uses adjacency tables (`entities` + `relationships` with JSONB attributes) and recursive CTEs for multi-hop traversal. This avoids a second database engine and enables GraphRAG queries that combine graph traversal (L3) with vector similarity search (L2/pgvector) in a single SQL round-trip.

Self-hosted deployments use PostgreSQL via Docker Compose — no SQLite path. See [Knowledge Graph](10-knowledge-graph.md) for the full schema, Go interfaces, and query patterns.

| Concern | Approach |
|---|---|
| Graph storage | PostgreSQL adjacency tables (two tables: `entities`, `relationships`) |
| Flexible attributes | JSONB columns on both tables |
| Multi-hop traversal | Recursive CTEs (`WITH RECURSIVE`) |
| Path finding | Recursive CTEs with depth tracking |
| GraphRAG | Single query combining CTE graph scope + pgvector similarity |
| Go abstraction | `KnowledgeGraph` interface (base) + optional `GraphRAGQuerier` interface |
| Migration path | PostgreSQL → Ent (code-gen ORM, same DB) → Neo4j (external, only if needed) |

---

**See also:** [Architecture](01-architecture.md) · [Providers](02-providers.md) · [Overview: Performance Targets](00-overview.md#performance-targets) · [Knowledge Graph](10-knowledge-graph.md)
