// Package mcphost provides a concrete implementation of the [mcp.Host] interface.
//
// It connects to MCP servers via stdio or streamable-HTTP transports using the
// official MCP Go SDK (github.com/modelcontextprotocol/go-sdk), maintains a
// concurrent-safe in-memory tool registry, enforces latency-based budget tiers,
// and calibrates tool performance through measured rolling-window percentiles.
//
// Typical usage:
//
//	h := mcphost.New()
//
//	// Register an external MCP server.
//	err := h.RegisterServer(ctx, mcp.ServerConfig{
//	    Name:      "dice",
//	    Transport: mcp.TransportStdio,
//	    Command:   "/usr/local/bin/mcp-dice-server",
//	})
//
//	// Or register a built-in Go function.
//	h.RegisterBuiltin(mcphost.BuiltinTool{
//	    Definition:  llm.ToolDefinition{Name: "roll_d20", ...},
//	    Handler:     rollD20,
//	    DeclaredP50: 1,
//	})
//
//	// Calibrate latencies (optional but recommended).
//	h.Calibrate(ctx)
//
//	// Get tools visible under the current budget.
//	tools := h.AvailableTools(mcp.BudgetFast)
//
//	// Execute a tool.
//	result, err := h.ExecuteTool(ctx, "roll_d20", "{}")
//
//	// Shut down when done.
//	h.Close()
package mcphost

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/MrWong99/glyphoxa/internal/mcp"
	"github.com/MrWong99/glyphoxa/pkg/provider/llm"
)

// defaultWindowSize is the default capacity of each tool's rolling window.
const defaultWindowSize = 100

// toolEntry holds all metadata for a single registered tool.
type toolEntry struct {
	def           llm.ToolDefinition
	serverName    string
	declaredP50Ms int64
	declaredMaxMs int64
	measuredP50Ms int64
	measuredP99Ms int64
	callCount     int64
	errorCount    int64
	tier          mcp.BudgetTier
	degraded      bool // true if health-demoted to a higher tier
	measurements  *rollingWindow

	// builtinFn is non-nil for in-process tools registered via RegisterBuiltin.
	builtinFn func(ctx context.Context, args string) (string, error)
}

// serverConn holds a live connection to an external MCP server.
type serverConn struct {
	session *mcpsdk.ClientSession
}

// Host is a concrete implementation of [mcp.Host].
//
// It manages connections to one or more MCP servers (external via stdio /
// streamable-HTTP, or internal Go functions), enforces latency-based budget
// tiers, and tracks per-tool health via rolling latency windows.
//
// The zero value is NOT usable; create instances with [New].
type Host struct {
	mu      sync.RWMutex
	tools   map[string]toolEntry  // key: tool name
	servers map[string]serverConn // key: server name

	// client is reused across all server connections. The official SDK allows
	// a single Client to manage multiple sessions concurrently.
	client *mcpsdk.Client

	enforcer BudgetEnforcer
}

// Compile-time check: Host must implement mcp.Host.
var _ mcp.Host = (*Host)(nil)

// New creates and returns a ready-to-use Host.
func New() *Host {
	client := mcpsdk.NewClient(
		&mcpsdk.Implementation{Name: "glyphoxa-mcphost", Version: "1.0.0"},
		nil,
	)
	return &Host{
		tools:   make(map[string]toolEntry),
		servers: make(map[string]serverConn),
		client:  client,
	}
}

// RegisterServer connects to the MCP server described by cfg and imports its
// tool catalogue into the host. If a server with the same Name is already
// registered, the old connection is closed and replaced.
//
// For [mcp.TransportStdio] transport: cfg.Command is split on spaces into
// executable + args; cfg.Env is passed as additional environment variables.
//
// For [mcp.TransportStreamableHTTP] transport: cfg.URL is the endpoint address.
//
// Returns an error if the transport cannot be established or the initial tool
// listing fails.
func (h *Host) RegisterServer(ctx context.Context, cfg mcp.ServerConfig) error {
	if cfg.Name == "" {
		return fmt.Errorf("mcp host: server config must have a non-empty name")
	}
	if !cfg.Transport.IsValid() {
		return fmt.Errorf("mcp host: unknown transport %q for server %q", cfg.Transport, cfg.Name)
	}

	var transport mcpsdk.Transport

	switch cfg.Transport {
	case mcp.TransportStdio:
		executable, args := splitCommand(cfg.Command)
		if executable == "" {
			return fmt.Errorf("mcp host: stdio server %q requires a non-empty Command", cfg.Name)
		}
		cmd := exec.CommandContext(ctx, executable, args...)
		// Inject additional environment variables.
		for k, v := range cfg.Env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		transport = &mcpsdk.CommandTransport{Command: cmd}

	case mcp.TransportStreamableHTTP:
		if cfg.URL == "" {
			return fmt.Errorf("mcp host: streamable-http server %q requires a non-empty URL", cfg.Name)
		}
		transport = &mcpsdk.StreamableClientTransport{Endpoint: cfg.URL}
	}

	session, err := h.client.Connect(ctx, transport, nil)
	if err != nil {
		return fmt.Errorf("mcp host: failed to connect to server %q: %w", cfg.Name, err)
	}

	// Discover tools using the iterator.
	var discoveredTools []mcpsdk.Tool
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			_ = session.Close()
			return fmt.Errorf("mcp host: failed to list tools for server %q: %w", cfg.Name, err)
		}
		discoveredTools = append(discoveredTools, *tool)
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	// Close the old connection if it exists.
	if old, ok := h.servers[cfg.Name]; ok {
		_ = old.session.Close()
		// Remove tools that belonged to this server.
		for name, t := range h.tools {
			if t.serverName == cfg.Name {
				delete(h.tools, name)
			}
		}
	}

	h.servers[cfg.Name] = serverConn{session: session}

	// Register each discovered tool.
	for _, mcpTool := range discoveredTools {
		entry := buildToolEntry(mcpTool, cfg.Name)
		h.tools[mcpTool.Name] = entry
	}

	return nil
}

// buildToolEntry converts an official SDK Tool into an internal toolEntry.
func buildToolEntry(t mcpsdk.Tool, serverName string) toolEntry {
	p50, maxMs := extractLatencyHints(t)

	def := llm.ToolDefinition{
		Name:                t.Name,
		Description:         t.Description,
		Parameters:          schemaToMap(t.InputSchema),
		EstimatedDurationMs: int(p50),
		MaxDurationMs:       int(maxMs),
	}

	return toolEntry{
		def:           def,
		serverName:    serverName,
		declaredP50Ms: p50,
		declaredMaxMs: maxMs,
		tier:          tierFromDeclaredP50(p50),
		measurements:  newRollingWindow(defaultWindowSize),
	}
}

// extractLatencyHints tries to read estimated_duration_ms and max_duration_ms
// from a tool's metadata, InputSchema properties, or description.
func extractLatencyHints(t mcpsdk.Tool) (p50Ms, maxMs int64) {
	// Try InputSchema properties (if it's a map[string]any after JSON round-trip).
	if schema := schemaToMap(t.InputSchema); schema != nil {
		if props, ok := schema["properties"].(map[string]any); ok {
			if meta, ok := props["_metadata"].(map[string]any); ok {
				p50Ms = extractInt64(meta, "estimated_duration_ms")
				maxMs = extractInt64(meta, "max_duration_ms")
			}
		}
	}

	// Try description-embedded JSON.
	if p50Ms == 0 {
		p50Ms, maxMs = parseLatencyFromDescription(t.Description)
	}

	return p50Ms, maxMs
}

// extractInt64 retrieves an integer value from a map by key.
func extractInt64(m map[string]any, key string) int64 {
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case int64:
		return n
	case float64:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	}
	return 0
}

// parseLatencyFromDescription tries to unmarshal a JSON blob embedded in a
// tool description to extract latency hints.
func parseLatencyFromDescription(desc string) (int64, int64) {
	start := strings.Index(desc, "{")
	end := strings.LastIndex(desc, "}")
	if start < 0 || end < start {
		return 0, 0
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(desc[start:end+1]), &m); err != nil {
		return 0, 0
	}
	return extractInt64(m, "estimated_duration_ms"), extractInt64(m, "max_duration_ms")
}

// schemaToMap converts any schema value to a map[string]any.
func schemaToMap(schema any) map[string]any {
	if schema == nil {
		return map[string]any{"type": "object"}
	}
	if m, ok := schema.(map[string]any); ok {
		return m
	}
	data, err := json.Marshal(schema)
	if err != nil {
		return map[string]any{"type": "object"}
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return map[string]any{"type": "object"}
	}
	return m
}

// AvailableTools returns all tools whose assigned [mcp.BudgetTier] is ≤ tier,
// sorted by estimated latency ascending (fastest first).
//
// If [Host.Calibrate] has not been called, tools retain the tiers implied by
// their declared EstimatedDurationMs and MaxDurationMs values.
func (h *Host) AvailableTools(tier mcp.BudgetTier) []llm.ToolDefinition {
	h.mu.RLock()
	entries := make([]toolEntry, 0, len(h.tools))
	for _, e := range h.tools {
		entries = append(entries, e)
	}
	h.mu.RUnlock()

	return h.enforcer.FilterTools(entries, tier)
}

// ExecuteTool calls the named tool with JSON-encoded args and returns the
// result. name must exactly match a [llm.ToolDefinition.Name] returned by
// [Host.AvailableTools].
//
// args must be a valid JSON object string. An empty object ("{}") is valid for
// parameter-less tools.
//
// A non-nil *ToolResult is returned on success even when [mcp.ToolResult.IsError]
// is true (application-level error). A Go error is returned only on transport
// or protocol failure.
func (h *Host) ExecuteTool(ctx context.Context, name string, args string) (*mcp.ToolResult, error) {
	h.mu.RLock()
	entry, ok := h.tools[name]
	h.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("mcp host: tool %q not found", name)
	}

	start := time.Now()

	var result *mcp.ToolResult
	var execErr error

	if entry.builtinFn != nil {
		result, execErr = h.executeBuiltin(ctx, entry, args)
	} else {
		result, execErr = h.executeMCPTool(ctx, entry, args)
	}

	durationMs := time.Since(start).Milliseconds()
	isError := execErr != nil || (result != nil && result.IsError)

	// Record the measurement and update tier.
	h.recordAndUpdateTier(name, durationMs, isError)

	if execErr != nil {
		return nil, execErr
	}
	result.DurationMs = durationMs
	return result, nil
}

// executeBuiltin calls the in-process handler for a builtin tool.
func (h *Host) executeBuiltin(ctx context.Context, entry toolEntry, args string) (*mcp.ToolResult, error) {
	output, err := entry.builtinFn(ctx, args)
	if err != nil {
		return &mcp.ToolResult{Content: err.Error(), IsError: true}, nil
	}
	return &mcp.ToolResult{Content: output}, nil
}

// executeMCPTool routes the call to the appropriate server session.
func (h *Host) executeMCPTool(ctx context.Context, entry toolEntry, args string) (*mcp.ToolResult, error) {
	h.mu.RLock()
	conn, ok := h.servers[entry.serverName]
	h.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("mcp host: server %q not found for tool %q", entry.serverName, entry.def.Name)
	}

	// Decode args into a map for the SDK.
	var argsMap map[string]any
	if args != "" && args != "{}" {
		if err := json.Unmarshal([]byte(args), &argsMap); err != nil {
			return nil, fmt.Errorf("mcp host: invalid args JSON for tool %q: %w", entry.def.Name, err)
		}
	}

	callResult, err := conn.session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      entry.def.Name,
		Arguments: argsMap,
	})
	if err != nil {
		return nil, fmt.Errorf("mcp host: call to tool %q failed: %w", entry.def.Name, err)
	}

	// Concatenate all text content from the result.
	var sb strings.Builder
	for _, c := range callResult.Content {
		if tc, ok := c.(*mcpsdk.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}

	return &mcp.ToolResult{
		Content: sb.String(),
		IsError: callResult.IsError,
	}, nil
}

// recordAndUpdateTier records a measurement and re-evaluates the tool's tier.
func (h *Host) recordAndUpdateTier(name string, durationMs int64, isError bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	entry, ok := h.tools[name]
	if !ok {
		return
	}

	entry.measurements.Record(durationMs, isError)
	entry.callCount++
	if isError {
		entry.errorCount++
	}

	p50 := entry.measurements.P50()
	p99 := entry.measurements.P99()
	entry.measuredP50Ms = p50
	entry.measuredP99Ms = p99

	// Assign tier from measured P50.
	newTier := tierFromMeasuredP50(p50)

	// Health demotion: if error rate exceeds 30%, bump tier by one.
	errRate := entry.measurements.ErrorRate()
	entry.degraded = errRate > 0.3
	if entry.degraded && newTier < mcp.BudgetDeep {
		newTier++
	}

	entry.tier = newTier
	h.tools[name] = entry
}

// tierFromMeasuredP50 maps a measured P50 latency to a BudgetTier.
func tierFromMeasuredP50(p50Ms int64) mcp.BudgetTier {
	switch {
	case p50Ms <= 500:
		return mcp.BudgetFast
	case p50Ms <= 1500:
		return mcp.BudgetStandard
	default:
		return mcp.BudgetDeep
	}
}

// Close shuts down all server connections and releases associated resources.
// After Close returns the Host must not be used again.
func (h *Host) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	var firstErr error
	for name, conn := range h.servers {
		if err := conn.session.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("mcp host: error closing server %q: %w", name, err)
		}
		delete(h.servers, name)
	}

	// Clear the tool registry.
	h.tools = make(map[string]toolEntry)

	return firstErr
}

// splitCommand splits a command string into executable and arguments.
// e.g. "/bin/foo --bar baz" → ("/bin/foo", ["--bar", "baz"]).
func splitCommand(command string) (executable string, args []string) {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return "", nil
	}
	return parts[0], parts[1:]
}
