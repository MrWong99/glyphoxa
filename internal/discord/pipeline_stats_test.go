package discord

import (
	"testing"
	"time"
)

func TestNewPipelineStats_DefaultWindowSize(t *testing.T) {
	t.Parallel()

	ps := NewPipelineStats(0)
	// Should use default window size (100), not panic.
	ps.RecordSTT(10 * time.Millisecond)

	snap := ps.Snapshot()
	if snap.STT.P50 != 10*time.Millisecond {
		t.Errorf("STT P50 = %v, want 10ms", snap.STT.P50)
	}
}

func TestPipelineStats_RecordAndSnapshot(t *testing.T) {
	t.Parallel()

	ps := NewPipelineStats(100)

	// Record samples.
	for i := 1; i <= 100; i++ {
		ps.RecordSTT(time.Duration(i) * time.Millisecond)
	}
	ps.RecordLLM(500 * time.Millisecond)
	ps.RecordTTS(200 * time.Millisecond)
	ps.RecordS2S(1000 * time.Millisecond)

	ps.IncrUtterances()
	ps.IncrUtterances()
	ps.IncrUtterances()
	ps.IncrErrors()

	snap := ps.Snapshot()

	if snap.Utterances != 3 {
		t.Errorf("Utterances = %d, want 3", snap.Utterances)
	}
	if snap.Errors != 1 {
		t.Errorf("Errors = %d, want 1", snap.Errors)
	}

	// STT: 100 samples from 1ms to 100ms.
	// P50 should be around 50ms, P95 around 95ms.
	if snap.STT.P50 != 50*time.Millisecond {
		t.Errorf("STT P50 = %v, want 50ms", snap.STT.P50)
	}
	if snap.STT.P95 != 95*time.Millisecond {
		t.Errorf("STT P95 = %v, want 95ms", snap.STT.P95)
	}

	// LLM: single sample of 500ms.
	if snap.LLM.P50 != 500*time.Millisecond {
		t.Errorf("LLM P50 = %v, want 500ms", snap.LLM.P50)
	}
	if snap.TTS.P50 != 200*time.Millisecond {
		t.Errorf("TTS P50 = %v, want 200ms", snap.TTS.P50)
	}
	if snap.S2S.P50 != 1000*time.Millisecond {
		t.Errorf("S2S P50 = %v, want 1000ms", snap.S2S.P50)
	}
}

func TestPipelineStats_EmptySnapshot(t *testing.T) {
	t.Parallel()

	ps := NewPipelineStats(10)
	snap := ps.Snapshot()

	if snap.STT.P50 != 0 || snap.STT.P95 != 0 {
		t.Errorf("empty STT = %+v, want zero", snap.STT)
	}
	if snap.Utterances != 0 {
		t.Errorf("empty Utterances = %d, want 0", snap.Utterances)
	}
	if snap.Errors != 0 {
		t.Errorf("empty Errors = %d, want 0", snap.Errors)
	}
}

func TestPipelineStats_RingBufferWrap(t *testing.T) {
	t.Parallel()

	// Small buffer to force wrap-around.
	ps := NewPipelineStats(3)

	ps.RecordSTT(10 * time.Millisecond)
	ps.RecordSTT(20 * time.Millisecond)
	ps.RecordSTT(30 * time.Millisecond)
	// Wrap around: overwrites first entry.
	ps.RecordSTT(40 * time.Millisecond)

	snap := ps.Snapshot()
	// Buffer now contains [40, 20, 30] (40 overwrote 10 at pos 0).
	// Sorted: [20, 30, 40].
	// P50 of 3 elements: ceil(0.5 * 3) - 1 = 1 => index 1 => 30ms.
	if snap.STT.P50 != 30*time.Millisecond {
		t.Errorf("STT P50 after wrap = %v, want 30ms", snap.STT.P50)
	}
}

func TestPercentile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		sorted []time.Duration
		p      float64
		want   time.Duration
	}{
		{"empty", nil, 0.5, 0},
		{"single element p50", []time.Duration{100 * time.Millisecond}, 0.5, 100 * time.Millisecond},
		{"single element p95", []time.Duration{100 * time.Millisecond}, 0.95, 100 * time.Millisecond},
		{"two elements p50", []time.Duration{10 * time.Millisecond, 20 * time.Millisecond}, 0.5, 10 * time.Millisecond},
		{"two elements p95", []time.Duration{10 * time.Millisecond, 20 * time.Millisecond}, 0.95, 20 * time.Millisecond},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := percentile(tt.sorted, tt.p)
			if got != tt.want {
				t.Errorf("percentile(%v, %.2f) = %v, want %v", tt.sorted, tt.p, got, tt.want)
			}
		})
	}
}
