package observe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// testSetup creates both metrics and tracing infrastructure for middleware tests.
func testSetup(t *testing.T) (*Metrics, *sdkmetric.ManualReader, *tracetest.InMemoryExporter) {
	t.Helper()

	// Metrics.
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })

	m, err := NewMetrics(mp)
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}

	// Tracing.
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	origTP := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { otel.SetTracerProvider(origTP) })

	return m, reader, exp
}

func TestMiddleware_SetsCorrelationID(t *testing.T) {
	m, _, _ := testSetup(t)
	mw := Middleware(m)

	var capturedCID string
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedCID = CorrelationID(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// A correlation ID (trace ID) should have been generated.
	if capturedCID == "" {
		t.Error("middleware did not set correlation ID in context")
	}
	if len(capturedCID) != 32 {
		t.Errorf("generated correlation ID length = %d, want 32", len(capturedCID))
	}

	// Response header should contain the same ID.
	if got := rec.Header().Get("X-Correlation-ID"); got != capturedCID {
		t.Errorf("response X-Correlation-ID = %q, want %q", got, capturedCID)
	}
}

func TestMiddleware_CreatesSpan(t *testing.T) {
	m, _, exp := testSetup(t)
	mw := Middleware(m)

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/span-test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	spans := exp.GetSpans()
	if len(spans) == 0 {
		t.Fatal("middleware did not create a span")
	}
	if spans[0].Name != "HTTP GET /span-test" {
		t.Errorf("span name = %q, want %q", spans[0].Name, "HTTP GET /span-test")
	}
}

func TestMiddleware_RecordsDuration(t *testing.T) {
	m, reader, _ := testSetup(t)
	mw := Middleware(m)

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/metrics-test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}

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

	// Verify attributes.
	dp := hist.DataPoints[0]
	if dp.Count != 1 {
		t.Errorf("sample count = %d, want 1", dp.Count)
	}

	attrs := dp.Attributes.ToSlice()
	foundMethod, foundPath := false, false
	for _, kv := range attrs {
		if string(kv.Key) == "method" && kv.Value.AsString() == "GET" {
			foundMethod = true
		}
		if string(kv.Key) == "path" && kv.Value.AsString() == "/metrics-test" {
			foundPath = true
		}
	}
	if !foundMethod {
		t.Error("missing method attribute")
	}
	if !foundPath {
		t.Error("missing path attribute")
	}
}

func TestMiddleware_CapturesStatusCode(t *testing.T) {
	m, _, exp := testSetup(t)
	mw := Middleware(m)

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))

	req := httptest.NewRequest("GET", "/not-found", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("response status = %d, want %d", rec.Code, http.StatusNotFound)
	}

	// Verify span has status code attribute.
	spans := exp.GetSpans()
	if len(spans) == 0 {
		t.Fatal("no spans recorded")
	}
	found := false
	for _, a := range spans[0].Attributes {
		if string(a.Key) == "http.response.status_code" && a.Value.AsInt64() == 404 {
			found = true
		}
	}
	if !found {
		t.Error("span missing http.response.status_code attribute")
	}
}

func TestMiddleware_PropagatesW3CTraceContext(t *testing.T) {
	m, _, _ := testSetup(t)
	mw := Middleware(m)

	var capturedCID string
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedCID = CorrelationID(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	// Send a request with a W3C traceparent header.
	req := httptest.NewRequest("GET", "/propagate", nil)
	req.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// The correlation ID should be the trace ID from the incoming header.
	if capturedCID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("correlation ID = %q, want %q", capturedCID, "4bf92f3577b34da6a3ce929d0e0e4736")
	}

	// The response should also contain this correlation ID.
	if got := rec.Header().Get("X-Correlation-ID"); got != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("response X-Correlation-ID = %q, want %q", got, "4bf92f3577b34da6a3ce929d0e0e4736")
	}
}
