![Glyphoxa](assets/banner-logo.png)

# 🐉 Glyphoxa

[![Go Version](https://img.shields.io/badge/Go-1.23+-00ADD8?style=flat&logo=go)](https://go.dev)
[![License: GPL v3](https://img.shields.io/badge/License-GPLv3-blue.svg)](LICENSE)

**AI-Powered Voice NPCs for Tabletop RPGs** — a platform-agnostic, provider-independent voice AI framework that brings your NPCs to life.

---

## What is Glyphoxa?

Glyphoxa is a real-time voice AI framework that brings AI-driven talking personas into live voice chat sessions. Built for tabletop RPGs, it serves as a persistent AI co-pilot for the Dungeon Master — voicing NPCs with distinct personalities, transcribing sessions, and answering rules questions — without ever replacing the human storyteller. Written in Go for native concurrency and sub-2-second mouth-to-ear latency.

## ✨ Feature Highlights

- 🗣️ **Voice NPC Personas** — AI-controlled NPCs with distinct voices, personalities, and backstories that speak in real-time
- 🧠 **Hybrid Memory System** — NPCs remember. Hot layer for instant context, cold layer for deep history, knowledge graph for world state
- 🔧 **MCP Tool Integration** — Plug-and-play tools (dice, rules lookup, image gen, web search) with performance-budgeted execution
- 🔄 **Provider-Agnostic** — Swap LLM, STT, TTS, or audio platform with a config change, not a rewrite
- ⚡ **Sub-2s Latency** — End-to-end streaming pipeline with speculative pre-fetch and sentence-level TTS
- 🎭 **Multi-NPC Conversations** — Multiple NPCs with turn-taking and distinct voice profiles in the same scene
- 📜 **Live Session Transcription** — Continuous STT with speaker identification for session logging and future lookup

## 🏗️ Architecture Overview

```
┌──────────────────────────────────────────────────────────┐
│                    Audio Transport                       │
│              (Discord / WebRTC / Custom)                 │
├────────────────────┬─────────────────────────────────────┤
│   Audio In (VAD)   │          Audio Out (Opus)           │
├────────────────────┴──────────────────────────────-──────┤
│                   Speech Pipeline                        │
│         STT Provider ◄──────► TTS Provider              │
├──────────────────────────────────────────────────────────┤
│                 Agent Orchestrator                       │
│    ┌─────────┐  ┌─────────┐  ┌─────────┐                 │
│    │ NPC #1  │  │ NPC #2  │  │ NPC #3  │  ...            │
│    └────┬────┘  └────┬────┘  └────┬────┘                 │
├─────────┴────────────┴────────────┴──────────────────────┤
│                    LLM Core                              │
│          (Streaming completions + tool calls)            │
├──────────────────────────────────────────────────────────┤
│   Memory Subsystem          │    MCP Tool Execution      │
│  ┌─────┐ ┌─────┐ ┌─────┐  │  ┌──────┐ ┌──────┐           │
│  │ L1  │ │ L2  │ │ L3  │  │  │ Dice │ │Rules │ ...       │
│  │ Log │ │ Vec │ │Graph│  │  └──────┘ └──────┘           │
│  └─────┘ └─────┘ └─────┘  │                              │
└──────────────────────────────────────────────────────────┘
```

## 🚀 Quick Start

> **⚠️ Glyphoxa is in early development. This section will be updated as the project matures.**

**Prerequisites:** Go 1.26+, CGo-enabled C toolchain, and two native libraries:
- **libopus** — `apt install libopus-dev` (Debian/Ubuntu) or `pacman -S opus` (Arch) or `brew install opus` (macOS)
- **ONNX Runtime** — shared library from [onnxruntime releases](https://github.com/microsoft/onnxruntime/releases) (required for Silero VAD)

```bash
# Clone the repository
git clone https://github.com/MrWong99/glyphoxa.git
cd glyphoxa

# Build (requires CGO_ENABLED=1)
go build ./cmd/glyphoxa

# Run with config
./glyphoxa --config config.yaml
```

## 🔌 Provider Support

| Component | Default | Fallback | Local/Free |
|-----------|---------|----------|------------|
| **STT** | Deepgram Nova-3 | AssemblyAI Universal-2 | whisper.cpp |
| **LLM (fast)** | GPT-4o-mini | Gemini 2.5 Flash | Ollama + Llama 3.x |
| **LLM (strong)** | Claude Sonnet | GPT-4o | Ollama + Llama 3.1 70B |
| **TTS** | ElevenLabs Flash v2.5 | Cartesia Sonic | Coqui XTTS |
| **S2S** | Gemini Live (Flash native audio) | OpenAI Realtime (gpt-4o-mini) | — |
| **Embeddings** | OpenAI text-embedding-3-small | Voyage AI | nomic-embed-text |
| **Audio** | Discord (discordgo) | — | WebRTC (Pion) |

## ⚡ Performance Targets

| Metric | Target | Hard Limit |
|--------|--------|------------|
| Mouth-to-ear latency | < 1.2s | 2.0s |
| STT time-to-first-token | < 300ms | 500ms |
| LLM time-to-first-token | < 400ms | 800ms |
| TTS time-to-first-byte | < 200ms | 500ms |
| Concurrent NPC voices | ≥ 3 | ≥ 1 |
| Hot memory assembly | < 50ms | < 150ms |

## 🔩 Tech Stack

| Component | Library | Notes |
|-----------|---------|-------|
| LLM (multi-provider) | `mozilla-ai/any-llm-go` | Unified interface over OpenAI, Anthropic, Gemini, Ollama |
| MCP | `modelcontextprotocol/go-sdk` | Official Go SDK, v1.0.0 |
| Discord | `bwmarrin/discordgo` | Full voice support with per-user Opus streams |
| WebSocket | `coder/websocket` | Streaming clients for Deepgram, ElevenLabs, Gemini Live |
| PostgreSQL | `jackc/pgx` v5 + `pgxpool` | Binary protocol, connection pooling |
| pgvector | `pgvector/pgvector-go` | L2 semantic index |
| Opus codec | `hraban/opus` v2 | **CGo** — requires libopus |
| VAD (Silero) | `streamer45/silero-vad-go` | **CGo** — requires ONNX Runtime |
| OpenAI Realtime | `WqyJh/go-openai-realtime` | S2S provider |

## 📚 Design Documents

Detailed design documentation lives in [`docs/design/`](docs/design/):

| Document | Description |
|----------|-------------|
| [00 — Overview](docs/design/00-overview.md) | Vision, goals, product principles |
| [01 — Architecture](docs/design/01-architecture.md) | System layers and data flow |
| [02 — Providers](docs/design/02-providers.md) | LLM, STT, TTS, Audio platform interfaces |
| [03 — Memory](docs/design/03-memory.md) | Hybrid memory system and knowledge graph |
| [04 — MCP Tools](docs/design/04-mcp-tools.md) | Tool integration and performance budgets |
| [05 — Sentence Cascade](docs/design/05-sentence-cascade.md) | ⚠️ Dual-model sentence cascade (experimental) |
| [06 — NPC Agents](docs/design/06-npc-agents.md) | NPC agent design and multi-NPC orchestration |
| [07 — Technology](docs/design/07-technology.md) | Technology decisions and latency budget |
| [08 — Open Questions](docs/design/08-open-questions.md) | Unresolved design questions |
| [09 — Roadmap](docs/design/09-roadmap.md) | Development phases and next steps |
| [10 — Knowledge Graph](docs/design/10-knowledge-graph.md) | L3 graph schema, Go interfaces, query patterns |

## 🤝 Contributing

> **Contributing guidelines coming soon.** Glyphoxa is in early development. If you're interested in contributing, watch this repo for updates or open an issue to start a conversation.

## 📄 License

[GPL v3](LICENSE) © Glyphoxa Contributors
