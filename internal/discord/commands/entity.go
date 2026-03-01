package commands

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/MrWong99/glyphoxa/internal/discord"
	"github.com/MrWong99/glyphoxa/internal/entity"
)

const (
	entityAddModalID          = "entity_add_modal"
	entityRemoveCancelID      = "entity_remove_cancel"
	entityRemoveConfirmPrefix = "entity_remove_confirm:"
	maxImportSize             = 10 << 20 // 10 MB
)

// EntityCommands holds the dependencies for /entity slash commands.
type EntityCommands struct {
	perms    *discord.PermissionChecker
	getStore func() entity.Store
}

// NewEntityCommands creates an EntityCommands and registers its handlers
// with the router.
func NewEntityCommands(perms *discord.PermissionChecker, getStore func() entity.Store) *EntityCommands {
	return &EntityCommands{
		perms:    perms,
		getStore: getStore,
	}
}

// Register registers the /entity command group with the router.
func (ec *EntityCommands) Register(router *discord.CommandRouter) {
	def := ec.Definition()
	router.RegisterCommand("entity", def, func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		discord.RespondEphemeral(s, i, "Please use a subcommand: `/entity add`, `/entity list`, `/entity remove`, or `/entity import`.")
	})
	router.RegisterHandler("entity/add", ec.handleAdd)
	router.RegisterHandler("entity/list", ec.handleList)
	router.RegisterHandler("entity/remove", ec.handleRemove)
	router.RegisterHandler("entity/import", ec.handleImport)

	router.RegisterAutocomplete("entity/remove", ec.autocompleteRemove)

	router.RegisterModal(entityAddModalID, ec.handleAddModal)
	router.RegisterComponent(entityRemoveCancelID, ec.handleRemoveCancel)
	router.RegisterComponentPrefix(entityRemoveConfirmPrefix, ec.handleRemoveConfirm)
}

// Definition returns the /entity ApplicationCommand for Discord registration.
func (ec *EntityCommands) Definition() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "entity",
		Description: "Manage campaign entities (NPCs, locations, items, etc.)",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "add",
				Description: "Add a new entity via a form",
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "list",
				Description: "List all entities, optionally filtered by type",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "type",
						Description: "Filter by entity type",
						Required:    false,
						Choices: []*discordgo.ApplicationCommandOptionChoice{
							{Name: "NPC", Value: string(entity.EntityNPC)},
							{Name: "Location", Value: string(entity.EntityLocation)},
							{Name: "Item", Value: string(entity.EntityItem)},
							{Name: "Faction", Value: string(entity.EntityFaction)},
							{Name: "Quest", Value: string(entity.EntityQuest)},
							{Name: "Lore", Value: string(entity.EntityLore)},
						},
					},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "remove",
				Description: "Remove an entity by name",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:         discordgo.ApplicationCommandOptionString,
						Name:         "name",
						Description:  "Entity name",
						Required:     true,
						Autocomplete: true,
					},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "import",
				Description: "Import entities from a YAML or JSON file attachment",
			},
		},
	}
}

// handleAdd opens the entity creation modal.
func (ec *EntityCommands) handleAdd(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !ec.perms.IsDM(i) {
		discord.RespondEphemeral(s, i, "You need the DM role to add entities.")
		return
	}

	discord.RespondModal(s, i, &discordgo.InteractionResponseData{
		CustomID: entityAddModalID,
		Title:    "Add Entity",
		Components: []discordgo.MessageComponent{
			discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				discordgo.TextInput{
					CustomID:    "entity_name",
					Label:       "Name",
					Style:       discordgo.TextInputShort,
					Placeholder: "e.g., Gundren Rockseeker",
					Required:    new(true),
					MaxLength:   100,
				},
			}},
			discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				discordgo.TextInput{
					CustomID:    "entity_type",
					Label:       "Type (npc, location, item, faction, quest, lore)",
					Style:       discordgo.TextInputShort,
					Placeholder: "npc",
					Required:    new(true),
					MaxLength:   20,
				},
			}},
			discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				discordgo.TextInput{
					CustomID:    "entity_description",
					Label:       "Description",
					Style:       discordgo.TextInputParagraph,
					Placeholder: "A dwarf merchant who hired the party...",
					Required:    new(false),
					MaxLength:   2000,
				},
			}},
			discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				discordgo.TextInput{
					CustomID:    "entity_tags",
					Label:       "Tags (comma-separated)",
					Style:       discordgo.TextInputShort,
					Placeholder: "ally, phandalin, quest-giver",
					Required:    new(false),
					MaxLength:   200,
				},
			}},
		},
	})
}

// handleList responds with a formatted embed of entities.
func (ec *EntityCommands) handleList(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !ec.perms.IsDM(i) {
		discord.RespondEphemeral(s, i, "You need the DM role to list entities.")
		return
	}

	opts := entity.ListOptions{}
	// Extract optional type filter from subcommand options.
	subOpts := subcommandOptions(i)
	for _, opt := range subOpts {
		if opt.Name == "type" {
			opts.Type = entity.EntityType(opt.StringValue())
		}
	}

	store := ec.getStore()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	entities, err := store.List(ctx, opts)
	if err != nil {
		discord.RespondError(s, i, fmt.Errorf("list entities: %w", err))
		return
	}

	if len(entities) == 0 {
		discord.RespondEphemeral(s, i, "No entities found.")
		return
	}

	// Build embed fields, capping at 25 (Discord embed limit).
	var fields []*discordgo.MessageEmbedField
	for idx, ent := range entities {
		if idx >= 25 {
			break
		}
		desc := ent.Description
		if len(desc) > 100 {
			desc = desc[:97] + "..."
		}
		value := fmt.Sprintf("**Type:** %s", ent.Type)
		if desc != "" {
			value += fmt.Sprintf("\n%s", desc)
		}
		if len(ent.Tags) > 0 {
			value += fmt.Sprintf("\n**Tags:** %s", strings.Join(ent.Tags, ", "))
		}
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:  ent.Name,
			Value: value,
		})
	}

	title := "Entities"
	if opts.Type != "" {
		title = fmt.Sprintf("Entities (%s)", opts.Type)
	}

	embed := &discordgo.MessageEmbed{
		Title:  title,
		Fields: fields,
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("%d total", len(entities)),
		},
	}

	discord.RespondEmbed(s, i, embed)
}

// handleRemove prompts confirmation before removing an entity.
func (ec *EntityCommands) handleRemove(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !ec.perms.IsDM(i) {
		discord.RespondEphemeral(s, i, "You need the DM role to remove entities.")
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
		discord.RespondEphemeral(s, i, "Please provide an entity name.")
		return
	}

	// Look up entity by name to get its ID.
	store := ec.getStore()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	entities, err := store.List(ctx, entity.ListOptions{})
	if err != nil {
		discord.RespondError(s, i, fmt.Errorf("list entities: %w", err))
		return
	}

	var match *entity.EntityDefinition
	for idx := range entities {
		if strings.EqualFold(entities[idx].Name, name) {
			match = &entities[idx]
			break
		}
	}

	if match == nil {
		discord.RespondEphemeral(s, i, fmt.Sprintf("Entity %q not found.", name))
		return
	}

	// Respond with confirmation buttons.
	err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{{
				Title:       "Remove Entity",
				Description: fmt.Sprintf("Remove entity **%s** (%s)? This cannot be undone.", match.Name, match.Type),
				Color:       0xFF4444,
			}},
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					discordgo.Button{
						Label:    "Cancel",
						Style:    discordgo.SecondaryButton,
						CustomID: entityRemoveCancelID,
					},
					discordgo.Button{
						Label:    "Confirm Remove",
						Style:    discordgo.DangerButton,
						CustomID: entityRemoveConfirmPrefix + match.ID,
					},
				}},
			},
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})
	if err != nil {
		slog.Warn("discord: failed to send remove confirmation", "err", err)
	}
}

// handleRemoveCancel handles the cancel button on entity removal.
func (ec *EntityCommands) handleRemoveCancel(s *discordgo.Session, i *discordgo.InteractionCreate) {
	discord.RespondEphemeral(s, i, "Entity removal cancelled.")
}

// handleRemoveConfirm handles the confirm button on entity removal.
func (ec *EntityCommands) handleRemoveConfirm(s *discordgo.Session, i *discordgo.InteractionCreate) {
	customID := i.MessageComponentData().CustomID
	entityID := strings.TrimPrefix(customID, entityRemoveConfirmPrefix)
	if entityID == "" {
		discord.RespondEphemeral(s, i, "Invalid entity ID.")
		return
	}

	store := ec.getStore()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := store.Remove(ctx, entityID); err != nil {
		discord.RespondError(s, i, fmt.Errorf("remove entity: %w", err))
		return
	}

	discord.RespondEphemeral(s, i, fmt.Sprintf("Entity `%s` removed.", entityID))
}

// autocompleteRemove provides name autocomplete for /entity remove.
func (ec *EntityCommands) autocompleteRemove(s *discordgo.Session, i *discordgo.InteractionCreate) {
	store := ec.getStore()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	entities, err := store.List(ctx, entity.ListOptions{})
	if err != nil {
		slog.Warn("discord: entity autocomplete failed", "err", err)
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionApplicationCommandAutocompleteResult,
			Data: &discordgo.InteractionResponseData{},
		})
		return
	}

	// Get the current typed value for filtering.
	var typed string
	subOpts := subcommandOptions(i)
	for _, opt := range subOpts {
		if opt.Name == "name" && opt.Focused {
			typed = strings.ToLower(opt.StringValue())
		}
	}

	var choices []*discordgo.ApplicationCommandOptionChoice
	for _, ent := range entities {
		if typed != "" && !strings.Contains(strings.ToLower(ent.Name), typed) {
			continue
		}
		choices = append(choices, &discordgo.ApplicationCommandOptionChoice{
			Name:  fmt.Sprintf("%s (%s)", ent.Name, ent.Type),
			Value: ent.Name,
		})
		if len(choices) >= 25 {
			break
		}
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionApplicationCommandAutocompleteResult,
		Data: &discordgo.InteractionResponseData{Choices: choices},
	})
}

// handleImport processes a file attachment import.
func (ec *EntityCommands) handleImport(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !ec.perms.IsDM(i) {
		discord.RespondEphemeral(s, i, "You need the DM role to import entities.")
		return
	}

	attachment := FirstAttachment(i)
	if attachment == nil {
		discord.RespondEphemeral(s, i, "Please attach a YAML or JSON file to import.")
		return
	}

	if attachment.Size > maxImportSize {
		discord.RespondEphemeral(s, i, fmt.Sprintf("File too large (%d bytes). Maximum is 10 MB.", attachment.Size))
		return
	}

	format := DetectFormat(attachment.Filename)
	if format == FormatUnknown {
		discord.RespondEphemeral(s, i, "Unsupported file format. Use .yaml, .yml, or .json.")
		return
	}

	// Defer reply since download + parse may take a moment.
	discord.DeferReply(s, i)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dl, err := DownloadAttachment(ctx, attachment)
	if err != nil {
		discord.FollowUp(s, i, fmt.Sprintf("Failed to download attachment: %v", err))
		return
	}
	defer dl.Body.Close()

	store := ec.getStore()
	var count int

	switch format {
	case FormatYAML:
		cf, parseErr := entity.LoadCampaignFromReader(dl.Body)
		if parseErr != nil {
			discord.FollowUp(s, i, fmt.Sprintf("Failed to parse YAML: %v", parseErr))
			return
		}
		count, err = entity.ImportCampaign(ctx, store, cf)
	case FormatJSON:
		count, err = entity.ImportFoundryVTT(ctx, store, dl.Body)
	}

	if err != nil {
		discord.FollowUp(s, i, fmt.Sprintf("Import error: %v (imported %d entities before error)", err, count))
		return
	}

	discord.FollowUp(s, i, fmt.Sprintf("Import complete. **%d** entities imported from `%s`.", count, attachment.Filename))
}

// subcommandOptions extracts the options from the first subcommand in an
// interaction's application command data. Returns nil if no subcommand exists.
func subcommandOptions(i *discordgo.InteractionCreate) []*discordgo.ApplicationCommandInteractionDataOption {
	data := i.ApplicationCommandData()
	if len(data.Options) > 0 && data.Options[0].Type == discordgo.ApplicationCommandOptionSubCommand {
		return data.Options[0].Options
	}
	return nil
}
