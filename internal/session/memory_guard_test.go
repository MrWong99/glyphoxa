package session

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MrWong99/glyphoxa/pkg/memory"
	memorymock "github.com/MrWong99/glyphoxa/pkg/memory/mock"
)

func TestMemoryGuard_WriteEntry(t *testing.T) {
	t.Run("successful write", func(t *testing.T) {
		store := &memorymock.SessionStore{}
		mg := NewMemoryGuard(store)

		entry := memory.TranscriptEntry{Text: "hello"}
		err := mg.WriteEntry(context.Background(), "s1", entry)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if mg.IsDegraded() {
			t.Error("should not be degraded after successful write")
		}
		if store.CallCount("WriteEntry") != 1 {
			t.Errorf("expected 1 WriteEntry call, got %d", store.CallCount("WriteEntry"))
		}
	})

	t.Run("write failure is swallowed", func(t *testing.T) {
		store := &memorymock.SessionStore{
			WriteEntryErr: errors.New("disk full"),
		}
		mg := NewMemoryGuard(store)

		entry := memory.TranscriptEntry{Text: "hello"}
		err := mg.WriteEntry(context.Background(), "s1", entry)
		if err != nil {
			t.Fatalf("expected nil error (swallowed), got %v", err)
		}
		if !mg.IsDegraded() {
			t.Error("should be degraded after failed write")
		}
	})

	t.Run("recovers from degraded after successful write", func(t *testing.T) {
		store := &memorymock.SessionStore{
			WriteEntryErr: errors.New("temporary failure"),
		}
		mg := NewMemoryGuard(store)

		// First call fails.
		_ = mg.WriteEntry(context.Background(), "s1", memory.TranscriptEntry{Text: "a"})
		if !mg.IsDegraded() {
			t.Error("should be degraded")
		}

		// Fix the store.
		store.WriteEntryErr = nil

		// Second call succeeds.
		_ = mg.WriteEntry(context.Background(), "s1", memory.TranscriptEntry{Text: "b"})
		if mg.IsDegraded() {
			t.Error("should have recovered from degraded state")
		}
	})
}

func TestMemoryGuard_GetRecent(t *testing.T) {
	t.Run("successful read", func(t *testing.T) {
		entries := []memory.TranscriptEntry{
			{Text: "hello"},
			{Text: "world"},
		}
		store := &memorymock.SessionStore{
			GetRecentResult: entries,
		}
		mg := NewMemoryGuard(store)

		got, err := mg.GetRecent(context.Background(), "s1", 5*time.Minute)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 2 {
			t.Errorf("expected 2 entries, got %d", len(got))
		}
		if mg.IsDegraded() {
			t.Error("should not be degraded")
		}
	})

	t.Run("read failure returns empty slice", func(t *testing.T) {
		store := &memorymock.SessionStore{
			GetRecentErr: errors.New("connection refused"),
		}
		mg := NewMemoryGuard(store)

		got, err := mg.GetRecent(context.Background(), "s1", 5*time.Minute)
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
		if len(got) != 0 {
			t.Errorf("expected empty slice, got %d entries", len(got))
		}
		if !mg.IsDegraded() {
			t.Error("should be degraded after failed read")
		}
	})
}

func TestMemoryGuard_Search(t *testing.T) {
	t.Run("successful search", func(t *testing.T) {
		entries := []memory.TranscriptEntry{
			{Text: "found it"},
		}
		store := &memorymock.SessionStore{
			SearchResult: entries,
		}
		mg := NewMemoryGuard(store)

		got, err := mg.Search(context.Background(), "goblin", memory.SearchOpts{SessionID: "s1"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 {
			t.Errorf("expected 1 result, got %d", len(got))
		}
	})

	t.Run("search failure returns empty slice", func(t *testing.T) {
		store := &memorymock.SessionStore{
			SearchErr: errors.New("index corrupted"),
		}
		mg := NewMemoryGuard(store)

		got, err := mg.Search(context.Background(), "dragon", memory.SearchOpts{})
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
		if len(got) != 0 {
			t.Errorf("expected empty slice, got %d results", len(got))
		}
		if !mg.IsDegraded() {
			t.Error("should be degraded after failed search")
		}
	})
}

func TestMemoryGuard_IsDegraded(t *testing.T) {
	t.Run("initially not degraded", func(t *testing.T) {
		mg := NewMemoryGuard(&memorymock.SessionStore{})
		if mg.IsDegraded() {
			t.Error("should not be degraded initially")
		}
	})

	t.Run("mixed operations track degraded state", func(t *testing.T) {
		store := &memorymock.SessionStore{}
		mg := NewMemoryGuard(store)

		// Successful write — not degraded.
		_ = mg.WriteEntry(context.Background(), "s1", memory.TranscriptEntry{})
		if mg.IsDegraded() {
			t.Error("should not be degraded after success")
		}

		// Failed search — degraded.
		store.SearchErr = errors.New("oops")
		_, _ = mg.Search(context.Background(), "q", memory.SearchOpts{})
		if !mg.IsDegraded() {
			t.Error("should be degraded after failed search")
		}

		// Successful write recovers.
		store.SearchErr = nil
		_ = mg.WriteEntry(context.Background(), "s1", memory.TranscriptEntry{})
		if mg.IsDegraded() {
			t.Error("should have recovered after successful write")
		}
	})
}

func TestMemoryGuard_ImplementsSessionStore(t *testing.T) {
	// This is a compile-time check, but let's also verify at runtime.
	var _ memory.SessionStore = NewMemoryGuard(&memorymock.SessionStore{})
}
