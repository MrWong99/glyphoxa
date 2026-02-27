package entity

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"slices"
	"sync"
)

// Compile-time assertion that MemStore satisfies the Store interface.
var _ Store = (*MemStore)(nil)

// MemStore is a thread-safe, in-memory implementation of [Store].
// It is suitable for single-session use and testing.
// The zero value is ready to use.
type MemStore struct {
	mu       sync.RWMutex
	entities map[string]EntityDefinition
}

// NewMemStore returns an initialised [MemStore].
func NewMemStore() *MemStore {
	return &MemStore{
		entities: make(map[string]EntityDefinition),
	}
}

// Add implements [Store.Add].
func (s *MemStore) Add(ctx context.Context, entity EntityDefinition) (EntityDefinition, error) {
	if entity.ID == "" {
		id, err := generateID()
		if err != nil {
			return EntityDefinition{}, fmt.Errorf("entity: generate id: %w", err)
		}
		entity.ID = id
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.entities == nil {
		s.entities = make(map[string]EntityDefinition)
	}

	if _, exists := s.entities[entity.ID]; exists {
		return EntityDefinition{}, ErrDuplicateID
	}

	s.entities[entity.ID] = entity
	return entity, nil
}

// Get implements [Store.Get].
func (s *MemStore) Get(ctx context.Context, id string) (EntityDefinition, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	e, ok := s.entities[id]
	if !ok {
		return EntityDefinition{}, ErrNotFound
	}
	return e, nil
}

// List implements [Store.List].
func (s *MemStore) List(ctx context.Context, opts ListOptions) ([]EntityDefinition, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]EntityDefinition, 0, len(s.entities))
	for _, e := range s.entities {
		if !matchesOpts(e, opts) {
			continue
		}
		result = append(result, e)
	}
	return result, nil
}

// Update implements [Store.Update].
func (s *MemStore) Update(ctx context.Context, entity EntityDefinition) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.entities[entity.ID]; !ok {
		return ErrNotFound
	}

	s.entities[entity.ID] = entity
	return nil
}

// Remove implements [Store.Remove].
func (s *MemStore) Remove(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.entities[id]; !ok {
		return ErrNotFound
	}

	delete(s.entities, id)
	return nil
}

// BulkImport implements [Store.BulkImport].
// The import is best-effort: entities are added one at a time and the count of
// successfully added entities is returned along with the first error encountered.
func (s *MemStore) BulkImport(ctx context.Context, entities []EntityDefinition) (int, error) {
	count := 0
	for _, e := range entities {
		if _, err := s.Add(ctx, e); err != nil {
			return count, fmt.Errorf("entity: bulk import at index %d (name %q): %w", count, e.Name, err)
		}
		count++
	}
	return count, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

// generateID produces a random 16-byte hex string using crypto/rand.
// The resulting string is 32 hex characters and is statistically unique.
func generateID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// matchesOpts reports whether e satisfies all conditions in opts.
func matchesOpts(e EntityDefinition, opts ListOptions) bool {
	if opts.Type != "" && e.Type != opts.Type {
		return false
	}
	for _, want := range opts.Tags {
		if !containsTag(e.Tags, want) {
			return false
		}
	}
	return true
}

// containsTag reports whether tags contains target (case-sensitive).
func containsTag(tags []string, target string) bool {
	return slices.Contains(tags, target)
}
