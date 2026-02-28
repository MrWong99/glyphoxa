package discord

import (
	"log/slog"
	"strings"
	"sync"

	"github.com/bwmarrin/discordgo"
)

// HandlerFunc is the signature for slash command handlers.
type HandlerFunc func(s *discordgo.Session, i *discordgo.InteractionCreate)

// AutocompleteFunc is the signature for autocomplete handlers.
type AutocompleteFunc func(s *discordgo.Session, i *discordgo.InteractionCreate)

// commandEntry stores a command definition along with its handler.
type commandEntry struct {
	command  *discordgo.ApplicationCommand
	handler  HandlerFunc
}

// CommandRouter dispatches Discord interactions to registered handlers.
type CommandRouter struct {
	mu               sync.RWMutex
	commands         map[string]commandEntry             // "command" or "command/subcommand" → entry
	autocomplete     map[string]AutocompleteFunc          // "command" or "command/subcommand" → handler
	components       map[string]HandlerFunc               // custom_id → handler (for buttons)
	componentPrefix  map[string]HandlerFunc               // prefix → handler (for buttons with dynamic suffixes)
	modals           map[string]HandlerFunc               // custom_id → handler (for modal submits)
}

// NewCommandRouter creates an empty router.
func NewCommandRouter() *CommandRouter {
	return &CommandRouter{
		commands:        make(map[string]commandEntry),
		autocomplete:    make(map[string]AutocompleteFunc),
		components:      make(map[string]HandlerFunc),
		componentPrefix: make(map[string]HandlerFunc),
		modals:          make(map[string]HandlerFunc),
	}
}

// RegisterCommand registers a handler for a slash command. The key format is
// "command" or "command/subcommand" (e.g., "npc/mute"). The cmd definition
// is used when registering commands with Discord (only top-level commands are
// registered; subcommands are nested inside).
func (r *CommandRouter) RegisterCommand(key string, cmd *discordgo.ApplicationCommand, handler HandlerFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commands[key] = commandEntry{command: cmd, handler: handler}
}

// RegisterHandler registers a handler for a slash command key without
// providing a command definition. Use this for subcommand handlers when
// the parent command is already registered.
func (r *CommandRouter) RegisterHandler(key string, handler HandlerFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commands[key] = commandEntry{handler: handler}
}

// RegisterAutocomplete registers an autocomplete handler.
func (r *CommandRouter) RegisterAutocomplete(key string, handler AutocompleteFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.autocomplete[key] = handler
}

// RegisterComponent registers a handler for a message component interaction (buttons).
func (r *CommandRouter) RegisterComponent(customID string, handler HandlerFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.components[customID] = handler
}

// RegisterComponentPrefix registers a handler that matches any component
// whose custom_id starts with the given prefix. This is useful for buttons
// with dynamic suffixes (e.g., "entity_remove_confirm:" matches
// "entity_remove_confirm:some-id").
func (r *CommandRouter) RegisterComponentPrefix(prefix string, handler HandlerFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.componentPrefix[prefix] = handler
}

// RegisterModal registers a handler for a modal submit interaction.
func (r *CommandRouter) RegisterModal(customID string, handler HandlerFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.modals[customID] = handler
}

// ApplicationCommands returns the deduplicated list of top-level command
// definitions for registration with the Discord API.
func (r *CommandRouter) ApplicationCommands() []*discordgo.ApplicationCommand {
	r.mu.RLock()
	defer r.mu.RUnlock()

	seen := make(map[string]bool)
	var cmds []*discordgo.ApplicationCommand
	for _, entry := range r.commands {
		if entry.command != nil && !seen[entry.command.Name] {
			seen[entry.command.Name] = true
			cmds = append(cmds, entry.command)
		}
	}
	return cmds
}

// Handle dispatches an interaction to the appropriate handler.
func (r *CommandRouter) Handle(s *discordgo.Session, i *discordgo.InteractionCreate) {
	switch i.Type {
	case discordgo.InteractionApplicationCommand:
		r.handleApplicationCommand(s, i)

	case discordgo.InteractionApplicationCommandAutocomplete:
		r.handleAutocomplete(s, i)

	case discordgo.InteractionMessageComponent:
		r.handleComponent(s, i)

	case discordgo.InteractionModalSubmit:
		r.handleModal(s, i)

	default:
		slog.Warn("discord: unhandled interaction type", "type", i.Type)
	}
}

// interactionKey builds a router key from an ApplicationCommand interaction.
func interactionKey(data discordgo.ApplicationCommandInteractionData) string {
	key := data.Name
	if len(data.Options) > 0 && data.Options[0].Type == discordgo.ApplicationCommandOptionSubCommand {
		key += "/" + data.Options[0].Name
	}
	return key
}

func (r *CommandRouter) handleApplicationCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()
	key := interactionKey(data)

	r.mu.RLock()
	entry, ok := r.commands[key]
	r.mu.RUnlock()

	if !ok {
		slog.Warn("discord: unknown command", "key", key)
		RespondEphemeral(s, i, "Unknown command.")
		return
	}
	entry.handler(s, i)
}

func (r *CommandRouter) handleAutocomplete(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()
	key := interactionKey(data)

	r.mu.RLock()
	handler, ok := r.autocomplete[key]
	r.mu.RUnlock()

	if !ok {
		slog.Debug("discord: no autocomplete handler", "key", key)
		// Respond with empty choices.
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionApplicationCommandAutocompleteResult,
			Data: &discordgo.InteractionResponseData{},
		})
		return
	}
	handler(s, i)
}

func (r *CommandRouter) handleComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	customID := i.MessageComponentData().CustomID

	r.mu.RLock()
	handler, ok := r.components[customID]
	if !ok {
		// Fall back to prefix matching.
		for prefix, h := range r.componentPrefix {
			if strings.HasPrefix(customID, prefix) {
				handler = h
				ok = true
				break
			}
		}
	}
	r.mu.RUnlock()

	if !ok {
		slog.Warn("discord: unknown component", "custom_id", customID)
		RespondEphemeral(s, i, "Unknown component.")
		return
	}
	handler(s, i)
}

func (r *CommandRouter) handleModal(s *discordgo.Session, i *discordgo.InteractionCreate) {
	customID := i.ModalSubmitData().CustomID

	r.mu.RLock()
	handler, ok := r.modals[customID]
	r.mu.RUnlock()

	if !ok {
		slog.Warn("discord: unknown modal", "custom_id", customID)
		RespondEphemeral(s, i, "Unknown modal.")
		return
	}
	handler(s, i)
}
