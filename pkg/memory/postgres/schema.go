// Package postgres provides a PostgreSQL-backed implementation of the three-layer
// Glyphoxa memory architecture (L1 session log, L2 semantic index, L3 knowledge graph).
//
// All three layers share a single [pgxpool.Pool] connection pool. The pgvector
// extension must be available in the target database; [Migrate] installs it
// automatically via CREATE EXTENSION IF NOT EXISTS.
//
// Usage:
//
//	store, err := postgres.NewStore(ctx, dsn, 1536)
//	if err != nil { … }
//
//	// L1
//	_ = store.WriteEntry(ctx, sessionID, entry)
//
//	// L2
//	_ = store.IndexChunk(ctx, chunk)
//
//	// L3
//	_ = store.AddEntity(ctx, entity)
//
//	// GraphRAG
//	results, _ := store.QueryWithContext(ctx, "who is the blacksmith's ally?", scope)
package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ─────────────────────────────────────────────────────────────────────────────
// L1 DDL — session log
// ─────────────────────────────────────────────────────────────────────────────

const ddlSessionEntries = `
CREATE TABLE IF NOT EXISTS session_entries (
    id           BIGSERIAL    PRIMARY KEY,
    session_id   TEXT         NOT NULL,
    speaker_id   TEXT         NOT NULL DEFAULT '',
    speaker_name TEXT         NOT NULL DEFAULT '',
    text         TEXT         NOT NULL,
    raw_text     TEXT         NOT NULL DEFAULT '',
    npc_id       TEXT         NOT NULL DEFAULT '',
    timestamp    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    duration_ns  BIGINT       NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_session_entries_session_id
    ON session_entries (session_id);

CREATE INDEX IF NOT EXISTS idx_session_entries_timestamp
    ON session_entries (timestamp);

CREATE INDEX IF NOT EXISTS idx_session_entries_session_timestamp
    ON session_entries (session_id, timestamp);

CREATE INDEX IF NOT EXISTS idx_session_entries_fts
    ON session_entries USING GIN (to_tsvector('english', text));
`

// ─────────────────────────────────────────────────────────────────────────────
// L3 DDL — knowledge graph (entities + relationships)
// ─────────────────────────────────────────────────────────────────────────────

const ddlKnowledgeGraph = `
CREATE TABLE IF NOT EXISTS entities (
    id          TEXT         PRIMARY KEY,
    type        TEXT         NOT NULL,
    name        TEXT         NOT NULL,
    attributes  JSONB        NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_entities_type ON entities (type);
CREATE INDEX IF NOT EXISTS idx_entities_name ON entities (name);

CREATE TABLE IF NOT EXISTS relationships (
    source_id   TEXT         NOT NULL REFERENCES entities (id) ON DELETE CASCADE,
    target_id   TEXT         NOT NULL REFERENCES entities (id) ON DELETE CASCADE,
    rel_type    TEXT         NOT NULL,
    attributes  JSONB        NOT NULL DEFAULT '{}',
    provenance  JSONB        NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    PRIMARY KEY (source_id, target_id, rel_type)
);

CREATE INDEX IF NOT EXISTS idx_rel_source
    ON relationships (source_id);

CREATE INDEX IF NOT EXISTS idx_rel_target
    ON relationships (target_id);

CREATE INDEX IF NOT EXISTS idx_rel_type
    ON relationships (rel_type);

CREATE INDEX IF NOT EXISTS idx_rel_provenance_confidence
    ON relationships ((provenance->>'confidence'));
`

// ddlL2 returns the L2 DDL with the embedding dimension substituted.
// The vector dimension is baked into the column type at schema creation time.
func ddlL2(embeddingDimensions int) string {
	return fmt.Sprintf(`
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS chunks (
    id          TEXT         PRIMARY KEY,
    session_id  TEXT         NOT NULL,
    content     TEXT         NOT NULL,
    embedding   vector(%d),
    speaker_id  TEXT         NOT NULL DEFAULT '',
    entity_id   TEXT         NOT NULL DEFAULT '',
    topic       TEXT         NOT NULL DEFAULT '',
    timestamp   TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_chunks_session_id
    ON chunks (session_id);

CREATE INDEX IF NOT EXISTS idx_chunks_embedding
    ON chunks USING hnsw (embedding vector_cosine_ops);
`, embeddingDimensions)
}

// Migrate creates or ensures all required database tables and extensions exist.
// It is idempotent (CREATE TABLE IF NOT EXISTS / CREATE INDEX IF NOT EXISTS) and
// safe to call on every application start.
//
// embeddingDimensions must match the vector model configured for your deployment
// (e.g., 1536 for OpenAI text-embedding-3-small, 768 for nomic-embed-text).
// Changing this value after the first migration requires a manual schema update.
func Migrate(ctx context.Context, pool *pgxpool.Pool, embeddingDimensions int) error {
	statements := []string{
		ddlSessionEntries,
		ddlL2(embeddingDimensions),
		ddlKnowledgeGraph,
	}

	for _, stmt := range statements {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("postgres migrate: %w", err)
		}
	}
	return nil
}
