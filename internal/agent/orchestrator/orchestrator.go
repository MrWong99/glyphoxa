package orchestrator

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/MrWong99/glyphoxa/internal/agent"
	"github.com/MrWong99/glyphoxa/internal/engine"
	"github.com/MrWong99/glyphoxa/pkg/memory"
	"github.com/MrWong99/glyphoxa/pkg/provider/stt"
)

// Compile-time check that *Orchestrator satisfies [agent.Router].
var _ agent.Router = (*Orchestrator)(nil)

const (
	defaultBufferSize     = 20
	defaultBufferDuration = 5 * time.Minute
)

// Orchestrator manages NPC agents within a Glyphoxa session. It implements
// [agent.Router] and adds lifecycle management, address detection, cross-NPC
// awareness, and DM override capabilities.
//
// All exported methods are safe for concurrent use.
type Orchestrator struct {
	mu          sync.RWMutex
	agents      map[string]*agentEntry // id → entry
	lastSpeaker string                 // id of most recently addressed NPC

	detector *AddressDetector
	buffer   *UtteranceBuffer

	dmOverrides map[string]string // speaker → forced NPC id (puppet mode)
}

// agentEntry pairs an [agent.NPCAgent] with its muted state.
type agentEntry struct {
	agent agent.NPCAgent
	muted bool
}

// Option configures an [Orchestrator] during construction.
type Option func(*Orchestrator)

// WithBufferSize sets the maximum number of entries retained in the
// cross-NPC utterance buffer. The default is 20.
func WithBufferSize(n int) Option {
	return func(o *Orchestrator) {
		o.buffer = NewUtteranceBuffer(n, o.buffer.maxAge)
	}
}

// WithBufferDuration sets the maximum age of entries in the cross-NPC
// utterance buffer. The default is 5 minutes.
func WithBufferDuration(d time.Duration) Option {
	return func(o *Orchestrator) {
		o.buffer = NewUtteranceBuffer(o.buffer.maxSize, d)
	}
}

// New creates an Orchestrator with the given NPC agents and functional options.
//
// Each agent must have a unique [agent.NPCAgent.ID]; duplicates are silently
// dropped (last one wins). Passing a nil or empty agents slice is valid and
// results in an orchestrator with no active agents.
func New(agents []agent.NPCAgent, opts ...Option) *Orchestrator {
	entries := make(map[string]*agentEntry, len(agents))
	for _, a := range agents {
		entries[a.ID()] = &agentEntry{agent: a}
	}

	o := &Orchestrator{
		agents:      entries,
		buffer:      NewUtteranceBuffer(defaultBufferSize, defaultBufferDuration),
		dmOverrides: make(map[string]string),
	}

	for _, opt := range opts {
		opt(o)
	}

	// Build address detector from the final agent set.
	agentSlice := make([]agent.NPCAgent, 0, len(entries))
	for _, e := range entries {
		agentSlice = append(agentSlice, e.agent)
	}
	o.detector = NewAddressDetector(agentSlice)

	return o
}

// Route determines which [agent.NPCAgent] was addressed by speaker's utterance
// and returns that agent. Before returning, Route injects recent cross-NPC
// utterances into the target agent's engine via [engine.VoiceEngine.InjectContext].
//
// If no NPC can be identified or the identified NPC is muted, Route returns
// [ErrNoTarget].
func (o *Orchestrator) Route(ctx context.Context, speaker string, transcript stt.Transcript) (agent.NPCAgent, error) {
	// Respect context cancellation eagerly.
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("orchestrator: %w", err)
	}

	o.mu.Lock()
	defer o.mu.Unlock()

	targetID, err := o.detector.Detect(transcript.Text, o.lastSpeaker, o.agents, o.dmOverrides, speaker)
	if err != nil {
		return nil, err
	}

	entry, ok := o.agents[targetID]
	if !ok || entry.muted {
		return nil, ErrNoTarget
	}

	// Update last speaker for conversational continuity.
	o.lastSpeaker = targetID

	// Inject cross-NPC context.
	recent := o.buffer.Recent(targetID, defaultBufferSize)
	if len(recent) > 0 {
		entries := make([]memory.TranscriptEntry, len(recent))
		for i, r := range recent {
			entries[i] = memory.TranscriptEntry{
				SpeakerID:   r.SpeakerID,
				SpeakerName: r.SpeakerName,
				Text:        r.Text,
				NPCID:       r.NPCID,
				Timestamp:   r.Timestamp,
			}
		}
		if injectErr := entry.agent.Engine().InjectContext(ctx, engine.ContextUpdate{
			RecentUtterances: entries,
		}); injectErr != nil {
			return nil, fmt.Errorf("orchestrator: inject context: %w", injectErr)
		}
	}

	return entry.agent, nil
}

// ActiveAgents returns a snapshot of all NPC agents currently managed by
// this orchestrator, including both muted and unmuted agents.
func (o *Orchestrator) ActiveAgents() []agent.NPCAgent {
	o.mu.RLock()
	defer o.mu.RUnlock()

	result := make([]agent.NPCAgent, 0, len(o.agents))
	for _, e := range o.agents {
		result = append(result, e.agent)
	}
	return result
}

// MuteAgent prevents the agent identified by id from receiving new utterances.
// Returns an error if id does not correspond to a registered agent.
func (o *Orchestrator) MuteAgent(id string) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	entry, ok := o.agents[id]
	if !ok {
		return fmt.Errorf("orchestrator: agent %q not found", id)
	}
	entry.muted = true
	return nil
}

// UnmuteAgent re-enables routing to the agent identified by id.
// Returns an error if id does not correspond to a registered agent.
// Calling UnmuteAgent on an already-unmuted agent is a no-op.
func (o *Orchestrator) UnmuteAgent(id string) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	entry, ok := o.agents[id]
	if !ok {
		return fmt.Errorf("orchestrator: agent %q not found", id)
	}
	entry.muted = false
	return nil
}

// SetPuppet forces all utterances from speaker to be routed to the NPC
// identified by npcID, bypassing address detection. Use for DM puppeteering.
//
// Pass an empty npcID to clear the override for that speaker.
// Returns an error if npcID is non-empty and does not correspond to a
// registered agent.
func (o *Orchestrator) SetPuppet(speaker string, npcID string) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if npcID == "" {
		delete(o.dmOverrides, speaker)
		return nil
	}

	if _, ok := o.agents[npcID]; !ok {
		return fmt.Errorf("orchestrator: agent %q not found", npcID)
	}
	o.dmOverrides[speaker] = npcID
	return nil
}

// AddAgent registers a new NPC agent with the orchestrator.
// Returns an error if an agent with the same ID is already registered.
func (o *Orchestrator) AddAgent(a agent.NPCAgent) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	id := a.ID()
	if _, ok := o.agents[id]; ok {
		return fmt.Errorf("orchestrator: agent %q already registered", id)
	}

	o.agents[id] = &agentEntry{agent: a}
	o.rebuildDetector()
	return nil
}

// RemoveAgent unregisters an NPC agent. Returns an error if not found.
func (o *Orchestrator) RemoveAgent(id string) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if _, ok := o.agents[id]; !ok {
		return fmt.Errorf("orchestrator: agent %q not found", id)
	}

	delete(o.agents, id)

	// Clear last speaker if it was the removed agent.
	if o.lastSpeaker == id {
		o.lastSpeaker = ""
	}

	// Clear any puppet overrides pointing to this agent.
	for sp, npc := range o.dmOverrides {
		if npc == id {
			delete(o.dmOverrides, sp)
		}
	}

	o.rebuildDetector()
	return nil
}

// BroadcastScene pushes a scene update to all active (unmuted) agents.
// Errors from individual agents are collected; the first encountered error
// is returned, but all agents are attempted regardless.
func (o *Orchestrator) BroadcastScene(ctx context.Context, scene agent.SceneContext) error {
	o.mu.RLock()
	agents := make([]*agentEntry, 0, len(o.agents))
	for _, e := range o.agents {
		agents = append(agents, e)
	}
	o.mu.RUnlock()

	var firstErr error
	for _, e := range agents {
		if e.muted {
			continue
		}
		if err := e.agent.UpdateScene(ctx, scene); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("orchestrator: broadcast scene: %w", err)
		}
	}
	return firstErr
}

// rebuildDetector rebuilds the address detector's name index from the current
// agent set. Must be called with o.mu held.
func (o *Orchestrator) rebuildDetector() {
	agents := make([]agent.NPCAgent, 0, len(o.agents))
	for _, e := range o.agents {
		agents = append(agents, e.agent)
	}
	o.detector.Rebuild(agents)
}
