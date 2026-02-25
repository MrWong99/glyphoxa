// Package mcp defines the interface for a Model Context Protocol (MCP) host.
//
// The MCP host manages connections to one or more MCP servers, maintains a
// catalogue of available tools (keyed by [types.BudgetTier]), executes tool
// calls on behalf of NPC agents, and calibrates tool latency so that tool
// definitions carry accurate tier assignments.
//
// Lifecycle:
//
//  1. Call [Host.RegisterServer] for each MCP server to connect to.
//  2. Optionally call [Host.Calibrate] to measure real tool latencies and
//     assign tiers based on observed performance.
//  3. Use [Host.AvailableTools] to enumerate tools valid for a budget tier.
//  4. Use [Host.ExecuteTool] to run tools on behalf of NPC agents.
//  5. Call [Host.Close] to release all connections and background goroutines.
//
// All methods must be safe for concurrent use.
package mcp

import (
	"context"

	"github.com/MrWong99/glyphoxa/pkg/types"
)

// ServerConfig describes how to connect to a single MCP server.
type ServerConfig struct {
	// Name is the human-readable identifier for this server.
	// Must be unique within a single [Host]. Used in log messages and errors.
	Name string

	// Transport specifies the connection mechanism.
	// Supported values:
	//   "stdio" — spawn a subprocess and communicate over stdin/stdout.
	//   "http"  — communicate via HTTP JSON-RPC (stateless).
	//   "sse"   — communicate via HTTP Server-Sent Events (streaming).
	Transport string

	// Command is the executable path (and optional arguments) used when
	// Transport is "stdio".
	// Example: "/usr/local/bin/mcp-server --config /etc/mcp.json"
	// Ignored for http and sse transports.
	Command string

	// URL is the endpoint address used when Transport is "http" or "sse".
	// Example: "https://tools.example.com/mcp"
	// Ignored for stdio transport.
	URL string

	// Env holds additional environment variables injected into the server
	// process when Transport is "stdio". May be nil.
	Env map[string]string
}

// ToolResult holds the outcome of a single tool execution.
type ToolResult struct {
	// Content is the tool's textual output, typically a JSON string or
	// human-readable text ready for insertion into an LLM context window.
	Content string

	// IsError indicates that the tool returned an application-level error
	// (as opposed to a transport or protocol failure returned via the Go error
	// return value). When IsError is true, Content contains the error message.
	IsError bool

	// DurationMs is the wall-clock time in milliseconds from when the request
	// was dispatched until the full response was received.
	DurationMs int64
}

// ToolHealth captures the measured runtime performance of a single MCP tool,
// populated by [Host.Calibrate] and used to assign [types.BudgetTier] values.
type ToolHealth struct {
	// Name is the tool's unique identifier, matching [types.ToolDefinition.Name].
	Name string

	// MeasuredP50Ms is the observed median (50th-percentile) execution latency
	// in milliseconds, as recorded during the most recent [Host.Calibrate] run.
	MeasuredP50Ms int64

	// MeasuredP99Ms is the observed 99th-percentile execution latency in
	// milliseconds, as recorded during the most recent [Host.Calibrate] run.
	MeasuredP99Ms int64

	// CallCount is the total number of times this tool has been invoked since
	// the [Host] was created (or since the last reset, implementation-defined).
	CallCount int

	// ErrorRate is the fraction of calls that resulted in an error (0.0–1.0).
	ErrorRate float64

	// Tier is the [types.BudgetTier] assigned to this tool based on its
	// measured latency. Assignment follows [types.BudgetTier.MaxLatencyMs]:
	//   BudgetFast     — MeasuredP50Ms ≤ 500
	//   BudgetStandard — MeasuredP50Ms ≤ 1500
	//   BudgetDeep     — all remaining tools
	Tier types.BudgetTier
}

// Host manages connections to MCP servers, routes tool calls, and tracks
// per-tool performance metrics for latency-based budget tier assignment.
//
// Implementations must be safe for concurrent use.
type Host interface {
	// RegisterServer connects to the MCP server described by cfg and imports
	// its tool catalogue into the host. If a server with the same Name is
	// already registered it is reconnected / refreshed rather than duplicated.
	//
	// Returns an error if the transport cannot be established or the initial
	// tool listing request fails.
	RegisterServer(ctx context.Context, cfg ServerConfig) error

	// AvailableTools returns all tools whose assigned [types.BudgetTier] is ≤
	// tier, sorted by EstimatedDurationMs ascending (fastest first).
	//
	// If [Host.Calibrate] has not been called, tools retain the tiers implied
	// by their declared EstimatedDurationMs and MaxDurationMs values.
	AvailableTools(tier types.BudgetTier) []types.ToolDefinition

	// ExecuteTool calls the named tool with JSON-encoded args and returns the
	// result. name must exactly match a [types.ToolDefinition.Name] returned
	// by [Host.AvailableTools].
	//
	// args must be a valid JSON object string conforming to the tool's
	// Parameters schema. An empty object ("{}") is valid for parameter-less tools.
	//
	// A non-nil *ToolResult is returned on success even when [ToolResult.IsError]
	// is true (application-level error). A Go error is returned only on
	// transport or protocol failure.
	ExecuteTool(ctx context.Context, name string, args string) (*ToolResult, error)

	// Calibrate sends lightweight probe requests to every registered tool,
	// measures their round-trip latency, and updates each tool's assigned
	// [types.BudgetTier]. Probes must run concurrently and respect ctx for
	// cancellation and deadline propagation.
	Calibrate(ctx context.Context) error

	// Close shuts down all server connections and releases associated resources.
	// After Close returns the Host must not be used again.
	Close() error
}
