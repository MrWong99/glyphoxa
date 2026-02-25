package agent

import (
	"context"

	"github.com/MrWong99/glyphoxa/pkg/types"
)

// Router determines which NPC was addressed by a player's utterance and
// dispatches the utterance to the appropriate [NPCAgent].
//
// The routing strategy is implementation-defined — common approaches include:
//   - Name-spotting: detect NPC names in the transcript text.
//   - Proximity: route to the nearest NPC in the game world.
//   - LLM-based: ask a small model to classify addressee.
//   - Last-speaker: route to whichever NPC spoke most recently.
//
// Implementations must be safe for concurrent use.
type Router interface {
	// Route determines which [NPCAgent] was addressed by speaker's utterance
	// and returns that agent. If no NPC can be identified as the target,
	// Route returns an error (e.g., a sentinel error defined by the implementation).
	//
	// speaker is the platform participant ID of the player who spoke.
	// transcript is the full STT result including text and metadata.
	//
	// Route must not modify the transcript or attempt to process the utterance
	// itself; that is the responsibility of the returned agent's
	// [NPCAgent.HandleUtterance] method.
	Route(ctx context.Context, speaker string, transcript types.Transcript) (NPCAgent, error)

	// ActiveAgents returns a snapshot of all NPC agents currently managed by
	// this router, including both muted and unmuted agents. The returned slice
	// must not be mutated by the caller.
	ActiveAgents() []NPCAgent

	// MuteAgent prevents the agent identified by id from receiving new utterances.
	// Utterances routed to a muted agent are silently dropped (no error is returned
	// to the caller). If id does not correspond to an active agent, MuteAgent
	// returns an error.
	MuteAgent(id string) error

	// UnmuteAgent re-enables routing to the agent identified by id.
	// If id does not correspond to an active agent, UnmuteAgent returns an error.
	// Calling UnmuteAgent on an already-unmuted agent is a no-op and returns nil.
	UnmuteAgent(id string) error
}
