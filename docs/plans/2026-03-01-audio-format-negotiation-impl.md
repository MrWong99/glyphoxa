# Audio Format Negotiation — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Centralize all audio format conversion (resampling + channel remix) into a shared `FormatConverter`, removing scattered conversion logic from providers and platform adapters.

**Architecture:** A new `audio.FormatConverter` sits at pipeline boundaries. The mixer output changes from `func([]byte)` to `func(AudioFrame)`, stamping format metadata from `AudioSegment`. Providers set format metadata instead of resampling; platforms create converters targeting their required format.

**Tech Stack:** Go standard library only (`sync`, `log/slog`, `math`). No new dependencies.

---

### Task 1: Add `SampleRate` and `Channels` to `AudioSegment`

**Files:**
- Modify: `pkg/audio/mixer.go:39-57`
- Modify: `pkg/audio/mixer/mixer_test.go:15-38` (test helpers)

**Step 1: Write the failing test**

Add a test in `pkg/audio/mixer/mixer_test.go` that creates an `AudioSegment` with format fields and asserts they're accessible:

```go
func TestAudioSegment_FormatFields(t *testing.T) {
	ch := make(chan []byte)
	close(ch)
	seg := &audio.AudioSegment{
		NPCID:      "npc-1",
		Audio:      ch,
		SampleRate: 22050,
		Channels:   1,
		Priority:   5,
	}
	if seg.SampleRate != 22050 {
		t.Fatalf("SampleRate = %d, want 22050", seg.SampleRate)
	}
	if seg.Channels != 1 {
		t.Fatalf("Channels = %d, want 1", seg.Channels)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./pkg/audio/mixer/ -run TestAudioSegment_FormatFields -v`
Expected: FAIL — `audio.AudioSegment` has no field `SampleRate`

**Step 3: Add the fields to AudioSegment**

In `pkg/audio/mixer.go`, add `SampleRate` and `Channels` fields to the `AudioSegment` struct after the `Audio` field (line ~47):

```go
// SampleRate is the sample rate in Hz of the PCM data on the Audio channel
// (e.g., 22050, 44100, 48000). Must be > 0.
SampleRate int

// Channels is the number of audio channels (1 = mono, 2 = stereo).
// Must be > 0.
Channels int
```

**Step 4: Run test to verify it passes**

Run: `go test ./pkg/audio/mixer/ -run TestAudioSegment_FormatFields -v`
Expected: PASS

**Step 5: Update test helpers**

Update `makeSegment` and `makeOpenSegment` in `pkg/audio/mixer/mixer_test.go` to accept and set `SampleRate` and `Channels`. Use 48000/1 as defaults so existing tests don't break:

```go
func makeSegment(npcID string, priority int, chunks ...[]byte) *audio.AudioSegment {
	ch := make(chan []byte, len(chunks))
	for _, c := range chunks {
		ch <- c
	}
	close(ch)
	return &audio.AudioSegment{
		NPCID:      npcID,
		Audio:      ch,
		SampleRate: 48000,
		Channels:   1,
		Priority:   priority,
	}
}

func makeOpenSegment(npcID string, priority int) (*audio.AudioSegment, chan []byte) {
	ch := make(chan []byte, 16)
	seg := &audio.AudioSegment{
		NPCID:      npcID,
		Audio:      ch,
		SampleRate: 48000,
		Channels:   1,
		Priority:   priority,
	}
	return seg, ch
}
```

**Step 6: Run all mixer tests**

Run: `go test ./pkg/audio/mixer/ -v`
Expected: All PASS

**Step 7: Commit**

```bash
git add pkg/audio/mixer.go pkg/audio/mixer/mixer_test.go
git commit -m "feat(audio): add SampleRate and Channels fields to AudioSegment"
```

---

### Task 2: Create `FormatConverter` with conversion functions

**Files:**
- Create: `pkg/audio/convert.go`
- Create: `pkg/audio/convert_test.go`

**Step 1: Write failing tests for conversion functions**

Create `pkg/audio/convert_test.go`:

```go
package audio_test

import (
	"testing"

	"github.com/MrWong99/glyphoxa/pkg/audio"
)

// helper: encode int16 samples as little-endian bytes
func samplesToBytes(samples []int16) []byte {
	b := make([]byte, len(samples)*2)
	for i, s := range samples {
		b[i*2] = byte(s)
		b[i*2+1] = byte(s >> 8)
	}
	return b
}

// helper: decode little-endian bytes back to int16 samples
func bytesToSamples(b []byte) []int16 {
	samples := make([]int16, len(b)/2)
	for i := range samples {
		samples[i] = int16(b[i*2]) | int16(b[i*2+1])<<8
	}
	return samples
}

func TestMonoToStereo(t *testing.T) {
	mono := samplesToBytes([]int16{100, -200, 300})
	stereo := audio.MonoToStereo(mono)
	got := bytesToSamples(stereo)
	// Each mono sample should appear twice (L, R)
	want := []int16{100, 100, -200, -200, 300, 300}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sample[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}

func TestStereoToMono(t *testing.T) {
	// Stereo: L=100 R=200, L=-100 R=-200
	stereo := samplesToBytes([]int16{100, 200, -100, -200})
	mono := audio.StereoToMono(stereo)
	got := bytesToSamples(mono)
	// Average of each pair: (100+200)/2=150, (-100+-200)/2=-150
	want := []int16{150, -150}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sample[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}

func TestStereoToMono_Clamping(t *testing.T) {
	// Two max-positive samples should clamp to max int16, not overflow
	stereo := samplesToBytes([]int16{32767, 32767})
	mono := audio.StereoToMono(stereo)
	got := bytesToSamples(mono)
	if got[0] != 32767 {
		t.Errorf("sample = %d, want 32767 (clamped)", got[0])
	}
}

func TestResampleMono16_SameRate(t *testing.T) {
	in := samplesToBytes([]int16{100, 200, 300})
	out := audio.ResampleMono16(in, 48000, 48000)
	if len(out) != len(in) {
		t.Fatalf("len = %d, want %d", len(out), len(in))
	}
}

func TestResampleMono16_Upsample(t *testing.T) {
	// 2 samples at 16kHz → expect 6 samples at 48kHz
	in := samplesToBytes([]int16{0, 3000})
	out := audio.ResampleMono16(in, 16000, 48000)
	got := bytesToSamples(out)
	if len(got) != 6 {
		t.Fatalf("len = %d, want 6", len(got))
	}
	// First sample should be 0, last should be close to 3000
	if got[0] != 0 {
		t.Errorf("first sample = %d, want 0", got[0])
	}
}

func TestResampleMono16_Downsample(t *testing.T) {
	// 6 samples at 48kHz → expect 2 samples at 16kHz
	in := samplesToBytes([]int16{0, 1000, 2000, 3000, 4000, 5000})
	out := audio.ResampleMono16(in, 48000, 16000)
	got := bytesToSamples(out)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
}

func TestResampleStereo16(t *testing.T) {
	// 2 stereo frames (4 samples) at 16kHz → 6 stereo frames at 48kHz
	in := samplesToBytes([]int16{100, 200, 300, 400})
	out := audio.ResampleStereo16(in, 16000, 48000)
	got := bytesToSamples(out)
	// 6 frames * 2 channels = 12 samples
	if len(got) != 12 {
		t.Fatalf("len = %d, want 12", len(got))
	}
}

func TestFormatConverter_NoOp(t *testing.T) {
	conv := audio.FormatConverter{Target: audio.Format{SampleRate: 48000, Channels: 2}}
	frame := audio.AudioFrame{
		Data:       []byte{1, 2, 3, 4},
		SampleRate: 48000,
		Channels:   2,
	}
	out := conv.Convert(frame)
	if &out.Data[0] != &frame.Data[0] {
		t.Error("expected no-op to return same slice (zero allocation)")
	}
}

func TestFormatConverter_MonoToStereo(t *testing.T) {
	conv := audio.FormatConverter{Target: audio.Format{SampleRate: 48000, Channels: 2}}
	mono := samplesToBytes([]int16{1000, 2000})
	frame := audio.AudioFrame{Data: mono, SampleRate: 48000, Channels: 1}
	out := conv.Convert(frame)
	if out.Channels != 2 {
		t.Fatalf("Channels = %d, want 2", out.Channels)
	}
	got := bytesToSamples(out.Data)
	if len(got) != 4 {
		t.Fatalf("len = %d, want 4", len(got))
	}
}

func TestFormatConverter_FullConversion(t *testing.T) {
	// 22050 Hz mono → 48000 Hz stereo
	conv := audio.FormatConverter{Target: audio.Format{SampleRate: 48000, Channels: 2}}
	mono := samplesToBytes([]int16{1000, 2000, 3000, 4000})
	frame := audio.AudioFrame{Data: mono, SampleRate: 22050, Channels: 1}
	out := conv.Convert(frame)
	if out.SampleRate != 48000 {
		t.Fatalf("SampleRate = %d, want 48000", out.SampleRate)
	}
	if out.Channels != 2 {
		t.Fatalf("Channels = %d, want 2", out.Channels)
	}
}

func TestFormatConverter_OddByteCount(t *testing.T) {
	conv := audio.FormatConverter{Target: audio.Format{SampleRate: 48000, Channels: 1}}
	// 3 bytes is invalid for int16 PCM
	frame := audio.AudioFrame{Data: []byte{1, 2, 3}, SampleRate: 48000, Channels: 1}
	out := conv.Convert(frame)
	// Should return empty frame (dropped)
	if len(out.Data) != 0 {
		t.Fatalf("expected empty data for odd byte count, got %d bytes", len(out.Data))
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `go test ./pkg/audio/ -run 'TestMonoToStereo|TestStereoToMono|TestResample|TestFormatConverter' -v`
Expected: FAIL — functions don't exist

**Step 3: Implement the converter**

Create `pkg/audio/convert.go`:

```go
package audio

import (
	"log/slog"
	"sync"
)

// Format describes the PCM audio format (sample rate and channel count).
type Format struct {
	SampleRate int // Hz (e.g., 16000, 22050, 44100, 48000)
	Channels   int // 1 = mono, 2 = stereo
}

// FormatConverter converts AudioFrames to a target Format. It is safe for
// concurrent use from a single goroutine (not designed for shared use across
// goroutines — create one per stream).
type FormatConverter struct {
	Target Format
	warned sync.Once
}

// Convert transforms frame to the converter's target format. If the frame
// is already in the target format, it is returned unchanged (zero allocation).
//
// Conversion order: resample first, then channel convert. This avoids
// resampling stereo data when the target is mono.
//
// Invalid frames (odd byte count for int16 PCM) are logged and returned empty.
func (c *FormatConverter) Convert(frame AudioFrame) AudioFrame {
	// Validate PCM data.
	if len(frame.Data)%2 != 0 {
		c.warned.Do(func() {
			slog.Warn("audio format converter: odd byte count in PCM data, dropping frame",
				"bytes", len(frame.Data),
				"sampleRate", frame.SampleRate,
				"channels", frame.Channels,
			)
		})
		return AudioFrame{
			SampleRate: c.Target.SampleRate,
			Channels:   c.Target.Channels,
			Timestamp:  frame.Timestamp,
		}
	}

	// Fast path: no conversion needed.
	if frame.SampleRate == c.Target.SampleRate && frame.Channels == c.Target.Channels {
		return frame
	}

	c.warned.Do(func() {
		slog.Warn("audio format mismatch: converting",
			"from", formatString(frame.SampleRate, frame.Channels),
			"to", formatString(c.Target.SampleRate, c.Target.Channels),
		)
	})

	data := frame.Data
	srcChannels := frame.Channels
	srcRate := frame.SampleRate

	// Step 1: Resample (if rates differ).
	if srcRate != c.Target.SampleRate {
		if srcChannels == 1 {
			data = ResampleMono16(data, srcRate, c.Target.SampleRate)
		} else {
			data = ResampleStereo16(data, srcRate, c.Target.SampleRate)
		}
		srcRate = c.Target.SampleRate
	}

	// Step 2: Channel conversion (if channels differ).
	if srcChannels != c.Target.Channels {
		if srcChannels == 1 && c.Target.Channels == 2 {
			data = MonoToStereo(data)
		} else if srcChannels == 2 && c.Target.Channels == 1 {
			data = StereoToMono(data)
		}
	}

	return AudioFrame{
		Data:       data,
		SampleRate: c.Target.SampleRate,
		Channels:   c.Target.Channels,
		Timestamp:  frame.Timestamp,
	}
}

// ConvertStream wraps an input AudioFrame channel with format conversion.
// It spawns a goroutine that reads from in, converts each frame to the target
// format, and sends on the returned channel. The returned channel is closed
// when in is closed.
func ConvertStream(in <-chan AudioFrame, target Format) <-chan AudioFrame {
	out := make(chan AudioFrame, cap(in))
	go func() {
		defer close(out)
		conv := FormatConverter{Target: target}
		for frame := range in {
			converted := conv.Convert(frame)
			if len(converted.Data) > 0 {
				out <- converted
			}
		}
	}()
	return out
}

// MonoToStereo duplicates each 16-bit mono sample into a stereo pair.
// Input must be little-endian int16 PCM (2 bytes per sample).
func MonoToStereo(pcm []byte) []byte {
	out := make([]byte, len(pcm)*2)
	for i := 0; i+1 < len(pcm); i += 2 {
		lo, hi := pcm[i], pcm[i+1]
		j := i * 2
		out[j] = lo
		out[j+1] = hi
		out[j+2] = lo
		out[j+3] = hi
	}
	return out
}

// StereoToMono averages left and right channels into a single mono channel.
// Input must be little-endian int16 interleaved stereo PCM (4 bytes per frame).
// Uses int32 arithmetic to prevent overflow before averaging.
func StereoToMono(pcm []byte) []byte {
	// Each stereo frame is 4 bytes (2 samples × 2 bytes).
	frameCount := len(pcm) / 4
	out := make([]byte, frameCount*2)
	for i := range frameCount {
		left := int32(int16(pcm[i*4]) | int16(pcm[i*4+1])<<8)
		right := int32(int16(pcm[i*4+2]) | int16(pcm[i*4+3])<<8)
		avg := (left + right) / 2
		// Clamp to int16 range.
		if avg > 32767 {
			avg = 32767
		} else if avg < -32768 {
			avg = -32768
		}
		out[i*2] = byte(avg)
		out[i*2+1] = byte(avg >> 8)
	}
	return out
}

// ResampleMono16 resamples 16-bit mono PCM from srcRate to dstRate using linear
// interpolation. The input must be little-endian int16 samples. If srcRate ==
// dstRate, the input is returned unchanged.
func ResampleMono16(pcm []byte, srcRate, dstRate int) []byte {
	if srcRate == dstRate || len(pcm) < 2 {
		return pcm
	}
	srcSamples := len(pcm) / 2
	dstSamples := int(int64(srcSamples) * int64(dstRate) / int64(srcRate))
	if dstSamples == 0 {
		return nil
	}

	out := make([]byte, dstSamples*2)
	ratio := float64(srcRate) / float64(dstRate)

	for i := range dstSamples {
		srcPos := float64(i) * ratio
		srcIdx := int(srcPos)
		frac := srcPos - float64(srcIdx)

		s0 := int16(pcm[srcIdx*2]) | int16(pcm[srcIdx*2+1])<<8
		var s1 int16
		if srcIdx+1 < srcSamples {
			s1 = int16(pcm[(srcIdx+1)*2]) | int16(pcm[(srcIdx+1)*2+1])<<8
		} else {
			s1 = s0
		}

		interpolated := int16(float64(s0)*(1-frac) + float64(s1)*frac)
		out[i*2] = byte(interpolated)
		out[i*2+1] = byte(interpolated >> 8)
	}
	return out
}

// ResampleStereo16 resamples 16-bit interleaved stereo PCM from srcRate to
// dstRate using linear interpolation per channel. The input must be
// little-endian int16 interleaved samples (L, R, L, R, ...).
func ResampleStereo16(pcm []byte, srcRate, dstRate int) []byte {
	if srcRate == dstRate || len(pcm) < 4 {
		return pcm
	}
	srcFrames := len(pcm) / 4 // 4 bytes per stereo frame
	dstFrames := int(int64(srcFrames) * int64(dstRate) / int64(srcRate))
	if dstFrames == 0 {
		return nil
	}

	out := make([]byte, dstFrames*4)
	ratio := float64(srcRate) / float64(dstRate)

	for i := range dstFrames {
		srcPos := float64(i) * ratio
		srcIdx := int(srcPos)
		frac := srcPos - float64(srcIdx)

		// Left channel
		l0 := int16(pcm[srcIdx*4]) | int16(pcm[srcIdx*4+1])<<8
		// Right channel
		r0 := int16(pcm[srcIdx*4+2]) | int16(pcm[srcIdx*4+3])<<8

		var l1, r1 int16
		if srcIdx+1 < srcFrames {
			l1 = int16(pcm[(srcIdx+1)*4]) | int16(pcm[(srcIdx+1)*4+1])<<8
			r1 = int16(pcm[(srcIdx+1)*4+2]) | int16(pcm[(srcIdx+1)*4+3])<<8
		} else {
			l1, r1 = l0, r0
		}

		li := int16(float64(l0)*(1-frac) + float64(l1)*frac)
		ri := int16(float64(r0)*(1-frac) + float64(r1)*frac)

		out[i*4] = byte(li)
		out[i*4+1] = byte(li >> 8)
		out[i*4+2] = byte(ri)
		out[i*4+3] = byte(ri >> 8)
	}
	return out
}

func formatString(rate, channels int) string {
	ch := "mono"
	if channels == 2 {
		ch = "stereo"
	}
	return fmt.Sprintf("%dHz %s", rate, ch)
}
```

Note: add `"fmt"` to the imports.

**Step 4: Run tests to verify they pass**

Run: `go test ./pkg/audio/ -v`
Expected: All PASS

**Step 5: Commit**

```bash
git add pkg/audio/convert.go pkg/audio/convert_test.go
git commit -m "feat(audio): add FormatConverter with resampling and channel conversion"
```

---

### Task 3: Change mixer output from `func([]byte)` to `func(AudioFrame)`

**Files:**
- Modify: `pkg/audio/mixer/mixer.go:54-93,315-331`
- Modify: `pkg/audio/mixer/mixer_test.go:43-60`
- Modify: `internal/app/session_manager.go:127-136`

**Step 1: Write failing test for the new mixer output signature**

Update `collectOutput` in `pkg/audio/mixer/mixer_test.go` to receive `AudioFrame` and add a test:

```go
func TestMixer_OutputEmitsAudioFrame(t *testing.T) {
	var got []audio.AudioFrame
	var mu sync.Mutex
	m := mixer.New(func(frame audio.AudioFrame) {
		mu.Lock()
		cp := make([]byte, len(frame.Data))
		copy(cp, frame.Data)
		got = append(got, audio.AudioFrame{
			Data:       cp,
			SampleRate: frame.SampleRate,
			Channels:   frame.Channels,
		})
		mu.Unlock()
	}, mixer.WithGap(0))
	defer m.Close()

	seg := &audio.AudioSegment{
		NPCID:      "npc",
		Audio:      makeCh([]byte{1, 2}),
		SampleRate: 22050,
		Channels:   1,
		Priority:   1,
	}
	m.Enqueue(seg, 1)

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(got) == 0 {
		t.Fatal("expected at least one AudioFrame")
	}
	if got[0].SampleRate != 22050 {
		t.Errorf("SampleRate = %d, want 22050", got[0].SampleRate)
	}
	if got[0].Channels != 1 {
		t.Errorf("Channels = %d, want 1", got[0].Channels)
	}
}

func makeCh(chunks ...[]byte) <-chan []byte {
	ch := make(chan []byte, len(chunks))
	for _, c := range chunks {
		ch <- c
	}
	close(ch)
	return ch
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./pkg/audio/mixer/ -run TestMixer_OutputEmitsAudioFrame -v`
Expected: FAIL — `New` expects `func([]byte)`, not `func(AudioFrame)`

**Step 3: Change mixer output type and play() method**

In `pkg/audio/mixer/mixer.go`:

1. Change the `output` field (line 55):
   ```go
   output func(audio.AudioFrame)
   ```

2. Change `New` constructor signature (line 79):
   ```go
   func New(output func(audio.AudioFrame), opts ...Option) *PriorityMixer {
   ```

3. Change `play()` method (lines 315-331) to wrap chunks as `AudioFrame`:
   ```go
   func (m *PriorityMixer) play(seg *audio.AudioSegment, cancel chan struct{}) {
   	for {
   		select {
   		case <-m.done:
   			go audio.Drain(seg.Audio)
   			return
   		case <-cancel:
   			go audio.Drain(seg.Audio)
   			return
   		case chunk, ok := <-seg.Audio:
   			if !ok {
   				return
   			}
   			m.output(audio.AudioFrame{
   				Data:       chunk,
   				SampleRate: seg.SampleRate,
   				Channels:   seg.Channels,
   			})
   		}
   	}
   }
   ```

4. Add segment validation in `Enqueue` (after line 106, before `m.seq++`):
   ```go
   if segment.SampleRate <= 0 || segment.Channels <= 0 {
   	slog.Error("mixer: rejecting segment with invalid format",
   		"npcID", segment.NPCID,
   		"sampleRate", segment.SampleRate,
   		"channels", segment.Channels,
   	)
   	go audio.Drain(segment.Audio)
   	return
   }
   ```
   Add `"log/slog"` to imports.

**Step 4: Update test helpers**

Update `collectOutput` in `pkg/audio/mixer/mixer_test.go`:

```go
func collectOutput() (func(audio.AudioFrame), func() [][]byte) {
	var mu sync.Mutex
	var chunks [][]byte
	output := func(frame audio.AudioFrame) {
		mu.Lock()
		defer mu.Unlock()
		cp := make([]byte, len(frame.Data))
		copy(cp, frame.Data)
		chunks = append(chunks, cp)
	}
	get := func() [][]byte {
		mu.Lock()
		defer mu.Unlock()
		out := make([][]byte, len(chunks))
		copy(out, chunks)
		return out
	}
	return output, get
}
```

**Step 5: Run all mixer tests**

Run: `go test ./pkg/audio/mixer/ -v`
Expected: All PASS

**Step 6: Update session_manager mixer wiring**

In `internal/app/session_manager.go` (lines 130-136), update the mixer creation:

```go
pm := audiomixer.New(func(frame audio.AudioFrame) {
	outStream <- frame
})
```

Remove the `SampleRate: 48000, Channels: 1` hardcoding — the mixer now passes the segment's format through.

**Step 7: Verify compilation**

Run: `go build ./...`
Expected: No errors

**Step 8: Run all tests**

Run: `go test ./... 2>&1 | tail -30`
Expected: All PASS

**Step 9: Commit**

```bash
git add pkg/audio/mixer/mixer.go pkg/audio/mixer/mixer_test.go internal/app/session_manager.go
git commit -m "feat(audio): change mixer output to func(AudioFrame) with format metadata"
```

---

### Task 4: Integrate converter in Discord sendLoop

**Files:**
- Modify: `pkg/audio/discord/connection.go:206-265`
- Modify: `pkg/audio/discord/opus.go:87-100`

**Step 1: Update sendLoop to use FormatConverter**

In `pkg/audio/discord/connection.go`, update `sendLoop` (line 206):

1. Add a `FormatConverter` creation at the top of `sendLoop`, after the encoder:
   ```go
   conv := audio.FormatConverter{Target: audio.Format{SampleRate: opusSampleRate, Channels: opusChannels}}
   ```

2. Replace the manual mono-to-stereo conversion block (lines 239-243):
   ```go
   // Before:
   data := frame.Data
   if frame.Channels <= 1 {
   	data = monoToStereo(data)
   }

   // After:
   frame = conv.Convert(frame)
   data := frame.Data
   ```

3. Add `"github.com/MrWong99/glyphoxa/pkg/audio"` to imports if not already present.

**Step 2: Remove monoToStereo from opus.go**

Delete the `monoToStereo` function (lines 87-100) from `pkg/audio/discord/opus.go`.

**Step 3: Verify compilation**

Run: `go build ./pkg/audio/discord/`
Expected: No errors

**Step 4: Run Discord tests**

Run: `go test ./pkg/audio/discord/ -v`
Expected: All PASS

**Step 5: Commit**

```bash
git add pkg/audio/discord/connection.go pkg/audio/discord/opus.go
git commit -m "refactor(discord): use FormatConverter in sendLoop, remove monoToStereo"
```

---

### Task 5: Integrate converter in WebRTC forwardOutput

**Files:**
- Modify: `pkg/audio/webrtc/connection.go:250-274`

**Step 1: Update forwardOutput to use FormatConverter**

In `pkg/audio/webrtc/connection.go`, update `forwardOutput` (line 252):

1. The WebRTC connection needs access to the platform's sample rate. Add a `sampleRate` field to the `Connection` struct if not already present, or read it from the platform config.

2. Create a `FormatConverter` at the top of `forwardOutput`:
   ```go
   conv := audio.FormatConverter{Target: audio.Format{SampleRate: c.sampleRate, Channels: 2}}
   ```

3. Convert frames before sending to peers:
   ```go
   // Before:
   _ = p.transport.SendAudio(frame)

   // After:
   _ = p.transport.SendAudio(conv.Convert(frame))
   ```

**Step 2: Verify compilation**

Run: `go build ./pkg/audio/webrtc/`
Expected: No errors

**Step 3: Run WebRTC tests**

Run: `go test ./pkg/audio/webrtc/ -v`
Expected: All PASS

**Step 4: Commit**

```bash
git add pkg/audio/webrtc/connection.go
git commit -m "refactor(webrtc): use FormatConverter in forwardOutput"
```

---

### Task 6: Remove resampling from Coqui provider

**Files:**
- Modify: `pkg/provider/tts/coqui/coqui.go:132-139,398-401,444-447,651-687`

**Step 1: Remove `WithOutputSampleRate` option**

Delete the `WithOutputSampleRate` function (lines 132-139) from `pkg/provider/tts/coqui/coqui.go`.

Remove the `outputRate` field from the `Provider` struct (line 150).

**Step 2: Remove resampling conditionals from synthesizeXTTS**

In `synthesizeXTTS` (around line 398-401), remove the resampling block:

```go
// Remove this:
if p.outputRate > 0 && info.SampleRate != p.outputRate && info.Channels == 1 {
	pcm = resampleMono16(pcm, info.SampleRate, p.outputRate)
}
```

**Step 3: Remove resampling conditionals from synthesizeStandard**

Same removal in `synthesizeStandard` (around line 444-447).

**Step 4: Remove resampleMono16 function**

Delete the `resampleMono16` function (lines 653-687) — it now lives in `pkg/audio/convert.go`.

**Step 5: Verify compilation**

Run: `go build ./pkg/provider/tts/coqui/`
Expected: No errors

**Step 6: Run Coqui tests**

Run: `go test ./pkg/provider/tts/coqui/ -v`
Expected: All PASS (tests that used `WithOutputSampleRate` need updating — if any exist, remove the option from the test setup and verify the test still makes sense)

**Step 7: Commit**

```bash
git add pkg/provider/tts/coqui/coqui.go
git commit -m "refactor(coqui): remove provider-level resampling, defer to FormatConverter"
```

---

### Task 7: Set AudioSegment format in NPC agent

**Files:**
- Modify: `internal/agent/npc.go:256-261`
- Modify: `internal/engine/engine.go:73-94`
- Modify: `internal/engine/cascade/cascade.go:174-191`

**Step 1: Add SampleRate and Channels to engine.Response**

In `internal/engine/engine.go`, add format fields to `Response` (after `Audio` field, around line 85):

```go
// SampleRate is the sample rate in Hz of the PCM data on the Audio channel.
SampleRate int

// Channels is the number of audio channels (1 = mono, 2 = stereo).
Channels int
```

**Step 2: Set format fields in cascade engine**

The cascade engine gets WAV format info from Coqui's parseWAV, but the `SynthesizeStream` interface returns `<-chan []byte` without format metadata. Since the TTS provider's sample rate is known at configuration time (Coqui's native rate or ElevenLabs' `pcm_16000`), we need a way to propagate this.

**Approach:** Add `SampleRate` and `Channels` fields to the `tts.Provider` interface or use a well-known convention. The simplest approach: the engine sets the format based on provider configuration.

For the cascade engine in `internal/engine/cascade/cascade.go`, the TTS provider's native output rate needs to be known. Since Coqui previously resampled internally and we're removing that, the cascade engine should record the TTS output format.

Add fields to the cascade engine struct for the expected TTS output format, set from provider configuration. Then stamp them on `engine.Response`:

```go
// Line 178:
return &engine.Response{Text: opener, Audio: audioCh, SampleRate: e.ttsSampleRate, Channels: e.ttsChannels}, nil

// Line 191:
resp := &engine.Response{Text: opener, Audio: audioCh, SampleRate: e.ttsSampleRate, Channels: e.ttsChannels}
```

**Note:** The exact TTS output format depends on the provider. For Coqui, the model's native rate (22050 for XTTS, varies for standard models). For ElevenLabs, `pcm_16000` by default. This information should be passed into the engine at construction time. Check the cascade engine's constructor to see how to thread this through.

**Step 3: Set format on AudioSegment in NPC agent**

In `internal/agent/npc.go` (line 256-260), propagate format from `resp`:

```go
seg := &audio.AudioSegment{
	NPCID:      a.id,
	Audio:      resp.Audio,
	SampleRate: resp.SampleRate,
	Channels:   resp.Channels,
	Priority:   defaultAudioPriority,
}
```

**Step 4: Verify compilation**

Run: `go build ./...`
Expected: No errors

**Step 5: Run tests**

Run: `go test ./internal/... -v 2>&1 | tail -30`
Expected: All PASS

**Step 6: Commit**

```bash
git add internal/agent/npc.go internal/engine/engine.go internal/engine/cascade/cascade.go
git commit -m "feat(engine): propagate TTS audio format through Response to AudioSegment"
```

---

### Task 8: Integrate ConvertStream on the input path

**Files:**
- Modify: `internal/app/app.go:388-402,413-482`

**Step 1: Wrap input streams with ConvertStream**

In `internal/app/app.go`, update `startAudioLoop` (line 388) to wrap each input channel:

```go
func (a *App) startAudioLoop(ctx context.Context, conn audio.Connection) {
	target := audio.Format{SampleRate: 16000, Channels: 1}

	for userID, inputCh := range conn.InputStreams() {
		go a.processParticipant(ctx, userID, audio.ConvertStream(inputCh, target))
	}

	conn.OnParticipantChange(func(ev audio.Event) {
		if ev.Type == audio.EventJoin {
			streams := conn.InputStreams()
			if ch, ok := streams[ev.UserID]; ok {
				go a.processParticipant(ctx, ev.UserID, audio.ConvertStream(ch, target))
			}
		}
	})
	// ... rest unchanged
}
```

**Step 2: Update processParticipant's STT/VAD config**

In `processParticipant` (line 413), the STT `StreamConfig` and VAD `Config` already specify `SampleRate: 48000`. Update them to `16000` since the input is now pre-converted:

```go
// STT config (line 419-422):
sess, err := a.providers.STT.StartStream(ctx, stt.StreamConfig{
	SampleRate: 16000,
	Channels:   1,
	Language:   "en-US",
})

// VAD config (line 435-439):
sess, err := a.providers.VAD.NewSession(vad.Config{
	SampleRate:       16000,
	FrameSizeMs:      20,
	SpeechThreshold:  0.5,
	SilenceThreshold: 0.35,
})
```

**Step 3: Verify compilation**

Run: `go build ./...`
Expected: No errors

**Step 4: Run all tests**

Run: `go test ./... 2>&1 | tail -30`
Expected: All PASS

**Step 5: Commit**

```bash
git add internal/app/app.go
git commit -m "feat(app): use ConvertStream for input path format conversion to 16kHz mono"
```

---

### Task 9: Final verification and cleanup

**Files:**
- All modified files

**Step 1: Full build**

Run: `go build ./...`
Expected: No errors

**Step 2: Full test suite**

Run: `go test ./... -count=1`
Expected: All PASS

**Step 3: Check for unused imports or dead code**

Run: `go vet ./...`
Expected: No warnings

**Step 4: Verify no remaining references to removed code**

Run: `grep -r 'WithOutputSampleRate\|resampleMono16' --include='*.go' . | grep -v '_test.go' | grep -v 'convert.go' | grep -v 'vendor'`
Expected: No matches (only `convert.go` should have `ResampleMono16`, and it's the exported version)

Run: `grep -rn 'monoToStereo' pkg/audio/discord/ --include='*.go'`
Expected: No matches (removed from `opus.go`)

**Step 5: Commit any cleanup**

If any dead code or unused imports were found:

```bash
git add -A
git commit -m "chore: clean up dead code from format negotiation refactor"
```
