package audio

import (
	"sync/atomic"
	"time"
)

// InterruptReason identifies why the current audio segment was cut short.
// It is passed to [Mixer.Interrupt] so that the mixer can apply
// reason-specific behaviour (e.g., fade-out vs. hard cut).
type InterruptReason int

const (
	// DMOverride indicates that the game-master or orchestrator forcibly
	// stopped playback, typically to inject a priority narrative event.
	DMOverride InterruptReason = iota

	// PlayerBargeIn indicates that a player started speaking while the NPC
	// was still talking. The mixer should honour conversational turn-taking
	// semantics and yield the floor to the player.
	PlayerBargeIn
)

// String returns the human-readable name of the interrupt reason.
func (r InterruptReason) String() string {
	switch r {
	case DMOverride:
		return "DM_OVERRIDE"
	case PlayerBargeIn:
		return "PLAYER_BARGE_IN"
	default:
		return "UNKNOWN"
	}
}

// AudioSegment is the unit of NPC speech submitted to a [Mixer].
// Audio is streamed — frames arrive incrementally on the Audio channel —
// so the mixer can begin playback before encoding is complete.
type AudioSegment struct {
	// NPCID identifies the NPC whose voice this segment belongs to.
	NPCID string

	// Audio is a read-only channel of raw audio bytes (e.g., Opus packets or
	// PCM chunks). The channel is closed by the producer when the segment ends
	// or when a mid-stream error occurs. After the channel closes, call
	// [AudioSegment.Err] to check whether synthesis completed cleanly.
	Audio <-chan []byte

	// SampleRate is the sample rate in Hz of the PCM data on the Audio channel
	// (e.g., 22050, 44100, 48000). Must be > 0.
	SampleRate int

	// Channels is the number of audio channels (1 = mono, 2 = stereo).
	// Must be > 0.
	Channels int

	// Priority controls scheduling when multiple segments are queued.
	// Higher values preempt lower ones. Equal-priority segments are played
	// in FIFO order.
	Priority int

	// streamErr stores the error that caused the Audio channel to close early.
	// Access via Err and SetStreamErr.
	streamErr atomic.Pointer[error]
}

// Err returns the error that caused the Audio channel to close prematurely,
// or nil if the stream completed successfully. Callers should check Err after
// the Audio channel is closed.
func (s *AudioSegment) Err() error {
	if p := s.streamErr.Load(); p != nil {
		return *p
	}
	return nil
}

// SetStreamErr records a mid-stream error. The producer should call this
// before closing the Audio channel so that the [Mixer] can distinguish a
// clean completion from a failure.
func (s *AudioSegment) SetStreamErr(err error) {
	s.streamErr.Store(&err)
}

// Mixer manages the NPC audio output queue and arbitrates between competing
// voices. It sits between the NPC agents and the [Connection.OutputStream],
// ensuring that only one NPC speaks at a time, that high-priority speech can
// interrupt lower-priority speech, and that barge-in from players is detected
// and surfaced to the orchestrator.
//
// Implementations must be safe for concurrent use.
type Mixer interface {
	// Enqueue schedules segment for playback. The priority parameter overrides
	// the priority embedded in segment.Priority, allowing call-site context to
	// elevate or demote a segment without mutating the struct.
	//
	// If a higher-priority segment is already playing, the new segment is
	// buffered; if the new segment has higher priority than the current one,
	// the current segment is interrupted with [DMOverride] semantics.
	Enqueue(segment *AudioSegment, priority int)

	// Interrupt immediately stops the currently playing segment for the given
	// reason and advances to the next queued segment (if any). If nothing is
	// playing, Interrupt is a no-op.
	Interrupt(reason InterruptReason)

	// OnBargeIn registers handler as the callback to invoke when voice-activity
	// detection determines that a player has started speaking while an NPC is
	// playing. speakerID is the platform participant ID of the interrupting player.
	//
	// Only one handler may be registered at a time; subsequent calls replace
	// the previous registration. The handler is invoked on an internal goroutine
	// and must not block.
	OnBargeIn(handler func(speakerID string))

	// SetGap configures the minimum silence duration inserted between consecutive
	// segments. A gap of zero means segments are played back-to-back.
	// Changes take effect before the next segment starts.
	SetGap(d time.Duration)
}
