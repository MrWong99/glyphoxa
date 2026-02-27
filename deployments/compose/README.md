# Glyphoxa — Docker Compose Deployment

Docker Compose setup for running Glyphoxa with all its dependencies. Supports
two modes: cloud API providers or fully local inference.

## Quick Start (Local Stack)

Run everything locally — no cloud API keys required:

```bash
# Copy the local config (uses Ollama, Whisper.cpp, Coqui TTS)
cp config.local.yaml config.yaml

# Start all services
docker compose --profile local up -d

# Watch logs
docker compose --profile local logs -f
```

On first start, the `ollama-bootstrap` service automatically pulls the required
models (~2GB for llama3.2, ~300MB for nomic-embed-text). This runs once and exits.

## Quick Start (Cloud APIs)

If you prefer cloud providers (OpenAI, Anthropic, ElevenLabs, Deepgram, etc.):

```bash
# Copy the base config and edit with your API keys
cp config.yaml.example config.yaml
# Edit config.yaml with your API keys...

# Start only postgres + glyphoxa (no local inference)
docker compose up -d
```

## Services

| Service | Image | Port | Profile | Description |
|---------|-------|------|---------|-------------|
| **postgres** | `pgvector/pgvector:pg17` | 5432 | default | PostgreSQL with pgvector for embeddings |
| **glyphoxa** | built from source | 8080 | default | Glyphoxa application server |
| **ollama** | `ollama/ollama` | 11434 | `local` | Local LLM inference (llama3.2) |
| **ollama-bootstrap** | `ollama/ollama` | — | `local` | One-shot model puller |
| **whisper** | `ghcr.io/ggml-org/whisper.cpp` | 9000 | `local` | Local speech-to-text (base.en model) |
| **tts** | `ghcr.io/coqui-ai/tts-cpu` | 5002 | `local` | Local text-to-speech (VITS model) |

## GPU Acceleration

### NVIDIA GPU

Ensure the [NVIDIA Container Toolkit](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/)
is installed, then uncomment the `deploy` block in `docker-compose.yml` for the
relevant service:

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

For Whisper.cpp with CUDA, swap the image tag:
```yaml
whisper:
  image: ghcr.io/ggml-org/whisper.cpp:main-cuda
```

## Models

### Ollama (LLM + Embeddings)

The bootstrap service pulls these models on first start:
- **llama3.2** — 3B parameter chat model (~2GB)
- **nomic-embed-text** — embedding model (~300MB)

To use a different model, edit the bootstrap command and `config.yaml`:
```bash
# Pull a different model
docker compose exec ollama ollama pull mistral
```

### Whisper.cpp (STT)

The image includes `ggml-base.en.bin` (147MB, English-only). For other models:
```bash
# Download a larger model into the container
docker compose exec whisper \
  bash /app/models/download-ggml-model.sh large-v3

# Then update the --model flag in docker-compose.yml
```

Available models: tiny, base, small, medium, large-v3

### Coqui TTS

Default model is `tts_models/en/ljspeech/vits` (fast, English, ~50MB download).

For higher quality multilingual synthesis, change the model in `docker-compose.yml`:
```yaml
tts:
  command: ["--model_name", "tts_models/multilingual/multi-dataset/xtts_v2", "--port", "5002"]
```

Note: XTTS v2 requires ~2GB download and is significantly slower on CPU.

## Configuration

### `config.local.yaml` (fully local)

Uses the local services for all providers:
- **LLM**: Ollama (llama3.2) at `http://ollama:11434`
- **STT**: Whisper.cpp at `http://whisper:8080`
- **TTS**: Coqui TTS (standard API) at `http://tts:5002`
- **Embeddings**: Ollama (nomic-embed-text) at `http://ollama:11434`
- **VAD**: Silero (built-in, no container needed)

### Custom Configuration

Create your own `config.yaml` to mix local and cloud providers:

```yaml
providers:
  llm:
    name: ollama                        # local
    base_url: "http://ollama:11434"
    model: llama3.2

  stt:
    name: deepgram                      # cloud
    api_key: "YOUR_DEEPGRAM_KEY"
    model: nova-3

  tts:
    name: elevenlabs                    # cloud
    api_key: "YOUR_ELEVENLABS_KEY"

  embeddings:
    name: ollama                        # local
    base_url: "http://ollama:11434"
    model: nomic-embed-text
```

## Endpoints for Development

Once running, these endpoints are available for testing:

```bash
# Whisper.cpp — transcribe audio
curl http://localhost:9000/inference \
  -F "file=@audio.wav" \
  -F "response_format=json"

# Coqui TTS — synthesize speech
curl "http://localhost:5002/api/tts?text=Hello+world" -o output.wav

# Ollama — chat completion
curl http://localhost:11434/api/chat -d '{
  "model": "llama3.2",
  "messages": [{"role": "user", "content": "Hello"}]
}'

# Ollama — embeddings
curl http://localhost:11434/api/embed -d '{
  "model": "nomic-embed-text",
  "input": "The innkeeper greets you warmly."
}'
```

## Troubleshooting

**Ollama models not downloading:**
```bash
# Check bootstrap logs
docker compose --profile local logs ollama-bootstrap

# Manually pull
docker compose exec ollama ollama pull llama3.2
```

**Whisper connection reset:**
The whisper.cpp image uses `ENTRYPOINT ["bash", "-c"]` — the compose file
overrides this with the explicit binary path. If you modify the whisper
command, use the `entrypoint` + `command` pattern shown in the compose file.

**TTS model download slow:**
Coqui TTS downloads the model on first start (~50MB for VITS, ~2GB for XTTS v2).
The model is cached in the `tts_models` Docker volume.

**Port conflicts:**
If ports 5432, 5002, 9000, or 11434 are in use, change the host-side port
mapping in `docker-compose.yml` (e.g., `"15432:5432"`).

## Cleanup

```bash
# Stop everything
docker compose --profile local down

# Stop and remove volumes (deletes all data including models)
docker compose --profile local down -v
```
