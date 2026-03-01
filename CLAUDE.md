# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Glyphoxa is an AI-powered voice NPC framework for tabletop RPGs, written in Go 1.26+ (CGo required). It brings AI-driven NPCs to life in live voice chat sessions with distinct voices, personalities, and persistent memory. Platform-agnostic (Discord, WebRTC) and provider-independent (any LLM/STT/TTS can be swapped via config).

## Build & Test Commands

```bash
make build           # go build -o bin/glyphoxa ./cmd/glyphoxa
make test            # go test -race -count=1 ./...
make test-v          # verbose test output
make test-cover      # coverage report
make lint            # golangci-lint run ./...
make vet             # go vet ./...
make fmt             # gofmt -l -w .
make check           # fmt + vet + test (full pre-commit)
```

Run a single test:
```bash
go test -race -count=1 -run TestFunctionName ./path/to/package/...
```

### Prerequisites

- **libopus**: `apt install libopus-dev` / `pacman -S opus` / `brew install opus`
- **ONNX Runtime**: shared library from onnxruntime releases (for Silero VAD)
- **whisper.cpp** (optional): `make whisper-libs` then export `C_INCLUDE_PATH`, `LIBRARY_PATH`, `CGO_ENABLED=1`

## Architecture

### Package Layout

- **`cmd/glyphoxa/`** — Entry point. Parses config, registers provider factories, wires `app.New()`, handles graceful shutdown (SIGINT/SIGTERM).
- **`internal/`** — Application-private packages (not importable externally).
- **`pkg/`** — Public API packages (external code may import).

### Core Data Flow

Player speaks → **VAD** (voice activity detection) → **STT** (speech-to-text) → **Hot Context Assembly** (<50ms: NPC identity + transcript + scene) → **LLM** (with optional MCP tool calls) → **TTS** (sentence-by-sentence streaming) → **Mixer** (priority queue, barge-in) → **Transport** (Discord/WebRTC). Transcript correction and knowledge graph updates happen asynchronously.

**Latency budget**: <1.2s mouth-to-ear target, 2.0s hard limit. End-to-end streaming via pipelined Go channels.

### Key Subsystems

- **`internal/app/`** — Top-level wiring and lifecycle. `app.New()` initializes all subsystems; `Run()` is the main loop; `Shutdown()` does graceful teardown. Uses functional options for DI in tests.
- **`internal/engine/`** — `VoiceEngine` interface. Implementations: `cascade/` (STT→LLM→TTS pipeline), `s2s/` (speech-to-speech via Gemini Live / OpenAI Realtime).
- **`internal/agent/`** — `NPCAgent` and `Router` interfaces. `orchestrator/` coordinates multi-NPC scenes. `npcstore/` is PostgreSQL-backed NPC definitions.
- **`internal/config/`** — YAML config loader with provider registry and hot-reload support.
- **`internal/hotctx/`** — Concurrent assembly of NPC identity, recent transcript, and scene context in <50ms.
- **`internal/mcp/`** — MCP host and tool registry with budget tiers (instant/fast/standard).
- **`pkg/audio/`** — `Platform`/`Connection` interfaces. Adapters: `discord/`, `webrtc/`. `mixer/` handles priority queue with barge-in detection.
- **`pkg/memory/`** — 3-layer memory: L1 (session log), L2 (semantic/vector via pgvector), L3 (knowledge graph with recursive CTEs). Backend: PostgreSQL + pgvector.
- **`pkg/provider/`** — Provider interfaces and implementations for `llm/`, `stt/`, `tts/`, `s2s/`, `vad/`, `embeddings/`. Each has a `mock/` subdirectory.

### Key Design Principle

Every external dependency sits behind a Go interface. Swapping providers is a config change, not a rewrite. Provider registry pattern: config names the provider, registry instantiates the concrete implementation.

## Code Conventions

### Go Style

- **gofmt** enforced in CI. Linter: golangci-lint with goimports.
- **Error wrapping**: `%w` with package prefix (e.g., `"agent: open session: %w"`).
- **Godoc**: all exported symbols need doc comments.
- **No naked returns**.

### Concurrency

- All public methods must be safe for concurrent use (race detector always on).
- Prefer `sync.Mutex` over channels for shared state.
- Never hold locks during blocking I/O.
- Use `container/heap` for priority queues, `slices.SortFunc` over `sort.Slice`.

### Testing

- **`t.Parallel()`** on all tests and subtests — mandatory.
- **Table-driven tests** with `t.Run` for coverage.
- **Compile-time interface assertions**: `var _ Interface = (*Impl)(nil)` at top of implementation files.
- **Mocks** in `<package>/mock/` subdirectories (hand-written, not generated).
- Race detector always enabled: `-race -count=1`.

### Branch Naming

`feat/`, `fix/`, `docs/`, `refactor/` prefixes followed by short description.
