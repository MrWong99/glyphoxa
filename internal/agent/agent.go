// Package agent defines the NPCAgent and Router interfaces, along with the
// identity and scene-context types used by the orchestrator to drive NPC behaviour.
//
// The two primary abstractions are:
//
//   - [NPCAgent] — represents a single NPC, owns its [engine.VoiceEngine], and
//     reacts to player utterances and scene changes.
//   - [Router] — decides which NPC (if any) was addressed by a player and
//     distributes utterances accordingly.
//
// This package lives under internal/ because it encapsulates application-private
// orchestration logic and is not intended to be imported by external code.
package agent

import (
	"context"

	"github.com/MrWong99/glyphoxa/internal/engine"
	"github.com/MrWong99/glyphoxa/pkg/types"
)

// NPCIdentity describes the static persona of an NPC.
// It is loaded at startup from a configuration file or database and passed
// to [NPCAgent.Identity] callers and the underlying [engine.VoiceEngine].
type NPCIdentity struct {
	// Name is the NPC's in-world name (e.g., "Greymantle the Sage").
	Name string

	// Personality is a free-text description of the NPC's character, speech
	// patterns, quirks, and motivations. Injected verbatim into the LLM system prompt.
	Personality string

	// Voice is the TTS voice profile used when synthesising this NPC's speech.
	Voice types.VoiceProfile

	// KnowledgeScope lists topics or domains the NPC is knowledgeable about.
	// The orchestrator uses this list to route player questions to the most
	// appropriate NPC and to build retrieval queries.
	KnowledgeScope []string

	// SecretKnowledge lists facts the NPC knows but will not volunteer —
	// they must be revealed through specific conversational triggers.
	// This list is injected into the system prompt under strict confidentiality
	// instructions so that the LLM guards them appropriately.
	SecretKnowledge []string

	// BehaviorRules are hard constraints on the NPC's responses (e.g.,
	// "Never break character", "Always speak in archaic English").
	// Appended to the system prompt as a numbered list of rules.
	BehaviorRules []string
}

// SceneContext describes the current in-game situation passed to an NPC
// via [NPCAgent.UpdateScene]. The NPC uses this information to adapt
// its dialogue to the evolving game world.
type SceneContext struct {
	// Location is the name of the current in-game location (e.g., "Thornwood Tavern").
	Location string

	// TimeOfDay is a narrative descriptor of the in-game time (e.g., "late evening",
	// "dawn", "high noon"). The NPC may reference this in ambient dialogue.
	TimeOfDay string

	// PresentNPCs lists the IDs of other NPCs currently sharing the scene with
	// this NPC. Can be used to generate inter-NPC acknowledgements or to avoid
	// contradicting what another NPC might say.
	PresentNPCs []string

	// ActiveQuests lists the quest IDs that are currently active and relevant
	// to this NPC's knowledge. Used to surface quest hints and plot-critical
	// dialogue options.
	ActiveQuests []string
}

// NPCAgent controls a single NPC within a Glyphoxa session.
//
// Each NPCAgent owns exactly one [engine.VoiceEngine] and is responsible for
// translating high-level orchestrator instructions (player utterances, scene
// updates) into engine calls.
//
// Implementations must be safe for concurrent use. An NPCAgent is expected to
// outlive individual player utterances and may handle many calls to
// [NPCAgent.HandleUtterance] over the lifetime of a session.
type NPCAgent interface {
	// ID returns the stable, unique identifier for this NPC within the session.
	// IDs are used as map keys and for logging; they must not change after creation.
	ID() string

	// Name returns the human-readable in-world name of this NPC.
	Name() string

	// Identity returns the full NPC persona configuration.
	Identity() NPCIdentity

	// Engine returns the underlying [engine.VoiceEngine] that this agent uses
	// for STT/LLM/TTS processing. The orchestrator may use this for direct
	// low-level control (e.g., tool injection, transcript streaming) while the
	// agent handles the higher-level conversational logic.
	Engine() engine.VoiceEngine

	// HandleUtterance processes a player's spoken utterance directed at this NPC.
	// speaker is the platform participant ID of the player.
	// transcript is the STT result including text, confidence, and timing.
	//
	// The implementation must:
	//  1. Build a [engine.PromptContext] from current state.
	//  2. Call [engine.VoiceEngine.Process] with the transcript audio frame.
	//  3. Enqueue the resulting audio for playback via the audio mixer.
	//  4. Record the exchange in the session transcript log.
	//
	// HandleUtterance returns when the NPC's reply has been fully enqueued for
	// playback (not when audio finishes playing). It is safe to call from multiple
	// goroutines, though concurrent calls for the same NPC will be serialised
	// internally to preserve conversational coherence.
	HandleUtterance(ctx context.Context, speaker string, transcript types.Transcript) error

	// UpdateScene pushes a new scene context to the NPC. The NPC incorporates
	// this into its next [engine.VoiceEngine.InjectContext] call so that subsequent
	// responses reflect the updated environment.
	//
	// UpdateScene is non-blocking; the context is queued and applied before the
	// next HandleUtterance call processes the player's speech.
	UpdateScene(ctx context.Context, scene SceneContext) error
}
