// Package memory defines the three-layer memory architecture used by Glyphoxa NPC agents.
//
// The architecture is organised as a hierarchy of increasing abstraction:
//
//   - L1 – Session Store ([SessionStore]): hot, time-ordered transcript log.
//     Allows fast writes and recency-window retrieval during an active session.
//   - L2 – Semantic Index ([SemanticIndex]): vector store for embedding-based
//     similarity search over chunked transcript content.
//   - L3 – Knowledge Graph ([KnowledgeGraph] / [GraphRAGQuerier]): a graph of
//     named entities and typed relationships, supporting multi-hop traversal
//     and graph-augmented retrieval (GraphRAG).
//
// All interfaces are public so that external packages can supply alternative
// storage backends (Postgres/pgvector, Redis, Neo4j, in-memory, …) without
// depending on glyphoxa internals.
//
// Every implementation must be safe for concurrent use.
package memory

import (
	"context"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// L1 supporting types
// ─────────────────────────────────────────────────────────────────────────────

// SearchOpts configures a keyword / full-text search over session entries (L1).
// All non-zero fields are applied as AND conditions.
type SearchOpts struct {
	// SessionID restricts the search to a single session.
	// An empty string searches across all sessions.
	SessionID string

	// After filters entries recorded after this instant (exclusive).
	// A zero Time disables the lower bound.
	After time.Time

	// Before filters entries recorded before this instant (exclusive).
	// A zero Time disables the upper bound.
	Before time.Time

	// SpeakerID restricts results to a specific speaker.
	// An empty string matches all speakers.
	SpeakerID string

	// Limit caps the number of results returned.
	// A value of 0 means the implementation may apply its own default.
	Limit int
}

// ─────────────────────────────────────────────────────────────────────────────
// L2 supporting types
// ─────────────────────────────────────────────────────────────────────────────

// Chunk is a processed segment of transcript content prepared for semantic
// indexing (L2). A Chunk carries its pre-computed embedding so the index does
// not need to re-embed on insertion.
type Chunk struct {
	// ID is the unique identifier for this chunk (e.g., a UUID).
	ID string

	// SessionID is the session this chunk belongs to.
	SessionID string

	// Content is the raw text of the chunk (may be a sentence, paragraph, or utterance).
	Content string

	// Embedding is the vector representation of Content.
	// Dimension must match the index configuration (e.g., 1536 for OpenAI
	// text-embedding-3-small).
	Embedding []float32

	// SpeakerID identifies who produced this chunk.
	SpeakerID string

	// EntityID is the knowledge-graph entity associated with this chunk
	// (NPC, location, item, etc.). Used for GraphRAG scoping.
	EntityID string

	// Topic is an optional coarse topic label (e.g., "quest", "trade", "lore").
	Topic string

	// Timestamp is when this chunk was recorded.
	Timestamp time.Time
}

// ChunkFilter narrows a semantic search to a subset of indexed chunks (L2).
// All non-zero fields are applied as AND conditions.
type ChunkFilter struct {
	// SessionID restricts results to a single session.
	SessionID string

	// SpeakerID restricts results to chunks produced by a specific speaker.
	SpeakerID string

	// EntityID restricts results to chunks associated with a specific entity.
	EntityID string

	// After filters chunks recorded after this instant (exclusive).
	After time.Time

	// Before filters chunks recorded before this instant (exclusive).
	Before time.Time
}

// ChunkResult pairs a retrieved chunk with its vector-space distance from the
// query embedding (L2). Lower Distance values indicate higher semantic similarity.
type ChunkResult struct {
	// Chunk is the retrieved segment.
	Chunk Chunk

	// Distance is the vector-space distance to the query embedding
	// (e.g., cosine distance or L2 — interpretation is implementation-defined).
	Distance float64
}

// ─────────────────────────────────────────────────────────────────────────────
// L3 supporting types
// ─────────────────────────────────────────────────────────────────────────────

// Entity represents a named object in the knowledge graph (L3).
// Entities are typed nodes; their dynamic attributes are stored in a
// free-form map to accommodate the diversity of tabletop RPG settings.
type Entity struct {
	// ID is the unique, stable identifier for this entity (e.g., a UUID).
	ID string

	// Type classifies the entity.
	// Recommended values: npc, player, location, item, faction, event, quest, concept.
	// Custom values are allowed.
	Type string

	// Name is the canonical display name (e.g., "Eldrinax the Undying").
	Name string

	// Attributes holds arbitrary key/value metadata specific to this entity
	// (e.g., alignment, health, occupation, description).
	Attributes map[string]any

	// CreatedAt is when the entity was first added to the graph.
	CreatedAt time.Time

	// UpdatedAt is when the entity was last modified.
	UpdatedAt time.Time
}

// Provenance records the origin of a fact asserted in the knowledge graph.
// It is embedded in [Relationship] to allow downstream reasoning about reliability.
type Provenance struct {
	// SessionID is the game session during which this fact was established.
	SessionID string

	// Timestamp is when the fact was established.
	Timestamp time.Time

	// Confidence is the model's confidence in this fact (0.0–1.0).
	Confidence float64

	// Source describes how the fact was derived.
	// Well-known values: "stated" (directly spoken), "inferred" (model reasoning).
	Source string

	// DMConfirmed indicates a human Dungeon Master has validated this fact.
	DMConfirmed bool
}

// Relationship is a directed, typed edge between two entities in the knowledge
// graph (L3).
type Relationship struct {
	// SourceID is the ID of the originating entity.
	SourceID string

	// TargetID is the ID of the destination entity.
	TargetID string

	// RelType is the semantic label of the relationship
	// (e.g., "knows", "hates", "owns", "member_of").
	RelType string

	// Attributes holds additional edge metadata
	// (e.g., since, strength, public, description).
	Attributes map[string]any

	// Provenance records the evidence trail for this relationship.
	Provenance Provenance

	// CreatedAt is when this relationship was first added.
	CreatedAt time.Time
}

// EntityFilter specifies predicates for entity lookup queries.
// All non-zero fields are applied as AND conditions.
type EntityFilter struct {
	// Type restricts results to entities of this type. Empty matches all types.
	Type string

	// Name restricts results to entities whose name contains this substring
	// (case-insensitive). Empty matches all names.
	Name string

	// AttributeQuery is a map of attribute keys to required values.
	// An entity matches if every key/value pair in AttributeQuery is present
	// in its Attributes map.
	AttributeQuery map[string]any
}

// relQueryOptions accumulates options for [KnowledgeGraph.GetRelationships].
// Unexported — callers configure it via [RelQueryOpt] functional options.
type relQueryOptions struct {
	relTypes     []string
	directionIn  bool
	directionOut bool
	limit        int
}

// RelQueryOpt is a functional option for [KnowledgeGraph.GetRelationships].
type RelQueryOpt func(*relQueryOptions)

// WithRelTypes restricts the returned relationships to those whose RelType is
// in the provided list. An empty list (the default) returns all types.
func WithRelTypes(relTypes ...string) RelQueryOpt {
	return func(o *relQueryOptions) {
		o.relTypes = append(o.relTypes, relTypes...)
	}
}

// WithIncoming includes relationships where the queried entity is the target
// (i.e., inbound edges). By default only outgoing relationships are returned.
func WithIncoming() RelQueryOpt {
	return func(o *relQueryOptions) { o.directionIn = true }
}

// WithOutgoing includes relationships where the queried entity is the source
// (i.e., outbound edges). This is the default behaviour; calling it explicitly
// is a no-op but improves readability when combined with [WithIncoming].
func WithOutgoing() RelQueryOpt {
	return func(o *relQueryOptions) { o.directionOut = true }
}

// WithRelLimit caps the number of relationships returned.
// A value of 0 means the implementation may apply its own default.
func WithRelLimit(n int) RelQueryOpt {
	return func(o *relQueryOptions) { o.limit = n }
}

// traversalOptions accumulates options for [KnowledgeGraph.Neighbors].
// Unexported — callers configure it via [TraversalOpt] functional options.
type traversalOptions struct {
	relTypes  []string
	nodeTypes []string
	maxNodes  int
}

// TraversalOpt is a functional option for [KnowledgeGraph.Neighbors] graph
// traversals.
type TraversalOpt func(*traversalOptions)

// TraverseRelTypes restricts traversal to edges whose RelType is in the
// provided list. An empty list (the default) follows all edge types.
func TraverseRelTypes(relTypes ...string) TraversalOpt {
	return func(o *traversalOptions) {
		o.relTypes = append(o.relTypes, relTypes...)
	}
}

// TraverseNodeTypes restricts traversal to entity nodes whose Type is in the
// provided list. An empty list (the default) visits all node types.
func TraverseNodeTypes(nodeTypes ...string) TraversalOpt {
	return func(o *traversalOptions) {
		o.nodeTypes = append(o.nodeTypes, nodeTypes...)
	}
}

// TraverseMaxNodes caps the number of entities returned during a traversal.
// A value of 0 means the implementation may apply its own default.
func TraverseMaxNodes(n int) TraversalOpt {
	return func(o *traversalOptions) { o.maxNodes = n }
}

// NPCIdentity is a compact snapshot of an NPC's knowledge-graph identity,
// suitable for injection into a system prompt or context window.
type NPCIdentity struct {
	// Entity is the NPC's own node in the knowledge graph.
	Entity Entity

	// Relationships are all direct edges from/to this NPC.
	Relationships []Relationship

	// RelatedEntities are the entities connected to this NPC by Relationships.
	RelatedEntities []Entity
}

// ContextResult pairs a knowledge-graph entity with retrieved textual content
// that is relevant to a [GraphRAGQuerier.QueryWithContext] call.
type ContextResult struct {
	// Entity is the knowledge-graph node that anchors this result.
	Entity Entity

	// Content is the retrieved text passage relevant to the query.
	Content string

	// Score is the combined retrieval relevance score (0.0–1.0, higher is better).
	Score float64
}

// ─────────────────────────────────────────────────────────────────────────────
// L1 – Session Store interface
// ─────────────────────────────────────────────────────────────────────────────

// SessionStore is the L1 memory layer: a time-ordered, append-only log of
// [TranscriptEntry] records for one or more game sessions.
//
// Entries must be returned in chronological order unless otherwise specified.
// Implementations must be safe for concurrent use.
type SessionStore interface {
	// WriteEntry appends a TranscriptEntry to the store for the given session.
	// sessionID must be non-empty.
	// Returns an error only on persistent storage failure.
	WriteEntry(ctx context.Context, sessionID string, entry TranscriptEntry) error

	// GetRecent returns all entries for the given session whose Timestamp is
	// no earlier than time.Now()-duration.
	// Returns an empty (non-nil) slice when no matching entries exist.
	GetRecent(ctx context.Context, sessionID string, duration time.Duration) ([]TranscriptEntry, error)

	// Search performs keyword / full-text search over stored entries.
	// The query string is matched against the Text field.
	// opts refines the result set by time range, speaker, or session scope.
	// Returns an empty (non-nil) slice when no entries match.
	Search(ctx context.Context, query string, opts SearchOpts) ([]TranscriptEntry, error)
}

// ─────────────────────────────────────────────────────────────────────────────
// L2 – Semantic Index interface
// ─────────────────────────────────────────────────────────────────────────────

// SemanticIndex is the L2 memory layer: a vector store for embedding-based
// similarity search over chunked transcript content.
//
// Callers are responsible for producing embeddings before calling IndexChunk or
// Search. Implementations must be safe for concurrent use.
type SemanticIndex interface {
	// IndexChunk stores a pre-embedded [Chunk] in the vector index.
	// If a chunk with the same ID already exists it must be replaced (upsert).
	IndexChunk(ctx context.Context, chunk Chunk) error

	// Search finds the topK chunks whose embeddings are closest to the query
	// embedding, filtered by filter.
	// Results are ordered by ascending Distance (most similar first).
	// Returns an empty (non-nil) slice when no chunks match.
	Search(ctx context.Context, embedding []float32, topK int, filter ChunkFilter) ([]ChunkResult, error)
}

// ─────────────────────────────────────────────────────────────────────────────
// L3 – Knowledge Graph interface
// ─────────────────────────────────────────────────────────────────────────────

// KnowledgeGraph is the L3 memory layer: a graph of named [Entity] nodes
// connected by typed [Relationship] edges.
//
// It supports full CRUD on nodes and edges, multi-hop neighbourhood traversal,
// shortest-path queries, and NPC-specific projection methods.
//
// Mutating operations that act on a primary key (AddEntity, AddRelationship)
// must behave as upserts rather than returning an error on duplicates.
// Deletions of non-existent records are not errors.
//
// Implementations must be safe for concurrent use.
type KnowledgeGraph interface {
	// AddEntity upserts an entity into the graph.
	// If an entity with the same ID already exists it is completely replaced.
	AddEntity(ctx context.Context, entity Entity) error

	// GetEntity retrieves an entity by its unique ID.
	// Returns (nil, nil) when the entity does not exist.
	GetEntity(ctx context.Context, id string) (*Entity, error)

	// UpdateEntity merges attrs into the Attributes map of the specified entity
	// and refreshes its UpdatedAt timestamp. Keys present in attrs overwrite
	// existing values; absent keys are left unchanged.
	// Returns an error when the entity does not exist.
	UpdateEntity(ctx context.Context, id string, attrs map[string]any) error

	// DeleteEntity removes the entity and all its associated relationships from
	// the graph. Deleting a non-existent entity is not an error.
	DeleteEntity(ctx context.Context, id string) error

	// FindEntities returns all entities matching filter.
	// Returns an empty (non-nil) slice when no entities match.
	FindEntities(ctx context.Context, filter EntityFilter) ([]Entity, error)

	// AddRelationship upserts a directed edge between two entities.
	// If a relationship with the same (SourceID, TargetID, RelType) already
	// exists it is completely replaced.
	AddRelationship(ctx context.Context, rel Relationship) error

	// GetRelationships returns relationships associated with entityID.
	// By default only outgoing edges are returned; use [WithIncoming] to include
	// inbound edges, and [WithRelTypes] to filter by edge type.
	// Returns an empty (non-nil) slice when no relationships match.
	GetRelationships(ctx context.Context, entityID string, opts ...RelQueryOpt) ([]Relationship, error)

	// DeleteRelationship removes the directed edge identified by (sourceID,
	// targetID, relType). Deleting a non-existent edge is not an error.
	DeleteRelationship(ctx context.Context, sourceID, targetID, relType string) error

	// Neighbors performs a breadth-first traversal from entityID up to depth
	// hops and returns all reachable entities (the start entity is excluded).
	// [TraversalOpt] options can restrict which edge or node types are followed.
	// Returns an empty (non-nil) slice when no neighbours are reachable.
	Neighbors(ctx context.Context, entityID string, depth int, opts ...TraversalOpt) ([]Entity, error)

	// FindPath returns the shortest sequence of entities connecting fromID to
	// toID inclusive, following directed edges up to maxDepth hops.
	// Returns an empty (non-nil) slice when no path exists within maxDepth.
	FindPath(ctx context.Context, fromID, toID string, maxDepth int) ([]Entity, error)

	// VisibleSubgraph returns the subset of the graph visible from the
	// perspective of npcID: the NPC node itself, all entities it has direct
	// relationships with, and those relationships.
	// Implementations may apply visibility rules (e.g., only publicly known facts).
	VisibleSubgraph(ctx context.Context, npcID string) ([]Entity, []Relationship, error)

	// IdentitySnapshot assembles a compact [NPCIdentity] for npcID, suitable for
	// injecting into a system prompt or context window.
	IdentitySnapshot(ctx context.Context, npcID string) (*NPCIdentity, error)
}

// ─────────────────────────────────────────────────────────────────────────────
// GraphRAG querier (extends KnowledgeGraph)
// ─────────────────────────────────────────────────────────────────────────────

// GraphRAGQuerier extends [KnowledgeGraph] with graph-augmented retrieval
// (GraphRAG). It combines structured graph traversal with semantic text
// retrieval to produce contextually grounded results for LLM consumption.
//
// Two query methods are provided:
//   - [GraphRAGQuerier.QueryWithContext] uses full-text search (FTS) and
//     requires no embedding provider. Useful as a fallback when embeddings
//     are unavailable or during entity extraction where embedding budget is
//     limited.
//   - [GraphRAGQuerier.QueryWithEmbedding] uses pgvector cosine similarity
//     against pre-computed chunk embeddings. This is the true GraphRAG path
//     described in the design docs and produces higher-quality results when
//     an embedding provider is available.
type GraphRAGQuerier interface {
	KnowledgeGraph

	// QueryWithContext performs a GraphRAG query using full-text search (FTS):
	// it matches the query string against chunk content using PostgreSQL
	// plainto_tsquery, scoped to entities in graphScope.
	// Results are ranked by FTS relevance (ts_rank).
	//
	// graphScope limits results to chunks whose entity association is in the
	// list. An empty graphScope searches all chunks.
	//
	// Use this when no embedding vector is available. For higher-quality
	// semantic retrieval, prefer [GraphRAGQuerier.QueryWithEmbedding].
	QueryWithContext(ctx context.Context, query string, graphScope []string) ([]ContextResult, error)

	// QueryWithEmbedding performs a GraphRAG query using vector similarity:
	// it finds the topK chunks whose embeddings are closest (cosine distance)
	// to the provided query embedding, scoped to entities in graphScope.
	// Results are ranked by ascending cosine distance (most similar first).
	//
	// graphScope limits results to chunks whose entity association is in the
	// list. An empty graphScope searches all chunks.
	//
	// The embedding must match the dimensionality of stored chunk embeddings.
	// topK controls the maximum number of results returned.
	QueryWithEmbedding(ctx context.Context, embedding []float32, topK int, graphScope []string) ([]ContextResult, error)
}
