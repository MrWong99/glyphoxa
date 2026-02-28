package commands

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/MrWong99/glyphoxa/internal/discord"
	"github.com/MrWong99/glyphoxa/internal/entity"
)

// handleAddModal processes the entity creation modal submission.
func (ec *EntityCommands) handleAddModal(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ModalSubmitData()

	var name, entityType, description, tagsRaw string
	for _, row := range data.Components {
		ar, ok := row.(*discordgo.ActionsRow)
		if !ok {
			continue
		}
		for _, comp := range ar.Components {
			ti, ok := comp.(*discordgo.TextInput)
			if !ok {
				continue
			}
			switch ti.CustomID {
			case "entity_name":
				name = strings.TrimSpace(ti.Value)
			case "entity_type":
				entityType = strings.TrimSpace(strings.ToLower(ti.Value))
			case "entity_description":
				description = strings.TrimSpace(ti.Value)
			case "entity_tags":
				tagsRaw = strings.TrimSpace(ti.Value)
			}
		}
	}

	if name == "" {
		discord.RespondEphemeral(s, i, "Entity name is required.")
		return
	}

	eType := entity.EntityType(entityType)
	if !eType.IsValid() {
		discord.RespondEphemeral(s, i, fmt.Sprintf(
			"Invalid entity type %q. Valid types: npc, location, item, faction, quest, lore.",
			entityType,
		))
		return
	}

	var tags []string
	if tagsRaw != "" {
		for _, t := range strings.Split(tagsRaw, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				tags = append(tags, t)
			}
		}
	}

	def := entity.EntityDefinition{
		Name:        name,
		Type:        eType,
		Description: description,
		Tags:        tags,
	}

	if err := entity.Validate(def); err != nil {
		discord.RespondEphemeral(s, i, fmt.Sprintf("Validation error: %v", err))
		return
	}

	store := ec.getStore()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	created, err := store.Add(ctx, def)
	if err != nil {
		discord.RespondError(s, i, fmt.Errorf("add entity: %w", err))
		return
	}

	discord.RespondEphemeral(s, i, fmt.Sprintf(
		"Entity created!\n**Name:** %s\n**Type:** %s\n**ID:** `%s`",
		created.Name, created.Type, created.ID,
	))
}
