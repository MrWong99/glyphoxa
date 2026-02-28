---
title: "refactor: Comprehensive Codebase Improvements"
type: refactor
status: completed
date: 2026-02-28
---

# Comprehensive Codebase Improvements

## Overview

A systematic improvement pass across the entire Glyphoxa codebase targeting four areas: **bug fixes** (race conditions, resource leaks), **performance optimizations** (hot-path allocations, query efficiency), **architectural refinements** (interface design, DRY violations, separation of concerns), and **code quality** (dead code removal, naming consistency, test reliability). All changes preserve existing functionality and public API compatibility.

## Problem Statement

Deep analysis of the codebase revealed 30+ improvement opportunities across all layers. Several are latent correctness issues (data races in `FallbackGroup`, potential channel panics in cascade engine `Close()`, deadlock risk in config watcher). Others are performance drags on the sub-2s latency target (quadratic string scanning in sentence detection, per-utterance sorting, missing composite DB indexes). The remainder are maintainability concerns (duplicated provider lists, hardcoded config values, dead code).

## Technical Approach

### Architecture

Changes are organized into four independent phases that can be implemented in parallel or sequentially. Each phase targets a different concern:

1. **Phase 1: Critical Bug Fixes** -- correctness issues that could cause panics, data races, or deadlocks
2. **Phase 2: Performance Optimizations** -- hot-path improvements targeting the sub-2s latency budget
3. **Phase 3: Architectural Refinements** -- interface design, DRY, separation of concerns
4. **Phase 4: Code Quality & Cleanup** -- dead code, naming, test reliability, documentation gaps

### Implementation Phases

---

#### Phase 1: Critical Bug Fixes

**Goal:** Eliminate all identified correctness hazards. These are the highest-priority changes.

##### 1.1 Cascade engine `Close()` must wait for background goroutines

- **File:** `internal/engine/cascade/cascade.go:262-272`
- **Problem:** `Close()` closes `transcriptCh` without calling `e.wg.Wait()`. If a background `Process` goroutine is still running (strong model stage, line 194), it can write to the closed channel, causing a panic.
- **Fix:** Call `e.wg.Wait()` before closing `transcriptCh` in `Close()`.
- **Test:** Add a test that calls `Process()` then immediately `Close()` concurrently to verify no panic.

##### 1.2 `FallbackGroup.AddFallback()` data race

- **File:** `internal/resilience/fallback.go:55-63`
- **Problem:** `AddFallback()` appends to `entries` without synchronization while `Execute()`/`ExecuteWithResult()` iterate the same slice. The doc comment claims "safe for concurrent use" which is incorrect.
- **Fix:** Either add a `sync.RWMutex` (write lock on `AddFallback`, read lock on `Execute`), or change the doc comment to require all `AddFallback` calls before first `Execute`, and add a `sealed` flag that panics if violated.
- **Test:** Existing tests should pass; add a test that calls `AddFallback` and `Execute` concurrently under `-race`.

##### 1.3 Config watcher `onChange` callback called under lock (deadlock risk)

- **File:** `internal/config/watcher.go:122-141`
- **Problem:** `check()` calls `w.onChange(old, cfg)` while holding `w.mu.Lock()`. If the callback calls `w.Current()`, deadlock.
- **Fix:** Copy state under lock, release lock, then call callback:

```go
w.mu.Lock()
old := w.current
w.current = cfg
w.lastHash = hash
w.lastMtime = newMtime
w.mu.Unlock()
slog.Info("config watcher: configuration reloaded", "path", w.path)
if w.onChange != nil {
    w.onChange(old, cfg)
}
```

- **Test:** Add test where `onChange` calls `Current()` to verify no deadlock.

##### 1.4 `ContextManager.summariseOldest` stale-index after unlock/relock

- **File:** `internal/session/context_manager.go:130-163`
- **Problem:** Temporarily releases mutex for LLM call, re-acquires, then removes `messages[:half]` using a `half` computed before unlock. Concurrent `AddMessages` can invalidate the index.
- **Fix:** Snapshot messages before unlocking; use snapshot length for post-summarisation bounds check.
- **Test:** Add concurrent `AddMessages` during mock summarisation to verify correctness.

##### 1.5 `npc.go` tool handler uses `context.Background()` instead of session context

- **File:** `internal/agent/npc.go:129`
- **Problem:** Tool execution via MCP ignores session lifecycle. If a session is stopped, tool calls continue with an uncancellable context.
- **Fix:** Capture the `Process` method's context in the closure or pass it through the `OnToolCall` callback.
- **Test:** Verify that cancelling the process context also cancels in-flight tool calls.

##### 1.6 Reconnector does not disconnect old connection on reconnect

- **File:** `internal/session/reconnect.go:186-201`
- **Problem:** `attemptReconnect` replaces `r.conn` without calling `Disconnect()` on the old connection, leaking resources.
- **Fix:** Call `oldConn.Disconnect()` before replacing.

---

#### Phase 2: Performance Optimizations

**Goal:** Reduce hot-path latency and memory pressure. Target: measurable improvement in STT-to-TTS pipeline timing.

##### 2.1 Pre-sort address detector candidates (eliminate per-utterance sort)

- **File:** `internal/agent/orchestrator/address.go:123-150`
- **Problem:** Every utterance triggers O(n log n) sort + allocation of `[]candidate`. With 10+ NPCs (30-50 name index entries), this adds unnecessary CPU in the hot path.
- **Fix:** Pre-sort candidates in `buildIndex()`/`Rebuild()` and store as `sorted []candidate` on the struct. `matchName` just iterates the pre-sorted slice (zero alloc).
- **Expected gain:** Eliminates ~1 allocation and O(n log n) sort per utterance.

##### 2.2 Fix quadratic string scanning in cascade sentence boundary detection

- **File:** `internal/engine/cascade/cascade.go:399-413`
- **Problem:** `forwardSentences` calls `buf.String()` 3 times per loop iteration (3 allocations). `firstSentenceBoundary` re-scans from index 0 every time. O(n^2) over the response.
- **Fix:** Call `buf.String()` once per iteration, reuse the result. Consider tracking last-checked index.
- **Expected gain:** ~3x fewer string allocations per sentence boundary check.

##### 2.3 Add composite database index for hot-context assembly

- **File:** `pkg/memory/postgres/schema.go:50-54`
- **Problem:** `GetRecent` filters on `(session_id, timestamp)` but only has separate indexes. Forces bitmap AND.
- **Fix:** Add `CREATE INDEX IF NOT EXISTS idx_session_entries_session_timestamp ON session_entries (session_id, timestamp)`.
- **Expected gain:** `GetRecent` from ~5-15ms to ~1-3ms for sessions with 1000+ entries. Direct impact on hot-context assembly (target <50ms).

##### 2.4 Batch quest entity fetches in hot context assembler

- **File:** `internal/hotctx/assembler.go:206-218`
- **Problem:** N+1 pattern -- separate `GetEntity` call per quest target ID.
- **Fix:** Add a batch `FindEntities(ctx, filter)` method or use the existing `fetchEntitiesIn` internally. Single query instead of N.
- **Expected gain:** Saves (N-1) * 2-5ms per NPC per utterance for NPCs with 3-4 quests.

##### 2.5 UtteranceBuffer memory leak over long sessions

- **File:** `internal/agent/orchestrator/utterance_buffer.go:105-121`
- **Problem:** `evict()` reslices `entries` but never reallocates the backing array. Evicted entries stay in memory for the session lifetime.
- **Fix:** Copy surviving entries to a fresh slice in `evict()`.

##### 2.6 Reuse timer in mixer dispatch loop

- **File:** `pkg/audio/mixer/mixer.go:261`
- **Problem:** `time.After(gapDur)` allocates a new `time.Timer` per segment in the audio hot path.
- **Fix:** Use a reusable `time.NewTimer` with `Reset()`.

##### 2.7 Pool WAV encoding buffer in Whisper provider

- **File:** `pkg/provider/stt/whisper/whisper.go:379-440`
- **Problem:** 320KB+ allocation per flush (WAV buffer + multipart writer).
- **Fix:** Use a pre-allocated buffer in the session struct (single-threaded `processLoop`) or `sync.Pool`.

##### 2.8 Stream WAV parsing in Coqui TTS

- **File:** `pkg/provider/tts/coqui/coqui.go:348-429`
- **Problem:** `io.ReadAll` reads entire WAV response into memory before chunking.
- **Fix:** Read 44-byte header, then stream data directly in `pcmChunkSize` chunks. Reduces peak memory from O(response_size) to O(4KB).

---

#### Phase 3: Architectural Refinements

**Goal:** Improve maintainability, reduce coupling, eliminate DRY violations.

##### 3.1 Unify duplicated provider name lists

- **Files:** `cmd/glyphoxa/main.go:150-156` and `internal/config/loader.go:17-25`
- **Problem:** Identical provider name maps maintained separately. Adding a provider requires updating both.
- **Fix:** Make `config.ValidProviderNames` the single source of truth. Remove the duplicate from `main.go` or derive it from registry state.

##### 3.2 Extract `buildProvider` generic helper in main.go

- **File:** `cmd/glyphoxa/main.go:306-394`
- **Problem:** Seven identical copy-pasted provider creation blocks.
- **Fix:** Create a generic `buildProvider[T]` function.

##### 3.3 Abstract orchestrator interface for Discord commands

- **File:** `internal/discord/commands/npc.go:12,19`
- **Problem:** `NPCCommands` depends on concrete `*orchestrator.Orchestrator` rather than an interface.
- **Fix:** Extend `agent.Router` or create `agent.Manager` interface with `AgentByName`, `IsMuted`, `MuteAll`, `UnmuteAll`. Update `NPCCommands` to depend on the interface.

##### 3.4 Move shared types out of cross-dependent provider packages

- **File:** `pkg/provider/s2s/provider.go:19-22`
- **Problem:** `s2s` package imports `tts.VoiceProfile` and `llm.ToolDefinition`, creating coupling between pipeline alternatives.
- **Fix:** Extract `VoiceProfile` and `ToolDefinition` to a shared `pkg/provider/types` package.

##### 3.5 Make audio/STT configuration data-driven

- **File:** `internal/app/app.go:412-432`
- **Problem:** Sample rate (48000), language ("en-US"), VAD thresholds are hardcoded.
- **Fix:** Add these to the config schema under `server` or `audio` section. Non-English deployments currently require code changes.

##### 3.6 Extract duplicate session ID construction

- **File:** `internal/app/app.go:261,514-517`
- **Problem:** Same `"session-" + a.cfg.Campaign.Name` logic duplicated.
- **Fix:** Extract to `(a *App) sessionID() string` method.

##### 3.7 Move `mcp.Transport` type to config or shared types

- **File:** `internal/config/config.go:5`
- **Problem:** Config package imports `internal/mcp` for a single string type, inverting expected dependency direction.
- **Fix:** Move `Transport` type to config package or a shared types package.

##### 3.8 Add optional `io.Closer` support for providers

- **Files:** `pkg/provider/*/provider.go`
- **Problem:** Provider interfaces have no `Close()` method. Providers with persistent resources (connection pools, goroutines) cannot be cleaned up at shutdown.
- **Fix:** In `app.Shutdown`, iterate providers and close those implementing `io.Closer`.

##### 3.9 Config diff should warn on non-hot-reloadable changes

- **File:** `internal/config/diff.go:76-92`
- **Problem:** Changes to `Engine`, `Tools`, `KnowledgeScope`, `CascadeConfig` are silently ignored.
- **Fix:** Detect these changes and log a warning ("engine change for NPC X requires restart").

---

#### Phase 4: Code Quality & Cleanup

**Goal:** Remove dead code, fix naming inconsistencies, improve test reliability.

##### 4.1 Remove dead code

- **`pkg/provider/tts/elevenlabs/elevenlabs.go:296`**: Remove `var _ = strings.Contains` (unused import suppression with no actual usage).
- **`internal/observe/middleware_test.go:205`**: Remove `var _ = attribute.String("", "")`.
- **`internal/discord/dashboard.go:271-284`**: Remove unused `formatDuration` function.

##### 4.2 Replace custom `bytesReaderImpl` with `bytes.NewReader`

- **File:** `internal/config/watcher.go:178-194`
- **Problem:** Unnecessary reimplementation of stdlib `bytes.NewReader`.

##### 4.3 Add missing compile-time interface checks on mocks

- **`internal/agent/mock/mock.go`**: Add `var _ agent.NPCAgent = (*NPCAgent)(nil)` and `var _ agent.Router = (*Router)(nil)`.
- **`internal/engine/mock/mock.go`**: Add `var _ engine.VoiceEngine = (*VoiceEngine)(nil)`.
- **`pkg/audio/mock/mock.go`**: Add checks for `Connection`, `Platform`, `Mixer`.

##### 4.4 Standardize mock reset method naming

- Rename `ResetCalls()` to `Reset()` in STT and S2S session mocks for consistency with provider-level mocks.

##### 4.5 Fix VAD naming inconsistency

- **File:** `pkg/provider/vad/provider.go:75`
- **Problem:** Uses `Engine` instead of `Provider` (every other provider package uses `Provider`).
- **Fix:** Rename to `Provider` or document the intentional deviation.

##### 4.6 Add Deepgram transcript drop observability

- **File:** `pkg/provider/stt/deepgram/deepgram.go:273-284`
- **Problem:** If transcript channels are full, behavior is unclear from logs.
- **Fix:** Add timeout with warning log and dropped-count metric.

##### 4.7 Reduce `time.Sleep` usage in tests

- **Priority files:** `pkg/audio/mixer/mixer_test.go` (20 sleeps), `internal/config/watcher_test.go` (5 sleeps).
- **Fix:** Replace with `require.Eventually`, channel-based sync, or test clocks where feasible.

##### 4.8 Return errors from Discord response helpers

- **File:** `internal/discord/respond.go:11-76`
- **Fix:** Return `error` from `RespondEphemeral`, `RespondEmbed`, `DeferReply`, `FollowUp`, `FollowUpEmbed`. Existing callers can ignore with `_ =`.

##### 4.9 Add `CLAUDE.md` with project conventions

- Create a `CLAUDE.md` at the project root documenting:
  - Interface-first design pattern
  - Compile-time interface assertions
  - Mock conventions (`<package>/mock/` with exported fields)
  - Error wrapping format (`fmt.Errorf("package: %w", err)`)
  - `t.Parallel()` requirement
  - British English spelling convention
  - Functional options pattern for constructors
  - Channel ownership (creator closes)

---

## Acceptance Criteria

### Functional Requirements

- [x] All existing tests pass (`make test` with `-race`)
- [x] No new `golangci-lint` warnings
- [x] All race conditions from Phase 1 are resolved
- [x] Cascade engine `Close()` is safe to call during active `Process()`
- [x] Config watcher `onChange` cannot deadlock
- [x] Context propagation fixed in NPC tool handler

### Non-Functional Requirements

- [x] `GetRecent` query uses composite index scan (verify with `EXPLAIN ANALYZE`)
- [x] No per-utterance allocations in address detection
- [x] Sentence boundary detection is O(n) not O(n^2)
- [x] No memory growth from UtteranceBuffer over 4+ hour sessions

### Quality Gates

- [x] `make check` passes (fmt + vet + test with race detector)
- [x] No duplicate provider name lists
- [x] All mock packages have compile-time interface checks
- [x] Zero dead code (no unused import suppressions)

## Risk Analysis & Mitigation

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Phase 1 fixes change concurrency semantics | Medium | High | Thorough concurrent test coverage; run `-race` in CI |
| Composite DB index slows writes | Low | Low | Index is on a read-heavy table; write overhead is negligible |
| Interface renames break downstream code | Low | Medium | All consumers are internal; grep + fix in same PR |
| VAD `Engine` -> `Provider` rename is breaking | Low | Medium | Internal only; single-PR atomic rename |
| `buildProvider` generic may reduce readability | Low | Low | Keep the generic simple; add doc comment |

## Dependencies & Prerequisites

- No external dependency changes required
- PostgreSQL schema migration needed for composite index (Phase 2.3)
- Phase 3.3 (abstract orchestrator) should be done before Phase 3.4 (shared types) as both touch provider interfaces

## References & Research

### Internal References

- Architecture: `docs/design/01-architecture.md`
- Memory system: `docs/design/03-memory.md`
- MCP tools: `docs/design/04-mcp-tools.md`
- Sentence cascade: `docs/design/05-sentence-cascade.md`
- Technology choices: `docs/design/07-technology.md`
- Roadmap: `docs/design/09-roadmap.md`
- Contributing guide: `CONTRIBUTING.md`

### Key Files by Phase

**Phase 1 (Bugs):**
- `internal/engine/cascade/cascade.go`
- `internal/resilience/fallback.go`
- `internal/config/watcher.go`
- `internal/session/context_manager.go`
- `internal/agent/npc.go`
- `internal/session/reconnect.go`

**Phase 2 (Performance):**
- `internal/agent/orchestrator/address.go`
- `internal/engine/cascade/cascade.go`
- `pkg/memory/postgres/schema.go`
- `internal/hotctx/assembler.go`
- `internal/agent/orchestrator/utterance_buffer.go`
- `pkg/audio/mixer/mixer.go`
- `pkg/provider/stt/whisper/whisper.go`
- `pkg/provider/tts/coqui/coqui.go`

**Phase 3 (Architecture):**
- `cmd/glyphoxa/main.go`
- `internal/config/loader.go`
- `internal/discord/commands/npc.go`
- `pkg/provider/s2s/provider.go`
- `internal/app/app.go`
- `internal/config/config.go`
- `internal/config/diff.go`

**Phase 4 (Quality):**
- `pkg/provider/tts/elevenlabs/elevenlabs.go`
- `internal/observe/middleware_test.go`
- `internal/discord/dashboard.go`
- `internal/config/watcher.go`
- `internal/agent/mock/mock.go`
- `internal/engine/mock/mock.go`
- `pkg/audio/mock/mock.go`
- `internal/discord/respond.go`
