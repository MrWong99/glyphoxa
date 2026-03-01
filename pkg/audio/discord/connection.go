package discord

import (
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/MrWong99/glyphoxa/pkg/audio"
	"github.com/bwmarrin/discordgo"
)

// Compile-time interface assertion.
var _ audio.Connection = (*Connection)(nil)

const (
	inputChannelBuffer  = 64
	outputChannelBuffer = 64
)

// Connection wraps a discordgo.VoiceConnection and adapts it to the
// [audio.Connection] interface. It demuxes incoming Opus packets by SSRC
// into per-participant PCM input streams, and encodes outgoing PCM frames
// to Opus for transmission.
//
// Connection is safe for concurrent use.
type Connection struct {
	vc      *discordgo.VoiceConnection
	session *discordgo.Session
	guildID string

	inputsMu sync.RWMutex
	inputs   map[string]chan audio.AudioFrame // keyed by SSRC string
	ssrcUser map[uint32]string                // SSRC -> userID mapping

	output chan audio.AudioFrame

	changeCb func(audio.Event)
	changeMu sync.Mutex

	done      chan struct{}
	closeOnce sync.Once

	removeHandler func() // removes the VoiceStateUpdate handler

	// disconnectVC is called during Disconnect to tear down the voice connection.
	// Defaults to vc.Disconnect; overridden in tests.
	disconnectVC func() error
}

// newConnection initialises a Connection for an already-joined voice channel.
// It starts background goroutines for receiving and sending audio.
func newConnection(vc *discordgo.VoiceConnection, session *discordgo.Session, guildID string) (*Connection, error) {
	c := &Connection{
		vc:           vc,
		session:      session,
		guildID:      guildID,
		inputs:       make(map[string]chan audio.AudioFrame),
		ssrcUser:     make(map[uint32]string),
		output:       make(chan audio.AudioFrame, outputChannelBuffer),
		done:         make(chan struct{}),
		disconnectVC: vc.Disconnect,
	}

	// Register a VoiceStateUpdate handler to detect participant join/leave.
	c.removeHandler = session.AddHandler(c.handleVoiceStateUpdate)

	// Start the receive loop (reads Opus from Discord, demuxes by SSRC, decodes to PCM).
	go c.recvLoop()

	// Start the send loop (reads PCM from output channel, encodes to Opus, sends to Discord).
	go c.sendLoop()

	return c, nil
}

// InputStreams returns a snapshot of the current per-participant audio channels.
// The map key is the SSRC (as a string); the value is the read-only input channel.
func (c *Connection) InputStreams() map[string]<-chan audio.AudioFrame {
	c.inputsMu.RLock()
	defer c.inputsMu.RUnlock()
	snap := make(map[string]<-chan audio.AudioFrame, len(c.inputs))
	for id, ch := range c.inputs {
		snap[id] = ch
	}
	return snap
}

// OutputStream returns the write-only channel for NPC audio output.
// Frames written here are encoded to Opus and sent to Discord.
func (c *Connection) OutputStream() chan<- audio.AudioFrame {
	return c.output
}

// OnParticipantChange registers cb as the callback for participant join/leave events.
// Only one callback may be registered; subsequent calls replace the previous one.
func (c *Connection) OnParticipantChange(cb func(audio.Event)) {
	c.changeMu.Lock()
	defer c.changeMu.Unlock()
	c.changeCb = cb
}

// Disconnect cleanly tears down the voice connection and stops all background
// goroutines. It is safe to call more than once; subsequent calls return nil.
func (c *Connection) Disconnect() error {
	var err error
	c.closeOnce.Do(func() {
		close(c.done)

		if c.removeHandler != nil {
			c.removeHandler()
		}

		if c.disconnectVC != nil {
			err = c.disconnectVC()
		}

		// Close all input channels so downstream consumers see EOF.
		c.inputsMu.Lock()
		for id, ch := range c.inputs {
			close(ch)
			delete(c.inputs, id)
		}
		c.inputsMu.Unlock()
	})
	return err
}

// recvLoop reads Opus packets from the Discord voice connection, demuxes them
// by SSRC, decodes Opus to PCM, and delivers AudioFrames to per-participant channels.
func (c *Connection) recvLoop() {
	// Each SSRC gets its own decoder to maintain state across frames.
	decoders := make(map[uint32]*opusDecoder)

	for {
		select {
		case <-c.done:
			return
		case pkt, ok := <-c.vc.OpusRecv:
			if !ok {
				return
			}
			if pkt == nil {
				continue
			}

			ssrc := pkt.SSRC
			ssrcStr := strconv.FormatUint(uint64(ssrc), 10)

			// Lazily create a decoder for this SSRC.
			dec, exists := decoders[ssrc]
			if !exists {
				var err error
				dec, err = newOpusDecoder()
				if err != nil {
					slog.Error("discord: failed to create opus decoder", "ssrc", ssrcStr, "error", err)
					continue
				}
				decoders[ssrc] = dec
			}

			// Ensure an input channel exists for this SSRC.
			c.inputsMu.Lock()
			ch, chExists := c.inputs[ssrcStr]
			if !chExists {
				ch = make(chan audio.AudioFrame, inputChannelBuffer)
				c.inputs[ssrcStr] = ch
				c.ssrcUser[ssrc] = ssrcStr
			}
			c.inputsMu.Unlock()

			if !chExists {
				// Notify about a new participant (identified by SSRC for now).
				c.emitEvent(audio.Event{
					Type:   audio.EventJoin,
					UserID: ssrcStr,
				})
			}

			pcm, err := dec.decode(pkt.Opus)
			if err != nil {
				slog.Warn("discord: opus decode error", "ssrc", ssrcStr, "error", err)
				continue
			}

			frame := audio.AudioFrame{
				Data:       pcm,
				SampleRate: opusSampleRate,
				Channels:   opusChannels,
				Timestamp:  time.Duration(pkt.Timestamp) * time.Second / time.Duration(opusSampleRate),
			}

			select {
			case ch <- frame:
			default:
				// Channel full — drop frame rather than block.
			}
		}
	}
}

// sendLoop reads PCM AudioFrames from the output channel, converts them to
// Discord's target format (48 kHz stereo), extracts exact Opus frame-sized
// chunks, encodes them to Opus, and sends the encoded data via the Discord
// voice connection.
func (c *Connection) sendLoop() {
	enc, err := newOpusEncoder()
	if err != nil {
		slog.Error("discord: failed to create opus encoder", "error", err)
		return
	}

	conv := audio.FormatConverter{Target: audio.Format{SampleRate: opusSampleRate, Channels: opusChannels}}

	// Signal speaking when we start sending audio.
	speakingSet := false

	// opusFrameBytes is the exact PCM input size for one Opus frame:
	// 960 samples/channel × 2 channels × 2 bytes/sample = 3840 bytes.
	const opusFrameBytes = opusFrameSize * opusChannels * 2

	var buf []byte

	for {
		select {
		case <-c.done:
			if speakingSet {
				c.setSpeaking(false)
			}
			return
		case frame, ok := <-c.output:
			if !ok {
				return
			}

			if !speakingSet {
				c.setSpeaking(true)
				speakingSet = true
			}

			// Convert to Discord's target format (48 kHz stereo).
			frame = conv.Convert(frame)
			data := frame.Data

			buf = append(buf, data...)

			// Encode and send complete Opus frames.
			for len(buf) >= opusFrameBytes {
				opus, eErr := enc.encode(buf[:opusFrameBytes])
				if eErr != nil {
					slog.Warn("discord: opus encode error", "error", eErr)
					buf = buf[opusFrameBytes:]
					continue
				}
				buf = buf[opusFrameBytes:]

				select {
				case c.vc.OpusSend <- opus:
				case <-c.done:
					return
				}
			}
		}
	}
}

// handleVoiceStateUpdate processes Discord VoiceStateUpdate events to detect
// participant joins and leaves for the voice channel this connection is on.
func (c *Connection) handleVoiceStateUpdate(_ *discordgo.Session, vsu *discordgo.VoiceStateUpdate) {
	if vsu.GuildID != c.guildID {
		return
	}

	channelID := c.vc.ChannelID

	// Participant left our channel.
	if vsu.BeforeUpdate != nil && vsu.BeforeUpdate.ChannelID == channelID && vsu.ChannelID != channelID {
		username := ""
		if vsu.Member != nil && vsu.Member.User != nil {
			username = vsu.Member.User.Username
		}
		c.emitEvent(audio.Event{
			Type:     audio.EventLeave,
			UserID:   vsu.UserID,
			Username: username,
		})
		return
	}

	// Participant joined our channel.
	if vsu.ChannelID == channelID && (vsu.BeforeUpdate == nil || vsu.BeforeUpdate.ChannelID != channelID) {
		username := ""
		if vsu.Member != nil && vsu.Member.User != nil {
			username = vsu.Member.User.Username
		}
		c.emitEvent(audio.Event{
			Type:     audio.EventJoin,
			UserID:   vsu.UserID,
			Username: username,
		})
	}
}

// setSpeaking sends a speaking notification to Discord, logging any errors.
func (c *Connection) setSpeaking(b bool) {
	if err := c.vc.Speaking(b); err != nil {
		slog.Warn("discord: speaking notification error", "speaking", b, "error", err)
	}
}

// emitEvent safely invokes the registered participant change callback.
func (c *Connection) emitEvent(ev audio.Event) {
	c.changeMu.Lock()
	cb := c.changeCb
	c.changeMu.Unlock()
	if cb != nil {
		go cb(ev)
	}
}

// SSRCToUserID returns the user ID associated with the given SSRC, if known.
// This mapping is populated as audio packets arrive and VoiceStateUpdate events
// provide user identity. Returns an empty string if the SSRC is unknown.
func (c *Connection) SSRCToUserID(ssrc uint32) string {
	c.inputsMu.RLock()
	defer c.inputsMu.RUnlock()
	userID, ok := c.ssrcUser[ssrc]
	if !ok {
		return fmt.Sprintf("%d", ssrc)
	}
	return userID
}
