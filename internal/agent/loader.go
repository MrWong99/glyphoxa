package agent

import (
	"errors"

	"github.com/MrWong99/glyphoxa/internal/engine"
	"github.com/MrWong99/glyphoxa/internal/hotctx"
	"github.com/MrWong99/glyphoxa/internal/mcp"
	"github.com/MrWong99/glyphoxa/pkg/audio"
)

// Loader creates [NPCAgent] instances by wiring together their dependencies.
//
// A Loader is constructed once per session with shared infrastructure (assembler,
// session ID, optional MCP host and audio mixer) and then used to create
// individual agents via [Loader.Load]. This avoids repeating dependency plumbing
// for each NPC.
//
// Loader is safe for concurrent use after construction; its fields are immutable.
type Loader struct {
	assembler *hotctx.Assembler
	mcpHost   mcp.Host
	mixer     audio.Mixer
	sessionID string
}

// LoaderOption is a functional option for [NewLoader].
type LoaderOption func(*Loader)

// WithMCPHost configures the [Loader] to inject the given [mcp.Host] into every
// agent it creates, enabling MCP tool calling.
func WithMCPHost(host mcp.Host) LoaderOption {
	return func(l *Loader) { l.mcpHost = host }
}

// WithMixer configures the [Loader] to inject the given [audio.Mixer] into every
// agent it creates, enabling audio playback through the shared mixer.
func WithMixer(mixer audio.Mixer) LoaderOption {
	return func(l *Loader) { l.mixer = mixer }
}

// NewLoader creates a [Loader] with the given shared dependencies.
//
// assembler is the hot-context assembler shared by all agents created by this
// loader. sessionID is the session identifier used for transcript retrieval.
// Both are required; passing nil or empty values will cause [Loader.Load] to
// return validation errors.
//
// Use [WithMCPHost] and [WithMixer] to configure optional dependencies.
func NewLoader(assembler *hotctx.Assembler, sessionID string, opts ...LoaderOption) *Loader {
	l := &Loader{
		assembler: assembler,
		sessionID: sessionID,
	}
	for _, o := range opts {
		o(l)
	}
	return l
}

// Load creates an [NPCAgent] from the given identity and engine.
//
// id is the stable NPC identifier; identity describes the NPC's persona; eng is
// the engine instance dedicated to this NPC; budgetTier controls which MCP tools
// are available. If budgetTier is zero-valued it defaults to [mcp.BudgetFast].
//
// The loader injects its shared assembler, session ID, MCP host, and mixer into
// the new agent automatically.
//
// Errors are prefixed with "agent: ".
func (l *Loader) Load(id string, identity NPCIdentity, eng engine.VoiceEngine, budgetTier mcp.BudgetTier) (NPCAgent, error) {
	if l.assembler == nil {
		return nil, errors.New("agent: Loader has nil Assembler")
	}
	if l.sessionID == "" {
		return nil, errors.New("agent: Loader has empty SessionID")
	}

	return NewAgent(AgentConfig{
		ID:         id,
		Identity:   identity,
		Engine:     eng,
		Assembler:  l.assembler,
		MCPHost:    l.mcpHost,
		Mixer:      l.mixer,
		SessionID:  l.sessionID,
		BudgetTier: budgetTier,
	})
}
