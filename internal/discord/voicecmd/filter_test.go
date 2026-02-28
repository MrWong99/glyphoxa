package voicecmd

import (
	"context"
	"testing"

	"github.com/MrWong99/glyphoxa/internal/agent"
	"github.com/MrWong99/glyphoxa/internal/agent/mock"
	"github.com/MrWong99/glyphoxa/internal/agent/orchestrator"
)

// agentSlice converts mock NPCAgents to the agent.NPCAgent interface slice
// expected by orchestrator.New.
func agentSlice(mocks ...*mock.NPCAgent) []agent.NPCAgent {
	agents := make([]agent.NPCAgent, len(mocks))
	for i, m := range mocks {
		agents[i] = m
	}
	return agents
}

func TestFilter_NonDMIgnored(t *testing.T) {
	t.Parallel()

	f := New("dm-user-123")
	orch := orchestrator.New(nil)

	matched, err := f.Check(context.Background(), "player-456", "mute Grimjaw", orch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if matched {
		t.Error("expected non-DM transcript to be ignored")
	}
}

func TestFilter_EmptyDMUserID(t *testing.T) {
	t.Parallel()

	f := New("")
	orch := orchestrator.New(nil)

	matched, err := f.Check(context.Background(), "anyone", "mute Grimjaw", orch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if matched {
		t.Error("expected empty DM user ID to match no one")
	}
}

func TestFilter_EmptyText(t *testing.T) {
	t.Parallel()

	f := New("dm-user")
	orch := orchestrator.New(nil)

	matched, err := f.Check(context.Background(), "dm-user", "", orch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if matched {
		t.Error("expected empty text to not match")
	}
}

func TestFilter_NoMatch(t *testing.T) {
	t.Parallel()

	f := New("dm-user")
	orch := orchestrator.New(nil)

	matched, err := f.Check(context.Background(), "dm-user", "hello there", orch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if matched {
		t.Error("expected non-command text to not match")
	}
}

func TestFilter_SetDMUserID(t *testing.T) {
	t.Parallel()

	f := New("old-dm")
	f.SetDMUserID("new-dm")
	orch := orchestrator.New(nil)

	matched, _ := f.Check(context.Background(), "old-dm", "everyone, stop", orch)
	if matched {
		t.Error("expected old DM to not match after SetDMUserID")
	}
}

func TestFilter_PatternMatching(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		text    string
		matches bool
	}{
		{"mute by name", "mute Grimjaw", true},
		{"mute by name uppercase", "Mute GRIMJAW", true},
		{"be quiet", "Grimjaw, be quiet", true},
		{"be quiet no comma", "Grimjaw be quiet", true},
		{"unmute by name", "unmute Grimjaw", true},
		{"everyone stop", "everyone, stop", true},
		{"everyone stop no comma", "everyone stop", true},
		{"everyone continue", "everyone, continue", true},
		{"speak as", "Grimjaw, say Hello adventurers", true},
		{"speak as no comma", "Grimjaw say Welcome", true},
		{"no match regular speech", "I attack the dragon", false},
		{"no match partial command", "stop", false},
		{"no match just name", "Grimjaw", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := New("dm-user")
			orch := orchestrator.New(nil)

			matched, _ := f.Check(context.Background(), "dm-user", tt.text, orch)
			if matched != tt.matches {
				t.Errorf("text %q: got matched=%v, want %v", tt.text, matched, tt.matches)
			}
		})
	}
}

func TestFilter_MuteEveryoneWithAgents(t *testing.T) {
	t.Parallel()

	grimjaw := &mock.NPCAgent{IDResult: "grimjaw", NameResult: "Grimjaw"}
	elara := &mock.NPCAgent{IDResult: "elara", NameResult: "Elara"}

	orch := orchestrator.New(agentSlice(grimjaw, elara))

	f := New("dm-user")
	matched, err := f.Check(context.Background(), "dm-user", "everyone, stop", orch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !matched {
		t.Error("expected 'everyone, stop' to match")
	}

	muted1, _ := orch.IsMuted("grimjaw")
	muted2, _ := orch.IsMuted("elara")
	if !muted1 || !muted2 {
		t.Errorf("expected all agents muted, got grimjaw=%v elara=%v", muted1, muted2)
	}
}

func TestFilter_MuteByNameWithAgents(t *testing.T) {
	t.Parallel()

	grimjaw := &mock.NPCAgent{IDResult: "grimjaw", NameResult: "Grimjaw"}
	elara := &mock.NPCAgent{IDResult: "elara", NameResult: "Elara"}

	orch := orchestrator.New(agentSlice(grimjaw, elara))

	f := New("dm-user")
	matched, err := f.Check(context.Background(), "dm-user", "mute Grimjaw", orch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !matched {
		t.Error("expected 'mute Grimjaw' to match")
	}

	muted, _ := orch.IsMuted("grimjaw")
	if !muted {
		t.Error("expected Grimjaw to be muted")
	}

	mutedElara, _ := orch.IsMuted("elara")
	if mutedElara {
		t.Error("expected Elara to remain unmuted")
	}
}

func TestFilter_UnmuteByName(t *testing.T) {
	t.Parallel()

	grimjaw := &mock.NPCAgent{IDResult: "grimjaw", NameResult: "Grimjaw"}
	orch := orchestrator.New(agentSlice(grimjaw))

	// Mute first.
	_ = orch.MuteAgent("grimjaw")

	f := New("dm-user")
	matched, err := f.Check(context.Background(), "dm-user", "unmute Grimjaw", orch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !matched {
		t.Error("expected 'unmute Grimjaw' to match")
	}

	muted, _ := orch.IsMuted("grimjaw")
	if muted {
		t.Error("expected Grimjaw to be unmuted")
	}
}

func TestFilter_SpeakAs(t *testing.T) {
	t.Parallel()

	grimjaw := &mock.NPCAgent{IDResult: "grimjaw", NameResult: "Grimjaw"}
	orch := orchestrator.New(agentSlice(grimjaw))

	f := New("dm-user")
	matched, err := f.Check(context.Background(), "dm-user", "Grimjaw, say Hello adventurers", orch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !matched {
		t.Error("expected speak-as to match")
	}

	if len(grimjaw.SpeakTextCalls) != 1 {
		t.Fatalf("expected 1 SpeakText call, got %d", len(grimjaw.SpeakTextCalls))
	}
	if grimjaw.SpeakTextCalls[0] != "Hello adventurers" {
		t.Errorf("expected SpeakText(%q), got SpeakText(%q)", "Hello adventurers", grimjaw.SpeakTextCalls[0])
	}
}

func TestFilter_BeQuiet(t *testing.T) {
	t.Parallel()

	grimjaw := &mock.NPCAgent{IDResult: "grimjaw", NameResult: "Grimjaw"}
	orch := orchestrator.New(agentSlice(grimjaw))

	f := New("dm-user")
	matched, err := f.Check(context.Background(), "dm-user", "Grimjaw, be quiet", orch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !matched {
		t.Error("expected 'be quiet' to match")
	}

	muted, _ := orch.IsMuted("grimjaw")
	if muted != true {
		t.Error("expected Grimjaw to be muted after 'be quiet'")
	}
}

func TestFilter_UnknownNPCReturnsError(t *testing.T) {
	t.Parallel()

	orch := orchestrator.New(nil)

	f := New("dm-user")
	matched, err := f.Check(context.Background(), "dm-user", "mute NonExistentNPC", orch)
	if !matched {
		t.Error("expected pattern to match even if NPC is unknown")
	}
	if err == nil {
		t.Error("expected error for unknown NPC")
	}
}
