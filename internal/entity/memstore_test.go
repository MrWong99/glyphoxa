package entity_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/MrWong99/glyphoxa/internal/entity"
)

func TestAdd(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("with empty ID generates one", func(t *testing.T) {
		t.Parallel()
		s := entity.NewMemStore()
		e := entity.EntityDefinition{Name: "Gandalf", Type: entity.EntityNPC}
		got, err := s.Add(ctx, e)
		if err != nil {
			t.Fatalf("Add: unexpected error: %v", err)
		}
		if got.ID == "" {
			t.Fatal("Add: expected generated ID, got empty string")
		}
	})

	t.Run("with explicit ID is preserved", func(t *testing.T) {
		t.Parallel()
		s := entity.NewMemStore()
		e := entity.EntityDefinition{ID: "npc-001", Name: "Sauron", Type: entity.EntityNPC}
		got, err := s.Add(ctx, e)
		if err != nil {
			t.Fatalf("Add: unexpected error: %v", err)
		}
		if got.ID != "npc-001" {
			t.Fatalf("Add: expected ID %q, got %q", "npc-001", got.ID)
		}
	})

	t.Run("duplicate ID returns ErrDuplicateID", func(t *testing.T) {
		t.Parallel()
		s := entity.NewMemStore()
		e := entity.EntityDefinition{ID: "dup-01", Name: "First", Type: entity.EntityNPC}
		if _, err := s.Add(ctx, e); err != nil {
			t.Fatalf("Add first: unexpected error: %v", err)
		}
		_, err := s.Add(ctx, e)
		if !errors.Is(err, entity.ErrDuplicateID) {
			t.Fatalf("Add duplicate: expected ErrDuplicateID, got %v", err)
		}
	})
}

func TestGet(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := entity.NewMemStore()
	added, _ := s.Add(ctx, entity.EntityDefinition{Name: "The Shire", Type: entity.EntityLocation})

	t.Run("existing entity", func(t *testing.T) {
		t.Parallel()
		got, err := s.Get(ctx, added.ID)
		if err != nil {
			t.Fatalf("Get: unexpected error: %v", err)
		}
		if got.Name != "The Shire" {
			t.Fatalf("Get: expected name %q, got %q", "The Shire", got.Name)
		}
	})

	t.Run("missing entity returns ErrNotFound", func(t *testing.T) {
		t.Parallel()
		_, err := s.Get(ctx, "does-not-exist")
		if !errors.Is(err, entity.ErrNotFound) {
			t.Fatalf("Get: expected ErrNotFound, got %v", err)
		}
	})
}

func TestList(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := entity.NewMemStore()
	fixtures := []entity.EntityDefinition{
		{Name: "Rivendell", Type: entity.EntityLocation, Tags: []string{"elven", "sanctuary"}},
		{Name: "Mirkwood", Type: entity.EntityLocation, Tags: []string{"elven", "forest"}},
		{Name: "Legolas", Type: entity.EntityNPC, Tags: []string{"elven", "archer"}},
	}
	for _, f := range fixtures {
		if _, err := s.Add(ctx, f); err != nil {
			t.Fatalf("setup Add: %v", err)
		}
	}

	t.Run("no filter returns all", func(t *testing.T) {
		t.Parallel()
		all, err := s.List(ctx, entity.ListOptions{})
		if err != nil {
			t.Fatalf("List: unexpected error: %v", err)
		}
		if len(all) != 3 {
			t.Fatalf("List: expected 3 entities, got %d", len(all))
		}
	})
}

func TestListFilterByType(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := entity.NewMemStore()
	fixtures := []entity.EntityDefinition{
		{Name: "Rivendell", Type: entity.EntityLocation},
		{Name: "Mirkwood", Type: entity.EntityLocation},
		{Name: "Legolas", Type: entity.EntityNPC},
	}
	for _, f := range fixtures {
		if _, err := s.Add(ctx, f); err != nil {
			t.Fatalf("setup Add: %v", err)
		}
	}

	tests := []struct {
		name      string
		filterTyp entity.EntityType
		wantCount int
	}{
		{"location filter", entity.EntityLocation, 2},
		{"npc filter", entity.EntityNPC, 1},
		{"item filter (none)", entity.EntityItem, 0},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := s.List(ctx, entity.ListOptions{Type: tc.filterTyp})
			if err != nil {
				t.Fatalf("List: unexpected error: %v", err)
			}
			if len(got) != tc.wantCount {
				t.Fatalf("List(%s): expected %d, got %d", tc.filterTyp, tc.wantCount, len(got))
			}
		})
	}
}

func TestListFilterByTags(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := entity.NewMemStore()
	fixtures := []entity.EntityDefinition{
		{Name: "Rivendell", Type: entity.EntityLocation, Tags: []string{"elven", "sanctuary"}},
		{Name: "Mirkwood", Type: entity.EntityLocation, Tags: []string{"elven", "forest"}},
		{Name: "Legolas", Type: entity.EntityNPC, Tags: []string{"elven", "archer"}},
		{Name: "Gimli", Type: entity.EntityNPC, Tags: []string{"dwarf", "warrior"}},
	}
	for _, f := range fixtures {
		if _, err := s.Add(ctx, f); err != nil {
			t.Fatalf("setup Add: %v", err)
		}
	}

	tests := []struct {
		name      string
		tags      []string
		wantCount int
	}{
		{"elven tag", []string{"elven"}, 3},
		{"sanctuary tag", []string{"sanctuary"}, 1},
		{"elven+forest", []string{"elven", "forest"}, 1},
		{"dwarf tag", []string{"dwarf"}, 1},
		{"non-existent tag", []string{"hobbit"}, 0},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := s.List(ctx, entity.ListOptions{Tags: tc.tags})
			if err != nil {
				t.Fatalf("List: unexpected error: %v", err)
			}
			if len(got) != tc.wantCount {
				t.Fatalf("List(tags=%v): expected %d, got %d", tc.tags, tc.wantCount, len(got))
			}
		})
	}
}

func TestUpdate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("updates existing entity", func(t *testing.T) {
		t.Parallel()
		s := entity.NewMemStore()
		added, _ := s.Add(ctx, entity.EntityDefinition{Name: "Old Name", Type: entity.EntityNPC})
		added.Name = "New Name"
		if err := s.Update(ctx, added); err != nil {
			t.Fatalf("Update: unexpected error: %v", err)
		}
		got, _ := s.Get(ctx, added.ID)
		if got.Name != "New Name" {
			t.Fatalf("Update: expected name %q, got %q", "New Name", got.Name)
		}
	})

	t.Run("missing entity returns ErrNotFound", func(t *testing.T) {
		t.Parallel()
		s := entity.NewMemStore()
		err := s.Update(ctx, entity.EntityDefinition{ID: "ghost", Name: "Ghost", Type: entity.EntityNPC})
		if !errors.Is(err, entity.ErrNotFound) {
			t.Fatalf("Update: expected ErrNotFound, got %v", err)
		}
	})
}

func TestRemove(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("removes existing entity", func(t *testing.T) {
		t.Parallel()
		s := entity.NewMemStore()
		added, _ := s.Add(ctx, entity.EntityDefinition{Name: "Temporary", Type: entity.EntityItem})
		if err := s.Remove(ctx, added.ID); err != nil {
			t.Fatalf("Remove: unexpected error: %v", err)
		}
		if _, err := s.Get(ctx, added.ID); !errors.Is(err, entity.ErrNotFound) {
			t.Fatalf("Get after Remove: expected ErrNotFound, got %v", err)
		}
	})

	t.Run("missing entity returns ErrNotFound", func(t *testing.T) {
		t.Parallel()
		s := entity.NewMemStore()
		err := s.Remove(ctx, "missing-id")
		if !errors.Is(err, entity.ErrNotFound) {
			t.Fatalf("Remove: expected ErrNotFound, got %v", err)
		}
	})
}

func TestBulkImport(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := entity.NewMemStore()

	batch := []entity.EntityDefinition{
		{Name: "Alpha", Type: entity.EntityNPC},
		{Name: "Beta", Type: entity.EntityLocation},
		{Name: "Gamma", Type: entity.EntityItem},
	}

	n, err := s.BulkImport(ctx, batch)
	if err != nil {
		t.Fatalf("BulkImport: unexpected error: %v", err)
	}
	if n != 3 {
		t.Fatalf("BulkImport: expected 3, got %d", n)
	}

	all, _ := s.List(ctx, entity.ListOptions{})
	if len(all) != 3 {
		t.Fatalf("BulkImport: expected 3 entities in store, got %d", len(all))
	}
}

func TestConcurrentAccess(t *testing.T) {
	t.Parallel()

	const goroutines = 50
	ctx := context.Background()
	s := entity.NewMemStore()

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			e, err := s.Add(ctx, entity.EntityDefinition{
				Name: "Concurrent NPC",
				Type: entity.EntityNPC,
			})
			if err != nil {
				return // unlikely in this test; just skip
			}
			_, _ = s.Get(ctx, e.ID)
			_, _ = s.List(ctx, entity.ListOptions{})
			_ = s.Update(ctx, entity.EntityDefinition{ID: e.ID, Name: "Updated", Type: entity.EntityNPC})
			_ = s.Remove(ctx, e.ID)
		}()
	}

	wg.Wait()
}
