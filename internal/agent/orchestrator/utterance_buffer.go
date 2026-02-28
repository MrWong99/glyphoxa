package orchestrator

import (
	"sync"
	"time"
)

// UtteranceBuffer maintains a shared buffer of recent utterances from all
// NPCs and players. Each NPC can read from this buffer to understand what
// other NPCs have said recently (cross-NPC awareness).
//
// The buffer enforces both a maximum entry count and a maximum age. Entries
// that exceed either limit are evicted automatically on every [Add] call.
//
// All methods are safe for concurrent use.
type UtteranceBuffer struct {
	mu      sync.RWMutex
	entries []BufferEntry
	maxSize int
	maxAge  time.Duration
}

// BufferEntry represents a single utterance stored in the [UtteranceBuffer].
type BufferEntry struct {
	// SpeakerID identifies the speaker (player user-ID or NPC agent ID).
	SpeakerID string

	// SpeakerName is the human-readable name of the speaker.
	SpeakerName string

	// Text is the utterance text.
	Text string

	// NPCID is non-empty when the utterance was produced by an NPC agent.
	NPCID string

	// Timestamp records when the utterance occurred.
	Timestamp time.Time
}

// NewUtteranceBuffer creates a buffer that retains at most maxSize entries
// and evicts entries older than maxAge.
func NewUtteranceBuffer(maxSize int, maxAge time.Duration) *UtteranceBuffer {
	return &UtteranceBuffer{
		entries: make([]BufferEntry, 0, maxSize),
		maxSize: maxSize,
		maxAge:  maxAge,
	}
}

// Add appends an entry to the buffer and evicts entries that exceed the
// configured maximum size or age.
func (b *UtteranceBuffer) Add(entry BufferEntry) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.entries = append(b.entries, entry)
	b.evict()
}

// Recent returns up to maxEntries entries that are within the configured
// maxAge window, excluding any entries whose NPCID matches excludeNPCID.
// This allows an NPC to see what other NPCs and players have said without
// seeing its own utterances as "cross-NPC" context.
//
// Entries are returned in chronological order (oldest first).
func (b *UtteranceBuffer) Recent(excludeNPCID string, maxEntries int) []BufferEntry {
	b.mu.RLock()
	defer b.mu.RUnlock()

	cutoff := time.Now().Add(-b.maxAge)
	result := make([]BufferEntry, 0, maxEntries)

	for i := len(b.entries) - 1; i >= 0 && len(result) < maxEntries; i-- {
		e := b.entries[i]
		if e.Timestamp.Before(cutoff) {
			continue
		}
		if e.NPCID == excludeNPCID && excludeNPCID != "" {
			continue
		}
		result = append(result, e)
	}

	// Reverse to chronological order.
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result
}

// Entries returns all current buffer entries in chronological order.
// Intended for testing and debugging.
func (b *UtteranceBuffer) Entries() []BufferEntry {
	b.mu.RLock()
	defer b.mu.RUnlock()

	out := make([]BufferEntry, len(b.entries))
	copy(out, b.entries)
	return out
}

// evict removes entries that are too old or exceed maxSize.
// Must be called with b.mu held.
//
// Surviving entries are copied to a fresh backing array to prevent the old
// (evicted) entries from pinning memory for the lifetime of the session.
func (b *UtteranceBuffer) evict() {
	cutoff := time.Now().Add(-b.maxAge)

	// Evict by age — find the first entry that is not expired.
	start := 0
	for start < len(b.entries) && b.entries[start].Timestamp.Before(cutoff) {
		start++
	}

	keep := b.entries[start:]

	// Evict by size — keep only the most recent maxSize entries.
	if len(keep) > b.maxSize {
		keep = keep[len(keep)-b.maxSize:]
	}

	// Copy to a fresh slice so evicted entries can be garbage collected.
	if start > 0 || len(keep) < len(b.entries) {
		fresh := make([]BufferEntry, len(keep), b.maxSize)
		copy(fresh, keep)
		b.entries = fresh
	}
}
