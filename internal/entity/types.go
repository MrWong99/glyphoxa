// Package entity provides pre-session entity management for Glyphoxa.
//
// The DM uses this package to define and import game entities (NPCs, locations,
// items, factions, etc.) before a session starts. Entities defined here can be
// bulk-loaded into the knowledge graph (pkg/memory.KnowledgeGraph) at session
// start via the session initialisation layer.
//
// Supported input formats:
//   - Native YAML campaign files ([LoadCampaignFile], [LoadCampaignFromReader])
//   - Foundry VTT world exports ([ImportFoundryVTT])
//   - Roll20 campaign exports ([ImportRoll20])
//
// All store operations are safe for concurrent use.
package entity

// EntityDefinition is the declarative format for defining game entities
// (NPCs, locations, items, factions) outside of the knowledge graph.
// It is used for pre-session setup via YAML config or VTT import.
type EntityDefinition struct {
	// ID is a unique identifier. Auto-generated if empty during import.
	ID string `yaml:"id" json:"id"`

	// Name is the entity's display name.
	Name string `yaml:"name" json:"name"`

	// Type classifies the entity (npc, location, item, faction, quest, etc.)
	Type EntityType `yaml:"type" json:"type"`

	// Description is a free-text description of the entity.
	Description string `yaml:"description" json:"description"`

	// Properties holds arbitrary key-value metadata.
	Properties map[string]string `yaml:"properties,omitempty" json:"properties,omitempty"`

	// Relationships defines connections to other entities.
	Relationships []RelationshipDef `yaml:"relationships,omitempty" json:"relationships,omitempty"`

	// Tags are searchable labels for categorization.
	Tags []string `yaml:"tags,omitempty" json:"tags,omitempty"`

	// Visibility controls which NPCs can "see" this entity.
	// An empty slice means visible to all.
	Visibility []string `yaml:"visibility,omitempty" json:"visibility,omitempty"`
}

// EntityType classifies an entity in the knowledge graph.
type EntityType string

const (
	// EntityNPC represents a non-player character.
	EntityNPC EntityType = "npc"

	// EntityLocation represents a place in the game world.
	EntityLocation EntityType = "location"

	// EntityItem represents a physical object or artifact.
	EntityItem EntityType = "item"

	// EntityFaction represents an organisation, guild, or faction.
	EntityFaction EntityType = "faction"

	// EntityQuest represents a quest, mission, or story hook.
	EntityQuest EntityType = "quest"

	// EntityLore represents lore, historical records, or journal entries.
	EntityLore EntityType = "lore"
)

// IsValid reports whether t is a recognised entity type.
func (t EntityType) IsValid() bool {
	switch t {
	case EntityNPC, EntityLocation, EntityItem, EntityFaction, EntityQuest, EntityLore:
		return true
	}
	return false
}

// RelationshipDef declares a connection from an entity to another entity.
type RelationshipDef struct {
	// TargetID is the ID of the related entity.
	TargetID string `yaml:"target_id" json:"target_id"`

	// TargetName is an alternative to TargetID — resolved by name during import.
	// If both TargetID and TargetName are set, TargetID takes precedence.
	TargetName string `yaml:"target_name,omitempty" json:"target_name,omitempty"`

	// Type describes the relationship (e.g., "lives_in", "owns", "allied_with").
	Type string `yaml:"type" json:"type"`

	// Bidirectional indicates the relationship applies in both directions.
	Bidirectional bool `yaml:"bidirectional,omitempty" json:"bidirectional,omitempty"`
}
