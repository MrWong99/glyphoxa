# Troubleshooting

Common issues and their fixes for the Glyphoxa voice AI framework.

---

## :hammer_and_wrench: Build Issues

### libopus not found

**Symptom**

```
# layeh.com/gopus
cgo: C compiler cannot find -lopus
```

**Cause** -- The `gopus` Opus bindings (used for Discord voice encoding/decoding) require the libopus C library and its development headers. They are not bundled with the Go module.

**Fix**

| Platform | Command |
|---|---|
| Debian / Ubuntu | `sudo apt-get install libopus-dev` |
| Fedora / RHEL | `sudo dnf install opus-devel` |
| Arch Linux | `sudo pacman -S opus` |
| macOS (Homebrew) | `brew install opus` |
| Alpine (Docker) | `apk add opus-dev` |

After installing, re-run:

```bash
make build
```

---

### ONNX Runtime not found (Silero VAD)

**Symptom**

```
error while loading shared libraries: libonnxruntime.so: cannot open shared object file
```

or at build time:

```
ld: library not found for -lonnxruntime
```

**Cause** -- The Silero VAD engine depends on ONNX Runtime for model inference. The dynamic linker cannot find the shared library.

**Fix**

1. Download ONNX Runtime from [the official releases](https://github.com/microsoft/onnxruntime/releases) for your platform.
2. Extract it to a known path (e.g., `/opt/onnxruntime/`).
3. Set the library path environment variable:

```bash
# Linux
export LD_LIBRARY_PATH=/opt/onnxruntime/lib:$LD_LIBRARY_PATH

# macOS
export DYLD_LIBRARY_PATH=/opt/onnxruntime/lib:$DYLD_LIBRARY_PATH
```

4. Optionally, make it persistent by adding the export to your shell profile (`~/.bashrc`, `~/.zshrc`).

---

### whisper.cpp build failures

**Symptom**

```
whisper: load model "...": ...
```

or during build:

```
ld: library not found for -lwhisper
fatal error: whisper.h: No such file or directory
```

**Cause** -- The native whisper.cpp STT provider (`whisper-native`) uses CGO bindings that require the `libwhisper.a` static library and `whisper.h` header at link time. These must be built from source.

**Fix**

Use the provided Makefile target:

```bash
make whisper-libs
```

This clones whisper.cpp, builds the static library, and installs headers + `.a` files to `/tmp/whisper-install/`. Then export the required paths:

```bash
export C_INCLUDE_PATH=/tmp/whisper-install/include
export LIBRARY_PATH=/tmp/whisper-install/lib
export CGO_ENABLED=1
```

Re-run the build:

```bash
make build
```

> **Tip:** If you only use the HTTP-based whisper provider (`whisper` instead of `whisper-native`), you do not need the CGO bindings. You can run a standalone `whisper-server` process and point the provider at it via `base_url`.

---

### CGo compilation errors

**Symptom**

```
cgo: C compiler "gcc" not found: exec: "gcc": executable file not found in $PATH
```

or

```
CGO_ENABLED=0 but package requires CGO
```

**Cause** -- Glyphoxa requires `CGO_ENABLED=1` because the Opus codec, Silero VAD, and whisper-native providers all use C bindings. A C compiler (gcc or clang) must be available.

**Fix**

1. Install a C toolchain:

   | Platform | Command |
   |---|---|
   | Debian / Ubuntu | `sudo apt-get install build-essential` |
   | Fedora / RHEL | `sudo dnf install gcc` |
   | Arch Linux | `sudo pacman -S base-devel` |
   | macOS | `xcode-select --install` |
   | Alpine (Docker) | `apk add gcc musl-dev` |

2. Ensure CGo is enabled:

   ```bash
   export CGO_ENABLED=1
   ```

3. Re-run `make build`.

---

## :electric_plug: Provider Issues

### Deepgram: WebSocket disconnects

**Symptom** -- STT transcription stops mid-session. Logs show:

```
deepgram: dial: ...connection refused
```

or the read loop exits silently and the `Finals()` / `Partials()` channels close unexpectedly.

**Cause** -- The Deepgram WebSocket connection at `wss://api.deepgram.com/v1/listen` was dropped due to a network interruption, idle timeout, or Deepgram-side rate limit.

**Fix**

1. Check your network connectivity to `api.deepgram.com`.
2. Ensure your Deepgram API key is valid and has sufficient quota:
   ```yaml
   providers:
     stt:
       name: deepgram
       api_key: "YOUR_DEEPGRAM_API_KEY"
   ```
3. If using the STT fallback group, the circuit breaker will automatically route to the next healthy provider after 5 consecutive failures (default `MaxFailures`). Check logs for:
   ```
   circuit breaker opened  name=deepgram
   provider failed, trying next  provider=deepgram
   ```
4. If disconnects are frequent, consider adding a local whisper fallback:
   ```yaml
   providers:
     stt:
       name: deepgram
       api_key: "..."
       options:
         fallback: whisper
   ```

---

### Deepgram: authentication failures

**Symptom**

```
deepgram: dial: websocket: bad handshake (HTTP 401)
```

**Cause** -- The API key sent in the `Authorization: Token ...` header is invalid, expired, or missing.

**Fix**

1. Verify the key in your config file under `providers.stt.api_key`.
2. Confirm the key is active in the [Deepgram console](https://console.deepgram.com/).
3. Ensure there are no trailing whitespace or newline characters in the key.

---

### ElevenLabs: rate limiting

**Symptom** -- TTS synthesis fails intermittently. The `ListVoices` call returns:

```
elevenlabs: list voices: unexpected status 429
```

or the WebSocket dial fails with a 429-like rejection.

**Cause** -- Your ElevenLabs plan's concurrent request or character limit has been exceeded.

**Fix**

1. Check your ElevenLabs plan quotas at [elevenlabs.io/app/subscription](https://elevenlabs.io/app/subscription).
2. Reduce the number of NPCs speaking simultaneously.
3. Consider using a local TTS fallback (e.g., Coqui TTS) for lower-priority NPCs.
4. The TTS circuit breaker will automatically trip after 5 consecutive failures and retry after 30 seconds. Monitor with:
   ```
   circuit breaker opened  name=elevenlabs
   ```

---

### ElevenLabs: voice ID not found

**Symptom**

```
elevenlabs: voice.ID must not be empty
```

or the WebSocket connection opens but returns no audio (the voice ID path segment in the URL is invalid).

**Cause** -- The `voice_id` in your NPC's voice config is empty, incorrect, or refers to a voice not available on your ElevenLabs account.

**Fix**

1. List available voices by calling the API or via the ElevenLabs dashboard:
   ```bash
   curl -H "xi-api-key: YOUR_KEY" https://api.elevenlabs.io/v1/voices | jq '.voices[].voice_id'
   ```
2. Update your config:
   ```yaml
   npcs:
     - name: "Greymantle the Sage"
       voice:
         provider: elevenlabs
         voice_id: "pNInz6obpgDQGcFmaJgB"  # a valid voice ID
   ```

---

### Ollama: model not loaded / connection refused

**Symptom**

```
anyllm: completion: Post "http://localhost:11434/...": dial tcp 127.0.0.1:11434: connect: connection refused
```

or

```
anyllm: completion: model "llama3" not found
```

**Cause** -- The Ollama daemon is not running, or the requested model has not been pulled.

**Fix**

1. Start the Ollama daemon:
   ```bash
   ollama serve
   ```
2. Pull the model:
   ```bash
   ollama pull llama3
   ```
3. If Ollama runs on a non-default address, set `base_url` in your config:
   ```yaml
   providers:
     llm:
       name: ollama
       model: "llama3"
       base_url: "http://my-ollama-host:11434"
   ```

---

### whisper.cpp: library not found at runtime

**Symptom**

```
whisper: load model "/path/to/ggml-base.bin": dlopen: libwhisper.so: cannot open shared object file
```

**Cause** -- The whisper shared library is not on the dynamic linker's search path at runtime. This affects the `whisper-native` provider only.

**Fix**

Set the library path before starting Glyphoxa:

```bash
export LD_LIBRARY_PATH=/tmp/whisper-install/lib:$LD_LIBRARY_PATH  # Linux
export DYLD_LIBRARY_PATH=/tmp/whisper-install/lib:$DYLD_LIBRARY_PATH  # macOS
```

If using the HTTP-based whisper provider instead, ensure your whisper-server process is running:

```bash
# Start whisper-server separately
./whisper-server --model /path/to/ggml-base.bin --port 8080
```

Then configure the provider:

```yaml
providers:
  stt:
    name: whisper
    base_url: "http://localhost:8080"
```

---

### OpenAI / Anthropic: API key issues

**Symptom**

```
anyllm: create "openai" backend: ...
anyllm: completion: 401 Unauthorized
```

**Cause** -- The API key is missing, invalid, or not provided via the config file or environment variable.

**Fix**

Provide the key in config:

```yaml
providers:
  llm:
    name: openai
    api_key: "sk-..."
    model: "gpt-4o"
```

Or via the corresponding environment variable:

| Provider | Environment Variable |
|---|---|
| OpenAI | `OPENAI_API_KEY` |
| Anthropic | `ANTHROPIC_API_KEY` |
| Gemini | `GEMINI_API_KEY` or `GOOGLE_API_KEY` |
| DeepSeek | `DEEPSEEK_API_KEY` |
| Mistral | `MISTRAL_API_KEY` |
| Groq | `GROQ_API_KEY` |

---

### OpenAI / Anthropic: model not available

**Symptom**

```
anyllm: completion: 404 model "gpt-5-turbo" not found
```

**Cause** -- The model name in your config does not match an available model on the provider's API.

**Fix**

Double-check the model name against the provider's documentation. Common valid values:

| Provider | Example models |
|---|---|
| OpenAI | `gpt-4o`, `gpt-4o-mini`, `gpt-4-turbo`, `o1`, `o3-mini` |
| Anthropic | `claude-3-5-sonnet-latest`, `claude-3-5-haiku-latest`, `claude-3-opus-20240229` |
| Gemini | `gemini-2.0-flash`, `gemini-1.5-pro`, `gemini-1.5-flash` |
| Ollama | Any locally pulled model (run `ollama list`) |

---

## :gear: Runtime Issues

### NPC not responding to speech

**Symptom** -- You speak in the Discord voice channel but the NPC never responds.

**Cause** -- This can stem from several pipeline stages. Work through them in order:

| Stage | Diagnostic | Likely cause |
|---|---|---|
| **Audio input** | Check that the bot is receiving Opus packets (log `discord: opus decode error` would indicate packets are arriving) | Bot is deafened, or not in the voice channel |
| **VAD** | Check for `VADSpeechStart` / `VADSpeechEnd` events in debug logs | VAD thresholds too high, wrong sample rate, or ONNX Runtime missing |
| **STT** | Check for `deepgram: dial:` or `whisper: http request:` errors | STT provider misconfigured or unreachable |
| **Address detection** | Enable debug logging; look for NPC name matching | Player did not address the NPC by name or the NPC name is not in the STT keyword list |
| **LLM** | Check for `anyllm: completion:` errors | LLM provider unreachable, API key invalid, or model not available |
| **TTS** | Check for `elevenlabs: dial:` errors | TTS provider unreachable or voice ID invalid |

**Fix**

1. Set `log_level: debug` in your config to see full pipeline trace.
2. Check the `/readyz` endpoint to verify all dependencies are healthy.
3. Verify the NPC is not muted (check the session dashboard or use the `/npc` slash command).

---

### High latency

**Symptom** -- The NPC responds but with a noticeable delay (> 2 seconds).

**Cause** -- One or more pipeline stages are slow. The cascaded pipeline runs STT -> LLM -> TTS sequentially, so latencies compound.

**Fix**

1. Check the session dashboard embed in Discord -- it shows per-stage p50/p95 latencies:
   ```
   STT: p50=120.0ms p95=340.0ms
   LLM: p50=800.0ms p95=1500.0ms
   TTS: p50=200.0ms p95=450.0ms
   ```
2. Identify the bottleneck stage and address it:

   | Stage | Fix |
   |---|---|
   | **STT** | Use Deepgram (streaming, low latency) over whisper.cpp (batch). Ensure the `model` is set to `nova-3` for fastest results. |
   | **LLM** | Use a faster model (`gpt-4o-mini`, `claude-3-5-haiku-latest`, or a local Ollama model). Consider the `sentence_cascade` engine for perceived latency reduction. |
   | **TTS** | Use `eleven_flash_v2_5` (the default ElevenLabs model). Reduce `speed_factor` if set above 1.0. |

3. Check Prometheus metrics at `/metrics` for `glyphoxa_pipeline_*` histograms if enabled.
4. For the `sentence_cascade` engine, ensure `cascade_mode` is set to `auto` or `always`:
   ```yaml
   npcs:
     - name: "Greymantle"
       engine: sentence_cascade
       cascade_mode: auto
       cascade:
         fast_model: "gpt-4o-mini"
         strong_model: "gpt-4o"
   ```

---

### Memory not populating

**Symptom** -- The session dashboard shows `Memory Entries: 0` even after extended conversation. NPC responses lack context from previous sessions.

**Cause** -- The PostgreSQL memory store is not connected, the `pgvector` extension is not installed, or `embedding_dimensions` is misconfigured.

**Fix**

1. Verify `memory.postgres_dsn` is set in your config:
   ```yaml
   memory:
     postgres_dsn: "postgres://user:pass@localhost:5432/glyphoxa?sslmode=disable"
     embedding_dimensions: 1536
   ```
2. Check that PostgreSQL is reachable:
   ```bash
   psql "postgres://user:pass@localhost:5432/glyphoxa" -c "SELECT 1;"
   ```
3. Check that the `pgvector` extension is available:
   ```bash
   psql "postgres://user:pass@localhost:5432/glyphoxa" -c "CREATE EXTENSION IF NOT EXISTS vector;"
   ```
   If this fails, install pgvector for your PostgreSQL version. See the [pgvector installation guide](https://github.com/pgvector/pgvector#installation).
4. Ensure `embedding_dimensions` matches your embedding model:

   | Embedding provider/model | Dimensions |
   |---|---|
   | OpenAI `text-embedding-3-small` | 1536 |
   | OpenAI `text-embedding-3-large` | 3072 |
   | Ollama `nomic-embed-text` | 768 |

5. Check for migration errors in the startup logs:
   ```
   postgres store: migrate: ...
   postgres store: ping: ...
   ```

---

### Transcript correction not working

**Symptom** -- NPC and place names are consistently misspelled in transcripts (e.g., "Eldrinax" becomes "Elder Next").

**Cause** -- STT keyword boosting is not configured, or the provider does not support mid-stream keyword updates.

**Fix**

1. For Deepgram, keywords are passed as URL query parameters at stream start. Ensure your NPC and entity names are included in the STT `StreamConfig.Keywords` via the orchestrator.
2. Note that both Deepgram and whisper.cpp do **not** support mid-session keyword updates. Keywords set after stream start will return:
   ```
   deepgram: mid-session keyword updates are not supported
   whisper: keyword boosting is not supported by whisper.cpp
   ```
   Keywords must be provided before starting the STT stream.
3. Consider adding campaign entity names to the keyword list via the `/entity` slash command so they are available at session start.

---

## :speech_balloon: Discord Issues

### Bot not connecting

**Symptom**

```
discord: create session: ...
discord: open session: ...
```

**Cause** -- The Discord bot token is invalid, or the bot lacks required gateway intents.

**Fix**

1. Verify `discord.token` in your config file. The token should not include the `Bot ` prefix (Glyphoxa adds it automatically).
   ```yaml
   discord:
     token: "MTIz..."  # just the token, no "Bot " prefix
     guild_id: "123456789"
   ```
2. In the [Discord Developer Portal](https://discord.com/developers/applications), ensure these **Privileged Gateway Intents** are enabled for your bot:
   - **Server Members Intent** (for role-based DM permission checks)
   - **Message Content Intent** (if using text-based commands)
3. Verify the required intents match the code:
   ```
   IntentsGuildMessages | IntentsGuildVoiceStates | IntentsGuilds
   ```

---

### Slash commands not appearing

**Symptom** -- After starting the bot, the `/session`, `/npc`, `/entity`, and other commands do not appear in Discord.

**Cause** -- Guild-scoped command registration can take up to a few minutes. If commands never appear, the `guild_id` may be wrong or the bot lacks the `applications.commands` scope.

**Fix**

1. Double-check `discord.guild_id` matches your target server. You can copy the guild ID by right-clicking the server name in Discord (Developer Mode must be enabled).
2. Verify the bot was invited with the `applications.commands` OAuth2 scope. The invite URL should include:
   ```
   &scope=bot+applications.commands
   ```
3. Check the logs for registration errors:
   ```
   discord: register commands: ...
   ```
   A successful registration logs:
   ```
   discord commands registered  count=N
   ```
4. Guild-scoped commands should appear within a few seconds; global commands can take up to an hour. Glyphoxa uses guild-scoped registration by default.

---

### Voice channel issues

**Symptom** -- The bot joins the voice channel but no audio is received or sent. Players hear silence.

**Cause** -- The bot may lack voice permissions, or the Opus codec layer is failing.

**Fix**

1. Ensure the bot has these permissions in the target voice channel:
   - **Connect**
   - **Speak**
   - **Use Voice Activity** (not Push-to-Talk only)
2. Check for Opus codec errors in logs:
   ```
   discord: failed to create opus decoder  ssrc=...
   discord: opus decode error  ssrc=...
   discord: failed to create opus encoder
   ```
   These indicate a libopus installation issue. See the [libopus not found](#libopus-not-found) section above.
3. Verify the bot is not self-deafened. The platform connects with `mute=false, deaf=false`.
4. If audio frames are being dropped, you will see `Channel full -- drop frame rather than block` behavior silently. Increase system resources if under heavy load.

---

### DM permissions not working

**Symptom** -- All users can execute privileged commands, or no users can (even the DM).

**Cause** -- The `dm_role_id` is misconfigured.

**Fix**

1. If `dm_role_id` is empty, **all users are treated as DMs** (development mode). Set it to restrict access:
   ```yaml
   discord:
     dm_role_id: "987654321012345678"
   ```
2. The role ID must be a Discord role in the target guild. Copy it from Server Settings > Roles (right-click the role with Developer Mode enabled).
3. Interactions from DM channels (outside guilds) always return `IsDM = false` because there is no `Member` object. Privileged commands must be run inside the guild.

---

## :page_facing_up: Configuration Issues

### Hot-reload not picking up changes

**Symptom** -- You edit `glyphoxa.yaml` but the running instance does not reflect the changes.

**Cause** -- The config watcher uses **polling** (not filesystem events). It checks the file's modification time every 5 seconds by default. If the mtime has not changed (e.g., some editors write atomically to a temp file then rename), the watcher may not detect the change.

**Fix**

1. Check the logs for watcher activity:
   ```
   config watcher: configuration reloaded  path=glyphoxa.yaml
   ```
   If you see `config watcher: failed to load config`, the file has a YAML syntax error. Fix the syntax and the watcher will pick up the next valid version.
2. The watcher computes a SHA-256 hash of the file contents. If the file was touched but the content is identical, no reload occurs. This is by design.
3. Only certain fields support hot-reload without restart:
   - NPC personality, voice, and budget tier changes
   - Log level changes
4. Changes to **providers**, **discord**, **memory**, or **server** settings require a full restart.
5. If your editor writes to a temp file then renames, ensure the final file path matches the one Glyphoxa is watching.

---

### Invalid provider options

**Symptom**

```
config: provider not registered: llm/"openaii"
```

or a startup warning:

```
unknown provider name -- may be a typo or third-party provider  kind=llm  name=openaii
```

**Cause** -- The `name` field in a provider entry does not match any registered provider.

**Fix**

Use one of the valid provider names:

| Kind | Valid names |
|---|---|
| `llm` | `openai`, `anthropic`, `ollama`, `gemini`, `deepseek`, `mistral`, `groq`, `llamacpp`, `llamafile` |
| `stt` | `deepgram`, `whisper`, `whisper-native` |
| `tts` | `elevenlabs`, `coqui` |
| `s2s` | `openai-realtime`, `gemini-live` |
| `embeddings` | `openai`, `ollama` |
| `vad` | `silero` |
| `audio` | `discord` |

---

### Missing required fields

**Symptom** -- Startup fails with one or more validation errors:

```
npcs[0].name is required
npcs[0]: engine "cascaded" requires an LLM provider but providers.llm is not configured
npcs[0]: engine "cascaded" requires a TTS provider but providers.tts is not configured
npcs[0]: engine "s2s" requires an S2S provider but providers.s2s is not configured
mcp.servers[0].name is required
mcp.servers[0].command is required when transport is stdio
```

**Cause** -- The config validator checks for cross-field consistency. Engine mode dictates which providers are required.

**Fix**

| Engine | Required providers |
|---|---|
| `cascaded` | `llm` + `tts` (and `stt` for speech input) |
| `sentence_cascade` | `llm` + `tts` |
| `s2s` | `s2s` |

Additional rules:

- Every NPC must have a non-empty `name`.
- NPC names must be unique (duplicates are rejected).
- `voice.speed_factor` must be in `[0.5, 2.0]`.
- `voice.pitch_shift` must be in `[-10, 10]`.
- `server.log_level` must be one of `debug`, `info`, `warn`, `error` (or empty for default).
- MCP servers with transport `stdio` require a `command`; `streamable-http` requires a `url`.

---

### Unknown fields in config

**Symptom**

```
config: decode yaml: line 12: field foo_bar not found in type config.Config
```

**Cause** -- The YAML decoder is configured with `KnownFields(true)`, which rejects unrecognised keys. This catches typos early.

**Fix**

Remove or correct the unrecognised field. Check the field name against the struct tags in `internal/config/config.go`. Common mistakes:

| Wrong | Correct |
|---|---|
| `api-key` | `api_key` |
| `guildId` | `guild_id` |
| `listenAddr` | `listen_addr` |
| `postgresdsn` | `postgres_dsn` |

---

## :mag: Diagnostic Steps

### Health endpoint check

Glyphoxa exposes two health endpoints:

| Endpoint | Purpose |
|---|---|
| `GET /healthz` | Liveness probe. Always returns `200 OK` if the process is running. |
| `GET /readyz` | Readiness probe. Returns `200` only when all registered health checks pass. |

```bash
# Liveness
curl -s http://localhost:8080/healthz | jq .
# {"status":"ok"}

# Readiness (includes per-check results)
curl -s http://localhost:8080/readyz | jq .
# {
#   "status": "ok",
#   "checks": {
#     "database": "ok",
#     "providers": "ok"
#   }
# }
```

If `/readyz` returns `503 Service Unavailable`, inspect the `checks` map for the failing component:

```json
{
  "status": "fail",
  "checks": {
    "database": "fail: postgres store: ping: connection refused",
    "providers": "ok"
  }
}
```

Each individual check has a 5-second timeout. If a check consistently times out, the underlying dependency is likely unreachable.

---

### Log analysis

Set the log level to `debug` for maximum visibility:

```yaml
server:
  log_level: debug
```

Key log patterns to look for:

| Pattern | Meaning |
|---|---|
| `circuit breaker opened name=...` | A provider has failed 5+ times and is being bypassed |
| `circuit breaker transitioning to half-open` | The breaker is testing whether the provider has recovered |
| `circuit breaker closed after successful probes` | The provider has recovered |
| `provider failed, trying next provider=... error=...` | A fallback group is trying the next healthy provider |
| `all providers failed` | Every provider in a fallback group has failed or has an open circuit |
| `config watcher: configuration reloaded` | Hot-reload detected and applied a config change |
| `config watcher: failed to load config` | Hot-reload detected a change but the new config is invalid |
| `discord: register commands` | Slash command registration outcome |
| `whisper native inference failed` | whisper.cpp CGO inference error |
| `voicecmd: command executed` | A DM voice command was recognised and executed |

---

### Prometheus metrics inspection

If you have Prometheus scraping enabled, key metrics to inspect:

```promql
# Pipeline latency by stage (p95 over 5 minutes)
histogram_quantile(0.95, rate(glyphoxa_pipeline_duration_seconds_bucket[5m]))

# Error rate
rate(glyphoxa_pipeline_errors_total[5m])

# Circuit breaker state (1 = open, 0 = closed)
glyphoxa_circuit_breaker_state{name="deepgram"}

# STT / LLM / TTS individual stage latencies
histogram_quantile(0.95, rate(glyphoxa_stt_duration_seconds_bucket[5m]))
histogram_quantile(0.95, rate(glyphoxa_llm_duration_seconds_bucket[5m]))
histogram_quantile(0.95, rate(glyphoxa_tts_duration_seconds_bucket[5m]))
```

---

### Common SQL queries for memory debugging

Connect to your PostgreSQL database and run these queries to diagnose memory issues:

```sql
-- Check if tables were created
SELECT tablename FROM pg_tables WHERE schemaname = 'public';

-- Verify pgvector extension
SELECT * FROM pg_extension WHERE extname = 'vector';

-- Count session log entries
SELECT session_id, COUNT(*) AS entries
FROM session_entries
GROUP BY session_id
ORDER BY entries DESC;

-- Recent session entries (last 20)
SELECT id, session_id, speaker_name, LEFT(text, 80) AS text_preview, timestamp
FROM session_entries
ORDER BY timestamp DESC
LIMIT 20;

-- Count semantic chunks
SELECT COUNT(*) FROM chunks;

-- Check if embeddings are populated (NULL = missing embeddings)
SELECT id, session_id, LEFT(content, 60) AS content_preview,
       (embedding IS NOT NULL) AS has_embedding
FROM chunks
ORDER BY timestamp DESC
LIMIT 20;

-- Count entities in the knowledge graph
SELECT type, COUNT(*) FROM entities GROUP BY type;

-- List all relationships
SELECT source_id, rel_type, target_id
FROM relationships
ORDER BY created_at DESC
LIMIT 20;

-- Search session log (full-text)
SELECT id, speaker_name, text, timestamp
FROM session_entries
WHERE to_tsvector('english', text) @@ plainto_tsquery('english', 'blacksmith sword')
ORDER BY timestamp DESC
LIMIT 10;

-- Find nearest semantic chunks (requires a pre-computed embedding vector)
-- Replace the vector literal with an actual embedding:
-- SELECT id, content, embedding <=> '[0.1, 0.2, ...]'::vector AS distance
-- FROM chunks
-- ORDER BY distance
-- LIMIT 5;
```

---

## See also

- [getting-started.md](getting-started.md) -- Installation, first run, and basic configuration
- `configuration.md` -- Full configuration reference
- `observability.md` -- Prometheus metrics, logging, and dashboard details
- `deployment.md` -- Docker, systemd, and production deployment guides
