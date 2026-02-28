# 🚀 Deployment & Production Setup

Guide for deploying Glyphoxa in production. Covers Docker Compose, binary releases, building from source, database setup, TLS, and operational considerations.

---

## 📦 Overview

Glyphoxa can be deployed in three ways:

| Method | Best for | GPU support | Complexity |
|--------|----------|-------------|------------|
| **Docker Compose** (recommended) | Most deployments — local or cloud providers | NVIDIA (via container toolkit) | Low |
| **Binary from release** | Custom orchestration, systemd, bare-metal | Host-native (CUDA, Metal) | Medium |
| **Build from source** | Development, customisation, non-Linux platforms | Host-native | High |

All methods produce a single statically-linked binary with whisper.cpp baked in. The binary requires a PostgreSQL database with the pgvector extension and a YAML configuration file.

---

## 🐳 Docker Compose Deployment

The `deployments/compose/` directory provides a ready-to-use Docker Compose stack with two operational modes.

### Local mode (no API keys)

Uses Ollama (LLM + embeddings), native whisper.cpp (STT), Coqui TTS, and Silero VAD -- all running locally:

```bash
cd deployments/compose
cp config.local.yaml config.yaml
docker compose --profile local up -d
```

On first start, the bootstrap services automatically download:
- **llama3.2** (~2 GB) and **nomic-embed-text** (~300 MB) via Ollama
- **ggml-base.en.bin** (~147 MB) whisper model

### Cloud mode (API-based providers)

Uses cloud APIs (OpenAI, Deepgram, ElevenLabs, etc.) -- only PostgreSQL runs locally:

```bash
cd deployments/compose
cp config.yaml.example config.yaml
# Edit config.yaml — add your API keys
docker compose up -d
```

### Profiles

| Profile | Command | Services started |
|---------|---------|-----------------|
| (default) | `docker compose up` | postgres, glyphoxa |
| `local` | `docker compose --profile local up` | postgres, glyphoxa, ollama, ollama-bootstrap, whisper-model-download, tts |
| `alpha` | `docker compose --profile alpha up` | adds prometheus, grafana |

Profiles can be combined: `docker compose --profile local --profile alpha up` starts the full stack with monitoring.

For complete setup details -- GPU acceleration, model selection, endpoints, and troubleshooting -- see [`deployments/compose/README.md`](../deployments/compose/README.md).

---

## 🔨 Building from Source

### Prerequisites

- **Go 1.26+** with `CGO_ENABLED=1`
- **C/C++ toolchain**: `gcc`, `g++`, `cmake` (for whisper.cpp)
- **System libraries**: `libopus-dev` (Debian/Ubuntu), `opus` (Arch/macOS)
- **ONNX Runtime**: required for the Silero VAD provider

Install build dependencies:

```bash
# Debian / Ubuntu
sudo apt install -y build-essential cmake git libopus-dev pkg-config

# Arch Linux
sudo pacman -S base-devel cmake git opus

# macOS
brew install cmake opus pkg-config
```

### Build the binary

```bash
# 1. Build whisper.cpp static library
make whisper-libs

# 2. Set environment for CGO linking
export C_INCLUDE_PATH=/tmp/whisper-install/include
export LIBRARY_PATH=/tmp/whisper-install/lib
export CGO_ENABLED=1

# 3. Build Glyphoxa
make build
```

The binary is output to `./bin/glyphoxa`. It is statically linked and has no runtime library dependencies.

### Run the binary

```bash
./bin/glyphoxa -config /path/to/config.yaml
```

---

## 📦 GoReleaser

Tagged releases (`v*`) are built automatically by GitHub Actions using [GoReleaser](https://goreleaser.com/).

### What the release pipeline produces

1. **Statically-linked binaries** for `linux/amd64` and `linux/arm64`
2. **tar.gz archives** named `glyphoxa_<version>_linux_<arch>.tar.gz`
3. **SHA-256 checksums** in `checksums.txt`
4. **Multi-arch Docker image** pushed to `ghcr.io/mrwong99/glyphoxa`

### Docker image tags

Each release produces the following tags:

```
ghcr.io/mrwong99/glyphoxa:<version>       # e.g. 0.3.0
ghcr.io/mrwong99/glyphoxa:<major>.<minor>  # e.g. 0.3
```

The `main` branch builds a separate image via the multi-stage `Dockerfile` (used in the Compose stack by default).

### Supported architectures

| Architecture | Binary | Docker image |
|-------------|--------|-------------|
| `linux/amd64` | Yes | Yes |
| `linux/arm64` | Yes | Yes |

Both architectures cross-compile whisper.cpp and libopus as static libraries during the CI build. The release Docker image uses `gcr.io/distroless/static-debian12:nonroot` as its base -- no shell, no package manager, minimal attack surface.

---

## 🎮 GPU Setup

GPU acceleration is optional but strongly recommended for local inference providers.

### Ollama (LLM)

Ollama automatically detects NVIDIA GPUs. Ensure the [NVIDIA Container Toolkit](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/) is installed, then enable GPU passthrough in `docker-compose.yml`:

```yaml
ollama:
  image: ollama/ollama:latest
  deploy:
    resources:
      reservations:
        devices:
          - driver: nvidia
            count: all
            capabilities: [gpu]
```

This block is already present in the provided Compose file.

### Coqui TTS

The Compose stack uses the GPU-enabled Coqui image (`ghcr.io/coqui-ai/tts`) with `--use_cuda true`. For CPU-only deployments, switch to the CPU image and remove the flag:

```yaml
tts:
  image: ghcr.io/coqui-ai/tts-cpu
  command: ["--model_name", "tts_models/en/ljspeech/vits", "--port", "5002"]
  # Remove the deploy.resources block
```

### whisper.cpp (native STT)

whisper.cpp is compiled into the Glyphoxa binary. GPU acceleration depends on how the library was built:

| Backend | Build flag | Platform |
|---------|-----------|----------|
| CPU only | (default) | All |
| CUDA | `-DGGML_CUDA=ON` | Linux (NVIDIA) |
| Metal | `-DGGML_METAL=ON` | macOS (Apple Silicon) |

To build with CUDA support locally:

```bash
git clone --depth 1 https://github.com/ggml-org/whisper.cpp.git /tmp/whisper-src
cd /tmp/whisper-src
cmake -B build \
  -DCMAKE_BUILD_TYPE=Release \
  -DBUILD_SHARED_LIBS=OFF \
  -DGGML_CUDA=ON \
  -DWHISPER_BUILD_EXAMPLES=OFF \
  -DWHISPER_BUILD_TESTS=OFF
cmake --build build --config Release -j$(nproc)
```

Then set `C_INCLUDE_PATH` and `LIBRARY_PATH` to point at the built artifacts and rebuild Glyphoxa with `make build`.

> **Note:** The Docker multi-stage build (`Dockerfile`) compiles whisper.cpp in CPU-only mode. For GPU-accelerated whisper in containers, build a custom image with the CUDA flag enabled and the appropriate NVIDIA base image.

---

## 🗄️ PostgreSQL Production Setup

Glyphoxa uses PostgreSQL with the [pgvector](https://github.com/pgvector/pgvector) extension for semantic memory and knowledge graph storage.

### Required extension

The `pgvector` extension must be available in your PostgreSQL installation. Glyphoxa runs `CREATE EXTENSION IF NOT EXISTS vector` automatically during schema migration.

For the Docker Compose setup, the `pgvector/pgvector:pg17` image includes the extension pre-installed and the `init.sql` script enables it:

```sql
CREATE EXTENSION IF NOT EXISTS vector;
```

### Schema initialisation

Glyphoxa auto-migrates its schema on startup using idempotent DDL (`CREATE TABLE IF NOT EXISTS` / `CREATE INDEX IF NOT EXISTS`). The following tables are created:

| Table | Purpose |
|-------|---------|
| `session_entries` | Short-term conversation log (L1 memory) |
| `entities` | Knowledge graph nodes (NPCs, locations, items) |
| `relationships` | Knowledge graph edges between entities |
| `chunks` | Semantic retrieval vectors (L2 memory, pgvector) |
| `npc_definitions` | Persistent NPC configurations |

### Recommended indices

All indices are created automatically by the migration. Key indices for query performance:

```sql
-- Session entries
CREATE INDEX idx_session_entries_session_id ON session_entries (session_id);
CREATE INDEX idx_session_entries_timestamp ON session_entries (timestamp);
CREATE INDEX idx_session_entries_session_timestamp ON session_entries (session_id, timestamp);
CREATE INDEX idx_session_entries_fts ON session_entries USING GIN (to_tsvector('english', text));

-- Knowledge graph
CREATE INDEX idx_entities_type ON entities (type);
CREATE INDEX idx_entities_name ON entities (name);
CREATE INDEX idx_rel_source ON relationships (source_id);
CREATE INDEX idx_rel_target ON relationships (target_id);

-- Vector search (HNSW)
CREATE INDEX idx_chunks_embedding ON chunks USING hnsw (embedding vector_cosine_ops);
```

The HNSW index on the `chunks` table is critical for vector similarity search performance. It is created with the default pgvector HNSW parameters; for large deployments (>100k chunks), consider tuning `m` and `ef_construction`:

```sql
CREATE INDEX idx_chunks_embedding ON chunks
  USING hnsw (embedding vector_cosine_ops)
  WITH (m = 24, ef_construction = 200);
```

### Embedding dimensions

The `embedding_dimensions` config value must match the model configured in `providers.embeddings`. Common values:

| Model | Dimensions |
|-------|-----------|
| `nomic-embed-text` (Ollama) | 768 |
| `text-embedding-3-small` (OpenAI) | 1536 |
| `text-embedding-3-large` (OpenAI) | 3072 |

### Backup considerations

- **Regular backups**: Use `pg_dump` or continuous archiving (WAL-based PITR) for production deployments.
- **Volume snapshots**: For Docker deployments, back up the `pgdata` volume.
- The `session_entries` table grows proportionally to game session length. Consider partitioning by `session_id` or archiving old sessions for long-running campaigns.
- The `chunks` table contains embedding vectors that are expensive to regenerate. Prioritise this table in backup strategies.

### Connection string

Configure the DSN in your config file:

```yaml
memory:
  postgres_dsn: "postgres://glyphoxa:STRONG_PASSWORD@db-host:5432/glyphoxa?sslmode=require"
  embedding_dimensions: 768
```

For production, always use `sslmode=require` or `sslmode=verify-full`.

---

## 🔒 TLS Configuration

Glyphoxa supports TLS natively via the `server.tls` configuration block. When configured, the server listens on HTTPS instead of plain HTTP.

```yaml
server:
  listen_addr: ":8443"
  log_level: info
  tls:
    cert_file: /etc/glyphoxa/tls/server.crt
    key_file: /etc/glyphoxa/tls/server.key
```

| Field | Description |
|-------|-------------|
| `server.tls.cert_file` | Path to the PEM-encoded TLS certificate (or certificate chain) |
| `server.tls.key_file` | Path to the PEM-encoded TLS private key |

When `tls` is omitted or `null`, the server runs plain HTTP.

For Docker deployments, mount the certificate files as read-only volumes:

```yaml
glyphoxa:
  volumes:
    - ./config.yaml:/etc/glyphoxa/config.yaml:ro
    - /etc/letsencrypt/live/example.com/fullchain.pem:/etc/glyphoxa/tls/server.crt:ro
    - /etc/letsencrypt/live/example.com/privkey.pem:/etc/glyphoxa/tls/server.key:ro
```

> **Tip:** In most production setups, TLS is terminated at a reverse proxy (nginx, Caddy, Traefik) or cloud load balancer rather than in Glyphoxa itself. Use the built-in TLS when Glyphoxa is exposed directly or when end-to-end encryption is required.

---

## 📊 Resource Requirements

Recommendations vary by deployment profile. All values are per-host minimums.

### Minimal (cloud providers only)

Glyphoxa + PostgreSQL. All inference handled by cloud APIs.

| Resource | Recommendation |
|----------|---------------|
| CPU | 2 cores |
| Memory | 1 GB |
| Storage | 5 GB (database + config) |
| GPU | Not required |
| Network | Low latency to cloud API endpoints |

### Standard (mixed local + cloud)

Glyphoxa + PostgreSQL + Ollama (LLM). STT/TTS via cloud APIs.

| Resource | Recommendation |
|----------|---------------|
| CPU | 4 cores |
| Memory | 8 GB (4 GB for Ollama with 3B model) |
| Storage | 15 GB (models + database) |
| GPU | Recommended (6+ GB VRAM for 3B model) |

### Full local (no cloud APIs)

Glyphoxa + PostgreSQL + Ollama + Coqui TTS. Whisper.cpp runs in-process.

| Resource | Recommendation |
|----------|---------------|
| CPU | 8 cores |
| Memory | 16 GB |
| Storage | 30 GB (LLM models ~2 GB, embedding model ~300 MB, TTS models, whisper models, database) |
| GPU | Strongly recommended (8+ GB VRAM) |
| Shared memory | 256 MB+ for PostgreSQL (`shm_size` in Docker) |

### Scaling notes

- **Concurrent sessions**: Each active voice session maintains a WebSocket connection and a VAD pipeline. Budget ~200 MB per concurrent session for audio buffers and inference state.
- **Ollama**: Memory usage scales with model size. The 3B llama3.2 model uses ~2 GB in VRAM (GPU) or ~4 GB in RAM (CPU). Larger models (7B, 13B) require proportionally more.
- **whisper.cpp**: The `base.en` model uses ~150 MB. The `large-v3` model uses ~3 GB. Memory usage is per-inference, not persistent.
- **PostgreSQL**: For campaigns with thousands of session entries, increase `shared_buffers` and `work_mem` from the PostgreSQL defaults.

---

## 🏥 Health Checks

Glyphoxa exposes two health endpoints for load balancer integration and container orchestration.

### Liveness probe: `/healthz`

Always returns `200 OK` when the process is running and serving HTTP:

```bash
curl http://localhost:8080/healthz
```

```json
{"status": "ok"}
```

### Readiness probe: `/readyz`

Returns `200 OK` only when all registered dependency checks pass. Returns `503 Service Unavailable` if any check fails:

```bash
curl http://localhost:8080/readyz
```

```json
{"status": "ok", "checks": {"database": "ok", "providers": "ok"}}
```

Failed check example:

```json
{"status": "fail", "checks": {"database": "fail: connection refused", "providers": "ok"}}
```

Each individual check has a 5-second timeout.

### Kubernetes / Docker configuration

```yaml
# Kubernetes liveness probe
livenessProbe:
  httpGet:
    path: /healthz
    port: 8080
  initialDelaySeconds: 5
  periodSeconds: 10

# Kubernetes readiness probe
readinessProbe:
  httpGet:
    path: /readyz
    port: 8080
  initialDelaySeconds: 10
  periodSeconds: 5
```

```yaml
# Docker Compose healthcheck
glyphoxa:
  healthcheck:
    test: ["CMD", "wget", "--spider", "-q", "http://localhost:8080/healthz"]
    interval: 10s
    timeout: 3s
    retries: 5
```

> **Note:** The release Docker image is based on distroless and does not include `curl` or `wget`. For Docker healthchecks, use a sidecar or the Glyphoxa binary itself as a health probe client if needed. Kubernetes probes work natively via httpGet.

---

## ✅ Production Checklist

### Security hardening

- [ ] Use strong, unique PostgreSQL credentials (not the default `glyphoxa/glyphoxa`)
- [ ] Set `sslmode=require` or `sslmode=verify-full` on the PostgreSQL DSN
- [ ] Enable TLS on Glyphoxa or terminate TLS at a reverse proxy
- [ ] Store API keys and secrets outside the config file (environment variables or a secrets manager)
- [ ] Run the container as non-root (the distroless image uses `nonroot` by default)
- [ ] Restrict network access -- Ollama, Coqui TTS, and PostgreSQL should not be exposed publicly
- [ ] Set a `dm_role_id` in the Discord config to restrict privileged slash commands

### Log configuration

- [ ] Set `server.log_level` to `info` for production (use `debug` only for troubleshooting)
- [ ] Forward logs to a centralised log aggregator (stdout-based; works with Docker logging drivers, Fluentd, Loki)

### Monitoring

- [ ] Configure Prometheus to scrape the `/metrics` endpoint
- [ ] Set up Grafana dashboards (pre-built dashboards in `deployments/compose/grafana/`)
- [ ] Enable the `alpha` Compose profile for built-in Prometheus + Grafana: `docker compose --profile alpha up`
- [ ] Configure alerts for `/readyz` failures and high latency on voice pipeline stages
- [ ] See [observability.md](observability.md) for detailed metrics, tracing, and alerting setup

### Backup & recovery

- [ ] Schedule regular `pg_dump` or configure WAL-based continuous archiving
- [ ] Test restore procedures before going live
- [ ] Back up whisper model files and Ollama models (or document download steps for recovery)

### Performance tuning

- [ ] Tune PostgreSQL `shared_buffers`, `work_mem`, and `effective_cache_size` for your workload
- [ ] For large vector tables (>100k rows), tune the HNSW index parameters (`m`, `ef_construction`)
- [ ] If running Ollama on CPU, consider using a quantised model variant (e.g., `llama3.2:q4_0`)
- [ ] Set Docker `shm_size: 256m` or higher for PostgreSQL

---

## 📖 See Also

- [getting-started.md](getting-started.md) -- developer setup and first run
- [configuration.md](configuration.md) -- full configuration reference
- [observability.md](observability.md) -- metrics, tracing, and alerting
- [troubleshooting.md](troubleshooting.md) -- common issues and solutions
- [deployments/compose/README.md](../deployments/compose/README.md) -- Docker Compose setup details
