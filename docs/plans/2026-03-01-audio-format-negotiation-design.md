# Audio Pipeline Format Negotiation

**Date:** 2026-03-01
**Status:** Approved
**Packages:** `pkg/audio/`, `pkg/provider/tts/`, `pkg/audio/discord/`, `pkg/audio/webrtc/`

## Problem

The alpha audio fix (resampling + mono-to-stereo + Opus framing) is wired through
provider-specific and platform-specific code rather than a shared layer:

- **Resampling** lives inside the Coqui provider (`WithOutputSampleRate`). Other
  TTS providers would need their own resampling, duplicating logic.
- **Mono-to-stereo conversion + Opus framing** lives in the Discord `sendLoop`.
  WebRTC has a separate output path that would need identical treatment.
- **`AudioSegment.Audio` (`<-chan []byte`)** carries no format metadata. Downstream
  consumers must assume a fixed format, which breaks when a different TTS model or
  provider emits a different rate.
- **Input path** (Discord 48kHz stereo to 16kHz mono for STT/VAD) has implicit
  format assumptions with no validation.

## Approach: Centralized Format Converter

Introduce an `audio.FormatConverter` that sits at pipeline boundaries, handling
both the output path (TTS to platform) and input path (platform to STT/VAD).

Key principles:
- Format metadata on `AudioSegment` so downstream code can inspect rather than assume
- Single shared converter component for all resampling and channel conversion
- Log-and-convert on format mismatch (warn once per stream, not per frame)
- Keep linear interpolation for resampling; upgrade to sinc/polyphase later

## Design

### 1. Core Type Changes

**AudioSegment** gains format metadata:

```go
type AudioSegment struct {
    NPCID      string
    Audio      <-chan []byte
    SampleRate int            // Hz of the PCM data on Audio channel
    Channels   int            // 1=mono, 2=stereo
    Priority   int
    streamErr  atomic.Pointer[error]
}
```

**AudioFrame** already carries `SampleRate` and `Channels` -- no changes needed.

**Mixer output signature** changes from `func([]byte)` to `func(AudioFrame)`.
The mixer's `play()` goroutine wraps each chunk with the segment's format fields.

### 2. FormatConverter

Stateless utility in `pkg/audio/convert.go`:

```go
type Format struct {
    SampleRate int  // Hz
    Channels   int  // 1 or 2
}

type FormatConverter struct {
    Target Format
    warned sync.Once
}

func (c *FormatConverter) Convert(frame AudioFrame) AudioFrame
```

**Conversion order:** resample first, then channel convert. This avoids
resampling stereo data when the target is mono.

**Operations** (all moved/consolidated into this file):
- `resampleMono16(pcm, srcRate, dstRate)` -- moved from Coqui provider
- `resampleStereo16(pcm, srcRate, dstRate)` -- new, same algorithm per-channel
- `monoToStereo(pcm)` -- moved from Discord opus.go
- `stereoToMono(pcm)` -- new, averages L+R with int16 clamping

**No-op fast path:** if source format matches target, return frame unchanged
with zero allocation.

**Warning logging:** `sync.Once` logs a `slog.Warn` on first mismatch:
`"audio format mismatch: converting from 22050Hz mono to 48000Hz stereo"`

**Stream helper:**

```go
func ConvertStream(in <-chan AudioFrame, target Format) <-chan AudioFrame
```

Spawns a goroutine that reads, converts, and forwards. Closes when `in` closes.

### 3. Output Path Integration

```
TTS Provider (any rate, mono)
  | stamps SampleRate/Channels on AudioSegment
AudioSegment enqueued to Mixer
  | play() wraps chunks as AudioFrame with segment's format
output callback receives AudioFrame
  |
FormatConverter (target: platform format)
  |
Platform adapter (Discord sendLoop / WebRTC forwardOutput)
```

**TTS providers:** Set `AudioSegment.SampleRate`/`Channels` from WAV header or
API response. No longer resample themselves.

**Mixer:** `play()` wraps each `[]byte` chunk as `AudioFrame` with the segment's
format. Validates `SampleRate > 0` and `Channels > 0` on enqueue; rejects
segments with missing format (programming bug).

**Discord sendLoop:** Creates `FormatConverter{Target: Format{48000, 2}}`.
Converts each frame before buffering and Opus encoding.

**WebRTC forwardOutput:** Creates converter with platform's configured format.
Converts each frame before `SendAudio()`.

### 4. Input Path Integration

```
Platform adapter (Discord recvLoop / WebRTC readPeerInput)
  | stamps AudioFrame with platform format
Per-participant input channel
  |
ConvertStream(in, Format{16000, 1})
  |
VAD / STT consumer
```

**Platform adapters:** Already stamp AudioFrame with correct format -- no change.

**Consumer side:** Where the orchestrator reads from `Connection.InputStreams()`,
wrap each stream with `ConvertStream(in, Format{16000, 1})`.

**stereoToMono:** Averages left and right channels: `out[i] = (L[i] + R[i]) / 2`
with clamping to prevent int16 overflow.

### 5. Validation

- **Per-frame:** FormatConverter logs warning on first mismatch per converter
  instance (`sync.Once`), then silently converts.
- **Corrupt data:** Odd byte count for int16 PCM logs warning and drops the frame.
- **Segment-level:** Mixer rejects segments with `SampleRate <= 0` or `Channels <= 0`.

## Changes Summary

### New code

| File | Contents |
|------|----------|
| `pkg/audio/convert.go` | `Format`, `FormatConverter`, `ConvertStream`, `resampleMono16`, `resampleStereo16`, `monoToStereo`, `stereoToMono` |

### Modified code

| File | Changes |
|------|---------|
| `pkg/audio/types.go` or `mixer.go` | Add `SampleRate`, `Channels` to `AudioSegment` |
| `pkg/audio/mixer/mixer.go` | Output `func([]byte)` to `func(AudioFrame)`. Wrap chunks in `play()`. Validate on enqueue. |
| `pkg/audio/discord/connection.go` | `sendLoop` uses `FormatConverter` targeting 48kHz stereo |
| `pkg/audio/discord/opus.go` | Remove `monoToStereo` (moved to converter) |
| `pkg/audio/webrtc/connection.go` | `forwardOutput` uses `FormatConverter` targeting platform format |
| `pkg/provider/tts/coqui/coqui.go` | Set segment format from WAV header. Remove `WithOutputSampleRate`, `resampleMono16`, resampling logic. |
| Orchestrator / NPC engine | Wrap input channels with `ConvertStream(in, Format{16000, 1})` |

### Removed code

- `pkg/provider/tts/coqui/coqui.go`: `WithOutputSampleRate`, `resampleMono16`, resampling conditionals
- `pkg/audio/discord/opus.go`: `monoToStereo`

## Future Work

- **Resampler quality upgrade:** Replace linear interpolation with windowed-sinc
  or polyphase interpolation for higher audio fidelity. Current linear
  interpolation is acceptable for voice but may introduce aliasing artifacts
  when downsampling aggressively (e.g., 48kHz to 16kHz).
