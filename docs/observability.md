# 🔭 Observability

Monitoring, metrics, and tracing for Glyphoxa in production and development.

---

## 📖 Overview

Glyphoxa ships a comprehensive observability stack built on industry-standard components:

| Layer | Technology | Purpose |
|-------|-----------|---------|
| **Instrumentation** | [OpenTelemetry](https://opentelemetry.io/) Go SDK | Traces and metrics recorded at every pipeline stage |
| **Collection** | [Prometheus](https://prometheus.io/) | Scrapes the `/metrics` endpoint; stores time-series data |
| **Visualisation** | [Grafana](https://grafana.com/) | Pre-built dashboards for latency, throughput, and errors |
| **Health probes** | Built-in `/healthz` and `/readyz` | Kubernetes-style liveness and readiness checks |

All three infrastructure services (Glyphoxa, Prometheus, Grafana) are defined in the Docker Compose file and activated with the `alpha` profile:

```bash
docker compose --profile alpha up
```

---

## 🔗 OpenTelemetry Integration

### Provider Initialisation

The `observe.InitProvider` function (in `internal/observe/provider.go`) bootstraps the OpenTelemetry SDK at startup. It creates:

1. **MeterProvider** -- backed by a Prometheus exporter bridge so all OTel metrics are exposed on the standard `/metrics` HTTP endpoint.
2. **TracerProvider** -- optionally batching spans to an OTLP exporter. When no `TraceExporter` is configured, spans are recorded in-process only (useful for dev / metrics-only deployments).

Both providers are registered as the global OTel providers via `otel.SetMeterProvider` and `otel.SetTracerProvider`.

```go
shutdown, err := observe.InitProvider(ctx, observe.ProviderConfig{
    ServiceName:    "glyphoxa",
    ServiceVersion: version,
    TraceExporter:  otlpExporter, // nil to disable export
})
defer shutdown(ctx)
```

The returned `shutdown` function flushes pending telemetry and closes exporters -- call it in a `defer` from `main()`.

### Tracing

The `observe` package exposes convenience helpers for distributed tracing:

| Function | Description |
|----------|-------------|
| `observe.Tracer()` | Returns the package-level `trace.Tracer` scoped to `github.com/MrWong99/glyphoxa` |
| `observe.StartSpan(ctx, name, ...opts)` | Starts a new span, returns `(ctx, span)`. Caller must call `span.End()` |
| `observe.CorrelationID(ctx)` | Extracts the 32-character hex trace ID from the span context (empty string if no active span) |
| `observe.Logger(ctx)` | Returns an `slog.Logger` enriched with `trace_id` and `span_id` attributes |

### HTTP Middleware

`observe.Middleware(m *Metrics)` wraps any `http.Handler` and performs the following for every request:

1. **Extracts** W3C Trace Context (`traceparent` header) from incoming requests, or starts a new trace.
2. **Starts** a server span named `HTTP <METHOD> <PATH>` with semantic convention attributes.
3. **Sets** the `X-Correlation-ID` response header to the trace ID.
4. **Injects** W3C trace context into response headers for downstream propagation.
5. **Records** request duration to `glyphoxa.http.request.duration`.
6. **Logs** request completion with status code, duration, and trace info via `slog`.
7. **Ends** the span with `http.response.status_code` attribute.

### Span Naming Conventions

Spans created across the codebase follow this pattern:

| Span Name | Where |
|-----------|-------|
| `HTTP GET /healthz` | HTTP middleware |
| `HTTP POST /api/v1/...` | HTTP middleware |
| Custom operation names | Application code via `observe.StartSpan` |

### Structured Logging Correlation

The `observe.Logger(ctx)` helper bridges tracing and logging. When an active span exists in the context, the returned logger automatically includes `trace_id` and `span_id` fields, allowing log aggregation tools to correlate log lines with traces:

```json
{
  "level": "INFO",
  "msg": "request completed",
  "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
  "span_id": "00f067aa0ba902b7",
  "method": "GET",
  "path": "/readyz",
  "status": 200,
  "duration": "1.234ms"
}
```

---

## 📊 Prometheus Metrics

All metrics are defined in `internal/observe/metrics.go` under the instrumentation scope `github.com/MrWong99/glyphoxa`. The Prometheus exporter bridge translates OTel metric names to Prometheus conventions (dots become underscores, unit suffixes are appended automatically).

### Histogram Bucket Boundaries

All latency histograms share the same bucket boundaries, optimised for voice-pipeline latencies:

```
0.01s, 0.025s, 0.05s, 0.1s, 0.25s, 0.5s, 1s, 2.5s, 5s, 10s
```

### Full Metric Reference

#### Histograms (Latency)

| OTel Name | Prometheus Name | Unit | Labels | Description |
|-----------|----------------|------|--------|-------------|
| `glyphoxa.stt.duration` | `glyphoxa_stt_duration_seconds` | seconds | _(none)_ | Latency of speech-to-text transcription |
| `glyphoxa.llm.duration` | `glyphoxa_llm_duration_seconds` | seconds | _(none)_ | Latency of LLM inference |
| `glyphoxa.tts.duration` | `glyphoxa_tts_duration_seconds` | seconds | _(none)_ | Latency of text-to-speech synthesis |
| `glyphoxa.s2s.duration` | `glyphoxa_s2s_duration_seconds` | seconds | _(none)_ | End-to-end speech-to-speech latency (mouth-to-ear) |
| `glyphoxa.tool_execution.duration` | `glyphoxa_tool_execution_duration_seconds` | seconds | _(none)_ | Latency of MCP tool execution |
| `glyphoxa.http.request.duration` | `glyphoxa_http_request_duration_seconds` | seconds | `method`, `path` | HTTP request processing time |

#### Counters

| OTel Name | Prometheus Name | Labels | Description |
|-----------|----------------|--------|-------------|
| `glyphoxa.provider.requests` | `glyphoxa_provider_requests_total` | `provider`, `kind`, `status` | Total provider API requests |
| `glyphoxa.tool.calls` | `glyphoxa_tool_calls_total` | `tool`, `status` | Total MCP tool invocations |
| `glyphoxa.npc.utterances` | `glyphoxa_npc_utterances_total` | `npc_id` | Total NPC response utterances |
| `glyphoxa.provider.errors` | `glyphoxa_provider_errors_total` | `provider`, `kind` | Total provider errors |

#### Gauges (UpDownCounters)

| OTel Name | Prometheus Name | Labels | Description |
|-----------|----------------|--------|-------------|
| `glyphoxa.active_npcs` | `glyphoxa_active_npcs` | _(none)_ | Number of currently active NPC agents |
| `glyphoxa.active_sessions` | `glyphoxa_active_sessions` | _(none)_ | Number of live voice sessions |
| `glyphoxa.active_participants` | `glyphoxa_active_participants` | _(none)_ | Number of connected participants across all sessions |

### Label Reference

| Label | Used By | Values |
|-------|---------|--------|
| `provider` | `provider.requests`, `provider.errors` | `openai`, `anthropic`, `deepgram`, `elevenlabs`, `ollama`, `coqui`, etc. |
| `kind` | `provider.requests`, `provider.errors` | `llm`, `stt`, `tts`, `s2s`, `embeddings` |
| `status` | `provider.requests`, `tool.calls` | `ok`, `error` |
| `tool` | `tool.calls` | MCP tool name (e.g. `dice_roll`, `lookup_spell`) |
| `npc_id` | `npc.utterances` | NPC identifier (e.g. `bartender_01`, `guard_02`) |
| `method` | `http.request.duration` | HTTP method (`GET`, `POST`, etc.) |
| `path` | `http.request.duration` | Request path (e.g. `/healthz`, `/readyz`) |

### Convenience Recording Methods

The `Metrics` struct provides helper methods that apply the correct label sets automatically:

```go
m := observe.DefaultMetrics()

// Record a successful OpenAI LLM request
m.RecordProviderRequest(ctx, "openai", "llm", "ok")

// Record a provider error
m.RecordProviderError(ctx, "elevenlabs", "tts")

// Record a tool call
m.RecordToolCall(ctx, "dice_roll", "ok")

// Record an NPC utterance
m.RecordNPCUtterance(ctx, "bartender_01")

// Adjust gauges
m.ActiveSessions.Add(ctx, 1)   // session started
m.ActiveSessions.Add(ctx, -1)  // session ended
```

---

## 📈 Grafana Dashboards

### Pre-Built Dashboard: Glyphoxa - Alpha Overview

A comprehensive overview dashboard is provisioned automatically at `deployments/compose/grafana/dashboards/glyphoxa-overview.json`. It includes:

| Panel | Type | Description |
|-------|------|-------------|
| **Active Sessions** | Stat | Current number of live voice sessions. Thresholds: green < 3, yellow 3-5, red > 5 |
| **Active NPCs** | Stat | Current number of active NPC agents |
| **NPC Utterances (rate)** | Stat | Utterances per second across all NPCs |
| **Provider Errors (rate)** | Stat | Errors per second across all providers. Threshold: red > 0.1/s |
| **STT Latency (p50 / p95)** | Time series | Speech-to-text latency percentiles over time |
| **LLM Latency (p50 / p95)** | Time series | LLM inference latency percentiles over time |
| **TTS Latency (p50 / p95)** | Time series | Text-to-speech latency percentiles over time |
| **End-to-End S2S Latency (p50 / p95)** | Time series | Full mouth-to-ear latency percentiles |
| **Provider Requests by Kind** | Time series | Request rate broken down by provider kind (llm, stt, tts, etc.) |
| **Tool Calls by Tool** | Time series | MCP tool invocation rate by tool name |
| **HTTP Request Duration (p95)** | Time series | p95 HTTP request latency broken down by path |

### Accessing Grafana

| | |
|---|---|
| **URL** | `http://localhost:3000` |
| **Default user** | `admin` |
| **Default password** | `admin` |
| **Dashboard folder** | `Glyphoxa` |

Dashboards are auto-provisioned from the filesystem on startup. The provisioning configuration is at:

- **Dashboard provider**: `deployments/compose/grafana/provisioning/dashboards/default.yml`
- **Datasource**: `deployments/compose/grafana/provisioning/datasources/prometheus.yml`

The Prometheus datasource is pre-configured to connect to `http://prometheus:9090` and is set as the default.

### Adding Custom Dashboards

Place additional JSON dashboard files in `deployments/compose/grafana/dashboards/`. They are automatically discovered on Grafana startup.

---

## 🏥 Health Endpoints

The health subsystem (`internal/health/health.go`) provides Kubernetes-compatible probe endpoints.

### Endpoints

| Endpoint | Method | Purpose | Always Healthy? |
|----------|--------|---------|-----------------|
| `/healthz` | `GET` | **Liveness probe** -- is the process alive and serving HTTP? | Yes (always returns 200) |
| `/readyz` | `GET` | **Readiness probe** -- is the service ready to accept traffic? | No (depends on checker results) |

### Response Format

Both endpoints return JSON with `Content-Type: application/json; charset=utf-8`.

**Healthy response** (`200 OK`):

```json
{
  "status": "ok",
  "checks": {
    "database": "ok",
    "providers": "ok"
  }
}
```

**Unhealthy response** (`503 Service Unavailable`):

```json
{
  "status": "fail",
  "checks": {
    "database": "fail: connection refused",
    "providers": "ok"
  }
}
```

The `/healthz` endpoint always returns `{"status": "ok"}` with no checks -- if the process can respond to HTTP, it is alive.

The `/readyz` endpoint evaluates registered `Checker` functions sequentially. Each checker has a **5-second timeout**. If any checker fails or times out, the overall status is `fail` and HTTP status is `503`.

### Kubernetes Probe Configuration

```yaml
livenessProbe:
  httpGet:
    path: /healthz
    port: 8080
  initialDelaySeconds: 5
  periodSeconds: 10
  failureThreshold: 3

readinessProbe:
  httpGet:
    path: /readyz
    port: 8080
  initialDelaySeconds: 10
  periodSeconds: 5
  failureThreshold: 3
```

### Registering Custom Checkers

```go
import "github.com/MrWong99/glyphoxa/internal/health"

h := health.New(
    health.Checker{
        Name:  "database",
        Check: func(ctx context.Context) error {
            return db.PingContext(ctx)
        },
    },
    health.Checker{
        Name:  "providers",
        Check: func(ctx context.Context) error {
            // verify provider connectivity
            return nil
        },
    },
)

h.Register(mux) // adds GET /healthz and GET /readyz
```

---

## 🎯 Key Metrics to Monitor

### Latency

The voice pipeline's perceived quality depends on end-to-end latency. Monitor these in order of impact:

| What | PromQL | Target |
|------|--------|--------|
| **Mouth-to-ear p95** | `histogram_quantile(0.95, sum(rate(glyphoxa_s2s_duration_seconds_bucket[5m])) by (le))` | < 2s |
| **STT p95** | `histogram_quantile(0.95, sum(rate(glyphoxa_stt_duration_seconds_bucket[5m])) by (le))` | < 500ms |
| **LLM p95** | `histogram_quantile(0.95, sum(rate(glyphoxa_llm_duration_seconds_bucket[5m])) by (le))` | < 1s |
| **TTS p95** | `histogram_quantile(0.95, sum(rate(glyphoxa_tts_duration_seconds_bucket[5m])) by (le))` | < 500ms |
| **Tool execution p95** | `histogram_quantile(0.95, sum(rate(glyphoxa_tool_execution_duration_seconds_bucket[5m])) by (le))` | < 2s |
| **HTTP p95 by path** | `histogram_quantile(0.95, sum(rate(glyphoxa_http_request_duration_seconds_bucket[5m])) by (le, path))` | < 100ms |

### Error Rates

| What | PromQL | Alert When |
|------|--------|------------|
| **Provider error rate (total)** | `sum(rate(glyphoxa_provider_errors_total[5m]))` | > 0.1/s sustained |
| **Provider error rate by provider** | `sum(rate(glyphoxa_provider_errors_total[5m])) by (provider)` | Any provider > 0.05/s |
| **Provider error rate by kind** | `sum(rate(glyphoxa_provider_errors_total[5m])) by (kind)` | STT or TTS errors (breaks pipeline) |
| **Failed tool calls** | `sum(rate(glyphoxa_tool_calls_total{status="error"}[5m]))` | > 0.1/s |
| **Provider error ratio** | `sum(rate(glyphoxa_provider_errors_total[5m])) / sum(rate(glyphoxa_provider_requests_total[5m]))` | > 5% |

### Resource Usage

Use standard Go runtime metrics exposed by the Prometheus client alongside Glyphoxa metrics:

| What | PromQL | Notes |
|------|--------|-------|
| **Goroutines** | `go_goroutines` | Watch for leaks -- sustained growth indicates a bug |
| **Heap memory** | `go_memstats_heap_alloc_bytes` | Should be stable under load |
| **GC pause time** | `go_gc_duration_seconds` | High pauses affect voice latency |

### Session Metrics

| What | PromQL | Notes |
|------|--------|-------|
| **Active sessions** | `glyphoxa_active_sessions` | Current voice sessions |
| **Active NPCs** | `glyphoxa_active_npcs` | Currently loaded NPC agents |
| **Active participants** | `glyphoxa_active_participants` | Connected players across all sessions |
| **Utterance throughput** | `sum(rate(glyphoxa_npc_utterances_total[5m]))` | NPC responses per second |
| **Utterances per NPC** | `sum(rate(glyphoxa_npc_utterances_total[5m])) by (npc_id)` | Identifies hot NPCs |

---

## 🚨 Alerting Recommendations

### Critical Alerts

```yaml
# Provider is completely down
- alert: ProviderDown
  expr: sum(rate(glyphoxa_provider_errors_total[5m])) by (provider, kind) > 0.5
  for: 2m
  labels:
    severity: critical
  annotations:
    summary: "Provider {{ $labels.provider }} ({{ $labels.kind }}) is failing"
    description: "Error rate exceeds 0.5/s for 2 minutes. Voice pipeline is likely broken."

# No active sessions when there should be
- alert: NoActiveSessions
  expr: glyphoxa_active_sessions == 0
  for: 10m
  labels:
    severity: warning
  annotations:
    summary: "No active voice sessions"
    description: "No voice sessions have been active for 10 minutes during expected hours."

# End-to-end latency is unacceptable
- alert: HighS2SLatency
  expr: histogram_quantile(0.95, sum(rate(glyphoxa_s2s_duration_seconds_bucket[5m])) by (le)) > 5
  for: 5m
  labels:
    severity: critical
  annotations:
    summary: "End-to-end voice latency p95 exceeds 5 seconds"
    description: "Players are experiencing unacceptable delays in NPC responses."
```

### Warning Alerts

```yaml
# LLM latency is degrading
- alert: HighLLMLatency
  expr: histogram_quantile(0.95, sum(rate(glyphoxa_llm_duration_seconds_bucket[5m])) by (le)) > 3
  for: 5m
  labels:
    severity: warning
  annotations:
    summary: "LLM inference p95 latency exceeds 3 seconds"

# Tool execution is slow
- alert: SlowToolExecution
  expr: histogram_quantile(0.95, sum(rate(glyphoxa_tool_execution_duration_seconds_bucket[5m])) by (le)) > 5
  for: 5m
  labels:
    severity: warning
  annotations:
    summary: "MCP tool execution p95 exceeds 5 seconds"

# Error ratio is climbing
- alert: HighProviderErrorRatio
  expr: >
    sum(rate(glyphoxa_provider_errors_total[5m]))
    /
    sum(rate(glyphoxa_provider_requests_total[5m]))
    > 0.05
  for: 5m
  labels:
    severity: warning
  annotations:
    summary: "Provider error ratio exceeds 5%"

# Goroutine leak
- alert: GoroutineLeak
  expr: go_goroutines > 1000
  for: 15m
  labels:
    severity: warning
  annotations:
    summary: "Goroutine count exceeds 1000 for 15 minutes"
    description: "Possible goroutine leak. Investigate with pprof."

# STT latency spike
- alert: HighSTTLatency
  expr: histogram_quantile(0.95, sum(rate(glyphoxa_stt_duration_seconds_bucket[5m])) by (le)) > 2
  for: 5m
  labels:
    severity: warning
  annotations:
    summary: "STT transcription p95 exceeds 2 seconds"

# TTS latency spike
- alert: HighTTSLatency
  expr: histogram_quantile(0.95, sum(rate(glyphoxa_tts_duration_seconds_bucket[5m])) by (le)) > 2
  for: 5m
  labels:
    severity: warning
  annotations:
    summary: "TTS synthesis p95 exceeds 2 seconds"
```

---

## ⚙️ Configuration

### Prometheus Scrape Configuration

Prometheus is configured in `deployments/compose/prometheus.yml`:

```yaml
global:
  scrape_interval: 15s
  evaluation_interval: 15s

scrape_configs:
  - job_name: glyphoxa
    static_configs:
      - targets: ["glyphoxa:8080"]
    metrics_path: /metrics
```

The scrape target assumes Glyphoxa is running in the same Docker Compose network on port 8080. The `/metrics` endpoint is served by the Prometheus exporter bridge created in `observe.InitProvider`.

### Docker Compose Services

Both Prometheus and Grafana are activated with the `alpha` profile:

```bash
docker compose --profile alpha up
```

| Service | Port | Profile | Storage |
|---------|------|---------|---------|
| **Prometheus** | `9090` | `alpha` | `prometheus_data` volume, 30-day retention |
| **Grafana** | `3000` | `alpha` | `grafana_data` volume |

### Server Configuration

Observability-relevant fields in the Glyphoxa config file (`config.yaml`):

```yaml
server:
  # The listen address also serves as the metrics endpoint
  # Metrics are exposed at http://<listen_addr>/metrics
  listen_addr: ":8080"

  # Log verbosity: debug | info | warn | error
  # Use "debug" during development to see trace IDs in every log line
  log_level: info
```

### OpenTelemetry Provider Configuration

The `observe.ProviderConfig` struct controls OTel SDK setup:

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `ServiceName` | `string` | `"glyphoxa"` | Service name reported in all telemetry (resource attribute `service.name`) |
| `ServiceVersion` | `string` | `""` | Service version reported in telemetry (resource attribute `service.version`) |
| `TraceExporter` | `sdktrace.SpanExporter` | `nil` | Span exporter (e.g. OTLP). When nil, spans are recorded but not exported |

To export traces to an OTLP-compatible backend (Jaeger, Tempo, Honeycomb, etc.), configure a `TraceExporter` in the `ProviderConfig`:

```go
import "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"

exporter, err := otlptracegrpc.New(ctx,
    otlptracegrpc.WithEndpoint("tempo:4317"),
    otlptracegrpc.WithInsecure(),
)

shutdown, err := observe.InitProvider(ctx, observe.ProviderConfig{
    ServiceName:    "glyphoxa",
    ServiceVersion: "1.0.0",
    TraceExporter:  exporter,
})
```

### Environment Variables

Standard OTel environment variables are respected by the Go SDK:

| Variable | Example | Purpose |
|----------|---------|---------|
| `OTEL_SERVICE_NAME` | `glyphoxa` | Override service name |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `http://tempo:4317` | OTLP collector endpoint |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | `grpc` | OTLP transport protocol |
| `OTEL_TRACES_SAMPLER` | `parentbased_traceidratio` | Trace sampling strategy |
| `OTEL_TRACES_SAMPLER_ARG` | `0.1` | Sample 10% of traces |

---

## 🔍 See also

- [`deployment.md`](deployment.md) -- Production deployment guide
- [`troubleshooting.md`](troubleshooting.md) -- Common issues and debugging
- [`architecture.md`](architecture.md) -- System architecture and pipeline design
