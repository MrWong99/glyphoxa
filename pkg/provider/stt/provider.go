// Package stt defines the Provider interface for Speech-to-Text backends.
//
// An STT provider wraps a real-time transcription service (e.g., Deepgram, Google
// Speech-to-Text, or a local Whisper server) and exposes a uniform streaming
// interface. The central abstraction is SessionHandle: once opened, a session
// accepts raw PCM audio frames and emits two streams of Transcript values —
// low-latency partials for responsiveness and authoritative finals for the session
// log.
//
// Implementations must be safe for concurrent use. Audio input and transcript
// output channels are goroutine-safe by construction.
package stt

import (
	"context"

	"github.com/MrWong99/glyphoxa/pkg/types"
)

// StreamConfig describes the audio format and recognition hints for a new STT
// session. All fields must be compatible with what the underlying provider supports;
// see each provider's documentation for valid ranges.
type StreamConfig struct {
	// SampleRate is the audio sample rate in Hz. Common values: 16000 (STT-optimised
	// mono), 48000 (Discord Opus decode output).
	SampleRate int

	// Channels is the number of audio channels. 1 = mono (required by most STT
	// providers). Implementors may downmix stereo internally.
	Channels int

	// Language is the BCP-47 language tag for recognition (e.g., "en-US", "de-DE").
	// An empty string lets the provider auto-detect the language, if supported.
	Language string

	// Keywords is a list of vocabulary hints that increase recognition probability
	// for uncommon words such as fantasy proper nouns. See types.KeywordBoost for
	// the boost intensity semantics.
	Keywords []types.KeywordBoost
}

// SessionHandle represents an open STT streaming session. It is an interface so
// that test code can provide mock implementations without requiring a live provider
// connection.
//
// Callers must call Close when the session is no longer needed. Failing to do so
// may leak goroutines and network connections inside the provider implementation.
// All methods must be safe for concurrent use.
type SessionHandle interface {
	// SendAudio delivers a chunk of raw PCM audio bytes to the provider for
	// transcription. The chunk should match the SampleRate, Channels, and bit-depth
	// agreed in StreamConfig. Calling SendAudio after Close returns an error.
	SendAudio(chunk []byte) error

	// Partials returns a read-only channel that emits low-latency interim Transcript
	// values as the provider makes preliminary guesses. These are suitable for
	// driving UI indicators but must not be written to the authoritative session log.
	// The channel is closed when the session ends.
	Partials() <-chan types.Transcript

	// Finals returns a read-only channel that emits authoritative Transcript values
	// once the provider has committed to a recognition result. These are the values
	// that should be stored in the session log and passed to the LLM.
	// The channel is closed when the session ends.
	Finals() <-chan types.Transcript

	// SetKeywords replaces the active keyword boost list without restarting the
	// session. Providers that do not support mid-session keyword updates may return
	// ErrNotSupported. Changes take effect on a best-effort basis; already-buffered
	// audio frames may still use the previous keyword set.
	SetKeywords(keywords []types.KeywordBoost) error

	// Close terminates the session, flushes any pending audio, and releases all
	// associated resources. After Close returns, the Partials and Finals channels
	// will be closed. Calling Close more than once is safe and returns nil.
	Close() error
}

// Provider is the abstraction over any STT backend.
//
// Implementations must be safe for concurrent use. Multiple sessions may be open
// simultaneously (e.g., one per player in a multiplayer session).
type Provider interface {
	// StartStream opens a new streaming transcription session with the given audio
	// format and recognition configuration. The returned SessionHandle is ready to
	// accept audio immediately.
	//
	// Returns an error if the provider cannot establish the session (e.g.,
	// authentication failure, unsupported configuration, or ctx already cancelled).
	// The caller owns the SessionHandle and must call Close when done.
	StartStream(ctx context.Context, cfg StreamConfig) (SessionHandle, error)
}
