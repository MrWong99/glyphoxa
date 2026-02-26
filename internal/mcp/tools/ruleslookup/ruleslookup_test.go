package ruleslookup

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// search_rules
// ─────────────────────────────────────────────────────────────────────────────

func TestSearchRules_KnownKeyword(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	tests := []struct {
		query       string
		wantMinHits int
		wantID      string // at least one result should have this ID
	}{
		{"fireball", 1, "spell-fireball"},
		{"blinded", 1, "condition-blinded"},
		{"grapple", 1, "combat-grapple"},
		{"concentration", 1, "general-concentration"},
		{"advantage", 1, "general-advantage"},
		{"short rest", 1, "general-short-rest"},
		{"charmed", 1, "condition-charmed"},
		{"misty step", 1, "spell-misty-step"},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			args, _ := json.Marshal(searchRulesArgs{Query: tt.query})
			out, err := searchRulesHandler(ctx, string(args))
			if err != nil {
				t.Fatalf("searchRulesHandler(%q) unexpected error: %v", tt.query, err)
			}

			var results []Rule
			if err := json.Unmarshal([]byte(out), &results); err != nil {
				t.Fatalf("failed to unmarshal: %v\noutput: %s", err, out)
			}
			if len(results) < tt.wantMinHits {
				t.Errorf("expected at least %d result(s), got %d", tt.wantMinHits, len(results))
			}

			found := false
			for _, r := range results {
				if r.ID == tt.wantID {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected rule %q in results", tt.wantID)
			}
		})
	}
}

func TestSearchRules_CaseInsensitive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	queries := []string{"FIREBALL", "Fireball", "FiReBalL"}
	for _, q := range queries {
		t.Run(q, func(t *testing.T) {
			args, _ := json.Marshal(searchRulesArgs{Query: q})
			out, err := searchRulesHandler(ctx, string(args))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			var results []Rule
			if err := json.Unmarshal([]byte(out), &results); err != nil {
				t.Fatalf("failed to unmarshal: %v", err)
			}
			if len(results) == 0 {
				t.Error("expected at least one result for case-insensitive query")
			}
		})
	}
}

func TestSearchRules_NoMatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	args, _ := json.Marshal(searchRulesArgs{Query: "xyznomatch12345"})
	out, err := searchRulesHandler(ctx, string(args))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var results []Rule
	if err := json.Unmarshal([]byte(out), &results); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected empty results, got %d", len(results))
	}
}

func TestSearchRules_SystemFilter(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Searching for "fireball" with system "dnd5e" should find it.
	args, _ := json.Marshal(searchRulesArgs{Query: "fireball", System: "dnd5e"})
	out, err := searchRulesHandler(ctx, string(args))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var results []Rule
	if err := json.Unmarshal([]byte(out), &results); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected results for dnd5e system filter")
	}

	// Searching with a non-existent system should return empty.
	args2, _ := json.Marshal(searchRulesArgs{Query: "fireball", System: "pathfinder2e"})
	out2, err := searchRulesHandler(ctx, string(args2))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var results2 []Rule
	if err := json.Unmarshal([]byte(out2), &results2); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if len(results2) != 0 {
		t.Errorf("expected empty results for unknown system, got %d", len(results2))
	}
}

func TestSearchRules_EmptyQuery(t *testing.T) {
	t.Parallel()
	args, _ := json.Marshal(searchRulesArgs{Query: ""})
	_, err := searchRulesHandler(context.Background(), string(args))
	if err == nil {
		t.Error("expected error for empty query")
	}
	if !strings.HasPrefix(err.Error(), "rules:") {
		t.Errorf("error %q should be prefixed with 'rules:'", err.Error())
	}
}

func TestSearchRules_BadJSON(t *testing.T) {
	t.Parallel()
	_, err := searchRulesHandler(context.Background(), `{bad`)
	if err == nil {
		t.Error("expected error for bad JSON")
	}
}

func TestSearchRules_AllCategoriesPresent(t *testing.T) {
	t.Parallel()
	// Verify the embedded data covers all required categories.
	ctx := context.Background()
	categories := map[string]bool{
		"condition": false,
		"combat":    false,
		"spell":     false,
		"general":   false,
	}

	args, _ := json.Marshal(searchRulesArgs{Query: "the"}) // broad match
	out, err := searchRulesHandler(ctx, string(args))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var results []Rule
	if err := json.Unmarshal([]byte(out), &results); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	for _, r := range results {
		categories[r.Category] = true
	}

	for cat, found := range categories {
		if !found {
			t.Errorf("category %q not represented in embedded rules", cat)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// get_rule
// ─────────────────────────────────────────────────────────────────────────────

func TestGetRule_ValidIDs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	validIDs := []struct {
		id       string
		wantName string
	}{
		{"condition-blinded", "Blinded"},
		{"condition-charmed", "Charmed"},
		{"condition-frightened", "Frightened"},
		{"condition-poisoned", "Poisoned"},
		{"condition-stunned", "Stunned"},
		{"combat-opportunity-attack", "Opportunity Attack"},
		{"combat-cover", "Cover"},
		{"combat-flanking", "Flanking"},
		{"combat-grapple", "Grapple"},
		{"combat-shove", "Shove"},
		{"spell-fireball", "Fireball"},
		{"spell-shield", "Shield"},
		{"spell-healing-word", "Healing Word"},
		{"spell-counterspell", "Counterspell"},
		{"spell-misty-step", "Misty Step"},
		{"general-short-rest", "Short Rest"},
		{"general-long-rest", "Long Rest"},
		{"general-death-saves", "Death Saving Throws"},
		{"general-concentration", "Concentration"},
		{"general-advantage", "Advantage and Disadvantage"},
	}

	for _, tt := range validIDs {
		t.Run(tt.id, func(t *testing.T) {
			args, _ := json.Marshal(getRuleArgs{ID: tt.id})
			out, err := getRuleHandler(ctx, string(args))
			if err != nil {
				t.Fatalf("getRuleHandler(%q) unexpected error: %v", tt.id, err)
			}

			var rule Rule
			if err := json.Unmarshal([]byte(out), &rule); err != nil {
				t.Fatalf("failed to unmarshal: %v\noutput: %s", err, out)
			}
			if rule.ID != tt.id {
				t.Errorf("ID = %q, want %q", rule.ID, tt.id)
			}
			if rule.Name != tt.wantName {
				t.Errorf("Name = %q, want %q", rule.Name, tt.wantName)
			}
			if rule.Text == "" {
				t.Error("Text must not be empty")
			}
			if rule.System == "" {
				t.Error("System must not be empty")
			}
			if rule.Category == "" {
				t.Error("Category must not be empty")
			}
		})
	}
}

func TestGetRule_InvalidID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	args, _ := json.Marshal(getRuleArgs{ID: "nonexistent-rule-id"})
	_, err := getRuleHandler(ctx, string(args))
	if err == nil {
		t.Error("expected error for invalid rule ID")
	}
	if !strings.HasPrefix(err.Error(), "rules:") {
		t.Errorf("error %q should be prefixed with 'rules:'", err.Error())
	}
}

func TestGetRule_EmptyID(t *testing.T) {
	t.Parallel()
	args, _ := json.Marshal(getRuleArgs{ID: ""})
	_, err := getRuleHandler(context.Background(), string(args))
	if err == nil {
		t.Error("expected error for empty ID")
	}
}

func TestGetRule_BadJSON(t *testing.T) {
	t.Parallel()
	_, err := getRuleHandler(context.Background(), `{bad`)
	if err == nil {
		t.Error("expected error for bad JSON")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Tools
// ─────────────────────────────────────────────────────────────────────────────

func TestTools_ReturnsExpectedTools(t *testing.T) {
	t.Parallel()
	ts := Tools()
	if len(ts) != 2 {
		t.Fatalf("Tools() returned %d tools, want 2", len(ts))
	}

	names := map[string]bool{}
	for _, tool := range ts {
		names[tool.Definition.Name] = true
		if tool.Handler == nil {
			t.Errorf("tool %q has nil Handler", tool.Definition.Name)
		}
		if tool.DeclaredP50 <= 0 {
			t.Errorf("tool %q DeclaredP50 = %d, want > 0", tool.Definition.Name, tool.DeclaredP50)
		}
		if tool.DeclaredMax <= 0 {
			t.Errorf("tool %q DeclaredMax = %d, want > 0", tool.Definition.Name, tool.DeclaredMax)
		}
	}

	for _, want := range []string{"search_rules", "get_rule"} {
		if !names[want] {
			t.Errorf("Tools() missing tool %q", want)
		}
	}
}

// TestRulesByIDIndex verifies that the init() index is consistent with srdRules.
func TestRulesByIDIndex(t *testing.T) {
	t.Parallel()
	if len(rulesByID) != len(srdRules) {
		t.Errorf("rulesByID has %d entries, want %d (same as srdRules)", len(rulesByID), len(srdRules))
	}
	for _, r := range srdRules {
		if _, ok := rulesByID[r.ID]; !ok {
			t.Errorf("rulesByID missing entry for ID %q", r.ID)
		}
	}
}

// TestMinimumRuleCount verifies that each required category has at least 5 entries.
func TestMinimumRuleCount(t *testing.T) {
	t.Parallel()
	counts := map[string]int{}
	for _, r := range srdRules {
		counts[r.Category]++
	}

	required := []string{"condition", "combat", "spell", "general"}
	for _, cat := range required {
		if counts[cat] < 5 {
			t.Errorf("category %q has %d rules, want ≥ 5", cat, counts[cat])
		}
	}
}
