// Package tts defines the Provider interface for Text-to-Speech backends.
//
// A TTS provider wraps a speech synthesis service (e.g., ElevenLabs, Google
// Cloud TTS, or a local Piper instance) and presents a uniform streaming interface.
// The primary entry point is SynthesizeStream, which accepts a channel of text
// fragments and returns a channel of raw PCM audio bytes as they become available —
// enabling low-latency pipelining between the LLM output and the audio mixer.
//
// Implementations must be safe for concurrent use.
package tts

import (
	"context"

	"github.com/MrWong99/glyphoxa/pkg/types"
)

// Provider is the abstraction over any TTS backend.
//
// Implementations must be safe for concurrent use. Multiple synthesis requests may
// run in parallel (e.g., multiple NPC voices at once).
type Provider interface {
	// SynthesizeStream consumes text fragments from the text channel and returns a
	// channel that emits raw PCM audio byte slices as they are synthesised. This
	// design allows the caller to pipe LLM streaming output directly into synthesis
	// without waiting for the full text to be available.
	//
	// The returned audio channel is closed by the implementation when all text has
	// been synthesised or when ctx is cancelled. The caller must drain the audio
	// channel to avoid blocking the provider's internal goroutines.
	//
	// voice specifies the voice profile to use for synthesis. Providers should return
	// an error if the requested voice is not available.
	//
	// Returns a non-nil error only if the stream cannot be started. Errors
	// encountered during synthesis are signalled by closing the audio channel early;
	// callers should check ctx.Err() to distinguish cancellation from provider errors.
	SynthesizeStream(ctx context.Context, text <-chan string, voice types.VoiceProfile) (<-chan []byte, error)

	// ListVoices returns all voice profiles available from this provider. The list
	// reflects the provider's current catalogue and may change between calls if the
	// underlying service adds or removes voices.
	//
	// Returns an error if the provider cannot be reached or if ctx is cancelled
	// before the list is retrieved.
	ListVoices(ctx context.Context) ([]types.VoiceProfile, error)

	// CloneVoice creates a new voice profile by training on the supplied audio
	// samples. Each element of samples must be raw PCM or a provider-supported
	// encoded format (e.g., WAV, MP3 — consult the implementation).
	//
	// This is an expensive operation and should not be called in the hot path.
	// Returns a pointer to the newly created VoiceProfile (with a provider-assigned
	// ID) or an error if cloning fails. A nil samples slice or an empty slice should
	// return an error rather than panic.
	CloneVoice(ctx context.Context, samples [][]byte) (*types.VoiceProfile, error)
}
