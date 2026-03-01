package app

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/MrWong99/glyphoxa/internal/agent"
	"github.com/MrWong99/glyphoxa/internal/agent/orchestrator"
	"github.com/MrWong99/glyphoxa/internal/config"
	"github.com/MrWong99/glyphoxa/internal/entity"
	"github.com/MrWong99/glyphoxa/internal/hotctx"
	"github.com/MrWong99/glyphoxa/internal/mcp"
	"github.com/MrWong99/glyphoxa/internal/session"
	"github.com/MrWong99/glyphoxa/pkg/audio"
	audiomixer "github.com/MrWong99/glyphoxa/pkg/audio/mixer"
	"github.com/MrWong99/glyphoxa/pkg/memory"
	"github.com/MrWong99/glyphoxa/pkg/provider/llm"
)

// consolidationInterval is the consolidation period for alpha mode sessions.
const consolidationInterval = 5 * time.Minute

// SessionInfo holds metadata about an active session.
type SessionInfo struct {
	// SessionID is the unique identifier for this session.
	SessionID string

	// CampaignName is the name of the campaign being played.
	CampaignName string

	// StartedAt is when the session was started.
	StartedAt time.Time

	// StartedBy is the Discord user ID of the DM who started the session.
	StartedBy string

	// ChannelID is the voice channel ID the session is connected to.
	ChannelID string
}

// SessionManager manages the lifecycle of voice sessions.
// Only one session can be active at a time (enforced by mutex).
// All exported methods are safe for concurrent use.
type SessionManager struct {
	mu           sync.Mutex
	active       bool
	info         SessionInfo
	conn         audio.Connection
	orch         *orchestrator.Orchestrator
	consolidator *session.Consolidator
	mixer        audio.Mixer
	agents       []agent.NPCAgent
	cancel       context.CancelFunc

	// closers are called in reverse order during Stop.
	closers []func() error

	// Dependencies injected at construction.
	platform     audio.Platform
	cfg          *config.Config
	providers    *Providers
	sessionStore memory.SessionStore
	graph        memory.KnowledgeGraph
	mcpHost      mcp.Host
	entities     entity.Store
}

// SessionManagerConfig holds all dependencies for a [SessionManager].
type SessionManagerConfig struct {
	Platform     audio.Platform
	Config       *config.Config
	Providers    *Providers
	SessionStore memory.SessionStore
	Graph        memory.KnowledgeGraph
	MCPHost      mcp.Host
	Entities     entity.Store
}

// NewSessionManager creates a SessionManager with the given dependencies.
func NewSessionManager(cfg SessionManagerConfig) *SessionManager {
	return &SessionManager{
		platform:     cfg.Platform,
		cfg:          cfg.Config,
		providers:    cfg.Providers,
		sessionStore: cfg.SessionStore,
		graph:        cfg.Graph,
		mcpHost:      cfg.MCPHost,
		entities:     cfg.Entities,
	}
}

// Start begins a new voice session. It connects to the voice channel,
// creates NPC agents, sets up the orchestrator, starts the consolidator,
// and begins processing audio.
//
// Returns an error if a session is already active.
func (sm *SessionManager) Start(ctx context.Context, channelID string, dmUserID string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.active {
		return fmt.Errorf("session: a session is already active (id=%s)", sm.info.SessionID)
	}

	// Generate session ID.
	campaignName := sm.cfg.Campaign.Name
	if campaignName == "" {
		campaignName = "default"
	}
	now := time.Now().UTC()
	sessionID := fmt.Sprintf("session-%s-%s",
		sanitizeName(campaignName),
		now.Format("20060102T1504Z"),
	)

	// Connect to voice channel.
	conn, err := sm.platform.Connect(ctx, channelID)
	if err != nil {
		return fmt.Errorf("session: connect to voice channel: %w", err)
	}

	// Create mixer for this session, wired to the voice connection output.
	outStream := conn.OutputStream()
	var mixer audio.Mixer
	var closers []func() error
	pm := audiomixer.New(func(frame audio.AudioFrame) {
		outStream <- frame
	})
	mixer = pm
	closers = append(closers, pm.Close)

	// Create hot-context assembler.
	assembler := hotctx.NewAssembler(sm.sessionStore, sm.graph)

	// Create NPC agents from config.
	agents, agentClosers, err := sm.loadAgents(ctx, assembler, mixer, sessionID)
	if err != nil {
		// Clean up mixer on failure.
		_ = pm.Close()
		_ = conn.Disconnect()
		return fmt.Errorf("session: load agents: %w", err)
	}
	closers = append(closers, agentClosers...)

	// Create orchestrator with loaded agents.
	orch := orchestrator.New(agents)

	// Create a session-scoped context for background work.
	sessionCtx, cancel := context.WithCancel(context.Background())

	// Start consolidator if we have a session store and a context manager.
	// For the alpha, create a minimal consolidator that periodically writes
	// to the session store.
	var consolid *session.Consolidator
	if sm.sessionStore != nil {
		// Create a context manager for the consolidator.
		ctxMgr := session.NewContextManager(session.ContextManagerConfig{
			MaxTokens:      128000,
			ThresholdRatio: 0.75,
			Summariser:     &noopSummariser{},
		})
		consolid = session.NewConsolidator(session.ConsolidatorConfig{
			Store:      sm.sessionStore,
			ContextMgr: ctxMgr,
			SessionID:  sessionID,
			Interval:   consolidationInterval,
		})
		consolid.Start(sessionCtx)
	}

	sm.active = true
	sm.conn = conn
	sm.orch = orch
	sm.consolidator = consolid
	sm.mixer = mixer
	sm.agents = agents
	sm.cancel = cancel
	sm.closers = closers
	sm.info = SessionInfo{
		SessionID:    sessionID,
		CampaignName: campaignName,
		StartedAt:    now,
		StartedBy:    dmUserID,
		ChannelID:    channelID,
	}

	slog.Info("session started",
		"session_id", sessionID,
		"channel_id", channelID,
		"dm_user_id", dmUserID,
		"campaign", campaignName,
		"npcs", len(agents),
	)

	return nil
}

// Stop gracefully ends the active session. It consolidates remaining
// conversation history, disconnects from voice, and cleans up resources.
//
// Returns an error if no session is active.
func (sm *SessionManager) Stop(ctx context.Context) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if !sm.active {
		return fmt.Errorf("session: no active session to stop")
	}

	sessionID := sm.info.SessionID

	// Consolidate remaining conversation history before teardown.
	if sm.consolidator != nil {
		if err := sm.consolidator.ConsolidateNow(ctx); err != nil {
			slog.Warn("session: final consolidation error", "session_id", sessionID, "err", err)
		}
		sm.consolidator.Stop()
	}

	// Disconnect from voice.
	if sm.conn != nil {
		if err := sm.conn.Disconnect(); err != nil {
			slog.Warn("session: voice disconnect error", "session_id", sessionID, "err", err)
		}
	}

	// Cancel session context (stops consolidator loop and background work).
	if sm.cancel != nil {
		sm.cancel()
	}

	// Run closers (engines, mixer) in reverse order.
	for i := len(sm.closers) - 1; i >= 0; i-- {
		if err := sm.closers[i](); err != nil {
			slog.Warn("session: closer error", "session_id", sessionID, "index", i, "err", err)
		}
	}

	// Clear state.
	sm.active = false
	sm.conn = nil
	sm.orch = nil
	sm.consolidator = nil
	sm.mixer = nil
	sm.agents = nil
	sm.cancel = nil
	sm.closers = nil
	sm.info = SessionInfo{}

	slog.Info("session stopped", "session_id", sessionID)

	return nil
}

// IsActive reports whether a session is currently running.
func (sm *SessionManager) IsActive() bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.active
}

// Info returns metadata about the active session.
// Returns zero value if no session is active.
func (sm *SessionManager) Info() SessionInfo {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.info
}

// Orchestrator returns the active session's orchestrator.
// Returns nil if no session is active.
func (sm *SessionManager) Orchestrator() *orchestrator.Orchestrator {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.orch
}

// PropagateEntity persists a new entity and propagates it to the knowledge
// graph for mid-session use. Steps:
//  1. Add entity to the entity store.
//  2. Convert to memory.Entity and add to the knowledge graph.
//  3. (Best-effort) STT keyword boosting and phonetic index are logged but
//     not yet wired through agents; providers that support mid-session keyword
//     updates will be integrated in a future release.
//
// Returns the stored entity (with generated ID) and any error.
func (sm *SessionManager) PropagateEntity(ctx context.Context, def entity.EntityDefinition) (entity.EntityDefinition, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.entities == nil {
		return entity.EntityDefinition{}, fmt.Errorf("propagate entity: entity store not configured")
	}

	// Step 1: Persist to entity store.
	stored, err := sm.entities.Add(ctx, def)
	if err != nil {
		return entity.EntityDefinition{}, fmt.Errorf("propagate entity: store add: %w", err)
	}

	// Step 2: Add to knowledge graph if available.
	if sm.graph != nil {
		attrs := make(map[string]any)
		if stored.Description != "" {
			attrs["description"] = stored.Description
		}
		for k, v := range stored.Properties {
			attrs[k] = v
		}
		if len(stored.Tags) > 0 {
			attrs["tags"] = stored.Tags
		}

		memEntity := memory.Entity{
			ID:         stored.ID,
			Type:       string(stored.Type),
			Name:       stored.Name,
			Attributes: attrs,
		}

		if graphErr := sm.graph.AddEntity(ctx, memEntity); graphErr != nil {
			slog.Warn("propagate entity: knowledge graph add failed (entity stored but not in graph)",
				"entity_id", stored.ID, "name", stored.Name, "err", graphErr)
		} else {
			slog.Info("propagate entity: added to knowledge graph", "entity_id", stored.ID, "name", stored.Name)
		}
	}

	// Step 3: STT keyword boost (best-effort, not yet wired through agents).
	// Future: iterate over active agents and call SessionHandle.SetKeywords
	// with the entity name added to the boost list.
	slog.Debug("propagate entity: STT keyword boost not yet wired for mid-session updates", "name", stored.Name)

	return stored, nil
}

// loadAgents creates per-NPC engines and agents, mirroring App.initAgents.
// Returns the loaded agents and a list of closers for engine cleanup.
func (sm *SessionManager) loadAgents(ctx context.Context, assembler *hotctx.Assembler, mixer audio.Mixer, sessionID string) ([]agent.NPCAgent, []func() error, error) {
	if len(sm.cfg.NPCs) == 0 {
		slog.Info("session: no NPCs configured")
		return nil, nil, nil
	}

	var loaderOpts []agent.LoaderOption
	if sm.mcpHost != nil {
		loaderOpts = append(loaderOpts, agent.WithMCPHost(sm.mcpHost))
	}
	if mixer != nil {
		loaderOpts = append(loaderOpts, agent.WithMixer(mixer))
	}
	if sm.providers.TTS != nil {
		loaderOpts = append(loaderOpts, agent.WithTTS(sm.providers.TTS))
	}

	loader, err := agent.NewLoader(assembler, sessionID, loaderOpts...)
	if err != nil {
		return nil, nil, fmt.Errorf("create agent loader: %w", err)
	}

	var agents []agent.NPCAgent
	var closers []func() error

	for i, npc := range sm.cfg.NPCs {
		eng, err := buildEngine(sm.providers, npc)
		if err != nil {
			// Clean up already-created engines on failure.
			for j := len(closers) - 1; j >= 0; j-- {
				_ = closers[j]()
			}
			return nil, nil, fmt.Errorf("build engine for NPC %q (index %d): %w", npc.Name, i, err)
		}
		closers = append(closers, eng.Close)

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
			for j := len(closers) - 1; j >= 0; j-- {
				_ = closers[j]()
			}
			return nil, nil, fmt.Errorf("load agent %q: %w", npc.Name, err)
		}
		agents = append(agents, ag)
		slog.Info("session: loaded NPC agent", "name", npc.Name, "engine", npc.Engine, "tier", tier)
	}

	_ = ctx // reserved for future async agent init
	return agents, closers, nil
}

// sanitizeName replaces spaces with hyphens and lowercases a name
// for use in session IDs.
func sanitizeName(name string) string {
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, " ", "-")
	return name
}

// noopSummariser is a placeholder summariser that returns an empty string.
// Used during alpha to satisfy the ContextManager's Summariser requirement
// without needing an LLM provider.
type noopSummariser struct{}

func (n *noopSummariser) Summarise(_ context.Context, _ []llm.Message) (string, error) {
	return "", nil
}
