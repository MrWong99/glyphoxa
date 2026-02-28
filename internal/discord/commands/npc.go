package commands

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/MrWong99/glyphoxa/internal/agent/orchestrator"
	"github.com/MrWong99/glyphoxa/internal/discord"
)

// NPCCommands handles /npc slash command group.
type NPCCommands struct {
	perms *discord.PermissionChecker
	// getOrch returns the current session's orchestrator, or nil if no session is active.
	getOrch func() *orchestrator.Orchestrator
}

// NewNPCCommands creates an NPCCommands handler.
func NewNPCCommands(perms *discord.PermissionChecker, getOrch func() *orchestrator.Orchestrator) *NPCCommands {
	return &NPCCommands{
		perms:   perms,
		getOrch: getOrch,
	}
}

// Register registers all /npc subcommands with the router.
func (nc *NPCCommands) Register(router *discord.CommandRouter) {
	def := nc.Definition()
	router.RegisterCommand("npc", def, func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		discord.RespondEphemeral(s, i, "Please use a subcommand: `/npc list`, `/npc mute`, `/npc unmute`, `/npc speak`, `/npc muteall`, `/npc unmuteall`.")
	})
	router.RegisterHandler("npc/list", nc.handleList)
	router.RegisterHandler("npc/mute", nc.handleMute)
	router.RegisterHandler("npc/unmute", nc.handleUnmute)
	router.RegisterHandler("npc/speak", nc.handleSpeak)
	router.RegisterHandler("npc/muteall", nc.handleMuteAll)
	router.RegisterHandler("npc/unmuteall", nc.handleUnmuteAll)

	router.RegisterAutocomplete("npc/mute", nc.handleAutocomplete)
	router.RegisterAutocomplete("npc/unmute", nc.handleAutocomplete)
	router.RegisterAutocomplete("npc/speak", nc.handleAutocomplete)
}

// Definition returns the /npc ApplicationCommand for Discord registration.
func (nc *NPCCommands) Definition() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "npc",
		Description: "Manage NPC agents",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "list",
				Description: "List all NPCs with their status",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
			},
			{
				Name:        "mute",
				Description: "Mute an NPC",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{
						Name:         "name",
						Description:  "NPC name",
						Type:         discordgo.ApplicationCommandOptionString,
						Required:     true,
						Autocomplete: true,
					},
				},
			},
			{
				Name:        "unmute",
				Description: "Unmute an NPC",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{
						Name:         "name",
						Description:  "NPC name",
						Type:         discordgo.ApplicationCommandOptionString,
						Required:     true,
						Autocomplete: true,
					},
				},
			},
			{
				Name:        "speak",
				Description: "Make an NPC speak pre-written text",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{
						Name:         "name",
						Description:  "NPC name",
						Type:         discordgo.ApplicationCommandOptionString,
						Required:     true,
						Autocomplete: true,
					},
					{
						Name:        "text",
						Description: "Text for the NPC to speak",
						Type:        discordgo.ApplicationCommandOptionString,
						Required:    true,
					},
				},
			},
			{
				Name:        "muteall",
				Description: "Mute all NPCs",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
			},
			{
				Name:        "unmuteall",
				Description: "Unmute all NPCs",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
			},
		},
	}
}

// handleList handles /npc list.
func (nc *NPCCommands) handleList(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !nc.perms.IsDM(i) {
		discord.RespondEphemeral(s, i, "You need the DM role to manage NPCs.")
		return
	}

	orch := nc.getOrch()
	if orch == nil {
		discord.RespondEphemeral(s, i, "No active session.")
		return
	}

	agents := orch.ActiveAgents()
	if len(agents) == 0 {
		discord.RespondEphemeral(s, i, "No NPCs in this session.")
		return
	}

	var lines []string
	for _, a := range agents {
		muted, _ := orch.IsMuted(a.ID())
		icon := "🔊"
		if muted {
			icon = "🔇"
		}
		lines = append(lines, fmt.Sprintf("%s **%s** (ID: `%s`)", icon, a.Name(), a.ID()))
	}

	embed := &discordgo.MessageEmbed{
		Title:       "NPC Agents",
		Description: strings.Join(lines, "\n"),
		Color:       0x5865F2,
	}
	discord.RespondEmbed(s, i, embed)
}

// handleMute handles /npc mute <name>.
func (nc *NPCCommands) handleMute(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !nc.perms.IsDM(i) {
		discord.RespondEphemeral(s, i, "You need the DM role to manage NPCs.")
		return
	}

	orch := nc.getOrch()
	if orch == nil {
		discord.RespondEphemeral(s, i, "No active session.")
		return
	}

	name := subcommandStringOption(i, "name")
	a := orch.AgentByName(name)
	if a == nil {
		discord.RespondEphemeral(s, i, fmt.Sprintf("NPC %q not found.", name))
		return
	}

	if err := orch.MuteAgent(a.ID()); err != nil {
		discord.RespondError(s, i, err)
		return
	}

	discord.RespondEphemeral(s, i, fmt.Sprintf("Muted **%s**.", a.Name()))
}

// handleUnmute handles /npc unmute <name>.
func (nc *NPCCommands) handleUnmute(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !nc.perms.IsDM(i) {
		discord.RespondEphemeral(s, i, "You need the DM role to manage NPCs.")
		return
	}

	orch := nc.getOrch()
	if orch == nil {
		discord.RespondEphemeral(s, i, "No active session.")
		return
	}

	name := subcommandStringOption(i, "name")
	a := orch.AgentByName(name)
	if a == nil {
		discord.RespondEphemeral(s, i, fmt.Sprintf("NPC %q not found.", name))
		return
	}

	if err := orch.UnmuteAgent(a.ID()); err != nil {
		discord.RespondError(s, i, err)
		return
	}

	discord.RespondEphemeral(s, i, fmt.Sprintf("Unmuted **%s**.", a.Name()))
}

// handleSpeak handles /npc speak <name> <text>.
func (nc *NPCCommands) handleSpeak(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !nc.perms.IsDM(i) {
		discord.RespondEphemeral(s, i, "You need the DM role to manage NPCs.")
		return
	}

	orch := nc.getOrch()
	if orch == nil {
		discord.RespondEphemeral(s, i, "No active session.")
		return
	}

	name := subcommandStringOption(i, "name")
	text := subcommandStringOption(i, "text")

	a := orch.AgentByName(name)
	if a == nil {
		discord.RespondEphemeral(s, i, fmt.Sprintf("NPC %q not found.", name))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := a.SpeakText(ctx, text); err != nil {
		discord.RespondError(s, i, err)
		return
	}

	discord.RespondEphemeral(s, i, fmt.Sprintf("**%s** is speaking: %q", a.Name(), text))
}

// handleMuteAll handles /npc muteall.
func (nc *NPCCommands) handleMuteAll(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !nc.perms.IsDM(i) {
		discord.RespondEphemeral(s, i, "You need the DM role to manage NPCs.")
		return
	}

	orch := nc.getOrch()
	if orch == nil {
		discord.RespondEphemeral(s, i, "No active session.")
		return
	}

	count := orch.MuteAll()
	discord.RespondEphemeral(s, i, fmt.Sprintf("Muted %d NPC(s).", count))
}

// handleUnmuteAll handles /npc unmuteall.
func (nc *NPCCommands) handleUnmuteAll(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !nc.perms.IsDM(i) {
		discord.RespondEphemeral(s, i, "You need the DM role to manage NPCs.")
		return
	}

	orch := nc.getOrch()
	if orch == nil {
		discord.RespondEphemeral(s, i, "No active session.")
		return
	}

	count := orch.UnmuteAll()
	discord.RespondEphemeral(s, i, fmt.Sprintf("Unmuted %d NPC(s).", count))
}

// handleAutocomplete provides autocomplete for the "name" option across
// /npc mute, /npc unmute, and /npc speak.
func (nc *NPCCommands) handleAutocomplete(s *discordgo.Session, i *discordgo.InteractionCreate) {
	orch := nc.getOrch()
	if orch == nil {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionApplicationCommandAutocompleteResult,
			Data: &discordgo.InteractionResponseData{},
		})
		return
	}

	// Find the focused option's partial value.
	partial := ""
	data := i.ApplicationCommandData()
	if len(data.Options) > 0 && data.Options[0].Type == discordgo.ApplicationCommandOptionSubCommand {
		for _, opt := range data.Options[0].Options {
			if opt.Focused {
				partial = strings.ToLower(opt.StringValue())
				break
			}
		}
	}

	agents := orch.ActiveAgents()
	var choices []*discordgo.ApplicationCommandOptionChoice
	for _, a := range agents {
		if partial == "" || strings.HasPrefix(strings.ToLower(a.Name()), partial) {
			choices = append(choices, &discordgo.ApplicationCommandOptionChoice{
				Name:  a.Name(),
				Value: a.Name(),
			})
		}
		// Discord limits autocomplete to 25 choices.
		if len(choices) >= 25 {
			break
		}
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionApplicationCommandAutocompleteResult,
		Data: &discordgo.InteractionResponseData{
			Choices: choices,
		},
	})
}

// subcommandStringOption extracts a string option value from a subcommand interaction.
func subcommandStringOption(i *discordgo.InteractionCreate, name string) string {
	data := i.ApplicationCommandData()
	if len(data.Options) > 0 && data.Options[0].Type == discordgo.ApplicationCommandOptionSubCommand {
		for _, opt := range data.Options[0].Options {
			if opt.Name == name {
				return opt.StringValue()
			}
		}
	}
	return ""
}
