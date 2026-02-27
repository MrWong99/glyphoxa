// Package npcstore provides persistent storage and management for NPC
// definitions. An [NPCDefinition] is the full declarative configuration for an
// NPC agent — personality, voice, knowledge, behaviour rules, and budget tier —
// and can be loaded from YAML config files, stored in a PostgreSQL database, or
// both.
//
// The primary abstraction is the [Store] interface, which offers CRUD and list
// operations. The reference implementation [PostgresStore] stores definitions in
// a single npc_definitions table using JSONB columns for structured sub-fields.
//
// Conversion helpers ([ToIdentity]) bridge between the storage representation
// and the runtime [agent.NPCIdentity] / [tts.VoiceProfile] types used by the
// orchestrator.
package npcstore

import (
	"errors"
	"fmt"
	"time"

	"github.com/MrWong99/glyphoxa/internal/agent"
	"github.com/MrWong99/glyphoxa/pkg/provider/tts"
)

// NPCDefinition is the full declarative configuration for an NPC agent.
// It can be loaded from YAML config files, stored in a database, or both.
type NPCDefinition struct {
	// ID is the unique identifier for this NPC definition.
	ID string `yaml:"id" json:"id"`

	// CampaignID groups NPCs that belong to the same campaign.
	CampaignID string `yaml:"campaign_id" json:"campaign_id"`

	// Name is the NPC's in-world display name (e.g., "Greymantle the Sage").
	Name string `yaml:"name" json:"name"`

	// Personality is a free-text description of the NPC's character, speech
	// patterns, quirks, and motivations.
	Personality string `yaml:"personality" json:"personality"`

	// Engine selects the voice-processing pipeline: "cascaded" (STT→LLM→TTS)
	// or "s2s" (speech-to-speech). An empty value defaults to "cascaded".
	Engine string `yaml:"engine" json:"engine"`

	// Voice configures the TTS voice used for this NPC.
	Voice VoiceConfig `yaml:"voice" json:"voice"`

	// KnowledgeScope lists topics or domains the NPC is knowledgeable about.
	KnowledgeScope []string `yaml:"knowledge_scope" json:"knowledge_scope"`

	// SecretKnowledge lists facts the NPC knows but will not volunteer.
	SecretKnowledge []string `yaml:"secret_knowledge" json:"secret_knowledge"`

	// BehaviorRules are hard constraints on the NPC's responses.
	BehaviorRules []string `yaml:"behavior_rules" json:"behavior_rules"`

	// Tools lists the tool names this NPC is allowed to invoke.
	Tools []string `yaml:"tools" json:"tools"`

	// BudgetTier controls the cost/quality trade-off: "fast", "standard", or
	// "deep". An empty value defaults to "fast".
	BudgetTier string `yaml:"budget_tier" json:"budget_tier"`

	// Attributes holds arbitrary key-value metadata for the NPC.
	Attributes map[string]any `yaml:"attributes" json:"attributes"`

	// CreatedAt is the time the definition was first persisted.
	CreatedAt time.Time `json:"created_at" yaml:"-"`

	// UpdatedAt is the time the definition was last modified.
	UpdatedAt time.Time `json:"updated_at" yaml:"-"`
}

// VoiceConfig describes the TTS voice configuration for an NPC.
type VoiceConfig struct {
	// Provider identifies which TTS provider to use (e.g., "elevenlabs", "azure").
	Provider string `yaml:"provider" json:"provider"`

	// VoiceID is the provider-specific voice identifier.
	VoiceID string `yaml:"voice_id" json:"voice_id"`

	// PitchShift adjusts pitch in semitones (-10 to +10, 0 = no change).
	PitchShift float64 `yaml:"pitch_shift" json:"pitch_shift"`

	// SpeedFactor adjusts speaking rate (0.5–2.0, 1.0 = normal speed).
	// A zero value means "use provider default".
	SpeedFactor float64 `yaml:"speed_factor" json:"speed_factor"`
}

// validEngines is the set of accepted Engine values.
var validEngines = map[string]struct{}{
	"":         {}, // empty defaults to "cascaded"
	"cascaded": {},
	"s2s":      {},
}

// validBudgetTiers is the set of accepted BudgetTier values.
var validBudgetTiers = map[string]struct{}{
	"":         {}, // empty defaults to "fast"
	"fast":     {},
	"standard": {},
	"deep":     {},
}

// Validate checks the NPCDefinition for logical consistency. It returns a
// joined error describing every violation found, or nil if the definition is
// valid.
func (d *NPCDefinition) Validate() error {
	var errs []error

	if d.Name == "" {
		errs = append(errs, fmt.Errorf("npcstore: name must not be empty"))
	}

	if _, ok := validEngines[d.Engine]; !ok {
		errs = append(errs, fmt.Errorf("npcstore: engine must be \"cascaded\" or \"s2s\", got %q", d.Engine))
	}

	if _, ok := validBudgetTiers[d.BudgetTier]; !ok {
		errs = append(errs, fmt.Errorf("npcstore: budget_tier must be \"fast\", \"standard\", or \"deep\", got %q", d.BudgetTier))
	}

	if d.Voice.SpeedFactor != 0 && (d.Voice.SpeedFactor < 0.5 || d.Voice.SpeedFactor > 2.0) {
		errs = append(errs, fmt.Errorf("npcstore: voice speed_factor must be in [0.5, 2.0], got %g", d.Voice.SpeedFactor))
	}

	if d.Voice.PitchShift < -10 || d.Voice.PitchShift > 10 {
		errs = append(errs, fmt.Errorf("npcstore: voice pitch_shift must be in [-10, 10], got %g", d.Voice.PitchShift))
	}

	return errors.Join(errs...)
}

// ToIdentity converts an [NPCDefinition] into an [agent.NPCIdentity] suitable
// for use by the runtime orchestrator.
func ToIdentity(def *NPCDefinition) agent.NPCIdentity {
	return agent.NPCIdentity{
		Name:        def.Name,
		Personality: def.Personality,
		Voice: tts.VoiceProfile{
			ID:          def.Voice.VoiceID,
			Name:        def.Name,
			Provider:    def.Voice.Provider,
			PitchShift:  def.Voice.PitchShift,
			SpeedFactor: def.Voice.SpeedFactor,
		},
		KnowledgeScope:  def.KnowledgeScope,
		SecretKnowledge: def.SecretKnowledge,
		BehaviorRules:   def.BehaviorRules,
	}
}
