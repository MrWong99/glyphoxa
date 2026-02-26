// Package engine defines the VoiceEngine interface and its supporting types.
//
// A VoiceEngine is responsible for the core conversational loop of a single NPC:
// it receives an audio frame from the player, runs STT → LLM → TTS (or an equivalent
// end-to-end model), and returns a [Response] containing the NPC's reply text,
// a streaming audio channel, and any tool calls the model requested.
//
// Context injection ([VoiceEngine.InjectContext]) lets the orchestrator push
// scene changes, identity updates, and recent utterances into a live session
// without tearing down and re-creating the engine — important for low-latency
// voice loops where re-initialisation costs are unacceptable.
//
// Implementations are provided by provider-specific packages. The interface is
// intentionally narrow so that the orchestrator remains provider-agnostic.
//
// This package lives under internal/ because it encapsulates application-private
// processing logic and is not intended to be imported by external code.
package engine

import (
	"context"
	"sync/atomic"

	"github.com/MrWong99/glyphoxa/internal/mcp"
	"github.com/MrWong99/glyphoxa/pkg/audio"
	"github.com/MrWong99/glyphoxa/pkg/memory"
	"github.com/MrWong99/glyphoxa/pkg/provider/llm"
)

// PromptContext bundles everything the VoiceEngine needs to build the LLM prompt
// for a single [VoiceEngine.Process] call.
type PromptContext struct {
	// SystemPrompt is the full NPC persona / system instruction sent as the
	// first message in the LLM conversation history.
	SystemPrompt string

	// HotContext is a short, dynamically generated string injected just before
	// the player's utterance. Typical contents: current location, active quest,
	// visible objects. Kept intentionally short to fit within latency budgets.
	HotContext string

	// PreFetchResults holds pre-fetched tool results that the orchestrator
	// resolved speculatively before the player finished speaking. Passed as
	// context so the LLM can reference them without issuing additional tool calls.
	PreFetchResults []string

	// Messages is the recent conversation history. The engine may truncate or
	// summarise this list to stay within the model's context window.
	Messages []llm.Message

	// BudgetTier controls which tools are offered to the LLM based on latency
	// constraints. See [mcp.BudgetTier] for tier definitions.
	BudgetTier mcp.BudgetTier
}

// ContextUpdate carries a mid-session context refresh pushed via
// [VoiceEngine.InjectContext]. Fields are merged into the engine's running
// state; zero values are ignored.
type ContextUpdate struct {
	// Identity is an updated NPC persona / system prompt fragment. If non-empty,
	// the engine replaces or amends its current identity context.
	Identity string

	// Scene is an updated description of the current in-game scene sent as
	// additional context to the LLM.
	Scene string

	// RecentUtterances are the latest transcript entries to append to the
	// engine's conversation history before the next process call.
	RecentUtterances []memory.TranscriptEntry
}

// Response is the result of a successful [VoiceEngine.Process] call.
type Response struct {
	// Text is the NPC's reply in plain text (already cleaned of SSML / markup).
	// Useful for logging, transcript recording, and subtitle display.
	Text string

	// Audio is a read-only channel that streams raw audio bytes (e.g., Opus
	// packets or PCM chunks) as they are produced by the TTS stage. The channel
	// is closed when synthesis completes or when a mid-stream error occurs.
	// After the channel closes, call [Response.Err] to check whether synthesis
	// completed cleanly. Callers must drain the channel even if they do not use
	// the audio, to avoid blocking the engine's internal pipeline.
	Audio <-chan []byte

	// ToolCalls lists any tool invocations the LLM requested during generation.
	// The orchestrator is responsible for executing them and, if needed, feeding
	// results back to the engine via a follow-up [VoiceEngine.Process] call.
	ToolCalls []llm.ToolCall

	// streamErr stores the error that caused the Audio channel to close early.
	// Access via Err and SetStreamErr.
	streamErr atomic.Pointer[error]
}

// Err returns the error that caused the Audio channel to close prematurely,
// or nil if the stream completed successfully. Callers should check Err after
// the Audio channel is closed.
func (r *Response) Err() error {
	if p := r.streamErr.Load(); p != nil {
		return *p
	}
	return nil
}

// SetStreamErr records a mid-stream error. The engine goroutine should call
// this before closing the Audio channel so that callers can distinguish a
// clean completion from a failure.
func (r *Response) SetStreamErr(err error) {
	r.streamErr.Store(&err)
}

// VoiceEngine handles the complete speech-in / speech-out pipeline for one NPC.
//
// A single VoiceEngine instance is owned by one NPC agent. Multiple agents must
// not share an engine; create one engine per NPC.
//
// All methods that accept a [context.Context] respect cancellation. Cancelling a
// context passed to [VoiceEngine.Process] will abort the in-flight STT/LLM/TTS
// call and close the [Response.Audio] channel.
//
// Implementations must be safe for concurrent use, though callers should avoid
// issuing concurrent [VoiceEngine.Process] calls for the same NPC unless the
// implementation explicitly documents support for that pattern.
type VoiceEngine interface {
	// Process handles a complete voice interaction: it transcribes input (if the
	// engine performs STT internally), generates a response with the LLM using
	// prompt, synthesises speech, and returns a [Response]. The call blocks until
	// at least the text response is available; audio may continue streaming after
	// Process returns.
	//
	// An error is returned if any pipeline stage fails unrecoverably. Transient
	// errors (e.g., a single dropped packet) are handled internally.
	Process(ctx context.Context, input audio.AudioFrame, prompt PromptContext) (*Response, error)

	// InjectContext pushes an out-of-band context update into the running session.
	// The engine merges update into its state and applies it on the next call to
	// [VoiceEngine.Process]. InjectContext is non-blocking and returns as soon as
	// the update is queued.
	InjectContext(ctx context.Context, update ContextUpdate) error

	// SetTools replaces the full set of tools offered to the LLM. The new list
	// takes effect on the next [VoiceEngine.Process] call. Pass a nil or empty
	// slice to disable tool calling.
	SetTools(tools []llm.ToolDefinition) error

	// OnToolCall registers handler as the synchronous executor for LLM tool calls.
	// When the LLM requests a tool during [VoiceEngine.Process], the engine calls
	// handler(name, args) where args is a JSON-encoded argument string. handler must
	// return a JSON-encoded result string, or a non-nil error if execution fails.
	//
	// Only one handler may be registered at a time; subsequent calls replace the
	// previous registration. handler is called on the engine's internal goroutine
	// and must not block for longer than the configured tool budget.
	OnToolCall(handler func(name string, args string) (string, error))

	// Transcripts returns a read-only channel on which the engine publishes
	// [memory.TranscriptEntry] values — one for each final STT result and one
	// for each NPC response. The channel is closed when the engine is closed.
	Transcripts() <-chan memory.TranscriptEntry

	// Close releases all resources held by the engine (connections, goroutines,
	// TTS synthesis streams). It closes the [Transcripts] channel and is safe to
	// call multiple times; subsequent calls return nil.
	Close() error
}
