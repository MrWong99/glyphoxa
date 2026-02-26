package diceroller

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// parseExpression tests
// ─────────────────────────────────────────────────────────────────────────────

func TestParseExpression_Valid(t *testing.T) {
	t.Parallel()
	tests := []struct {
		expr            string
		wantCount       int
		wantSides       int
		wantModifier    int
	}{
		{"1d6", 1, 6, 0},
		{"2d6+3", 2, 6, 3},
		{"4d8-1", 4, 8, -1},
		{"1d20", 1, 20, 0},
		{"10d10+5", 10, 10, 5},
		{"1d1", 1, 1, 0},
		{"d20", 1, 20, 0},   // implicit count of 1
		{"D6", 1, 6, 0},     // case-insensitive
		{"3d6+0", 3, 6, 0},
		{"1d100-50", 1, 100, -50},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			count, sides, modifier, err := parseExpression(tt.expr)
			if err != nil {
				t.Fatalf("parseExpression(%q) unexpected error: %v", tt.expr, err)
			}
			if count != tt.wantCount {
				t.Errorf("count = %d, want %d", count, tt.wantCount)
			}
			if sides != tt.wantSides {
				t.Errorf("sides = %d, want %d", sides, tt.wantSides)
			}
			if modifier != tt.wantModifier {
				t.Errorf("modifier = %d, want %d", modifier, tt.wantModifier)
			}
		})
	}
}

func TestParseExpression_Invalid(t *testing.T) {
	t.Parallel()
	cases := []string{
		"",         // empty
		"6",        // no 'd'
		"0d6",      // count < 1
		"2d0",      // sides < 1
		"xd6",      // non-numeric count
		"2dx",      // non-numeric sides
		"2d6+y",    // non-numeric modifier
		"2d6-z",    // non-numeric modifier after minus
		"abc",      // complete garbage
	}

	for _, expr := range cases {
		t.Run(expr, func(t *testing.T) {
			_, _, _, err := parseExpression(expr)
			if err == nil {
				t.Errorf("parseExpression(%q) expected error, got nil", expr)
			}
			if !strings.HasPrefix(err.Error(), "diceroller:") {
				t.Errorf("error %q should be prefixed with 'diceroller:'", err.Error())
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// roll tool tests
// ─────────────────────────────────────────────────────────────────────────────

func TestRollHandler_Valid(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		args       string
		wantCount  int  // expected number of rolls
		minTotal   int
		maxTotal   int
	}{
		{
			name:      "1d1",
			args:      `{"expression":"1d1"}`,
			wantCount: 1,
			minTotal:  1,
			maxTotal:  1,
		},
		{
			name:      "2d6+3",
			args:      `{"expression":"2d6+3"}`,
			wantCount: 2,
			minTotal:  5,  // 2*1+3
			maxTotal:  15, // 2*6+3
		},
		{
			name:      "4d8-1",
			args:      `{"expression":"4d8-1"}`,
			wantCount: 4,
			minTotal:  3,  // 4*1-1
			maxTotal:  31, // 4*8-1
		},
		{
			name:      "10d10+5",
			args:      `{"expression":"10d10+5"}`,
			wantCount: 10,
			minTotal:  15, // 10*1+5
			maxTotal:  105, // 10*10+5
		},
		{
			name:      "1d20",
			args:      `{"expression":"1d20"}`,
			wantCount: 1,
			minTotal:  1,
			maxTotal:  20,
		},
	}

	ctx := context.Background()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := rollHandler(ctx, tt.args)
			if err != nil {
				t.Fatalf("rollHandler(%q) unexpected error: %v", tt.args, err)
			}

			var res rollResult
			if err := json.Unmarshal([]byte(out), &res); err != nil {
				t.Fatalf("failed to unmarshal result: %v\noutput: %s", err, out)
			}

			if len(res.Rolls) != tt.wantCount {
				t.Errorf("len(Rolls) = %d, want %d", len(res.Rolls), tt.wantCount)
			}
			if res.Total < tt.minTotal || res.Total > tt.maxTotal {
				t.Errorf("Total = %d, want in [%d, %d]", res.Total, tt.minTotal, tt.maxTotal)
			}
			// Each die roll should be in [1, sides].
			// We can verify the total equals sum of rolls + modifier.
			// We verify by checking total consistency:
			sum := 0
			for _, r := range res.Rolls {
				if r < 1 {
					t.Errorf("individual roll %d < 1", r)
				}
				sum += r
			}
			// Parse expression to get modifier for cross-check.
			_, _, modifier, err := parseExpression(res.Expression)
			if err != nil {
				t.Fatalf("unexpected parse error on echoed expression %q: %v", res.Expression, err)
			}
			if res.Total != sum+modifier {
				t.Errorf("Total %d != sum(%d) + modifier(%d)", res.Total, sum, modifier)
			}
		})
	}
}

func TestRollHandler_Invalid(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cases := []struct {
		name string
		args string
	}{
		{"empty expression", `{"expression":""}`},
		{"no expression key", `{}`},
		{"invalid expression", `{"expression":"abc"}`},
		{"zero count", `{"expression":"0d6"}`},
		{"bad JSON", `{bad`},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := rollHandler(ctx, tt.args)
			if err == nil {
				t.Errorf("rollHandler(%q) expected error, got nil", tt.args)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// roll_table tool tests
// ─────────────────────────────────────────────────────────────────────────────

func TestRollTableHandler_Valid(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	validTables := []string{"wild_magic", "treasure_hoard", "random_encounter"}

	for _, tableName := range validTables {
		t.Run(tableName, func(t *testing.T) {
			args, _ := json.Marshal(rollTableArgs{TableName: tableName})
			out, err := rollTableHandler(ctx, string(args))
			if err != nil {
				t.Fatalf("rollTableHandler(%q) unexpected error: %v", tableName, err)
			}

			var res rollTableResult
			if err := json.Unmarshal([]byte(out), &res); err != nil {
				t.Fatalf("failed to unmarshal result: %v\noutput: %s", err, out)
			}

			if res.Table != tableName {
				t.Errorf("Table = %q, want %q", res.Table, tableName)
			}

			entries := builtinTables[tableName]
			if res.Roll < 1 || res.Roll > len(entries) {
				t.Errorf("Roll = %d, want in [1, %d]", res.Roll, len(entries))
			}

			if res.Result == "" {
				t.Error("Result must not be empty")
			}

			// Verify roll matches result.
			if res.Result != entries[res.Roll-1] {
				t.Errorf("Result %q does not match table entry for roll %d", res.Result, res.Roll)
			}
		})
	}
}

func TestRollTableHandler_Invalid(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cases := []struct {
		name string
		args string
	}{
		{"unknown table", `{"table_name":"nonexistent_table"}`},
		{"bad JSON", `{bad`},
		{"empty table name", `{"table_name":""}`},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := rollTableHandler(ctx, tt.args)
			if err == nil {
				t.Errorf("rollTableHandler(%q) expected error, got nil", tt.args)
			}
			if err != nil && !strings.HasPrefix(err.Error(), "diceroller:") {
				t.Errorf("error %q should be prefixed with 'diceroller:'", err.Error())
			}
		})
	}
}

// TestTools verifies that [Tools] returns the expected tool definitions.
func TestTools(t *testing.T) {
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

	for _, want := range []string{"roll", "roll_table"} {
		if !names[want] {
			t.Errorf("Tools() missing tool %q", want)
		}
	}
}
