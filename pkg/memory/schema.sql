-- Glyphoxa Memory Schema
-- PostgreSQL schema for all three memory layers.
--
-- Layer 1 (L1): session_entries — time-ordered transcript log.
-- Layer 2 (L2): chunks          — vector-embedded transcript segments.
-- Layer 3 (L3): entities        — knowledge-graph nodes.
--             : relationships   — knowledge-graph edges.
--
-- Requires the pgvector extension for L2 vector similarity search.
-- Installation: https://github.com/pgvector/pgvector

-- ─────────────────────────────────────────────────────────────────────────────
-- Extensions
-- ─────────────────────────────────────────────────────────────────────────────

CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- ─────────────────────────────────────────────────────────────────────────────
-- L1 – Session Entries
-- ─────────────────────────────────────────────────────────────────────────────

-- session_entries stores every TranscriptEntry written during a game session.
-- Rows are append-only; do not update or delete entries — the log is the
-- source of truth for what was said.
CREATE TABLE IF NOT EXISTS session_entries (
    -- Surrogate primary key; caller may supply a deterministic UUID.
    id              UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),

    -- session_id groups entries belonging to a single game session.
    session_id      TEXT        NOT NULL,

    -- speaker_id is the stable identifier of who spoke
    -- (player Discord/user ID or NPC identifier).
    speaker_id      TEXT        NOT NULL DEFAULT '',

    -- speaker_name is the human-readable name for display purposes.
    speaker_name    TEXT        NOT NULL DEFAULT '',

    -- text is the (possibly corrected) transcript content.
    text            TEXT        NOT NULL DEFAULT '',

    -- raw_text is the original STT output before any corrections.
    -- Preserved for debugging and re-correction.
    raw_text        TEXT        NOT NULL DEFAULT '',

    -- npc_id identifies the NPC agent that produced this entry.
    -- Empty for non-NPC (e.g. player) entries.
    npc_id          TEXT        NOT NULL DEFAULT '',

    -- timestamp is the wall-clock time when the utterance was recorded.
    timestamp       TIMESTAMPTZ NOT NULL,

    -- duration_ns stores utterance length in nanoseconds.
    -- Stored as int8 to avoid precision loss from interval rounding.
    duration_ns     BIGINT      NOT NULL DEFAULT 0
);

-- Primary access pattern: recent entries for a single session.
CREATE INDEX IF NOT EXISTS idx_session_entries_session_timestamp
    ON session_entries (session_id, timestamp DESC);

-- Speaker-scoped queries (e.g., "what has Aldric said today?").
CREATE INDEX IF NOT EXISTS idx_session_entries_speaker
    ON session_entries (session_id, speaker_id, timestamp DESC);

-- NPC-scoped queries (partial index to keep it lean).
CREATE INDEX IF NOT EXISTS idx_session_entries_npc
    ON session_entries (npc_id, timestamp DESC)
    WHERE npc_id <> '';

-- Full-text search over corrected transcript text.
CREATE INDEX IF NOT EXISTS idx_session_entries_text_fts
    ON session_entries USING GIN (to_tsvector('english', text));

-- ─────────────────────────────────────────────────────────────────────────────
-- L2 – Semantic Chunks
-- ─────────────────────────────────────────────────────────────────────────────

-- chunks stores pre-embedded transcript segments for vector similarity search.
-- The embedding column uses pgvector with dimension 1536, matching
-- OpenAI text-embedding-3-small. Adjust if using a different model.
CREATE TABLE IF NOT EXISTS chunks (
    -- Caller-assigned UUID. Use deterministic IDs to make IndexChunk idempotent
    -- via ON CONFLICT DO UPDATE (upsert).
    id          UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),

    -- session_id links this chunk to a game session.
    session_id  TEXT        NOT NULL,

    -- content is the raw text of this chunk.
    content     TEXT        NOT NULL DEFAULT '',

    -- embedding is the vector representation of content.
    -- Dimension 1536 matches OpenAI text-embedding-3-small; update as needed.
    embedding   vector(1536),

    -- speaker_id is the speaker who produced this chunk.
    speaker_id  TEXT        NOT NULL DEFAULT '',

    -- npc_id is the NPC context for this chunk, if any.
    npc_id      TEXT        NOT NULL DEFAULT '',

    -- topic is a coarse topic label (e.g., "quest", "trade", "lore").
    topic       TEXT        NOT NULL DEFAULT '',

    -- timestamp is when this chunk was recorded.
    timestamp   TIMESTAMPTZ NOT NULL
);

-- Session-scoped lookup.
CREATE INDEX IF NOT EXISTS idx_chunks_session_id
    ON chunks (session_id, timestamp DESC);

-- Speaker-scoped lookup within a session.
CREATE INDEX IF NOT EXISTS idx_chunks_speaker
    ON chunks (session_id, speaker_id);

-- NPC-scoped lookup (partial index; most chunks will have an empty npc_id).
CREATE INDEX IF NOT EXISTS idx_chunks_npc
    ON chunks (npc_id)
    WHERE npc_id <> '';

-- IVFFlat approximate nearest-neighbour index for cosine similarity search.
-- lists=100 is a sensible starting point; retune as the table grows.
-- Rebuild after large bulk inserts: REINDEX INDEX idx_chunks_embedding;
CREATE INDEX IF NOT EXISTS idx_chunks_embedding
    ON chunks USING ivfflat (embedding vector_cosine_ops)
    WITH (lists = 100);

-- ─────────────────────────────────────────────────────────────────────────────
-- L3 – Knowledge Graph: Entities
-- ─────────────────────────────────────────────────────────────────────────────

-- entities stores named nodes in the knowledge graph.
-- Recommended type values: npc, player, location, item, faction,
--                          event, quest, concept.
CREATE TABLE IF NOT EXISTS entities (
    -- Stable UUID primary key. Callers should assign UUIDs so that
    -- cross-session references remain stable across migrations.
    id          UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),

    -- type classifies the node (npc, player, location, item, …).
    type        TEXT        NOT NULL,

    -- name is the canonical display name (e.g., "Eldrinax the Undying").
    name        TEXT        NOT NULL,

    -- attributes holds arbitrary key/value metadata as a JSON object.
    -- Example: {"alignment": "neutral evil", "level": 12, "alive": false}
    attributes  JSONB       NOT NULL DEFAULT '{}',

    -- created_at is set on first insertion and never changes.
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- updated_at is refreshed on every UpdateEntity call.
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Type-based filtering (e.g., "find all locations").
CREATE INDEX IF NOT EXISTS idx_entities_type
    ON entities (type);

-- Exact and prefix name lookups.
CREATE INDEX IF NOT EXISTS idx_entities_name
    ON entities (name);

-- Full-text search over entity names.
CREATE INDEX IF NOT EXISTS idx_entities_name_fts
    ON entities USING GIN (to_tsvector('english', name));

-- Attribute containment / path queries.
-- Example: WHERE attributes @> '{"alignment": "neutral evil"}'
CREATE INDEX IF NOT EXISTS idx_entities_attributes
    ON entities USING GIN (attributes);

-- ─────────────────────────────────────────────────────────────────────────────
-- L3 – Knowledge Graph: Relationships
-- ─────────────────────────────────────────────────────────────────────────────

-- relationships stores directed, typed edges between entity nodes.
-- The composite primary key (source_id, target_id, rel_type) enforces
-- uniqueness and makes AddRelationship naturally idempotent via
-- ON CONFLICT DO UPDATE.
CREATE TABLE IF NOT EXISTS relationships (
    -- source_id is the originating entity.
    -- Cascading delete ensures orphaned edges are never left behind.
    source_id   UUID        NOT NULL REFERENCES entities(id) ON DELETE CASCADE,

    -- target_id is the destination entity.
    target_id   UUID        NOT NULL REFERENCES entities(id) ON DELETE CASCADE,

    -- rel_type is the semantic label (e.g., "knows", "hates", "owns",
    -- "member_of", "located_in").
    rel_type    TEXT        NOT NULL,

    -- attributes holds additional edge metadata as a JSON object.
    -- Example: {"since": "session-3", "strength": 0.8, "public": true}
    attributes  JSONB       NOT NULL DEFAULT '{}',

    -- provenance records the evidence trail for this relationship.
    -- Schema mirrors the Provenance Go struct:
    -- { "session_id": "...", "timestamp": "...", "confidence": 0.9,
    --   "source": "stated", "dm_confirmed": false }
    provenance  JSONB       NOT NULL DEFAULT '{}',

    -- created_at is set on first insertion.
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (source_id, target_id, rel_type)
);

-- Outgoing edge traversal from a source node.
CREATE INDEX IF NOT EXISTS idx_relationships_source
    ON relationships (source_id);

-- Incoming edge traversal to a target node (reverse direction).
CREATE INDEX IF NOT EXISTS idx_relationships_target
    ON relationships (target_id);

-- Edge-type filtering (e.g., all "hates" relationships globally).
CREATE INDEX IF NOT EXISTS idx_relationships_rel_type
    ON relationships (rel_type);

-- Provenance containment queries (e.g., all DM-confirmed facts).
-- Example: WHERE provenance @> '{"dm_confirmed": true}'
CREATE INDEX IF NOT EXISTS idx_relationships_provenance
    ON relationships USING GIN (provenance);
