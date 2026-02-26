package mcphost

import (
	"context"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/MrWong99/glyphoxa/internal/mcp"
)

// Calibrate sends lightweight probe requests to every registered tool, measures
// their round-trip latency, and updates each tool's assigned [mcp.BudgetTier].
//
// Probes run concurrently using an [errgroup] and respect ctx for cancellation
// and deadline propagation. If ctx is cancelled, outstanding probes are
// abandoned and Calibrate returns the context error.
//
// The probe is a minimal call with an empty JSON object ("{}") as arguments. For
// tools that require specific parameters this will typically return an error —
// that is intentional; the latency and error-rate data still improve tier
// assignments.
//
// After calibration completes, each tool's tier is reassigned:
//
//	MeasuredP50 ≤  500ms → [mcp.BudgetFast]
//	MeasuredP50 ≤ 1500ms → [mcp.BudgetStandard]
//	otherwise            → [mcp.BudgetDeep]
//
// If a tool's error rate within the calibration window exceeds 30 %, it is
// marked degraded and its tier is bumped up by one level.
func (h *Host) Calibrate(ctx context.Context) error {
	// Snapshot tool names under a read lock to avoid holding the lock
	// during potentially slow network calls.
	h.mu.RLock()
	names := make([]string, 0, len(h.tools))
	for name := range h.tools {
		names = append(names, name)
	}
	h.mu.RUnlock()

	g, gctx := errgroup.WithContext(ctx)

	for _, name := range names {
		g.Go(func() error {
			if err := gctx.Err(); err != nil {
				return err
			}
			h.probeOne(gctx, name)
			return nil
		})
	}

	// Wait for all probes. We intentionally ignore per-tool errors (they are
	// recorded in the rolling window) and only propagate context cancellation.
	return g.Wait()
}

// probeOne sends a single probe to the named tool and records the result.
func (h *Host) probeOne(ctx context.Context, name string) {
	h.mu.RLock()
	entry, ok := h.tools[name]
	h.mu.RUnlock()
	if !ok {
		return
	}

	start := time.Now()
	var isError bool

	if entry.builtinFn != nil {
		// In-process call with empty args.
		_, err := entry.builtinFn(ctx, "{}")
		isError = err != nil
	} else {
		// External tool: use ExecuteTool which records measurements too, but
		// here we call the low-level helper to avoid double-recording.
		result, err := h.executeMCPTool(ctx, entry, "{}")
		isError = err != nil || (result != nil && result.IsError)
	}

	durationMs := time.Since(start).Milliseconds()

	// Write measurements and reassign tier.
	h.mu.Lock()
	defer h.mu.Unlock()

	entry, ok = h.tools[name]
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

	newTier := tierFromMeasuredP50(p50)

	errRate := entry.measurements.ErrorRate()
	entry.degraded = errRate > 0.3
	if entry.degraded && newTier < mcp.BudgetDeep {
		newTier++
	}

	entry.tier = newTier
	h.tools[name] = entry
}
