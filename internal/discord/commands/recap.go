package commands

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/MrWong99/glyphoxa/internal/app"
	"github.com/MrWong99/glyphoxa/internal/discord"
	"github.com/MrWong99/glyphoxa/internal/session"
	"github.com/MrWong99/glyphoxa/pkg/memory"
	"github.com/MrWong99/glyphoxa/pkg/provider/llm"
)

// recapColor is the embed sidebar color for recap embeds.
const recapColor = 0x3498DB

// maxEmbedDescriptionLen is the Discord embed description character limit.
const maxEmbedDescriptionLen = 4096

// RecapCommands handles the /session recap slash command.
type RecapCommands struct {
	sessionMgr   *app.SessionManager
	perms        *discord.PermissionChecker
	sessionStore memory.SessionStore
	summariser   session.Summariser
}

// RecapConfig holds dependencies for creating RecapCommands.
type RecapConfig struct {
	Bot          *discord.Bot
	SessionMgr   *app.SessionManager
	Perms        *discord.PermissionChecker
	SessionStore memory.SessionStore
	Summariser   session.Summariser // optional; if nil, raw transcript is shown
}

// NewRecapCommands creates a RecapCommands and registers the recap handler
// with the bot's router.
func NewRecapCommands(cfg RecapConfig) *RecapCommands {
	rc := &RecapCommands{
		sessionMgr:   cfg.SessionMgr,
		perms:        cfg.Perms,
		sessionStore: cfg.SessionStore,
		summariser:   cfg.Summariser,
	}
	rc.Register(cfg.Bot.Router())
	return rc
}

// Register registers the /session recap subcommand handler with the router.
// The parent /session command definition is expected to be already registered
// by SessionCommands; this only adds the handler for the recap subcommand.
func (rc *RecapCommands) Register(router *discord.CommandRouter) {
	router.RegisterHandler("session/recap", rc.handleRecap)
}

// handleRecap handles /session recap.
func (rc *RecapCommands) handleRecap(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !rc.perms.IsDM(i) {
		discord.RespondEphemeral(s, i, "You need the DM role to view session recaps.")
		return
	}

	info := rc.sessionMgr.Info()

	if info.SessionID == "" {
		discord.RespondEphemeral(s, i, "No session data available. Start a session first with `/session start`.")
		return
	}

	// Defer reply since transcript retrieval + summarisation may take time.
	discord.DeferReply(s, i)

	duration := time.Since(info.StartedAt).Truncate(time.Second)
	status := "Ended"
	if rc.sessionMgr.IsActive() {
		status = "Active"
	}

	// Build NPC list from the orchestrator if the session is active.
	npcList := rc.buildNPCList()

	// Retrieve transcript and build summary.
	summary := rc.buildSummary(info.SessionID)

	fields := []*discordgo.MessageEmbedField{
		{Name: "Campaign", Value: info.CampaignName, Inline: true},
		{Name: "Status", Value: status, Inline: true},
		{Name: "Session ID", Value: fmt.Sprintf("`%s`", info.SessionID), Inline: true},
		{Name: "Started By", Value: fmt.Sprintf("<@%s>", info.StartedBy), Inline: true},
		{Name: "Duration", Value: duration.String(), Inline: true},
		{Name: "Channel", Value: fmt.Sprintf("<#%s>", info.ChannelID), Inline: true},
		{Name: "NPCs", Value: npcList, Inline: false},
	}

	// Add entry count if session store is available.
	if rc.sessionStore != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		count, err := rc.sessionStore.EntryCount(ctx, info.SessionID)
		if err != nil {
			slog.Warn("recap: failed to get entry count", "session_id", info.SessionID, "err", err)
		} else {
			fields = append(fields, &discordgo.MessageEmbedField{
				Name: "Transcript Entries", Value: fmt.Sprintf("%d", count), Inline: true,
			})
		}
	}

	// Build embeds, splitting summary if needed.
	embeds := rc.buildRecapEmbeds(fields, summary)

	// Send the first embed as follow-up.
	if len(embeds) > 0 {
		discord.FollowUpEmbed(s, i, embeds[0])
	}
	// Send additional embeds as separate follow-ups if summary was split.
	for _, extra := range embeds[1:] {
		discord.FollowUpEmbed(s, i, extra)
	}
}

// buildNPCList returns a formatted list of active NPCs with mute state.
func (rc *RecapCommands) buildNPCList() string {
	if !rc.sessionMgr.IsActive() {
		return "Session not active."
	}
	orch := rc.sessionMgr.Orchestrator()
	if orch == nil {
		return "No NPC data available."
	}
	agents := orch.ActiveAgents()
	if len(agents) == 0 {
		return "No NPCs active."
	}

	var sb strings.Builder
	for _, a := range agents {
		muted, _ := orch.IsMuted(a.ID())
		muteLabel := ""
		if muted {
			muteLabel = " (muted)"
		}
		fmt.Fprintf(&sb, "- %s%s\n", a.Name(), muteLabel)
	}
	return sb.String()
}

// buildSummary retrieves the session transcript and optionally summarises it.
func (rc *RecapCommands) buildSummary(sessionID string) string {
	if rc.sessionStore == nil {
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Get all entries from the session (up to 24h window).
	entries, err := rc.sessionStore.GetRecent(ctx, sessionID, 24*time.Hour)
	if err != nil {
		slog.Warn("recap: failed to get transcript", "session_id", sessionID, "err", err)
		return "Failed to retrieve transcript."
	}
	if len(entries) == 0 {
		return "No transcript entries recorded."
	}

	// Try LLM summarisation if available.
	if rc.summariser != nil {
		messages := transcriptToMessages(entries)
		summary, err := rc.summariser.Summarise(ctx, messages)
		if err != nil {
			slog.Warn("recap: summarisation failed, falling back to raw transcript",
				"session_id", sessionID, "err", err)
		} else if summary != "" {
			return summary
		}
	}

	// Fall back to a simple transcript listing.
	return formatTranscript(entries)
}

// transcriptToMessages converts transcript entries to LLM messages for
// summarisation.
func transcriptToMessages(entries []memory.TranscriptEntry) []llm.Message {
	messages := make([]llm.Message, 0, len(entries))
	for _, e := range entries {
		role := "user"
		if e.IsNPC() {
			role = "assistant"
		}
		messages = append(messages, llm.Message{
			Role:    role,
			Name:    e.SpeakerName,
			Content: e.Text,
		})
	}
	return messages
}

// formatTranscript creates a simple chronological transcript listing.
func formatTranscript(entries []memory.TranscriptEntry) string {
	var sb strings.Builder
	for _, e := range entries {
		ts := e.Timestamp.Format("15:04:05")
		fmt.Fprintf(&sb, "**[%s] %s:** %s\n", ts, e.SpeakerName, e.Text)
	}
	result := sb.String()
	// Truncate if too long for embed.
	if len(result) > maxEmbedDescriptionLen-100 {
		result = result[:maxEmbedDescriptionLen-150] + "\n\n*... (truncated)*"
	}
	return result
}

// buildRecapEmbeds builds one or more embeds for the recap, splitting the
// summary across multiple embeds if it exceeds Discord's 4096-char limit.
func (rc *RecapCommands) buildRecapEmbeds(fields []*discordgo.MessageEmbedField, summary string) []*discordgo.MessageEmbed {
	if summary == "" {
		return []*discordgo.MessageEmbed{{
			Title:     "Session Recap",
			Color:     recapColor,
			Fields:    fields,
			Footer:    &discordgo.MessageEmbedFooter{Text: "Session recap"},
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}}
	}

	// If summary fits in one embed, use it as description.
	if len(summary) <= maxEmbedDescriptionLen {
		return []*discordgo.MessageEmbed{{
			Title:       "Session Recap",
			Description: summary,
			Color:       recapColor,
			Fields:      fields,
			Footer:      &discordgo.MessageEmbedFooter{Text: "Session recap"},
			Timestamp:   time.Now().UTC().Format(time.RFC3339),
		}}
	}

	// Split summary across multiple embeds.
	var embeds []*discordgo.MessageEmbed
	remaining := summary
	first := true
	for len(remaining) > 0 {
		chunk := remaining
		if len(chunk) > maxEmbedDescriptionLen {
			chunk = remaining[:maxEmbedDescriptionLen]
			remaining = remaining[maxEmbedDescriptionLen:]
		} else {
			remaining = ""
		}

		embed := &discordgo.MessageEmbed{
			Description: chunk,
			Color:       recapColor,
		}
		if first {
			embed.Title = "Session Recap"
			embed.Fields = fields
			first = false
		}
		if remaining == "" {
			embed.Footer = &discordgo.MessageEmbedFooter{Text: "Session recap"}
			embed.Timestamp = time.Now().UTC().Format(time.RFC3339)
		}
		embeds = append(embeds, embed)
	}
	return embeds
}
