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

// ─── New ─────────────────────────────────────────────────────────────────────

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

// ─── Init helpers ────────────────────────────────────────────────────────────

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
			Transport: srv.Transport,
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
	pm := audiomixer.New(func([]byte) {})
	a.mixer = pm
	a.closers = append(a.closers, pm.Close)
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

// ─── Run ─────────────────────────────────────────────────────────────────────

// Run starts the main voice processing loop and blocks until ctx is cancelled.
//
// Run connects to the audio platform, starts per-participant processing
// goroutines, and records transcripts. When ctx is done, Run returns
// context.Canceled (or the underlying cause).
func (a *App) Run(ctx context.Context) error {
	// ── Connect to audio platform ────────────────────────────────────────
	if a.providers.Audio != nil {
		channelID := a.cfg.Server.ListenAddr
		conn, err := a.providers.Audio.Connect(ctx, channelID)
		if err != nil {
			return fmt.Errorf("app: connect audio platform: %w", err)
		}
		a.conn = conn

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
	sessionID := "session-" + a.cfg.Campaign.Name
	if sessionID == "session-" {
		sessionID = "session-default"
	}
	for {
		select {
		case <-ctx.Done():
			return
		case entry, ok := <-ch:
			if !ok {
				return
			}
			if err := a.sessions.WriteEntry(ctx, sessionID, entry); err != nil {
				slog.Warn("failed to record transcript", "npc", ag.Name(), "err", err)
			}
		}
	}
}

// ─── Shutdown ────────────────────────────────────────────────────────────────

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

		// Run closers in order.
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

// ─── Helpers ─────────────────────────────────────────────────────────────────

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
