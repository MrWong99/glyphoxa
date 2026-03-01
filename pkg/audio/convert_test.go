package audio_test

import (
	"encoding/binary"
	"testing"

	"github.com/MrWong99/glyphoxa/pkg/audio"
)

// samplesToBytes converts a slice of int16 samples to little-endian byte representation.
func samplesToBytes(samples []int16) []byte {
	buf := make([]byte, len(samples)*2)
	for i, s := range samples {
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(s))
	}
	return buf
}

// bytesToSamples converts a little-endian byte slice to int16 samples.
func bytesToSamples(b []byte) []int16 {
	samples := make([]int16, len(b)/2)
	for i := range samples {
		samples[i] = int16(binary.LittleEndian.Uint16(b[i*2:]))
	}
	return samples
}

func TestMonoToStereo(t *testing.T) {
	mono := samplesToBytes([]int16{100, 200, 300})
	stereo := audio.MonoToStereo(mono)
	got := bytesToSamples(stereo)
	want := []int16{100, 100, 200, 200, 300, 300}
	if len(got) != len(want) {
		t.Fatalf("length mismatch: got %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sample %d: got %d, want %d", i, got[i], want[i])
		}
	}
}

func TestStereoToMono(t *testing.T) {
	// Two stereo frames: L=100,R=200 and L=-100,R=-200
	stereo := samplesToBytes([]int16{100, 200, -100, -200})
	mono := audio.StereoToMono(stereo)
	got := bytesToSamples(mono)
	want := []int16{150, -150}
	if len(got) != len(want) {
		t.Fatalf("length mismatch: got %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sample %d: got %d, want %d", i, got[i], want[i])
		}
	}
}

func TestStereoToMono_Clamping(t *testing.T) {
	// Two max-positive samples should clamp to 32767 (not overflow).
	stereo := samplesToBytes([]int16{32767, 32767})
	mono := audio.StereoToMono(stereo)
	got := bytesToSamples(mono)
	want := []int16{32767}
	if len(got) != len(want) {
		t.Fatalf("length mismatch: got %d, want %d", len(got), len(want))
	}
	if got[0] != want[0] {
		t.Errorf("got %d, want %d", got[0], want[0])
	}
}

func TestResampleMono16_SameRate(t *testing.T) {
	pcm := samplesToBytes([]int16{100, 200, 300})
	out := audio.ResampleMono16(pcm, 48000, 48000)
	if len(out) != len(pcm) {
		t.Fatalf("length mismatch: got %d, want %d", len(out), len(pcm))
	}
}

func TestResampleMono16_Upsample(t *testing.T) {
	// 2 samples at 16kHz → 6 samples at 48kHz (3x)
	pcm := samplesToBytes([]int16{1000, 2000})
	out := audio.ResampleMono16(pcm, 16000, 48000)
	got := bytesToSamples(out)
	if len(got) != 6 {
		t.Fatalf("expected 6 samples, got %d", len(got))
	}
	// First output sample should equal first source sample.
	if got[0] != 1000 {
		t.Errorf("first sample: got %d, want 1000", got[0])
	}
	// Last output sample should be close to last source sample.
	last := got[len(got)-1]
	if last < 1800 || last > 2200 {
		t.Errorf("last sample: got %d, want close to 2000", last)
	}
}

func TestResampleMono16_Downsample(t *testing.T) {
	// 6 samples at 48kHz → 2 samples at 16kHz (1/3x)
	pcm := samplesToBytes([]int16{100, 200, 300, 400, 500, 600})
	out := audio.ResampleMono16(pcm, 48000, 16000)
	got := bytesToSamples(out)
	if len(got) != 2 {
		t.Fatalf("expected 2 samples, got %d", len(got))
	}
}

func TestResampleStereo16(t *testing.T) {
	// 2 stereo frames at 16kHz → 6 stereo frames (12 samples) at 48kHz
	pcm := samplesToBytes([]int16{100, 200, 300, 400})
	out := audio.ResampleStereo16(pcm, 16000, 48000)
	got := bytesToSamples(out)
	if len(got) != 12 {
		t.Fatalf("expected 12 samples, got %d", len(got))
	}
}

func TestFormatConverter_NoOp(t *testing.T) {
	conv := audio.FormatConverter{
		Target: audio.Format{SampleRate: 48000, Channels: 2},
	}
	frame := audio.AudioFrame{
		Data:       samplesToBytes([]int16{100, 200}),
		SampleRate: 48000,
		Channels:   2,
	}
	result := conv.Convert(frame)
	// Same slice — pointer equality check.
	if &result.Data[0] != &frame.Data[0] {
		t.Error("expected same slice (zero allocation) for matching format")
	}
}

func TestFormatConverter_MonoToStereo(t *testing.T) {
	conv := audio.FormatConverter{
		Target: audio.Format{SampleRate: 48000, Channels: 2},
	}
	frame := audio.AudioFrame{
		Data:       samplesToBytes([]int16{100, 200, 300}),
		SampleRate: 48000,
		Channels:   1,
	}
	result := conv.Convert(frame)
	got := bytesToSamples(result.Data)
	want := []int16{100, 100, 200, 200, 300, 300}
	if len(got) != len(want) {
		t.Fatalf("length mismatch: got %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sample %d: got %d, want %d", i, got[i], want[i])
		}
	}
	if result.SampleRate != 48000 || result.Channels != 2 {
		t.Errorf("unexpected format: %dHz %dch", result.SampleRate, result.Channels)
	}
}

func TestFormatConverter_FullConversion(t *testing.T) {
	// 22050 Hz mono → 48000 Hz stereo
	conv := audio.FormatConverter{
		Target: audio.Format{SampleRate: 48000, Channels: 2},
	}
	frame := audio.AudioFrame{
		Data:       samplesToBytes([]int16{1000, 2000}),
		SampleRate: 22050,
		Channels:   1,
	}
	result := conv.Convert(frame)
	if result.SampleRate != 48000 {
		t.Errorf("expected 48000Hz, got %d", result.SampleRate)
	}
	if result.Channels != 2 {
		t.Errorf("expected 2 channels, got %d", result.Channels)
	}
	// After resampling 2 mono samples from 22050→48000 we get some number of mono samples,
	// then channel conversion doubles that. Output should be stereo (even number of samples).
	got := bytesToSamples(result.Data)
	if len(got)%2 != 0 {
		t.Errorf("stereo output should have even number of samples, got %d", len(got))
	}
	if len(got) == 0 {
		t.Error("expected non-empty output")
	}
}

func TestFormatConverter_OddByteCount(t *testing.T) {
	conv := audio.FormatConverter{
		Target: audio.Format{SampleRate: 48000, Channels: 1},
	}
	frame := audio.AudioFrame{
		Data:       []byte{1, 2, 3}, // 3 bytes — odd, invalid for int16 PCM
		SampleRate: 22050,
		Channels:   1,
	}
	result := conv.Convert(frame)
	if len(result.Data) != 0 {
		t.Errorf("expected empty data for odd byte count, got %d bytes", len(result.Data))
	}
	// Dropped frame should carry target format, not source format.
	if result.SampleRate != 48000 {
		t.Errorf("expected target sample rate 48000, got %d", result.SampleRate)
	}
	if result.Channels != 1 {
		t.Errorf("expected target channels 1, got %d", result.Channels)
	}
}

func TestFormatConverter_OddByteCount_MatchingFormat(t *testing.T) {
	// C2: odd byte count should be caught even when formats match.
	conv := audio.FormatConverter{
		Target: audio.Format{SampleRate: 48000, Channels: 1},
	}
	frame := audio.AudioFrame{
		Data:       []byte{1, 2, 3}, // odd byte count
		SampleRate: 48000,           // matches target
		Channels:   1,               // matches target
	}
	result := conv.Convert(frame)
	if len(result.Data) != 0 {
		t.Errorf("expected empty data for odd byte count even when formats match, got %d bytes", len(result.Data))
	}
}

func TestMonoToStereo_OddLengthInput(t *testing.T) {
	// I2: odd-length input should not produce trailing zero bytes.
	// 5 bytes = 2 complete samples + 1 trailing byte.
	pcm := []byte{0x64, 0x00, 0xC8, 0x00, 0xFF} // 100, 200, then junk byte
	stereo := audio.MonoToStereo(pcm)
	// Should only process 2 complete samples → 4 stereo samples → 8 bytes.
	if len(stereo) != 8 {
		t.Fatalf("expected 8 bytes for 2 complete mono samples, got %d", len(stereo))
	}
	got := bytesToSamples(stereo)
	want := []int16{100, 100, 200, 200}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sample %d: got %d, want %d", i, got[i], want[i])
		}
	}
}

func TestResampleMono16_ZeroRate(t *testing.T) {
	pcm := samplesToBytes([]int16{100, 200})
	// Zero srcRate should return input unchanged.
	out := audio.ResampleMono16(pcm, 0, 48000)
	if len(out) != len(pcm) {
		t.Errorf("expected unchanged output for zero srcRate, got len %d", len(out))
	}
	// Zero dstRate should return input unchanged.
	out = audio.ResampleMono16(pcm, 48000, 0)
	if len(out) != len(pcm) {
		t.Errorf("expected unchanged output for zero dstRate, got len %d", len(out))
	}
	// Negative rates should return input unchanged.
	out = audio.ResampleMono16(pcm, -1, 48000)
	if len(out) != len(pcm) {
		t.Errorf("expected unchanged output for negative srcRate, got len %d", len(out))
	}
}

func TestResampleStereo16_ZeroRate(t *testing.T) {
	pcm := samplesToBytes([]int16{100, 200, 300, 400})
	out := audio.ResampleStereo16(pcm, 0, 48000)
	if len(out) != len(pcm) {
		t.Errorf("expected unchanged output for zero srcRate, got len %d", len(out))
	}
	out = audio.ResampleStereo16(pcm, 48000, 0)
	if len(out) != len(pcm) {
		t.Errorf("expected unchanged output for zero dstRate, got len %d", len(out))
	}
}

func TestConvertStream(t *testing.T) {
	in := make(chan audio.AudioFrame, 3)
	target := audio.Format{SampleRate: 48000, Channels: 2}

	out := audio.ConvertStream(in, target)

	// Send a valid mono frame that needs conversion.
	in <- audio.AudioFrame{
		Data:       samplesToBytes([]int16{100, 200}),
		SampleRate: 48000,
		Channels:   1,
	}
	// Send an odd-byte frame that should be dropped.
	in <- audio.AudioFrame{
		Data:       []byte{1, 2, 3},
		SampleRate: 48000,
		Channels:   1,
	}
	// Send a frame that matches target (pass-through).
	in <- audio.AudioFrame{
		Data:       samplesToBytes([]int16{500, 600, 700, 800}),
		SampleRate: 48000,
		Channels:   2,
	}
	close(in)

	var results []audio.AudioFrame
	for frame := range out {
		results = append(results, frame)
	}

	// Should get 2 frames (the odd-byte frame is dropped).
	if len(results) != 2 {
		t.Fatalf("expected 2 frames, got %d", len(results))
	}

	// First frame: mono→stereo conversion.
	if results[0].SampleRate != 48000 || results[0].Channels != 2 {
		t.Errorf("frame 0: expected 48000Hz stereo, got %dHz %dch",
			results[0].SampleRate, results[0].Channels)
	}
	got := bytesToSamples(results[0].Data)
	want := []int16{100, 100, 200, 200}
	if len(got) != len(want) {
		t.Fatalf("frame 0: expected %d samples, got %d", len(want), len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("frame 0 sample %d: got %d, want %d", i, got[i], want[i])
		}
	}

	// Second frame: pass-through.
	if results[1].SampleRate != 48000 || results[1].Channels != 2 {
		t.Errorf("frame 1: expected 48000Hz stereo, got %dHz %dch",
			results[1].SampleRate, results[1].Channels)
	}
	got2 := bytesToSamples(results[1].Data)
	want2 := []int16{500, 600, 700, 800}
	if len(got2) != len(want2) {
		t.Fatalf("frame 1: expected %d samples, got %d", len(want2), len(got2))
	}
	for i := range want2 {
		if got2[i] != want2[i] {
			t.Errorf("frame 1 sample %d: got %d, want %d", i, got2[i], want2[i])
		}
	}
}
