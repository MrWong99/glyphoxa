package discord

import (
	"fmt"
	"log/slog"

	"github.com/bwmarrin/discordgo"
)

// RespondEphemeral sends an ephemeral text response to an interaction.
func RespondEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: content,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
	if err != nil {
		slog.Warn("discord: failed to send ephemeral response", "err", err)
	}
}

// RespondEmbed sends an ephemeral embed response to an interaction.
func RespondEmbed(s *discordgo.Session, i *discordgo.InteractionCreate, embed *discordgo.MessageEmbed) {
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{embed},
			Flags:  discordgo.MessageFlagsEphemeral,
		},
	})
	if err != nil {
		slog.Warn("discord: failed to send embed response", "err", err)
	}
}

// RespondError sends a formatted error response (ephemeral).
func RespondError(s *discordgo.Session, i *discordgo.InteractionCreate, err error) {
	RespondEphemeral(s, i, fmt.Sprintf("Error: %v", err))
}

// RespondModal opens a modal dialog.
func RespondModal(s *discordgo.Session, i *discordgo.InteractionCreate, modal *discordgo.InteractionResponseData) {
	respondErr := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: modal,
	})
	if respondErr != nil {
		slog.Warn("discord: failed to open modal", "err", respondErr)
	}
}

// DeferReply sends a deferred response (for long-running commands).
func DeferReply(s *discordgo.Session, i *discordgo.InteractionCreate) {
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})
	if err != nil {
		slog.Warn("discord: failed to defer reply", "err", err)
	}
}

// FollowUp sends a follow-up message after a deferred response.
func FollowUp(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	_, err := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Content: content,
		Flags:   discordgo.MessageFlagsEphemeral,
	})
	if err != nil {
		slog.Warn("discord: failed to send follow-up", "err", err)
	}
}

// FollowUpEmbed sends an embed follow-up message after a deferred response.
func FollowUpEmbed(s *discordgo.Session, i *discordgo.InteractionCreate, embed *discordgo.MessageEmbed) {
	_, err := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Embeds: []*discordgo.MessageEmbed{embed},
		Flags:  discordgo.MessageFlagsEphemeral,
	})
	if err != nil {
		slog.Warn("discord: failed to send embed follow-up", "err", err)
	}
}
