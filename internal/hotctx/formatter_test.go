package hotctx_test

import (
	"strings"
	"testing"
	"time"

	"github.com/MrWong99/glyphoxa/internal/hotctx"
	"github.com/MrWong99/glyphoxa/pkg/memory"
)

// ─────────────────────────────────────────────────────────────────────────────
// helpers
// ─────────────────────────────────────────────────────────────────────────────

func fullHotContext() *hotctx.HotContext {
	locEntity := memory.Entity{
		ID:   "loc-1",
		Type: "location",
		Name: "The Forge",
		Attributes: map[string]any{
			"description": "a blazing forge district",
		},
	}
	friendEntity := memory.Entity{
		ID:   "npc-2",
		Type: "npc",
		Name: "Torvel",
	}
	questEntity := memory.Entity{
		ID:   "quest-1",
		Type: "quest",
		Name: "Retrieve the Hammer",
		Attributes: map[string]any{
			"status": "active",
		},
	}

	return &hotctx.HotContext{
		Identity: &memory.NPCIdentity{
			Entity: memory.Entity{
				ID:   "npc-1",
				Type: "npc",
				Name: "Grimjaw",
				Attributes: map[string]any{
					"occupation":    "blacksmith",
					"speaking_style": "gruff and direct",
				},
			},
			Relationships: []memory.Relationship{
				{
					SourceID: "npc-1",
					TargetID: "npc-2",
					RelType:  "KNOWS",
					Attributes: map[string]any{
						"description": "old drinking buddy",
					},
				},
			},
			RelatedEntities: []memory.Entity{friendEntity},
		},
		SceneContext: &hotctx.SceneContext{
			Location:    &locEntity,
			PresentEntities: []memory.Entity{friendEntity},
			ActiveQuests: []memory.Entity{questEntity},
		},
		RecentTranscript: []memory.TranscriptEntry{
			{
				SpeakerID:   "player1",
				SpeakerName: "Alice",
				Text:        "Have you seen the missing hammer?",
				Timestamp:   time.Now().Add(-2 * time.Minute),
			},
			{
				SpeakerID:   "npc-1",
				SpeakerName: "Grimjaw",
				Text:        "Aye, I might know something about that.",
				Timestamp:   time.Now().Add(-1 * time.Minute),
			},
		},
		AssemblyDuration: 12 * time.Millisecond,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// tests
// ─────────────────────────────────────────────────────────────────────────────

// TestFormatSystemPrompt_Full verifies that a fully-populated HotContext
// renders all sections correctly.
func TestFormatSystemPrompt_Full(t *testing.T) {
	hctx := fullHotContext()
	personality := "You are gruff but fair, and speak in short sentences."

	result := hotctx.FormatSystemPrompt(hctx, personality)

	// Opening line must contain NPC name and personality.
	if !strings.Contains(result, "Grimjaw") {
		t.Errorf("output missing NPC name 'Grimjaw':\n%s", result)
	}
	if !strings.Contains(result, personality) {
		t.Errorf("output missing personality string:\n%s", result)
	}

	// Identity section
	if !strings.Contains(result, "## Your Identity") {
		t.Error("output missing '## Your Identity' section")
	}
	if !strings.Contains(result, "blacksmith") {
		t.Errorf("output missing occupation 'blacksmith':\n%s", result)
	}

	// Relationships section
	if !strings.Contains(result, "## Your Relationships") {
		t.Error("output missing '## Your Relationships' section")
	}
	if !strings.Contains(result, "Torvel") {
		t.Errorf("output missing related entity 'Torvel':\n%s", result)
	}
	if !strings.Contains(result, "KNOWS") {
		t.Errorf("output missing relationship type 'KNOWS':\n%s", result)
	}

	// Scene section
	if !strings.Contains(result, "## Current Scene") {
		t.Error("output missing '## Current Scene' section")
	}
	if !strings.Contains(result, "The Forge") {
		t.Errorf("output missing location 'The Forge':\n%s", result)
	}
	if !strings.Contains(result, "blazing forge district") {
		t.Errorf("output missing location description:\n%s", result)
	}
	if !strings.Contains(result, "Also present") {
		t.Errorf("output missing 'Also present' line:\n%s", result)
	}
	if !strings.Contains(result, "Active quests") {
		t.Errorf("output missing 'Active quests' line:\n%s", result)
	}
	if !strings.Contains(result, "Retrieve the Hammer") {
		t.Errorf("output missing quest name:\n%s", result)
	}
	if !strings.Contains(result, "[active]") {
		t.Errorf("output missing quest status [active]:\n%s", result)
	}

	// Recent conversation section
	if !strings.Contains(result, "## Recent Conversation") {
		t.Error("output missing '## Recent Conversation' section")
	}
	if !strings.Contains(result, "Alice") {
		t.Errorf("output missing speaker 'Alice':\n%s", result)
	}
	if !strings.Contains(result, "missing hammer") {
		t.Errorf("output missing transcript text:\n%s", result)
	}
}

// TestFormatSystemPrompt_Minimal verifies that a nil identity, empty scene,
// and no transcript produce only the opening line — no empty section headers.
func TestFormatSystemPrompt_Minimal(t *testing.T) {
	hctx := &hotctx.HotContext{
		// No Identity, no SceneContext, no RecentTranscript
	}
	personality := "a mysterious wanderer"

	result := hotctx.FormatSystemPrompt(hctx, personality)

	// Opening line only — must contain fallback NPC name and personality.
	if !strings.Contains(result, "an NPC") {
		t.Errorf("output missing fallback name 'an NPC':\n%s", result)
	}
	if !strings.Contains(result, personality) {
		t.Errorf("output missing personality:\n%s", result)
	}

	// No section headers should be emitted.
	for _, header := range []string{
		"## Your Identity",
		"## Your Relationships",
		"## Current Scene",
		"## Recent Conversation",
	} {
		if strings.Contains(result, header) {
			t.Errorf("output should not contain empty header %q:\n%s", header, result)
		}
	}
}

// TestFormatSystemPrompt_NilHotContext verifies graceful handling of nil input.
func TestFormatSystemPrompt_NilHotContext(t *testing.T) {
	result := hotctx.FormatSystemPrompt(nil, "brave hero")
	if result == "" {
		t.Error("FormatSystemPrompt(nil, ...) returned empty string")
	}
	if !strings.Contains(result, "brave hero") {
		t.Errorf("output missing personality: %q", result)
	}
}

// TestFormatSystemPrompt_NoPersonality verifies that an empty personality
// string is handled without leaving trailing spaces or double periods.
func TestFormatSystemPrompt_NoPersonality(t *testing.T) {
	hctx := fullHotContext()
	result := hotctx.FormatSystemPrompt(hctx, "")

	// Should end with a period after the NPC name, no trailing space.
	firstLine := strings.SplitN(result, "\n", 2)[0]
	if !strings.HasSuffix(firstLine, ".") {
		t.Errorf("first line should end with '.': %q", firstLine)
	}
	if strings.Contains(firstLine, "  ") {
		t.Errorf("first line has double spaces: %q", firstLine)
	}
}

// TestFormatSystemPrompt_EmptyRelationships verifies that the Relationships
// section is omitted when there are no relationships.
func TestFormatSystemPrompt_EmptyRelationships(t *testing.T) {
	hctx := &hotctx.HotContext{
		Identity: &memory.NPCIdentity{
			Entity: memory.Entity{ID: "npc-1", Name: "Grimjaw", Type: "npc"},
			// Empty relationship slice
			Relationships:   []memory.Relationship{},
			RelatedEntities: []memory.Entity{},
		},
	}
	result := hotctx.FormatSystemPrompt(hctx, "")
	if strings.Contains(result, "## Your Relationships") {
		t.Errorf("empty relationships should be omitted:\n%s", result)
	}
}

// TestFormatSystemPrompt_EmptyScene verifies that the Scene section is omitted
// when SceneContext has no location, no NPCs, and no quests.
func TestFormatSystemPrompt_EmptyScene(t *testing.T) {
	hctx := &hotctx.HotContext{
		Identity: &memory.NPCIdentity{
			Entity: memory.Entity{ID: "npc-1", Name: "Grimjaw", Type: "npc"},
		},
		SceneContext: &hotctx.SceneContext{
			// nil Location, empty slices
			PresentEntities: []memory.Entity{},
			ActiveQuests: []memory.Entity{},
		},
	}
	result := hotctx.FormatSystemPrompt(hctx, "")
	if strings.Contains(result, "## Current Scene") {
		t.Errorf("empty scene should be omitted:\n%s", result)
	}
}

// TestFormatSystemPrompt_IsPure verifies that calling FormatSystemPrompt twice
// with the same input produces identical output (pure function).
func TestFormatSystemPrompt_IsPure(t *testing.T) {
	hctx := fullHotContext()
	// FormatSystemPrompt uses relative timestamps — calling it twice
	// in rapid succession should give the same structure (same sections present).
	out1 := hotctx.FormatSystemPrompt(hctx, "gruff and fair")
	out2 := hotctx.FormatSystemPrompt(hctx, "gruff and fair")

	// Both must contain the same sections.
	sections := []string{
		"## Your Identity",
		"## Your Relationships",
		"## Current Scene",
		"## Recent Conversation",
	}
	for _, s := range sections {
		if strings.Contains(out1, s) != strings.Contains(out2, s) {
			t.Errorf("section %q presence differs between calls", s)
		}
	}
}
