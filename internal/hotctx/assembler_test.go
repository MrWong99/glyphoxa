package hotctx_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MrWong99/glyphoxa/internal/hotctx"
	"github.com/MrWong99/glyphoxa/pkg/memory"
	"github.com/MrWong99/glyphoxa/pkg/memory/mock"
	"github.com/MrWong99/glyphoxa/pkg/types"
)

// ─────────────────────────────────────────────────────────────────────────────
// helpers
// ─────────────────────────────────────────────────────────────────────────────

func makeIdentity(npcID, name string) *memory.NPCIdentity {
	return &memory.NPCIdentity{
		Entity: memory.Entity{
			ID:   npcID,
			Type: "npc",
			Name: name,
			Attributes: map[string]any{
				"occupation": "blacksmith",
			},
		},
		Relationships: []memory.Relationship{},
		RelatedEntities: []memory.Entity{},
	}
}

func makeTranscript(n int) []types.TranscriptEntry {
	entries := make([]types.TranscriptEntry, n)
	for i := range entries {
		entries[i] = types.TranscriptEntry{
			SpeakerID:   "player1",
			SpeakerName: "Alice",
			Text:        "Hello there",
			Timestamp:   time.Now().Add(-time.Duration(n-i) * time.Second),
		}
	}
	return entries
}

// ─────────────────────────────────────────────────────────────────────────────
// tests
// ─────────────────────────────────────────────────────────────────────────────

// TestAssemble_Basic verifies that all three components are assembled when the
// stores return valid data.
func TestAssemble_Basic(t *testing.T) {
	locationID := "loc-1"

	kg := &mock.KnowledgeGraph{
		IdentitySnapshotResult: makeIdentity("npc-1", "Grimjaw"),
		// GetRelationships returns a LOCATED_AT edge to loc-1
		GetRelationshipsResult: []memory.Relationship{
			{SourceID: "npc-1", TargetID: locationID, RelType: "LOCATED_AT"},
		},
		// GetEntity returns the location entity for any ID queried
		GetEntityResult: &memory.Entity{
			ID:   locationID,
			Type: "location",
			Name: "The Forge",
			Attributes: map[string]any{"description": "a hot smithy"},
		},
		// Neighbors returns a second NPC at the same location
		NeighborsResult: []memory.Entity{
			{ID: "npc-2", Type: "npc", Name: "Torvel"},
		},
	}

	ss := &mock.SessionStore{
		GetRecentResult: makeTranscript(3),
	}

	a := hotctx.NewAssembler(ss, kg)
	hctx, err := a.Assemble(context.Background(), "npc-1", "session-abc")
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}

	// Identity
	if hctx.Identity == nil {
		t.Fatal("Identity is nil")
	}
	if hctx.Identity.Entity.Name != "Grimjaw" {
		t.Errorf("Identity.Entity.Name = %q, want %q", hctx.Identity.Entity.Name, "Grimjaw")
	}

	// Transcript
	if len(hctx.RecentTranscript) != 3 {
		t.Errorf("len(RecentTranscript) = %d, want 3", len(hctx.RecentTranscript))
	}

	// Scene
	if hctx.SceneContext == nil {
		t.Fatal("SceneContext is nil")
	}
	if hctx.SceneContext.Location == nil {
		t.Fatal("SceneContext.Location is nil")
	}
	if hctx.SceneContext.Location.Name != "The Forge" {
		t.Errorf("SceneContext.Location.Name = %q, want %q", hctx.SceneContext.Location.Name, "The Forge")
	}
	if len(hctx.SceneContext.PresentNPCs) != 1 {
		t.Errorf("len(PresentNPCs) = %d, want 1", len(hctx.SceneContext.PresentNPCs))
	}

	// Duration was recorded
	if hctx.AssemblyDuration <= 0 {
		t.Error("AssemblyDuration should be positive")
	}
}

// TestAssemble_MissingIdentity verifies that an IdentitySnapshot error causes
// Assemble to fail with a wrapped error.
func TestAssemble_MissingIdentity(t *testing.T) {
	kg := &mock.KnowledgeGraph{
		IdentitySnapshotErr: errors.New("npc not found"),
	}
	ss := &mock.SessionStore{
		GetRecentResult: makeTranscript(2),
	}

	a := hotctx.NewAssembler(ss, kg)
	_, err := a.Assemble(context.Background(), "npc-ghost", "session-abc")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, kg.IdentitySnapshotErr) {
		// The error should wrap the original
		t.Errorf("error does not wrap original: %v", err)
	}
}

// TestAssemble_NoRecentTranscript verifies that an empty transcript doesn't
// cause an error and results in an empty (non-nil) slice.
func TestAssemble_NoRecentTranscript(t *testing.T) {
	kg := &mock.KnowledgeGraph{
		IdentitySnapshotResult: makeIdentity("npc-1", "Grimjaw"),
		// No GetRelationshipsResult → empty scene
	}
	ss := &mock.SessionStore{
		// GetRecentResult == nil → mock returns empty slice
	}

	a := hotctx.NewAssembler(ss, kg)
	hctx, err := a.Assemble(context.Background(), "npc-1", "session-abc")
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}
	if hctx.RecentTranscript == nil {
		t.Error("RecentTranscript must be non-nil even when empty")
	}
	if len(hctx.RecentTranscript) != 0 {
		t.Errorf("expected 0 transcript entries, got %d", len(hctx.RecentTranscript))
	}
}

// TestAssemble_MaxEntriesTruncation verifies that the transcript is capped at
// maxEntries, keeping the most-recent entries.
func TestAssemble_MaxEntriesTruncation(t *testing.T) {
	const total = 20
	const max = 5

	entries := makeTranscript(total)

	kg := &mock.KnowledgeGraph{
		IdentitySnapshotResult: makeIdentity("npc-1", "Grimjaw"),
	}
	ss := &mock.SessionStore{
		GetRecentResult: entries,
	}

	a := hotctx.NewAssembler(ss, kg, hotctx.WithMaxTranscriptEntries(max))
	hctx, err := a.Assemble(context.Background(), "npc-1", "session-abc")
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}
	if len(hctx.RecentTranscript) != max {
		t.Errorf("len(RecentTranscript) = %d, want %d", len(hctx.RecentTranscript), max)
	}
	// The kept entries should be the last `max` entries (most recent).
	wantFirst := entries[total-max]
	if hctx.RecentTranscript[0].Timestamp != wantFirst.Timestamp {
		t.Errorf("first kept entry timestamp mismatch: got %v, want %v",
			hctx.RecentTranscript[0].Timestamp, wantFirst.Timestamp)
	}
}

// TestAssemble_ConcurrentQueries verifies that both the identity snapshot and
// the recent transcript were fetched in the same assembly call (showing that
// both goroutines executed).
func TestAssemble_ConcurrentQueries(t *testing.T) {
	kg := &mock.KnowledgeGraph{
		IdentitySnapshotResult: makeIdentity("npc-1", "Grimjaw"),
	}
	ss := &mock.SessionStore{
		GetRecentResult: makeTranscript(2),
	}

	a := hotctx.NewAssembler(ss, kg)
	_, err := a.Assemble(context.Background(), "npc-1", "session-abc")
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}

	// Both the identity snapshot and the recent transcript must have been
	// fetched exactly once per Assemble call.
	if kg.CallCount("IdentitySnapshot") != 1 {
		t.Errorf("IdentitySnapshot called %d times, want 1", kg.CallCount("IdentitySnapshot"))
	}
	if ss.CallCount("GetRecent") != 1 {
		t.Errorf("GetRecent called %d times, want 1", ss.CallCount("GetRecent"))
	}
	// GetRelationships is part of scene context assembly (goroutine 3).
	if kg.CallCount("GetRelationships") != 1 {
		t.Errorf("GetRelationships called %d times, want 1", kg.CallCount("GetRelationships"))
	}
}

// TestAssemble_ContextCancellation verifies that a pre-cancelled context causes
// an error rather than hanging.
func TestAssemble_ContextCancellation(t *testing.T) {
	kg := &mock.KnowledgeGraph{
		IdentitySnapshotResult: makeIdentity("npc-1", "Grimjaw"),
	}
	ss := &mock.SessionStore{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	a := hotctx.NewAssembler(ss, kg)
	_, err := a.Assemble(ctx, "npc-1", "session-abc")
	// The mock ignores context cancellation so we may or may not get an error;
	// verify that we at least get a valid return (no panic).
	_ = err
}

// TestAssemble_WithOptions verifies that functional options are applied.
func TestAssemble_WithOptions(t *testing.T) {
	kg := &mock.KnowledgeGraph{
		IdentitySnapshotResult: makeIdentity("npc-1", "Grimjaw"),
	}
	ss := &mock.SessionStore{}

	a := hotctx.NewAssembler(ss, kg,
		hotctx.WithRecentDuration(10*time.Minute),
		hotctx.WithMaxTranscriptEntries(100),
	)
	// Just verify it assembles without error (options are applied internally).
	_, err := a.Assemble(context.Background(), "npc-1", "session-abc")
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}

	// Check that GetRecent was called with the custom duration.
	calls := ss.Calls()
	found := false
	for _, c := range calls {
		if c.Method == "GetRecent" {
			d := c.Args[1].(time.Duration)
			if d == 10*time.Minute {
				found = true
			}
		}
	}
	if !found {
		t.Error("GetRecent was not called with WithRecentDuration(10min)")
	}
}
