# App Wiring Design

## Problem

`cmd/glyphoxa/main.go` loads config and creates a provider registry but discards all instantiated providers. No engines, agents, orchestrator, MCP host, memory stores, audio platform, or processing loop are wired. The application starts and immediately waits for a signal with nothing running.

All subsystem packages (phases 1–5) are fully implemented with clean interfaces and mocks. The missing piece is the top-level wiring that connects them into a running application.

## Design

### Package: `internal/app`

Two files: `app.go` (struct + lifecycle) and `app_test.go` (integration test).

### Providers struct

Replaces the empty `providerSet` in main.go. Holds one interface value per provider slot:

```go
type Providers struct {
    LLM        llm.Provider        // nil if not configured
    STT        stt.Provider        // nil if not configured
    TTS        tts.Provider        // nil if not configured
    S2S        s2s.Provider        // nil if not configured
    Embeddings embeddings.Provider // nil if not configured
    VAD        vad.Engine          // nil if not configured
    Audio      audio.Platform      // nil if not configured
}
```

### App struct

Owns all subsystem lifetimes:

```go
type App struct {
    cfg       *config.Config
    providers *Providers
    mcpHost   mcp.Host
    entities  entity.Store
    sessions  memory.SessionStore
    graph     memory.KnowledgeGraph
    assembler *hotctx.Assembler
    mixer     audio.Mixer
    conn      audio.Connection
    agents    []agent.NPCAgent
    router    agent.Router
    pipeline  transcript.Pipeline
    closers   []func() error  // ordered shutdown list
}
```

### Functional options for DI

```go
type Option func(*App)

func WithSessionStore(s memory.SessionStore) Option
func WithKnowledgeGraph(g memory.KnowledgeGraph) Option
func WithEntityStore(s entity.Store) Option
func WithMixer(m audio.Mixer) Option
func WithMCPHost(h mcp.Host) Option
```

When not provided, `New` creates real implementations from config.

### New(cfg, providers, ...Option) — all wiring

Order:

1. Apply options (inject test doubles)
2. Entity store: `entity.NewMemStore()` if not injected. Load campaign files and VTT imports from `cfg.Campaign`.
3. Memory: `postgres.NewStore(ctx, dsn, dims)` if not injected. Extract L1 (SessionStore) and L3 (KnowledgeGraph) via `.L1()` / store itself.
4. MCP host: `mcphost.New()` if not injected. `RegisterServer` for each `cfg.MCP.Servers`. Register built-in tools. `Calibrate`.
5. Hot context assembler: `hotctx.NewAssembler(sessions, graph)`.
6. Mixer: `mixer.New(outputFn)` if not injected.
7. Agent loader: `agent.NewLoader(assembler, sessionID, WithMCPHost, WithMixer)`.
8. Per-NPC engine construction based on `NPCConfig.Engine`:
   - `cascaded` / `sentence_cascade`: `cascade.New(fastLLM, strongLLM, tts, voice, opts...)`
   - `s2s`: `s2s.New(s2sProvider, sessionCfg, opts...)`
9. Agent loading: `loader.Load(id, identity, engine, budgetTier)` per NPC.
10. Orchestrator: `orchestrator.New(agents)`.
11. Transcript pipeline: `transcript.NewPipeline(WithPhoneticMatcher, WithLLMCorrector)`.
12. Append closers in reverse-shutdown order.

### Run(ctx) — main processing loop

1. Connect to audio platform: `providers.Audio.Connect(ctx, channelID)`.
2. Start per-participant goroutines reading from `conn.InputStreams()`:
   - VAD filtering (if configured)
   - STT streaming: `sttProvider.StartStream` → read Finals channel
   - Transcript correction via pipeline
   - Route through orchestrator: `router.Route(speaker, transcript)`
   - Agent processes: `agent.HandleUtterance(ctx, speaker, transcript)`
3. Start transcript recording goroutines draining `engine.Transcripts()` → `sessions.WriteEntry`.
4. Register `conn.OnParticipantChange` to add/remove participant goroutines.
5. Register `mixer.OnBargeIn` to interrupt current NPC speech.
6. Block until ctx is cancelled.

### Shutdown(ctx) — ordered teardown

Walk `closers` slice in order:

1. `conn.Disconnect()` — stop accepting audio
2. Close all NPC engines — drain in-flight responses
3. `mcpHost.Close()` — disconnect MCP servers
4. `memoryStore.Close()` — release DB pool
5. Respect shutdown context deadline; log warnings for slow closers.

### main.go changes

Stays thin (~50 lines in `run()`):

```go
func run() int {
    // flags, config, logger (unchanged)
    // registry + registerBuiltinProviders (now with real factories)
    // buildProviders (now returns *app.Providers with real fields)
    // printStartupSummary (unchanged)

    application, err := app.New(ctx, cfg, providers)
    if err != nil { ... return 1 }

    ctx, stop := signal.NotifyContext(...)
    defer stop()

    if err := application.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
        slog.Error("run error", "err", err)
        return 1
    }

    shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
    defer cancel()
    if err := application.Shutdown(shutdownCtx); err != nil {
        slog.Error("shutdown error", "err", err)
        return 1
    }
    return 0
}
```

`buildProviders` gets real `providerSet` fields and returns `*app.Providers`. `registerBuiltinProviders` calls `reg.RegisterLLM("openai", anyllm.Factory)` etc. with real factory functions.

## Integration test: TestApp_FullLifecycle

All mocks, no external dependencies.

```
Setup:
  - Build config.Config with 1 NPC (cascaded engine, budget fast)
  - Create mock providers: LLM, STT, TTS, VAD, Audio Platform
  - Create mock memory: SessionStore, KnowledgeGraph
  - Create mock MCP Host, mock Mixer
  - Inject all via Options

Test flow:
  1. app.New succeeds
  2. Start app.Run in goroutine with cancellable ctx
  3. Simulate: push AudioFrame into mock platform's input channel
  4. Verify: mock engine.Process was called (utterance reached the NPC)
  5. Verify: mock mixer.Enqueue was called (response audio was scheduled)
  6. Cancel context
  7. Verify: app.Shutdown completes within 5s
  8. Assert mock close methods were called (engine.Close, mcpHost.Close)

Assertions:
  - Engine.Process call count == 1
  - Mixer.Enqueue call count == 1
  - Engine.Close call count == 1
  - MCPHost.Close call count == 1
  - No goroutine leaks (goleak or manual)
```

## Files touched

| File | Action |
|------|--------|
| `internal/app/app.go` | New file |
| `internal/app/app_test.go` | New file |
| `cmd/glyphoxa/main.go` | Refactor: thin `run()`, real `providerSet` → `app.Providers`, delegate to `app.New`/`Run`/`Shutdown` |

## Open questions

None — all subsystem interfaces are stable and have mocks.
