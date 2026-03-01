---
parent: Design Documents
nav_order: 11
---

## 6. OpenAI Realtime: Server `error` Events Silently Dropped

**Package:** `pkg/provider/s2s/openai/`

The OpenAI Realtime API emits `{"type":"error","error":{...}}` for non-fatal issues
(e.g., unintelligible audio, rate limits). These currently fall through the
`handleServerEvent` switch unhandled — the session continues but the caller has no
visibility into the error.

**Options:**
- A. Add a dedicated `Errors() <-chan error` to `s2s.SessionHandle` (interface change)
- B. Add an `OnError(func(error))` callback (mirror `OnToolCall` pattern)
- C. Surface errors via the existing `Err()` method (only available after channel close — not useful for transient errors)
- D. Log-and-ignore (acceptable for alpha, revisit for beta)

**Recommendation:** Option B — minimal interface change, consistent with `OnToolCall` callback pattern.

## 7. WebRTC: `OutputStream()` Channel Not Closed on `Disconnect()`

**Package:** `pkg/audio/webrtc/`

The `audio.Connection` interface doc says "all channels returned by Connection
methods are closed automatically when the connection terminates." The write-only
`outputCh` returned by `OutputStream()` is never closed on `Disconnect()`.

Closing it from `Disconnect()` would panic any caller still writing after
disconnect. This is a channel ownership question: write-only channels are
conventionally closed by the writer (the caller), not the reader (the platform).

**Options:**
- A. Close `outputCh` in `Disconnect()` — simple but panics on late writes
- B. Use a wrapper that converts writes-after-close to a no-op (recover from panic or check `disconnected` flag)
- C. Update the interface doc to clarify that write-only channels are caller-owned and not closed by the platform
- D. Return a struct with `Send(frame)` + `Close()` methods instead of a bare channel

**Recommendation:** Option C for now (doc fix), Option D for v1 (richer API).

## 8. Audio Pipeline Format Negotiation ✅

**Decision:** Centralized `FormatConverter` with metadata on `AudioSegment`.
See `docs/plans/2026-03-01-audio-format-negotiation-design.md`.

## 9. Resampler Quality Upgrade

**Package:** `pkg/audio/`

The `FormatConverter` uses linear interpolation for resampling. This is fast and
adequate for voice audio but may introduce aliasing artifacts when downsampling
aggressively (e.g., 48 kHz → 16 kHz). A windowed-sinc or polyphase resampler
would improve fidelity.

**Options:**
- A. Use a Go resampling library (e.g., `go-audio/transforms` or similar)
- B. Implement windowed-sinc interpolation in pure Go (no external dependency)

**Priority:** Low — revisit when audio quality becomes a user-facing concern.
