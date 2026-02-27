package webrtc

import (
	"context"
	"fmt"
	"sync"

	"github.com/MrWong99/glyphoxa/pkg/audio"
)

const outputChannelBuffer = 64

// peer holds the runtime state for a single connected WebRTC peer.
type peer struct {
	userID    string
	username  string
	transport PeerTransport
	inputCh   chan audio.AudioFrame
	done      chan struct{} // closed by RemovePeer/Disconnect to signal goroutines
}

// Connection manages WebRTC peer connections for a single room (channel).
// It implements [audio.Connection].
//
// Connection is safe for concurrent use.
type Connection struct {
	channelID   string
	sampleRate  int
	stunServers []string

	mu           sync.RWMutex
	peers        map[string]*peer
	inputStreams map[string]chan audio.AudioFrame
	outputCh     chan audio.AudioFrame
	onChange     func(audio.Event)
	disconnected bool

	ctx          context.Context
	cancel       context.CancelFunc
	newTransport func(userID string) PeerTransport // injectable; defaults to mockTransport
}

func newConnection(channelID string, sampleRate int, stunServers []string) *Connection {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Connection{
		channelID:    channelID,
		sampleRate:   sampleRate,
		stunServers:  stunServers,
		peers:        make(map[string]*peer),
		inputStreams: make(map[string]chan audio.AudioFrame),
		outputCh:     make(chan audio.AudioFrame, outputChannelBuffer),
		ctx:          ctx,
		cancel:       cancel,
		newTransport: func(_ string) PeerTransport {
			return newMockTransport()
		},
	}
	go c.forwardOutput()
	return c
}

// InputStreams returns a consistent snapshot of the per-participant audio channels.
// The map key is the participant ID; the value is the read-only input channel.
//
// Callers should call InputStreams again after receiving an [audio.EventJoin] event
// to pick up newly added channels.
func (c *Connection) InputStreams() map[string]<-chan audio.AudioFrame {
	c.mu.RLock()
	defer c.mu.RUnlock()
	snap := make(map[string]<-chan audio.AudioFrame, len(c.inputStreams))
	for id, ch := range c.inputStreams {
		snap[id] = ch
	}
	return snap
}

// OutputStream returns the write-only channel for NPC audio output.
// Frames written here are forwarded to all currently connected peers.
func (c *Connection) OutputStream() chan<- audio.AudioFrame {
	return c.outputCh
}

// OnParticipantChange registers cb as the participant lifecycle callback.
// Subsequent calls replace the previous registration.
// The callback is invoked on an internal goroutine — callers must not block.
func (c *Connection) OnParticipantChange(cb func(audio.Event)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onChange = cb
}

// Disconnect cleanly tears down all peer connections and stops internal
// goroutines. It is safe to call more than once; subsequent calls return nil.
func (c *Connection) Disconnect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.disconnected {
		return nil
	}
	c.disconnected = true

	// Cancel the context to stop forwardOutput and all readPeerInput goroutines.
	c.cancel()

	// Signal each peer's goroutine to stop and release the transport.
	for userID, p := range c.peers {
		close(p.done)
		_ = p.transport.Close()
		delete(c.peers, userID)
		delete(c.inputStreams, userID)
	}
	return nil
}

// AddPeer registers a new WebRTC peer for this connection. In a full pion
// implementation this would be called by the signaling handler after the
// WebRTC handshake completes. For this alpha it is a public method for testing.
//
// Returns the read-only input channel for audio arriving from this peer,
// or an error if the connection is disconnected or the peer already exists.
func (c *Connection) AddPeer(userID, username string) (<-chan audio.AudioFrame, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.disconnected {
		return nil, fmt.Errorf("webrtc: connection %q is disconnected", c.channelID)
	}
	if _, exists := c.peers[userID]; exists {
		return nil, fmt.Errorf("webrtc: peer %q is already connected in room %q", userID, c.channelID)
	}

	transport := c.newTransport(userID)
	inputCh := make(chan audio.AudioFrame, 64)
	p := &peer{
		userID:    userID,
		username:  username,
		transport: transport,
		inputCh:   inputCh,
		done:      make(chan struct{}),
	}
	c.peers[userID] = p
	c.inputStreams[userID] = inputCh

	go c.readPeerInput(p)

	if cb := c.onChange; cb != nil {
		go cb(audio.Event{Type: audio.EventJoin, UserID: userID, Username: username})
	}
	return inputCh, nil
}

// RemovePeer disconnects and removes the peer identified by userID.
func (c *Connection) RemovePeer(userID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.disconnected {
		return fmt.Errorf("webrtc: connection %q is disconnected", c.channelID)
	}
	p, exists := c.peers[userID]
	if !exists {
		return fmt.Errorf("webrtc: peer %q not found in room %q", userID, c.channelID)
	}

	// Signal the readPeerInput goroutine to stop (it closes inputCh via defer).
	close(p.done)
	_ = p.transport.Close()
	delete(c.peers, userID)
	delete(c.inputStreams, userID)

	if cb := c.onChange; cb != nil {
		username := p.username
		go cb(audio.Event{Type: audio.EventLeave, UserID: userID, Username: username})
	}
	return nil
}

// readPeerInput reads audio frames from the peer's transport and forwards them
// to the peer's inputCh until the peer is removed or the connection is closed.
// It closes inputCh on exit to signal any downstream consumer.
func (c *Connection) readPeerInput(p *peer) {
	defer close(p.inputCh)
	audioIn := p.transport.AudioInput()
	for {
		select {
		case <-p.done:
			return
		case <-c.ctx.Done():
			return
		case frame, ok := <-audioIn:
			if !ok {
				return
			}
			select {
			case p.inputCh <- frame:
			case <-p.done:
				return
			case <-c.ctx.Done():
				return
			}
		}
	}
}

// forwardOutput reads NPC audio frames from the output channel and sends them
// to all currently connected peers via their transports.
func (c *Connection) forwardOutput() {
	for {
		select {
		case <-c.ctx.Done():
			return
		case frame, ok := <-c.outputCh:
			if !ok {
				return
			}
			// Snapshot peers under read lock to minimise contention.
			c.mu.RLock()
			peers := make([]*peer, 0, len(c.peers))
			for _, p := range c.peers {
				peers = append(peers, p)
			}
			c.mu.RUnlock()

			for _, p := range peers {
				_ = p.transport.SendAudio(frame)
			}
		}
	}
}
