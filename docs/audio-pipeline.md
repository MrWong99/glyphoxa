---
nav_order: 8
---

# Audio Pipeline

End-to-end documentation for the Glyphoxa audio pipeline -- from player microphone to NPC voice output.

---

## Overview

Glyphoxa's audio pipeline is a bidirectional streaming system that captures player speech from a voice platform, detects speech boundaries, processes it through an AI engine, and delivers synthesised NPC responses back to all participants. The entire path is built on Go channels and goroutines, enabling true end-to-end streaming where each pipeline stage begins processing before the previous stage completes.

The pipeline consists of six core stages:

1. **Platform Transport (Inbound)** -- captures per-participant audio from Discord or WebRTC
2. **Voice Activity Detection** -- segments speech from silence using Silero ONNX
3. **Engine Processing** -- converts player speech to NPC response (three engine types)
4. **Audio Mixer** -- priority-queues NPC speech segments, handles barge-in
5. **Platform Transport (Outbound)** -- encodes and transmits NPC audio to all participants
6. **Utterance Buffer** -- maintains cross-NPC awareness of recent conversation

---

## Audio Flow Diagram

```
                            INBOUND (Player -> Engine)
  ┌────────────┐     ┌───────────────┐     ┌─────────┐     ┌─────────────┐
  │ Microphone │────>│  Platform     │────>│   VAD   │────>│   Engine    │
  │ (Player)   │     │  Transport    │     │ (Silero)│     │ (Cascaded / │
  └────────────┘     │  (Discord /   │     └────┬────┘     │  S2S /      │
                     │   WebRTC)     │          │          │  Cascade)   │
                     └───────────────┘    SpeechStart/     └──────┬──────┘
                         │                 SpeechEnd              │
                         │                 gates STT              │
                    per-participant                        engine.Response
                    AudioFrame channels                   {Text, Audio <-chan}
                                                                 │
                           OUTBOUND (Engine -> Speaker)          │
  ┌────────────┐     ┌──────────────┐     ┌──────────┐           │
  │  Speaker   │<────│  Platform    │<────│  Audio   │<──────────┘
  │ (All       │     │  Transport   │     │  Mixer   │
  │  Players)  │     │  (Discord /  │     │ (Priority│
  └────────────┘     │   WebRTC)    │     │  Queue)  │
                     └──────────────┘     └──────────┘
                         │                    │
                    Opus encode /        Barge-in detection
                    WebRTC send          DM override
                                         Inter-segment gap
```

**Detailed data flow:**

```
Player speaks
  │
  ├─ Discord: Opus packet ──> OpusRecv ──> Opus decode ──> PCM AudioFrame
  │                                                             │
  ├─ WebRTC: PeerTransport.AudioInput() ──> PCM AudioFrame ─────┤
  │                                                             │
  ▼                                                             ▼
  InputStreams()  ─────────────────────────>  per-participant <-chan AudioFrame
  (map[participantID] channel)                      │
                                                    ▼
                                              VAD.ProcessFrame()
                                              ┌─ SpeechStart: begin buffering
                                              ├─ SpeechContinue: accumulate
                                              ├─ SpeechEnd: dispatch to engine
                                              └─ Silence: discard / reset
                                                    │
                                                    ▼
                                          VoiceEngine.Process(AudioFrame, PromptContext)
                                                    │
                                              ┌─────┴──────────┐
                                              │ engine.Response │
                                              │ .Audio <-chan   │
                                              │ .Text string    │
                                              │ .ToolCalls []   │
                                              └─────┬──────────┘
                                                    │
                                                    ▼
                                          AudioSegment{NPCID, Audio, Priority}
                                                    │
                                              Mixer.Enqueue(segment, priority)
                                                    │
                                              dispatch goroutine
                                              ├─ priority queue (max-heap)
                                              ├─ inter-segment gap + jitter
                                              └─ output callback ──> []byte
                                                    │
                                                    ▼
                                          Connection.OutputStream() chan<- AudioFrame
                                                    │
                                              ┌─────┴──────────┐
                                              ├─ Discord: Opus encode ──> OpusSend
                                              └─ WebRTC: PeerTransport.SendAudio()
                                                    │
                                                    ▼
                                               All participants hear NPC
```

---

## :satellite: Platform Transports

Platform transports implement the `audio.Platform` and `audio.Connection` interfaces, abstracting voice channel connectivity from the rest of the pipeline.

### Core Interfaces

```go
// Platform is the entry point for a voice-channel provider.
type Platform interface {
    Connect(ctx context.Context, channelID string) (Connection, error)
}

// Connection represents an active session on a voice channel.
type Connection interface {
    InputStreams() map[string]<-chan AudioFrame   // per-participant input
    OutputStream() chan<- AudioFrame              // single mixed output
    OnParticipantChange(cb func(Event))           // join/leave callbacks
    Disconnect() error
}
```

### :video_game: Discord Transport (`pkg/audio/discord/`)

The Discord transport bridges Discord's Opus-based voice protocol with Glyphoxa's PCM `AudioFrame` pipeline using the `bwmarrin/discordgo` library.

**How it works:**

| Stage | Detail |
|---|---|
| **Inbound** | Opus packets arrive via `VoiceConnection.OpusRecv`. Each SSRC gets its own `gopus.Decoder`. Decoded PCM frames are delivered to per-participant channels (buffer: 64 frames). |
| **Outbound** | PCM `AudioFrame` values written to `OutputStream()` are encoded to Opus via `gopus.Encoder` and sent to `VoiceConnection.OpusSend`. Discord speaking notifications are managed automatically. |
| **Codec** | 48 kHz stereo Opus, 20 ms frame size (960 samples/channel). |
| **Participant tracking** | `VoiceStateUpdate` events detect joins/leaves by guild and channel ID. SSRC-to-user-ID mapping is built lazily as packets arrive. |
| **Lifecycle** | `Disconnect()` closes all input channels, removes event handlers, and disconnects the voice connection. Safe to call multiple times. |

**Configuration:**

```go
platform := discord.New(session, guildID)
conn, err := platform.Connect(ctx, channelID)
```

**When to use:** Production Discord bot deployments. This is the primary transport for TTRPG sessions hosted on Discord.

### :globe_with_meridians: WebRTC Transport (`pkg/audio/webrtc/`)

The WebRTC transport enables browser-based voice sessions via `pion/webrtc`, without requiring Discord or any third-party voice platform.

**How it works:**

| Stage | Detail |
|---|---|
| **Inbound** | Each peer has a `PeerTransport` that delivers audio via `AudioInput()`. A `readPeerInput` goroutine forwards frames to the per-participant channel. |
| **Outbound** | A `forwardOutput` goroutine reads from the output channel and fans out to all connected peers via `PeerTransport.SendAudio()`. |
| **Signaling** | HTTP-based signaling server with three endpoints: `POST /rooms/{roomID}/join` (SDP offer/answer), `POST /rooms/{roomID}/ice` (ICE candidates), `DELETE /rooms/{roomID}/leave`. |
| **ICE** | Configurable STUN servers (default: `stun:stun.l.google.com:19302`). |
| **Lifecycle** | `OutputWriter` provides lifecycle-aware writes that safely drop frames after disconnect instead of panicking. |

**Configuration:**

```go
platform := webrtc.New(
    webrtc.WithSTUNServers("stun:stun.l.google.com:19302"),
    webrtc.WithSampleRate(48000),
)
conn, err := platform.Connect(ctx, roomID)
```

**When to use:** Custom web UIs, browser-based TTRPG tools, or environments where Discord is not available. Currently in alpha -- the `PeerTransport` interface abstracts the pion/webrtc integration so it can be developed independently.

### Transport Comparison

| Feature | Discord | WebRTC |
|---|---|---|
| Codec | Opus (48 kHz stereo) | Configurable (default 48 kHz) |
| Client | Discord app | Any WebRTC browser |
| Setup | Bot token + guild ID | Signaling server + STUN |
| Maturity | Production | Alpha |
| Participant tracking | VoiceStateUpdate events | Explicit AddPeer/RemovePeer |
| Multi-room | One connection per channel | One connection per room |

---

## :loud_sound: Voice Activity Detection (VAD)

VAD sits between the platform transport and the engine, segmenting continuous audio into discrete speech regions. Only audio classified as speech is forwarded to the engine, saving STT/S2S costs and preventing hallucinated transcriptions of silence.

### Interface

```go
// Engine is the factory for VAD sessions.
type Engine interface {
    NewSession(cfg Config) (SessionHandle, error)
}

// SessionHandle processes frames for a single audio stream.
type SessionHandle interface {
    ProcessFrame(frame []byte) (VADEvent, error)
    Reset()
    Close() error
}
```

### Silero ONNX Model

Glyphoxa uses **Silero VAD** as its default (and currently only) VAD backend. Silero is a compact neural network (~1 MB) that runs locally via ONNX Runtime -- no network hop, no API key required.

**Key characteristics:**

- **Model:** Silero VAD v4/v5 ONNX, loaded once at startup
- **Inference:** Local CPU via ONNX Runtime (requires `libonnxruntime` shared library)
- **Latency:** Sub-millisecond per frame on modern hardware
- **Statefulness:** Each `SessionHandle` maintains its own internal state (ring buffers, smoothing history) so multiple concurrent participant streams are independent

### Detection States

Each call to `ProcessFrame()` returns one of four `VADEventType` values:

| Event | Meaning | Pipeline Action |
|---|---|---|
| `VADSpeechStart` | Speech just began | Begin buffering audio frames for the engine |
| `VADSpeechContinue` | Ongoing speech | Continue accumulating frames |
| `VADSpeechEnd` | Speech just ended | Dispatch accumulated audio to the engine |
| `VADSilence` | No speech detected | Discard frame / keep session idle |

The transition from `VADSilence` to `VADSpeechStart` requires the speech probability to exceed `SpeechThreshold`. The reverse transition from active speech to `VADSpeechEnd` requires the probability to drop below `SilenceThreshold` -- providing hysteresis that prevents flickering on borderline frames.

### Configuration and Tuning

```go
cfg := vad.Config{
    SampleRate:       16000, // must match incoming PCM
    FrameSizeMs:      30,    // 10, 20, or 30 ms
    SpeechThreshold:  0.5,   // probability to start speech
    SilenceThreshold: 0.35,  // probability to end speech
}
session, err := vadEngine.NewSession(cfg)
```

**Tuning guidelines:**

| Parameter | Lower Value | Higher Value |
|---|---|---|
| `SpeechThreshold` | More sensitive -- catches quiet speech, more false positives | Less sensitive -- misses soft speech, fewer false starts |
| `SilenceThreshold` | Longer speech segments -- waits through pauses | Shorter segments -- cuts off mid-pause, more fragmented |
| `FrameSizeMs` | Finer granularity, more CPU | Coarser granularity, less CPU |

**Recommended starting values:** `SpeechThreshold: 0.5`, `SilenceThreshold: 0.35`, `FrameSizeMs: 30`. These defaults work well for typical TTRPG voice sessions where players speak clearly into microphones.

For noisy environments (background music, fan noise), increase `SpeechThreshold` to 0.6-0.7. For quiet, deliberate speakers, lower `SpeechThreshold` to 0.4.

---

## :gear: Engine Types

The `VoiceEngine` interface is the core abstraction over the AI processing pipeline. All engines implement the same interface, making them interchangeable per NPC:

```go
type VoiceEngine interface {
    Process(ctx context.Context, input AudioFrame, prompt PromptContext) (*Response, error)
    InjectContext(ctx context.Context, update ContextUpdate) error
    SetTools(tools []llm.ToolDefinition) error
    OnToolCall(handler func(name, args string) (string, error))
    Transcripts() <-chan memory.TranscriptEntry
    Close() error
}
```

### :chains: Cascaded Engine (STT -> LLM -> TTS)

**Package:** `internal/engine/cascade/` (sentence cascade variant) and the standard cascaded pipeline

The cascaded engine breaks voice processing into three explicit stages, each backed by an independent provider:

1. **STT** -- transcribes player audio to text (e.g., Deepgram, Whisper)
2. **LLM** -- generates NPC dialogue from the transcript + context (e.g., GPT-4o, Claude Sonnet)
3. **TTS** -- synthesises the NPC's response to audio (e.g., ElevenLabs, Coqui)

**How it works:**
- The agent assembles a `PromptContext` (system prompt, hot context, conversation history, budget tier)
- `Process()` sends the transcript to the LLM with tool definitions gated by the MCP budget tier
- LLM tokens stream back via a Go channel; sentence boundaries trigger incremental TTS synthesis
- The `Response.Audio` channel streams audio chunks as they are synthesised -- playback begins before the LLM finishes generating

**Strengths:** Maximum flexibility -- each provider can be swapped independently. Full control over voice selection, model choice, and tool calling. Keyword boosting for fantasy proper nouns in STT.

**Trade-off:** Highest latency of the three engines due to three sequential network hops (STT + LLM + TTS).

### :zap: Speech-to-Speech Engine (S2S)

**Package:** `internal/engine/s2s/`

The S2S engine wraps a speech-to-speech provider (e.g., OpenAI Realtime, Gemini Live) that handles recognition, generation, and synthesis in a single API call.

**How it works:**
- Lazily opens an S2S session on the first `Process()` call; reconnects transparently if the session dies
- Audio frames are sent directly via `session.SendAudio()`
- Context is injected via `session.UpdateInstructions()` and `session.InjectTextContext()`
- Response audio streams back on `session.Audio()` and is forwarded to a per-turn channel
- A silence timeout (`defaultTurnTimeout: 2s`) detects end-of-turn when no audio arrives
- Tools are forwarded to the session via `session.SetTools()` and executed via the registered tool handler

**Strengths:** Lowest latency -- a single network hop replaces three. The model handles voice natively.

**Trade-off:** Less control over voice characteristics, model selection, and STT quality. No keyword boosting for fantasy names. S2S transcripts require more aggressive correction.

### :test_tube: Sentence Cascade Engine (Experimental)

**Package:** `internal/engine/cascade/`

> [!WARNING]
> **Experimental.** This is a novel technique not implemented in any known production system. It is inspired by speculative decoding and model cascading research but operates at the sentence level. Significant prototyping is required to validate coherence, latency gains, and the conditions under which it outperforms a single-model approach.

The sentence cascade reduces **perceived** latency by starting TTS playback with a fast model's opening sentence while a stronger model generates the substantive continuation.

**How it works:**

1. Player finishes speaking; STT finalises the transcript
2. **Fast model** (e.g., GPT-4o-mini, Gemini Flash) generates only the first sentence (~200 ms TTFT)
3. TTS starts immediately on the first sentence -- voice onset within ~500 ms
4. **Strong model** (e.g., Claude Sonnet, GPT-4o) receives the same prompt plus the fast model's first sentence as a forced assistant-role continuation prefix
5. Strong model's output streams to TTS sentence-by-sentence -- seamless single utterance

**Single-model fast path:** If the fast model's entire response is one sentence (detected via `FinishReason`), the strong model is skipped entirely. This avoids unnecessary overhead for simple greetings.

**Sentence boundary detection:** Sentences are split at `.`, `!`, or `?` followed by whitespace. Partial sentences are flushed when the stream ends.

**Strengths:** Sub-600 ms perceived latency for complex responses. The opening reaction sounds natural ("Ah, the goblins!") while the strong model assembles the real answer.

**Trade-off:** Approximately doubles LLM cost per utterance. Risk of coherence/tone mismatch between models. Only valuable for latency-critical, complex interactions.

### Engine Comparison

| Aspect | Cascaded (STT->LLM->TTS) | Speech-to-Speech (S2S) | Sentence Cascade |
|---|---|---|---|
| **Latency (end-to-end)** | 1.5--3s | 0.5--1.5s | 0.5--1s perceived |
| **Voice quality** | High (dedicated TTS) | Model-dependent | High (dedicated TTS) |
| **Voice control** | Full (any TTS voice) | Limited (model voices) | Full (any TTS voice) |
| **Model flexibility** | Any STT + LLM + TTS | S2S providers only | Any 2 LLMs + TTS |
| **Tool calling** | Full MCP support | Provider-dependent | Strong model only |
| **Keyword boosting** | Yes (STT level) | No | Yes (STT level) |
| **Cost per utterance** | Moderate | Low--moderate | ~2x LLM cost |
| **Complexity** | Low | Low | High |
| **Status** | Production | Production | Experimental |

---

## :control_knobs: Audio Mixer

**Package:** `pkg/audio/mixer/`

The audio mixer sits between engine outputs and the platform transport's single output stream. Discord limits a bot to one outbound audio stream per guild, so when multiple NPCs need to speak, the mixer serialises their output through a priority queue.

### Priority Queue

The `PriorityMixer` uses a max-heap (`container/heap`) ordered by priority (descending) with FIFO tie-breaking on insertion sequence:

```go
mixer := mixer.New(outputCallback,
    mixer.WithGap(300 * time.Millisecond),
    mixer.WithQueueCapacity(16),
)
```

**Scheduling rules:**

| Rule | Behaviour |
|---|---|
| **Priority ordering** | Higher-priority segments play first. DM-designated NPCs get elevated priority. |
| **FIFO within priority** | Equal-priority segments play in insertion order. |
| **Preemption** | Enqueueing a segment with higher priority than the currently playing one immediately interrupts it with `DMOverride` semantics. |
| **Streaming playback** | Segments stream incrementally -- the mixer begins playback before the entire segment is synthesised. Each `AudioSegment.Audio` channel delivers `[]byte` chunks as they arrive. |

### Inter-Segment Gaps

A configurable silence gap (default: 300 ms) is inserted between consecutive segments to simulate natural turn-taking:

- **Jitter:** +/- 1/6 of the base gap is applied randomly to prevent robotic timing
- **Zero gap:** Setting the gap to zero plays segments back-to-back
- **Runtime adjustment:** `SetGap(d)` changes the gap before the next segment starts

### Barge-In Detection and Behaviour

When a player starts speaking while an NPC is still outputting audio:

1. **VAD detects player speech** on an input stream during NPC playback
2. **`BargeIn(speakerID)` is called** on the mixer
3. **Current segment is interrupted** with `PlayerBargeIn` semantics
4. **Queue is cleared** -- all pending NPC segments are drained (the conversation context has changed)
5. **Barge-in handler fires** -- the registered callback receives the interrupting player's ID on a new goroutine
6. **Player's speech is routed** to the addressed NPC's engine for processing

**Interrupt reasons:**

| Reason | Behaviour |
|---|---|
| `PlayerBargeIn` | Hard cut current segment + **clear entire queue** (player took the floor) |
| `DMOverride` | Hard cut current segment, **preserve queue** (DM injected priority speech) |

### Mixer Interface

```go
type Mixer interface {
    Enqueue(segment *AudioSegment, priority int)
    Interrupt(reason InterruptReason)
    OnBargeIn(handler func(speakerID string))
    SetGap(d time.Duration)
}
```

---

## :speech_balloon: Utterance Buffer

**Package:** `internal/agent/orchestrator/`

The `UtteranceBuffer` provides **cross-NPC awareness** -- each NPC can see what other NPCs and players have said recently, enabling coherent multi-NPC scenes (e.g., a tavern with three NPCs who reference each other's dialogue).

### How It Works

1. Every utterance (player or NPC) is added to the shared buffer via `Add(entry)`
2. Before routing a new utterance to an NPC, the orchestrator calls `Recent(excludeNPCID, maxEntries)` to retrieve recent cross-NPC context
3. These entries are injected into the target NPC's engine via `InjectContext()` so the NPC's next response reflects what others have said
4. The NPC's own utterances are excluded (via `excludeNPCID`) to avoid self-referential context

### Eviction Policy

The buffer enforces two limits:

| Limit | Default | Purpose |
|---|---|---|
| **Max entries** | 20 | Bounds memory usage |
| **Max age** | 5 minutes | Ensures only recent, relevant context is injected |

Eviction runs on every `Add()` call. Surviving entries are copied to a fresh backing array to prevent evicted entries from pinning memory.

### Buffer Entry Structure

```go
type BufferEntry struct {
    SpeakerID   string    // player user-ID or NPC agent ID
    SpeakerName string    // human-readable name
    Text        string    // utterance text
    NPCID       string    // non-empty when produced by an NPC
    Timestamp   time.Time // when the utterance occurred
}
```

### Configuration

```go
buffer := orchestrator.NewUtteranceBuffer(maxSize, maxAge)

// Or via orchestrator options:
orch := orchestrator.New(agents,
    orchestrator.WithBufferSize(30),
    orchestrator.WithBufferDuration(10 * time.Minute),
)
```

---

## :bar_chart: Choosing an Engine

Use this decision guide to select the right engine for each NPC:

| Criterion | Cascaded (STT->LLM->TTS) | Speech-to-Speech (S2S) | Sentence Cascade |
|---|---|---|---|
| **Best for** | Most NPCs, full control | Low-latency, simple NPCs | High-importance, complex NPCs |
| **Latency** | 1.5--3s | 0.5--1.5s | ~0.5s perceived |
| **Voice quality** | :star: :star: :star: Dedicated TTS voices | :star: :star: Model-dependent | :star: :star: :star: Dedicated TTS voices |
| **Response quality** | :star: :star: :star: Any LLM | :star: :star: S2S model only | :star: :star: :star: Strong model continuation |
| **Cost** | $$ | $ -- $$ | $$$ (~2x LLM) |
| **Tool calling** | Full MCP tool support | Provider-dependent | Strong model only |
| **Fantasy name handling** | Good (STT keyword boost) | Poor (no boost) | Good (STT keyword boost) |
| **Configuration complexity** | Low (3 providers) | Low (1 provider) | High (2 LLMs + TTS + STT) |
| **Maturity** | Production | Production | Experimental |

### Decision Flowchart

```
Is sub-second latency critical for this NPC?
├─ No  ──> Use Cascaded (best flexibility and quality)
├─ Yes
│   ├─ Are complex, multi-sentence responses expected?
│   │   ├─ Yes ──> Consider Sentence Cascade (experimental)
│   │   └─ No  ──> Use S2S (simplest low-latency option)
│   └─ Is fine voice control needed?
│       ├─ Yes ──> Use Cascaded or Sentence Cascade
│       └─ No  ──> Use S2S
```

**Rules of thumb:**

- **Default choice:** Cascaded. It offers the best balance of quality, flexibility, and debuggability.
- **Combat callouts, simple greetings:** S2S for lowest latency when response quality is less critical.
- **Quest-givers, villain monologues, deep lore:** Sentence cascade (if willing to accept experimental status and higher cost).
- **Budget-constrained:** S2S or cascaded with a fast LLM (GPT-4o-mini, Gemini Flash).

---

## :book: See Also

- [architecture.md](architecture.md) -- system-level architecture overview
- [providers.md](providers.md) -- provider configuration and supported backends
- [configuration.md](configuration.md) -- full configuration reference
- [design/01-architecture.md](design/01-architecture.md) -- detailed architecture design document
- [design/05-sentence-cascade.md](design/05-sentence-cascade.md) -- sentence cascade design rationale and research context
