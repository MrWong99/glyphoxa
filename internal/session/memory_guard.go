package session

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/MrWong99/glyphoxa/pkg/memory"
)

// MemoryGuard wraps a [memory.SessionStore] and makes all operations
// non-fatal. If the underlying store fails, operations return defaults
// and log warnings instead of propagating errors.
//
// This allows the voice engine to continue operating even when the memory
// backend is temporarily unavailable (e.g., database restart, network
// partition). The IsDegraded method reports whether the store is currently
// experiencing failures.
//
// MemoryGuard implements [memory.SessionStore].
//
// All methods are safe for concurrent use.
type MemoryGuard struct {
	store    memory.SessionStore
	degraded atomic.Bool
}

// NewMemoryGuard creates a new [MemoryGuard] wrapping the given store.
func NewMemoryGuard(store memory.SessionStore) *MemoryGuard {
	return &MemoryGuard{store: store}
}

// WriteEntry attempts to write an entry to the underlying store. On failure
// the error is logged and swallowed; the store is marked as degraded.
// On success the degraded flag is cleared.
func (mg *MemoryGuard) WriteEntry(ctx context.Context, sessionID string, entry memory.TranscriptEntry) error {
	err := mg.store.WriteEntry(ctx, sessionID, entry)
	if err != nil {
		mg.degraded.Store(true)
		slog.Warn("memory guard: WriteEntry failed, swallowing error",
			"session_id", sessionID,
			"error", err,
		)
		return nil
	}
	mg.degraded.Store(false)
	return nil
}

// GetRecent attempts to read recent entries from the underlying store.
// On failure an empty slice is returned and the store is marked as degraded.
func (mg *MemoryGuard) GetRecent(ctx context.Context, sessionID string, duration time.Duration) ([]memory.TranscriptEntry, error) {
	entries, err := mg.store.GetRecent(ctx, sessionID, duration)
	if err != nil {
		mg.degraded.Store(true)
		slog.Warn("memory guard: GetRecent failed, returning empty",
			"session_id", sessionID,
			"duration", duration,
			"error", err,
		)
		return []memory.TranscriptEntry{}, nil
	}
	mg.degraded.Store(false)
	return entries, nil
}

// Search attempts a keyword search over stored entries. On failure an empty
// slice is returned and the store is marked as degraded.
func (mg *MemoryGuard) Search(ctx context.Context, query string, opts memory.SearchOpts) ([]memory.TranscriptEntry, error) {
	entries, err := mg.store.Search(ctx, query, opts)
	if err != nil {
		mg.degraded.Store(true)
		slog.Warn("memory guard: Search failed, returning empty",
			"query", query,
			"error", err,
		)
		return []memory.TranscriptEntry{}, nil
	}
	mg.degraded.Store(false)
	return entries, nil
}

// EntryCount delegates to the underlying store. On failure the error is
// logged and 0 is returned; the store is marked as degraded.
func (mg *MemoryGuard) EntryCount(ctx context.Context, sessionID string) (int, error) {
	n, err := mg.store.EntryCount(ctx, sessionID)
	if err != nil {
		mg.degraded.Store(true)
		slog.Warn("memory guard: EntryCount failed, returning 0", "session", sessionID, "err", err)
		return 0, nil
	}
	mg.degraded.Store(false)
	return n, nil
}

// IsDegraded reports whether the store is currently operating in degraded
// mode (i.e., the most recent operation on the underlying store failed).
func (mg *MemoryGuard) IsDegraded() bool {
	return mg.degraded.Load()
}

// Compile-time check that MemoryGuard satisfies memory.SessionStore.
var _ memory.SessionStore = (*MemoryGuard)(nil)
