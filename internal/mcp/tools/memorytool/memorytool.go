// Package memorytool provides built-in MCP tools that expose Glyphoxa's
// three-layer memory architecture to NPC agents.
//
// Four tools are exported via [NewTools]:
//   - "search_sessions" — full-text search across L1 session transcripts.
//   - "query_entities"  — entity lookup in the L3 knowledge graph.
//   - "get_summary"     — NPC identity snapshot from the L3 knowledge graph.
//   - "search_facts"    — full-text search for facts (L2 fallback via L1).
//
// All handlers are safe for concurrent use.
package memorytool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/MrWong99/glyphoxa/internal/mcp/tools"
	"github.com/MrWong99/glyphoxa/pkg/memory"
	"github.com/MrWong99/glyphoxa/pkg/provider/llm"
)

// ─────────────────────────────────────────────────────────────────────────────
// search_sessions
// ─────────────────────────────────────────────────────────────────────────────

// searchSessionsArgs is the JSON-decoded input for the "search_sessions" tool.
type searchSessionsArgs struct {
	// Query is the full-text search string matched against transcript entries.
	Query string `json:"query"`

	// SessionID optionally restricts the search to a single session.
	// An empty string searches across all sessions.
	SessionID string `json:"session_id,omitempty"`
}

// ─────────────────────────────────────────────────────────────────────────────
// query_entities
// ─────────────────────────────────────────────────────────────────────────────

// queryEntitiesArgs is the JSON-decoded input for the "query_entities" tool.
type queryEntitiesArgs struct {
	// Name restricts results to entities whose name contains this substring
	// (case-insensitive). Leave empty to match all names.
	Name string `json:"name,omitempty"`

	// Type restricts results to entities of this type (e.g. "npc", "location").
	// Leave empty to match all types.
	Type string `json:"type,omitempty"`
}

// ─────────────────────────────────────────────────────────────────────────────
// get_summary
// ─────────────────────────────────────────────────────────────────────────────

// getSummaryArgs is the JSON-decoded input for the "get_summary" tool.
type getSummaryArgs struct {
	// EntityID is the unique knowledge-graph ID of the entity to look up.
	EntityID string `json:"entity_id"`
}

// ─────────────────────────────────────────────────────────────────────────────
// search_facts
// ─────────────────────────────────────────────────────────────────────────────

// searchFactsArgs is the JSON-decoded input for the "search_facts" tool.
type searchFactsArgs struct {
	// Query is the search string used for full-text retrieval.
	Query string `json:"query"`

	// TopK caps the number of results returned. Defaults to 10 when ≤ 0.
	TopK int `json:"top_k,omitempty"`
}

// defaultTopK is the default result limit when TopK is not provided.
const defaultTopK = 10

// ─────────────────────────────────────────────────────────────────────────────
// Handler constructors
// ─────────────────────────────────────────────────────────────────────────────

// makeSearchSessionsHandler returns a handler for the "search_sessions" tool
// that delegates to sessions.Search.
func makeSearchSessionsHandler(sessions memory.SessionStore) func(context.Context, string) (string, error) {
	return func(ctx context.Context, args string) (string, error) {
		var a searchSessionsArgs
		if err := json.Unmarshal([]byte(args), &a); err != nil {
			return "", fmt.Errorf("memory tool: search_sessions: failed to parse arguments: %w", err)
		}
		if a.Query == "" {
			return "", fmt.Errorf("memory tool: search_sessions: query must not be empty")
		}

		entries, err := sessions.Search(ctx, a.Query, memory.SearchOpts{
			SessionID: a.SessionID,
		})
		if err != nil {
			return "", fmt.Errorf("memory tool: search_sessions: %w", err)
		}

		res, err := json.Marshal(entries)
		if err != nil {
			return "", fmt.Errorf("memory tool: search_sessions: failed to encode result: %w", err)
		}
		return string(res), nil
	}
}

// makeQueryEntitiesHandler returns a handler for the "query_entities" tool
// that delegates to graph.FindEntities.
func makeQueryEntitiesHandler(graph memory.KnowledgeGraph) func(context.Context, string) (string, error) {
	return func(ctx context.Context, args string) (string, error) {
		var a queryEntitiesArgs
		if err := json.Unmarshal([]byte(args), &a); err != nil {
			return "", fmt.Errorf("memory tool: query_entities: failed to parse arguments: %w", err)
		}

		entities, err := graph.FindEntities(ctx, memory.EntityFilter{
			Type: a.Type,
			Name: a.Name,
		})
		if err != nil {
			return "", fmt.Errorf("memory tool: query_entities: %w", err)
		}

		res, err := json.Marshal(entities)
		if err != nil {
			return "", fmt.Errorf("memory tool: query_entities: failed to encode result: %w", err)
		}
		return string(res), nil
	}
}

// makeGetSummaryHandler returns a handler for the "get_summary" tool that
// delegates to graph.IdentitySnapshot.
func makeGetSummaryHandler(graph memory.KnowledgeGraph) func(context.Context, string) (string, error) {
	return func(ctx context.Context, args string) (string, error) {
		var a getSummaryArgs
		if err := json.Unmarshal([]byte(args), &a); err != nil {
			return "", fmt.Errorf("memory tool: get_summary: failed to parse arguments: %w", err)
		}
		if a.EntityID == "" {
			return "", fmt.Errorf("memory tool: get_summary: entity_id must not be empty")
		}

		snapshot, err := graph.IdentitySnapshot(ctx, a.EntityID)
		if err != nil {
			return "", fmt.Errorf("memory tool: get_summary: %w", err)
		}
		if snapshot == nil {
			return "", fmt.Errorf("memory tool: get_summary: entity %q not found", a.EntityID)
		}

		res, err := json.Marshal(snapshot)
		if err != nil {
			return "", fmt.Errorf("memory tool: get_summary: failed to encode result: %w", err)
		}
		return string(res), nil
	}
}

// makeSearchFactsHandler returns a handler for the "search_facts" tool.
// Because an embedding provider is not available at this layer, the handler
// falls back to full-text search via sessions.Search.
func makeSearchFactsHandler(sessions memory.SessionStore) func(context.Context, string) (string, error) {
	return func(ctx context.Context, args string) (string, error) {
		var a searchFactsArgs
		if err := json.Unmarshal([]byte(args), &a); err != nil {
			return "", fmt.Errorf("memory tool: search_facts: failed to parse arguments: %w", err)
		}
		if a.Query == "" {
			return "", fmt.Errorf("memory tool: search_facts: query must not be empty")
		}

		topK := a.TopK
		if topK <= 0 {
			topK = defaultTopK
		}

		// No embedding provider is available here; use full-text search as a
		// fallback. The session store's Search method matches against the Text
		// field of transcript entries.
		entries, err := sessions.Search(ctx, a.Query, memory.SearchOpts{
			Limit: topK,
		})
		if err != nil {
			return "", fmt.Errorf("memory tool: search_facts: %w", err)
		}

		res, err := json.Marshal(entries)
		if err != nil {
			return "", fmt.Errorf("memory tool: search_facts: failed to encode result: %w", err)
		}
		return string(res), nil
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// NewTools
// ─────────────────────────────────────────────────────────────────────────────

// NewTools constructs the full set of memory tools, wired to the provided
// memory backend implementations.
//
// sessions is the L1 session store used by search_sessions and search_facts.
// index is the L2 semantic index (reserved for future embedding-based search).
// graph is the L3 knowledge graph used by query_entities and get_summary.
//
// All three parameters must be non-nil.
func NewTools(sessions memory.SessionStore, _ memory.SemanticIndex, graph memory.KnowledgeGraph) []tools.Tool {
	return []tools.Tool{
		{
			Definition: llm.ToolDefinition{
				Name:        "search_sessions",
				Description: "Perform a full-text search across session transcripts (L1 memory). Returns matching transcript entries ordered by relevance. Optionally restrict results to a specific session.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{
							"type":        "string",
							"description": "Full-text search query matched against transcript entry text.",
						},
						"session_id": map[string]any{
							"type":        "string",
							"description": "Restrict results to this session ID. Omit to search all sessions.",
						},
					},
					"required": []string{"query"},
				},
				EstimatedDurationMs: 100,
				MaxDurationMs:       500,
				Idempotent:          true,
				CacheableSeconds:    30,
			},
			Handler:     makeSearchSessionsHandler(sessions),
			DeclaredP50: 100,
			DeclaredMax: 500,
		},
		{
			Definition: llm.ToolDefinition{
				Name:        "query_entities",
				Description: "Find entities in the knowledge graph (L3 memory) by name and/or type. Returns matching entities with their attributes. Useful for looking up NPCs, locations, factions, and items.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name": map[string]any{
							"type":        "string",
							"description": "Substring to match against entity names (case-insensitive). Omit to match all names.",
						},
						"type": map[string]any{
							"type":        "string",
							"description": "Entity type to filter by (e.g. npc, player, location, item, faction, event, quest, concept). Omit to match all types.",
						},
					},
					"required": []string{},
				},
				EstimatedDurationMs: 50,
				MaxDurationMs:       200,
				Idempotent:          true,
				CacheableSeconds:    60,
			},
			Handler:     makeQueryEntitiesHandler(graph),
			DeclaredP50: 50,
			DeclaredMax: 200,
		},
		{
			Definition: llm.ToolDefinition{
				Name:        "get_summary",
				Description: "Retrieve a full identity snapshot for a knowledge-graph entity. The snapshot includes the entity's own attributes, all direct relationships, and the connected entities. Ideal for loading an NPC's full profile before a scene.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"entity_id": map[string]any{
							"type":        "string",
							"description": "The unique knowledge-graph ID of the entity to summarise.",
						},
					},
					"required": []string{"entity_id"},
				},
				EstimatedDurationMs: 80,
				MaxDurationMs:       300,
				Idempotent:          true,
				CacheableSeconds:    60,
			},
			Handler:     makeGetSummaryHandler(graph),
			DeclaredP50: 80,
			DeclaredMax: 300,
		},
		{
			Definition: llm.ToolDefinition{
				Name:        "search_facts",
				Description: "Search for facts and information across session history using full-text matching. Returns relevant transcript entries. For best results supply a focused query. Use top_k to control result count.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{
							"type":        "string",
							"description": "Full-text search query to retrieve relevant facts.",
						},
						"top_k": map[string]any{
							"type":        "integer",
							"description": "Maximum number of results to return. Defaults to 10.",
							"minimum":     1,
							"maximum":     100,
						},
					},
					"required": []string{"query"},
				},
				EstimatedDurationMs: 200,
				MaxDurationMs:       800,
				Idempotent:          true,
				CacheableSeconds:    30,
			},
			Handler:     makeSearchFactsHandler(sessions),
			DeclaredP50: 200,
			DeclaredMax: 800,
		},
	}
}
