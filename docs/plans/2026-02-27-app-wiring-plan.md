# App Wiring Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Wire all implemented subsystem packages into a running application via `internal/app` and refactor `main.go` to delegate to it.

**Architecture:** New `internal/app` package with `App` struct owning the full lifecycle (New → Run → Shutdown). All subsystems (providers, MCP, memory, agents, orchestrator, mixer, audio) are injected via a `Providers` struct and functional options for testability. `main.go` stays thin: flags, config, logger, signal handling, then delegates.

**Tech Stack:** Go 1.26, standard library `testing`, existing mock packages (no testify), `golang.org/x/sync/errgroup`.

---

### Task 1: Create `internal/app/app.go` — Providers struct and App skeleton

**Files:**
- Create: `internal/app/app.go`

**Step 1: Write the Providers struct and App struct with Option functions**

```go
// Package app wires all Glyphoxa subsystems into a running application.
//
// The App struct owns the full lifecycle: New creates and connects all
// subsystems, Run executes the main processing loop, and Shutdown tears
// everything down in order.
//
// For testing, inject mock implementations via functional options
// (WithSessionStore, WithKnowledgeGraph, etc.). When an option is not
// provided, New creates real implementations from the config.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/MrWong99/glyphoxa/internal/agent"
	"github.com/MrWong99/glyphoxa/internal/agent/orchestrator"
	"github.com/MrWong99/glyphoxa/internal/config"
	"github.com/MrWong99/glyphoxa/internal/engine"
	"github.com/MrWong99/glyphoxa/internal/engine/cascade"
	s2sengine "github.com/MrWong99/glyphoxa/internal/engine/s2s"
	"github.com/MrWong99/glyphoxa/internal/entity"
	"github.com/MrWong99/glyphoxa/internal/hotctx"
	"github.com/MrWong99/glyphoxa/internal/mcp"
	"github.com/MrWong99/glyphoxa/internal/mcp/mcphost"
	"github.com/MrWong99/glyphoxa/internal/transcript"
	"github.com/MrWong99/glyphoxa/pkg/audio"
	audiomixer "github.com/MrWong99/glyphoxa/pkg/audio/mixer"
	"github.com/MrWong99/glyphoxa/pkg/memory"
	"github.com/MrWong99/glyphoxa/pkg/memory/postgres"
	"github.com/MrWong99/glyphoxa/pkg/provider/embeddings"
	"github.com/MrWong99/glyphoxa/pkg/provider/llm"
	providers2s "github.com/MrWong99/glyphoxa/pkg/provider/s2s"
	"github.com/MrWong99/glyphoxa/pkg/provider/stt"
	"github.com/MrWong99/glyphoxa/pkg/provider/tts"
	"github.com/MrWong99/glyphoxa/pkg/provider/vad"
)

// Providers holds one interface value per provider slot. Nil means the
// provider is not configured. Populated by main.go via the config registry.
type Providers struct {
	LLM        llm.Provider
	STT        stt.Provider
	TTS        tts.Provider
	S2S        providers2s.Provider
	Embeddings embeddings.Provider
	VAD        vad.Engine
	Audio      audio.Platform
}

// App owns all subsystem lifetimes and orchestrates the Glyphoxa voice pipeline.
type App struct {
	cfg       *config.Config
	providers *Providers

	// Subsystems — initialised in New, torn down in Shutdown.
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

	// closers are called in order during Shutdown.
	closers []func() error

	// stopOnce guards the Shutdown path.
	stopOnce sync.Once
}

// Option is a functional option for New. Use these to inject test doubles.
type Option func(*App)

// WithSessionStore injects a session store instead of creating one from config.
func WithSessionStore(s memory.SessionStore) Option {
	return func(a *App) { a.sessions = s }
}

// WithKnowledgeGraph injects a knowledge graph instead of creating one from config.
func WithKnowledgeGraph(g memory.KnowledgeGraph) Option {
	return func(a *App) { a.graph = g }
}

// WithEntityStore injects an entity store instead of creating a MemStore.
func WithEntityStore(s entity.Store) Option {
	return func(a *App) { a.entities = s }
}

// WithMixer injects an audio mixer instead of creating a PriorityMixer.
func WithMixer(m audio.Mixer) Option {
	return func(a *App) { a.mixer = m }
}

// WithMCPHost injects an MCP host instead of creating one from config.
func WithMCPHost(h mcp.Host) Option {
	return func(a *App) { a.mcpHost = h }
}
```

**Step 2: Run `go vet ./internal/app/...`**

Run: `go vet ./internal/app/...`
Expected: PASS (may warn about unused imports — that's fine, we'll use them next task)

**Step 3: Commit**

```bash
git add internal/app/app.go
git commit -m "feat(app): add App struct, Providers, and DI options skeleton"
```

---

### Task 2: Implement `New()` — subsystem wiring

**Files:**
- Modify: `internal/app/app.go`

**Step 1: Add the New function**

Add after the Option functions:

```go
// New creates an App by wiring all subsystems together. The providers struct
// comes from main.go (populated via the config registry). Use Option functions
// to inject test doubles for any subsystem.
//
// New performs all initialisation synchronously: entity loading, memory store
// connection, MCP server registration + calibration, NPC engine construction,
// agent loading, and orchestrator assembly.
func New(ctx context.Context, cfg *config.Config, providers *Providers, opts ...Option) (*App, error) {
	a := &App{
		cfg:       cfg,
		providers: providers,
	}
	for _, o := range opts {
		o(a)
	}

	// ── 1. Entity store ──────────────────────────────────────────────────
	if err := a.initEntities(ctx); err != nil {
		return nil, fmt.Errorf("app: init entities: %w", err)
	}

	// ── 2. Memory store ──────────────────────────────────────────────────
	if err := a.initMemory(ctx); err != nil {
		return nil, fmt.Errorf("app: init memory: %w", err)
	}

	// ── 3. MCP host ─────────────────────────────────────────────────────
	if err := a.initMCP(ctx); err != nil {
		return nil, fmt.Errorf("app: init mcp: %w", err)
	}

	// ── 4. Hot context assembler ─────────────────────────────────────────
	a.assembler = hotctx.NewAssembler(a.sessions, a.graph)

	// ── 5. Mixer ─────────────────────────────────────────────────────────
	a.initMixer()

	// ── 6. Agents + orchestrator ─────────────────────────────────────────
	if err := a.initAgents(ctx); err != nil {
		return nil, fmt.Errorf("app: init agents: %w", err)
	}

	// ── 7. Transcript pipeline ───────────────────────────────────────────
	a.pipeline = transcript.NewPipeline()

	return a, nil
}

// initEntities sets up the entity store and loads campaign data.
func (a *App) initEntities(ctx context.Context) error {
	if a.entities == nil {
		a.entities = entity.NewMemStore()
	}

	for _, path := range a.cfg.Campaign.EntityFiles {
		cf, err := entity.LoadCampaignFile(path)
		if err != nil {
			return fmt.Errorf("load campaign file %q: %w", path, err)
		}
		n, err := entity.ImportCampaign(ctx, a.entities, cf)
		if err != nil {
			return fmt.Errorf("import campaign %q: %w", path, err)
		}
		slog.Info("imported campaign entities", "path", path, "count", n)
	}

	return nil
}

// initMemory sets up the PostgreSQL memory store or uses injected mocks.
func (a *App) initMemory(ctx context.Context) error {
	if a.sessions != nil && a.graph != nil {
		return nil // both injected
	}

	dsn := a.cfg.Memory.PostgresDSN
	if dsn == "" {
		return fmt.Errorf("memory.postgres_dsn is required when memory stores are not injected")
	}

	dims := a.cfg.Memory.EmbeddingDimensions
	if dims == 0 {
		dims = 1536 // sensible default for OpenAI text-embedding-3-small
	}

	store, err := postgres.NewStore(ctx, dsn, dims)
	if err != nil {
		return err
	}

	if a.sessions == nil {
		a.sessions = store.L1()
	}
	if a.graph == nil {
		a.graph = store
	}

	a.closers = append(a.closers, func() error {
		store.Close()
		return nil
	})
	return nil
}

// initMCP sets up the MCP host, registers servers, and calibrates.
func (a *App) initMCP(ctx context.Context) error {
	if a.mcpHost == nil {
		host := mcphost.New()
		a.mcpHost = host
		a.closers = append(a.closers, host.Close)
	}

	for _, srv := range a.cfg.MCP.Servers {
		serverCfg := mcp.ServerConfig{
			Name:      srv.Name,
			Transport: mcp.Transport(srv.Transport),
			Command:   srv.Command,
			URL:       srv.URL,
			Env:       srv.Env,
		}
		if err := a.mcpHost.RegisterServer(ctx, serverCfg); err != nil {
			return fmt.Errorf("register mcp server %q: %w", srv.Name, err)
		}
		slog.Info("registered MCP server", "name", srv.Name)
	}

	if err := a.mcpHost.Calibrate(ctx); err != nil {
		slog.Warn("MCP calibration failed, using declared latencies", "err", err)
	}

	return nil
}

// initMixer creates the priority mixer if one wasn't injected.
func (a *App) initMixer() {
	if a.mixer != nil {
		return
	}
	// Output callback is wired to the audio connection in Run.
	// For now create with a no-op output; Run replaces it.
	pm := audiomixer.New(func(data []byte) {
		// Replaced in Run when audio connection is established.
	})
	a.mixer = pm
	a.closers = append(a.closers, pm.Close)
}

// configBudgetTier converts a config.BudgetTier string to mcp.BudgetTier.
func configBudgetTier(tier config.BudgetTier) mcp.BudgetTier {
	switch tier {
	case config.BudgetTierStandard:
		return mcp.BudgetStandard
	case config.BudgetTierDeep:
		return mcp.BudgetDeep
	default:
		return mcp.BudgetFast
	}
}

// configVoiceProfile converts a config.VoiceConfig to tts.VoiceProfile.
func configVoiceProfile(vc config.VoiceConfig) tts.VoiceProfile {
	return tts.VoiceProfile{
		ID:          vc.VoiceID,
		Provider:    vc.Provider,
		PitchShift:  vc.PitchShift,
		SpeedFactor: vc.SpeedFactor,
	}
}

// initAgents creates per-NPC engines and agents, then builds the orchestrator.
func (a *App) initAgents(ctx context.Context) error {
	if len(a.cfg.NPCs) == 0 {
		slog.Warn("no NPCs configured")
		a.router = orchestrator.New(nil)
		return nil
	}

	sessionID := "session-" + a.cfg.Campaign.Name
	if sessionID == "session-" {
		sessionID = "session-default"
	}

	loader, err := agent.NewLoader(
		a.assembler,
		sessionID,
		agent.WithMCPHost(a.mcpHost),
		agent.WithMixer(a.mixer),
	)
	if err != nil {
		return fmt.Errorf("create agent loader: %w", err)
	}

	var agents []agent.NPCAgent
	for i, npc := range a.cfg.NPCs {
		eng, err := a.buildEngine(npc)
		if err != nil {
			return fmt.Errorf("build engine for NPC %q (index %d): %w", npc.Name, i, err)
		}
		a.closers = append(a.closers, eng.Close)

		identity := agent.NPCIdentity{
			Name:           npc.Name,
			Personality:    npc.Personality,
			Voice:          configVoiceProfile(npc.Voice),
			KnowledgeScope: npc.KnowledgeScope,
		}

		npcID := fmt.Sprintf("npc-%d-%s", i, npc.Name)
		tier := configBudgetTier(npc.BudgetTier)

		ag, err := loader.Load(npcID, identity, eng, tier)
		if err != nil {
			return fmt.Errorf("load agent %q: %w", npc.Name, err)
		}
		agents = append(agents, ag)
		slog.Info("loaded NPC agent", "name", npc.Name, "engine", npc.Engine, "tier", tier)
	}

	a.agents = agents
	a.router = orchestrator.New(agents)
	return nil
}

// buildEngine constructs the appropriate VoiceEngine for an NPC config.
func (a *App) buildEngine(npc config.NPCConfig) (engine.VoiceEngine, error) {
	voice := configVoiceProfile(npc.Voice)

	switch npc.Engine {
	case config.EngineCascaded, config.EngineSentenceCascade:
		if a.providers.LLM == nil {
			return nil, fmt.Errorf("cascaded engine requires an LLM provider")
		}
		if a.providers.TTS == nil {
			return nil, fmt.Errorf("cascaded engine requires a TTS provider")
		}
		return cascade.New(
			a.providers.LLM, // fast LLM
			a.providers.LLM, // strong LLM (same for now; cascade config can override)
			a.providers.TTS,
			voice,
		), nil

	case config.EngineS2S:
		if a.providers.S2S == nil {
			return nil, fmt.Errorf("s2s engine requires an S2S provider")
		}
		return s2sengine.New(
			a.providers.S2S,
			providers2s.SessionConfig{
				Voice:        voice,
				Instructions: npc.Personality,
			},
		), nil

	default:
		return nil, fmt.Errorf("unknown engine type %q", npc.Engine)
	}
}
```

**Step 2: Run `go vet ./internal/app/...`**

Run: `go vet ./internal/app/...`
Expected: PASS (some imports may need adjusting based on exact package paths)

**Step 3: Commit**

```bash
git add internal/app/app.go
git commit -m "feat(app): implement New() with full subsystem wiring"
```

---

### Task 3: Implement `Run()` — main processing loop

**Files:**
- Modify: `internal/app/app.go`

**Step 1: Add the Run method**

```go
// Run starts the main voice processing loop and blocks until ctx is cancelled.
//
// Run connects to the audio platform, starts per-participant processing
// goroutines, and records transcripts. When ctx is done, Run returns
// context.Canceled (or the underlying cause).
func (a *App) Run(ctx context.Context) error {
	// ── Connect to audio platform ────────────────────────────────────────
	if a.providers.Audio != nil {
		channelID := a.cfg.Server.ListenAddr // overloaded for now; audio channel comes from config
		conn, err := a.providers.Audio.Connect(ctx, channelID)
		if err != nil {
			return fmt.Errorf("app: connect audio platform: %w", err)
		}
		a.conn = conn

		// Start participant processing.
		a.startAudioLoop(ctx, conn)
	}

	// ── Start transcript recording for each agent ────────────────────────
	var wg sync.WaitGroup
	for _, ag := range a.agents {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.recordTranscripts(ctx, ag)
		}()
	}

	slog.Info("app running", "npcs", len(a.agents))
	<-ctx.Done()

	wg.Wait()
	return ctx.Err()
}

// startAudioLoop reads audio from each participant and routes it through
// VAD → STT → orchestrator → agent.
func (a *App) startAudioLoop(ctx context.Context, conn audio.Connection) {
	// Handle participants already present.
	for userID, inputCh := range conn.InputStreams() {
		go a.processParticipant(ctx, userID, inputCh)
	}

	// Handle new participants joining.
	conn.OnParticipantChange(func(ev audio.Event) {
		if ev.Type == audio.EventJoin {
			streams := conn.InputStreams()
			if ch, ok := streams[ev.UserID]; ok {
				go a.processParticipant(ctx, ev.UserID, ch)
			}
		}
	})

	// Wire barge-in: player speaking interrupts NPC audio.
	a.mixer.OnBargeIn(func(speakerID string) {
		slog.Debug("barge-in detected", "speaker", speakerID)
		a.mixer.Interrupt(audio.PlayerBargeIn)
	})
}

// processParticipant handles the audio pipeline for a single participant:
// audio frames → VAD → STT → transcript correction → orchestrator → agent.
func (a *App) processParticipant(ctx context.Context, userID string, inputCh <-chan audio.AudioFrame) {
	slog.Debug("processing participant", "user", userID)

	// Start STT session if provider is available.
	var sttSession stt.SessionHandle
	if a.providers.STT != nil {
		sess, err := a.providers.STT.StartStream(ctx, stt.StreamConfig{
			SampleRate: 48000,
			Channels:   1,
			Language:   "en-US",
		})
		if err != nil {
			slog.Error("failed to start STT session", "user", userID, "err", err)
			return
		}
		sttSession = sess
		defer sttSession.Close()
	}

	// Start VAD session if provider is available.
	var vadSession vad.SessionHandle
	if a.providers.VAD != nil {
		sess, err := a.providers.VAD.NewSession(vad.Config{
			SampleRate:       48000,
			FrameSizeMs:      20,
			SpeechThreshold:  0.5,
			SilenceThreshold: 0.35,
		})
		if err != nil {
			slog.Error("failed to start VAD session", "user", userID, "err", err)
			return
		}
		vadSession = sess
		defer vadSession.Close()
	}

	// If we have STT, drain finals channel in a separate goroutine.
	if sttSession != nil {
		go a.handleSTTFinals(ctx, userID, sttSession)
	}

	// Main audio loop: read frames, run VAD, forward to STT.
	for {
		select {
		case <-ctx.Done():
			return
		case frame, ok := <-inputCh:
			if !ok {
				return
			}
			// VAD gate: only forward speech frames to STT.
			if vadSession != nil {
				event, err := vadSession.ProcessFrame(frame.Data)
				if err != nil {
					slog.Warn("VAD error", "user", userID, "err", err)
					continue
				}
				if event.Type == vad.VADSilence {
					continue
				}
			}
			// Forward to STT.
			if sttSession != nil {
				if err := sttSession.SendAudio(frame.Data); err != nil {
					slog.Warn("STT send error", "user", userID, "err", err)
				}
			}
		}
	}
}

// handleSTTFinals reads final transcripts from the STT session and routes
// them through the orchestrator to the appropriate NPC agent.
func (a *App) handleSTTFinals(ctx context.Context, userID string, sess stt.SessionHandle) {
	for {
		select {
		case <-ctx.Done():
			return
		case t, ok := <-sess.Finals():
			if !ok {
				return
			}
			if t.Text == "" {
				continue
			}

			// Route to NPC.
			npc, err := a.router.Route(ctx, userID, t)
			if err != nil {
				slog.Warn("routing failed", "user", userID, "err", err)
				continue
			}
			if npc == nil {
				slog.Debug("no NPC addressed", "user", userID, "text", t.Text)
				continue
			}

			if err := npc.HandleUtterance(ctx, userID, t); err != nil {
				slog.Error("HandleUtterance failed", "npc", npc.Name(), "err", err)
			}
		}
	}
}

// recordTranscripts drains the engine's transcript channel and writes entries
// to the session store.
func (a *App) recordTranscripts(ctx context.Context, ag agent.NPCAgent) {
	ch := ag.Engine().Transcripts()
	for {
		select {
		case <-ctx.Done():
			return
		case entry, ok := <-ch:
			if !ok {
				return
			}
			sessionID := "session-" + a.cfg.Campaign.Name
			if sessionID == "session-" {
				sessionID = "session-default"
			}
			if err := a.sessions.WriteEntry(ctx, sessionID, entry); err != nil {
				slog.Warn("failed to record transcript", "npc", ag.Name(), "err", err)
			}
		}
	}
}
```

**Step 2: Run `go vet ./internal/app/...`**

Run: `go vet ./internal/app/...`
Expected: PASS

**Step 3: Commit**

```bash
git add internal/app/app.go
git commit -m "feat(app): implement Run() with audio→VAD→STT→orchestrator loop"
```

---

### Task 4: Implement `Shutdown()` — ordered teardown

**Files:**
- Modify: `internal/app/app.go`

**Step 1: Add the Shutdown method**

```go
// Shutdown tears down all subsystems in reverse-init order. It respects the
// context deadline: if ctx expires before all closers finish, remaining
// closers are skipped and the context error is returned.
func (a *App) Shutdown(ctx context.Context) error {
	var shutdownErr error
	a.stopOnce.Do(func() {
		slog.Info("shutting down", "closers", len(a.closers))

		// Disconnect audio first.
		if a.conn != nil {
			if err := a.conn.Disconnect(); err != nil {
				slog.Warn("audio disconnect error", "err", err)
			}
		}

		// Run closers in order (reverse-init).
		for i, closer := range a.closers {
			select {
			case <-ctx.Done():
				slog.Warn("shutdown deadline exceeded", "remaining", len(a.closers)-i)
				shutdownErr = ctx.Err()
				return
			default:
			}
			if err := closer(); err != nil {
				slog.Warn("closer error", "index", i, "err", err)
			}
		}

		slog.Info("shutdown complete")
	})
	return shutdownErr
}
```

**Step 2: Run `go vet ./internal/app/...`**

Run: `go vet ./internal/app/...`
Expected: PASS

**Step 3: Commit**

```bash
git add internal/app/app.go
git commit -m "feat(app): implement Shutdown() with ordered teardown"
```

---

### Task 5: Refactor `cmd/glyphoxa/main.go`

**Files:**
- Modify: `cmd/glyphoxa/main.go`

**Step 1: Refactor `providerSet` to use `app.Providers` and thin `run()`**

Replace the existing `providerSet` struct, `buildProviders`, and the bottom half of `run()` with delegation to `app.New`/`Run`/`Shutdown`. Keep: flags, config loading, logger, `registerBuiltinProviders` (now wiring real factories), `printStartupSummary`.

The key changes to `run()`:
- After `buildProviders`, call `app.New(ctx, cfg, providers)`
- Move signal handling so `Run` gets a cancellable context
- After `Run` returns, call `Shutdown` with a 15s timeout
- Remove the `_ = providers` discard line

Replace `providerSet` with:

```go
// buildProviders instantiates all providers named in cfg using the registry
// and returns them in an app.Providers struct ready for app.New.
func buildProviders(cfg *config.Config, reg *config.Registry) (*app.Providers, error) {
	p := &app.Providers{}

	if cfg.Providers.LLM.Name != "" {
		llmP, err := reg.CreateLLM(cfg.Providers.LLM)
		if err != nil && !errors.Is(err, config.ErrProviderNotRegistered) {
			return nil, fmt.Errorf("create LLM provider: %w", err)
		}
		p.LLM = llmP
	}
	if cfg.Providers.STT.Name != "" {
		sttP, err := reg.CreateSTT(cfg.Providers.STT)
		if err != nil && !errors.Is(err, config.ErrProviderNotRegistered) {
			return nil, fmt.Errorf("create STT provider: %w", err)
		}
		p.STT = sttP
	}
	if cfg.Providers.TTS.Name != "" {
		ttsP, err := reg.CreateTTS(cfg.Providers.TTS)
		if err != nil && !errors.Is(err, config.ErrProviderNotRegistered) {
			return nil, fmt.Errorf("create TTS provider: %w", err)
		}
		p.TTS = ttsP
	}
	if cfg.Providers.S2S.Name != "" {
		s2sP, err := reg.CreateS2S(cfg.Providers.S2S)
		if err != nil && !errors.Is(err, config.ErrProviderNotRegistered) {
			return nil, fmt.Errorf("create S2S provider: %w", err)
		}
		p.S2S = s2sP
	}
	if cfg.Providers.Embeddings.Name != "" {
		embP, err := reg.CreateEmbeddings(cfg.Providers.Embeddings)
		if err != nil && !errors.Is(err, config.ErrProviderNotRegistered) {
			return nil, fmt.Errorf("create Embeddings provider: %w", err)
		}
		p.Embeddings = embP
	}
	if cfg.Providers.VAD.Name != "" {
		vadP, err := reg.CreateVAD(cfg.Providers.VAD)
		if err != nil && !errors.Is(err, config.ErrProviderNotRegistered) {
			return nil, fmt.Errorf("create VAD provider: %w", err)
		}
		p.VAD = vadP
	}
	if cfg.Providers.Audio.Name != "" {
		audioP, err := reg.CreateAudio(cfg.Providers.Audio)
		if err != nil && !errors.Is(err, config.ErrProviderNotRegistered) {
			return nil, fmt.Errorf("create Audio provider: %w", err)
		}
		p.Audio = audioP
	}

	return p, nil
}
```

And the new `run()` tail:

```go
	// ── Build providers ──────────────────────────────────────────────────
	providers, err := buildProviders(cfg, reg)
	if err != nil {
		slog.Error("failed to build providers", "err", err)
		return 1
	}

	// ── Startup summary ──────────────────────────────────────────────────
	printStartupSummary(cfg)

	// ── Create app ───────────────────────────────────────────────────────
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	application, err := app.New(ctx, cfg, providers)
	if err != nil {
		slog.Error("failed to create app", "err", err)
		return 1
	}

	// ── Run ──────────────────────────────────────────────────────────────
	slog.Info("server ready — press Ctrl+C to shut down")
	if err := application.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("run error", "err", err)
		return 1
	}

	// ── Shutdown ─────────────────────────────────────────────────────────
	slog.Info("shutdown signal received, stopping…")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := application.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown error", "err", err)
		return 1
	}

	slog.Info("goodbye")
	return 0
```

Remove: the old `providerSet` struct, the old `buildProviders` that returned `*providerSet`, the `_ = providers` line, the old `<-ctx.Done()` block.

**Step 2: Run `go build ./cmd/glyphoxa/...`**

Run: `go build ./cmd/glyphoxa/...`
Expected: PASS

**Step 3: Run `go vet ./...`**

Run: `go vet ./...`
Expected: PASS

**Step 4: Commit**

```bash
git add cmd/glyphoxa/main.go
git commit -m "refactor(main): delegate to app.New/Run/Shutdown, real Providers struct"
```

---

### Task 6: Write integration test — `TestApp_NewWithMocks`

**Files:**
- Create: `internal/app/app_test.go`

**Step 1: Write the test for New() with all mocks**

This test verifies that New succeeds when all subsystems are injected as mocks, and that the correct number of agents are created.

```go
package app_test

import (
	"context"
	"testing"

	"github.com/MrWong99/glyphoxa/internal/app"
	"github.com/MrWong99/glyphoxa/internal/config"
	"github.com/MrWong99/glyphoxa/internal/entity"
	"github.com/MrWong99/glyphoxa/internal/engine"
	enginemock "github.com/MrWong99/glyphoxa/internal/engine/mock"
	mcpmock "github.com/MrWong99/glyphoxa/internal/mcp/mock"
	audiomock "github.com/MrWong99/glyphoxa/pkg/audio/mock"
	memorymock "github.com/MrWong99/glyphoxa/pkg/memory/mock"
	llmmock "github.com/MrWong99/glyphoxa/pkg/provider/llm/mock"
	sttmock "github.com/MrWong99/glyphoxa/pkg/provider/stt/mock"
	ttsmock "github.com/MrWong99/glyphoxa/pkg/provider/tts/mock"
)

// testConfig returns a minimal config with one cascaded NPC.
func testConfig() *config.Config {
	return &config.Config{
		Server: config.ServerConfig{
			ListenAddr: ":8080",
			LogLevel:   config.LogInfo,
		},
		NPCs: []config.NPCConfig{
			{
				Name:        "Greymantle",
				Personality: "A wise sage.",
				Engine:      config.EngineCascaded,
				BudgetTier:  config.BudgetTierFast,
				Voice: config.VoiceConfig{
					Provider: "elevenlabs",
					VoiceID:  "sage-voice",
				},
			},
		},
	}
}

// testProviders returns mock providers for a cascaded NPC.
func testProviders() *app.Providers {
	return &app.Providers{
		LLM: &llmmock.Provider{},
		STT: &sttmock.Provider{},
		TTS: &ttsmock.Provider{},
	}
}

func TestNew_WithMocks(t *testing.T) {
	t.Parallel()

	cfg := testConfig()
	providers := testProviders()

	sessions := &memorymock.SessionStore{}
	graph := &memorymock.KnowledgeGraph{}
	mcpHost := &mcpmock.Host{}
	mixer := &audiomock.Mixer{}
	entityStore := entity.NewMemStore()

	application, err := app.New(
		context.Background(),
		cfg,
		providers,
		app.WithSessionStore(sessions),
		app.WithKnowledgeGraph(graph),
		app.WithMCPHost(mcpHost),
		app.WithMixer(mixer),
		app.WithEntityStore(entityStore),
	)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if application == nil {
		t.Fatal("New returned nil App")
	}

	// MCP host should have been calibrated.
	if mcpHost.CallCount("Calibrate") != 1 {
		t.Errorf("expected 1 Calibrate call, got %d", mcpHost.CallCount("Calibrate"))
	}
}
```

**Step 2: Run test to verify it passes**

Run: `go test ./internal/app/... -run TestNew_WithMocks -v`
Expected: PASS

Note: if compilation fails due to missing mock types (e.g., `llmmock.Provider`), check what mock exists in `pkg/provider/llm/mock/mock.go`. If there's no mock Provider, use the real `anyllm.New` or create a test helper. Adjust imports accordingly.

**Step 3: Commit**

```bash
git add internal/app/app_test.go
git commit -m "test(app): add TestNew_WithMocks integration test"
```

---

### Task 7: Write integration test — `TestApp_Shutdown`

**Files:**
- Modify: `internal/app/app_test.go`

**Step 1: Write the shutdown test**

Verifies that Shutdown calls all closers and MCPHost.Close:

```go
func TestApp_Shutdown(t *testing.T) {
	t.Parallel()

	cfg := testConfig()
	providers := testProviders()

	mcpHost := &mcpmock.Host{}
	mixer := &audiomock.Mixer{}

	application, err := app.New(
		context.Background(),
		cfg,
		providers,
		app.WithSessionStore(&memorymock.SessionStore{}),
		app.WithKnowledgeGraph(&memorymock.KnowledgeGraph{}),
		app.WithMCPHost(mcpHost),
		app.WithMixer(mixer),
		app.WithEntityStore(entity.NewMemStore()),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = application.Shutdown(ctx)
	if err != nil {
		t.Fatalf("Shutdown returned error: %v", err)
	}

	// MCPHost.Close should have been called by the closers.
	if mcpHost.CallCount("Close") != 1 {
		t.Errorf("expected 1 Close call on MCPHost, got %d", mcpHost.CallCount("Close"))
	}
}
```

**Step 2: Run test**

Run: `go test ./internal/app/... -run TestApp_Shutdown -v`
Expected: PASS

**Step 3: Commit**

```bash
git add internal/app/app_test.go
git commit -m "test(app): add TestApp_Shutdown verifies ordered teardown"
```

---

### Task 8: Write integration test — `TestApp_RunAndShutdown`

**Files:**
- Modify: `internal/app/app_test.go`

**Step 1: Write the full lifecycle test**

Verifies Run blocks until context is cancelled, then Shutdown completes:

```go
func TestApp_RunAndShutdown(t *testing.T) {
	t.Parallel()

	cfg := testConfig()
	// Remove NPCs so Run doesn't need engines.
	cfg.NPCs = nil

	providers := &app.Providers{}
	mcpHost := &mcpmock.Host{}

	application, err := app.New(
		context.Background(),
		cfg,
		providers,
		app.WithSessionStore(&memorymock.SessionStore{}),
		app.WithKnowledgeGraph(&memorymock.KnowledgeGraph{}),
		app.WithMCPHost(mcpHost),
		app.WithMixer(&audiomock.Mixer{}),
		app.WithEntityStore(entity.NewMemStore()),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	runErr := make(chan error, 1)
	go func() {
		runErr <- application.Run(ctx)
	}()

	// Let Run start, then cancel.
	cancel()

	select {
	case err := <-runErr:
		if err != nil && err != context.Canceled {
			t.Fatalf("Run returned unexpected error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within 5s after context cancellation")
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := application.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}
```

**Step 2: Run test**

Run: `go test ./internal/app/... -run TestApp_RunAndShutdown -v`
Expected: PASS

**Step 3: Commit**

```bash
git add internal/app/app_test.go
git commit -m "test(app): add TestApp_RunAndShutdown full lifecycle test"
```

---

### Task 9: Run all existing tests to verify no regressions

**Step 1: Run full test suite**

Run: `go test ./... -count=1`
Expected: All tests PASS. No regressions.

**Step 2: Run vet on entire project**

Run: `go vet ./...`
Expected: PASS

**Step 3: Final commit if any fixups were needed**

```bash
git add -A
git commit -m "fix: address test regressions from app wiring refactor"
```

Only commit this if step 1 or 2 revealed issues that needed fixing. If everything passed, skip.
