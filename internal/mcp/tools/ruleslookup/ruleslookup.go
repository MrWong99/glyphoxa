// Package ruleslookup provides built-in MCP tools for searching and retrieving
// game rules from an embedded D&D 5e SRD dataset. The dataset is stored
// in-process (no external I/O) and supports simple keyword search.
//
// Two tools are exported via [Tools]:
//   - "search_rules" — keyword search across rules by name and text.
//   - "get_rule"     — retrieve a specific rule by its unique ID.
//
// All handlers are safe for concurrent use.
package ruleslookup

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MrWong99/glyphoxa/internal/mcp/tools"
	"github.com/MrWong99/glyphoxa/pkg/provider/llm"
)

// searchRulesArgs is the JSON-decoded input for the "search_rules" tool.
type searchRulesArgs struct {
	// Query is the keyword or phrase to search for.
	Query string `json:"query"`

	// System optionally restricts results to a specific game system
	// (e.g. "dnd5e"). An empty string searches all systems.
	System string `json:"system,omitempty"`
}

// getRuleArgs is the JSON-decoded input for the "get_rule" tool.
type getRuleArgs struct {
	// ID is the unique rule identifier to look up.
	ID string `json:"id"`
}

// searchRulesHandler implements the "search_rules" tool. It matches the
// query string against rule names and text using case-insensitive substring
// matching, optionally filtered by game system.
func searchRulesHandler(_ context.Context, args string) (string, error) {
	var a searchRulesArgs
	if err := json.Unmarshal([]byte(args), &a); err != nil {
		return "", fmt.Errorf("rules: search_rules: failed to parse arguments: %w", err)
	}
	if a.Query == "" {
		return "", fmt.Errorf("rules: search_rules: query must not be empty")
	}

	queryLower := strings.ToLower(a.Query)
	systemLower := strings.ToLower(a.System)

	var matches []Rule
	for _, r := range srdRules {
		// Filter by system when specified.
		if systemLower != "" && strings.ToLower(r.System) != systemLower {
			continue
		}
		// Keyword match against both name and text.
		if strings.Contains(strings.ToLower(r.Name), queryLower) ||
			strings.Contains(strings.ToLower(r.Text), queryLower) {
			matches = append(matches, r)
		}
	}

	if matches == nil {
		matches = []Rule{} // return empty array, not null
	}

	res, err := json.Marshal(matches)
	if err != nil {
		return "", fmt.Errorf("rules: search_rules: failed to encode result: %w", err)
	}
	return string(res), nil
}

// getRuleHandler implements the "get_rule" tool. It retrieves a single rule
// by its unique ID from the embedded SRD dataset.
func getRuleHandler(_ context.Context, args string) (string, error) {
	var a getRuleArgs
	if err := json.Unmarshal([]byte(args), &a); err != nil {
		return "", fmt.Errorf("rules: get_rule: failed to parse arguments: %w", err)
	}
	if a.ID == "" {
		return "", fmt.Errorf("rules: get_rule: id must not be empty")
	}

	rule, ok := rulesByID[a.ID]
	if !ok {
		return "", fmt.Errorf("rules: get_rule: rule %q not found", a.ID)
	}

	res, err := json.Marshal(rule)
	if err != nil {
		return "", fmt.Errorf("rules: get_rule: failed to encode result: %w", err)
	}
	return string(res), nil
}

// Tools returns the slice of built-in rules-lookup tools ready for
// registration with the MCP Host.
//
// The returned tools are:
//   - "search_rules": keyword search across the embedded SRD rule set.
//   - "get_rule": retrieve a specific rule by ID.
func Tools() []tools.Tool {
	return []tools.Tool{
		{
			Definition: llm.ToolDefinition{
				Name:        "search_rules",
				Description: "Search the embedded D&D 5e SRD rule database by keyword. Returns matching rules with their name, category, and full text. Optionally restrict the search to a specific game system.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{
							"type":        "string",
							"description": "Keyword or phrase to search for across rule names and descriptions.",
						},
						"system": map[string]any{
							"type":        "string",
							"description": "Game system to filter by (e.g. dnd5e). Omit to search all systems.",
						},
					},
					"required": []string{"query"},
				},
				EstimatedDurationMs: 30,
				MaxDurationMs:       100,
				Idempotent:          true,
				CacheableSeconds:    300,
			},
			Handler:     searchRulesHandler,
			DeclaredP50: 30,
			DeclaredMax: 100,
		},
		{
			Definition: llm.ToolDefinition{
				Name:        "get_rule",
				Description: "Retrieve a specific game rule by its unique ID from the embedded SRD database. Use search_rules first to discover rule IDs.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id": map[string]any{
							"type":        "string",
							"description": "The unique rule ID to retrieve (e.g. condition-blinded, spell-fireball).",
						},
					},
					"required": []string{"id"},
				},
				EstimatedDurationMs: 5,
				MaxDurationMs:       20,
				Idempotent:          true,
				CacheableSeconds:    3600,
			},
			Handler:     getRuleHandler,
			DeclaredP50: 5,
			DeclaredMax: 20,
		},
	}
}
