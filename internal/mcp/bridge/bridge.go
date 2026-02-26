// Package bridge wires MCP tools into an S2S voice session.
//
// A [Bridge] translates between the MCP Host's tool catalogue and the
// S2S session's native function-calling interface. On creation it declares
// the budget-appropriate tool set on the session and registers a
// [s2s.ToolCallHandler] that routes all tool calls back through the MCP Host
// for execution.
//
// Typical usage:
//
//	b, err := bridge.NewBridge(host, session, mcp.BudgetFast)
//	if err != nil { ... }
//	defer b.Close()
//
//	// mid-conversation, DM requests a deep search
//	if err := b.UpdateTier(ctx, mcp.BudgetDeep); err != nil { ... }
package bridge

import (
	"context"
	"fmt"
	"time"

	"github.com/MrWong99/glyphoxa/internal/mcp"
	"github.com/MrWong99/glyphoxa/pkg/provider/s2s"
)

// defaultToolTimeout is the context deadline applied to each tool execution
// when no external context is available (i.e. inside the ToolCallHandler,
// which receives no context from the S2S session).
const defaultToolTimeout = 30 * time.Second

// Option is a functional option for configuring a [Bridge].
type Option func(*Bridge)

// WithToolTimeout sets the deadline applied to each individual tool execution
// within the [s2s.ToolCallHandler]. If a tool call exceeds this duration the
// context is cancelled and an error is returned to the S2S session.
//
// The default is 30 seconds.
func WithToolTimeout(d time.Duration) Option {
	return func(b *Bridge) {
		b.toolTimeout = d
	}
}

// Bridge wires MCP tools into an S2S session. It declares budget-appropriate
// tool definitions on the session and routes tool calls back through the
// MCP Host for execution.
//
// The bridge is tied to a single S2S session and should be created when
// the session starts and discarded when it ends. Bridge is safe for concurrent
// use.
type Bridge struct {
	host        mcp.Host
	session     s2s.SessionHandle
	tier        mcp.BudgetTier
	toolTimeout time.Duration
}

// NewBridge creates a Bridge that declares tools from host filtered by tier
// on the given S2S session. It immediately calls session.SetTools with the
// appropriate definitions and registers a ToolCallHandler via session.OnToolCall.
//
// The ToolCallHandler routes all tool calls to host.ExecuteTool. Tool
// executions are bounded by a 30-second context timeout (configurable via
// [WithToolTimeout]).
//
// Returns an error if either host or session is nil, or if the initial
// session.SetTools call fails.
func NewBridge(host mcp.Host, session s2s.SessionHandle, tier mcp.BudgetTier, opts ...Option) (*Bridge, error) {
	if host == nil {
		return nil, fmt.Errorf("bridge: host must not be nil")
	}
	if session == nil {
		return nil, fmt.Errorf("bridge: session must not be nil")
	}

	b := &Bridge{
		host:        host,
		session:     session,
		tier:        tier,
		toolTimeout: defaultToolTimeout,
	}
	for _, opt := range opts {
		opt(b)
	}

	tools := host.AvailableTools(tier)
	if err := session.SetTools(tools); err != nil {
		return nil, fmt.Errorf("bridge: failed to set initial tools for tier %s: %w", tier, err)
	}

	session.OnToolCall(b.handleToolCall)
	return b, nil
}

// handleToolCall is the [s2s.ToolCallHandler] registered on the session.
// It executes the named MCP tool with the given JSON-encoded args and returns
// the tool's content string. A 30-second (configurable) context timeout is
// applied because OnToolCall does not propagate a caller context.
func (b *Bridge) handleToolCall(name string, args string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), b.toolTimeout)
	defer cancel()

	result, err := b.host.ExecuteTool(ctx, name, args)
	if err != nil {
		return "", fmt.Errorf("bridge: tool %q execution failed: %w", name, err)
	}
	return result.Content, nil
}

// UpdateTier changes the active budget tier, retrieves the newly appropriate
// tool set from the MCP Host, and updates the session via SetTools.
//
// ctx is respected for cancellation — if ctx is done before SetTools is
// called, UpdateTier returns without modifying the session.
//
// Returns an error if ctx is already cancelled or if SetTools fails.
func (b *Bridge) UpdateTier(ctx context.Context, newTier mcp.BudgetTier) error {
	tools := b.host.AvailableTools(newTier)

	// Respect cancellation before mutating the session.
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("bridge: context cancelled before updating tools: %w", err)
	}

	if err := b.session.SetTools(tools); err != nil {
		return fmt.Errorf("bridge: failed to update tools for tier %s: %w", newTier, err)
	}
	b.tier = newTier
	return nil
}

// Close deregisters the ToolCallHandler from the session. After Close, any
// tool call requests from the S2S model will not be handled. Close does not
// close the underlying session or MCP Host — callers are responsible for
// their own lifecycle management.
func (b *Bridge) Close() {
	b.session.OnToolCall(nil)
}
