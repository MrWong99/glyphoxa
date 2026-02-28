package discord

import (
	"math"
	"sort"
	"sync"
	"time"
)

// PipelineStats collects pipeline latency samples and counter values for
// dashboard display. It maintains a bounded ring buffer of recent latency
// observations from which percentiles are computed on demand.
//
// Thread-safe for concurrent use.
type PipelineStats struct {
	mu sync.Mutex

	stt latencyBuffer
	llm latencyBuffer
	tts latencyBuffer
	s2s latencyBuffer

	utterances int64
	errors     int64
}

// NewPipelineStats creates a PipelineStats with the given window size
// (maximum number of latency samples retained per stage).
func NewPipelineStats(windowSize int) *PipelineStats {
	if windowSize <= 0 {
		windowSize = 100
	}
	return &PipelineStats{
		stt: newLatencyBuffer(windowSize),
		llm: newLatencyBuffer(windowSize),
		tts: newLatencyBuffer(windowSize),
		s2s: newLatencyBuffer(windowSize),
	}
}

// RecordSTT records a speech-to-text latency sample.
func (ps *PipelineStats) RecordSTT(d time.Duration) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.stt.add(d)
}

// RecordLLM records an LLM inference latency sample.
func (ps *PipelineStats) RecordLLM(d time.Duration) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.llm.add(d)
}

// RecordTTS records a text-to-speech latency sample.
func (ps *PipelineStats) RecordTTS(d time.Duration) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.tts.add(d)
}

// RecordS2S records an end-to-end speech-to-speech latency sample.
func (ps *PipelineStats) RecordS2S(d time.Duration) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.s2s.add(d)
}

// IncrUtterances increments the utterance counter.
func (ps *PipelineStats) IncrUtterances() {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.utterances++
}

// IncrErrors increments the error counter.
func (ps *PipelineStats) IncrErrors() {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.errors++
}

// LatencyPercentiles holds p50 and p95 values for a latency stage.
type LatencyPercentiles struct {
	P50 time.Duration
	P95 time.Duration
}

// Snapshot captures a point-in-time view of all pipeline statistics.
type Snapshot struct {
	STT        LatencyPercentiles
	LLM        LatencyPercentiles
	TTS        LatencyPercentiles
	S2S        LatencyPercentiles
	Utterances int64
	Errors     int64
}

// Snapshot returns a point-in-time view of all pipeline statistics.
func (ps *PipelineStats) Snapshot() Snapshot {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	return Snapshot{
		STT:        ps.stt.percentiles(),
		LLM:        ps.llm.percentiles(),
		TTS:        ps.tts.percentiles(),
		S2S:        ps.s2s.percentiles(),
		Utterances: ps.utterances,
		Errors:     ps.errors,
	}
}

// latencyBuffer is a bounded ring buffer of duration samples.
type latencyBuffer struct {
	data []time.Duration
	size int
	pos  int
	full bool
}

func newLatencyBuffer(size int) latencyBuffer {
	return latencyBuffer{
		data: make([]time.Duration, size),
		size: size,
	}
}

func (lb *latencyBuffer) add(d time.Duration) {
	lb.data[lb.pos] = d
	lb.pos++
	if lb.pos >= lb.size {
		lb.pos = 0
		lb.full = true
	}
}

func (lb *latencyBuffer) percentiles() LatencyPercentiles {
	n := lb.pos
	if lb.full {
		n = lb.size
	}
	if n == 0 {
		return LatencyPercentiles{}
	}

	// Copy and sort the valid samples.
	sorted := make([]time.Duration, n)
	if lb.full {
		copy(sorted, lb.data)
	} else {
		copy(sorted, lb.data[:n])
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	return LatencyPercentiles{
		P50: percentile(sorted, 0.50),
		P95: percentile(sorted, 0.95),
	}
}

// percentile returns the value at the given percentile (0.0-1.0) from a
// sorted slice of durations using nearest-rank.
func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(p*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
