package mcphost

import (
	"cmp"
	"slices"

	"github.com/MrWong99/glyphoxa/internal/mcp"
	"github.com/MrWong99/glyphoxa/pkg/provider/llm"
)

// BudgetEnforcer filters tool definitions based on the active budget tier.
// It is the core mechanism that prevents over-budget tools from reaching the LLM.
//
// The zero value is ready for use.
type BudgetEnforcer struct{}

// FilterTools returns only the tool definitions whose tier is ≤ maxTier.
// The returned slice is sorted by estimated latency ascending (fastest first).
//
// Tier comparison uses the integer ordering: BudgetFast(0) ≤ BudgetStandard(1) ≤ BudgetDeep(2).
func (e *BudgetEnforcer) FilterTools(tools []toolEntry, maxTier mcp.BudgetTier) []llm.ToolDefinition {
	var result []toolEntry
	for i := range tools {
		if tools[i].tier <= maxTier {
			result = append(result, tools[i])
		}
	}

	// Sort by effective latency: prefer measured P50 when available, fall back to declared.
	slices.SortFunc(result, func(a, b toolEntry) int {
		return cmp.Compare(a.effectiveP50(), b.effectiveP50())
	})

	defs := make([]llm.ToolDefinition, len(result))
	for i, e := range result {
		defs[i] = e.def
	}
	return defs
}

// effectiveP50 returns the best-known P50 latency for sorting purposes.
// If the rolling window has measurements, that value is used; otherwise the
// declared P50 is returned.
func (e toolEntry) effectiveP50() int64 {
	if e.measurements != nil && e.measurements.Count() > 0 {
		return e.measuredP50Ms
	}
	return e.declaredP50Ms
}
