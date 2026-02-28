package whisper

import (
	"encoding/binary"
	"math"
	"testing"
)

func TestPcmToFloat32_Empty(t *testing.T) {
	out := pcmToFloat32(nil)
	if len(out) != 0 {
		t.Fatalf("expected 0 samples, got %d", len(out))
	}
}

func TestPcmToFloat32_SingleSample(t *testing.T) {
	pcm := make([]byte, 2)
	binary.LittleEndian.PutUint16(pcm, uint16(int16(16384))) // 0.5
	out := pcmToFloat32(pcm)
	if len(out) != 1 {
		t.Fatalf("expected 1 sample, got %d", len(out))
	}
	want := float32(16384) / 32768.0
	if math.Abs(float64(out[0]-want)) > 1e-6 {
		t.Errorf("sample = %f; want %f", out[0], want)
	}
}

func TestPcmToFloat32_FullScale(t *testing.T) {
	tests := []struct {
		name  string
		value int16
		want  float32
	}{
		{"max positive", 32767, 32767.0 / 32768.0},
		{"max negative", -32768, -1.0},
		{"zero", 0, 0.0},
		{"mid positive", 16384, 16384.0 / 32768.0},
		{"mid negative", -16384, -16384.0 / 32768.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pcm := make([]byte, 2)
			binary.LittleEndian.PutUint16(pcm, uint16(tt.value))
			out := pcmToFloat32(pcm)
			if math.Abs(float64(out[0]-tt.want)) > 1e-6 {
				t.Errorf("pcmToFloat32(%d) = %f; want %f", tt.value, out[0], tt.want)
			}
		})
	}
}

func TestPcmToFloat32_MultipleSamples(t *testing.T) {
	values := []int16{0, 100, -100, 32767, -32768}
	pcm := make([]byte, len(values)*2)
	for i, v := range values {
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(v))
	}
	out := pcmToFloat32(pcm)
	if len(out) != len(values) {
		t.Fatalf("expected %d samples, got %d", len(values), len(out))
	}
	for i, v := range values {
		want := float32(v) / 32768.0
		if math.Abs(float64(out[i]-want)) > 1e-6 {
			t.Errorf("sample[%d] = %f; want %f", i, out[i], want)
		}
	}
}

func TestPcmToFloat32_OddByteCount(t *testing.T) {
	// 3 bytes → only 1 complete sample (trailing byte ignored)
	pcm := []byte{0x00, 0x40, 0xFF}
	out := pcmToFloat32(pcm)
	if len(out) != 1 {
		t.Fatalf("expected 1 sample from 3-byte input, got %d", len(out))
	}
}

func TestPcmToFloat32Mono_SingleChannel(t *testing.T) {
	// channels=1 should be identical to pcmToFloat32
	values := []int16{100, -200, 300}
	pcm := make([]byte, len(values)*2)
	for i, v := range values {
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(v))
	}
	mono := pcmToFloat32Mono(pcm, 1)
	direct := pcmToFloat32(pcm)
	if len(mono) != len(direct) {
		t.Fatalf("length mismatch: mono=%d, direct=%d", len(mono), len(direct))
	}
	for i := range mono {
		if mono[i] != direct[i] {
			t.Errorf("sample[%d]: mono=%f, direct=%f", i, mono[i], direct[i])
		}
	}
}

func TestPcmToFloat32Mono_ZeroChannels(t *testing.T) {
	// channels <= 0 falls back to pcmToFloat32
	pcm := make([]byte, 4)
	binary.LittleEndian.PutUint16(pcm[0:], uint16(1000))
	v := int16(-1000)
	binary.LittleEndian.PutUint16(pcm[2:], uint16(v))
	mono := pcmToFloat32Mono(pcm, 0)
	if len(mono) != 2 {
		t.Fatalf("expected 2 samples, got %d", len(mono))
	}
}

func TestPcmToFloat32Mono_Stereo(t *testing.T) {
	// Two frames of stereo: (1000, 3000) and (-2000, -4000)
	// Expected mono: (1000+3000)/(2*32768) and (-2000+-4000)/(2*32768)
	values := []int16{1000, 3000, -2000, -4000}
	pcm := make([]byte, len(values)*2)
	for i, v := range values {
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(v))
	}
	mono := pcmToFloat32Mono(pcm, 2)
	if len(mono) != 2 {
		t.Fatalf("expected 2 mono samples from 4-sample stereo, got %d", len(mono))
	}
	// Frame 0: average of 1000/32768 and 3000/32768 = 2000/32768
	want0 := (float32(1000)/32768.0 + float32(3000)/32768.0) / 2.0
	if math.Abs(float64(mono[0]-want0)) > 1e-6 {
		t.Errorf("mono[0] = %f; want %f", mono[0], want0)
	}
	// Frame 1: average of -2000/32768 and -4000/32768 = -3000/32768
	want1 := (float32(-2000)/32768.0 + float32(-4000)/32768.0) / 2.0
	if math.Abs(float64(mono[1]-want1)) > 1e-6 {
		t.Errorf("mono[1] = %f; want %f", mono[1], want1)
	}
}

func TestPcmToFloat32Mono_ThreeChannels(t *testing.T) {
	// One frame of 3 channels: 3000, 6000, 9000
	// Expected mono: average = (3000+6000+9000) / (3*32768)
	values := []int16{3000, 6000, 9000}
	pcm := make([]byte, len(values)*2)
	for i, v := range values {
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(v))
	}
	mono := pcmToFloat32Mono(pcm, 3)
	if len(mono) != 1 {
		t.Fatalf("expected 1 mono sample from 3-channel frame, got %d", len(mono))
	}
	want := (float32(3000)/32768.0 + float32(6000)/32768.0 + float32(9000)/32768.0) / 3.0
	if math.Abs(float64(mono[0]-want)) > 1e-6 {
		t.Errorf("mono[0] = %f; want %f", mono[0], want)
	}
}
