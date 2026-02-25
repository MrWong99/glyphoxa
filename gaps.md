# Gap Analysis: Research Findings vs Design Documents

Gaps identified between the `research/` findings and the current `docs/design/` documents. These should be addressed as implementation progresses.

## 1. No Phonetic Matching Go Library Specified

`03-memory.md` describes Double Metaphone for transcript correction but names no Go package. Need to identify one (e.g., `go-phonetic` or `flier/gosfm`).

## 2. No Embeddings Provider Interface

`02-providers.md` defines LLM, STT, TTS, Audio, S2S, and VoiceEngine interfaces but has no `EmbeddingsProvider` interface. The provider stack lists OpenAI text-embedding-3-small but there's no abstraction for it.

## 3. No VAD Interface

`01-architecture.md` and `07-technology.md` reference Silero VAD with a Go binding, but `02-providers.md` has no `VADEngine` interface definition. It's listed in the architecture table but never fleshed out.

## 4. Broken Links to Voice Engine Design

`01-architecture.md`, `02-providers.md`, `03-memory.md`, `06-npc-agents.md`, `07-technology.md`, and `08-open-questions.md` reference `../plans/2026-02-25-voice-engine-design.md` which has been deleted. These links need updating or the doc needs to be restored/replaced.

## 5. Pre-session Entity Registration UX Undefined

Research 05 and `03-memory.md` mention pre-session DM entity registration as critical for keyword boosting, but no design doc describes the UX (Discord slash commands? Web form? VTT import?).

## 6. Audio Mixing Layer Undocumented

Research 03 provides PCM mixing code. `01-architecture.md` mentions a single output stream limitation but no design doc covers the audio mixer component.

## 7. Cost Analysis Absent from Design

Research 02 has detailed per-minute pricing for S2S vs cascaded. Design docs don't include cost considerations for provider selection or user-facing pricing tiers.

## 8. S2S Model Names May Be Outdated

Research 02 used pre-GA model names. Design uses `gpt-4o-mini-realtime`. OpenAI Realtime GA shipped August 2025 with `gpt-realtime` and `gpt-realtime-1.5`. Should verify current model IDs.

## 9. No `cmd/` Directory

README shows `go build ./cmd/glyphoxa` but there was no `cmd/` directory in the project. Standard Go layout expects `cmd/glyphoxa/main.go`. (Now added as a stub.)

## 10. `10-knowledge-graph.md` Was Missing from README

README listed docs 00-09 but the new doc 10 was never added. (Now fixed.)
