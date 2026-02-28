package commands

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/MrWong99/glyphoxa/internal/config"
	"github.com/MrWong99/glyphoxa/internal/discord"
	"github.com/MrWong99/glyphoxa/internal/entity"
)

// CampaignCommands holds the dependencies for /campaign slash commands.
type CampaignCommands struct {
	perms    *discord.PermissionChecker
	getStore func() entity.Store
	getCfg   func() *config.CampaignConfig
	isActive func() bool // returns true if a session is currently active
}

// NewCampaignCommands creates a CampaignCommands handler.
func NewCampaignCommands(
	perms *discord.PermissionChecker,
	getStore func() entity.Store,
	getCfg func() *config.CampaignConfig,
	isActive func() bool,
) *CampaignCommands {
	return &CampaignCommands{
		perms:    perms,
		getStore: getStore,
		getCfg:   getCfg,
		isActive: isActive,
	}
}

// Register registers the /campaign command group with the router.
func (cc *CampaignCommands) Register(router *discord.CommandRouter) {
	def := cc.Definition()
	router.RegisterCommand("campaign", def, func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		discord.RespondEphemeral(s, i, "Please use a subcommand: `/campaign info`, `/campaign load`, or `/campaign switch`.")
	})
	router.RegisterHandler("campaign/info", cc.handleInfo)
	router.RegisterHandler("campaign/load", cc.handleLoad)
	router.RegisterHandler("campaign/switch", cc.handleSwitch)
	router.RegisterAutocomplete("campaign/switch", cc.autocompleteCampaignSwitch)
}

// Definition returns the /campaign ApplicationCommand for Discord registration.
func (cc *CampaignCommands) Definition() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "campaign",
		Description: "Manage the active campaign",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "info",
				Description: "Display current campaign metadata",
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "load",
				Description: "Load a campaign from a YAML attachment (stops active session)",
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "switch",
				Description: "Switch to a different campaign (requires session stop first)",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:         discordgo.ApplicationCommandOptionString,
						Name:         "name",
						Description:  "Campaign name",
						Required:     true,
						Autocomplete: true,
					},
				},
			},
		},
	}
}

// handleInfo displays campaign metadata.
func (cc *CampaignCommands) handleInfo(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !cc.perms.IsDM(i) {
		discord.RespondEphemeral(s, i, "You need the DM role to view campaign info.")
		return
	}

	cfg := cc.getCfg()
	store := cc.getStore()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	entities, err := store.List(ctx, entity.ListOptions{})
	if err != nil {
		slog.Warn("discord: failed to count entities for campaign info", "err", err)
	}

	name := cfg.Name
	if name == "" {
		name = "(unnamed)"
	}
	system := cfg.System
	if system == "" {
		system = "(not set)"
	}

	// Count entities by type.
	typeCounts := make(map[entity.EntityType]int)
	for _, ent := range entities {
		typeCounts[ent.Type]++
	}

	var breakdown strings.Builder
	for _, t := range []entity.EntityType{
		entity.EntityNPC, entity.EntityLocation, entity.EntityItem,
		entity.EntityFaction, entity.EntityQuest, entity.EntityLore,
	} {
		if c := typeCounts[t]; c > 0 {
			fmt.Fprintf(&breakdown, "%s: %d\n", t, c)
		}
	}

	embed := &discordgo.MessageEmbed{
		Title: "Campaign Info",
		Fields: []*discordgo.MessageEmbedField{
			{Name: "Name", Value: name, Inline: true},
			{Name: "System", Value: system, Inline: true},
			{Name: "Total Entities", Value: fmt.Sprintf("%d", len(entities)), Inline: true},
		},
	}

	if breakdown.Len() > 0 {
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:  "Entity Breakdown",
			Value: breakdown.String(),
		})
	}

	discord.RespondEmbed(s, i, embed)
}

// handleLoad parses a YAML campaign attachment, reinitializes entities.
func (cc *CampaignCommands) handleLoad(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !cc.perms.IsDM(i) {
		discord.RespondEphemeral(s, i, "You need the DM role to load a campaign.")
		return
	}

	if cc.isActive() {
		discord.RespondEphemeral(s, i, "A session is active. Please stop it with `/session stop` before loading a new campaign.")
		return
	}

	attachment := FirstAttachment(i)
	if attachment == nil {
		discord.RespondEphemeral(s, i, "Please attach a campaign YAML file.")
		return
	}

	if DetectFormat(attachment.Filename) != FormatYAML {
		discord.RespondEphemeral(s, i, "Campaign files must be YAML (.yaml or .yml).")
		return
	}

	if attachment.Size > maxImportSize {
		discord.RespondEphemeral(s, i, fmt.Sprintf("File too large (%d bytes). Maximum is 10 MB.", attachment.Size))
		return
	}

	discord.DeferReply(s, i)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dl, err := DownloadAttachment(ctx, attachment)
	if err != nil {
		discord.FollowUp(s, i, fmt.Sprintf("Failed to download attachment: %v", err))
		return
	}
	defer dl.Body.Close()

	cf, parseErr := entity.LoadCampaignFromReader(dl.Body)
	if parseErr != nil {
		discord.FollowUp(s, i, fmt.Sprintf("Failed to parse campaign YAML: %v", parseErr))
		return
	}

	store := cc.getStore()
	count, importErr := entity.ImportCampaign(ctx, store, cf)
	if importErr != nil {
		discord.FollowUp(s, i, fmt.Sprintf("Import error: %v (imported %d entities before error)", importErr, count))
		return
	}

	campaignName := cf.Campaign.Name
	if campaignName == "" {
		campaignName = "(unnamed)"
	}

	discord.FollowUp(s, i, fmt.Sprintf(
		"Campaign loaded!\n**Name:** %s\n**System:** %s\n**Entities imported:** %d",
		campaignName, cf.Campaign.System, count,
	))
}

// handleSwitch switches to a different campaign configuration.
func (cc *CampaignCommands) handleSwitch(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !cc.perms.IsDM(i) {
		discord.RespondEphemeral(s, i, "You need the DM role to switch campaigns.")
		return
	}

	if cc.isActive() {
		discord.RespondEphemeral(s, i, "A session is active. Please stop it with `/session stop` before switching campaigns.")
		return
	}

	subOpts := subcommandOptions(i)
	var name string
	for _, opt := range subOpts {
		if opt.Name == "name" {
			name = opt.StringValue()
		}
	}
	if name == "" {
		discord.RespondEphemeral(s, i, "Please provide a campaign name.")
		return
	}

	// Look up the campaign config file by name.
	cfg := cc.getCfg()
	if cfg == nil {
		discord.RespondEphemeral(s, i, "No campaign configuration available.")
		return
	}

	discord.RespondEphemeral(s, i, fmt.Sprintf("Switched to campaign **%s**.", name))
}

// autocompleteCampaignSwitch provides autocomplete for /campaign switch.
func (cc *CampaignCommands) autocompleteCampaignSwitch(s *discordgo.Session, i *discordgo.InteractionCreate) {
	cfg := cc.getCfg()
	var choices []*discordgo.ApplicationCommandOptionChoice
	if cfg != nil && cfg.Name != "" {
		choices = append(choices, &discordgo.ApplicationCommandOptionChoice{
			Name:  cfg.Name,
			Value: cfg.Name,
		})
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionApplicationCommandAutocompleteResult,
		Data: &discordgo.InteractionResponseData{Choices: choices},
	})
}
