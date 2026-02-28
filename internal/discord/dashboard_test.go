package discord

import (
	"testing"
	"time"
)

// stubSessionData implements SessionData for testing.
type stubSessionData struct {
	active        bool
	sessionID     string
	campaignName  string
	startedAt     time.Time
	npcCount      int
	mutedCount    int
	memoryEntries int
}

func (s *stubSessionData) IsActive() bool       { return s.active }
func (s *stubSessionData) SessionID() string    { return s.sessionID }
func (s *stubSessionData) CampaignName() string { return s.campaignName }
func (s *stubSessionData) StartedAt() time.Time { return s.startedAt }
func (s *stubSessionData) NPCCount() int        { return s.npcCount }
func (s *stubSessionData) MutedNPCCount() int   { return s.mutedCount }
func (s *stubSessionData) MemoryEntries() int   { return s.memoryEntries }

func TestBuildEmbed(t *testing.T) {
	t.Parallel()

	data := &stubSessionData{
		active:       true,
		sessionID:    "session-test-123",
		campaignName: "Lost Mines",
		startedAt:    time.Now().Add(-5 * time.Minute),
		npcCount:     3,
		mutedCount:   1,
	}

	embed := buildEmbed(data, Snapshot{})

	if embed.Title != "Session Dashboard" {
		t.Errorf("Title = %q, want %q", embed.Title, "Session Dashboard")
	}
	if embed.Color != embedColorGreen {
		t.Errorf("Color = %d, want %d", embed.Color, embedColorGreen)
	}
	if embed.Fields[0].Name != "Campaign" || embed.Fields[0].Value != "Lost Mines" {
		t.Errorf("Field[0] = %q:%q, want Campaign:Lost Mines", embed.Fields[0].Name, embed.Fields[0].Value)
	}
	if embed.Fields[1].Name != "Session ID" || embed.Fields[1].Value != "`session-test-123`" {
		t.Errorf("Field[1] = %q:%q, want Session ID:`session-test-123`", embed.Fields[1].Name, embed.Fields[1].Value)
	}
	if embed.Fields[3].Name != "Active NPCs" || embed.Fields[3].Value != "3 (1 muted)" {
		t.Errorf("Field[3] = %q:%q, want Active NPCs:3 (1 muted)", embed.Fields[3].Name, embed.Fields[3].Value)
	}
	if embed.Footer == nil || embed.Footer.Text != "Live session" {
		t.Errorf("Footer = %v, want 'Live session'", embed.Footer)
	}
}

func TestBuildEmbed_NoMuted(t *testing.T) {
	t.Parallel()

	data := &stubSessionData{
		active:       true,
		sessionID:    "session-test-456",
		campaignName: "Dragon Heist",
		startedAt:    time.Now().Add(-10 * time.Minute),
		npcCount:     2,
		mutedCount:   0,
	}

	embed := buildEmbed(data, Snapshot{})

	if embed.Fields[3].Value != "2" {
		t.Errorf("NPC field = %q, want %q (no muted suffix)", embed.Fields[3].Value, "2")
	}
}

func TestBuildEndedEmbed(t *testing.T) {
	t.Parallel()

	data := &stubSessionData{
		active:       false,
		sessionID:    "session-test-789",
		campaignName: "Curse of Strahd",
		startedAt:    time.Now().Add(-1 * time.Hour),
		npcCount:     4,
		mutedCount:   0,
	}

	embed := buildEndedEmbed(data, Snapshot{})

	if embed.Title != "Session Dashboard" {
		t.Errorf("Title = %q, want %q", embed.Title, "Session Dashboard")
	}
	if embed.Color != embedColorRed {
		t.Errorf("Color = %d, want %d", embed.Color, embedColorRed)
	}
	if embed.Description != "Session has ended." {
		t.Errorf("Description = %q, want %q", embed.Description, "Session has ended.")
	}
	if embed.Footer == nil || embed.Footer.Text != "Session ended" {
		t.Errorf("Footer = %v, want 'Session ended'", embed.Footer)
	}
}

func TestDashboard_StartStop(t *testing.T) {
	t.Parallel()

	data := &stubSessionData{
		active:       true,
		sessionID:    "session-lifecycle",
		campaignName: "Test Campaign",
		startedAt:    time.Now(),
		npcCount:     1,
		mutedCount:   0,
	}

	cfg := DashboardConfig{
		Session:   nil,
		ChannelID: "test-channel",
		Interval:  50 * time.Millisecond,
		GetData:   func() SessionData { return data },
	}

	d := NewDashboard(cfg)

	if d.interval != 50*time.Millisecond {
		t.Errorf("interval = %v, want 50ms", d.interval)
	}
	if d.channelID != "test-channel" {
		t.Errorf("channelID = %q, want %q", d.channelID, "test-channel")
	}

	d2 := NewDashboard(DashboardConfig{
		ChannelID: "ch",
		GetData:   func() SessionData { return data },
	})
	if d2.interval != defaultInterval {
		t.Errorf("default interval = %v, want %v", d2.interval, defaultInterval)
	}
}

func TestFormatDuration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		d    time.Duration
		want string
	}{
		{"seconds only", 45 * time.Second, "45s"},
		{"minutes and seconds", 3*time.Minute + 15*time.Second, "3m 15s"},
		{"hours minutes seconds", 2*time.Hour + 30*time.Minute + 5*time.Second, "2h 30m 5s"},
		{"zero", 0, "0s"},
		{"sub-second truncated", 500 * time.Millisecond, "0s"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := formatDuration(tt.d)
			if got != tt.want {
				t.Errorf("formatDuration(%v) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}
}

func TestPipelineStats_Snapshot(t *testing.T) {
	t.Parallel()

	ps := NewPipelineStats(100)
	ps.RecordSTT(50 * time.Millisecond)
	ps.RecordSTT(100 * time.Millisecond)
	ps.RecordLLM(200 * time.Millisecond)
	ps.IncrUtterances()
	ps.IncrUtterances()
	ps.IncrErrors()

	snap := ps.Snapshot()
	if snap.Utterances != 2 {
		t.Errorf("Utterances = %d, want 2", snap.Utterances)
	}
	if snap.Errors != 1 {
		t.Errorf("Errors = %d, want 1", snap.Errors)
	}
	if snap.STT.P50 == 0 {
		t.Error("expected non-zero STT p50")
	}
	if snap.LLM.P50 == 0 {
		t.Error("expected non-zero LLM p50")
	}
}

func TestFormatLatencyField_Empty(t *testing.T) {
	t.Parallel()

	result := formatLatencyField(Snapshot{})
	if result != "" {
		t.Errorf("expected empty string for zero snapshot, got %q", result)
	}
}

func TestFormatMs(t *testing.T) {
	t.Parallel()

	got := formatMs(150 * time.Millisecond)
	if got != "150.0ms" {
		t.Errorf("formatMs(150ms) = %q, want %q", got, "150.0ms")
	}
}
