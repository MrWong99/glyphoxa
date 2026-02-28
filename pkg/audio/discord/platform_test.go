package discord

import (
	"sync"
	"testing"
	"time"

	"github.com/MrWong99/glyphoxa/pkg/audio"
	"github.com/bwmarrin/discordgo"
)

// ─── compile-time interface assertions ───────────────────────────────────────

var _ audio.Platform = (*Platform)(nil)
var _ audio.Connection = (*Connection)(nil)

// ─── test helpers ─────────────────────────────────────────────────────────────

// newTestConnection creates a Connection suitable for unit testing without
// a real Discord voice connection. It wires up fake OpusSend/OpusRecv channels.
func newTestConnection(t *testing.T) *Connection {
	t.Helper()
	vc := &discordgo.VoiceConnection{
		OpusSend: make(chan []byte, 16),
		OpusRecv: make(chan *discordgo.Packet, 16),
	}
	c := &Connection{
		vc:           vc,
		session:      &discordgo.Session{},
		guildID:      "guild-test",
		inputs:       make(map[string]chan audio.AudioFrame),
		ssrcUser:     make(map[uint32]string),
		output:       make(chan audio.AudioFrame, outputChannelBuffer),
		done:         make(chan struct{}),
		disconnectVC: func() error { return nil }, // no-op for tests
	}
	// Start loops like the real constructor (but without registering the handler
	// since session has no websocket).
	go c.recvLoop()
	go c.sendLoop()
	t.Cleanup(func() { _ = c.Disconnect() })
	return c
}

// ─── Platform tests ──────────────────────────────────────────────────────────

// TestNewPlatform verifies that New creates a Platform with the expected fields.
func TestNewPlatform(t *testing.T) {
	t.Parallel()

	s := &discordgo.Session{}
	p := New(s, "guild-123")
	if p == nil {
		t.Fatal("New returned nil")
	}
	if p.session != s {
		t.Error("session not stored correctly")
	}
	if p.guildID != "guild-123" {
		t.Errorf("guildID = %q, want %q", p.guildID, "guild-123")
	}
}

// ─── Connection tests ─────────────────────────────────────────────────────────

// TestConnection_DisconnectIdempotent verifies that Disconnect can be called
// multiple times without panicking and returns nil on subsequent calls.
func TestConnection_DisconnectIdempotent(t *testing.T) {
	t.Parallel()

	c := newTestConnection(t)
	for i := range 3 {
		err := c.Disconnect()
		// First call may return an error from the fake vc.Disconnect()
		// (which is expected since there's no real connection).
		// Subsequent calls must return nil (no-op).
		if i > 0 && err != nil {
			t.Fatalf("Disconnect[%d]: unexpected error: %v", i, err)
		}
	}
}

// TestConnection_InputStreamsEmpty verifies that InputStreams returns an empty
// map when no participants have sent audio.
func TestConnection_InputStreamsEmpty(t *testing.T) {
	t.Parallel()

	c := newTestConnection(t)
	streams := c.InputStreams()
	if streams == nil {
		t.Fatal("InputStreams returned nil")
	}
	if len(streams) != 0 {
		t.Errorf("InputStreams: want 0 entries, got %d", len(streams))
	}
}

// TestConnection_OutputStreamNotNil verifies that OutputStream returns a
// non-nil channel.
func TestConnection_OutputStreamNotNil(t *testing.T) {
	t.Parallel()

	c := newTestConnection(t)
	ch := c.OutputStream()
	if ch == nil {
		t.Fatal("OutputStream returned nil")
	}
}

// TestConnection_OnParticipantChangeRegisters verifies that a callback can
// be registered and replaced.
func TestConnection_OnParticipantChangeRegisters(t *testing.T) {
	t.Parallel()

	c := newTestConnection(t)

	called := make(chan audio.Event, 4)
	c.OnParticipantChange(func(ev audio.Event) {
		called <- ev
	})

	// Emit an event manually and verify callback is invoked.
	c.emitEvent(audio.Event{Type: audio.EventJoin, UserID: "test-user", Username: "Alice"})

	select {
	case ev := <-called:
		if ev.Type != audio.EventJoin {
			t.Errorf("event type = %v, want EventJoin", ev.Type)
		}
		if ev.UserID != "test-user" {
			t.Errorf("event UserID = %q, want %q", ev.UserID, "test-user")
		}
		if ev.Username != "Alice" {
			t.Errorf("event Username = %q, want %q", ev.Username, "Alice")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for participant change event")
	}

	// Replace the callback.
	called2 := make(chan audio.Event, 4)
	c.OnParticipantChange(func(ev audio.Event) {
		called2 <- ev
	})
	c.emitEvent(audio.Event{Type: audio.EventLeave, UserID: "test-user"})

	select {
	case ev := <-called2:
		if ev.Type != audio.EventLeave {
			t.Errorf("replaced callback: event type = %v, want EventLeave", ev.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event on replaced callback")
	}

	// Original callback should NOT receive the second event.
	select {
	case ev := <-called:
		t.Errorf("original callback should not receive events after replacement, got %v", ev)
	case <-time.After(50 * time.Millisecond):
		// expected
	}
}

// TestConnection_RecvDemux verifies that incoming Opus packets are demuxed
// by SSRC and appear on separate input streams.
func TestConnection_RecvDemux(t *testing.T) {
	t.Parallel()

	c := newTestConnection(t)

	// Create a valid Opus silence frame for decoding.
	// Opus silence frame: 0xF8 0xFF 0xFE (3 bytes).
	silenceOpus := []byte{0xF8, 0xFF, 0xFE}

	// Send packets from two different SSRCs.
	c.vc.OpusRecv <- &discordgo.Packet{SSRC: 100, Opus: silenceOpus}
	c.vc.OpusRecv <- &discordgo.Packet{SSRC: 200, Opus: silenceOpus}

	// Wait a bit for the recvLoop to process.
	time.Sleep(100 * time.Millisecond)

	streams := c.InputStreams()
	if len(streams) != 2 {
		t.Fatalf("InputStreams: want 2 entries, got %d", len(streams))
	}
	if _, ok := streams["100"]; !ok {
		t.Error("InputStreams: missing SSRC 100")
	}
	if _, ok := streams["200"]; !ok {
		t.Error("InputStreams: missing SSRC 200")
	}

	// Drain a frame from each stream.
	for ssrc, ch := range streams {
		select {
		case frame := <-ch:
			if frame.SampleRate != opusSampleRate {
				t.Errorf("SSRC %s: SampleRate = %d, want %d", ssrc, frame.SampleRate, opusSampleRate)
			}
			if frame.Channels != opusChannels {
				t.Errorf("SSRC %s: Channels = %d, want %d", ssrc, frame.Channels, opusChannels)
			}
			if len(frame.Data) == 0 {
				t.Errorf("SSRC %s: frame data is empty", ssrc)
			}
		case <-time.After(time.Second):
			t.Fatalf("SSRC %s: timed out waiting for frame", ssrc)
		}
	}
}

// TestConnection_SendEncodes verifies that frames written to OutputStream
// are encoded and appear on OpusSend.
func TestConnection_SendEncodes(t *testing.T) {
	t.Parallel()

	c := newTestConnection(t)

	// Create a PCM frame of the right size for 20ms stereo 48kHz.
	// 960 samples * 2 channels * 2 bytes/sample = 3840 bytes.
	pcmSize := opusFrameSize * opusChannels * 2
	pcm := make([]byte, pcmSize)
	frame := audio.AudioFrame{
		Data:       pcm,
		SampleRate: opusSampleRate,
		Channels:   opusChannels,
	}

	c.OutputStream() <- frame

	select {
	case opus := <-c.vc.OpusSend:
		if len(opus) == 0 {
			t.Error("OpusSend: received empty Opus packet")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Opus packet on OpusSend")
	}
}

// TestConnection_ConcurrentDisconnect exercises Disconnect from multiple
// goroutines to verify thread safety (run with -race).
func TestConnection_ConcurrentDisconnect(t *testing.T) {
	t.Parallel()

	c := newTestConnection(t)
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			_ = c.Disconnect()
		})
	}
	wg.Wait()
}
