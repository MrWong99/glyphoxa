// Package mock provides an in-memory test double for the MCP [mcp.Host] interface.
//
// [Host] records every method call for assertion in tests and exposes exported
// fields that control what the mock returns. It is safe for concurrent use via
// an internal [sync.Mutex].
//
// Typical usage:
//
//	h := &mock.Host{}
//	h.AvailableToolsResult = []types.ToolDefinition{{Name: "lookup_npc"}}
//	h.ExecuteToolResult = &mcp.ToolResult{Content: `{"name":"Eldrinax"}`}
//
//	// inject h into the system under test …
//
//	if got := h.CallCount("ExecuteTool"); got != 1 {
//	    t.Errorf("expected 1 ExecuteTool call, got %d", got)
//	}
package mock

import (
	"context"
	"sync"

	"github.com/MrWong99/glyphoxa/internal/mcp"
	"github.com/MrWong99/glyphoxa/pkg/types"
)

// Call records the name and arguments of a single method invocation.
type Call struct {
	// Method is the name of the interface method that was called.
	Method string

	// Args holds the non-context arguments passed to the method, in order.
	Args []any
}

// Host is a configurable test double for [mcp.Host].
// All exported *Err fields default to nil (success); all exported *Result
// fields default to nil / zero values.
type Host struct {
	mu sync.Mutex

	// calls records every method invocation in order.
	calls []Call

	// ──── RegisterServer ───────────────────────────────────────────────────

	// RegisterServerErr is returned by [Host.RegisterServer] when non-nil.
	RegisterServerErr error

	// ──── AvailableTools ───────────────────────────────────────────────────

	// AvailableToolsResult is returned by [Host.AvailableTools].
	// When nil, AvailableTools returns an empty non-nil slice.
	AvailableToolsResult []types.ToolDefinition

	// ──── ExecuteTool ──────────────────────────────────────────────────────

	// ExecuteToolResult is returned by [Host.ExecuteTool] when ExecuteToolErr
	// is nil.
	// When nil and ExecuteToolErr is also nil, a zero-value *ToolResult is
	// returned.
	ExecuteToolResult *mcp.ToolResult

	// ExecuteToolErr is returned by [Host.ExecuteTool] when non-nil.
	ExecuteToolErr error

	// ──── Calibrate ────────────────────────────────────────────────────────

	// CalibrateErr is returned by [Host.Calibrate] when non-nil.
	CalibrateErr error

	// ──── Close ────────────────────────────────────────────────────────────

	// CloseErr is returned by [Host.Close] when non-nil.
	CloseErr error
}

// Calls returns a copy of all recorded method invocations.
func (h *Host) Calls() []Call {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Call, len(h.calls))
	copy(out, h.calls)
	return out
}

// CallCount returns how many times the named method was invoked.
func (h *Host) CallCount(method string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, c := range h.calls {
		if c.Method == method {
			n++
		}
	}
	return n
}

// Reset clears all recorded calls without altering response configuration.
func (h *Host) Reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = nil
}

// RegisterServer implements [mcp.Host].
func (h *Host) RegisterServer(_ context.Context, cfg mcp.ServerConfig) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, Call{Method: "RegisterServer", Args: []any{cfg}})
	return h.RegisterServerErr
}

// AvailableTools implements [mcp.Host].
func (h *Host) AvailableTools(tier types.BudgetTier) []types.ToolDefinition {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, Call{Method: "AvailableTools", Args: []any{tier}})
	if h.AvailableToolsResult == nil {
		return []types.ToolDefinition{}
	}
	out := make([]types.ToolDefinition, len(h.AvailableToolsResult))
	copy(out, h.AvailableToolsResult)
	return out
}

// ExecuteTool implements [mcp.Host].
func (h *Host) ExecuteTool(_ context.Context, name string, args string) (*mcp.ToolResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, Call{Method: "ExecuteTool", Args: []any{name, args}})
	if h.ExecuteToolErr != nil {
		return nil, h.ExecuteToolErr
	}
	if h.ExecuteToolResult == nil {
		return &mcp.ToolResult{}, nil
	}
	// Return a copy so the caller cannot mutate the configured result.
	cp := *h.ExecuteToolResult
	return &cp, nil
}

// Calibrate implements [mcp.Host].
func (h *Host) Calibrate(_ context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, Call{Method: "Calibrate", Args: nil})
	return h.CalibrateErr
}

// Close implements [mcp.Host].
func (h *Host) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, Call{Method: "Close", Args: nil})
	return h.CloseErr
}

// Ensure Host satisfies the interface at compile time.
var _ mcp.Host = (*Host)(nil)
