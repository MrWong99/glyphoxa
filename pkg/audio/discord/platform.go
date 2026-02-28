// Package discord provides an [audio.Platform] implementation backed by
// Discord voice channels via the bwmarrin/discordgo library. It bridges
// Discord's Opus-based voice transport with Glyphoxa's PCM [audio.AudioFrame]
// pipeline.
//
// The platform requires an active *discordgo.Session (owned by the bot layer)
// and a guild ID. Each call to [Platform.Connect] joins the specified voice
// channel and returns a [Connection] that demuxes per-participant audio input
// and muxes NPC audio output.
package discord

import (
	"context"
	"fmt"

	"github.com/MrWong99/glyphoxa/pkg/audio"
	"github.com/bwmarrin/discordgo"
)

// Compile-time interface assertion.
var _ audio.Platform = (*Platform)(nil)

// Platform implements [audio.Platform] using a discordgo voice connection.
// It requires an active *discordgo.Session (owned by the bot layer).
//
// Platform is safe for concurrent use.
type Platform struct {
	session *discordgo.Session
	guildID string
}

// New creates a new Discord Platform for the given session and guild.
func New(session *discordgo.Session, guildID string) *Platform {
	return &Platform{
		session: session,
		guildID: guildID,
	}
}

// Connect joins the voice channel identified by channelID and returns an active
// [audio.Connection]. The supplied ctx governs the connection-setup phase only;
// once the Connection is returned it lives until [Connection.Disconnect] is called.
func (p *Platform) Connect(ctx context.Context, channelID string) (audio.Connection, error) {
	// Join the voice channel: mute=false (we send audio), deaf=false (we receive audio).
	vc, err := p.session.ChannelVoiceJoin(p.guildID, channelID, false, false)
	if err != nil {
		return nil, fmt.Errorf("discord: join voice channel %q: %w", channelID, err)
	}

	conn, err := newConnection(vc, p.session, p.guildID)
	if err != nil {
		_ = vc.Disconnect()
		return nil, fmt.Errorf("discord: create connection: %w", err)
	}
	return conn, nil
}
