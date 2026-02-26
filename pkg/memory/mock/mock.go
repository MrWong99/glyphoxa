// Package mock provides in-memory test doubles for the memory layer interfaces.
//
// Each mock records every method call for assertion in tests and exposes
// exported fields that control what the mock returns. All mocks are safe for
// concurrent use via an internal [sync.Mutex].
//
// Typical usage:
//
//	store := &mock.SessionStore{}
//	store.GetRecentResult = []types.TranscriptEntry{{Text: "hello"}}
//
//	// inject store into the system under test …
//
//	if got := store.CallCount("GetRecent"); got != 1 {
//	    t.Errorf("expected 1 GetRecent call, got %d", got)
//	}
package mock

import (
	"context"
	"sync"
	"time"

	"github.com/MrWong99/glyphoxa/pkg/memory"
	"github.com/MrWong99/glyphoxa/pkg/types"
)

// Call records the name and arguments of a single method invocation.
type Call struct {
	// Method is the name of the interface method that was called.
	Method string

	// Args holds the non-context arguments passed to the method, in order.
	Args []any
}

// ─────────────────────────────────────────────────────────────────────────────
// SessionStore mock (L1)
// ─────────────────────────────────────────────────────────────────────────────

// SessionStore is a configurable test double for [memory.SessionStore].
// All exported *Err fields default to nil (success); all exported *Result
// fields default to nil (empty slice returned).
type SessionStore struct {
	mu sync.Mutex

	// calls records every method invocation in order.
	calls []Call

	// WriteEntryErr is returned by [SessionStore.WriteEntry] when non-nil.
	WriteEntryErr error

	// GetRecentResult is returned by [SessionStore.GetRecent].
	// When nil, GetRecent returns an empty non-nil slice.
	GetRecentResult []types.TranscriptEntry

	// GetRecentErr is returned by [SessionStore.GetRecent] when non-nil.
	GetRecentErr error

	// SearchResult is returned by [SessionStore.Search].
	// When nil, Search returns an empty non-nil slice.
	SearchResult []types.TranscriptEntry

	// SearchErr is returned by [SessionStore.Search] when non-nil.
	SearchErr error
}

// Calls returns a copy of all recorded method invocations.
func (m *SessionStore) Calls() []Call {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Call, len(m.calls))
	copy(out, m.calls)
	return out
}

// CallCount returns how many times the named method was invoked.
func (m *SessionStore) CallCount(method string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, c := range m.calls {
		if c.Method == method {
			n++
		}
	}
	return n
}

// Reset clears all recorded calls without altering response configuration.
func (m *SessionStore) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = nil
}

// WriteEntry implements [memory.SessionStore].
func (m *SessionStore) WriteEntry(_ context.Context, sessionID string, entry types.TranscriptEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, Call{Method: "WriteEntry", Args: []any{sessionID, entry}})
	return m.WriteEntryErr
}

// GetRecent implements [memory.SessionStore].
func (m *SessionStore) GetRecent(_ context.Context, sessionID string, duration time.Duration) ([]types.TranscriptEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, Call{Method: "GetRecent", Args: []any{sessionID, duration}})
	if m.GetRecentResult == nil {
		return []types.TranscriptEntry{}, m.GetRecentErr
	}
	out := make([]types.TranscriptEntry, len(m.GetRecentResult))
	copy(out, m.GetRecentResult)
	return out, m.GetRecentErr
}

// Search implements [memory.SessionStore].
func (m *SessionStore) Search(_ context.Context, query string, opts memory.SearchOpts) ([]types.TranscriptEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, Call{Method: "Search", Args: []any{query, opts}})
	if m.SearchResult == nil {
		return []types.TranscriptEntry{}, m.SearchErr
	}
	out := make([]types.TranscriptEntry, len(m.SearchResult))
	copy(out, m.SearchResult)
	return out, m.SearchErr
}

// Ensure SessionStore satisfies the interface at compile time.
var _ memory.SessionStore = (*SessionStore)(nil)

// ─────────────────────────────────────────────────────────────────────────────
// SemanticIndex mock (L2)
// ─────────────────────────────────────────────────────────────────────────────

// SemanticIndex is a configurable test double for [memory.SemanticIndex].
type SemanticIndex struct {
	mu sync.Mutex

	calls []Call

	// IndexChunkErr is returned by [SemanticIndex.IndexChunk] when non-nil.
	IndexChunkErr error

	// SearchResult is returned by [SemanticIndex.Search].
	// When nil, Search returns an empty non-nil slice.
	SearchResult []memory.ChunkResult

	// SearchErr is returned by [SemanticIndex.Search] when non-nil.
	SearchErr error
}

// Calls returns a copy of all recorded method invocations.
func (m *SemanticIndex) Calls() []Call {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Call, len(m.calls))
	copy(out, m.calls)
	return out
}

// CallCount returns how many times the named method was invoked.
func (m *SemanticIndex) CallCount(method string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, c := range m.calls {
		if c.Method == method {
			n++
		}
	}
	return n
}

// Reset clears all recorded calls without altering response configuration.
func (m *SemanticIndex) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = nil
}

// IndexChunk implements [memory.SemanticIndex].
func (m *SemanticIndex) IndexChunk(_ context.Context, chunk memory.Chunk) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, Call{Method: "IndexChunk", Args: []any{chunk}})
	return m.IndexChunkErr
}

// Search implements [memory.SemanticIndex].
func (m *SemanticIndex) Search(_ context.Context, embedding []float32, topK int, filter memory.ChunkFilter) ([]memory.ChunkResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, Call{Method: "Search", Args: []any{embedding, topK, filter}})
	if m.SearchResult == nil {
		return []memory.ChunkResult{}, m.SearchErr
	}
	out := make([]memory.ChunkResult, len(m.SearchResult))
	copy(out, m.SearchResult)
	return out, m.SearchErr
}

// Ensure SemanticIndex satisfies the interface at compile time.
var _ memory.SemanticIndex = (*SemanticIndex)(nil)

// ─────────────────────────────────────────────────────────────────────────────
// KnowledgeGraph mock (L3)
// ─────────────────────────────────────────────────────────────────────────────

// KnowledgeGraph is a configurable test double for [memory.KnowledgeGraph].
// Each method has a corresponding *Err field (returned on non-nil) and a
// corresponding *Result field (returned on success).
type KnowledgeGraph struct {
	mu sync.Mutex

	calls []Call

	// ──── AddEntity ────────────────────────────────────────────────────────
	AddEntityErr error

	// ──── GetEntity ────────────────────────────────────────────────────────
	GetEntityResult *memory.Entity
	GetEntityErr    error

	// ──── UpdateEntity ─────────────────────────────────────────────────────
	UpdateEntityErr error

	// ──── DeleteEntity ─────────────────────────────────────────────────────
	DeleteEntityErr error

	// ──── FindEntities ─────────────────────────────────────────────────────
	FindEntitiesResult []memory.Entity
	FindEntitiesErr    error

	// ──── AddRelationship ──────────────────────────────────────────────────
	AddRelationshipErr error

	// ──── GetRelationships ─────────────────────────────────────────────────
	GetRelationshipsResult []memory.Relationship
	GetRelationshipsErr    error

	// ──── DeleteRelationship ───────────────────────────────────────────────
	DeleteRelationshipErr error

	// ──── Neighbors ────────────────────────────────────────────────────────
	NeighborsResult []memory.Entity
	NeighborsErr    error

	// ──── FindPath ─────────────────────────────────────────────────────────
	FindPathResult []memory.Entity
	FindPathErr    error

	// ──── VisibleSubgraph ──────────────────────────────────────────────────
	VisibleSubgraphEntities      []memory.Entity
	VisibleSubgraphRelationships []memory.Relationship
	VisibleSubgraphErr           error

	// ──── IdentitySnapshot ─────────────────────────────────────────────────
	IdentitySnapshotResult *memory.NPCIdentity
	IdentitySnapshotErr    error
}

// Calls returns a copy of all recorded method invocations.
func (m *KnowledgeGraph) Calls() []Call {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Call, len(m.calls))
	copy(out, m.calls)
	return out
}

// CallCount returns how many times the named method was invoked.
func (m *KnowledgeGraph) CallCount(method string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, c := range m.calls {
		if c.Method == method {
			n++
		}
	}
	return n
}

// Reset clears all recorded calls without altering response configuration.
func (m *KnowledgeGraph) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = nil
}

// AddEntity implements [memory.KnowledgeGraph].
func (m *KnowledgeGraph) AddEntity(_ context.Context, entity memory.Entity) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, Call{Method: "AddEntity", Args: []any{entity}})
	return m.AddEntityErr
}

// GetEntity implements [memory.KnowledgeGraph].
func (m *KnowledgeGraph) GetEntity(_ context.Context, id string) (*memory.Entity, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, Call{Method: "GetEntity", Args: []any{id}})
	return m.GetEntityResult, m.GetEntityErr
}

// UpdateEntity implements [memory.KnowledgeGraph].
func (m *KnowledgeGraph) UpdateEntity(_ context.Context, id string, attrs map[string]any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, Call{Method: "UpdateEntity", Args: []any{id, attrs}})
	return m.UpdateEntityErr
}

// DeleteEntity implements [memory.KnowledgeGraph].
func (m *KnowledgeGraph) DeleteEntity(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, Call{Method: "DeleteEntity", Args: []any{id}})
	return m.DeleteEntityErr
}

// FindEntities implements [memory.KnowledgeGraph].
func (m *KnowledgeGraph) FindEntities(_ context.Context, filter memory.EntityFilter) ([]memory.Entity, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, Call{Method: "FindEntities", Args: []any{filter}})
	if m.FindEntitiesResult == nil {
		return []memory.Entity{}, m.FindEntitiesErr
	}
	out := make([]memory.Entity, len(m.FindEntitiesResult))
	copy(out, m.FindEntitiesResult)
	return out, m.FindEntitiesErr
}

// AddRelationship implements [memory.KnowledgeGraph].
func (m *KnowledgeGraph) AddRelationship(_ context.Context, rel memory.Relationship) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, Call{Method: "AddRelationship", Args: []any{rel}})
	return m.AddRelationshipErr
}

// GetRelationships implements [memory.KnowledgeGraph].
func (m *KnowledgeGraph) GetRelationships(_ context.Context, entityID string, opts ...memory.RelQueryOpt) ([]memory.Relationship, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, Call{Method: "GetRelationships", Args: []any{entityID, opts}})
	if m.GetRelationshipsResult == nil {
		return []memory.Relationship{}, m.GetRelationshipsErr
	}
	out := make([]memory.Relationship, len(m.GetRelationshipsResult))
	copy(out, m.GetRelationshipsResult)
	return out, m.GetRelationshipsErr
}

// DeleteRelationship implements [memory.KnowledgeGraph].
func (m *KnowledgeGraph) DeleteRelationship(_ context.Context, sourceID, targetID, relType string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, Call{Method: "DeleteRelationship", Args: []any{sourceID, targetID, relType}})
	return m.DeleteRelationshipErr
}

// Neighbors implements [memory.KnowledgeGraph].
func (m *KnowledgeGraph) Neighbors(_ context.Context, entityID string, depth int, opts ...memory.TraversalOpt) ([]memory.Entity, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, Call{Method: "Neighbors", Args: []any{entityID, depth, opts}})
	if m.NeighborsResult == nil {
		return []memory.Entity{}, m.NeighborsErr
	}
	out := make([]memory.Entity, len(m.NeighborsResult))
	copy(out, m.NeighborsResult)
	return out, m.NeighborsErr
}

// FindPath implements [memory.KnowledgeGraph].
func (m *KnowledgeGraph) FindPath(_ context.Context, fromID, toID string, maxDepth int) ([]memory.Entity, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, Call{Method: "FindPath", Args: []any{fromID, toID, maxDepth}})
	if m.FindPathResult == nil {
		return []memory.Entity{}, m.FindPathErr
	}
	out := make([]memory.Entity, len(m.FindPathResult))
	copy(out, m.FindPathResult)
	return out, m.FindPathErr
}

// VisibleSubgraph implements [memory.KnowledgeGraph].
func (m *KnowledgeGraph) VisibleSubgraph(_ context.Context, npcID string) ([]memory.Entity, []memory.Relationship, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, Call{Method: "VisibleSubgraph", Args: []any{npcID}})

	entities := m.VisibleSubgraphEntities
	if entities == nil {
		entities = []memory.Entity{}
	} else {
		out := make([]memory.Entity, len(entities))
		copy(out, entities)
		entities = out
	}

	rels := m.VisibleSubgraphRelationships
	if rels == nil {
		rels = []memory.Relationship{}
	} else {
		out := make([]memory.Relationship, len(rels))
		copy(out, rels)
		rels = out
	}

	return entities, rels, m.VisibleSubgraphErr
}

// IdentitySnapshot implements [memory.KnowledgeGraph].
func (m *KnowledgeGraph) IdentitySnapshot(_ context.Context, npcID string) (*memory.NPCIdentity, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, Call{Method: "IdentitySnapshot", Args: []any{npcID}})
	return m.IdentitySnapshotResult, m.IdentitySnapshotErr
}

// Ensure KnowledgeGraph satisfies the interface at compile time.
var _ memory.KnowledgeGraph = (*KnowledgeGraph)(nil)

// ─────────────────────────────────────────────────────────────────────────────
// GraphRAGQuerier mock (extends KnowledgeGraph)
// ─────────────────────────────────────────────────────────────────────────────

// GraphRAGQuerier is a configurable test double for [memory.GraphRAGQuerier].
// It embeds [KnowledgeGraph] and adds the two GraphRAG query methods.
type GraphRAGQuerier struct {
	KnowledgeGraph

	// ──── QueryWithContext ─────────────────────────────────────────────────
	QueryWithContextResult []memory.ContextResult
	QueryWithContextErr    error

	// ──── QueryWithEmbedding ──────────────────────────────────────────────
	QueryWithEmbeddingResult []memory.ContextResult
	QueryWithEmbeddingErr    error
}

// QueryWithContext implements [memory.GraphRAGQuerier].
func (m *GraphRAGQuerier) QueryWithContext(_ context.Context, query string, graphScope []string) ([]memory.ContextResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, Call{Method: "QueryWithContext", Args: []any{query, graphScope}})
	if m.QueryWithContextResult == nil {
		return []memory.ContextResult{}, m.QueryWithContextErr
	}
	out := make([]memory.ContextResult, len(m.QueryWithContextResult))
	copy(out, m.QueryWithContextResult)
	return out, m.QueryWithContextErr
}

// QueryWithEmbedding implements [memory.GraphRAGQuerier].
func (m *GraphRAGQuerier) QueryWithEmbedding(_ context.Context, embedding []float32, topK int, graphScope []string) ([]memory.ContextResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, Call{Method: "QueryWithEmbedding", Args: []any{embedding, topK, graphScope}})
	if m.QueryWithEmbeddingResult == nil {
		return []memory.ContextResult{}, m.QueryWithEmbeddingErr
	}
	out := make([]memory.ContextResult, len(m.QueryWithEmbeddingResult))
	copy(out, m.QueryWithEmbeddingResult)
	return out, m.QueryWithEmbeddingErr
}

// Ensure GraphRAGQuerier satisfies the interface at compile time.
var _ memory.GraphRAGQuerier = (*GraphRAGQuerier)(nil)
