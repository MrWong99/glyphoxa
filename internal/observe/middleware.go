package observe

import (
	"log/slog"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// statusRecorder wraps [http.ResponseWriter] to capture the status code
// written by the downstream handler.
type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

// WriteHeader captures the status code and delegates to the wrapped writer.
func (r *statusRecorder) WriteHeader(code int) {
	r.statusCode = code
	r.ResponseWriter.WriteHeader(code)
}

// Middleware returns an [http.Handler] that:
//
//  1. Extracts W3C Trace Context from incoming request headers (or starts a
//     new trace).
//  2. Starts an OTel span for the HTTP request.
//  3. Sets the X-Correlation-ID response header from the trace ID.
//  4. Records request duration to [Metrics.HTTPRequestDuration].
//  5. Logs request completion with status code, duration, and trace info.
//  6. Ends the span on completion with status attributes.
func Middleware(m *Metrics) func(http.Handler) http.Handler {
	prop := propagation.TraceContext{}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// 1. Extract W3C trace context from incoming headers.
			ctx := prop.Extract(r.Context(), propagation.HeaderCarrier(r.Header))

			// 2. Start a span for this HTTP request.
			ctx, span := StartSpan(ctx, "HTTP "+r.Method+" "+r.URL.Path,
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(
					semconv.HTTPRequestMethodKey.String(r.Method),
					semconv.URLPath(r.URL.Path),
				),
			)
			defer span.End()

			// 3. Set correlation ID from trace ID.
			cid := CorrelationID(ctx)
			if cid != "" {
				w.Header().Set("X-Correlation-ID", cid)
			}

			// Inject trace context into response headers for downstream.
			prop.Inject(ctx, propagation.HeaderCarrier(w.Header()))

			r = r.WithContext(ctx)

			// Wrap the writer to capture the status code.
			rec := &statusRecorder{ResponseWriter: w, statusCode: http.StatusOK}

			// Serve the request.
			next.ServeHTTP(rec, r)

			// 4. Record duration.
			duration := time.Since(start)
			m.HTTPRequestDuration.Record(ctx, duration.Seconds(),
				metric.WithAttributes(
					attribute.String("method", r.Method),
					attribute.String("path", r.URL.Path),
				),
			)

			// Set span status attributes.
			span.SetAttributes(semconv.HTTPResponseStatusCode(rec.statusCode))

			// 5. Log completion.
			slog.LogAttrs(ctx, slog.LevelInfo, "request completed",
				slog.String("trace_id", cid),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rec.statusCode),
				slog.Duration("duration", duration),
			)
		})
	}
}
