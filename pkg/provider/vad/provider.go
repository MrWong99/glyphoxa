// Package vad defines the Engine interface for Voice Activity Detection backends.
//
// A VAD engine wraps a frame-level speech detector (e.g., Silero VAD, WebRTC VAD,
// or a custom model) and surfaces it as a stateful, per-stream session. Each
// session maintains its own internal state (ring buffers, smoothing history) so
// that multiple concurrent audio streams can be processed independently.
//
// VAD is synchronous by design: ProcessFrame returns immediately with a detection
// result, making it suitable for low-latency pipeline stages that gate STT input.
//
// Implementations must be safe for concurrent use across different sessions.
// A single SessionHandle should not be shared across goroutines unless the
// implementation explicitly documents thread safety for that type.
package vad

// Config holds the parameters for a VAD session. All numeric thresholds are
// expressed in the model's native scale; see each Engine's documentation for
// recommended starting values.
type Config struct {
	// SampleRate is the audio sample rate in Hz. Must match the rate of the PCM
	// frames passed to ProcessFrame. Common values: 8000, 16000, 48000.
	SampleRate int

	// FrameSizeMs is the duration of each audio frame in milliseconds. Most VAD
	// models operate on fixed frame sizes (e.g., 10, 20, or 30 ms).
	// ProcessFrame will return an error if the supplied frame does not match this
	// size.
	FrameSizeMs int

	// SpeechThreshold is the probability above which a frame is classified as
	// speech. Range: [0.0, 1.0]. Higher values reduce false positives at the cost
	// of increased speech start latency. Typical: 0.5.
	SpeechThreshold float64

	// SilenceThreshold is the probability below which a frame is classified as
	// silence and an active speech segment is considered ended. Range: [0.0, 1.0].
	// Must be ≤ SpeechThreshold. Typical: 0.35.
	SilenceThreshold float64
}

// SessionHandle represents an active VAD session for a single audio stream. It is
// an interface so that test code can supply mock implementations without a live
// engine. Each session maintains its own detection state; Reset clears this state
// without closing the session.
//
// A SessionHandle should not be shared between goroutines unless the implementation
// explicitly guarantees concurrent safety.
type SessionHandle interface {
	// ProcessFrame analyses a single audio frame and returns the detection result.
	// The frame must be raw little-endian PCM at the SampleRate and FrameSizeMs
	// configured when the session was created. Returns an error if the frame size
	// is wrong or if the engine encounters an internal failure.
	//
	// This method is designed to be called synchronously in the audio pipeline loop;
	// it must not block.
	ProcessFrame(frame []byte) (VADEvent, error)

	// Reset clears all accumulated detection state (ring buffers, speech-start
	// counters) without closing the session. Use this when the audio stream is
	// interrupted or restarted to avoid stale state from the previous segment
	// affecting subsequent frames.
	Reset()

	// Close releases all resources associated with the session. After Close,
	// ProcessFrame and Reset must return errors or be no-ops. Calling Close more
	// than once is safe and returns nil.
	Close() error
}

// Engine is the factory for VAD sessions. It is the top-level interface
// implemented by each VAD backend.
//
// Implementations must be safe for concurrent use: multiple goroutines may call
// NewSession simultaneously to create independent sessions.
type Engine interface {
	// NewSession creates a new VAD session with the given configuration. The session
	// is immediately ready to accept audio frames.
	//
	// Returns an error if the configuration is invalid (e.g., unsupported sample
	// rate, frame size, or threshold out of range) or if the engine cannot allocate
	// resources for the session.
	NewSession(cfg Config) (SessionHandle, error)
}
