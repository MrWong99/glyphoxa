package observe

import (
	"context"
	"errors"

	promexporter "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// ProviderConfig configures the OpenTelemetry SDK providers.
type ProviderConfig struct {
	// ServiceName is the service name reported in telemetry. Default: "glyphoxa".
	ServiceName string

	// ServiceVersion is the service version reported in telemetry.
	ServiceVersion string

	// TraceExporter is an optional span exporter. When nil, spans are
	// recorded but not exported (useful for testing or when only metrics are
	// needed). In production this would typically be an OTLP exporter.
	TraceExporter sdktrace.SpanExporter
}

// InitProvider initialises the OTel SDK with the given config. It sets up:
//
//   - A [sdkmetric.MeterProvider] with a Prometheus exporter so metrics can
//     still be scraped via /metrics.
//   - A [sdktrace.TracerProvider] with the configured exporter (or a no-op
//     exporter if none is provided).
//
// Both providers are registered as the global OTel providers.
//
// Returns a shutdown function that flushes and closes exporters. Call it in a
// defer from main().
func InitProvider(ctx context.Context, cfg ProviderConfig) (shutdown func(context.Context) error, err error) {
	if cfg.ServiceName == "" {
		cfg.ServiceName = "glyphoxa"
	}

	// Build the resource describing this service.
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion(cfg.ServiceVersion),
		),
	)
	if err != nil {
		return nil, err
	}

	var shutdownFuncs []func(context.Context) error

	// --- Metrics: Prometheus exporter bridge ---
	promExp, err := promexporter.New()
	if err != nil {
		return nil, err
	}

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(promExp),
	)
	otel.SetMeterProvider(mp)
	shutdownFuncs = append(shutdownFuncs, mp.Shutdown)

	// --- Traces ---
	tpOpts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
	}
	if cfg.TraceExporter != nil {
		tpOpts = append(tpOpts, sdktrace.WithBatcher(cfg.TraceExporter))
	}
	tp := sdktrace.NewTracerProvider(tpOpts...)
	otel.SetTracerProvider(tp)
	shutdownFuncs = append(shutdownFuncs, tp.Shutdown)

	// Combined shutdown.
	shutdown = func(ctx context.Context) error {
		var errs []error
		for _, fn := range shutdownFuncs {
			if e := fn(ctx); e != nil {
				errs = append(errs, e)
			}
		}
		return errors.Join(errs...)
	}

	return shutdown, nil
}
