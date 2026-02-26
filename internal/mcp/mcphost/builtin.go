package mcphost

import (
	"context"
	"fmt"

	"github.com/MrWong99/glyphoxa/internal/mcp"
	"github.com/MrWong99/glyphoxa/pkg/provider/llm"
)

// BuiltinTool represents a tool implemented as a Go function that runs in-process.
//
// Built-in tools bypass MCP protocol overhead: ExecuteTool calls the Handler
// directly without any network or subprocess round-trip. They are otherwise
// identical to external tools — subject to the same budget enforcement,
// calibration, and rolling-window metrics.
type BuiltinTool struct {
	// Definition is the tool's public descriptor presented to the LLM.
	Definition llm.ToolDefinition

	// Handler is the function invoked when ExecuteTool is called for this tool.
	// args is a JSON object string (e.g. "{}" or `{"key":"value"}`).
	// Returning a non-nil error marks the result as an error.
	Handler func(ctx context.Context, args string) (string, error)

	// DeclaredP50 is the estimated median latency in milliseconds, used for
	// initial tier assignment before calibration.
	DeclaredP50 int64

	// DeclaredMax is the estimated worst-case latency in milliseconds.
	DeclaredMax int64
}

// RegisterBuiltin registers a built-in tool that is called in-process.
//
// If a tool with the same name is already registered it is replaced.
// The tool is assigned an initial [mcp.BudgetTier] based on its DeclaredP50:
//
//	DeclaredP50 ≤ 500  → BudgetFast
//	DeclaredP50 ≤ 1500 → BudgetStandard
//	otherwise          → BudgetDeep
//
// RegisterBuiltin is safe for concurrent use.
func (h *Host) RegisterBuiltin(tool BuiltinTool) error {
	if tool.Definition.Name == "" {
		return fmt.Errorf("mcp host: builtin tool must have a non-empty name")
	}
	if tool.Handler == nil {
		return fmt.Errorf("mcp host: builtin tool %q must have a non-nil handler", tool.Definition.Name)
	}

	tier := tierFromDeclaredP50(tool.DeclaredP50)

	entry := toolEntry{
		def:           tool.Definition,
		serverName:    builtinServerName,
		declaredP50Ms: tool.DeclaredP50,
		declaredMaxMs: tool.DeclaredMax,
		tier:          tier,
		measurements:  newRollingWindow(defaultWindowSize),
		builtinFn:     tool.Handler,
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	h.tools[tool.Definition.Name] = entry
	return nil
}

// builtinServerName is the pseudo server name used for in-process tools.
const builtinServerName = "__builtin__"

// tierFromDeclaredP50 maps a declared P50 latency to a BudgetTier.
// It uses the same thresholds as [tierFromMeasuredP50].
func tierFromDeclaredP50(p50Ms int64) mcp.BudgetTier {
	return tierFromMeasuredP50(p50Ms)
}
