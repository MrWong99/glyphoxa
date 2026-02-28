// Package audio defines the interfaces and types for audio platform connectivity
// and stream management within Glyphoxa.
//
// The two primary abstractions are:
//
//   - [Platform] — connects to a voice channel and returns a [Connection].
//   - [Connection] — represents an active session on that channel, giving callers
//     per-participant input streams, a single mixed output stream, and lifecycle events.
//
// Implementations of these interfaces are provided by platform-specific adapter
// packages (e.g., audio/discord, audio/teamspeak). The interfaces are intentionally
// narrow to keep the orchestrator decoupled from provider details.
//
// This package lives under pkg/ because external code (third-party platform adapters)
// is expected to implement [Platform] and [Connection].
package audio

import (
	"context"
)

// EventType classifies participant lifecycle events emitted by a [Connection].
type EventType int

const (
	// EventJoin is emitted when a participant enters the voice channel.
	EventJoin EventType = iota

	// EventLeave is emitted when a participant leaves the voice channel.
	EventLeave
)

// String returns the human-readable name of the event type.
func (e EventType) String() string {
	switch e {
	case EventJoin:
		return "JOIN"
	case EventLeave:
		return "LEAVE"
	default:
		return "UNKNOWN"
	}
}

// Event describes a participant lifecycle change on a voice channel.
// Callbacks registered via [Connection.OnParticipantChange] receive values of this type.
type Event struct {
	// Type indicates whether the participant joined or left.
	Type EventType

	// UserID is the platform-specific unique identifier for the participant.
	UserID string

	// Username is the human-readable display name of the participant.
	Username string
}

// Connection represents an active session on a voice channel.
//
// A Connection is obtained by calling [Platform.Connect] and remains valid
// until [Connection.Disconnect] is called or the context used to create it
// is cancelled. All channels returned by [Connection] methods are closed
// automatically when the connection terminates.
//
// Implementations must be safe for concurrent use.
type Connection interface {
	// InputStreams returns a snapshot of the current per-participant audio channels.
	// The map key is the platform-specific participant ID; the value is a read-only
	// channel that delivers [AudioFrame] values as they arrive from that
	// participant. A new entry appears for each joining participant and is removed
	// (channel closed) when that participant leaves.
	//
	// Callers should call InputStreams again after receiving an [EventJoin] event to
	// pick up newly added channels.
	InputStreams() map[string]<-chan AudioFrame

	// OutputStream returns the single write-only channel for mixed NPC output.
	// Frames written here are mixed and sent to all channel participants.
	// The channel is buffered; writes must not block indefinitely.
	//
	// Ownership: The returned channel is owned by the caller (writer). The platform
	// does NOT close this channel on Disconnect — the caller is responsible for
	// stopping writes and optionally closing the channel. Writing to the channel
	// after Disconnect is called results in dropped frames (not a panic).
	OutputStream() chan<- AudioFrame

	// OnParticipantChange registers cb as the callback to invoke whenever a
	// participant joins or leaves the channel. Only one callback may be registered
	// at a time; subsequent calls replace the previous registration.
	// The callback is invoked on an internal goroutine — callers must not block.
	OnParticipantChange(cb func(Event))

	// Disconnect cleanly tears down the connection, drains pending frames, and
	// closes all channels. It is safe to call Disconnect more than once; subsequent
	// calls are no-ops and return nil.
	Disconnect() error
}

// Platform is the entry point for a voice-channel provider.
// Implementations wrap provider-specific SDKs (Discord, TeamSpeak, …) and
// expose a uniform [Connection] abstraction.
//
// Implementations must be safe for concurrent use.
type Platform interface {
	// Connect joins the voice channel identified by channelID and returns an active
	// [Connection]. The supplied ctx governs the lifetime of the connection attempt
	// only; once connected, the Connection remains alive until [Connection.Disconnect]
	// is called explicitly.
	//
	// Returns an error if the connection cannot be established (auth failure,
	// unknown channel, network error, etc.).
	Connect(ctx context.Context, channelID string) (Connection, error)
}
