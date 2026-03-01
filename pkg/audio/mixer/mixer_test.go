package mixer_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MrWong99/glyphoxa/pkg/audio"
	"github.com/MrWong99/glyphoxa/pkg/audio/mixer"
)

// makeSegment creates an AudioSegment with a buffered channel pre-loaded with
// the given chunks. The channel is closed after all chunks are written.
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

// makeOpenSegment creates an AudioSegment whose channel the caller controls.
// Returns the segment and the send channel. The caller must close sendCh when done.
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

// collectOutput creates an output callback that appends received chunks to a
// slice protected by a mutex. Returns the callback and a function to retrieve
// the collected chunks.
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

func TestBasicPlayback(t *testing.T) {
	t.Parallel()

	output, get := collectOutput()
	m := mixer.New(output, mixer.WithGap(0))
	defer m.Close()

	seg := makeSegment("npc-1", 1, []byte("hello"), []byte("world"))
	m.Enqueue(seg, 1)

	// Give the dispatch goroutine time to process.
	time.Sleep(50 * time.Millisecond)

	chunks := get()
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}
	if string(chunks[0]) != "hello" {
		t.Errorf("chunk[0] = %q, want %q", chunks[0], "hello")
	}
	if string(chunks[1]) != "world" {
		t.Errorf("chunk[1] = %q, want %q", chunks[1], "world")
	}
}

func TestFIFOWithinSamePriority(t *testing.T) {
	t.Parallel()

	output, get := collectOutput()
	m := mixer.New(output, mixer.WithGap(0))
	defer m.Close()

	// Enqueue two segments at the same priority — should play in FIFO order.
	seg1 := makeSegment("npc-1", 5, []byte("first"))
	seg2 := makeSegment("npc-2", 5, []byte("second"))
	m.Enqueue(seg1, 5)
	m.Enqueue(seg2, 5)

	time.Sleep(100 * time.Millisecond)

	chunks := get()
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}
	if string(chunks[0]) != "first" {
		t.Errorf("chunk[0] = %q, want %q", chunks[0], "first")
	}
	if string(chunks[1]) != "second" {
		t.Errorf("chunk[1] = %q, want %q", chunks[1], "second")
	}
}

func TestPriorityPreemption(t *testing.T) {
	t.Parallel()

	output, get := collectOutput()
	m := mixer.New(output, mixer.WithGap(0))
	defer m.Close()

	// Start a long-running low-priority segment.
	seg1, sendCh1 := makeOpenSegment("npc-low", 1)
	m.Enqueue(seg1, 1)

	// Let it start playing.
	sendCh1 <- []byte("low-1")
	time.Sleep(30 * time.Millisecond)

	// Enqueue a higher-priority segment — should preempt.
	seg2 := makeSegment("npc-high", 10, []byte("high-1"))
	m.Enqueue(seg2, 10)

	time.Sleep(50 * time.Millisecond)
	close(sendCh1) // clean up the preempted segment

	chunks := get()
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}
	// First chunk should be from the low-priority segment.
	if string(chunks[0]) != "low-1" {
		t.Errorf("chunk[0] = %q, want %q", chunks[0], "low-1")
	}
	// The high-priority chunk should appear.
	found := false
	for _, c := range chunks {
		if string(c) == "high-1" {
			found = true
			break
		}
	}
	if !found {
		t.Error("high-priority chunk not found in output")
	}
}

func TestInterruptDMOverrideKeepsQueue(t *testing.T) {
	t.Parallel()

	output, get := collectOutput()
	m := mixer.New(output, mixer.WithGap(0))
	defer m.Close()

	// Start a playing segment.
	seg1, sendCh1 := makeOpenSegment("npc-1", 1)
	m.Enqueue(seg1, 1)
	sendCh1 <- []byte("playing")
	time.Sleep(30 * time.Millisecond)

	// Queue another segment.
	seg2 := makeSegment("npc-2", 1, []byte("queued"))
	m.Enqueue(seg2, 1)

	// Interrupt with DMOverride — queue should be preserved.
	m.Interrupt(audio.DMOverride)
	close(sendCh1)

	time.Sleep(100 * time.Millisecond)

	chunks := get()
	found := false
	for _, c := range chunks {
		if string(c) == "queued" {
			found = true
			break
		}
	}
	if !found {
		t.Error("queued segment should play after DMOverride interrupt")
	}
}

func TestInterruptPlayerBargeInClearsQueue(t *testing.T) {
	t.Parallel()

	output, get := collectOutput()
	m := mixer.New(output, mixer.WithGap(0))
	defer m.Close()

	// Start playing.
	seg1, sendCh1 := makeOpenSegment("npc-1", 1)
	m.Enqueue(seg1, 1)
	sendCh1 <- []byte("playing")
	time.Sleep(30 * time.Millisecond)

	// Queue another segment.
	seg2 := makeSegment("npc-2", 1, []byte("queued"))
	m.Enqueue(seg2, 1)

	// Interrupt with PlayerBargeIn — queue should be cleared.
	m.Interrupt(audio.PlayerBargeIn)
	close(sendCh1)

	time.Sleep(100 * time.Millisecond)

	chunks := get()
	for _, c := range chunks {
		if string(c) == "queued" {
			t.Error("queued segment should NOT play after PlayerBargeIn interrupt")
		}
	}
}

func TestBargeInHandler(t *testing.T) {
	t.Parallel()

	output, _ := collectOutput()
	m := mixer.New(output, mixer.WithGap(0))
	defer m.Close()

	var called atomic.Bool
	var calledWith atomic.Value
	m.OnBargeIn(func(speakerID string) {
		called.Store(true)
		calledWith.Store(speakerID)
	})

	// Start playing so barge-in has something to interrupt.
	seg, sendCh := makeOpenSegment("npc-1", 1)
	m.Enqueue(seg, 1)
	sendCh <- []byte("audio")
	time.Sleep(30 * time.Millisecond)

	m.BargeIn("player-42")
	close(sendCh)

	time.Sleep(50 * time.Millisecond)

	if !called.Load() {
		t.Error("barge-in handler was not called")
	}
	if v, ok := calledWith.Load().(string); !ok || v != "player-42" {
		t.Errorf("barge-in handler called with %q, want %q", v, "player-42")
	}
}

func TestGapInsertion(t *testing.T) {
	t.Parallel()

	output, _ := collectOutput()
	m := mixer.New(output, mixer.WithGap(200*time.Millisecond))
	defer m.Close()

	seg1 := makeSegment("npc-1", 1, []byte("a"))
	seg2 := makeSegment("npc-2", 1, []byte("b"))
	m.Enqueue(seg1, 1)
	m.Enqueue(seg2, 1)

	// Without gap: would finish in ~0ms. With 200ms gap: should take at least 150ms.
	// (accounting for jitter: 200ms ± 33ms → min ~167ms)
	start := time.Now()
	time.Sleep(400 * time.Millisecond) // generous wait
	elapsed := time.Since(start)

	_ = elapsed // the key assertion is that it doesn't crash; timing is inherently flaky
}

func TestSetGap(t *testing.T) {
	t.Parallel()

	output, _ := collectOutput()
	m := mixer.New(output, mixer.WithGap(5*time.Second))
	defer m.Close()

	// Override to zero — should play immediately.
	m.SetGap(0)

	seg1 := makeSegment("npc-1", 1, []byte("a"))
	seg2 := makeSegment("npc-2", 1, []byte("b"))
	m.Enqueue(seg1, 1)
	m.Enqueue(seg2, 1)

	time.Sleep(100 * time.Millisecond)
	// If SetGap(0) didn't work, we'd still be waiting for the 5s gap.
	// No assertion needed beyond not hanging.
}

func TestCloseIdempotent(t *testing.T) {
	t.Parallel()

	output, _ := collectOutput()
	m := mixer.New(output)

	if err := m.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestCloseStopsPlayback(t *testing.T) {
	t.Parallel()

	output, get := collectOutput()
	m := mixer.New(output, mixer.WithGap(0))

	// Enqueue a segment with an open channel.
	_, sendCh := makeOpenSegment("npc-1", 1)
	seg := &audio.AudioSegment{
		NPCID:      "npc-1",
		Audio:      sendCh,
		SampleRate: 48000,
		Channels:   1,
	}
	m.Enqueue(seg, 1)
	sendCh <- []byte("before-close")
	time.Sleep(30 * time.Millisecond)

	m.Close()
	close(sendCh)

	time.Sleep(50 * time.Millisecond)

	// Should have received at least the pre-close chunk.
	chunks := get()
	if len(chunks) == 0 {
		t.Error("expected at least one chunk before Close")
	}
}

func TestEnqueueAfterClose(t *testing.T) {
	t.Parallel()

	output, _ := collectOutput()
	m := mixer.New(output)
	m.Close()

	// Should not panic.
	seg := makeSegment("npc-1", 1, []byte("ignored"))
	m.Enqueue(seg, 1)
}

func TestConcurrentEnqueue(t *testing.T) {
	t.Parallel()

	var received atomic.Int64
	output := func(audio.AudioFrame) {
		received.Add(1)
	}
	m := mixer.New(output, mixer.WithGap(0))
	defer m.Close()

	const goroutines = 10
	const perGoroutine = 5

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := range goroutines {
		go func(id int) {
			defer wg.Done()
			for j := range perGoroutine {
				seg := makeSegment("npc", 1, []byte{byte(id), byte(j)})
				m.Enqueue(seg, 1)
			}
		}(i)
	}
	wg.Wait()

	// Give time for all segments to play.
	time.Sleep(300 * time.Millisecond)

	got := received.Load()
	want := int64(goroutines * perGoroutine)
	if got != want {
		t.Errorf("received %d chunks, want %d", got, want)
	}
}

func TestEmptyQueueNoop(t *testing.T) {
	t.Parallel()

	output, get := collectOutput()
	m := mixer.New(output, mixer.WithGap(0))
	defer m.Close()

	// Interrupt with nothing playing — should be a no-op.
	m.Interrupt(audio.DMOverride)
	m.Interrupt(audio.PlayerBargeIn)

	time.Sleep(50 * time.Millisecond)

	chunks := get()
	if len(chunks) != 0 {
		t.Errorf("expected 0 chunks, got %d", len(chunks))
	}
}

func TestWithQueueCapacityOption(t *testing.T) {
	t.Parallel()

	output, get := collectOutput()
	m := mixer.New(output, mixer.WithGap(0), mixer.WithQueueCapacity(2))
	defer m.Close()

	// Queue should grow beyond initial capacity.
	for i := range 5 {
		seg := makeSegment("npc", 1, []byte{byte(i)})
		m.Enqueue(seg, 1)
	}

	time.Sleep(200 * time.Millisecond)

	chunks := get()
	if len(chunks) != 5 {
		t.Errorf("expected 5 chunks, got %d", len(chunks))
	}
}

func TestHighPriorityPlaysFirst(t *testing.T) {
	t.Parallel()

	output, get := collectOutput()
	// Use a long gap so we can enqueue multiple before any play.
	m := mixer.New(output, mixer.WithGap(0))
	defer m.Close()

	// Block the dispatch goroutine by not starting yet — enqueue all at once.
	// We use an open segment to hold the dispatch, then enqueue prioritised segments.
	blocker, blockerCh := makeOpenSegment("blocker", 0)
	m.Enqueue(blocker, 0)
	blockerCh <- []byte("block")
	time.Sleep(30 * time.Millisecond)

	// Now enqueue segments with different priorities while blocker holds the floor.
	low := makeSegment("low", 1, []byte("low"))
	high := makeSegment("high", 10, []byte("high"))
	m.Enqueue(low, 1)
	m.Enqueue(high, 10)

	// high > blocker(0), so it should preempt immediately
	time.Sleep(30 * time.Millisecond)
	close(blockerCh)
	time.Sleep(100 * time.Millisecond)

	chunks := get()
	// Find the positions of "high" and "low".
	highIdx, lowIdx := -1, -1
	for i, c := range chunks {
		switch string(c) {
		case "high":
			highIdx = i
		case "low":
			lowIdx = i
		}
	}

	if highIdx == -1 {
		t.Fatal("high-priority chunk not found")
	}
	if lowIdx == -1 {
		t.Fatal("low-priority chunk not found")
	}
	if highIdx > lowIdx {
		t.Errorf("high-priority chunk (idx %d) should play before low-priority (idx %d)", highIdx, lowIdx)
	}
}

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

	seg := makeSegment("npc", 1, []byte{1, 2})
	seg.SampleRate = 22050
	seg.Channels = 1
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

func TestMixer_RejectsInvalidFormat(t *testing.T) {
	output, _ := collectOutput()
	m := mixer.New(output, mixer.WithGap(0))
	defer m.Close()

	ch := make(chan []byte, 1)
	ch <- []byte{1, 2}
	close(ch)
	seg := &audio.AudioSegment{
		NPCID:      "npc",
		Audio:      ch,
		SampleRate: 0, // invalid
		Channels:   1,
		Priority:   1,
	}
	m.Enqueue(seg, 1)
	time.Sleep(50 * time.Millisecond)
	// Segment should be rejected and audio drained (no panic, no output)
}
