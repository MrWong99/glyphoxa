// Package s2s defines the Provider interface for Speech-to-Speech (S2S) backends.
//
// An S2S provider wraps a real-time voice AI service that accepts raw audio input
// and returns synthesised audio output in a single, stateful session — bypassing
// the separate STT → LLM → TTS pipeline entirely. Examples include the OpenAI
// Realtime API and similar low-latency voice models.
//
// The central abstraction is SessionHandle: a bidirectional, multiplexed channel
// that carries audio, transcripts, and tool calls concurrently. Sessions are
// designed to be long-lived (seconds to minutes) and support mid-session
// reconfiguration.
//
// All implementations must be safe for concurrent use.
package s2s

import (
	"context"

	"github.com/MrWong99/glyphoxa/pkg/memory"
	"github.com/MrWong99/glyphoxa/pkg/provider/llm"
	"github.com/MrWong99/glyphoxa/pkg/provider/tts"
)

// ToolCallHandler is a callback invoked by the session whenever the underlying
// model requests a tool call. The handler receives the tool name and a
// JSON-encoded arguments string and must return either a result string (to be
// injected back into the session as tool output) or an error.
//
// The handler must not block for longer than necessary; long-running tools should
// be offloaded to a goroutine and the result injected asynchronously if the
// provider permits it. The handler may be called from the session's internal
// receive goroutine — implementors must not call blocking session methods from
// within the handler to avoid deadlocks.
type ToolCallHandler func(name string, args string) (string, error)

// ContextItem is a text message injected into the session's context mid-conversation.
// It is used to add background knowledge, NPC state updates, or corrected
// transcripts without resending the full conversation history.
type ContextItem struct {
	// Role is the speaker role for this context item. Typical values match LLM
	// message roles: "system", "user", "assistant".
	Role string

	// Content is the text content of the context item.
	Content string
}

// SessionConfig is the initial configuration for a new S2S session.
type SessionConfig struct {
	// Voice defines the voice the model will use for synthesised speech output.
	Voice tts.VoiceProfile

	// Instructions is the system-level prompt that defines the NPC's personality,
	// backstory, and behavioural constraints. Equivalent to a system message in the
	// LLM paradigm.
	Instructions string

	// Tools is the initial set of tool definitions offered to the model. The model
	// may invoke these during the session; tool calls are surfaced via the
	// ToolCallHandler set with OnToolCall.
	Tools []llm.ToolDefinition
}

// S2SCapabilities describes static properties of the S2S provider.
// The values are assumed constant for the lifetime of the Provider instance.
type S2SCapabilities struct {
	// ContextWindow is the maximum token count (or provider-equivalent unit) the
	// model can maintain across the session.
	ContextWindow int

	// MaxSessionDurationMs is the hard upper bound on session lifetime in
	// milliseconds, as imposed by the provider. Zero means no documented limit.
	MaxSessionDurationMs int

	// SupportsResumption indicates whether a session can be reconnected after a
	// transient network failure without losing accumulated context.
	SupportsResumption bool

	// Voices lists the voice profiles available for this provider.
	Voices []tts.VoiceProfile
}

// SessionHandle represents an open S2S session. It is an interface so that test
// code can supply mock implementations without a live provider connection.
//
// The session is the hot path of the Glyphoxa voice pipeline — every method must
// return quickly. Audio I/O is channel-based to avoid blocking the caller's audio
// thread. All methods must be safe for concurrent use.
//
// Callers must call Close when the session is no longer needed.
type SessionHandle interface {
	// SendAudio delivers a raw PCM audio chunk to the provider for processing.
	// The chunk must match the audio format negotiated when the session was opened.
	// Returns an error if the session is closed or if the provider cannot accept
	// the chunk (e.g., buffer full, network error).
	SendAudio(chunk []byte) error

	// Audio returns a read-only channel that emits raw PCM audio byte slices as
	// the model synthesises its spoken response. The channel is closed when the
	// session ends or when a mid-stream error occurs. After the channel closes,
	// call [SessionHandle.Err] to check whether the session ended cleanly.
	// Consumers must drain this channel promptly to prevent backpressure from
	// stalling the provider's receive loop.
	Audio() <-chan []byte

	// Err returns the error that caused the Audio channel to close prematurely,
	// or nil if the session ended cleanly. Callers should check Err after the
	// Audio channel is closed.
	Err() error

	// Transcripts returns a read-only channel that emits TranscriptEntry values for
	// both user speech (as recognised by the model) and NPC responses (as generated
	// text). The channel is closed when the session ends.
	Transcripts() <-chan memory.TranscriptEntry

	// OnToolCall registers a handler that is invoked synchronously whenever the
	// model requests a tool call. Only one handler can be active at a time; calling
	// OnToolCall again replaces the previous handler. Passing nil clears the handler.
	// See ToolCallHandler for concurrency constraints.
	OnToolCall(handler ToolCallHandler)

	// SetTools replaces the active tool definitions without restarting the session.
	// Providers that do not support mid-session tool updates may return an error.
	// The change takes effect on a best-effort basis for in-flight turns.
	SetTools(tools []llm.ToolDefinition) error

	// UpdateInstructions replaces the system-level instructions for the NPC.
	// Providers that do not support mid-session instruction updates may return an
	// error. Effective immediately for the next model turn.
	UpdateInstructions(instructions string) error

	// InjectTextContext inserts one or more ContextItems into the session's rolling
	// context. This is used to surface important state (NPC health, quest updates)
	// without waiting for the user to speak. Implementations should append items in
	// order and truncate oldest context if the session's ContextWindow is exceeded.
	InjectTextContext(items []ContextItem) error

	// Interrupt signals the provider to stop generating the current response and
	// discard any buffered audio. Use this when the user begins speaking mid-response
	// (barge-in) or when the orchestrator needs to redirect the conversation. Returns
	// an error if the provider does not support interruption.
	Interrupt() error

	// Close terminates the session, releases all resources, and closes the Audio and
	// Transcripts channels. Calling Close more than once is safe and returns nil.
	Close() error
}

// Provider is the abstraction over any S2S backend.
//
// Implementations must be safe for concurrent use. The orchestrator may open
// multiple concurrent sessions, for example one per NPC in a scene.
type Provider interface {
	// Connect establishes a new S2S session with the given configuration.
	// The returned SessionHandle is ready to accept audio immediately.
	//
	// Returns an error if the session cannot be established (e.g., authentication
	// failure, invalid voice, or ctx already cancelled). The caller owns the
	// SessionHandle and is responsible for calling Close.
	Connect(ctx context.Context, cfg SessionConfig) (SessionHandle, error)

	// Capabilities returns static metadata about this provider's underlying model.
	// The result is assumed to be constant for the lifetime of the Provider instance.
	Capabilities() S2SCapabilities
}
