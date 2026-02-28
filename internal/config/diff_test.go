package config_test

import (
	"testing"

	"github.com/MrWong99/glyphoxa/internal/config"
)

func TestDiff_NoChanges(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Server: config.ServerConfig{LogLevel: config.LogInfo},
		NPCs: []config.NPCConfig{
			{Name: "Alice", Personality: "kind", BudgetTier: config.BudgetTierFast},
		},
	}
	d := config.Diff(cfg, cfg)
	if d.NPCsChanged {
		t.Error("expected NPCsChanged=false for identical configs")
	}
	if d.LogLevelChanged {
		t.Error("expected LogLevelChanged=false for identical configs")
	}
	if len(d.NPCChanges) != 0 {
		t.Errorf("expected 0 NPC changes, got %d", len(d.NPCChanges))
	}
}

func TestDiff_LogLevelChanged(t *testing.T) {
	t.Parallel()
	old := &config.Config{Server: config.ServerConfig{LogLevel: config.LogInfo}}
	new := &config.Config{Server: config.ServerConfig{LogLevel: config.LogDebug}}

	d := config.Diff(old, new)
	if !d.LogLevelChanged {
		t.Error("expected LogLevelChanged=true")
	}
	if d.NewLogLevel != config.LogDebug {
		t.Errorf("expected NewLogLevel=debug, got %q", d.NewLogLevel)
	}
}

func TestDiff_NPCPersonalityChanged(t *testing.T) {
	t.Parallel()
	old := &config.Config{
		NPCs: []config.NPCConfig{
			{Name: "Bob", Personality: "grumpy"},
		},
	}
	new := &config.Config{
		NPCs: []config.NPCConfig{
			{Name: "Bob", Personality: "cheerful"},
		},
	}

	d := config.Diff(old, new)
	if !d.NPCsChanged {
		t.Error("expected NPCsChanged=true")
	}
	if len(d.NPCChanges) != 1 {
		t.Fatalf("expected 1 NPC change, got %d", len(d.NPCChanges))
	}
	if !d.NPCChanges[0].PersonalityChanged {
		t.Error("expected PersonalityChanged=true")
	}
	if d.NPCChanges[0].VoiceChanged {
		t.Error("expected VoiceChanged=false")
	}
}

func TestDiff_NPCVoiceChanged(t *testing.T) {
	t.Parallel()
	old := &config.Config{
		NPCs: []config.NPCConfig{
			{Name: "Carol", Voice: config.VoiceConfig{VoiceID: "v1"}},
		},
	}
	new := &config.Config{
		NPCs: []config.NPCConfig{
			{Name: "Carol", Voice: config.VoiceConfig{VoiceID: "v2"}},
		},
	}

	d := config.Diff(old, new)
	if !d.NPCsChanged {
		t.Error("expected NPCsChanged=true")
	}
	found := false
	for _, nc := range d.NPCChanges {
		if nc.Name == "Carol" && nc.VoiceChanged {
			found = true
		}
	}
	if !found {
		t.Error("expected Carol's VoiceChanged=true")
	}
}

func TestDiff_NPCBudgetTierChanged(t *testing.T) {
	t.Parallel()
	old := &config.Config{
		NPCs: []config.NPCConfig{
			{Name: "Dan", BudgetTier: config.BudgetTierFast},
		},
	}
	new := &config.Config{
		NPCs: []config.NPCConfig{
			{Name: "Dan", BudgetTier: config.BudgetTierDeep},
		},
	}

	d := config.Diff(old, new)
	if !d.NPCsChanged {
		t.Error("expected NPCsChanged=true")
	}
	found := false
	for _, nc := range d.NPCChanges {
		if nc.Name == "Dan" && nc.BudgetTierChanged {
			found = true
		}
	}
	if !found {
		t.Error("expected Dan's BudgetTierChanged=true")
	}
}

func TestDiff_NPCAdded(t *testing.T) {
	t.Parallel()
	old := &config.Config{
		NPCs: []config.NPCConfig{
			{Name: "Eve"},
		},
	}
	new := &config.Config{
		NPCs: []config.NPCConfig{
			{Name: "Eve"},
			{Name: "Frank"},
		},
	}

	d := config.Diff(old, new)
	if !d.NPCsChanged {
		t.Error("expected NPCsChanged=true")
	}
	found := false
	for _, nc := range d.NPCChanges {
		if nc.Name == "Frank" && nc.Added {
			found = true
		}
	}
	if !found {
		t.Error("expected Frank Added=true")
	}
}

func TestDiff_NPCRemoved(t *testing.T) {
	t.Parallel()
	old := &config.Config{
		NPCs: []config.NPCConfig{
			{Name: "Grace"},
			{Name: "Hank"},
		},
	}
	new := &config.Config{
		NPCs: []config.NPCConfig{
			{Name: "Grace"},
		},
	}

	d := config.Diff(old, new)
	if !d.NPCsChanged {
		t.Error("expected NPCsChanged=true")
	}
	found := false
	for _, nc := range d.NPCChanges {
		if nc.Name == "Hank" && nc.Removed {
			found = true
		}
	}
	if !found {
		t.Error("expected Hank Removed=true")
	}
}

func TestDiff_MultipleChanges(t *testing.T) {
	t.Parallel()
	old := &config.Config{
		Server: config.ServerConfig{LogLevel: config.LogInfo},
		NPCs: []config.NPCConfig{
			{Name: "A", Personality: "p1"},
			{Name: "B", BudgetTier: config.BudgetTierFast},
		},
	}
	new := &config.Config{
		Server: config.ServerConfig{LogLevel: config.LogWarn},
		NPCs: []config.NPCConfig{
			{Name: "A", Personality: "p2"},
			{Name: "C"},
		},
	}

	d := config.Diff(old, new)
	if !d.LogLevelChanged {
		t.Error("expected LogLevelChanged=true")
	}
	if !d.NPCsChanged {
		t.Error("expected NPCsChanged=true")
	}
	// A: personality changed, B: removed, C: added
	changes := make(map[string]config.NPCDiff)
	for _, nc := range d.NPCChanges {
		changes[nc.Name] = nc
	}
	if !changes["A"].PersonalityChanged {
		t.Error("expected A PersonalityChanged=true")
	}
	if !changes["B"].Removed {
		t.Error("expected B Removed=true")
	}
	if !changes["C"].Added {
		t.Error("expected C Added=true")
	}
}
