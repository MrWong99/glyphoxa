package mcphost

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MrWong99/glyphoxa/internal/mcp"
	"github.com/MrWong99/glyphoxa/pkg/provider/llm"
)

// ──────────────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────────────

// echoTool returns a BuiltinTool that echoes its args back as the result.
func echoTool(name string, p50Ms int64) BuiltinTool {
	return BuiltinTool{
		Definition: llm.ToolDefinition{
			Name:                name,
			Description:         "echoes args",
			EstimatedDurationMs: int(p50Ms),
		},
		Handler: func(_ context.Context, args string) (string, error) {
			return args, nil
		},
		DeclaredP50: p50Ms,
	}
}

// failTool returns a BuiltinTool that always returns an error.
func failTool(name string, p50Ms int64) BuiltinTool {
	return BuiltinTool{
		Definition: llm.ToolDefinition{Name: name, EstimatedDurationMs: int(p50Ms)},
		Handler: func(_ context.Context, _ string) (string, error) {
			return "", fmt.Errorf("always fails")
		},
		DeclaredP50: p50Ms,
	}
}

// slowTool returns a BuiltinTool that sleeps for delay before responding.
func slowTool(name string, delay time.Duration, p50Ms int64) BuiltinTool {
	return BuiltinTool{
		Definition: llm.ToolDefinition{Name: name, EstimatedDurationMs: int(p50Ms)},
		Handler: func(ctx context.Context, args string) (string, error) {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(delay):
				return "ok", nil
			}
		},
		DeclaredP50: p50Ms,
	}
}

// toolNamed returns the first ToolDefinition with the given name, or nil.
func toolNamed(tools []llm.ToolDefinition, name string) *llm.ToolDefinition {
	for i := range tools {
		if tools[i].Name == name {
			return &tools[i]
		}
	}
	return nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Tests
// ──────────────────────────────────────────────────────────────────────────────

// TestRegisterBuiltin verifies that a registered built-in tool appears in
// AvailableTools at the correct tier.
func TestRegisterBuiltin(t *testing.T) {
	t.Parallel()
	h := New()
	defer h.Close()

	tool := echoTool("greet", 100) // 100ms → FAST
	if err := h.RegisterBuiltin(tool); err != nil {
		t.Fatalf("RegisterBuiltin: %v", err)
	}

	got := h.AvailableTools(mcp.BudgetDeep)
	if toolNamed(got, "greet") == nil {
		t.Errorf("tool %q not found in AvailableTools", "greet")
	}
}

// TestRegisterBuiltinEmptyName verifies that an empty name is rejected.
func TestRegisterBuiltinEmptyName(t *testing.T) {
	t.Parallel()
	h := New()
	defer h.Close()

	err := h.RegisterBuiltin(BuiltinTool{
		Handler: func(_ context.Context, _ string) (string, error) { return "", nil },
	})
	if err == nil {
		t.Error("expected error for empty name, got nil")
	}
}

// TestRegisterBuiltinNilHandler verifies that a nil handler is rejected.
func TestRegisterBuiltinNilHandler(t *testing.T) {
	t.Parallel()
	h := New()
	defer h.Close()

	err := h.RegisterBuiltin(BuiltinTool{
		Definition: llm.ToolDefinition{Name: "no-handler"},
	})
	if err == nil {
		t.Error("expected error for nil handler, got nil")
	}
}

// TestBudgetFiltering verifies that AvailableTools filters by tier correctly.
func TestBudgetFiltering(t *testing.T) {
	t.Parallel()
	h := New()
	defer h.Close()

	// fast: p50=100  → FAST
	// std:  p50=800  → STANDARD
	// deep: p50=2000 → DEEP
	must(t, h.RegisterBuiltin(echoTool("fast", 100)))
	must(t, h.RegisterBuiltin(echoTool("std", 800)))
	must(t, h.RegisterBuiltin(echoTool("deep", 2000)))

	// BudgetFast: only FAST tools.
	fastTools := h.AvailableTools(mcp.BudgetFast)
	assertContains(t, fastTools, "fast")
	assertNotContains(t, fastTools, "std")
	assertNotContains(t, fastTools, "deep")

	// BudgetStandard: FAST + STANDARD.
	stdTools := h.AvailableTools(mcp.BudgetStandard)
	assertContains(t, stdTools, "fast")
	assertContains(t, stdTools, "std")
	assertNotContains(t, stdTools, "deep")

	// BudgetDeep: all tools.
	deepTools := h.AvailableTools(mcp.BudgetDeep)
	assertContains(t, deepTools, "fast")
	assertContains(t, deepTools, "std")
	assertContains(t, deepTools, "deep")
}

// TestExecuteBuiltin verifies that ExecuteTool calls the handler and returns
// the result.
func TestExecuteBuiltin(t *testing.T) {
	t.Parallel()
	h := New()
	defer h.Close()

	must(t, h.RegisterBuiltin(echoTool("echo", 50)))

	result, err := h.ExecuteTool(context.Background(), "echo", `{"msg":"hello"}`)
	if err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Content != `{"msg":"hello"}` {
		t.Errorf("Content = %q, want %q", result.Content, `{"msg":"hello"}`)
	}
	if result.IsError {
		t.Error("IsError = true, want false")
	}
}

// TestExecuteToolNotFound verifies that calling an unknown tool returns an error.
func TestExecuteToolNotFound(t *testing.T) {
	t.Parallel()
	h := New()
	defer h.Close()

	_, err := h.ExecuteTool(context.Background(), "nonexistent", "{}")
	if err == nil {
		t.Error("expected error for unknown tool, got nil")
	}
}

// TestExecuteBuiltinError verifies that a handler error results in IsError=true.
func TestExecuteBuiltinError(t *testing.T) {
	t.Parallel()
	h := New()
	defer h.Close()

	must(t, h.RegisterBuiltin(failTool("boom", 50)))

	result, err := h.ExecuteTool(context.Background(), "boom", "{}")
	if err != nil {
		t.Fatalf("ExecuteTool returned unexpected transport error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("IsError = false, want true")
	}
}

// TestRollingWindow is a quick integration test exercising the rolling window
// through the host metrics path.
func TestRollingWindow(t *testing.T) {
	t.Parallel()
	w := newRollingWindow(10)

	// No measurements yet.
	if w.P50() != 0 || w.P99() != 0 || w.ErrorRate() != 0 {
		t.Error("empty window should return zeros")
	}

	w.Record(100, false)
	w.Record(200, false)
	w.Record(300, true)

	if c := w.Count(); c != 3 {
		t.Errorf("Count = %d, want 3", c)
	}
	if got := w.P50(); got == 0 {
		t.Error("P50 should be non-zero after recording")
	}
	if got := w.ErrorRate(); got == 0 {
		t.Error("ErrorRate should be non-zero after recording an error")
	}
}

// TestCalibrationBuiltin verifies that Calibrate calls each builtin and
// records measurements.
func TestCalibrationBuiltin(t *testing.T) {
	t.Parallel()
	h := New()
	defer h.Close()

	must(t, h.RegisterBuiltin(echoTool("ping", 100)))
	must(t, h.RegisterBuiltin(echoTool("pong", 200)))

	if err := h.Calibrate(context.Background()); err != nil {
		t.Fatalf("Calibrate: %v", err)
	}

	// After calibration the measurements count should be ≥ 1 for each tool.
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, name := range []string{"ping", "pong"} {
		entry, ok := h.tools[name]
		if !ok {
			t.Errorf("tool %q missing after calibration", name)
			continue
		}
		if c := entry.measurements.Count(); c == 0 {
			t.Errorf("tool %q has no measurements after calibration", name)
		}
	}
}

// TestCalibrationContextCancel verifies that Calibrate respects context cancellation.
func TestCalibrationContextCancel(t *testing.T) {
	t.Parallel()
	h := New()
	defer h.Close()

	// Register a slow tool.
	must(t, h.RegisterBuiltin(slowTool("slow", 500*time.Millisecond, 500)))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	// Calibrate may return ctx.Err() or nil (if the goroutine finishes before
	// the cancel propagates). We just ensure it doesn't hang.
	done := make(chan error, 1)
	go func() { done <- h.Calibrate(ctx) }()

	select {
	case <-done:
		// OK — either completed or was cancelled.
	case <-time.After(2 * time.Second):
		t.Fatal("Calibrate did not respect context cancellation within 2s")
	}
}

// TestHealthDemotion verifies that a tool that fails frequently is demoted
// to a higher tier.
func TestHealthDemotion(t *testing.T) {
	t.Parallel()
	h := New()
	defer h.Close()

	var callN atomic.Int64
	flaky := BuiltinTool{
		Definition:  llm.ToolDefinition{Name: "flaky", EstimatedDurationMs: 100},
		DeclaredP50: 100, // would normally be FAST
		Handler: func(_ context.Context, _ string) (string, error) {
			n := callN.Add(1)
			if n%2 == 0 {
				return "", fmt.Errorf("fail")
			}
			return "ok", nil
		},
	}
	must(t, h.RegisterBuiltin(flaky))

	// Execute enough times to push error rate above 30 %.
	ctx := context.Background()
	for range 20 {
		h.ExecuteTool(ctx, "flaky", "{}") //nolint:errcheck
	}

	h.mu.RLock()
	entry := h.tools["flaky"]
	h.mu.RUnlock()

	if !entry.degraded {
		t.Error("tool should be marked degraded after 50% error rate")
	}
	// Declared tier was FAST; after demotion it should be at least STANDARD.
	if entry.tier <= mcp.BudgetFast {
		t.Errorf("tier after demotion = %s, want > FAST", entry.tier)
	}
}

// TestAvailableToolsSorting verifies that tools are sorted by latency ascending.
func TestAvailableToolsSorting(t *testing.T) {
	t.Parallel()
	h := New()
	defer h.Close()

	// Register in reverse latency order.
	must(t, h.RegisterBuiltin(echoTool("slow", 400))) // 400ms
	must(t, h.RegisterBuiltin(echoTool("fast", 50)))  // 50ms
	must(t, h.RegisterBuiltin(echoTool("mid", 200)))  // 200ms

	tools := h.AvailableTools(mcp.BudgetDeep)
	if len(tools) < 3 {
		t.Fatalf("expected at least 3 tools, got %d", len(tools))
	}

	// All three are in the FAST tier (≤ 500ms), so they should be sorted.
	latencies := make([]int, len(tools))
	for i, td := range tools {
		latencies[i] = td.EstimatedDurationMs
	}
	for i := 1; i < len(latencies); i++ {
		if latencies[i] < latencies[i-1] {
			t.Errorf("tools not sorted: latencies[%d]=%d < latencies[%d]=%d",
				i, latencies[i], i-1, latencies[i-1])
		}
	}
}

// TestClose verifies that Close empties the tool and server registries.
func TestClose(t *testing.T) {
	t.Parallel()
	h := New()

	must(t, h.RegisterBuiltin(echoTool("x", 100)))

	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	h.mu.RLock()
	toolCount := len(h.tools)
	serverCount := len(h.servers)
	h.mu.RUnlock()

	if toolCount != 0 {
		t.Errorf("tools after Close: %d, want 0", toolCount)
	}
	if serverCount != 0 {
		t.Errorf("servers after Close: %d, want 0", serverCount)
	}
}

// TestConcurrentRegisterAndAvailable verifies no data races under concurrent
// registration and tool listing.
func TestConcurrentRegisterAndAvailable(t *testing.T) {
	t.Parallel()
	h := New()
	defer h.Close()

	done := make(chan struct{})
	go func() {
		for i := range 50 {
			name := fmt.Sprintf("tool-%d", i)
			_ = h.RegisterBuiltin(echoTool(name, 100))
		}
		close(done)
	}()

	for range 50 {
		h.AvailableTools(mcp.BudgetDeep)
	}
	<-done
}

// ──────────────────────────────────────────────────────────────────────────────
// Assertion helpers
// ──────────────────────────────────────────────────────────────────────────────

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertContains(t *testing.T, tools []llm.ToolDefinition, name string) {
	t.Helper()
	if toolNamed(tools, name) == nil {
		t.Errorf("expected tool %q to be present, but it was not", name)
	}
}

func assertNotContains(t *testing.T, tools []llm.ToolDefinition, name string) {
	t.Helper()
	if toolNamed(tools, name) != nil {
		t.Errorf("expected tool %q to be absent, but it was present", name)
	}
}
