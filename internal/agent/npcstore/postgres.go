package npcstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Schema is the SQL DDL for the npc_definitions table. Execute it via
// [PostgresStore.Migrate] or apply it manually during deployment.
const Schema = `
CREATE TABLE IF NOT EXISTS npc_definitions (
    id               TEXT PRIMARY KEY,
    campaign_id      TEXT NOT NULL DEFAULT '',
    name             TEXT NOT NULL,
    personality      TEXT NOT NULL DEFAULT '',
    engine           TEXT NOT NULL DEFAULT 'cascaded',
    voice            JSONB NOT NULL DEFAULT '{}',
    knowledge_scope  JSONB NOT NULL DEFAULT '[]',
    secret_knowledge JSONB NOT NULL DEFAULT '[]',
    behavior_rules   JSONB NOT NULL DEFAULT '[]',
    tools            JSONB NOT NULL DEFAULT '[]',
    budget_tier      TEXT NOT NULL DEFAULT 'fast',
    attributes       JSONB NOT NULL DEFAULT '{}',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_npc_definitions_campaign ON npc_definitions(campaign_id);
CREATE INDEX IF NOT EXISTS idx_npc_definitions_name ON npc_definitions(name);
`

// DB is the database interface used by [PostgresStore]. Both *pgxpool.Pool
// and *pgx.Conn satisfy this interface.
type DB interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// PostgresStore is a [Store] backed by a PostgreSQL database.
// It serialises structured sub-fields (voice, knowledge, etc.) as JSONB.
type PostgresStore struct {
	db DB
}

// Compile-time interface check.
var _ Store = (*PostgresStore)(nil)

// NewPostgresStore creates a new [PostgresStore] that uses the given database
// connection or pool. The caller is responsible for calling [PostgresStore.Migrate]
// to ensure the schema exists before issuing queries.
func NewPostgresStore(db DB) *PostgresStore {
	return &PostgresStore{db: db}
}

// Migrate executes the [Schema] DDL against the database, creating the
// npc_definitions table and indexes if they do not already exist.
func (s *PostgresStore) Migrate(ctx context.Context) error {
	_, err := s.db.Exec(ctx, Schema)
	if err != nil {
		return fmt.Errorf("npcstore: migrate: %w", err)
	}
	return nil
}

// Create inserts a new NPC definition. It validates the definition and returns
// an error if an NPC with the same ID already exists.
func (s *PostgresStore) Create(ctx context.Context, def *NPCDefinition) error {
	if err := def.Validate(); err != nil {
		return err
	}

	voiceJSON, err := json.Marshal(def.Voice)
	if err != nil {
		return fmt.Errorf("npcstore: marshal voice: %w", err)
	}
	ksJSON, err := json.Marshal(emptySlice(def.KnowledgeScope))
	if err != nil {
		return fmt.Errorf("npcstore: marshal knowledge_scope: %w", err)
	}
	skJSON, err := json.Marshal(emptySlice(def.SecretKnowledge))
	if err != nil {
		return fmt.Errorf("npcstore: marshal secret_knowledge: %w", err)
	}
	brJSON, err := json.Marshal(emptySlice(def.BehaviorRules))
	if err != nil {
		return fmt.Errorf("npcstore: marshal behavior_rules: %w", err)
	}
	toolsJSON, err := json.Marshal(emptySlice(def.Tools))
	if err != nil {
		return fmt.Errorf("npcstore: marshal tools: %w", err)
	}
	attrJSON, err := json.Marshal(emptyMap(def.Attributes))
	if err != nil {
		return fmt.Errorf("npcstore: marshal attributes: %w", err)
	}

	const query = `
		INSERT INTO npc_definitions (
			id, campaign_id, name, personality, engine,
			voice, knowledge_scope, secret_knowledge, behavior_rules, tools,
			budget_tier, attributes
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		RETURNING created_at, updated_at`

	err = s.db.QueryRow(ctx, query,
		def.ID, def.CampaignID, def.Name, def.Personality, defaultEngine(def.Engine),
		voiceJSON, ksJSON, skJSON, brJSON, toolsJSON,
		defaultBudgetTier(def.BudgetTier), attrJSON,
	).Scan(&def.CreatedAt, &def.UpdatedAt)
	if err != nil {
		if isDuplicateKeyError(err) {
			return fmt.Errorf("npcstore: npc with id %q already exists", def.ID)
		}
		return fmt.Errorf("npcstore: create: %w", err)
	}
	return nil
}

// Get retrieves an NPC definition by ID. It returns (nil, nil) if no NPC with
// the given ID exists.
func (s *PostgresStore) Get(ctx context.Context, id string) (*NPCDefinition, error) {
	const query = `
		SELECT id, campaign_id, name, personality, engine,
		       voice, knowledge_scope, secret_knowledge, behavior_rules, tools,
		       budget_tier, attributes, created_at, updated_at
		FROM npc_definitions
		WHERE id = $1`

	var def NPCDefinition
	var voiceJSON, ksJSON, skJSON, brJSON, toolsJSON, attrJSON []byte

	err := s.db.QueryRow(ctx, query, id).Scan(
		&def.ID, &def.CampaignID, &def.Name, &def.Personality, &def.Engine,
		&voiceJSON, &ksJSON, &skJSON, &brJSON, &toolsJSON,
		&def.BudgetTier, &attrJSON, &def.CreatedAt, &def.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("npcstore: get %q: %w", id, err)
	}

	if err := unmarshalFields(&def, voiceJSON, ksJSON, skJSON, brJSON, toolsJSON, attrJSON); err != nil {
		return nil, err
	}
	return &def, nil
}

// Update replaces an existing NPC definition. It validates the new definition
// and returns an error if the NPC is not found.
func (s *PostgresStore) Update(ctx context.Context, def *NPCDefinition) error {
	if err := def.Validate(); err != nil {
		return err
	}

	voiceJSON, err := json.Marshal(def.Voice)
	if err != nil {
		return fmt.Errorf("npcstore: marshal voice: %w", err)
	}
	ksJSON, err := json.Marshal(emptySlice(def.KnowledgeScope))
	if err != nil {
		return fmt.Errorf("npcstore: marshal knowledge_scope: %w", err)
	}
	skJSON, err := json.Marshal(emptySlice(def.SecretKnowledge))
	if err != nil {
		return fmt.Errorf("npcstore: marshal secret_knowledge: %w", err)
	}
	brJSON, err := json.Marshal(emptySlice(def.BehaviorRules))
	if err != nil {
		return fmt.Errorf("npcstore: marshal behavior_rules: %w", err)
	}
	toolsJSON, err := json.Marshal(emptySlice(def.Tools))
	if err != nil {
		return fmt.Errorf("npcstore: marshal tools: %w", err)
	}
	attrJSON, err := json.Marshal(emptyMap(def.Attributes))
	if err != nil {
		return fmt.Errorf("npcstore: marshal attributes: %w", err)
	}

	const query = `
		UPDATE npc_definitions SET
			campaign_id = $2, name = $3, personality = $4, engine = $5,
			voice = $6, knowledge_scope = $7, secret_knowledge = $8,
			behavior_rules = $9, tools = $10, budget_tier = $11,
			attributes = $12, updated_at = now()
		WHERE id = $1
		RETURNING updated_at`

	err = s.db.QueryRow(ctx, query,
		def.ID, def.CampaignID, def.Name, def.Personality, defaultEngine(def.Engine),
		voiceJSON, ksJSON, skJSON, brJSON, toolsJSON,
		defaultBudgetTier(def.BudgetTier), attrJSON,
	).Scan(&def.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("npcstore: npc with id %q not found", def.ID)
		}
		return fmt.Errorf("npcstore: update: %w", err)
	}
	return nil
}

// Delete removes an NPC definition by ID. Deleting a non-existent NPC is not
// an error.
func (s *PostgresStore) Delete(ctx context.Context, id string) error {
	const query = `DELETE FROM npc_definitions WHERE id = $1`
	_, err := s.db.Exec(ctx, query, id)
	if err != nil {
		return fmt.Errorf("npcstore: delete %q: %w", id, err)
	}
	return nil
}

// List returns all NPC definitions, optionally filtered by campaign ID. An
// empty campaignID returns all definitions.
func (s *PostgresStore) List(ctx context.Context, campaignID string) ([]NPCDefinition, error) {
	var (
		rows pgx.Rows
		err  error
	)
	if campaignID == "" {
		const query = `
			SELECT id, campaign_id, name, personality, engine,
			       voice, knowledge_scope, secret_knowledge, behavior_rules, tools,
			       budget_tier, attributes, created_at, updated_at
			FROM npc_definitions
			ORDER BY name`
		rows, err = s.db.Query(ctx, query)
	} else {
		const query = `
			SELECT id, campaign_id, name, personality, engine,
			       voice, knowledge_scope, secret_knowledge, behavior_rules, tools,
			       budget_tier, attributes, created_at, updated_at
			FROM npc_definitions
			WHERE campaign_id = $1
			ORDER BY name`
		rows, err = s.db.Query(ctx, query, campaignID)
	}
	if err != nil {
		return nil, fmt.Errorf("npcstore: list: %w", err)
	}
	defer rows.Close()

	var defs []NPCDefinition
	for rows.Next() {
		var def NPCDefinition
		var voiceJSON, ksJSON, skJSON, brJSON, toolsJSON, attrJSON []byte

		if err := rows.Scan(
			&def.ID, &def.CampaignID, &def.Name, &def.Personality, &def.Engine,
			&voiceJSON, &ksJSON, &skJSON, &brJSON, &toolsJSON,
			&def.BudgetTier, &attrJSON, &def.CreatedAt, &def.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("npcstore: list scan: %w", err)
		}

		if err := unmarshalFields(&def, voiceJSON, ksJSON, skJSON, brJSON, toolsJSON, attrJSON); err != nil {
			return nil, err
		}
		defs = append(defs, def)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("npcstore: list: %w", err)
	}
	return defs, nil
}

// Upsert creates or replaces an NPC definition. This is useful for importing
// definitions from YAML config files. The definition is validated before
// persistence.
func (s *PostgresStore) Upsert(ctx context.Context, def *NPCDefinition) error {
	if err := def.Validate(); err != nil {
		return err
	}

	voiceJSON, err := json.Marshal(def.Voice)
	if err != nil {
		return fmt.Errorf("npcstore: marshal voice: %w", err)
	}
	ksJSON, err := json.Marshal(emptySlice(def.KnowledgeScope))
	if err != nil {
		return fmt.Errorf("npcstore: marshal knowledge_scope: %w", err)
	}
	skJSON, err := json.Marshal(emptySlice(def.SecretKnowledge))
	if err != nil {
		return fmt.Errorf("npcstore: marshal secret_knowledge: %w", err)
	}
	brJSON, err := json.Marshal(emptySlice(def.BehaviorRules))
	if err != nil {
		return fmt.Errorf("npcstore: marshal behavior_rules: %w", err)
	}
	toolsJSON, err := json.Marshal(emptySlice(def.Tools))
	if err != nil {
		return fmt.Errorf("npcstore: marshal tools: %w", err)
	}
	attrJSON, err := json.Marshal(emptyMap(def.Attributes))
	if err != nil {
		return fmt.Errorf("npcstore: marshal attributes: %w", err)
	}

	const query = `
		INSERT INTO npc_definitions (
			id, campaign_id, name, personality, engine,
			voice, knowledge_scope, secret_knowledge, behavior_rules, tools,
			budget_tier, attributes
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT (id) DO UPDATE SET
			campaign_id = EXCLUDED.campaign_id,
			name = EXCLUDED.name,
			personality = EXCLUDED.personality,
			engine = EXCLUDED.engine,
			voice = EXCLUDED.voice,
			knowledge_scope = EXCLUDED.knowledge_scope,
			secret_knowledge = EXCLUDED.secret_knowledge,
			behavior_rules = EXCLUDED.behavior_rules,
			tools = EXCLUDED.tools,
			budget_tier = EXCLUDED.budget_tier,
			attributes = EXCLUDED.attributes,
			updated_at = now()
		RETURNING created_at, updated_at`

	err = s.db.QueryRow(ctx, query,
		def.ID, def.CampaignID, def.Name, def.Personality, defaultEngine(def.Engine),
		voiceJSON, ksJSON, skJSON, brJSON, toolsJSON,
		defaultBudgetTier(def.BudgetTier), attrJSON,
	).Scan(&def.CreatedAt, &def.UpdatedAt)
	if err != nil {
		return fmt.Errorf("npcstore: upsert: %w", err)
	}
	return nil
}

// unmarshalFields deserialises the JSONB columns into the corresponding
// [NPCDefinition] fields.
func unmarshalFields(def *NPCDefinition, voice, ks, sk, br, tools, attrs []byte) error {
	if err := json.Unmarshal(voice, &def.Voice); err != nil {
		return fmt.Errorf("npcstore: unmarshal voice: %w", err)
	}
	if err := json.Unmarshal(ks, &def.KnowledgeScope); err != nil {
		return fmt.Errorf("npcstore: unmarshal knowledge_scope: %w", err)
	}
	if err := json.Unmarshal(sk, &def.SecretKnowledge); err != nil {
		return fmt.Errorf("npcstore: unmarshal secret_knowledge: %w", err)
	}
	if err := json.Unmarshal(br, &def.BehaviorRules); err != nil {
		return fmt.Errorf("npcstore: unmarshal behavior_rules: %w", err)
	}
	if err := json.Unmarshal(tools, &def.Tools); err != nil {
		return fmt.Errorf("npcstore: unmarshal tools: %w", err)
	}
	if err := json.Unmarshal(attrs, &def.Attributes); err != nil {
		return fmt.Errorf("npcstore: unmarshal attributes: %w", err)
	}
	return nil
}

// defaultEngine returns the engine value, defaulting to "cascaded" if empty.
func defaultEngine(e string) string {
	if e == "" {
		return "cascaded"
	}
	return e
}

// defaultBudgetTier returns the budget tier value, defaulting to "fast" if empty.
func defaultBudgetTier(bt string) string {
	if bt == "" {
		return "fast"
	}
	return bt
}

// emptySlice returns s if non-nil, otherwise an empty non-nil slice. This
// ensures JSON marshalling produces "[]" instead of "null".
func emptySlice(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// emptyMap returns m if non-nil, otherwise an empty non-nil map. This ensures
// JSON marshalling produces "{}" instead of "null".
func emptyMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// isDuplicateKeyError checks whether a PostgreSQL error is a unique-violation
// (SQLSTATE 23505).
func isDuplicateKeyError(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}
