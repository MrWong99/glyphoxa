// Package mixer provides a concrete [audio.Mixer] implementation backed by a
// priority queue. It schedules NPC audio segments for playback, supports
// priority-based preemption, barge-in interrupts, and configurable inter-segment
// silence gaps with jitter.
package mixer

import "github.com/MrWong99/glyphoxa/pkg/audio"

// entry wraps an [audio.AudioSegment] with scheduling metadata for the
// priority queue. The seq field provides FIFO ordering within the same
// priority level.
type entry struct {
	segment  *audio.AudioSegment
	priority int
	seq      uint64 // monotonic insertion order for FIFO tie-breaking
}

// segmentHeap implements [container/heap.Interface] as a max-heap ordered by
// priority (descending), with FIFO tie-breaking on seq (ascending).
type segmentHeap []entry

func (h segmentHeap) Len() int { return len(h) }

// Less reports whether element i should be dequeued before element j.
// Higher priority wins; equal priority falls back to insertion order.
func (h segmentHeap) Less(i, j int) bool {
	if h[i].priority != h[j].priority {
		return h[i].priority > h[j].priority
	}
	return h[i].seq < h[j].seq
}

func (h segmentHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

// Push appends x to the heap. Called by [container/heap.Push]; callers must
// not invoke this directly.
func (h *segmentHeap) Push(x any) {
	*h = append(*h, x.(entry))
}

// Pop removes and returns the last element. Called by [container/heap.Pop];
// callers must not invoke this directly.
func (h *segmentHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}
