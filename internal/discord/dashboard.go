package discord

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

// SessionData provides the data needed to render a dashboard embed.
// This interface decouples the dashboard from the SessionManager implementation.
type SessionData interface {
	IsActive() bool
	SessionID() string
	CampaignName() string
	StartedAt() time.Time
	NPCCount() int
	MutedNPCCount() int
	MemoryEntries() int
}

// embedColorGreen is the embed sidebar color for an active session.
const embedColorGreen = 0x2ECC71

// embedColorRed is the embed sidebar color when a session has ended.
const embedColorRed = 0xE74C3C

// defaultInterval is the default dashboard update interval.
const defaultInterval = 10 * time.Second

// Dashboard renders and periodically updates a Discord embed showing
// live session metrics. The embed is created on Start and edited in place
// every update interval.
//
// Thread-safe for concurrent use.
type Dashboard struct {
	mu        sync.Mutex
	session   *discordgo.Session
	channelID string
	messageID string // embed message; created on first update
	interval  time.Duration
	getData   func() SessionData
	stats     *PipelineStats
	done      chan struct{}
	stopOnce  sync.Once
}

// DashboardConfig holds dependencies for creating a Dashboard.
type DashboardConfig struct {
	Session   *discordgo.Session
	ChannelID string
	Interval  time.Duration // Default: 10 seconds
	GetData   func() SessionData
	Stats     *PipelineStats
}

// NewDashboard creates a Dashboard.
func NewDashboard(cfg DashboardConfig) *Dashboard {
	interval := cfg.Interval
	if interval == 0 {
		interval = defaultInterval
	}
	return &Dashboard{
		session:   cfg.Session,
		channelID: cfg.ChannelID,
		interval:  interval,
		getData:   cfg.GetData,
		stats:     cfg.Stats,
		done:      make(chan struct{}),
	}
}

// Stats returns the pipeline stats collector for this dashboard,
// allowing callers to record latency and counter values.
func (d *Dashboard) Stats() *PipelineStats {
	return d.stats
}

// Start begins the periodic update loop in a background goroutine.
func (d *Dashboard) Start(ctx context.Context) {
	go d.loop(ctx)
}

// Stop halts the periodic update loop and posts a final "session ended" embed.
func (d *Dashboard) Stop(ctx context.Context) {
	d.stopOnce.Do(func() {
		close(d.done)
		d.postFinalEmbed(ctx)
	})
}

// loop runs the periodic embed update until Stop is called or ctx is cancelled.
func (d *Dashboard) loop(ctx context.Context) {
	// Post immediately on start.
	d.update(ctx)

	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()

	for {
		select {
		case <-d.done:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.update(ctx)
		}
	}
}

// update builds the embed from current data and creates or edits the message.
func (d *Dashboard) update(ctx context.Context) {
	data := d.getData()
	var snap Snapshot
	if d.stats != nil {
		snap = d.stats.Snapshot()
	}
	embed := buildEmbed(data, snap)

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.messageID == "" {
		msg, err := d.session.ChannelMessageSendEmbed(d.channelID, embed)
		if err != nil {
			slog.Warn("dashboard: failed to create embed message", "channel", d.channelID, "err", err)
			return
		}
		d.messageID = msg.ID
		slog.Debug("dashboard: created embed message", "message_id", msg.ID, "channel", d.channelID)
	} else {
		_, err := d.session.ChannelMessageEditEmbed(d.channelID, d.messageID, embed)
		if err != nil {
			slog.Warn("dashboard: failed to edit embed message", "message_id", d.messageID, "err", err)
		}
	}

	_ = ctx // reserved for future context-aware API calls
}

// postFinalEmbed posts a "session ended" version of the embed.
func (d *Dashboard) postFinalEmbed(_ context.Context) {
	data := d.getData()
	var snap Snapshot
	if d.stats != nil {
		snap = d.stats.Snapshot()
	}
	embed := buildEndedEmbed(data, snap)

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.messageID == "" {
		return
	}
	_, err := d.session.ChannelMessageEditEmbed(d.channelID, d.messageID, embed)
	if err != nil {
		slog.Warn("dashboard: failed to post final embed", "message_id", d.messageID, "err", err)
	}
}

// buildEmbed creates the live dashboard embed from session data and pipeline stats.
func buildEmbed(data SessionData, snap Snapshot) *discordgo.MessageEmbed {
	duration := time.Since(data.StartedAt()).Truncate(time.Second)
	npcField := fmt.Sprintf("%d", data.NPCCount())
	if muted := data.MutedNPCCount(); muted > 0 {
		npcField = fmt.Sprintf("%d (%d muted)", data.NPCCount(), muted)
	}

	fields := []*discordgo.MessageEmbedField{
		{Name: "Campaign", Value: data.CampaignName(), Inline: true},
		{Name: "Session ID", Value: fmt.Sprintf("`%s`", data.SessionID()), Inline: true},
		{Name: "Duration", Value: duration.String(), Inline: true},
		{Name: "Active NPCs", Value: npcField, Inline: true},
		{Name: "Utterances", Value: fmt.Sprintf("%d", snap.Utterances), Inline: true},
		{Name: "Errors", Value: fmt.Sprintf("%d", snap.Errors), Inline: true},
	}

	// Add latency fields if we have samples.
	if latency := formatLatencyField(snap); latency != "" {
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:   "Pipeline Latency",
			Value:  latency,
			Inline: false,
		})
	}

	// Add memory entry count.
	fields = append(fields, &discordgo.MessageEmbedField{
		Name:   "Memory Entries",
		Value:  fmt.Sprintf("%d", data.MemoryEntries()),
		Inline: true,
	})

	return &discordgo.MessageEmbed{
		Title:  "Session Dashboard",
		Color:  embedColorGreen,
		Fields: fields,
		Footer: &discordgo.MessageEmbedFooter{
			Text: "Live session",
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
}

// buildEndedEmbed creates the final "session ended" embed.
func buildEndedEmbed(data SessionData, snap Snapshot) *discordgo.MessageEmbed {
	duration := time.Since(data.StartedAt()).Truncate(time.Second)

	fields := []*discordgo.MessageEmbedField{
		{Name: "Campaign", Value: data.CampaignName(), Inline: true},
		{Name: "Session ID", Value: fmt.Sprintf("`%s`", data.SessionID()), Inline: true},
		{Name: "Duration", Value: duration.String(), Inline: true},
		{Name: "Utterances", Value: fmt.Sprintf("%d", snap.Utterances), Inline: true},
		{Name: "Errors", Value: fmt.Sprintf("%d", snap.Errors), Inline: true},
		{Name: "Memory Entries", Value: fmt.Sprintf("%d", data.MemoryEntries()), Inline: true},
	}

	return &discordgo.MessageEmbed{
		Title:       "Session Dashboard",
		Description: "Session has ended.",
		Color:       embedColorRed,
		Fields:      fields,
		Footer: &discordgo.MessageEmbedFooter{
			Text: "Session ended",
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
}

// formatLatencyField builds a compact multi-line string showing pipeline
// latencies. Returns empty string if no latency data is available.
func formatLatencyField(snap Snapshot) string {
	var lines []string
	if snap.STT.P50 > 0 || snap.STT.P95 > 0 {
		lines = append(lines, fmt.Sprintf("STT: p50=%s p95=%s", formatMs(snap.STT.P50), formatMs(snap.STT.P95)))
	}
	if snap.LLM.P50 > 0 || snap.LLM.P95 > 0 {
		lines = append(lines, fmt.Sprintf("LLM: p50=%s p95=%s", formatMs(snap.LLM.P50), formatMs(snap.LLM.P95)))
	}
	if snap.TTS.P50 > 0 || snap.TTS.P95 > 0 {
		lines = append(lines, fmt.Sprintf("TTS: p50=%s p95=%s", formatMs(snap.TTS.P50), formatMs(snap.TTS.P95)))
	}
	if snap.S2S.P50 > 0 || snap.S2S.P95 > 0 {
		lines = append(lines, fmt.Sprintf("Total: p50=%s p95=%s", formatMs(snap.S2S.P50), formatMs(snap.S2S.P95)))
	}
	if len(lines) == 0 {
		return ""
	}
	var result strings.Builder
	result.WriteString("```\n")
	for _, line := range lines {
		result.WriteString(line + "\n")
	}
	result.WriteString("```")
	return result.String()
}

// formatMs formats a duration as milliseconds with one decimal place.
func formatMs(d time.Duration) string {
	ms := float64(d) / float64(time.Millisecond)
	return fmt.Sprintf("%.1fms", ms)
}

// formatDuration formats a duration as "Xh Ym Zs".
func formatDuration(d time.Duration) string {
	d = d.Truncate(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60

	if h > 0 {
		return fmt.Sprintf("%dh %dm %ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}
