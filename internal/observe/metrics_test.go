package observe

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// newTestMetrics returns a Metrics instance backed by a ManualReader for
// programmatic metric inspection.
func newTestMetrics(t *testing.T) (*Metrics, *sdkmetric.ManualReader) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })

	m, err := NewMetrics(mp)
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}
	return m, reader
}

// collect gathers all metric data from the reader.
func collect(t *testing.T, reader *sdkmetric.ManualReader) metricdata.ResourceMetrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	return rm
}

// findMetric searches for a metric by name across all scope metrics.
func findMetric(rm metricdata.ResourceMetrics, name string) *metricdata.Metrics {
	for _, sm := range rm.ScopeMetrics {
		for i := range sm.Metrics {
			if sm.Metrics[i].Name == name {
				return &sm.Metrics[i]
			}
		}
	}
	return nil
}

func TestNewMetrics_CreatesWithoutError(t *testing.T) {
	m, _ := newTestMetrics(t)
	if m == nil {
		t.Fatal("NewMetrics returned nil")
	}
}

func TestHistogramObservation(t *testing.T) {
	m, reader := newTestMetrics(t)
	ctx := context.Background()

	histograms := []struct {
		name string
		h    metric.Float64Histogram
	}{
		{"glyphoxa.stt.duration", m.STTDuration},
		{"glyphoxa.llm.duration", m.LLMDuration},
		{"glyphoxa.tts.duration", m.TTSDuration},
		{"glyphoxa.s2s.duration", m.S2SDuration},
		{"glyphoxa.tool_execution.duration", m.ToolExecutionDuration},
	}

	for _, tc := range histograms {
		tc.h.Record(ctx, 0.123)
		tc.h.Record(ctx, 0.456)
	}

	rm := collect(t, reader)

	for _, tc := range histograms {
		t.Run(tc.name, func(t *testing.T) {
			met := findMetric(rm, tc.name)
			if met == nil {
				t.Fatalf("metric %q not found", tc.name)
			}
			hist, ok := met.Data.(metricdata.Histogram[float64])
			if !ok {
				t.Fatalf("metric %q is not a histogram", tc.name)
			}
			if len(hist.DataPoints) == 0 {
				t.Fatalf("metric %q has no data points", tc.name)
			}
			if got := hist.DataPoints[0].Count; got != 2 {
				t.Errorf("sample count = %d, want 2", got)
			}
		})
	}
}

func TestCounterIncrement(t *testing.T) {
	m, reader := newTestMetrics(t)
	ctx := context.Background()

	attrs := metric.WithAttributes(
		attribute.String("provider", "openai"),
		attribute.String("kind", "llm"),
		attribute.String("status", "ok"),
	)
	m.ProviderRequests.Add(ctx, 1, attrs)
	m.ProviderRequests.Add(ctx, 1, attrs)
	m.ProviderRequests.Add(ctx, 1, metric.WithAttributes(
		attribute.String("provider", "openai"),
		attribute.String("kind", "llm"),
		attribute.String("status", "error"),
	))

	rm := collect(t, reader)
	met := findMetric(rm, "glyphoxa.provider.requests")
	if met == nil {
		t.Fatal("metric not found")
	}
	sum, ok := met.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatal("metric is not a sum")
	}

	// Find the data point with status=ok.
	for _, dp := range sum.DataPoints {
		for _, kv := range dp.Attributes.ToSlice() {
			if string(kv.Key) == "status" && kv.Value.AsString() == "ok" {
				if dp.Value != 2 {
					t.Errorf("counter value = %d, want 2", dp.Value)
				}
				return
			}
		}
	}
	t.Error("data point with status=ok not found")
}

func TestToolCallsCounter(t *testing.T) {
	m, reader := newTestMetrics(t)
	ctx := context.Background()

	m.RecordToolCall(ctx, "dice_roll", "ok")
	m.RecordToolCall(ctx, "dice_roll", "error")

	rm := collect(t, reader)
	met := findMetric(rm, "glyphoxa.tool.calls")
	if met == nil {
		t.Fatal("metric not found")
	}
	sum, ok := met.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatal("metric is not a sum")
	}

	for _, dp := range sum.DataPoints {
		for _, kv := range dp.Attributes.ToSlice() {
			if string(kv.Key) == "status" && kv.Value.AsString() == "ok" {
				if dp.Value != 1 {
					t.Errorf("counter value = %d, want 1", dp.Value)
				}
				return
			}
		}
	}
	t.Error("data point with status=ok not found")
}

func TestNPCUtterancesCounter(t *testing.T) {
	m, reader := newTestMetrics(t)
	ctx := context.Background()

	m.RecordNPCUtterance(ctx, "bartender_01")
	m.RecordNPCUtterance(ctx, "bartender_01")
	m.RecordNPCUtterance(ctx, "guard_02")

	rm := collect(t, reader)
	met := findMetric(rm, "glyphoxa.npc.utterances")
	if met == nil {
		t.Fatal("metric not found")
	}
	sum, ok := met.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatal("metric is not a sum")
	}

	for _, dp := range sum.DataPoints {
		for _, kv := range dp.Attributes.ToSlice() {
			if string(kv.Key) == "npc_id" && kv.Value.AsString() == "bartender_01" {
				if dp.Value != 2 {
					t.Errorf("counter value = %d, want 2", dp.Value)
				}
				return
			}
		}
	}
	t.Error("data point with npc_id=bartender_01 not found")
}

func TestProviderErrorsCounter(t *testing.T) {
	m, reader := newTestMetrics(t)
	ctx := context.Background()

	m.RecordProviderError(ctx, "openai", "tts")

	rm := collect(t, reader)
	met := findMetric(rm, "glyphoxa.provider.errors")
	if met == nil {
		t.Fatal("metric not found")
	}
	sum, ok := met.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatal("metric is not a sum")
	}
	if len(sum.DataPoints) == 0 {
		t.Fatal("no data points")
	}
	if sum.DataPoints[0].Value != 1 {
		t.Errorf("counter value = %d, want 1", sum.DataPoints[0].Value)
	}
}

func TestGauges(t *testing.T) {
	m, reader := newTestMetrics(t)
	ctx := context.Background()

	// UpDownCounters are additive, so we simulate Set(5) as Add(5).
	m.ActiveNPCs.Add(ctx, 5)
	m.ActiveSessions.Add(ctx, 1)
	m.ActiveSessions.Add(ctx, 1)
	m.ActiveParticipants.Add(ctx, 3)

	rm := collect(t, reader)

	gauges := []struct {
		name string
		want int64
	}{
		{"glyphoxa.active_npcs", 5},
		{"glyphoxa.active_sessions", 2},
		{"glyphoxa.active_participants", 3},
	}

	for _, tc := range gauges {
		t.Run(tc.name, func(t *testing.T) {
			met := findMetric(rm, tc.name)
			if met == nil {
				t.Fatalf("metric %q not found", tc.name)
			}
			sum, ok := met.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("metric %q is not a sum", tc.name)
			}
			if len(sum.DataPoints) == 0 {
				t.Fatalf("metric %q has no data points", tc.name)
			}
			if got := sum.DataPoints[0].Value; got != tc.want {
				t.Errorf("gauge value = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestHTTPRequestDuration(t *testing.T) {
	m, reader := newTestMetrics(t)
	ctx := context.Background()

	m.HTTPRequestDuration.Record(ctx, 0.05,
		metric.WithAttributes(
			attribute.String("method", "GET"),
			attribute.String("path", "/healthz"),
		),
	)

	rm := collect(t, reader)
	met := findMetric(rm, "glyphoxa.http.request.duration")
	if met == nil {
		t.Fatal("metric not found")
	}
	hist, ok := met.Data.(metricdata.Histogram[float64])
	if !ok {
		t.Fatal("metric is not a histogram")
	}
	if len(hist.DataPoints) == 0 {
		t.Fatal("no data points")
	}
	if got := hist.DataPoints[0].Count; got != 1 {
		t.Errorf("sample count = %d, want 1", got)
	}
}

func TestDefaultMetrics_ReturnsSameInstance(t *testing.T) {
	// DefaultMetrics uses the global OTel provider so we just check
	// that repeated calls return the same pointer.
	a := DefaultMetrics()
	b := DefaultMetrics()
	if a != b {
		t.Error("DefaultMetrics returned different pointers")
	}
}
