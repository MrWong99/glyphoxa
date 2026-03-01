// Package discord provides the Discord bot layer for Glyphoxa. It owns
// the discordgo.Session lifecycle, routes slash command interactions to
// registered handlers, and checks DM role permissions.
package discord

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/bwmarrin/discordgo"

	"github.com/MrWong99/glyphoxa/pkg/audio"
	discordaudio "github.com/MrWong99/glyphoxa/pkg/audio/discord"
)

// Config holds Discord bot configuration.
type Config struct {
	// Token is the Discord bot token (e.g., "Bot MTIz...").
	Token string `yaml:"token"`

	// GuildID is the target guild (single-guild for alpha).
	GuildID string `yaml:"guild_id"`

	// DMRoleID is the Discord role ID that identifies Dungeon Masters.
	DMRoleID string `yaml:"dm_role_id"`
}

// Bot owns the Discord gateway connection and routes interactions
// to registered command handlers.
type Bot struct {
	mu        sync.RWMutex
	session   *discordgo.Session
	platform  *discordaudio.Platform
	router    *CommandRouter
	perms     *PermissionChecker
	guildID   string
	commands  []*discordgo.ApplicationCommand
	done      chan struct{}
	closeOnce sync.Once
}

// New creates a Bot, connects to Discord, and registers the interaction handler.
func New(_ context.Context, cfg Config) (*Bot, error) {
	session, err := discordgo.New("Bot " + cfg.Token)
	if err != nil {
		return nil, fmt.Errorf("discord: create session: %w", err)
	}

	session.Identify.Intents = discordgo.IntentsGuildMessages |
		discordgo.IntentsGuildVoiceStates |
		discordgo.IntentsGuilds

	if err := session.Open(); err != nil {
		return nil, fmt.Errorf("discord: open session: %w", err)
	}

	platform := discordaudio.New(session, cfg.GuildID)
	router := NewCommandRouter()
	perms := NewPermissionChecker(cfg.DMRoleID)

	b := &Bot{
		session:  session,
		platform: platform,
		router:   router,
		perms:    perms,
		guildID:  cfg.GuildID,
		done:     make(chan struct{}),
	}

	session.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		b.router.Handle(s, i)
	})

	return b, nil
}

// Platform returns the audio.Platform for voice channel connections.
func (b *Bot) Platform() audio.Platform {
	return b.platform
}

// GuildID returns the target guild ID.
func (b *Bot) GuildID() string {
	return b.guildID
}

// Session returns the underlying discordgo session. Used by subsystems
// that need direct Discord API access (e.g., dashboard embed updates).
func (b *Bot) Session() *discordgo.Session {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.session
}

// Router returns the command router for registering handlers.
func (b *Bot) Router() *CommandRouter {
	return b.router
}

// Permissions returns the permission checker.
func (b *Bot) Permissions() *PermissionChecker {
	return b.perms
}

// Run registers slash commands with the Discord API and blocks until
// ctx is cancelled.
func (b *Bot) Run(ctx context.Context) error {
	b.mu.RLock()
	appID := b.session.State.User.ID
	b.mu.RUnlock()

	cmds := b.router.ApplicationCommands()
	if len(cmds) > 0 {
		registered, err := b.session.ApplicationCommandBulkOverwrite(appID, b.guildID, cmds)
		if err != nil {
			return fmt.Errorf("discord: register commands: %w", err)
		}
		b.mu.Lock()
		b.commands = registered
		b.mu.Unlock()
		slog.Info("discord commands registered", "count", len(registered))
	}

	<-ctx.Done()
	return ctx.Err()
}

// Close disconnects from Discord and unregisters commands.
func (b *Bot) Close() error {
	var closeErr error
	b.closeOnce.Do(func() {
		b.mu.Lock()
		defer b.mu.Unlock()

		// Unregister commands.
		if b.session != nil && len(b.commands) > 0 {
			appID := b.session.State.User.ID
			for _, cmd := range b.commands {
				if err := b.session.ApplicationCommandDelete(appID, b.guildID, cmd.ID); err != nil {
					slog.Warn("discord: failed to delete command", "name", cmd.Name, "err", err)
				}
			}
		}

		// Close session.
		if b.session != nil {
			if err := b.session.Close(); err != nil {
				closeErr = fmt.Errorf("discord: close session: %w", err)
			}
		}

		slog.Info("discord bot closed")
	})
	return closeErr
}
