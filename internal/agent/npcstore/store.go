package npcstore

import "context"

// Store provides CRUD operations for NPC definitions.
// Implementations must be safe for concurrent use.
type Store interface {
	// Create inserts a new NPC definition. The definition is validated before
	// insertion. Returns an error if an NPC with the same ID already exists.
	Create(ctx context.Context, def *NPCDefinition) error

	// Get retrieves an NPC definition by ID. Returns (nil, nil) if not found.
	Get(ctx context.Context, id string) (*NPCDefinition, error)

	// Update replaces an existing NPC definition. The definition is validated
	// before the update. Returns an error if the NPC is not found.
	Update(ctx context.Context, def *NPCDefinition) error

	// Delete removes an NPC definition by ID. Deleting a non-existent NPC is
	// not an error.
	Delete(ctx context.Context, id string) error

	// List returns all NPC definitions, optionally filtered by campaign ID.
	// An empty campaignID returns all definitions.
	List(ctx context.Context, campaignID string) ([]NPCDefinition, error)

	// Upsert creates or replaces an NPC definition (useful for YAML import).
	// The definition is validated before persistence.
	Upsert(ctx context.Context, def *NPCDefinition) error
}
