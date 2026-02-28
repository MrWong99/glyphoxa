# Providers

## :jigsaw: Overview

Glyphoxa uses an **interface-first provider architecture** to decouple the voice AI pipeline from any specific vendor or service. Every external AI capability -- LLM completions, speech-to-text, text-to-speech, speech-to-speech, embeddings, and voice activity detection -- sits behind a Go interface defined in `pkg/provider/`. Swapping a cloud API for a local model, or migrating between vendors, is a configuration change that instantiates a different struct. No pipeline code needs to change.

Key design principles:

- **One interface per capability.** Each pipeline stage has exactly one interface. Implementations are in sub-packages named after the vendor or technology.
- **Concurrency-safe by contract.** All provider interfaces require implementations to be safe for concurrent use from multiple goroutines. Channels returned by streaming methods are closed by the implementation.
- **Compile-time enforcement.** Every concrete provider includes a `var _ Interface = (*Impl)(nil)` assertion so that missing methods are caught at compile time, not at runtime.
- **Resilience built in.** The `internal/resilience` package wraps any provider in a `FallbackGroup` with per-entry circuit breakers, enabling automatic failover without changing caller code.

---

## :electric_plug: Provider Interfaces

| Interface | Package | Key Methods | Purpose |
|---|---|---|---|
| `llm.Provider` | `pkg/provider/llm` | `StreamCompletion`, `Complete`, `CountTokens`, `Capabilities` | LLM text completions (streaming and batch), token counting, and model capability introspection |
| `stt.Provider` | `pkg/provider/stt` | `StartStream` (returns `SessionHandle`) | Opens streaming transcription sessions; `SessionHandle` exposes `SendAudio`, `Partials`, `Finals`, `SetKeywords`, `Close` |
| `tts.Provider` | `pkg/provider/tts` | `SynthesizeStream`, `ListVoices`, `CloneVoice` | Streaming text-to-speech synthesis, voice catalogue listing, and voice cloning |
| `s2s.Provider` | `pkg/provider/s2s` | `Connect` (returns `SessionHandle`), `Capabilities` | End-to-end speech-to-speech sessions; `SessionHandle` exposes `SendAudio`, `Audio`, `Transcripts`, `OnToolCall`, `SetTools`, `UpdateInstructions`, `InjectTextContext`, `Interrupt`, `Close` |
| `embeddings.Provider` | `pkg/provider/embeddings` | `Embed`, `EmbedBatch`, `Dimensions`, `ModelID` | Text-to-vector conversion for semantic memory retrieval (L2 index) |
| `vad.Engine` | `pkg/provider/vad` | `NewSession` (returns `SessionHandle`) | Voice activity detection; `SessionHandle` exposes `ProcessFrame`, `Reset`, `Close` |

### LLM Provider

The LLM interface normalises differences between OpenAI, Anthropic, Google, Groq, and local model APIs. It supports streaming completions via Go channels, tool/function calling, system prompts, and multi-turn conversation history.

```go
type Provider interface {
    StreamCompletion(ctx context.Context, req CompletionRequest) (<-chan Chunk, error)
    Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error)
    CountTokens(messages []Message) (int, error)
    Capabilities() ModelCapabilities
}
```

### STT Provider

The STT interface models real-time transcription as a session-based streaming API. Each session accepts PCM audio frames and emits two transcript streams: low-latency **partials** (for speculative pre-fetch) and authoritative **finals** (for the session log and LLM input).

```go
type Provider interface {
    StartStream(ctx context.Context, cfg StreamConfig) (SessionHandle, error)
}
```

The `SessionHandle` interface exposes `SendAudio`, `Partials`, `Finals`, `SetKeywords`, and `Close`.

### TTS Provider

The TTS interface accepts a channel of text fragments (piped directly from streaming LLM output) and returns a channel of raw PCM audio bytes. This channel-in/channel-out design enables low-latency pipelining without waiting for the full text.

```go
type Provider interface {
    SynthesizeStream(ctx context.Context, text <-chan string, voice VoiceProfile) (<-chan []byte, error)
    ListVoices(ctx context.Context) ([]VoiceProfile, error)
    CloneVoice(ctx context.Context, samples [][]byte) (*VoiceProfile, error)
}
```

### S2S Provider

The S2S (speech-to-speech) interface models providers that handle audio-in to audio-out in a single stateful session, bypassing the separate STT/LLM/TTS pipeline. Sessions are long-lived and support mid-session reconfiguration of instructions, tools, and context.

```go
type Provider interface {
    Connect(ctx context.Context, cfg SessionConfig) (SessionHandle, error)
    Capabilities() S2SCapabilities
}
```

### Embeddings Provider

The embeddings interface converts text to dense float32 vectors for the semantic memory layer (pgvector). It supports both single-text and batch embedding for efficiency.

```go
type Provider interface {
    Embed(ctx context.Context, text string) ([]float32, error)
    EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
    Dimensions() int
    ModelID() string
}
```

### VAD Engine

Voice Activity Detection runs locally with sub-millisecond latency. It is the first stage of the audio pipeline -- all audio passes through VAD before reaching STT or S2S engines. The interface is named `Engine` (not `Provider`) because VAD is always local and never a remote service.

```go
type Engine interface {
    NewSession(cfg Config) (SessionHandle, error)
}
```

The `SessionHandle` exposes synchronous `ProcessFrame(frame []byte) (VADEvent, error)`, `Reset()`, and `Close() error`.

---

## :package: Supported Providers

### LLM Providers

| Provider | Package | Backend | Status | Latency Tier | Cost Tier |
|---|---|---|---|---|---|
| OpenAI (GPT-4o, GPT-4o-mini, o1, o3) | `pkg/provider/llm/anyllm` | `any-llm-go` | Production | Medium | $$$  |
| Anthropic (Claude Sonnet, Haiku, Opus) | `pkg/provider/llm/anyllm` | `any-llm-go` | Production | Medium | $$$ |
| Google Gemini (2.0 Flash, 1.5 Pro) | `pkg/provider/llm/anyllm` | `any-llm-go` | Production | Medium | $$ |
| Groq (Llama 3.x) | `pkg/provider/llm/anyllm` | `any-llm-go` | Production | Low | $ |
| Ollama (any local model) | `pkg/provider/llm/anyllm` | `any-llm-go` | Production | Varies | Free |
| DeepSeek | `pkg/provider/llm/anyllm` | `any-llm-go` | Production | Medium | $ |
| Mistral | `pkg/provider/llm/anyllm` | `any-llm-go` | Production | Medium | $$ |
| llama.cpp (local server) | `pkg/provider/llm/anyllm` | `any-llm-go` | Production | Varies | Free |
| llamafile (local server) | `pkg/provider/llm/anyllm` | `any-llm-go` | Production | Varies | Free |
| Mock | `pkg/provider/llm/mock` | In-memory | Testing | -- | -- |

All LLM providers are implemented through a single unified adapter (`anyllm.Provider`) that wraps the [`mozilla-ai/any-llm-go`](https://github.com/mozilla-ai/any-llm-go) library. This library provides a consistent interface across all supported backends, with Go channel-based streaming and typed error normalisation. Convenience constructors (`NewOpenAI`, `NewAnthropic`, `NewGemini`, etc.) are available for common providers.

### STT Providers

| Provider | Package | Status | Latency Tier | Cost Tier | Keyword Boost |
|---|---|---|---|---|---|
| Deepgram Nova-3 | `pkg/provider/stt/deepgram` | Production | Low | $ | Yes (at session start) |
| Whisper.cpp (HTTP server) | `pkg/provider/stt/whisper` | Production | Medium | Free | No |
| Whisper.cpp (native CGO) | `pkg/provider/stt/whisper` (`NativeProvider`) | Production | Medium | Free | No |
| Mock | `pkg/provider/stt/mock` | Testing | -- | -- | -- |

### TTS Providers

| Provider | Package | Status | Latency Tier | Cost Tier | Voice Cloning |
|---|---|---|---|---|---|
| ElevenLabs (Flash v2.5) | `pkg/provider/tts/elevenlabs` | Production | Low | $$$ | Planned |
| Coqui TTS (Standard) | `pkg/provider/tts/coqui` | Production | Medium | Free | No |
| Coqui XTTS v2 | `pkg/provider/tts/coqui` (`APIModeXTTS`) | Production | Medium | Free | Yes |
| Mock | `pkg/provider/tts/mock` | Testing | -- | -- | -- |

### S2S Providers

| Provider | Package | Status | Latency Tier | Cost Tier | Mid-Session Updates |
|---|---|---|---|---|---|
| OpenAI Realtime (gpt-4o-realtime) | `pkg/provider/s2s/openai` | Production | Low | $$$$ | Instructions, tools, interrupt |
| Gemini Live (gemini-2.0-flash-live) | `pkg/provider/s2s/gemini` | Production | Low | $$ | Context injection only |
| Mock | `pkg/provider/s2s/mock` | Testing | -- | -- | -- |

### Embeddings Providers

| Provider | Package | Status | Latency Tier | Cost Tier | Dimensions |
|---|---|---|---|---|---|
| OpenAI (text-embedding-3-small/large) | `pkg/provider/embeddings/openai` | Production | Medium | $ | 1536 / 3072 |
| Ollama (nomic-embed-text, mxbai-embed-large, all-minilm) | `pkg/provider/embeddings/ollama` | Production | Low | Free | 768 / 1024 / 384 |
| Mock | `pkg/provider/embeddings/mock` | Testing | -- | -- | -- |

### VAD Engines

| Provider | Package | Status | Latency | Cost |
|---|---|---|---|---|
| Silero VAD | *Planned* (via `silero-vad-go`) | Planned | Sub-ms | Free |
| Mock | `pkg/provider/vad/mock` | Testing | -- | -- |

> **Note:** The VAD `Engine` interface and `SessionHandle` are fully defined. The Silero VAD implementation is planned; currently only the mock engine is available.

---

## :gear: Configuring Providers

Providers are configured in the `providers` section of the Glyphoxa YAML config file. Each entry follows the same `name` / `api_key` / `base_url` / `model` / `options` pattern.

### Configuration Schema

```go
type ProviderEntry struct {
    Name    string         `yaml:"name"`     // Provider implementation name
    APIKey  string         `yaml:"api_key"`  // Authentication key
    BaseURL string         `yaml:"base_url"` // Override default API endpoint
    Model   string         `yaml:"model"`    // Model selection within provider
    Options map[string]any `yaml:"options"`  // Provider-specific options
}
```

### Full Example

```yaml
providers:
  llm:
    name: openai
    api_key: "${OPENAI_API_KEY}"
    model: gpt-4o

  stt:
    name: deepgram
    api_key: "${DEEPGRAM_API_KEY}"
    model: nova-3
    options:
      language: en
      sample_rate: 16000

  tts:
    name: elevenlabs
    api_key: "${ELEVENLABS_API_KEY}"
    model: eleven_flash_v2_5
    options:
      output_format: pcm_16000

  s2s:
    name: openai
    api_key: "${OPENAI_API_KEY}"
    model: gpt-4o-realtime-preview

  embeddings:
    name: openai
    api_key: "${OPENAI_API_KEY}"
    model: text-embedding-3-small

  vad:
    name: silero
    options:
      speech_threshold: 0.5
      silence_threshold: 0.35
      frame_size_ms: 30
```

### Provider-Specific Examples

**Local-only stack (no API keys required):**

```yaml
providers:
  llm:
    name: ollama
    model: llama3.1:8b
    base_url: http://localhost:11434

  stt:
    name: whisper
    base_url: http://localhost:8080
    options:
      language: en
      silence_threshold_ms: 500

  tts:
    name: coqui
    base_url: http://localhost:5002
    options:
      language: en
      api_mode: standard

  embeddings:
    name: ollama
    model: nomic-embed-text
    base_url: http://localhost:11434
```

**S2S with Gemini Live:**

```yaml
providers:
  s2s:
    name: gemini
    api_key: "${GEMINI_API_KEY}"
    model: gemini-2.0-flash-live-001
```

**Coqui XTTS with voice cloning:**

```yaml
providers:
  tts:
    name: coqui
    base_url: http://localhost:8002
    options:
      api_mode: xtts
      language: en
```

---

## :hammer_and_wrench: Adding a New Provider

Follow these steps to add a new provider implementation.

### Step 1: Create the package

Create a new directory under the appropriate provider type:

```
pkg/provider/<type>/<vendor>/
```

For example, to add an AssemblyAI STT provider:

```
pkg/provider/stt/assemblyai/
    assemblyai.go
    assemblyai_test.go
```

### Step 2: Implement the interface

Your provider struct must implement the relevant interface. Here is a minimal skeleton for an STT provider:

```go
package assemblyai

import (
    "context"
    "github.com/MrWong99/glyphoxa/pkg/provider/stt"
)

// Provider implements stt.Provider backed by the AssemblyAI streaming API.
type Provider struct {
    apiKey string
    model  string
}

// New creates a new AssemblyAI Provider.
func New(apiKey string) (*Provider, error) {
    if apiKey == "" {
        return nil, errors.New("assemblyai: apiKey must not be empty")
    }
    return &Provider{apiKey: apiKey}, nil
}

// StartStream opens a streaming transcription session.
func (p *Provider) StartStream(ctx context.Context, cfg stt.StreamConfig) (stt.SessionHandle, error) {
    // Implementation here...
}
```

### Step 3: Add a compile-time interface assertion

Add this line at the package level (typically near the top of the file, after the struct definition):

```go
// Compile-time assertion that Provider satisfies stt.Provider.
var _ stt.Provider = (*Provider)(nil)
```

This ensures that if the interface changes, the compiler will catch any missing methods immediately rather than surfacing them as runtime panics.

### Step 4: Register in the config loader

Add your new provider name to the config loader so it can be selected via YAML configuration. Update the relevant factory function in `internal/config/` or the provider registry to handle the new `name` value.

### Step 5: Write tests

Every provider package should include tests that verify:

- The constructor rejects invalid inputs (empty API key, empty URL, etc.)
- The compile-time assertion is present
- Message parsing and serialisation are correct (unit-testable without a live service)
- Sessions behave correctly under cancellation and close

Follow the patterns established by existing test files (e.g., `deepgram_test.go`, `whisper_test.go`, `elevenlabs_test.go`).

### Step 6: Add a mock (if needed)

If your provider has a session-based interface, consider adding a mock implementation in a `mock/` sub-package for use in integration tests. See `pkg/provider/stt/mock/` for a reference implementation.

---

## :white_check_mark: Compile-Time Interface Assertions

Every concrete provider in Glyphoxa includes a compile-time assertion of the form:

```go
var _ stt.Provider = (*Provider)(nil)
```

This is a standard Go idiom that creates a zero-value pointer to the implementation type and assigns it to a blank variable typed as the interface. The compiler verifies that `*Provider` satisfies `stt.Provider` at compile time. If a method is missing or has the wrong signature, the build fails immediately.

### Why this matters

Without compile-time assertions, a provider could silently fail to implement an interface. The error would only surface at runtime when the value is first assigned to an interface variable -- potentially deep in the pipeline during a live session. In a real-time voice system where latency matters, this kind of late failure is unacceptable.

### Where to find them

Compile-time assertions are placed in every provider and mock package:

```go
// pkg/provider/stt/whisper/whisper.go
var _ stt.Provider = (*Provider)(nil)

// pkg/provider/stt/whisper/native.go
var _ stt.Provider = (*NativeProvider)(nil)
var _ stt.SessionHandle = (*nativeSession)(nil)

// pkg/provider/s2s/openai/openai.go
var _ s2s.Provider = (*Provider)(nil)
var _ s2s.SessionHandle = (*session)(nil)

// pkg/provider/tts/coqui/coqui.go
var _ tts.Provider = (*Provider)(nil)

// pkg/provider/embeddings/ollama/ollama.go
var _ embeddings.Provider = (*Provider)(nil)

// internal/resilience/llm_fallback.go
var _ llm.Provider = (*LLMFallback)(nil)
```

Session handle interfaces (e.g., `stt.SessionHandle`, `s2s.SessionHandle`, `vad.SessionHandle`) also receive assertions because they are equally important to correctness.

---

## :shield: Resilience and Failover

The `internal/resilience` package provides automatic failover and circuit breaking for provider calls. It ensures that a single provider outage does not take down the entire voice pipeline.

### Fallback Groups

A `FallbackGroup[T]` wraps a primary provider and zero or more fallbacks of the same type. When the primary fails (or its circuit breaker is open), the next healthy fallback is tried in registration order.

```go
// Create a fallback group with OpenAI as primary, Anthropic as fallback.
primary, _ := anyllm.NewOpenAI("gpt-4o")
fallback, _ := anyllm.NewAnthropic("claude-3-5-sonnet-latest")

group := resilience.NewLLMFallback(primary, "openai-gpt4o", resilience.FallbackConfig{
    CircuitBreaker: resilience.CircuitBreakerConfig{
        MaxFailures:  5,
        ResetTimeout: 30 * time.Second,
        HalfOpenMax:  3,
    },
})
group.AddFallback("anthropic-sonnet", fallback)

// Use group as a normal llm.Provider -- failover is transparent.
resp, err := group.Complete(ctx, req)
```

Type-specific fallback wrappers are provided for each provider interface:

| Wrapper | Interface | Package |
|---|---|---|
| `LLMFallback` | `llm.Provider` | `internal/resilience` |
| `STTFallback` | `stt.Provider` | `internal/resilience` |
| `TTSFallback` | `tts.Provider` | `internal/resilience` |

Each wrapper implements the full provider interface, so callers cannot distinguish a fallback-wrapped provider from a bare one.

### Circuit Breaker

Each provider entry in a `FallbackGroup` has its own `CircuitBreaker` with three states:

| State | Behaviour |
|---|---|
| **Closed** (normal) | All calls are forwarded. Consecutive failures are counted. |
| **Open** (tripped) | Calls are rejected immediately with `ErrCircuitOpen`. Entered after `MaxFailures` consecutive failures. |
| **Half-Open** (probe) | Entered after `ResetTimeout` elapses. A limited number of probe calls (`HalfOpenMax`) are allowed through. If they succeed, the breaker closes. If any fail, it re-opens. |

Configuration parameters:

| Parameter | Default | Description |
|---|---|---|
| `MaxFailures` | 5 | Consecutive failures before the breaker opens |
| `ResetTimeout` | 30s | How long the breaker stays open before transitioning to half-open |
| `HalfOpenMax` | 3 | Maximum probe calls in the half-open state |

### Failover Scope

Failover covers the **initial connection/request** only. For streaming providers (STT, TTS, S2S), once a stream is established, mid-stream errors are the caller's responsibility. This is a deliberate design choice: re-establishing a stream mid-utterance would produce incoherent audio output.

### Error Propagation

When all providers in a group fail, `Execute` returns `ErrAllFailed` wrapping the last error. The `FallbackGroup` logs each failure at `WARN` level and each circuit-open skip at `DEBUG` level, providing visibility into provider health without flooding logs.

---

## :scales: Engine Trade-offs

Glyphoxa supports three conversation engine modes, each offering a different balance of latency, quality, cost, and flexibility. The engine is selected per-NPC via the `engine` field in the NPC configuration.

### Engine Modes

**Cascaded (`cascaded`)** -- The traditional pipeline: STT transcribes player speech to text, the LLM generates a response, and TTS synthesises the response into audio. Each stage is independently configurable and swappable.

**Speech-to-Speech (`s2s`)** -- An end-to-end model (OpenAI Realtime or Gemini Live) handles the entire audio-in to audio-out flow in a single API session. The orchestrator interacts with the NPC exclusively through the `VoiceEngine` interface -- it does not know which engine is behind it.

**Sentence Cascade (`sentence_cascade`)** -- An experimental dual-model approach: a fast, small model generates the opening sentence (for rapid time-to-first-audio), then a stronger model generates the substantive continuation. Both outputs are piped through TTS.

### Comparison Table

| Dimension | Cascaded (STT+LLM+TTS) | S2S (Direct) | Sentence Cascade |
|---|---|---|---|
| **End-to-end latency** | 1.5--3s (additive) | 0.5--1s | 0.8--1.5s (first sentence fast) |
| **Response quality** | Highest (full LLM) | Good (constrained by S2S model) | High (strong model for body) |
| **Voice quality** | Excellent (dedicated TTS) | Good (model-integrated) | Excellent (dedicated TTS) |
| **Cost** | $$--$$$ (3 API calls) | $$$$  (single premium session) | $$$--$$$$ (2 LLM + TTS) |
| **Flexibility** | Full (mix any STT+LLM+TTS) | Limited (single provider) | High (two LLMs + TTS) |
| **Tool calling** | Full LLM tool support | Provider-specific limits | Full LLM tool support |
| **Keyword boosting** | Yes (via STT provider) | No (model handles STT) | Yes (via STT provider) |
| **Voice cloning** | Yes (via TTS provider) | No (fixed voice set) | Yes (via TTS provider) |
| **Provider requirements** | STT + LLM + TTS | S2S provider | STT + 2x LLM + TTS |
| **Status** | Production | Production | Experimental |

### When to Use Each Engine

**Use Cascaded when:**
- You need maximum flexibility in provider selection
- Response quality is the top priority
- You need keyword boosting for fantasy proper nouns
- You need voice cloning for custom NPC voices
- Latency of 1.5--3 seconds per turn is acceptable

**Use S2S when:**
- Sub-second response latency is critical (e.g., fast-paced combat narration)
- You are using OpenAI Realtime or Gemini Live
- Tool calling requirements are within S2S provider limits
- The S2S provider's built-in voices meet your NPC needs

**Use Sentence Cascade when:**
- You want faster time-to-first-audio than pure cascaded
- You want the quality of a strong model for the substantive response
- You are experimenting with the dual-model approach

### Configuration Example

```yaml
npcs:
  - name: "Greymantle the Sage"
    engine: cascaded
    personality: "A wise old wizard who speaks in measured tones."
    voice:
      provider: elevenlabs
      voice_id: "pNInz6obpgDQGcFmaJgB"
    budget_tier: standard

  - name: "Quicksilver"
    engine: s2s
    personality: "A fast-talking rogue who never misses a beat."
    voice:
      provider: openai
      voice_id: "alloy"
    budget_tier: fast

  - name: "The Oracle"
    engine: sentence_cascade
    personality: "A mysterious seer who begins with cryptic fragments."
    voice:
      provider: elevenlabs
      voice_id: "21m00Tcm4TlvDq8ikWAM"
    cascade_mode: always
    cascade:
      fast_model: gpt-4o-mini
      strong_model: gpt-4o
```

---

## :books: See also

- [architecture.md](architecture.md) -- System architecture overview
- [configuration.md](configuration.md) -- Full configuration reference
- [audio-pipeline.md](audio-pipeline.md) -- Audio pipeline and mixer design
- [design/02-providers.md](design/02-providers.md) -- Design rationale for the provider abstraction layer
