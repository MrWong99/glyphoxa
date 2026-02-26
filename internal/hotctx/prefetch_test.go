package hotctx_test

import (
	"context"
	"sync"
	"testing"

	"github.com/MrWong99/glyphoxa/internal/hotctx"
	"github.com/MrWong99/glyphoxa/pkg/memory"
	"github.com/MrWong99/glyphoxa/pkg/memory/mock"
)

// ─────────────────────────────────────────────────────────────────────────────
// helpers
// ─────────────────────────────────────────────────────────────────────────────

func graphWithEntities(entities ...memory.Entity) *mock.KnowledgeGraph {
	kg := &mock.KnowledgeGraph{
		FindEntitiesResult: entities,
	}
	// GetEntity returns the first entity whose ID matches — since the mock
	// only has one GetEntityResult, we set it to the first entity for simple
	// tests and override per-test when needed.
	if len(entities) > 0 {
		kg.GetEntityResult = &entities[0]
	}
	return kg
}

// ─────────────────────────────────────────────────────────────────────────────
// tests
// ─────────────────────────────────────────────────────────────────────────────

// TestPreFetcher_EntityDetection verifies that a partial transcript containing
// a substring of a known entity name triggers a pre-fetch.
func TestPreFetcher_EntityDetection(t *testing.T) {
	blacksmith := memory.Entity{
		ID:   "npc-1",
		Type: "npc",
		Name: "Grimjaw the blacksmith",
		Attributes: map[string]any{"occupation": "blacksmith"},
	}

	kg := graphWithEntities(blacksmith)
	kg.GetEntityResult = &blacksmith

	pf := hotctx.NewPreFetcher(kg)

	if err := pf.RefreshEntityList(context.Background()); err != nil {
		t.Fatalf("RefreshEntityList() error = %v", err)
	}

	// Partial transcript contains "the blacksmith" — a substring of "Grimjaw the blacksmith"
	fetched := pf.ProcessPartial(context.Background(), "I need to visit the blacksmith today")

	if len(fetched) != 1 {
		t.Fatalf("expected 1 fetched entity, got %d", len(fetched))
	}
	if fetched[0].ID != blacksmith.ID {
		t.Errorf("fetched entity ID = %q, want %q", fetched[0].ID, blacksmith.ID)
	}
	if fetched[0].Name != blacksmith.Name {
		t.Errorf("fetched entity Name = %q, want %q", fetched[0].Name, blacksmith.Name)
	}
}

// TestPreFetcher_CacheHit verifies that a second ProcessPartial call for the
// same entity does not re-fetch from the graph.
func TestPreFetcher_CacheHit(t *testing.T) {
	blacksmith := memory.Entity{
		ID:   "npc-1",
		Type: "npc",
		Name: "Grimjaw the blacksmith",
	}
	kg := graphWithEntities(blacksmith)
	kg.GetEntityResult = &blacksmith

	pf := hotctx.NewPreFetcher(kg)
	if err := pf.RefreshEntityList(context.Background()); err != nil {
		t.Fatalf("RefreshEntityList() error = %v", err)
	}

	// First call — fetches from graph.
	first := pf.ProcessPartial(context.Background(), "the blacksmith is here")
	if len(first) != 1 {
		t.Fatalf("first call: expected 1 fetched, got %d", len(first))
	}

	callsBefore := kg.CallCount("GetEntity")

	// Second call — should NOT call GetEntity again.
	second := pf.ProcessPartial(context.Background(), "ask the blacksmith again")
	if len(second) != 0 {
		t.Errorf("second call: expected 0 newly fetched (cache hit), got %d", len(second))
	}

	callsAfter := kg.CallCount("GetEntity")
	if callsAfter != callsBefore {
		t.Errorf("GetEntity was called again on cache hit (%d → %d)", callsBefore, callsAfter)
	}

	// Cached entity is still accessible via GetCachedEntities.
	cached := pf.GetCachedEntities()
	if len(cached) != 1 {
		t.Errorf("GetCachedEntities() returned %d entries, want 1", len(cached))
	}
}

// TestPreFetcher_Reset verifies that Reset clears the cache so subsequent calls
// re-fetch from the graph.
func TestPreFetcher_Reset(t *testing.T) {
	blacksmith := memory.Entity{
		ID:   "npc-1",
		Type: "npc",
		Name: "Grimjaw",
	}
	kg := graphWithEntities(blacksmith)
	kg.GetEntityResult = &blacksmith

	pf := hotctx.NewPreFetcher(kg)
	if err := pf.RefreshEntityList(context.Background()); err != nil {
		t.Fatalf("RefreshEntityList() error = %v", err)
	}

	// Populate the cache.
	pf.ProcessPartial(context.Background(), "I talked to Grimjaw")

	cached := pf.GetCachedEntities()
	if len(cached) != 1 {
		t.Fatalf("expected 1 cached entity before Reset, got %d", len(cached))
	}

	// Reset clears the cache.
	pf.Reset()

	cached = pf.GetCachedEntities()
	if len(cached) != 0 {
		t.Errorf("expected 0 cached entities after Reset, got %d", len(cached))
	}

	callsBefore := kg.CallCount("GetEntity")

	// Next ProcessPartial should re-fetch.
	pf.ProcessPartial(context.Background(), "I talked to Grimjaw again")
	callsAfter := kg.CallCount("GetEntity")
	if callsAfter == callsBefore {
		t.Error("expected GetEntity to be called again after Reset")
	}
}

// TestPreFetcher_RefreshEntityList verifies that RefreshEntityList updates the
// entity name index from the graph.
func TestPreFetcher_RefreshEntityList(t *testing.T) {
	// Start with an empty graph.
	kg := &mock.KnowledgeGraph{}

	pf := hotctx.NewPreFetcher(kg)
	if err := pf.RefreshEntityList(context.Background()); err != nil {
		t.Fatalf("RefreshEntityList() error = %v", err)
	}

	// No entities known → ProcessPartial returns nothing.
	fetched := pf.ProcessPartial(context.Background(), "Grimjaw is here")
	if len(fetched) != 0 {
		t.Errorf("expected 0 fetched before entity list loaded, got %d", len(fetched))
	}

	// Now add Grimjaw to the graph's find result and refresh.
	grimjaw := memory.Entity{ID: "npc-1", Type: "npc", Name: "Grimjaw"}
	kg.FindEntitiesResult = []memory.Entity{grimjaw}
	kg.GetEntityResult = &grimjaw

	if err := pf.RefreshEntityList(context.Background()); err != nil {
		t.Fatalf("second RefreshEntityList() error = %v", err)
	}

	// Now ProcessPartial should detect Grimjaw.
	fetched = pf.ProcessPartial(context.Background(), "Grimjaw is here")
	if len(fetched) != 1 {
		t.Errorf("expected 1 fetched after entity list refresh, got %d", len(fetched))
	}
}

// TestPreFetcher_ConcurrentProcessPartial verifies goroutine safety under
// concurrent ProcessPartial calls.
func TestPreFetcher_ConcurrentProcessPartial(t *testing.T) {
	entities := []memory.Entity{
		{ID: "npc-1", Type: "npc", Name: "Grimjaw"},
		{ID: "npc-2", Type: "npc", Name: "Torvel"},
		{ID: "loc-1", Type: "location", Name: "The Forge"},
	}
	kg := &mock.KnowledgeGraph{
		FindEntitiesResult: entities,
		// GetEntity returns npc-1 always — that's fine for race detection purposes.
		GetEntityResult: &entities[0],
	}

	pf := hotctx.NewPreFetcher(kg)
	if err := pf.RefreshEntityList(context.Background()); err != nil {
		t.Fatalf("RefreshEntityList() error = %v", err)
	}

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			// Alternate between different partial transcripts to exercise both
			// cache-hit and potential new-fetch code paths.
			if i%2 == 0 {
				pf.ProcessPartial(context.Background(), "let's talk to Grimjaw")
			} else {
				pf.ProcessPartial(context.Background(), "heading to Torvel and The Forge now")
			}
		}(i)
	}

	wg.Wait()

	// After all goroutines finish, GetCachedEntities must not panic.
	_ = pf.GetCachedEntities()
}

// TestPreFetcher_CaseInsensitive verifies that entity detection is
// case-insensitive ("GRIMJAW" matches entity named "Grimjaw").
func TestPreFetcher_CaseInsensitive(t *testing.T) {
	grimjaw := memory.Entity{ID: "npc-1", Type: "npc", Name: "Grimjaw"}
	kg := graphWithEntities(grimjaw)
	kg.GetEntityResult = &grimjaw

	pf := hotctx.NewPreFetcher(kg)
	if err := pf.RefreshEntityList(context.Background()); err != nil {
		t.Fatalf("RefreshEntityList() error = %v", err)
	}

	fetched := pf.ProcessPartial(context.Background(), "GRIMJAW said something important")
	if len(fetched) != 1 {
		t.Errorf("expected 1 fetched for uppercase name, got %d", len(fetched))
	}
}

// TestPreFetcher_NoMatchReturnsEmpty verifies that ProcessPartial returns an
// empty (non-nil) slice when no known entities are detected.
func TestPreFetcher_NoMatchReturnsEmpty(t *testing.T) {
	grimjaw := memory.Entity{ID: "npc-1", Type: "npc", Name: "Grimjaw"}
	kg := graphWithEntities(grimjaw)

	pf := hotctx.NewPreFetcher(kg)
	if err := pf.RefreshEntityList(context.Background()); err != nil {
		t.Fatalf("RefreshEntityList() error = %v", err)
	}

	fetched := pf.ProcessPartial(context.Background(), "nothing relevant here at all")
	if fetched == nil {
		t.Error("ProcessPartial must return non-nil slice on no match")
	}
	if len(fetched) != 0 {
		t.Errorf("expected 0 fetched, got %d", len(fetched))
	}
}
