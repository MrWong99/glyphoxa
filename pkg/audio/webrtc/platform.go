// Package webrtc provides an [audio.Platform] implementation backed by
// WebRTC via pion/webrtc. It enables browser-based voice sessions without
// requiring Discord or any other third-party voice platform.
//
// The platform runs a signaling server that accepts WebRTC peer connections.
// Each connected peer maps to a participant with a dedicated input audio
// stream and access to the shared NPC output stream.
//
// This is an alpha implementation that abstracts WebRTC peer connection
// handling behind the [PeerTransport] interface. The actual pion/webrtc
// integration can be added later as a concrete PeerTransport.
package webrtc

import (
	"context"

	"github.com/MrWong99/glyphoxa/pkg/audio"
)

// Compile-time interface assertions.
var _ audio.Platform = (*Platform)(nil)
var _ audio.Connection = (*Connection)(nil)

// Option configures a [Platform].
type Option func(*Platform)

// WithSTUNServers sets the STUN server URLs used during ICE negotiation.
// Defaults to ["stun:stun.l.google.com:19302"].
func WithSTUNServers(servers ...string) Option {
	return func(p *Platform) {
		p.stunServers = servers
	}
}

// WithSampleRate sets the audio sample rate in Hz. Defaults to 48000.
func WithSampleRate(rate int) Option {
	return func(p *Platform) {
		p.sampleRate = rate
	}
}

// Platform implements [audio.Platform] using WebRTC as the transport layer.
// Each call to [Platform.Connect] returns a new [Connection] that manages WebRTC
// peer connections for the specified room (channel ID). Multiple calls with the
// same channelID each produce an independent Connection.
//
// Platform is safe for concurrent use.
type Platform struct {
	stunServers []string // STUN server URLs for ICE negotiation; immutable after New
	sampleRate  int      // audio sample rate in Hz; immutable after New
}

// New creates a new WebRTC Platform with the given options applied.
func New(opts ...Option) *Platform {
	p := &Platform{
		stunServers: []string{"stun:stun.l.google.com:19302"},
		sampleRate:  48000,
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

// Connect creates a new [Connection] for the room identified by channelID.
// The supplied ctx governs the connection-setup phase only; once the Connection
// is returned it lives until [Connection.Disconnect] is called explicitly.
func (p *Platform) Connect(_ context.Context, channelID string) (audio.Connection, error) {
	return newConnection(channelID, p.sampleRate, p.stunServers), nil
}
