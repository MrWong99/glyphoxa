// Package mock provides in-memory mock implementations of [agent.NPCAgent] and
// [agent.Router] for use in unit tests.
//
// All mocks are safe for concurrent use, record method calls, and expose exported
// fields for configuring return values.
//
// Example:
//
//	eng := &enginemock.VoiceEngine{}
//	npc := &mock.NPCAgent{
//	    IDResult:     "greymantle",
//	    NameResult:   "Greymantle the Sage",
//	    EngineResult: eng,
//	}
//	router := &mock.Router{RouteResult: npc}
//	target, err := router.Route(ctx, "player-1", transcript)
package mock

import (
	"context"
	"sync"

	"github.com/MrWong99/glyphoxa/internal/agent"
	"github.com/MrWong99/glyphoxa/internal/engine"
	"github.com/MrWong99/glyphoxa/pkg/provider/stt"
)

// ─── NPCAgent ─────────────────────────────────────────────────────────────────

// HandleUtteranceCall records the arguments of a single [NPCAgent.HandleUtterance] invocation.
type HandleUtteranceCall struct {
	// Speaker is the platform participant ID of the player who spoke.
	Speaker string
	// Transcript is the STT result passed to HandleUtterance.
	Transcript stt.Transcript
}

// UpdateSceneCall records the arguments of a single [NPCAgent.UpdateScene] invocation.
type UpdateSceneCall struct {
	// Scene is the scene context passed to UpdateScene.
	Scene agent.SceneContext
}

// NPCAgent is a mock implementation of [agent.NPCAgent].
type NPCAgent struct {
	mu sync.Mutex

	// IDResult is returned by [NPCAgent.ID].
	IDResult string

	// NameResult is returned by [NPCAgent.Name].
	NameResult string

	// IdentityResult is returned by [NPCAgent.Identity].
	IdentityResult agent.NPCIdentity

	// EngineResult is returned by [NPCAgent.Engine].
	EngineResult engine.VoiceEngine

	// HandleUtteranceError is returned by [NPCAgent.HandleUtterance].
	HandleUtteranceError error

	// UpdateSceneError is returned by [NPCAgent.UpdateScene].
	UpdateSceneError error

	// SpeakTextError is returned by [NPCAgent.SpeakText].
	SpeakTextError error

	// HandleUtteranceCalls records all HandleUtterance invocations.
	HandleUtteranceCalls []HandleUtteranceCall

	// UpdateSceneCalls records all UpdateScene invocations.
	UpdateSceneCalls []UpdateSceneCall

	// CallCountID records how many times ID was called.
	CallCountID int

	// CallCountName records how many times Name was called.
	CallCountName int

	// CallCountIdentity records how many times Identity was called.
	CallCountIdentity int

	// CallCountEngine records how many times Engine was called.
	CallCountEngine int

	// SpeakTextCalls records the text passed to each SpeakText call.
	SpeakTextCalls []string
}

// ID implements [agent.NPCAgent]. Returns IDResult.
func (n *NPCAgent) ID() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.CallCountID++
	return n.IDResult
}

// Name implements [agent.NPCAgent]. Returns NameResult.
func (n *NPCAgent) Name() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.CallCountName++
	return n.NameResult
}

// Identity implements [agent.NPCAgent]. Returns IdentityResult.
func (n *NPCAgent) Identity() agent.NPCIdentity {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.CallCountIdentity++
	return n.IdentityResult
}

// Engine implements [agent.NPCAgent]. Returns EngineResult.
func (n *NPCAgent) Engine() engine.VoiceEngine {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.CallCountEngine++
	return n.EngineResult
}

// HandleUtterance implements [agent.NPCAgent]. Records the call and returns HandleUtteranceError.
func (n *NPCAgent) HandleUtterance(_ context.Context, speaker string, transcript stt.Transcript) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.HandleUtteranceCalls = append(n.HandleUtteranceCalls, HandleUtteranceCall{
		Speaker:    speaker,
		Transcript: transcript,
	})
	return n.HandleUtteranceError
}

// UpdateScene implements [agent.NPCAgent]. Records the call and returns UpdateSceneError.
func (n *NPCAgent) UpdateScene(_ context.Context, scene agent.SceneContext) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.UpdateSceneCalls = append(n.UpdateSceneCalls, UpdateSceneCall{Scene: scene})
	return n.UpdateSceneError
}

// SpeakText implements [agent.NPCAgent]. Records the text and returns SpeakTextError.
func (n *NPCAgent) SpeakText(_ context.Context, text string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.SpeakTextCalls = append(n.SpeakTextCalls, text)
	return n.SpeakTextError
}

// ─── Router ───────────────────────────────────────────────────────────────────

// RouteCall records the arguments of a single [Router.Route] invocation.
type RouteCall struct {
	// Speaker is the platform participant ID of the player who spoke.
	Speaker string
	// Transcript is the STT result passed to Route.
	Transcript stt.Transcript
}

// Router is a mock implementation of [agent.Router].
type Router struct {
	mu sync.Mutex

	// RouteResult is the [agent.NPCAgent] returned by [Router.Route].
	RouteResult agent.NPCAgent

	// RouteError is the error returned by [Router.Route].
	RouteError error

	// ActiveAgentsResult is the slice returned by [Router.ActiveAgents].
	// If nil, an empty non-nil slice is returned.
	ActiveAgentsResult []agent.NPCAgent

	// MuteAgentError is returned by [Router.MuteAgent].
	MuteAgentError error

	// UnmuteAgentError is returned by [Router.UnmuteAgent].
	UnmuteAgentError error

	// RouteCalls records all Route invocations.
	RouteCalls []RouteCall

	// MuteAgentCalls records all agent IDs passed to MuteAgent.
	MuteAgentCalls []string

	// UnmuteAgentCalls records all agent IDs passed to UnmuteAgent.
	UnmuteAgentCalls []string

	// CallCountActiveAgents records how many times ActiveAgents was called.
	CallCountActiveAgents int
}

// Route implements [agent.Router]. Records the call and returns RouteResult / RouteError.
func (r *Router) Route(_ context.Context, speaker string, transcript stt.Transcript) (agent.NPCAgent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.RouteCalls = append(r.RouteCalls, RouteCall{Speaker: speaker, Transcript: transcript})
	return r.RouteResult, r.RouteError
}

// ActiveAgents implements [agent.Router]. Returns ActiveAgentsResult.
// If ActiveAgentsResult is nil, an empty non-nil slice is returned.
func (r *Router) ActiveAgents() []agent.NPCAgent {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.CallCountActiveAgents++
	if r.ActiveAgentsResult == nil {
		return []agent.NPCAgent{}
	}
	return r.ActiveAgentsResult
}

// MuteAgent implements [agent.Router]. Records id and returns MuteAgentError.
func (r *Router) MuteAgent(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.MuteAgentCalls = append(r.MuteAgentCalls, id)
	return r.MuteAgentError
}

// UnmuteAgent implements [agent.Router]. Records id and returns UnmuteAgentError.
func (r *Router) UnmuteAgent(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.UnmuteAgentCalls = append(r.UnmuteAgentCalls, id)
	return r.UnmuteAgentError
}
