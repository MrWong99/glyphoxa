package observe

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// newTestTracerProvider returns a TracerProvider with an in-memory exporter
// for inspecting recorded spans.
func newTestTracerProvider(t *testing.T) (*sdktrace.TracerProvider, *tracetest.InMemoryExporter) {
	t.Helper()
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	return tp, exp
}

func TestCorrelationID_EmptyByDefault(t *testing.T) {
	ctx := context.Background()
	if got := CorrelationID(ctx); got != "" {
		t.Errorf("CorrelationID(background) = %q, want empty", got)
	}
}

func TestCorrelationID_ReturnsTraceID(t *testing.T) {
	tp, _ := newTestTracerProvider(t)
	tracer := tp.Tracer("test")

	ctx, span := tracer.Start(context.Background(), "test-span")
	defer span.End()

	cid := CorrelationID(ctx)
	if len(cid) != 32 {
		t.Errorf("correlation ID length = %d, want 32", len(cid))
	}

	// Verify it's valid hex.
	for _, c := range cid {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			t.Errorf("correlation ID contains non-hex character %q", c)
			break
		}
	}
}

func TestStartSpan_CreatesSpan(t *testing.T) {
	tp, exp := newTestTracerProvider(t)

	// Temporarily override the global provider.
	origTP := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { otel.SetTracerProvider(origTP) })

	ctx, span := StartSpan(context.Background(), "test-op")
	cid := CorrelationID(ctx)
	if cid == "" {
		t.Error("StartSpan did not create a span with a trace ID")
	}

	span.End()
	spans := exp.GetSpans()
	if len(spans) == 0 {
		t.Fatal("no spans recorded")
	}
	if spans[0].Name != "test-op" {
		t.Errorf("span name = %q, want %q", spans[0].Name, "test-op")
	}
}

func TestCorrelationID_Unique(t *testing.T) {
	tp, _ := newTestTracerProvider(t)
	tracer := tp.Tracer("test")

	ids := make(map[string]struct{}, 100)
	for range 100 {
		ctx, span := tracer.Start(context.Background(), "unique-test")
		cid := CorrelationID(ctx)
		span.End()
		if _, dup := ids[cid]; dup {
			t.Fatalf("duplicate correlation ID: %s", cid)
		}
		ids[cid] = struct{}{}
	}
}

func TestLogger_IncludesTraceID(t *testing.T) {
	tp, _ := newTestTracerProvider(t)
	tracer := tp.Tracer("test")

	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	slog.SetDefault(slog.New(handler))
	t.Cleanup(func() { slog.SetDefault(slog.Default()) })

	ctx, span := tracer.Start(context.Background(), "log-test")
	defer span.End()

	l := Logger(ctx)
	l.Info("test message")

	logged := buf.String()
	if !bytes.Contains([]byte(logged), []byte("trace_id=")) {
		t.Errorf("log output missing trace_id, got: %s", logged)
	}
	if !bytes.Contains([]byte(logged), []byte("span_id=")) {
		t.Errorf("log output missing span_id, got: %s", logged)
	}
}

func TestLogger_NoSpan(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	slog.SetDefault(slog.New(handler))
	t.Cleanup(func() { slog.SetDefault(slog.Default()) })

	ctx := context.Background()
	l := Logger(ctx)
	l.Info("test message")

	logged := buf.String()
	if bytes.Contains([]byte(logged), []byte("trace_id")) {
		t.Errorf("log output should not contain trace_id, got: %s", logged)
	}
}

// Ensure the Tracer function returns a valid tracer.
func TestTracer_ReturnsValidTracer(t *testing.T) {
	tr := Tracer()
	if tr == nil {
		t.Fatal("Tracer() returned nil")
	}
	// The tracer should implement the trace.Tracer interface.
	_ = trace.Tracer(tr)
}
