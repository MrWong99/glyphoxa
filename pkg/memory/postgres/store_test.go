package postgres_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pgxvec "github.com/pgvector/pgvector-go/pgx"

	"github.com/MrWong99/glyphoxa/pkg/memory"
	"github.com/MrWong99/glyphoxa/pkg/memory/postgres"
	"github.com/MrWong99/glyphoxa/pkg/types"
)

const testEmbeddingDim = 4

// testDSN returns the test database DSN from the environment, or skips the
// test if GLYPHOXA_TEST_POSTGRES_DSN is not set.
func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("GLYPHOXA_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("GLYPHOXA_TEST_POSTGRES_DSN not set — skipping PostgreSQL integration tests")
	}
	return dsn
}

// newTestStore creates a fresh [postgres.Store] with a clean schema.
// It calls t.Cleanup to close the store when the test finishes.
func newTestStore(t *testing.T) *postgres.Store {
	t.Helper()
	dsn := testDSN(t)
	ctx := context.Background()

	// Use a bare pool to drop and recreate the schema.
	cleanPool := mustPool(t, ctx, dsn)
	t.Cleanup(cleanPool.Close)
	dropSchema(t, ctx, cleanPool)

	store, err := postgres.NewStore(ctx, dsn, testEmbeddingDim)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(store.Close)
	return store
}

// mustPool opens a pgxpool with pgvector types registered (needed for HNSW
// index to not refuse our connection during dropSchema).
func mustPool(t *testing.T, ctx context.Context, dsn string) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		// best-effort: pgvector may not be installed yet on a fresh DB
		_ = pgxvec.RegisterTypes(ctx, conn)
		return nil
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	return pool
}

// dropSchema removes all tables created by Migrate in reverse dependency order.
func dropSchema(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	for _, stmt := range []string{
		"DROP TABLE IF EXISTS relationships CASCADE",
		"DROP TABLE IF EXISTS entities CASCADE",
		"DROP TABLE IF EXISTS chunks CASCADE",
		"DROP TABLE IF EXISTS session_entries CASCADE",
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("dropSchema %q: %v", stmt, err)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// L1 — SessionStore
// ─────────────────────────────────────────────────────────────────────────────

func TestL1_WriteAndGetRecent(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	l1 := store.L1()

	sessionID := "session-1"
	now := time.Now()
	entries := []types.TranscriptEntry{
		{
			SpeakerID:   "player-1",
			SpeakerName: "Alice",
			Text:        "I approach the blacksmith cautiously.",
			RawText:     "I approach the blacksmith cautiously.",
			IsNPC:       false,
			Timestamp:   now.Add(-10 * time.Minute),
			Duration:    2 * time.Second,
		},
		{
			SpeakerID:   "npc-grimjaw",
			SpeakerName: "Grimjaw",
			Text:        "What do ye want? I am busy forging.",
			IsNPC:       true,
			NPCID:       "npc-grimjaw",
			Timestamp:   now.Add(-9 * time.Minute),
			Duration:    3 * time.Second,
		},
		{
			SpeakerID:   "player-1",
			SpeakerName: "Alice",
			Text:        "We need weapons for the upcoming battle.",
			Timestamp:   now.Add(-1 * time.Minute),
			Duration:    2500 * time.Millisecond,
		},
	}

	for _, e := range entries {
		if err := l1.WriteEntry(ctx, sessionID, e); err != nil {
			t.Fatalf("WriteEntry: %v", err)
		}
	}

	// GetRecent with a wide window should return all 3.
	recent, err := l1.GetRecent(ctx, sessionID, 30*time.Minute)
	if err != nil {
		t.Fatalf("GetRecent(30m): %v", err)
	}
	if len(recent) != 3 {
		t.Errorf("GetRecent(30m): want 3, got %d", len(recent))
	}

	// GetRecent with a narrow window should return only the last entry.
	narrow, err := l1.GetRecent(ctx, sessionID, 5*time.Minute)
	if err != nil {
		t.Fatalf("GetRecent(5m): %v", err)
	}
	if len(narrow) != 1 {
		t.Errorf("GetRecent(5m): want 1, got %d", len(narrow))
	}
	if len(narrow) > 0 && narrow[0].Text != entries[2].Text {
		t.Errorf("GetRecent(5m): want %q, got %q", entries[2].Text, narrow[0].Text)
	}

	// GetRecent for a different session returns nothing.
	other, err := l1.GetRecent(ctx, "other-session", 30*time.Minute)
	if err != nil {
		t.Fatalf("GetRecent other: %v", err)
	}
	if len(other) != 0 {
		t.Errorf("GetRecent other: want 0, got %d", len(other))
	}

	// Duration is round-tripped correctly.
	if len(recent) > 0 && recent[0].Duration != entries[0].Duration {
		t.Errorf("Duration: want %v, got %v", entries[0].Duration, recent[0].Duration)
	}
}

func TestL1_Search(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	l1 := store.L1()

	sessionID := "search-session"
	writeL1Entries(t, ctx, l1, sessionID, []types.TranscriptEntry{
		{SpeakerID: "p1", Text: "The dragon hoards treasure in the mountain.", Timestamp: time.Now().Add(-5 * time.Minute)},
		{SpeakerID: "p2", Text: "We should negotiate with the goblin tribe.", Timestamp: time.Now().Add(-4 * time.Minute)},
		{SpeakerID: "npc1", IsNPC: true, NPCID: "npc1", Text: "The prophecy speaks of a chosen hero.", Timestamp: time.Now().Add(-3 * time.Minute)},
	})

	tests := []struct {
		name      string
		query     string
		opts      memory.SearchOpts
		wantCount int
		wantText  string
	}{
		{
			name:      "dragon treasure",
			query:     "dragon treasure",
			opts:      memory.SearchOpts{SessionID: sessionID},
			wantCount: 1,
			wantText:  "dragon",
		},
		{
			name:      "goblin",
			query:     "goblin",
			opts:      memory.SearchOpts{SessionID: sessionID},
			wantCount: 1,
			wantText:  "goblin",
		},
		{
			name:      "npc speaker filter",
			query:     "prophecy",
			opts:      memory.SearchOpts{SessionID: sessionID, SpeakerID: "npc1"},
			wantCount: 1,
		},
		{
			name:      "no match",
			query:     "wizard tower",
			opts:      memory.SearchOpts{SessionID: sessionID},
			wantCount: 0,
		},
		{
			name:      "limit",
			query:     "the",
			opts:      memory.SearchOpts{SessionID: sessionID, Limit: 1},
			wantCount: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			results, err := l1.Search(ctx, tc.query, tc.opts)
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			if len(results) != tc.wantCount {
				t.Errorf("want %d results, got %d", tc.wantCount, len(results))
			}
			if tc.wantText != "" && len(results) > 0 {
				if !strings.Contains(strings.ToLower(results[0].Text), strings.ToLower(tc.wantText)) {
					t.Errorf("want %q in first result text, got %q", tc.wantText, results[0].Text)
				}
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// L2 — SemanticIndex
// ─────────────────────────────────────────────────────────────────────────────

func TestL2_IndexAndSearch(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	l2 := store.L2()

	chunks := []memory.Chunk{
		{
			ID:        "chunk-1",
			SessionID: "s1",
			Content:   "The blacksmith talks about the missing shipment.",
			Embedding: []float32{1, 0, 0, 0},
			SpeakerID: "npc-grimjaw",
			NPCID:     "entity-grimjaw",
			Topic:     "trade",
			Timestamp: time.Now(),
		},
		{
			ID:        "chunk-2",
			SessionID: "s1",
			Content:   "The dragon guards treasure in the northern caves.",
			Embedding: []float32{0, 1, 0, 0},
			SpeakerID: "player-1",
			NPCID:     "",
			Topic:     "exploration",
			Timestamp: time.Now(),
		},
		{
			ID:        "chunk-3",
			SessionID: "s2",
			Content:   "The guild master reveals plans for an uprising.",
			Embedding: []float32{0, 0, 1, 0},
			SpeakerID: "npc-master",
			NPCID:     "entity-master",
			Topic:     "politics",
			Timestamp: time.Now(),
		},
	}

	for _, c := range chunks {
		if err := l2.IndexChunk(ctx, c); err != nil {
			t.Fatalf("IndexChunk %s: %v", c.ID, err)
		}
	}

	// Query closest to chunk-1 (embedding [1,0,0,0]).
	results, err := l2.Search(ctx, []float32{1, 0, 0, 0}, 3, memory.ChunkFilter{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("Search topK=3: want 3 results, got %d", len(results))
	}
	if len(results) > 0 && results[0].Chunk.ID != "chunk-1" {
		t.Errorf("closest chunk: want chunk-1, got %s (distance %.4f)", results[0].Chunk.ID, results[0].Distance)
	}

	// Scope to session s2.
	scoped, err := l2.Search(ctx, []float32{0, 0, 1, 0}, 10, memory.ChunkFilter{SessionID: "s2"})
	if err != nil {
		t.Fatalf("Search scoped: %v", err)
	}
	if len(scoped) != 1 || scoped[0].Chunk.ID != "chunk-3" {
		t.Errorf("session scope: want [chunk-3], got %v", chunkIDs(scoped))
	}

	// Filter by NPCID.
	npcFiltered, err := l2.Search(ctx, []float32{1, 0, 0, 0}, 10, memory.ChunkFilter{NPCID: "entity-grimjaw"})
	if err != nil {
		t.Fatalf("Search npc filter: %v", err)
	}
	if len(npcFiltered) != 1 {
		t.Errorf("npc filter: want 1, got %d", len(npcFiltered))
	}

	// Filter by SpeakerID.
	speakerFiltered, err := l2.Search(ctx, []float32{0, 1, 0, 0}, 10, memory.ChunkFilter{SpeakerID: "player-1"})
	if err != nil {
		t.Fatalf("Search speaker filter: %v", err)
	}
	if len(speakerFiltered) != 1 || speakerFiltered[0].Chunk.ID != "chunk-2" {
		t.Errorf("speaker filter: want [chunk-2], got %v", chunkIDs(speakerFiltered))
	}

	// Upsert: re-indexing chunk-1 with new data should replace it.
	updated := chunks[0]
	updated.Content = "Updated content after upsert."
	updated.Embedding = []float32{0, 0, 0, 1}
	if err := l2.IndexChunk(ctx, updated); err != nil {
		t.Fatalf("IndexChunk upsert: %v", err)
	}
	upserted, err := l2.Search(ctx, []float32{0, 0, 0, 1}, 1, memory.ChunkFilter{SessionID: "s1"})
	if err != nil {
		t.Fatalf("Search after upsert: %v", err)
	}
	if len(upserted) < 1 {
		t.Fatal("upsert: no results returned")
	}
	if upserted[0].Chunk.Content != updated.Content {
		t.Errorf("upsert: want content %q, got %q", updated.Content, upserted[0].Chunk.Content)
	}

	// Time filters.
	past := time.Now().Add(-1 * time.Hour)
	future := time.Now().Add(1 * time.Hour)
	afterFiltered, err := l2.Search(ctx, []float32{0, 1, 0, 0}, 10, memory.ChunkFilter{After: past})
	if err != nil {
		t.Fatalf("Search after filter: %v", err)
	}
	if len(afterFiltered) == 0 {
		t.Error("after filter: expected results, got none")
	}
	beforeFiltered, err := l2.Search(ctx, []float32{0, 1, 0, 0}, 10, memory.ChunkFilter{Before: future})
	if err != nil {
		t.Fatalf("Search before filter: %v", err)
	}
	if len(beforeFiltered) == 0 {
		t.Error("before filter: expected results, got none")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// L3 — Entity CRUD
// ─────────────────────────────────────────────────────────────────────────────

func TestL3_EntityCRUD(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	entity := memory.Entity{
		ID:   "ent-grimjaw",
		Type: "npc",
		Name: "Grimjaw",
		Attributes: map[string]any{
			"occupation": "blacksmith",
			"alignment":  "neutral",
		},
	}

	// Add.
	if err := store.AddEntity(ctx, entity); err != nil {
		t.Fatalf("AddEntity: %v", err)
	}

	// Get.
	got, err := store.GetEntity(ctx, entity.ID)
	if err != nil {
		t.Fatalf("GetEntity: %v", err)
	}
	if got == nil {
		t.Fatal("GetEntity: expected entity, got nil")
	}
	if got.Name != entity.Name {
		t.Errorf("Name: want %q, got %q", entity.Name, got.Name)
	}
	if got.Attributes["occupation"] != "blacksmith" {
		t.Errorf("Attributes: expected occupation=blacksmith, got %v", got.Attributes)
	}

	// Update merges new key while preserving existing.
	if err := store.UpdateEntity(ctx, entity.ID, map[string]any{"mood": "grumpy"}); err != nil {
		t.Fatalf("UpdateEntity: %v", err)
	}
	updated, _ := store.GetEntity(ctx, entity.ID)
	if updated.Attributes["mood"] != "grumpy" {
		t.Errorf("UpdateEntity: want mood=grumpy, got %v", updated.Attributes)
	}
	if updated.Attributes["occupation"] != "blacksmith" {
		t.Errorf("UpdateEntity: occupation should not be removed, got %v", updated.Attributes)
	}

	// UpdateEntity on missing ID returns error.
	if err := store.UpdateEntity(ctx, "does-not-exist", map[string]any{}); err == nil {
		t.Error("UpdateEntity missing: expected error, got nil")
	}

	// GetEntity for missing ID returns (nil, nil).
	missing, err := store.GetEntity(ctx, "does-not-exist")
	if err != nil {
		t.Fatalf("GetEntity missing: unexpected error: %v", err)
	}
	if missing != nil {
		t.Errorf("GetEntity missing: want nil, got %+v", missing)
	}

	// Delete.
	if err := store.DeleteEntity(ctx, entity.ID); err != nil {
		t.Fatalf("DeleteEntity: %v", err)
	}
	afterDelete, _ := store.GetEntity(ctx, entity.ID)
	if afterDelete != nil {
		t.Error("DeleteEntity: entity still present after delete")
	}

	// Delete non-existent is not an error.
	if err := store.DeleteEntity(ctx, "never-existed"); err != nil {
		t.Errorf("DeleteEntity non-existent: unexpected error: %v", err)
	}
}

func TestL3_FindEntities(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	for _, e := range []memory.Entity{
		{ID: "loc-tavern", Type: "location", Name: "The Rusty Tankard", Attributes: map[string]any{"atmosphere": "lively"}},
		{ID: "npc-elara", Type: "npc", Name: "Elara the Mage", Attributes: map[string]any{"class": "wizard"}},
		{ID: "npc-thorin", Type: "npc", Name: "Thorin", Attributes: map[string]any{"class": "fighter"}},
		{ID: "item-sword", Type: "item", Name: "Sword of Dawn", Attributes: map[string]any{"magical": true}},
	} {
		mustAddEntity(t, ctx, store, e)
	}

	tests := []struct {
		name      string
		filter    memory.EntityFilter
		wantIDs   []string
		wantCount int
	}{
		{"by type npc", memory.EntityFilter{Type: "npc"}, nil, 2},
		{"by name substring", memory.EntityFilter{Name: "Elara"}, []string{"npc-elara"}, 1},
		{"by attribute", memory.EntityFilter{AttributeQuery: map[string]any{"magical": true}}, []string{"item-sword"}, 1},
		{"no match", memory.EntityFilter{Type: "faction"}, nil, 0},
		{"empty filter", memory.EntityFilter{}, nil, 4},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			results, err := store.FindEntities(ctx, tc.filter)
			if err != nil {
				t.Fatalf("FindEntities: %v", err)
			}
			if tc.wantCount > 0 && len(results) != tc.wantCount {
				t.Errorf("want %d, got %d", tc.wantCount, len(results))
			}
			for _, wid := range tc.wantIDs {
				if !containsEntity(results, wid) {
					t.Errorf("expected entity %q not found in results %v", wid, entityIDs(results))
				}
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// L3 — Relationship CRUD
// ─────────────────────────────────────────────────────────────────────────────

func TestL3_RelationshipCRUD(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	grimjaw := memory.Entity{ID: "rel-grimjaw", Type: "npc", Name: "Grimjaw"}
	tavern := memory.Entity{ID: "rel-tavern", Type: "location", Name: "The Rusty Tankard"}
	guild := memory.Entity{ID: "rel-guild", Type: "faction", Name: "Blacksmiths Guild"}
	for _, e := range []memory.Entity{grimjaw, tavern, guild} {
		mustAddEntity(t, ctx, store, e)
	}

	rels := []memory.Relationship{
		{
			SourceID: grimjaw.ID, TargetID: tavern.ID, RelType: "LOCATED_AT",
			Attributes: map[string]any{"since": "year 1200"},
			Provenance: memory.Provenance{SessionID: "s1", Confidence: 0.9, Source: "stated"},
		},
		{
			SourceID: grimjaw.ID, TargetID: guild.ID, RelType: "MEMBER_OF",
			Attributes: map[string]any{},
			Provenance: memory.Provenance{Confidence: 0.8, Source: "inferred"},
		},
	}
	for _, r := range rels {
		if err := store.AddRelationship(ctx, r); err != nil {
			t.Fatalf("AddRelationship: %v", err)
		}
	}

	// GetRelationships: outgoing from grimjaw (default).
	out, err := store.GetRelationships(ctx, grimjaw.ID)
	if err != nil {
		t.Fatalf("GetRelationships: %v", err)
	}
	if len(out) != 2 {
		t.Errorf("outgoing: want 2, got %d", len(out))
	}

	// Filter by rel type.
	locRels, err := store.GetRelationships(ctx, grimjaw.ID, memory.WithRelTypes("LOCATED_AT"))
	if err != nil {
		t.Fatalf("WithRelTypes: %v", err)
	}
	if len(locRels) != 1 {
		t.Errorf("WithRelTypes: want 1, got %d", len(locRels))
	}

	// Incoming: tavern should see the edge from grimjaw.
	inc, err := store.GetRelationships(ctx, tavern.ID, memory.WithIncoming())
	if err != nil {
		t.Fatalf("incoming: %v", err)
	}
	if len(inc) != 1 {
		t.Errorf("incoming: want 1, got %d", len(inc))
	}

	// Provenance round-trip.
	if len(locRels) > 0 && locRels[0].Provenance.Confidence != 0.9 {
		t.Errorf("Provenance.Confidence: want 0.9, got %v", locRels[0].Provenance.Confidence)
	}
	if len(locRels) > 0 && locRels[0].Attributes["since"] != "year 1200" {
		t.Errorf("Attributes[since]: want year 1200, got %v", locRels[0].Attributes)
	}

	// Upsert: replace with new attribute value.
	updated := rels[0]
	updated.Attributes = map[string]any{"since": "year 1205"}
	if err := store.AddRelationship(ctx, updated); err != nil {
		t.Fatalf("AddRelationship upsert: %v", err)
	}
	got, _ := store.GetRelationships(ctx, grimjaw.ID, memory.WithRelTypes("LOCATED_AT"))
	if len(got) > 0 && got[0].Attributes["since"] != "year 1205" {
		t.Errorf("upsert: want year 1205, got %v", got[0].Attributes)
	}

	// Delete.
	if err := store.DeleteRelationship(ctx, grimjaw.ID, guild.ID, "MEMBER_OF"); err != nil {
		t.Fatalf("DeleteRelationship: %v", err)
	}
	after, _ := store.GetRelationships(ctx, grimjaw.ID)
	if len(after) != 1 {
		t.Errorf("after delete: want 1, got %d", len(after))
	}

	// Delete non-existent is not an error.
	if err := store.DeleteRelationship(ctx, "x", "y", "KNOWS"); err != nil {
		t.Errorf("DeleteRelationship non-existent: unexpected error: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// L3 — Graph traversal
// ─────────────────────────────────────────────────────────────────────────────

// buildTestGraph creates a 5-node directed graph:
//
//	grimjaw → (KNOWS)      → elara
//	grimjaw → (MEMBER_OF)  → guild
//	elara   → (LOCATED_AT) → tower
//	guild   → (ALLIED_WITH)→ mages
func buildTestGraph(t *testing.T, ctx context.Context, store *postgres.Store) (grimjaw, elara, guild, tower, mages memory.Entity) {
	t.Helper()
	grimjaw = memory.Entity{ID: "g-grimjaw", Type: "npc", Name: "Grimjaw"}
	elara = memory.Entity{ID: "g-elara", Type: "npc", Name: "Elara"}
	guild = memory.Entity{ID: "g-guild", Type: "faction", Name: "Blacksmiths Guild"}
	tower = memory.Entity{ID: "g-tower", Type: "location", Name: "Elara's Tower"}
	mages = memory.Entity{ID: "g-mages", Type: "faction", Name: "Mages Council"}
	for _, e := range []memory.Entity{grimjaw, elara, guild, tower, mages} {
		mustAddEntity(t, ctx, store, e)
	}
	for _, r := range []memory.Relationship{
		{SourceID: grimjaw.ID, TargetID: elara.ID, RelType: "KNOWS", Attributes: map[string]any{}},
		{SourceID: grimjaw.ID, TargetID: guild.ID, RelType: "MEMBER_OF", Attributes: map[string]any{}},
		{SourceID: elara.ID, TargetID: tower.ID, RelType: "LOCATED_AT", Attributes: map[string]any{}},
		{SourceID: guild.ID, TargetID: mages.ID, RelType: "ALLIED_WITH", Attributes: map[string]any{}},
	} {
		if err := store.AddRelationship(ctx, r); err != nil {
			t.Fatalf("AddRelationship: %v", err)
		}
	}
	return
}

func TestL3_Neighbors(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	grimjaw, _, _, _, _ := buildTestGraph(t, ctx, store)

	// Depth 1: directly connected elara + guild.
	n1, err := store.Neighbors(ctx, grimjaw.ID, 1)
	if err != nil {
		t.Fatalf("Neighbors(1): %v", err)
	}
	if len(n1) != 2 {
		t.Errorf("Neighbors(1): want 2, got %d %v", len(n1), entityIDs(n1))
	}

	// Depth 2: adds tower + mages.
	n2, err := store.Neighbors(ctx, grimjaw.ID, 2)
	if err != nil {
		t.Fatalf("Neighbors(2): %v", err)
	}
	if len(n2) != 4 {
		t.Errorf("Neighbors(2): want 4, got %d %v", len(n2), entityIDs(n2))
	}

	// Depth 3: same as depth 2 (no additional reachable nodes).
	n3, err := store.Neighbors(ctx, grimjaw.ID, 3)
	if err != nil {
		t.Fatalf("Neighbors(3): %v", err)
	}
	if len(n3) != 4 {
		t.Errorf("Neighbors(3): want 4, got %d %v", len(n3), entityIDs(n3))
	}

	// RelType filter: only KNOWS → should find elara (and at depth 2: tower).
	nKnows, err := store.Neighbors(ctx, grimjaw.ID, 2, memory.TraverseRelTypes("KNOWS", "LOCATED_AT"))
	if err != nil {
		t.Fatalf("Neighbors KNOWS: %v", err)
	}
	ids := entityIDs(nKnows)
	if !containsStr(ids, "g-elara") {
		t.Errorf("KNOWS filter: expected g-elara in %v", ids)
	}
	if containsStr(ids, "g-guild") {
		t.Errorf("KNOWS filter: g-guild should not be in %v", ids)
	}

	// NodeType filter: only faction nodes.
	nFaction, err := store.Neighbors(ctx, grimjaw.ID, 3, memory.TraverseNodeTypes("faction"))
	if err != nil {
		t.Fatalf("Neighbors faction: %v", err)
	}
	if len(nFaction) == 0 {
		t.Error("faction node filter: expected at least 1 result")
	}
	for _, e := range nFaction {
		if e.Type != "faction" {
			t.Errorf("faction filter: got entity with type %q", e.Type)
		}
	}

	// MaxNodes cap.
	nCapped, err := store.Neighbors(ctx, grimjaw.ID, 3, memory.TraverseMaxNodes(2))
	if err != nil {
		t.Fatalf("Neighbors max nodes: %v", err)
	}
	if len(nCapped) > 2 {
		t.Errorf("MaxNodes(2): want ≤2, got %d", len(nCapped))
	}
}

func TestL3_FindPath(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	grimjaw, _, _, tower, _ := buildTestGraph(t, ctx, store)

	// grimjaw → elara → tower requires 2 hops.
	path, err := store.FindPath(ctx, grimjaw.ID, tower.ID, 5)
	if err != nil {
		t.Fatalf("FindPath: %v", err)
	}
	if len(path) != 3 {
		t.Errorf("FindPath: want length 3, got %d %v", len(path), entityIDs(path))
	}
	if len(path) > 0 && path[0].ID != grimjaw.ID {
		t.Errorf("FindPath: want start %s, got %s", grimjaw.ID, path[0].ID)
	}
	if len(path) > 0 && path[len(path)-1].ID != tower.ID {
		t.Errorf("FindPath: want end %s, got %s", tower.ID, path[len(path)-1].ID)
	}

	// maxDepth=1 is not enough to reach tower — expect empty.
	short, err := store.FindPath(ctx, grimjaw.ID, tower.ID, 1)
	if err != nil {
		t.Fatalf("FindPath short: %v", err)
	}
	if len(short) != 0 {
		t.Errorf("FindPath short: want empty, got %v", entityIDs(short))
	}

	// Disconnected node — expect empty.
	isolated := memory.Entity{ID: "g-isolated", Type: "npc", Name: "Nobody"}
	mustAddEntity(t, ctx, store, isolated)
	none, err := store.FindPath(ctx, grimjaw.ID, isolated.ID, 5)
	if err != nil {
		t.Fatalf("FindPath none: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("FindPath none: want empty, got %v", entityIDs(none))
	}
}

func TestL3_VisibleSubgraph(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	grimjaw, elara, guild, _, _ := buildTestGraph(t, ctx, store)

	entities, rels, err := store.VisibleSubgraph(ctx, grimjaw.ID)
	if err != nil {
		t.Fatalf("VisibleSubgraph: %v", err)
	}

	ids := entityIDs(entities)
	for _, want := range []string{grimjaw.ID, elara.ID, guild.ID} {
		if !containsStr(ids, want) {
			t.Errorf("VisibleSubgraph: missing %s in %v", want, ids)
		}
	}
	if len(rels) != 2 {
		t.Errorf("VisibleSubgraph rels: want 2, got %d", len(rels))
	}
}

func TestL3_IdentitySnapshot(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	grimjaw, elara, guild, _, _ := buildTestGraph(t, ctx, store)

	snap, err := store.IdentitySnapshot(ctx, grimjaw.ID)
	if err != nil {
		t.Fatalf("IdentitySnapshot: %v", err)
	}
	if snap == nil {
		t.Fatal("IdentitySnapshot: expected non-nil")
	}
	if snap.Entity.ID != grimjaw.ID {
		t.Errorf("Entity.ID: want %s, got %s", grimjaw.ID, snap.Entity.ID)
	}
	if len(snap.Relationships) != 2 {
		t.Errorf("Relationships: want 2, got %d", len(snap.Relationships))
	}
	relatedIDs := entityIDs(snap.RelatedEntities)
	for _, want := range []string{elara.ID, guild.ID} {
		if !containsStr(relatedIDs, want) {
			t.Errorf("RelatedEntities: missing %s in %v", want, relatedIDs)
		}
	}

	// IdentitySnapshot for missing entity returns error.
	_, err = store.IdentitySnapshot(ctx, "does-not-exist")
	if err == nil {
		t.Error("IdentitySnapshot missing: expected error, got nil")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// GraphRAG — QueryWithContext
// ─────────────────────────────────────────────────────────────────────────────

func TestGraphRAG_QueryWithContext(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	l2 := store.L2()

	// Create an NPC entity so chunks can join via npc_id.
	npc := memory.Entity{ID: "rag-npc-1", Type: "npc", Name: "Grimjaw", Attributes: map[string]any{}}
	mustAddEntity(t, ctx, store, npc)

	// Index chunks associated with the npc entity's ID.
	for _, c := range []memory.Chunk{
		{
			ID: "rag-chunk-1", SessionID: "rag-s1", NPCID: npc.ID,
			Content:   "The blacksmith has a secret shipment of weapons hidden in the cellar.",
			Embedding: []float32{1, 0, 0, 0}, Timestamp: time.Now(),
		},
		{
			ID: "rag-chunk-2", SessionID: "rag-s1", NPCID: npc.ID,
			Content:   "Grimjaw owes money to the thieves guild and fears reprisal.",
			Embedding: []float32{0, 1, 0, 0}, Timestamp: time.Now(),
		},
	} {
		if err := l2.IndexChunk(ctx, c); err != nil {
			t.Fatalf("IndexChunk: %v", err)
		}
	}

	// Query matching "shipment weapons" — no scope restriction.
	results, err := store.QueryWithContext(ctx, "shipment weapons", nil)
	if err != nil {
		t.Fatalf("QueryWithContext: %v", err)
	}
	if len(results) == 0 {
		t.Error("QueryWithContext: expected results, got none")
	}
	if len(results) > 0 && results[0].Score == 0 {
		t.Error("QueryWithContext: expected non-zero score")
	}

	// Query with a graphScope that includes the npc entity.
	scoped, err := store.QueryWithContext(ctx, "thieves guild", []string{npc.ID})
	if err != nil {
		t.Fatalf("QueryWithContext scoped: %v", err)
	}
	if len(scoped) == 0 {
		t.Error("QueryWithContext scoped: expected results, got none")
	}
	if len(scoped) > 0 && scoped[0].Entity.ID != npc.ID {
		t.Errorf("QueryWithContext scoped: expected entity %s, got %s", npc.ID, scoped[0].Entity.ID)
	}

	// Query with scope that excludes the npc entity — expect no results.
	excluded, err := store.QueryWithContext(ctx, "blacksmith shipment", []string{"other-entity-id"})
	if err != nil {
		t.Fatalf("QueryWithContext excluded: %v", err)
	}
	if len(excluded) != 0 {
		t.Errorf("QueryWithContext excluded: expected 0, got %d", len(excluded))
	}

	// Query with no FTS match — expect no results.
	empty, err := store.QueryWithContext(ctx, "zzz-no-match-xyz-abc", nil)
	if err != nil {
		t.Fatalf("QueryWithContext empty: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("QueryWithContext no-match: expected 0, got %d", len(empty))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

func writeL1Entries(t *testing.T, ctx context.Context, l1 *postgres.SessionStoreImpl, sessionID string, entries []types.TranscriptEntry) {
	t.Helper()
	for i := range entries {
		if entries[i].Timestamp.IsZero() {
			entries[i].Timestamp = time.Now()
		}
		if err := l1.WriteEntry(ctx, sessionID, entries[i]); err != nil {
			t.Fatalf("WriteEntry[%d]: %v", i, err)
		}
	}
}

func mustAddEntity(t *testing.T, ctx context.Context, store *postgres.Store, e memory.Entity) {
	t.Helper()
	if e.Attributes == nil {
		e.Attributes = map[string]any{}
	}
	if err := store.AddEntity(ctx, e); err != nil {
		t.Fatalf("mustAddEntity %s: %v", e.ID, err)
	}
}

func entityIDs(entities []memory.Entity) []string {
	ids := make([]string, len(entities))
	for i, e := range entities {
		ids[i] = e.ID
	}
	return ids
}

func chunkIDs(results []memory.ChunkResult) []string {
	ids := make([]string, len(results))
	for i, r := range results {
		ids[i] = r.Chunk.ID
	}
	return ids
}

func containsEntity(entities []memory.Entity, id string) bool {
	for _, e := range entities {
		if e.ID == id {
			return true
		}
	}
	return false
}

func containsStr(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
