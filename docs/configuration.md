# :gear: Configuration Reference

Glyphoxa is configured through a single YAML file. This document is the
authoritative reference for every configuration field, provider option, and
runtime behaviour related to configuration.

---

## :open_book: Overview

### Config file format

Glyphoxa reads a **YAML** configuration file at startup. The YAML decoder
operates in **strict mode** (`KnownFields(true)`) -- any unrecognised key in the
file causes a hard parse error. This catches typos early.

### Specifying the config file

Pass the path via the `-config` CLI flag:

```bash
glyphoxa -config /etc/glyphoxa/config.yaml
```

The default is `config.yaml` in the working directory.

### Environment variable fallbacks

Glyphoxa itself does not read environment variables for configuration overrides.
However, several **provider SDKs** honour their own environment variables when no
`api_key` is set in the config file:

| Provider | Environment Variable |
|---|---|
| `openai` (LLM, embeddings) | `OPENAI_API_KEY` |
| `anthropic` | `ANTHROPIC_API_KEY` |
| `gemini` | `GEMINI_API_KEY` / `GOOGLE_API_KEY` |
| `deepseek` | `DEEPSEEK_API_KEY` |
| `mistral` | `MISTRAL_API_KEY` |
| `groq` | `GROQ_API_KEY` |

These are read by the upstream SDKs, not by Glyphoxa's config loader. If you set
`api_key` explicitly in the YAML, it takes precedence.

---

## :arrows_counterclockwise: Hot Reload

The configuration file can be edited while Glyphoxa is running. A background
**config watcher** detects changes and applies them without a restart.

### How it works

1. The watcher **polls** the config file (no `fsnotify` dependency).
2. Default polling interval: **5 seconds**.
3. On each tick the file's **mtime** is checked first (cheap). If mtime has not
   changed, no further work is done.
4. If mtime changed, the file is read and its **SHA-256 hash** is compared to
   the previously loaded config. Touching the file without changing content is a
   no-op.
5. The new file is fully **parsed and validated** before it replaces the old
   config. If validation fails, the old config is retained and a warning is
   logged.
6. When a valid change is detected, the `onChange` callback receives both the old
   and new `Config` values.

### What can be hot-reloaded

The `Diff` function tracks which changes are safe to apply at runtime:

| Change | Hot-reloaded? | Notes |
|---|---|---|
| `server.log_level` | :white_check_mark: Yes | Takes effect immediately |
| NPC `personality` | :white_check_mark: Yes | System prompt updated on next interaction |
| NPC `voice` (provider, voice_id, pitch, speed) | :white_check_mark: Yes | Applied to new TTS sessions |
| NPC `budget_tier` | :white_check_mark: Yes | Changes tool filtering immediately |
| Adding a new NPC | :white_check_mark: Yes | NPC becomes available without restart |
| Removing an NPC | :white_check_mark: Yes | NPC is unloaded |
| Provider changes (api_key, model, etc.) | :x: No | Requires restart |
| `server.listen_addr` / `server.tls` | :x: No | Requires restart |
| `discord.*` | :x: No | Requires restart |
| `memory.*` | :x: No | Requires restart |
| `mcp.servers` | :x: No | Requires restart |
| `campaign.*` | :x: No | Requires restart |

---

## :card_index_dividers: Complete Field Reference

### `server` -- Server Settings

| Field | Type | Default | Description |
|---|---|---|---|
| `server.listen_addr` | `string` | `""` | TCP address to listen on (e.g., `":8080"`). Empty means the server does not bind an HTTP listener. |
| `server.log_level` | `string` | `"info"` | Log verbosity. Valid values: `debug`, `info`, `warn`, `error`. Hot-reloadable. |
| `server.tls` | `object` | `null` | TLS configuration block. When omitted or `null`, the server runs plain HTTP. |
| `server.tls.cert_file` | `string` | -- | Path to PEM-encoded TLS certificate. Required if `tls` is set. |
| `server.tls.key_file` | `string` | -- | Path to PEM-encoded TLS private key. Required if `tls` is set. |

```yaml
server:
  listen_addr: ":8080"
  log_level: info
  tls:
    cert_file: /etc/ssl/glyphoxa.crt
    key_file: /etc/ssl/glyphoxa.key
```

---

### `discord` -- Discord Bot

| Field | Type | Default | Description |
|---|---|---|---|
| `discord.token` | `string` | `""` | Discord bot token (e.g., `"Bot MTIz..."`). Leave empty to disable the Discord bot entirely. |
| `discord.guild_id` | `string` | `""` | Target Discord guild (server) ID. Alpha deployments support one guild per bot instance. |
| `discord.dm_role_id` | `string` | `""` | Discord role ID for Dungeon Master permissions. Users with this role can execute `/session`, `/npc`, `/entity`, and `/campaign` commands. When empty, **all users are treated as DMs** (useful for development). |

```yaml
discord:
  token: "Bot MTIz..."
  guild_id: "123456789012345678"
  dm_role_id: "987654321098765432"
```

---

### `providers` -- AI Provider Configuration

Each provider slot follows the same `ProviderEntry` schema:

| Field | Type | Default | Description |
|---|---|---|---|
| `name` | `string` | `""` | Registered provider implementation name (e.g., `"openai"`, `"deepgram"`). Leave empty to disable the pipeline stage. |
| `api_key` | `string` | `""` | Authentication key for the provider's API. Some providers fall back to environment variables when empty. |
| `base_url` | `string` | `""` | Override the provider's default API endpoint. Leave empty to use the built-in default. |
| `model` | `string` | `""` | Model name within the provider (e.g., `"gpt-4o"`, `"nova-3"`). |
| `options` | `map[string]any` | `{}` | Provider-specific settings not covered by the standard fields. See [Provider-Specific Options](#provider-specific-options) below. |

#### `providers.llm` -- Large Language Model

Used for NPC reasoning in `cascaded` and `sentence_cascade` engine modes.

**Registered providers:** `openai`, `anthropic`, `ollama`, `gemini`, `deepseek`, `mistral`, `groq`, `llamacpp`, `llamafile`

```yaml
providers:
  llm:
    name: openai
    api_key: sk-...
    model: gpt-4o
    options:
      max_tokens: 1024
```

#### `providers.stt` -- Speech-to-Text

Transcribes player audio in `cascaded` engine mode.

**Registered providers:** `deepgram`, `whisper`, `whisper-native`

```yaml
providers:
  stt:
    name: deepgram
    api_key: dg-...
    model: nova-3
    options:
      language: en-US
```

#### `providers.tts` -- Text-to-Speech

Synthesises NPC voice responses in `cascaded` engine mode.

**Registered providers:** `elevenlabs`, `coqui`

```yaml
providers:
  tts:
    name: elevenlabs
    api_key: el-...
    model: eleven_multilingual_v2
    options:
      output_format: pcm_48000
```

#### `providers.s2s` -- Speech-to-Speech

End-to-end voice model that replaces the STT + LLM + TTS pipeline when an NPC
uses `engine: s2s`.

**Registered providers:** `openai-realtime`, `gemini-live`

```yaml
providers:
  s2s:
    name: openai-realtime
    api_key: sk-...
    model: gpt-4o-realtime-preview
```

#### `providers.embeddings` -- Embedding Model

Used by the memory layer for semantic retrieval (pgvector).

**Registered providers:** `openai`, `ollama`

```yaml
providers:
  embeddings:
    name: openai
    api_key: sk-...
    model: text-embedding-3-small
```

#### `providers.vad` -- Voice Activity Detection

Determines when a player is speaking. Runs locally, no API key required.

**Registered providers:** `silero`

```yaml
providers:
  vad:
    name: silero
    options:
      frame_size_ms: 30
      speech_threshold: 0.5
      silence_threshold: 0.35
```

#### `providers.audio` -- Audio Platform

Connects Glyphoxa to a voice channel. When Discord is enabled, this is
automatically provided by the Discord bot and does not need explicit
configuration.

**Registered providers:** `discord`

```yaml
providers:
  audio:
    name: discord
    options:
      guild_id: "123456789012345678"
```

---

### `npcs` -- NPC Definitions

An array of NPC configurations. Each entry describes a single NPC's personality,
voice, engine mode, and tool access.

| Field | Type | Default | Description |
|---|---|---|---|
| `name` | `string` | -- | **Required.** The NPC's in-world display name (e.g., `"Greymantle the Sage"`). Must be unique. |
| `personality` | `string` | `""` | Free-text persona description injected into the LLM system prompt. Supports multi-line YAML. Hot-reloadable. |
| `voice` | `object` | -- | TTS voice profile for this NPC. See sub-fields below. Hot-reloadable. |
| `voice.provider` | `string` | `""` | TTS provider name (e.g., `"elevenlabs"`, `"coqui"`). Should match `providers.tts.name`. |
| `voice.voice_id` | `string` | `""` | Provider-specific voice identifier. |
| `voice.pitch_shift` | `float` | `0` | Pitch adjustment in the range `[-10, +10]`. `0` means default. |
| `voice.speed_factor` | `float` | `0` | Speaking rate in the range `[0.5, 2.0]`. `1.0` means default; `0` means use provider default. |
| `engine` | `string` | `""` | Conversation pipeline mode. Valid values: `cascaded` (STT + LLM + TTS), `s2s` (end-to-end speech model), `sentence_cascade` (experimental dual-model). |
| `knowledge_scope` | `[]string` | `[]` | Topic domains the NPC is knowledgeable about. Used for routing player questions and building retrieval queries. |
| `tools` | `[]string` | `[]` | MCP tool names this NPC is permitted to invoke. |
| `budget_tier` | `string` | `""` | Constrains which MCP tools are offered based on latency. Valid values: `fast` (<=500ms), `standard` (<=1500ms), `deep` (all tools). Hot-reloadable. |
| `cascade_mode` | `string` | `"off"` | Controls the dual-model sentence cascade. Only effective when `engine` is `sentence_cascade`. Valid values: `off`, `auto`, `always`. |
| `cascade` | `object` | `null` | Sentence cascade engine settings. Only used when `engine` is `sentence_cascade`. |
| `cascade.fast_model` | `string` | `""` | Model for generating the opener sentence (fast, small model). Uses default LLM provider if empty. |
| `cascade.strong_model` | `string` | `""` | Model for generating the substantive continuation (large model). Uses default LLM provider if empty. |
| `cascade.opener_instruction` | `string` | `""` | Appended to the fast model's system prompt. Uses a built-in instruction if empty. |

```yaml
npcs:
  - name: Greymantle the Sage
    personality: |
      You are Greymantle, an ancient and enigmatic wizard...
    voice:
      provider: elevenlabs
      voice_id: pNInz6obpgDQGcFmaJgB
      pitch_shift: -2.0
      speed_factor: 0.85
    engine: cascaded
    budget_tier: standard
    knowledge_scope:
      - ancient history
      - arcane magic
    tools:
      - lookup_spell
      - query_lore_database
```

#### Engine Cross-Validation

The config validator enforces that the required providers are configured for each
engine mode:

| Engine | Required Providers |
|---|---|
| `cascaded` | `providers.llm`, `providers.tts` |
| `sentence_cascade` | `providers.llm`, `providers.tts` |
| `s2s` | `providers.s2s` |

---

### `memory` -- Long-Term Memory

| Field | Type | Default | Description |
|---|---|---|---|
| `memory.postgres_dsn` | `string` | `""` | PostgreSQL connection string for the pgvector memory store. Example: `"postgres://user:pass@localhost:5432/glyphoxa?sslmode=disable"`. When empty, long-term memory is unavailable. |
| `memory.embedding_dimensions` | `int` | `0` | Vector dimension for the embeddings column. Must match the model configured in `providers.embeddings`. Common values: `1536` (text-embedding-3-small), `3072` (text-embedding-3-large), `768` (nomic-embed-text). Defaults to `1536` if embeddings are configured but this field is unset. |

```yaml
memory:
  postgres_dsn: postgres://glyphoxa:secret@localhost:5432/glyphoxa?sslmode=disable
  embedding_dimensions: 1536
```

---

### `mcp` -- Model Context Protocol Tool Servers

| Field | Type | Default | Description |
|---|---|---|---|
| `mcp.servers` | `[]object` | `[]` | List of MCP servers to connect to. |

Each server entry:

| Field | Type | Default | Description |
|---|---|---|---|
| `name` | `string` | -- | **Required.** Unique human-readable identifier (used in logs). |
| `transport` | `string` | `""` | Connection mechanism. Valid values: `stdio`, `streamable-http`. |
| `command` | `string` | `""` | Executable (with arguments) for `stdio` transport. **Required** when `transport` is `stdio`. Ignored for `streamable-http`. |
| `url` | `string` | `""` | MCP endpoint URL for `streamable-http` transport (e.g., `"https://mcp.example.com/mcp"`). **Required** when `transport` is `streamable-http`. Ignored for `stdio`. |
| `env` | `map[string]string` | `{}` | Environment variables injected into the subprocess for `stdio` transport. |
| `auth` | `object` | `null` | Authentication for `streamable-http` servers. Ignored for `stdio`. |
| `auth.token` | `string` | `""` | Static Bearer token sent in the `Authorization` header. Mutually exclusive with `auth.oauth`. |
| `auth.oauth` | `object` | `null` | OAuth 2.1 client-credentials configuration. When set, `auth.token` is ignored. |
| `auth.oauth.client_id` | `string` | `""` | OAuth 2.1 client identifier. |
| `auth.oauth.client_secret` | `string` | `""` | OAuth 2.1 client secret. |
| `auth.oauth.token_url` | `string` | `""` | Authorization server's token endpoint. |
| `auth.oauth.scopes` | `[]string` | `[]` | OAuth scopes to request. |

```yaml
mcp:
  servers:
    # Stdio transport -- Glyphoxa spawns the process
    - name: local-tools
      transport: stdio
      command: /usr/local/bin/mcp-tools --config /etc/mcp-tools.json
      env:
        MCP_LOG_LEVEL: info

    # Streamable HTTP transport with static token auth
    - name: web-search
      transport: streamable-http
      url: https://mcp.example.com/search
      auth:
        token: "Bearer sk-mcp-..."

    # Streamable HTTP transport with OAuth 2.1
    - name: enterprise-tools
      transport: streamable-http
      url: https://mcp.corp.example.com/mcp
      auth:
        oauth:
          client_id: glyphoxa
          client_secret: super-secret
          token_url: https://auth.corp.example.com/oauth/token
          scopes:
            - mcp:tools
            - mcp:read
```

---

### `campaign` -- Campaign Data

| Field | Type | Default | Description |
|---|---|---|---|
| `campaign.name` | `string` | `""` | Campaign's human-readable name (e.g., `"Curse of Strahd"`). |
| `campaign.system` | `string` | `""` | Game system identifier (e.g., `"dnd5e"`, `"pf2e"`). |
| `campaign.entity_files` | `[]string` | `[]` | Paths to YAML files containing entity definitions loaded at startup. Paths are resolved **relative to the config file's directory**. |
| `campaign.vtt_imports` | `[]object` | `[]` | VTT export files to import at startup. |
| `campaign.vtt_imports[].path` | `string` | -- | Filesystem path to the VTT export file. |
| `campaign.vtt_imports[].format` | `string` | -- | VTT platform. Supported values: `"foundry"`, `"roll20"`. |

```yaml
campaign:
  name: Curse of Strahd
  system: dnd5e
  entity_files:
    - entities/npcs.yaml
    - entities/locations.yaml
  vtt_imports:
    - path: exports/foundry-actors.json
      format: foundry
```

---

## :jigsaw: Provider-Specific Options

The `options` map in each provider entry accepts provider-specific keys. These
are consumed by the provider factory functions at startup.

### LLM Providers

All LLM providers (`openai`, `anthropic`, `gemini`, `ollama`, `deepseek`,
`mistral`, `groq`, `llamacpp`, `llamafile`) use the standard `api_key`,
`base_url`, and `model` fields. The `options` map is passed through to the
underlying `any-llm-go` library but has no Glyphoxa-specific keys at this time.

| Option Key | Type | Default | Description |
|---|---|---|---|
| `max_tokens` | `int` | provider default | Maximum tokens in the completion response. Forwarded via the completion request, not the provider constructor. |

### STT: `deepgram`

| Option Key | Type | Default | Description |
|---|---|---|---|
| `language` | `string` | `"en"` | BCP-47 language code (e.g., `"en-US"`, `"de-DE"`). |

The `model` field sets the Deepgram model (default: `"nova-3"`).

### STT: `whisper`

Connects to a running **whisper.cpp HTTP server** (`whisper-server`).

| Option Key | Type | Default | Description |
|---|---|---|---|
| `language` | `string` | `"en"` | BCP-47 language code for transcription. |

`base_url` is **required** -- it must point to the whisper.cpp server (e.g.,
`"http://localhost:8080"`). The `model` field is forwarded as a hint to the
server.

### STT: `whisper-native`

Uses whisper.cpp via **CGO bindings** -- no HTTP server needed. The model file is
loaded directly into memory.

| Option Key | Type | Default | Description |
|---|---|---|---|
| `language` | `string` | `"en"` | BCP-47 language code for transcription. |
| `model_path` | `string` | -- | Filesystem path to the `.bin` model file. Also accepted via the `model` field. |

### TTS: `elevenlabs`

| Option Key | Type | Default | Description |
|---|---|---|---|
| `output_format` | `string` | `"pcm_16000"` | Audio output format. Common values: `"pcm_16000"`, `"pcm_24000"`, `"pcm_48000"`. |

The `model` field sets the ElevenLabs model ID (default: `"eleven_flash_v2_5"`).

### TTS: `coqui`

Connects to a locally-running **Coqui TTS** or **XTTS v2** server.

| Option Key | Type | Default | Description |
|---|---|---|---|
| `language` | `string` | `"en"` | BCP-47 language code sent to the TTS server. |
| `api_mode` | `string` | `"standard"` | Server API mode. `"standard"` for the standard Coqui TTS Docker image; `"xtts"` for the XTTS v2 API server. XTTS mode enables voice cloning. |

`base_url` is **required** -- it must point to the Coqui server (e.g.,
`"http://localhost:5002"` for standard, `"http://localhost:8002"` for XTTS).

### S2S: `openai-realtime`

| Option Key | Type | Default | Description |
|---|---|---|---|
| *(none)* | | | No provider-specific options. Uses `api_key`, `model`, and `base_url`. |

Default model: `"gpt-4o-realtime-preview"`.

Available voices: `alloy`, `ash`, `ballad`, `coral`, `echo`, `sage`, `shimmer`,
`verse`.

### S2S: `gemini-live`

| Option Key | Type | Default | Description |
|---|---|---|---|
| *(none)* | | | No provider-specific options. Uses `api_key`, `model`, and `base_url`. |

Default model: `"gemini-2.0-flash-live-001"`.

Available voices: `Aoede`, `Charon`, `Fenrir`, `Kore`, `Puck`.

### Embeddings: `openai`

| Option Key | Type | Default | Description |
|---|---|---|---|
| *(none)* | | | No provider-specific options. Uses `api_key`, `model`, and `base_url`. |

Default model: `"text-embedding-3-small"` (1536 dimensions).

### Embeddings: `ollama`

| Option Key | Type | Default | Description |
|---|---|---|---|
| *(none)* | | | No provider-specific options. Uses `base_url` and `model`. |

Default base URL: `"http://localhost:11434"`. Well-known dimension mappings:
`nomic-embed-text` (768), `mxbai-embed-large` (1024), `all-minilm` (384).
Unknown models are auto-probed on first use.

### VAD: `silero`

| Option Key | Type | Default | Description |
|---|---|---|---|
| `frame_size_ms` | `int` | `30` | Audio frame duration in milliseconds (e.g., `10`, `20`, `30`). |
| `speech_threshold` | `float` | `0.5` | Probability above which a frame is classified as speech. Range: `[0.0, 1.0]`. |
| `silence_threshold` | `float` | `0.35` | Probability below which an active speech segment is considered ended. Range: `[0.0, 1.0]`. Must be <= `speech_threshold`. |

---

## :rocket: Minimal Configuration

The smallest valid config to get Glyphoxa running with a single NPC in cascaded
mode:

```yaml
server:
  listen_addr: ":8080"

providers:
  llm:
    name: openai
    api_key: sk-...
    model: gpt-4o
  stt:
    name: deepgram
    api_key: dg-...
    model: nova-3
  tts:
    name: elevenlabs
    api_key: el-...

npcs:
  - name: Tavern Keeper
    personality: You are a friendly tavern keeper.
    voice:
      voice_id: pNInz6obpgDQGcFmaJgB
    engine: cascaded
```

To run with the S2S pipeline instead (no separate STT/TTS needed):

```yaml
server:
  listen_addr: ":8080"

providers:
  s2s:
    name: openai-realtime
    api_key: sk-...
    model: gpt-4o-realtime-preview

npcs:
  - name: Tavern Keeper
    personality: You are a friendly tavern keeper.
    voice:
      voice_id: alloy
    engine: s2s
```

---

## :page_facing_up: Full Example

A fully annotated example configuration is maintained at
[`configs/example.yaml`](../configs/example.yaml). Copy it, rename it to
`config.yaml`, and fill in your API keys to get started.

---

## :link: See Also

- [`docs/getting-started.md`](getting-started.md) -- First-run quickstart guide
- [`docs/providers.md`](providers.md) -- Deep dive into each provider's
  capabilities and trade-offs
- [`docs/deployment.md`](deployment.md) -- Production deployment patterns
  (Docker, systemd, Kubernetes)
- [`configs/example.yaml`](../configs/example.yaml) -- Annotated example
  configuration file
