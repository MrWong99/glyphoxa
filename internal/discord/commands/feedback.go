// Package commands implements Discord slash command handlers for the Glyphoxa
// DM experience.
package commands

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/MrWong99/glyphoxa/internal/discord"
	"github.com/bwmarrin/discordgo"
)

const (
	feedbackModalID = "feedback_modal"
)

// FeedbackStore persists post-session feedback.
type FeedbackStore interface {
	SaveFeedback(sessionID string, feedback Feedback) error
}

// Feedback holds a DM's post-session feedback ratings and comments.
type Feedback struct {
	SessionID      string
	UserID         string
	VoiceLatency   int // 1-5
	NPCPersonality int // 1-5
	MemoryAccuracy int // 1-5
	DMWorkflow     int // 1-5
	Comments       string
}

// FeedbackCommands handles the /feedback slash command.
type FeedbackCommands struct {
	perms        *discord.PermissionChecker
	store        FeedbackStore
	getSessionID func() string // returns current or last session ID
}

// NewFeedbackCommands creates a FeedbackCommands handler.
func NewFeedbackCommands(perms *discord.PermissionChecker, store FeedbackStore, getSessionID func() string) *FeedbackCommands {
	return &FeedbackCommands{
		perms:        perms,
		store:        store,
		getSessionID: getSessionID,
	}
}

// Register registers the /feedback command and modal handler with the router.
func (fc *FeedbackCommands) Register(router *discord.CommandRouter) {
	router.RegisterCommand("feedback", fc.Definition(), fc.handleFeedback)
	router.RegisterModal(feedbackModalID, fc.handleFeedbackModal)
}

// Definition returns the /feedback ApplicationCommand for Discord registration.
func (fc *FeedbackCommands) Definition() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "feedback",
		Description: "Submit post-session feedback",
	}
}

// handleFeedback opens the feedback modal.
func (fc *FeedbackCommands) handleFeedback(s *discordgo.Session, i *discordgo.InteractionCreate) {
	sessionID := fc.getSessionID()
	if sessionID == "" {
		discord.RespondEphemeral(s, i, "No session has been run yet. Start and stop a session before submitting feedback.")
		return
	}

	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{
			CustomID: feedbackModalID,
			Title:    "Session Feedback",
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.TextInput{
							CustomID:    "voice_latency",
							Label:       "Voice latency (1=terrible, 5=great)",
							Style:       discordgo.TextInputShort,
							Placeholder: "1-5",
							Required:    new(true),
							MinLength:   1,
							MaxLength:   1,
						},
					},
				},
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.TextInput{
							CustomID:    "npc_personality",
							Label:       "NPC personality quality (1-5)",
							Style:       discordgo.TextInputShort,
							Placeholder: "1-5",
							Required:    new(true),
							MinLength:   1,
							MaxLength:   1,
						},
					},
				},
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.TextInput{
							CustomID:    "memory_accuracy",
							Label:       "Memory accuracy (1-5)",
							Style:       discordgo.TextInputShort,
							Placeholder: "1-5",
							Required:    new(true),
							MinLength:   1,
							MaxLength:   1,
						},
					},
				},
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.TextInput{
							CustomID:    "dm_workflow",
							Label:       "DM workflow (1-5)",
							Style:       discordgo.TextInputShort,
							Placeholder: "1-5",
							Required:    new(true),
							MinLength:   1,
							MaxLength:   1,
						},
					},
				},
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.TextInput{
							CustomID:    "comments",
							Label:       "Comments (optional)",
							Style:       discordgo.TextInputParagraph,
							Placeholder: "What worked well? What needs improvement?",
							Required:    new(false),
							MaxLength:   1000,
						},
					},
				},
			},
		},
	})
	if err != nil {
		slog.Error("discord: failed to open feedback modal", "error", err)
	}
}

// handleFeedbackModal processes the submitted feedback form.
func (fc *FeedbackCommands) handleFeedbackModal(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ModalSubmitData()

	fb := Feedback{
		SessionID: fc.getSessionID(),
	}
	if i.Member != nil && i.Member.User != nil {
		fb.UserID = i.Member.User.ID
	}

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
			case "voice_latency":
				fb.VoiceLatency = parseRating(ti.Value)
			case "npc_personality":
				fb.NPCPersonality = parseRating(ti.Value)
			case "memory_accuracy":
				fb.MemoryAccuracy = parseRating(ti.Value)
			case "dm_workflow":
				fb.DMWorkflow = parseRating(ti.Value)
			case "comments":
				fb.Comments = strings.TrimSpace(ti.Value)
			}
		}
	}

	if fc.store != nil {
		if err := fc.store.SaveFeedback(fb.SessionID, fb); err != nil {
			slog.Error("discord: failed to save feedback", "error", err)
			discord.RespondEphemeral(s, i, fmt.Sprintf("Failed to save feedback: %v", err))
			return
		}
	}

	avg := float64(fb.VoiceLatency+fb.NPCPersonality+fb.MemoryAccuracy+fb.DMWorkflow) / 4.0
	discord.RespondEphemeral(s, i, fmt.Sprintf(
		"Thank you for your feedback! Average rating: %.1f/5\n\nSession: `%s`",
		avg, fb.SessionID,
	))
}

// parseRating converts a string to an int rating (1-5), clamping to range.
func parseRating(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 1 {
		return 1
	}
	if n > 5 {
		return 5
	}
	return n
}
