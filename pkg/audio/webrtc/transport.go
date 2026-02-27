package webrtc

import (
	"context"

	"github.com/MrWong99/glyphoxa/pkg/audio"
)

// PeerTransport abstracts the WebRTC peer connection.
// This decouples the platform logic from the pion/webrtc dependency and
// allows testing without pion. The actual pion integration can be added
// later as a concrete PeerTransport implementation.
type PeerTransport interface {
	// CreateOffer creates an SDP offer for a new peer.
	CreateOffer(ctx context.Context) (sdpOffer string, err error)

	// AcceptAnswer processes the remote peer's SDP answer.
	AcceptAnswer(ctx context.Context, sdpAnswer string) error

	// AddICECandidate adds a remote ICE candidate.
	AddICECandidate(candidate string) error

	// AudioInput returns the channel delivering audio frames received from this peer.
	AudioInput() <-chan audio.AudioFrame

	// SendAudio sends an audio frame to this peer.
	SendAudio(frame audio.AudioFrame) error

	// Close tears down the peer connection and releases resources.
	Close() error
}

// mockTransport is a [PeerTransport] used for testing and as the default
// transport in the alpha implementation. It exposes channels that tests
// can write to (simulate peer audio input) and read from (verify sent frames).
type mockTransport struct {
	audioIn  chan audio.AudioFrame
	audioOut chan audio.AudioFrame
	closed   chan struct{}
}

func newMockTransport() *mockTransport {
	return &mockTransport{
		audioIn:  make(chan audio.AudioFrame, 16),
		audioOut: make(chan audio.AudioFrame, 16),
		closed:   make(chan struct{}),
	}
}

func (m *mockTransport) CreateOffer(_ context.Context) (string, error) {
	return "v=0\r\no=- 0 0 IN IP4 127.0.0.1\r\ns=WebRTC Audio\r\n", nil
}

func (m *mockTransport) AcceptAnswer(_ context.Context, _ string) error {
	return nil
}

func (m *mockTransport) AddICECandidate(_ string) error {
	return nil
}

func (m *mockTransport) AudioInput() <-chan audio.AudioFrame {
	return m.audioIn
}

func (m *mockTransport) SendAudio(frame audio.AudioFrame) error {
	select {
	case m.audioOut <- frame:
	case <-m.closed:
	}
	return nil
}

func (m *mockTransport) Close() error {
	select {
	case <-m.closed:
		// already closed; no-op
	default:
		close(m.closed)
	}
	return nil
}
