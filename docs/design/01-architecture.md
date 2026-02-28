---
parent: Design Documents
nav_order: 1
---

> *This document is derived from the Glyphoxa Design Document v0.2*

# System Architecture

Glyphoxa follows a layered architecture with strict separation between the audio transport, AI processing pipeline, memory subsystem, and tool execution layer. Each layer communicates through Go interfaces and channels, enabling independent evolution and provider substitution.

## Architectural Layers

| Layer | Responsibility | Key Interfaces |
|---|---|---|
| Audio Transport | Captures voice from platform (Discord, custom WebRTC), manages per-speaker audio streams, plays TTS audio back | `AudioPlatform`, `AudioStream` |
| Voice Engine | Unifying abstraction over cascaded (STT→LLM→TTS) and speech-to-speech (S2S) pipelines. Each NPC declares its engine type. | `VoiceEngine`, `CascadedEngine`, `S2SEngine` |
| Speech Pipeline | STT transcription of incoming streams, TTS synthesis of outgoing NPC speech, voice activity detection. Used by `CascadedEngine`. | `STTProvider`, `TTSProvider`, `VADEngine` |
| S2S Pipeline | Single-API audio-in/audio-out processing via OpenAI Realtime or Gemini Live. Used by `S2SEngine`. | `S2SProvider`, `S2SSession` |
| Agent Orchestrator | Routes speech to the correct NPC agent, manages turn-taking, concurrent NPC conversations, DM commands, engine lifecycle, and tool budget enforcement | `AgentRouter`, `NPCAgent` |
| LLM Core | Generates NPC dialogue, answers questions, processes tool calls. Provider-agnostic abstraction over multiple LLM backends. Used by `CascadedEngine`. | `LLMProvider`, `CompletionRequest` |
| Memory Subsystem | Three-layer hybrid: hot layer (pre-woven identity + recent transcript), cold layer (MCP-invocable deep search), knowledge graph | `MemoryStore`, `KnowledgeGraph`, `EmbeddingsProvider`, `HotContext` |
| Tool Execution (MCP) | Performance-budgeted tool registry. Tools declare latency estimates. Orchestrator controls which tools are visible per engine via budget tiers. | `MCPHost`, `MCPTool`, `ToolBudget` |

## High-Level Data Flow

After Audio Capture and VAD, the data flow forks based on the NPC's `VoiceEngine` type. Both paths converge at Memory Write-back.

### 1. Audio Capture (shared)
The Audio Transport layer captures per-speaker audio streams from the voice platform via a channel per participant. VAD segments speech from silence using Silero (local, no network hop).

### Cascaded Path (CascadedEngine)

Steps 2–8 execute within `CascadedEngine`. This is the full pipeline with maximum flexibility over voice, model, and tool selection.

### 2. STT Transcription
Audio chunks stream to the STT provider in real-time over a goroutine-managed WebSocket. The STT provider receives a keyword boost list populated from the knowledge graph (L3) — all known entity names are boosted to improve recognition of fantasy proper nouns. Partial transcripts arrive as the speaker talks; a final transcript is emitted when the utterance ends.

### 2b. Phonetic Entity Match (Inline)
On the final transcript, a phonetic matching step corrects misheard entity names against the known entity list (< 1ms). This produces a cleaner transcript for both speculative pre-fetch and LLM prompt composition. See [Memory: Transcript Correction](03-memory.md#transcript-correction-pipeline).

### 3. Speculative Memory Pre-fetch
As STT partials arrive, a lightweight keyword extractor fires. If the partial contains entity names or temporal references ("last time," "the blacksmith"), a cold-layer vector search starts in parallel. Results are ready before the LLM prompt is assembled. The phonetic match from step 2b improves pre-fetch hit rates for misheard names. See [Memory: Speculative Pre-Fetch](03-memory.md#speculative-pre-fetch-bridging-the-gap).

### 4. Hot Context Assembly
The orchestrator assembles the NPC's hot context: identity from the knowledge graph (personality, relationships, emotional state) + the last ~5 minutes of session transcript. This is always available, never requires an LLM round-trip. See [Memory: Hot Layer](03-memory.md#hot-layer-always-available-npc-context).

### 5. Prompt Composition
Hot context + any speculative pre-fetch results + the player's utterance are composed into the LLM prompt. MCP tool definitions are injected as function definitions — only tools that fit within the active [budget tier](04-mcp-tools.md#orchestrator-enforced-budget-tiers) are included. See [MCP Tools](04-mcp-tools.md).

### 6. LLM Inference
The enriched prompt streams to the LLM. Tokens stream back via a Go channel. If the LLM invokes a tool (MCP), the tool executes and results feed back before the final response is composed.

### 7. TTS Synthesis
Response text streams to the TTS provider sentence-by-sentence as tokens arrive. Audio chunks stream back as they are generated.

### 8. Audio Playback
TTS audio is injected into the voice channel through the Audio Transport's output stream. The NPC's voice is heard by all participants.

### S2S Path (S2SEngine)

The S2S path replaces steps 2–8 with a single API call. Audio goes directly to the S2S provider (OpenAI Realtime or Gemini Live), which handles recognition, generation, and synthesis internally. See [Providers: VoiceEngine](02-providers.md#voiceengine-interface).

### S2S-2. Audio Forwarding
Raw audio from Audio Capture is forwarded to the S2S session via `SendAudio`. The provider handles speech recognition internally.

### S2S-3. S2S Processing
The S2S model generates and synthesizes the NPC response. If the model calls a tool (declared via `SetTools`), the `ToolCallHandler` executes the corresponding MCP tool and returns the result. Audio streams back through the `Audio()` channel.

### S2S-4. Audio Playback
S2S audio is played through the Audio Transport's output stream, identical to step 8 in the cascaded path.

### 9. Memory Write-back (shared)
The complete exchange (player utterance + NPC response) is written to the session transcript (L1). A background goroutine runs the [transcript correction pipeline](03-memory.md#transcript-correction-pipeline) (phonetic match + LLM correction for misheard entity names) and then queues entity extraction for the knowledge graph (L3). Both engine types emit transcript entries through the same `Transcripts()` channel — the memory system does not distinguish between them, though S2S transcripts receive more aggressive correction since they lack keyword boosting and word-level confidence data.

## Audio Mixing Layer

Discord limits a single bot to one outbound audio stream per guild. When multiple NPCs need to speak (e.g., a tavern scene with three NPCs), Glyphoxa must serialize their output through a priority queue.

### Output Queue

The Audio Mixer sits between the VoiceEngine outputs and the Audio Transport's single output stream. It manages:

| Concern | Approach |
|---|---|
| **Queueing** | Each NPC's audio output is enqueued as a speech segment. The mixer plays segments sequentially — one NPC finishes before the next begins. |
| **Priority** | The DM's designated NPC (e.g., "the quest giver is speaking") gets priority. Otherwise, priority is based on: (1) directly addressed NPCs first, (2) conversation relevance score, (3) FIFO order. |
| **Interruption** | The DM can interrupt any NPC mid-speech via voice command or `/npc mute`. The current segment is truncated and the next queued segment plays. Player speech also interrupts NPC output (barge-in). |
| **Natural pacing** | A configurable silence gap (200–500ms) is inserted between NPC segments to simulate natural turn-taking. A small random jitter (±50ms) prevents robotic timing. |
| **Mixing (future)** | If multi-bot support is added (one bot per NPC), true PCM mixing replaces the queue. Audio frames from concurrent bots are summed and clipped. This path is deferred — single-bot sequential output is the MVP. |

### Barge-in Detection

When a player starts speaking while an NPC is still outputting audio, the mixer triggers a barge-in:

1. VAD detects player speech on an input stream.
2. The mixer truncates the current NPC's output segment.
3. The player's speech is routed to the addressed NPC's VoiceEngine for processing.
4. Any queued NPC segments are discarded (the conversation context has changed).

## Streaming is Non-Negotiable

Every stage of the cascaded pipeline must support streaming. The TTS starts synthesizing before the LLM finishes generating. The audio transport starts playing before the TTS finishes the utterance. Go's channel-based concurrency makes this natural: each pipeline stage is a goroutine reading from an input channel and writing to an output channel. Without end-to-end streaming, the 1–2 second latency target is impossible.

S2S engines stream natively — audio chunks arrive as the model generates speech. The same channel-based playback applies.

---

**See also:** [Overview](00-overview.md) · [Providers](02-providers.md) · [Memory](03-memory.md) · [MCP Tools](04-mcp-tools.md) · [Technology](07-technology.md)
