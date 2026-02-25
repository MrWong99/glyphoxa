> *This document is derived from the Glyphoxa Design Document v0.2*

# Provider Abstraction Layer

Every external AI service sits behind a Go interface. This is the foundational architectural decision that prevents vendor lock-in. Each provider is a struct implementing the interface; swapping providers is a config change that instantiates a different struct.

## LLM Provider Interface

The LLM abstraction must support streaming completions, tool/function calling, system prompts, and multi-turn conversation history. It normalizes the differences between OpenAI-style chat completions, Anthropic's messages API, Google's Gemini API, and local model servers (Ollama, vLLM).

| Method | Description |
|---|---|
| `StreamCompletion(ctx, req) <-chan Chunk` | Streams tokens from the LLM. Returns a Go channel of token chunks. Must support `tool_choice` and tool definitions. |
| `Complete(ctx, req) (*Response, error)` | Non-streaming completion. Used for non-latency-critical tasks like memory summarization and entity extraction. |
| `CountTokens(messages []Message) int` | Token counting for context window management. Provider-specific tokenizer. |
| `Capabilities() ModelCapabilities` | Returns model metadata: context window size, tool calling support, vision support, max output tokens. |

**Candidate providers:** OpenAI (GPT-4o, GPT-4o-mini), Anthropic (Claude Sonnet/Haiku), Google (Gemini 2.5 Flash), Groq (Llama 3.x), Ollama (any local model), OpenRouter (aggregator).

**Go library:** `mozilla-ai/any-llm-go` — unified multi-provider interface with Go channel-based streaming (`<-chan ChatCompletionChunk`), typed error normalization, and functional options. Builds on official provider SDKs (`openai/openai-go`, `anthropics/anthropic-sdk-go`) internally. Fallback: use the official SDKs directly for provider-specific features any-llm-go doesn't expose.

## STT Provider Interface

| Method | Description |
|---|---|
| `StartStream(cfg StreamConfig) (*STTSession, error)` | Opens a streaming transcription session. Returns a session with an input channel (audio) and output channel (transcripts). |
| `Session.SendAudio(chunk []byte)` | Sends an audio chunk to the streaming session. |
| `Session.Partials() <-chan Transcript` | Channel that emits partial (interim) transcriptions as speech is recognized. Used for [speculative pre-fetch](03-memory.md#speculative-pre-fetch-bridging-the-gap). |
| `Session.Finals() <-chan Transcript` | Channel that emits final transcriptions when an utterance is complete. Authoritative text for LLM input and logging. Each `Transcript` includes word-level confidence scores when the provider supports them (Deepgram, Google). |
| `SetKeywords(keywords []KeywordBoost) error` | Updates the keyword boost list for entity name recognition. Populated from the knowledge graph (L3). See [Transcript Correction Pipeline](03-memory.md#transcript-correction-pipeline). |

**Keyword boosting** is critical for TTRPG sessions where fantasy proper nouns ("Eldrinax," "Shadowmancer") are out-of-vocabulary for STT models. Research shows keyword boosting alone reduces entity WER by 30–50%.

**Candidate providers:** Deepgram Nova-3 (best real-time, $0.005/min, keyword boosting), AssemblyAI Universal-2 (custom vocab), local Whisper via whisper.cpp (free, no keyword boost), Google Speech-to-Text v2 (speech adaptation).

**Go library:** Custom streaming WebSocket client over `coder/websocket`. No official Deepgram Go SDK — the streaming API is a straightforward WebSocket protocol.

## TTS Provider Interface

| Method | Description |
|---|---|
| `SynthesizeStream(ctx, text <-chan string, voice VoiceProfile) <-chan []byte` | Streams audio chunks as text sentences arrive via input channel. Returns audio chunk output channel. |
| `ListVoices() []VoiceProfile` | Returns available voice profiles with metadata (gender, age, accent, fantasy-appropriateness). |
| `CloneVoice(samples [][]byte) (*VoiceProfile, error)` | Creates a custom voice from audio samples. Not all providers support this. |
| `MeasureLatency(voice VoiceProfile) LatencyReport` | Returns measured TTFB for a voice. Used for provider selection under latency constraints. |

**Candidate providers:** ElevenLabs Flash v2.5 (75ms TTFB, best quality), Cartesia Sonic (low latency), Coqui XTTS (free local, voice cloning), OpenAI TTS, Google Cloud TTS.

**Go library:** Custom streaming client over `coder/websocket` for ElevenLabs/Cartesia WebSocket APIs. OpenAI TTS is available via `openai/openai-go` (REST, non-streaming). Local Coqui XTTS uses a REST API wrapper.

## Embeddings Provider Interface

The embeddings abstraction handles text-to-vector conversion for the L2 semantic index. It is used by the session processing pipeline (chunking → embedding → pgvector storage) and by cold-layer vector search queries.

| Method | Description |
|---|---|
| `Embed(ctx, text string) ([]float32, error)` | Embeds a single text string. Returns a dense vector. |
| `EmbedBatch(ctx, texts []string) ([][]float32, error)` | Embeds multiple texts in a single API call. Providers that support batching (OpenAI, Voyage AI) are more efficient than sequential `Embed` calls. |
| `Dimensions() int` | Returns the dimensionality of the embedding model's output vectors. Used to configure the pgvector column width. |
| `ModelID() string` | Returns the model identifier (e.g., `text-embedding-3-small`). Stored alongside vectors for model migration tracking — vectors from different models are not comparable. |

**Candidate providers:** OpenAI text-embedding-3-small (1536 dims, $0.02/1M tokens), Voyage AI voyage-3 (1024 dims), nomic-embed-text via Ollama (local, free, 768 dims).

**Go library:** `openai/openai-go` for OpenAI embeddings (REST API, batch support). Voyage AI and nomic-embed-text use OpenAI-compatible REST endpoints, so the same client works with a different base URL. Alternatively, `mozilla-ai/any-llm-go` if it exposes embeddings (check at implementation time).

## VAD Provider Interface

Voice Activity Detection segments audio streams into speech and silence. VAD runs locally with no network hop, making it latency-free. It is the first stage of the audio pipeline — all audio passes through VAD before reaching STT or S2S engines.

| Method | Description |
|---|---|
| `NewSession(cfg VADConfig) (*VADSession, error)` | Creates a new VAD session with the given configuration (sample rate, frame size, speech/silence thresholds). |
| `Session.ProcessFrame(frame []byte) (VADEvent, error)` | Processes a single audio frame and returns a speech/silence event. Must be sub-millisecond. |
| `Session.Reset()` | Resets the internal state (e.g., between speakers or after a long silence). |
| `Session.Close() error` | Releases resources (ONNX session, model memory). |

`VADEvent` carries the detection result: `SpeechStart`, `SpeechEnd`, or `SpeechContinue`, plus the speech probability score (0.0–1.0).

**Candidate providers:** Silero VAD (default — sub-millisecond inference, language-agnostic, ONNX-based), WebRTC VAD (lighter but less accurate), cloud-based VAD (not recommended — adds network latency to a stage that must be instant).

**Go library:** `streamer45/silero-vad-go` (wraps `yalue/onnxruntime_go`). Fallback: `plandem/silero-go`. Both require CGo and the ONNX Runtime shared library. See [Technology: CGo](07-technology.md#cgo-and-native-dependencies).

## Audio Platform Interface

The audio platform abstraction isolates Glyphoxa from the specifics of Discord, WebRTC, or any future voice platform. It models audio as multiple named input streams (one per participant) and one or more output streams (NPC voices). All streams are Go channels carrying audio frames.

| Method | Description |
|---|---|
| `Connect(ctx, channelID string) (*VoiceConn, error)` | Joins a voice channel/room. Returns a connection handle. |
| `VoiceConn.InputStreams() map[UserID]<-chan AudioFrame` | Returns a map of participant ID to readable audio channels (PCM/Opus). |
| `VoiceConn.OutputStream(voiceID string) chan<- AudioFrame` | Returns a writable audio channel for a specific NPC voice. Platform mixes multiple outputs. |
| `VoiceConn.OnParticipantChange(cb func(Event))` | Lifecycle events for participants entering/leaving the voice session. |
| `VoiceConn.Disconnect()` | Cleanly leaves the voice channel. |

### Audio Mixer

Discord limits a single bot to one outbound audio stream per guild. The `AudioMixer` serializes multiple NPC outputs through a priority queue. It sits between `VoiceEngine` outputs and the `VoiceConn` output stream. See [Architecture: Audio Mixing Layer](01-architecture.md#audio-mixing-layer) for the full design.

| Method | Description |
|---|---|
| `Enqueue(segment AudioSegment, priority int)` | Adds an NPC audio segment to the output queue with a priority level. Higher priority segments play first. |
| `Interrupt(reason InterruptReason)` | Truncates the currently playing segment and optionally clears the queue. Used for DM override and player barge-in. |
| `OnBargeIn(handler func(speakerID UserID))` | Registers a callback for when a player speaks during NPC output. The mixer truncates playback and invokes the handler so the orchestrator can route the player's speech. |
| `SetGap(duration time.Duration)` | Configures the silence gap between consecutive NPC segments (default 300ms ± 50ms jitter). |

**Go library:** `bwmarrin/discordgo` (v0.29.0, 5.6k stars) — full voice support with Opus send/receive and per-user audio streams. Build a custom audio pipeline on top of discordgo's voice connection primitives (`dgvoice` is a proof-of-concept reference only). **Future targets:** custom WebRTC server via `pion/webrtc`, Mumble, browser-based sessions.

## VoiceEngine Interface

The `VoiceEngine` is the top-level abstraction that unifies both pipeline approaches. The Agent Orchestrator interacts with NPCs exclusively through this interface — it does not know whether a cascaded or S2S engine is behind it.

| Method | Description |
|---|---|
| `Process(ctx, input, prompt) (*Response, error)` | Handles a complete voice interaction. Cascaded: runs STT→context→LLM→TTS. S2S: forwards audio to the session and returns the response audio. |
| `InjectContext(ctx, update) error` | Pushes updated context (identity, scene, cross-NPC utterances) into the engine mid-session. |
| `SetTools(tools []MCPToolDef) error` | Declares which MCP tools this engine can invoke. The orchestrator uses this to enforce latency budgets. |
| `OnToolCall(handler ToolCallHandler)` | Registers a handler for tool invocations from the LLM. The handler executes the MCP tool and returns results. |
| `Transcripts() <-chan TranscriptEntry` | Returns a channel of transcript entries (player + NPC) for session logging and cross-NPC awareness. |
| `Close() error` | Tears down the engine and releases resources. |

**Implementations:**
- `CascadedEngine` — bundles `STTProvider` + `LLMProvider` + `TTSProvider`. Uses the full cascaded pipeline.
- `S2SEngine` — wraps an `S2SProvider` session. Delegates everything to the S2S API.

See the `VoiceEngine` and `S2SProvider` interfaces below for the full specification.

## S2S Provider Interface

The S2S abstraction models providers that handle audio-in to audio-out in a single API, with built-in tool calling.

| Method | Description |
|---|---|
| `Connect(ctx, cfg S2SSessionConfig) (*S2SSession, error)` | Establishes a new S2S session with the given configuration (voice, instructions, tools). |
| `Capabilities() S2SCapabilities` | Returns provider metadata: context window, session duration limits, resumption support, available voices. |

### S2S Session

| Method | Description |
|---|---|
| `SendAudio(chunk []byte) error` | Sends raw audio to the S2S session. |
| `Audio() <-chan []byte` | Returns a channel of audio chunks from the S2S model's speech output. |
| `Transcripts() <-chan TranscriptEntry` | Returns transcripts of both the user's speech (as recognized by the S2S model) and the model's response text. |
| `OnToolCall(handler ToolCallHandler)` | Registers a handler for function calls from the S2S model. |
| `SetTools(tools []MCPToolDef) error` | Updates the tool set available to the model mid-session. |
| `UpdateInstructions(instructions string) error` | Updates the system instructions mid-session. |
| `InjectTextContext(items []ContextItem) error` | Injects text-based context items (NPC identity, scene, conversation history) into the session. |
| `Interrupt() error` | Interrupts the model's current speech output. |
| `Close() error` | Closes the session and releases the WebSocket connection. |

**Candidate providers:** OpenAI Realtime API (`gpt-realtime`, `gpt-realtime-mini`), Google Gemini Live API (`gemini-live-2.5-flash-native-audio`).

**Go libraries:** `WqyJh/go-openai-realtime` for OpenAI Realtime (supports all client/server events, uses `coder/websocket` internally). For Gemini Live, a custom WebSocket client over `coder/websocket` — no Go SDK exists for the Live API.

---

**See also:** [Architecture](01-architecture.md) · [Technology](07-technology.md)
