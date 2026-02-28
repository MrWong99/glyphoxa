// Package observe provides application-wide observability primitives for
// Glyphoxa: OpenTelemetry metrics, distributed tracing, structured logging,
// and HTTP middleware that ties them together.
//
// Metrics are recorded through the OpenTelemetry Metrics API. A Prometheus
// exporter bridge is available via [InitProvider] so that metrics can still be
// scraped via the standard /metrics endpoint. A package-level default
// [Metrics] instance ([DefaultMetrics]) is provided for convenience; tests
// should use [NewMetrics] with a custom [metric.MeterProvider] to avoid
// cross-test pollution.
package observe

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// meterName is the instrumentation scope name used for all Glyphoxa metrics.
const meterName = "github.com/MrWong99/glyphoxa"

// Metrics holds all OpenTelemetry metric instruments for the application.
// All fields are safe for concurrent use — the underlying OTel types handle
// their own synchronisation.
type Metrics struct {
	// --- Latency histograms per pipeline stage ---

	// STTDuration tracks speech-to-text transcription latency.
	STTDuration metric.Float64Histogram

	// LLMDuration tracks LLM inference latency.
	LLMDuration metric.Float64Histogram

	// TTSDuration tracks text-to-speech synthesis latency.
	TTSDuration metric.Float64Histogram

	// S2SDuration tracks end-to-end speech-to-speech latency.
	S2SDuration metric.Float64Histogram

	// ToolExecutionDuration tracks MCP tool execution latency.
	ToolExecutionDuration metric.Float64Histogram

	// --- Counters ---

	// ProviderRequests counts provider API calls. Use with attributes:
	//   attribute.String("provider", ...), attribute.String("kind", ...), attribute.String("status", ...)
	ProviderRequests metric.Int64Counter

	// ToolCalls counts tool invocations. Use with attributes:
	//   attribute.String("tool", ...), attribute.String("status", ...)
	ToolCalls metric.Int64Counter

	// NPCUtterances counts NPC responses. Use with attribute:
	//   attribute.String("npc_id", ...)
	NPCUtterances metric.Int64Counter

	// --- Error counters ---

	// ProviderErrors counts provider errors. Use with attributes:
	//   attribute.String("provider", ...), attribute.String("kind", ...)
	ProviderErrors metric.Int64Counter

	// --- Gauges ---

	// ActiveNPCs tracks the number of currently active NPC agents.
	ActiveNPCs metric.Int64UpDownCounter

	// ActiveSessions tracks the number of live voice sessions.
	ActiveSessions metric.Int64UpDownCounter

	// ActiveParticipants tracks the number of connected participants across
	// all sessions.
	ActiveParticipants metric.Int64UpDownCounter

	// --- HTTP middleware ---

	// HTTPRequestDuration tracks HTTP request processing time. Use with attributes:
	//   attribute.String("method", ...), attribute.String("path", ...)
	HTTPRequestDuration metric.Float64Histogram
}

// latencyBuckets defines histogram bucket boundaries (in seconds) optimised
// for voice-pipeline latencies.
var latencyBuckets = []float64{
	0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10,
}

// NewMetrics creates a fully initialised [Metrics] struct using the given
// [metric.MeterProvider]. Returns an error if any instrument creation fails.
func NewMetrics(mp metric.MeterProvider) (*Metrics, error) {
	m := mp.Meter(meterName)
	var err error
	met := &Metrics{}

	// Histograms.
	if met.STTDuration, err = m.Float64Histogram("glyphoxa.stt.duration",
		metric.WithDescription("Latency of speech-to-text transcription."),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(latencyBuckets...),
	); err != nil {
		return nil, err
	}
	if met.LLMDuration, err = m.Float64Histogram("glyphoxa.llm.duration",
		metric.WithDescription("Latency of LLM inference."),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(latencyBuckets...),
	); err != nil {
		return nil, err
	}
	if met.TTSDuration, err = m.Float64Histogram("glyphoxa.tts.duration",
		metric.WithDescription("Latency of text-to-speech synthesis."),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(latencyBuckets...),
	); err != nil {
		return nil, err
	}
	if met.S2SDuration, err = m.Float64Histogram("glyphoxa.s2s.duration",
		metric.WithDescription("End-to-end speech-to-speech latency."),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(latencyBuckets...),
	); err != nil {
		return nil, err
	}
	if met.ToolExecutionDuration, err = m.Float64Histogram("glyphoxa.tool_execution.duration",
		metric.WithDescription("Latency of MCP tool execution."),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(latencyBuckets...),
	); err != nil {
		return nil, err
	}

	// Counters.
	if met.ProviderRequests, err = m.Int64Counter("glyphoxa.provider.requests",
		metric.WithDescription("Total provider API requests by provider, kind, and status."),
	); err != nil {
		return nil, err
	}
	if met.ToolCalls, err = m.Int64Counter("glyphoxa.tool.calls",
		metric.WithDescription("Total tool invocations by tool name and status."),
	); err != nil {
		return nil, err
	}
	if met.NPCUtterances, err = m.Int64Counter("glyphoxa.npc.utterances",
		metric.WithDescription("Total NPC utterances by NPC ID."),
	); err != nil {
		return nil, err
	}

	// Error counters.
	if met.ProviderErrors, err = m.Int64Counter("glyphoxa.provider.errors",
		metric.WithDescription("Total provider errors by provider and kind."),
	); err != nil {
		return nil, err
	}

	// Gauges (UpDownCounters).
	if met.ActiveNPCs, err = m.Int64UpDownCounter("glyphoxa.active_npcs",
		metric.WithDescription("Number of currently active NPC agents."),
	); err != nil {
		return nil, err
	}
	if met.ActiveSessions, err = m.Int64UpDownCounter("glyphoxa.active_sessions",
		metric.WithDescription("Number of live voice sessions."),
	); err != nil {
		return nil, err
	}
	if met.ActiveParticipants, err = m.Int64UpDownCounter("glyphoxa.active_participants",
		metric.WithDescription("Number of connected participants across all sessions."),
	); err != nil {
		return nil, err
	}

	// HTTP middleware histogram.
	if met.HTTPRequestDuration, err = m.Float64Histogram("glyphoxa.http.request.duration",
		metric.WithDescription("HTTP request latency by method and path."),
		metric.WithUnit("s"),
	); err != nil {
		return nil, err
	}

	return met, nil
}

// defaultMetrics is the lazily-initialised package-level Metrics instance.
var (
	defaultMetrics     *Metrics
	defaultMetricsOnce sync.Once
)

// DefaultMetrics returns the package-level [Metrics] instance, creating it on
// first call using [otel.GetMeterProvider]. Subsequent calls return the same
// pointer. Panics if instrument creation fails (should not happen with the
// global provider).
func DefaultMetrics() *Metrics {
	defaultMetricsOnce.Do(func() {
		var err error
		defaultMetrics, err = NewMetrics(otel.GetMeterProvider())
		if err != nil {
			panic("observe: failed to create default metrics: " + err.Error())
		}
	})
	return defaultMetrics
}

// Attr is a convenience alias for [attribute.String] to reduce verbosity at
// call sites.
func Attr(key, value string) attribute.KeyValue {
	return attribute.String(key, value)
}

// RecordProviderRequest is a convenience method that records a provider
// request counter increment with the standard attribute set.
func (m *Metrics) RecordProviderRequest(ctx context.Context, provider, kind, status string) {
	m.ProviderRequests.Add(ctx, 1,
		metric.WithAttributes(
			attribute.String("provider", provider),
			attribute.String("kind", kind),
			attribute.String("status", status),
		),
	)
}

// RecordToolCall is a convenience method that records a tool call counter
// increment with the standard attribute set.
func (m *Metrics) RecordToolCall(ctx context.Context, tool, status string) {
	m.ToolCalls.Add(ctx, 1,
		metric.WithAttributes(
			attribute.String("tool", tool),
			attribute.String("status", status),
		),
	)
}

// RecordNPCUtterance is a convenience method that records an NPC utterance
// counter increment.
func (m *Metrics) RecordNPCUtterance(ctx context.Context, npcID string) {
	m.NPCUtterances.Add(ctx, 1,
		metric.WithAttributes(attribute.String("npc_id", npcID)),
	)
}

// RecordProviderError is a convenience method that records a provider error
// counter increment.
func (m *Metrics) RecordProviderError(ctx context.Context, provider, kind string) {
	m.ProviderErrors.Add(ctx, 1,
		metric.WithAttributes(
			attribute.String("provider", provider),
			attribute.String("kind", kind),
		),
	)
}
