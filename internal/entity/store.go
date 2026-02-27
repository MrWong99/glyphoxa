package entity

import (
	"context"
	"errors"
)

// ErrNotFound is returned by Get and Update when the requested entity does not exist.
var ErrNotFound = errors.New("entity not found")

// ErrDuplicateID is returned by Add when an entity with the same ID already exists.
var ErrDuplicateID = errors.New("entity with that ID already exists")

// Store manages entity definitions for pre-session setup.
//
// It is NOT the same as pkg/memory.KnowledgeGraph — it is a simpler CRUD
// layer for managing entity definitions before they are loaded into the
// knowledge graph at session start.
//
// All implementations must be safe for concurrent use.
type Store interface {
	// Add creates a new entity. Returns the entity with a generated ID if
	// the provided entity's ID is empty.
	// Returns [ErrDuplicateID] if an entity with the same non-empty ID exists.
	Add(ctx context.Context, entity EntityDefinition) (EntityDefinition, error)

	// Get retrieves an entity by ID.
	// Returns [ErrNotFound] when no entity with that ID exists.
	Get(ctx context.Context, id string) (EntityDefinition, error)

	// List returns all entities, optionally filtered by type and/or tags.
	// An empty [ListOptions] returns all entities.
	// Results order is not guaranteed.
	List(ctx context.Context, opts ListOptions) ([]EntityDefinition, error)

	// Update replaces an existing entity definition.
	// The entity's ID must be non-empty.
	// Returns [ErrNotFound] when no entity with that ID exists.
	Update(ctx context.Context, entity EntityDefinition) error

	// Remove deletes an entity by ID.
	// Returns [ErrNotFound] when no entity with that ID exists.
	Remove(ctx context.Context, id string) error

	// BulkImport adds multiple entities atomically.
	// Each entity without an ID gets one auto-generated.
	// Returns the number of entities successfully imported and any error
	// that caused the import to abort early.
	BulkImport(ctx context.Context, entities []EntityDefinition) (int, error)
}

// ListOptions narrows the result set of [Store.List].
// All non-zero fields are applied as AND conditions.
type ListOptions struct {
	// Type restricts results to entities of this type.
	// An empty value matches all types.
	Type EntityType

	// Tags restricts results to entities that carry all of the specified tags.
	// An empty slice matches all entities regardless of their tags.
	Tags []string
}
