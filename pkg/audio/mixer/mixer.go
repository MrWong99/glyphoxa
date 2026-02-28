package mixer

import (
	"container/heap"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/MrWong99/glyphoxa/pkg/audio"
)

// Compile-time interface assertion.
var _ audio.Mixer = (*PriorityMixer)(nil)

const (
	// DefaultGap is the base silence duration inserted between consecutive
	// segments when no explicit gap is configured via [WithGap].
	DefaultGap = 300 * time.Millisecond

	// defaultQueueCap is the initial capacity hint for the priority queue.
	defaultQueueCap = 16
)

// Option configures a [PriorityMixer] during construction.
type Option func(*PriorityMixer)

// WithGap sets the base silence gap inserted between consecutive segments.
// Jitter of ±1/6 of the gap is applied automatically. A gap of zero disables
// inter-segment silence entirely.
func WithGap(d time.Duration) Option {
	return func(m *PriorityMixer) {
		m.gap = d
	}
}

// WithQueueCapacity sets the initial capacity hint for the internal priority
// queue. This does not impose a hard limit — the queue grows as needed.
func WithQueueCapacity(n int) Option {
	return func(m *PriorityMixer) {
		if n > 0 {
			m.queue = make(segmentHeap, 0, n)
		}
	}
}

// PriorityMixer is a concrete [audio.Mixer] that schedules [audio.AudioSegment]
// playback using a priority queue backed by [container/heap].
//
// Higher-priority segments preempt lower-priority ones currently playing.
// Equal-priority segments are played in FIFO order. A configurable silence gap
// (with jitter) is inserted between consecutive segments to sound natural.
//
// All exported methods are safe for concurrent use.
type PriorityMixer struct {
	output func([]byte) // callback that receives audio chunks for playback

	mu             sync.Mutex
	queue          segmentHeap
	seq            uint64              // monotonic counter for FIFO ordering
	gap            time.Duration       // base silence gap between segments
	playing        *audio.AudioSegment // currently playing segment, or nil
	playingPri     int                 // priority of the currently playing segment
	cancelPlaying  chan struct{}       // closed to interrupt the current segment
	bargeInHandler func(string)        // last-writer-wins barge-in callback

	notify chan struct{} // signalled when a new segment is enqueued or interrupt fires
	done   chan struct{} // closed by Close to stop the dispatch goroutine
	closed bool
}

// New creates a [PriorityMixer] that delivers audio chunks to the output
// callback. The mixer starts a background dispatch goroutine immediately.
//
// output must not be nil; it is called sequentially from the dispatch goroutine
// and must not block for extended periods.
//
// Call [PriorityMixer.Close] to stop the background goroutine and release
// resources.
func New(output func([]byte), opts ...Option) *PriorityMixer {
	m := &PriorityMixer{
		output: output,
		queue:  make(segmentHeap, 0, defaultQueueCap),
		gap:    DefaultGap,
		notify: make(chan struct{}, 1),
		done:   make(chan struct{}),
	}
	for _, o := range opts {
		o(m)
	}
	heap.Init(&m.queue)
	go m.dispatch()
	return m
}

// Enqueue schedules segment for playback at the given priority. If the segment
// has higher priority than the one currently playing, the current segment is
// interrupted with [audio.DMOverride] semantics and the new segment begins
// immediately.
//
// The priority parameter overrides segment.Priority, allowing call-site context
// to elevate or demote a segment without mutating the struct.
func (m *PriorityMixer) Enqueue(segment *audio.AudioSegment, priority int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return
	}

	m.seq++
	heap.Push(&m.queue, entry{
		segment:  segment,
		priority: priority,
		seq:      m.seq,
	})

	// Preempt the current segment if the new one has higher priority.
	if m.playing != nil && priority > m.playingPri {
		m.interruptLocked(audio.DMOverride, false)
	}

	// Wake the dispatch goroutine.
	select {
	case m.notify <- struct{}{}:
	default:
	}
}

// Interrupt immediately stops the currently playing segment for the given
// reason and advances to the next queued segment (if any). If nothing is
// playing, Interrupt is a no-op.
//
// For [audio.PlayerBargeIn], the queue is also cleared — the player is taking
// the floor. For [audio.DMOverride], queued segments are preserved.
func (m *PriorityMixer) Interrupt(reason audio.InterruptReason) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.interruptLocked(reason, reason == audio.PlayerBargeIn)
}

// OnBargeIn registers handler as the callback to invoke when [BargeIn] is
// called. Only one handler may be active at a time; subsequent calls replace
// the previous registration. The handler is invoked on a new goroutine and
// must not block.
func (m *PriorityMixer) OnBargeIn(handler func(speakerID string)) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.bargeInHandler = handler
}

// BargeIn signals that a player started speaking while an NPC segment is
// playing. It interrupts the current segment with [audio.PlayerBargeIn]
// semantics, clears the queue, and invokes the registered barge-in handler
// (if any) on a new goroutine.
//
// This method is intended to be called by the platform adapter when voice
// activity detection fires during NPC playback.
func (m *PriorityMixer) BargeIn(speakerID string) {
	m.mu.Lock()
	handler := m.bargeInHandler
	m.interruptLocked(audio.PlayerBargeIn, true)
	m.mu.Unlock()

	if handler != nil {
		go handler(speakerID)
	}
}

// SetGap configures the base silence duration inserted between consecutive
// segments. Jitter of ±1/6 of the gap is applied automatically. A gap of
// zero disables inter-segment silence entirely. Changes take effect before
// the next segment starts.
func (m *PriorityMixer) SetGap(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.gap = d
}

// Close stops the background dispatch goroutine, drains any remaining queued
// segments, and releases resources. Close is idempotent — subsequent calls
// are no-ops and return nil.
func (m *PriorityMixer) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true

	// Interrupt current playback if any.
	if m.playing != nil {
		m.interruptLocked(audio.DMOverride, false)
	}

	// Drain the queue.
	for m.queue.Len() > 0 {
		e := heap.Pop(&m.queue).(entry)
		go audio.Drain(e.segment.Audio)
	}
	m.mu.Unlock()

	close(m.done)
	return nil
}

// interruptLocked cancels the currently playing segment and optionally clears
// the queue. Must be called with m.mu held.
func (m *PriorityMixer) interruptLocked(reason audio.InterruptReason, clearQueue bool) {
	_ = reason // available for future reason-specific behaviour (e.g., fade-out)

	if m.cancelPlaying != nil {
		close(m.cancelPlaying)
		m.cancelPlaying = nil
	}
	m.playing = nil

	if clearQueue {
		for m.queue.Len() > 0 {
			e := heap.Pop(&m.queue).(entry)
			go audio.Drain(e.segment.Audio)
		}
	}
}

// dispatch is the background goroutine that pulls segments from the queue and
// streams their audio chunks to the output callback. It runs until [Close] is
// called.
func (m *PriorityMixer) dispatch() {
	var lastPlayed bool // true if a segment was just played (for gap insertion)

	// Reusable timer for inter-segment gaps — avoids allocating a new
	// time.Timer on every segment transition.
	gapTimer := time.NewTimer(0)
	if !gapTimer.Stop() {
		<-gapTimer.C
	}
	defer gapTimer.Stop()

	for {
		// Wait for work or shutdown.
		select {
		case <-m.done:
			return
		case <-m.notify:
		}

		for {
			seg, _, cancel, ok := m.dequeue()
			if !ok {
				break
			}

			// Insert gap between consecutive segments.
			if lastPlayed {
				gapDur := m.gapWithJitter()
				if gapDur > 0 {
					gapTimer.Reset(gapDur)
					select {
					case <-m.done:
						if !gapTimer.Stop() {
							<-gapTimer.C
						}
						// Drain the segment we just dequeued.
						go audio.Drain(seg.Audio)
						return
					case <-cancel:
						if !gapTimer.Stop() {
							<-gapTimer.C
						}
						// Interrupted during gap — segment was preempted.
						go audio.Drain(seg.Audio)
						continue
					case <-gapTimer.C:
					}
				}
			}

			m.play(seg, cancel)
			lastPlayed = true

			// Clear the playing state after the segment finishes.
			m.mu.Lock()
			if m.playing == seg {
				m.playing = nil
				m.cancelPlaying = nil
			}
			m.mu.Unlock()
		}
	}
}

// dequeue pops the highest-priority segment from the queue and marks it as
// currently playing. Returns ok=false if the queue is empty.
func (m *PriorityMixer) dequeue() (seg *audio.AudioSegment, _ int, cancel chan struct{}, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.queue.Len() == 0 {
		return nil, 0, nil, false
	}

	e := heap.Pop(&m.queue).(entry)
	cancel = make(chan struct{})
	m.playing = e.segment
	m.playingPri = e.priority
	m.cancelPlaying = cancel
	return e.segment, e.priority, cancel, true
}

// play streams audio chunks from seg to the output callback until the segment
// ends or cancel is closed (interrupt).
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
				return // segment finished naturally
			}
			m.output(chunk)
		}
	}
}

// gapWithJitter returns the configured gap duration with ±1/6 jitter applied.
// Returns zero if the base gap is zero.
func (m *PriorityMixer) gapWithJitter() time.Duration {
	m.mu.Lock()
	base := m.gap
	m.mu.Unlock()

	if base <= 0 {
		return 0
	}

	jitterRange := base / 6
	if jitterRange <= 0 {
		return base
	}

	// rand/v2 is concurrency-safe with the global source.
	jitter := time.Duration(rand.Int64N(int64(2*jitterRange+1))) - jitterRange
	return base + jitter
}
