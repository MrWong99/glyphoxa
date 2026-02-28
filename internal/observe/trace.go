package observe

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

// tracerName is the instrumentation scope name for the Glyphoxa tracer.
const tracerName = "github.com/MrWong99/glyphoxa"

// Tracer returns the package-level [trace.Tracer] for Glyphoxa. It uses the
// globally registered [trace.TracerProvider].
func Tracer() trace.Tracer {
	return otel.Tracer(tracerName)
}

// StartSpan starts a new span and returns the updated context and span. The
// caller must call span.End() when done.
func StartSpan(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	return Tracer().Start(ctx, name, opts...)
}

// CorrelationID extracts the trace ID from the OTel span context in ctx.
// Returns the empty string when no active span with a valid trace ID exists.
//
// This provides backward compatibility with code that used the old
// correlation ID system — the trace ID serves as the correlation identifier.
func CorrelationID(ctx context.Context) string {
	sc := trace.SpanContextFromContext(ctx)
	if sc.HasTraceID() {
		return sc.TraceID().String()
	}
	return ""
}

// Logger returns an [slog.Logger] enriched with trace_id and span_id from
// the OTel span context in ctx. When no active span is present, the returned
// logger is the default slog logger without extra attributes.
func Logger(ctx context.Context) *slog.Logger {
	l := slog.Default()
	sc := trace.SpanContextFromContext(ctx)
	if sc.HasTraceID() {
		l = l.With(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}
	return l
}
