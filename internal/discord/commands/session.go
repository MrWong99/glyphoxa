// Package commands implements Discord slash command handlers for Glyphoxa.
package commands

import (
	"context"
	"fmt"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/MrWong99/glyphoxa/internal/app"
	"github.com/MrWong99/glyphoxa/internal/discord"
)

// SessionCommands holds the dependencies for /session slash commands.
type SessionCommands struct {
	sessionMgr *app.SessionManager
	perms      *discord.PermissionChecker
	bot        *discord.Bot
}

// NewSessionCommands creates a SessionCommands and registers its handlers
// with the bot's router.
func NewSessionCommands(bot *discord.Bot, sessionMgr *app.SessionManager, perms *discord.PermissionChecker) *SessionCommands {
	sc := &SessionCommands{
		sessionMgr: sessionMgr,
		perms:      perms,
		bot:        bot,
	}
	sc.Register(bot.Router())
	return sc
}

// Register registers the /session command group with the router.
func (sc *SessionCommands) Register(router *discord.CommandRouter) {
	def := sc.Definition()
	router.RegisterCommand("session", def, func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		// This handler is for the top-level command; subcommands are routed below.
		discord.RespondEphemeral(s, i, "Please use a subcommand: `/session start` or `/session stop`.")
	})
	router.RegisterHandler("session/start", sc.handleStart)
	router.RegisterHandler("session/stop", sc.handleStop)
}

// Definition returns the ApplicationCommand definition for Discord.
func (sc *SessionCommands) Definition() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "session",
		Description: "Manage voice sessions",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "start",
				Description: "Start a voice session in your current voice channel",
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "stop",
				Description: "Stop the active voice session",
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "recap",
				Description: "Show a recap of the current or most recent session",
			},
		},
	}
}

// handleStart handles /session start.
func (sc *SessionCommands) handleStart(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Check DM role.
	if !sc.perms.IsDM(i) {
		discord.RespondEphemeral(s, i, "You need the DM role to start a session.")
		return
	}

	// Check that the user is in a voice channel.
	guildID := sc.bot.GuildID()
	userID := interactionUserID(i)
	vs, err := s.State.VoiceState(guildID, userID)
	if err != nil || vs == nil || vs.ChannelID == "" {
		discord.RespondEphemeral(s, i, "You must be in a voice channel to start a session.")
		return
	}

	// Check no session already active.
	if sc.sessionMgr.IsActive() {
		info := sc.sessionMgr.Info()
		discord.RespondEphemeral(s, i, fmt.Sprintf("A session is already active (ID: `%s`).", info.SessionID))
		return
	}

	// Defer reply since connecting may take a moment.
	discord.DeferReply(s, i)

	// Start the session.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := sc.sessionMgr.Start(ctx, vs.ChannelID, userID); err != nil {
		discord.FollowUp(s, i, fmt.Sprintf("Failed to start session: %v", err))
		return
	}

	info := sc.sessionMgr.Info()
	discord.FollowUp(s, i, fmt.Sprintf(
		"Session started!\n**Session ID:** `%s`\n**Campaign:** %s\n**Channel:** <#%s>",
		info.SessionID,
		info.CampaignName,
		info.ChannelID,
	))
}

// handleStop handles /session stop.
func (sc *SessionCommands) handleStop(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Check DM role.
	if !sc.perms.IsDM(i) {
		discord.RespondEphemeral(s, i, "You need the DM role to stop a session.")
		return
	}

	// Check session is active.
	if !sc.sessionMgr.IsActive() {
		discord.RespondEphemeral(s, i, "No active session to stop.")
		return
	}

	info := sc.sessionMgr.Info()
	duration := time.Since(info.StartedAt).Truncate(time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := sc.sessionMgr.Stop(ctx); err != nil {
		discord.RespondError(s, i, fmt.Errorf("discord: stop session: %w", err))
		return
	}

	discord.RespondEphemeral(s, i, fmt.Sprintf(
		"Session `%s` stopped.\n**Duration:** %s",
		info.SessionID,
		duration.String(),
	))
}

// interactionUserID extracts the user ID from an interaction, handling
// both guild (Member) and DM (User) contexts.
func interactionUserID(i *discordgo.InteractionCreate) string {
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User.ID
	}
	if i.User != nil {
		return i.User.ID
	}
	return ""
}
