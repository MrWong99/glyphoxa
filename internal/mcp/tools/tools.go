// Package tools defines the shared [Tool] type used by all built-in MCP tool
// packages in Glyphoxa. Each sub-package exports a constructor function that
// returns a slice of [Tool] values ready for registration with the MCP Host.
package tools

import (
	"context"

	"github.com/MrWong99/glyphoxa/pkg/provider/llm"
)

// Tool represents a built-in tool ready for registration with the MCP Host.
//
// Each Tool carries its LLM-facing schema ([llm.ToolDefinition]) together
// with the handler function that is invoked when the LLM calls the tool.
// DeclaredP50 and DeclaredMax provide latency estimates used by the
// Budget Enforcer to assign tools to the correct [mcp.BudgetTier].
type Tool struct {
	// Definition is the tool's LLM-facing schema including its name,
	// description, and JSON Schema parameter specification.
	Definition llm.ToolDefinition

	// Handler executes the tool with JSON-encoded args and returns a
	// JSON-encoded result string on success, or a descriptive error.
	// Implementations must be safe for concurrent use and must respect
	// context cancellation.
	Handler func(ctx context.Context, args string) (string, error)

	// DeclaredP50 is the tool author's declared median (p50) execution
	// latency in milliseconds. Used by the Budget Enforcer for initial tier
	// assignment before live calibration data is available.
	DeclaredP50 int64

	// DeclaredMax is the tool author's declared p99 upper-bound latency in
	// milliseconds. Used as a hard timeout during tool execution.
	DeclaredMax int64
}
