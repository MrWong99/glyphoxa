package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/MrWong99/glyphoxa/internal/engine"
	"github.com/MrWong99/glyphoxa/internal/hotctx"
	"github.com/MrWong99/glyphoxa/internal/mcp"
	"github.com/MrWong99/glyphoxa/pkg/audio"
	"github.com/MrWong99/glyphoxa/pkg/memory"
	"github.com/MrWong99/glyphoxa/pkg/provider/llm"
	"github.com/MrWong99/glyphoxa/pkg/provider/stt"
)

// Compile-time interface check: liveAgent must satisfy NPCAgent.
var _ NPCAgent = (*liveAgent)(nil)

// AgentConfig holds all dependencies needed to create a [liveAgent].
//
// Required fields are ID, Engine, Assembler, and SessionID. MCPHost and
// Mixer are optional — a nil MCPHost means no tool calling, and a nil
// Mixer means audio responses are discarded (useful for text-only testing).
type AgentConfig struct {
	// ID is the stable, unique identifier for this NPC within the session.
	// Must not be empty.
	ID string

	// Identity is the NPC's persona configuration (name, personality, voice, etc.).
	Identity NPCIdentity

	// Engine is the VoiceEngine used for STT/LLM/TTS processing. Must not be nil.
	Engine engine.VoiceEngine

	// Assembler is the hot-context assembler used to build prompt context
	// before each LLM call. Must not be nil.
	Assembler *hotctx.Assembler

	// MCPHost is an optional MCP host for tool calling. When non-nil, the
	// agent wires available tools into the engine at construction time and
	// registers a tool-call handler that delegates execution to the host.
	MCPHost mcp.Host

	// Mixer is an optional audio mixer for enqueuing NPC speech segments.
	// When nil, response audio is silently drained instead of played.
	Mixer audio.Mixer

	// SessionID is the session identifier passed to the Assembler for transcript
	// retrieval. Must not be empty.
	SessionID string

	// BudgetTier controls which MCP tools are offered to the LLM based on
	// latency constraints. Defaults to [mcp.BudgetFast] when zero-valued.
	BudgetTier mcp.BudgetTier
}

// defaultAudioPriority is the priority used when enqueuing NPC audio segments.
const defaultAudioPriority = 1

// liveAgent is the concrete implementation of [NPCAgent].
//
// It owns a [engine.VoiceEngine] and coordinates hot context assembly,
// tool wiring, and audio output. Concurrent calls to [liveAgent.HandleUtterance]
// are serialised via an internal mutex to preserve conversational coherence.
type liveAgent struct {
	id         string
	identity   NPCIdentity
	eng        engine.VoiceEngine
	assembler  *hotctx.Assembler
	mcpHost    mcp.Host    // may be nil if no tools
	mixer      audio.Mixer // may be nil if not using mixer
	sessionID  string
	budgetTier mcp.BudgetTier

	mu       sync.Mutex
	scene    SceneContext
	messages []llm.Message // recent conversation history
}

// NewAgent creates a concrete [NPCAgent] from the given configuration.
//
// It validates that required fields are set, wires MCP tools into the engine
// when an MCPHost is provided, and returns the fully initialised agent.
//
// Errors are prefixed with "agent: ".
func NewAgent(cfg AgentConfig) (NPCAgent, error) {
	if cfg.ID == "" {
		return nil, errors.New("agent: ID must not be empty")
	}
	if cfg.Engine == nil {
		return nil, errors.New("agent: Engine must not be nil")
	}
	if cfg.Assembler == nil {
		return nil, errors.New("agent: Assembler must not be nil")
	}
	if cfg.SessionID == "" {
		return nil, errors.New("agent: SessionID must not be empty")
	}

	a := &liveAgent{
		id:         cfg.ID,
		identity:   cfg.Identity,
		eng:        cfg.Engine,
		assembler:  cfg.Assembler,
		mcpHost:    cfg.MCPHost,
		mixer:      cfg.Mixer,
		sessionID:  cfg.SessionID,
		budgetTier: cfg.BudgetTier,
	}

	// Wire MCP tools into the engine when a host is provided.
	if cfg.MCPHost != nil {
		tools := cfg.MCPHost.AvailableTools(cfg.BudgetTier)
		if err := cfg.Engine.SetTools(tools); err != nil {
			return nil, fmt.Errorf("agent: set tools: %w", err)
		}
		cfg.Engine.OnToolCall(func(name string, args string) (string, error) {
			result, err := cfg.MCPHost.ExecuteTool(context.Background(), name, args)
			if err != nil {
				return "", fmt.Errorf("agent: execute tool %q: %w", name, err)
			}
			return result.Content, nil
		})
	}

	return a, nil
}

// ID returns the stable, unique identifier for this NPC within the session.
func (a *liveAgent) ID() string { return a.id }

// Name returns the human-readable in-world name of this NPC.
func (a *liveAgent) Name() string { return a.identity.Name }

// Identity returns the full NPC persona configuration.
func (a *liveAgent) Identity() NPCIdentity { return a.identity }

// Engine returns the underlying [engine.VoiceEngine] that this agent uses
// for STT/LLM/TTS processing.
func (a *liveAgent) Engine() engine.VoiceEngine { return a.eng }

// HandleUtterance processes a player's spoken utterance directed at this NPC.
//
// The implementation:
//  1. Assembles hot context via the [hotctx.Assembler].
//  2. Formats a system prompt from the hot context and NPC personality.
//  3. Builds a [engine.PromptContext] with the system prompt, messages, and budget tier.
//  4. Calls [engine.VoiceEngine.Process] with a synthetic (empty) audio frame.
//  5. Enqueues the response audio to the mixer (if set).
//  6. Records the exchange in the conversation history.
//
// HandleUtterance respects context cancellation. Concurrent calls are serialised
// via an internal mutex.
func (a *liveAgent) HandleUtterance(ctx context.Context, speaker string, transcript stt.Transcript) error {
	// Check context before acquiring the lock.
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("agent: %w", err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	// Check context again after acquiring the lock (we may have waited).
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("agent: %w", err)
	}

	// 1. Assemble hot context.
	hctx, err := a.assembler.Assemble(ctx, a.id, a.sessionID)
	if err != nil {
		return fmt.Errorf("agent: assemble hot context: %w", err)
	}

	// 2. Format system prompt.
	systemPrompt := hotctx.FormatSystemPrompt(hctx, a.identity.Personality)

	// 3. Build prompt context with current messages + the user's new utterance.
	userMsg := llm.Message{
		Role:    "user",
		Content: transcript.Text,
		Name:    speaker,
	}
	msgs := make([]llm.Message, len(a.messages), len(a.messages)+1)
	copy(msgs, a.messages)
	msgs = append(msgs, userMsg)

	// Build the hot context string from the assembled hot context.
	var hotContextStr string
	if hctx.SceneContext != nil {
		var parts []string
		if hctx.SceneContext.Location != nil {
			parts = append(parts, "Location: "+hctx.SceneContext.Location.Name)
		}
		for _, e := range hctx.SceneContext.PresentEntities {
			parts = append(parts, "Present: "+e.Name)
		}
		for _, q := range hctx.SceneContext.ActiveQuests {
			parts = append(parts, "Quest: "+q.Name)
		}
		hotContextStr = strings.Join(parts, "; ")
	}

	promptCtx := engine.PromptContext{
		SystemPrompt: systemPrompt,
		HotContext:   hotContextStr,
		Messages:     msgs,
		BudgetTier:   a.budgetTier,
	}

	// 4. Create a synthetic audio frame (cascaded mode: STT already ran).
	frame := audio.AudioFrame{
		Data:       nil,
		SampleRate: 16000,
		Channels:   1,
		Timestamp:  0,
	}

	resp, err := a.eng.Process(ctx, frame, promptCtx)
	if err != nil {
		return fmt.Errorf("agent: engine process: %w", err)
	}

	// 5. Enqueue response audio to mixer (if set), otherwise drain.
	if a.mixer != nil && resp.Audio != nil {
		seg := &audio.AudioSegment{
			NPCID:    a.id,
			Audio:    resp.Audio,
			Priority: defaultAudioPriority,
		}
		a.mixer.Enqueue(seg, defaultAudioPriority)
	} else if resp.Audio != nil {
		// Drain audio channel to avoid blocking the engine pipeline.
		go func(ch <-chan []byte) {
			for range ch {
			}
		}(resp.Audio)
	}

	// 6. Record the exchange in conversation history.
	a.messages = append(a.messages, userMsg)
	if resp.Text != "" {
		a.messages = append(a.messages, llm.Message{
			Role:    "assistant",
			Content: resp.Text,
			Name:    a.identity.Name,
		})
	}

	return nil
}

// UpdateScene pushes a new scene context to the NPC. The scene is stored
// under lock and injected into the engine via [engine.VoiceEngine.InjectContext]
// so that subsequent responses reflect the updated environment.
func (a *liveAgent) UpdateScene(ctx context.Context, scene SceneContext) error {
	// Snapshot both the new scene and the current conversation history in a
	// single lock acquisition so that the two pieces of state are consistent
	// with each other and cannot be interleaved with a concurrent HandleUtterance.
	a.mu.Lock()
	a.scene = scene
	recentEntries := make([]memory.TranscriptEntry, 0, len(a.messages))
	for _, msg := range a.messages {
		recentEntries = append(recentEntries, memory.TranscriptEntry{
			SpeakerID:   msg.Name,
			SpeakerName: msg.Name,
			Text:        msg.Content,
			Timestamp:   time.Now(),
		})
	}
	a.mu.Unlock()

	// Build a scene description string outside the lock; scene is a value copy.
	var parts []string
	if scene.Location != "" {
		parts = append(parts, "Location: "+scene.Location)
	}
	if scene.TimeOfDay != "" {
		parts = append(parts, "Time: "+scene.TimeOfDay)
	}
	if len(scene.PresentEntities) > 0 {
		parts = append(parts, "Present: "+strings.Join(scene.PresentEntities, ", "))
	}
	if len(scene.ActiveQuests) > 0 {
		parts = append(parts, "Quests: "+strings.Join(scene.ActiveQuests, ", "))
	}
	sceneStr := strings.Join(parts, "; ")

	update := engine.ContextUpdate{
		Scene:            sceneStr,
		RecentUtterances: recentEntries,
	}

	if err := a.eng.InjectContext(ctx, update); err != nil {
		return fmt.Errorf("agent: inject context: %w", err)
	}

	return nil
}
