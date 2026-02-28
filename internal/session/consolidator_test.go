package session

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/MrWong99/glyphoxa/pkg/memory"
	memorymock "github.com/MrWong99/glyphoxa/pkg/memory/mock"
	"github.com/MrWong99/glyphoxa/pkg/provider/llm"
)

func TestConsolidator_ConsolidateNow(t *testing.T) {
	t.Run("writes new messages to store", func(t *testing.T) {
		store := &memorymock.SessionStore{}
		s := &mockSummariser{result: "summary"}
		cm := NewContextManager(ContextManagerConfig{
			MaxTokens:  100000,
			Summariser: s,
		})

		_ = cm.AddMessages(context.Background(),
			llm.Message{Role: "user", Name: "Player1", Content: "I attack the goblin!"},
			llm.Message{Role: "assistant", Name: "Grek", Content: "The goblin dodges!"},
		)

		c := NewConsolidator(ConsolidatorConfig{
			Store:      store,
			ContextMgr: cm,
			SessionID:  "session-1",
		})

		err := c.ConsolidateNow(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		writeCount := store.CallCount("WriteEntry")
		if writeCount != 2 {
			t.Errorf("expected 2 WriteEntry calls, got %d", writeCount)
		}
	})

	t.Run("does not re-write already consolidated messages", func(t *testing.T) {
		store := &memorymock.SessionStore{}
		s := &mockSummariser{result: "summary"}
		cm := NewContextManager(ContextManagerConfig{
			MaxTokens:  100000,
			Summariser: s,
		})

		_ = cm.AddMessages(context.Background(),
			llm.Message{Role: "user", Content: "First message"},
		)

		c := NewConsolidator(ConsolidatorConfig{
			Store:      store,
			ContextMgr: cm,
			SessionID:  "session-1",
		})

		_ = c.ConsolidateNow(context.Background())
		firstCount := store.CallCount("WriteEntry")

		// Consolidate again without new messages — should not write.
		store.Reset()
		_ = c.ConsolidateNow(context.Background())
		secondCount := store.CallCount("WriteEntry")

		if secondCount != 0 {
			t.Errorf("expected 0 writes on second consolidation, got %d (first had %d)", secondCount, firstCount)
		}
	})

	t.Run("writes only new messages on subsequent consolidation", func(t *testing.T) {
		store := &memorymock.SessionStore{}
		s := &mockSummariser{result: "summary"}
		cm := NewContextManager(ContextManagerConfig{
			MaxTokens:  100000,
			Summariser: s,
		})

		_ = cm.AddMessages(context.Background(),
			llm.Message{Role: "user", Content: "First"},
		)

		c := NewConsolidator(ConsolidatorConfig{
			Store:      store,
			ContextMgr: cm,
			SessionID:  "session-1",
		})

		_ = c.ConsolidateNow(context.Background())
		store.Reset()

		_ = cm.AddMessages(context.Background(),
			llm.Message{Role: "user", Content: "Second"},
			llm.Message{Role: "assistant", Content: "Reply"},
		)

		_ = c.ConsolidateNow(context.Background())
		if store.CallCount("WriteEntry") != 2 {
			t.Errorf("expected 2 writes for new messages, got %d", store.CallCount("WriteEntry"))
		}
	})

	t.Run("skips summary messages", func(t *testing.T) {
		store := &memorymock.SessionStore{}
		s := &mockSummariser{result: "condensed history"}
		cm := NewContextManager(ContextManagerConfig{
			MaxTokens:      40,
			ThresholdRatio: 0.5,
			Summariser:     s,
		})

		// Force summarisation by exceeding threshold.
		_ = cm.AddMessages(context.Background(),
			llm.Message{Role: "user", Content: strings.Repeat("a", 80)},
			llm.Message{Role: "assistant", Content: strings.Repeat("b", 80)},
		)

		c := NewConsolidator(ConsolidatorConfig{
			Store:      store,
			ContextMgr: cm,
			SessionID:  "session-1",
		})

		_ = c.ConsolidateNow(context.Background())

		// Verify that summary messages (starting with '[') are skipped.
		calls := store.Calls()
		for _, call := range calls {
			if call.Method == "WriteEntry" && len(call.Args) > 1 {
				entry, ok := call.Args[1].(memory.TranscriptEntry)
				if ok && len(entry.Text) > 0 && entry.Text[0] == '[' {
					t.Errorf("summary message should not be written to store, got: %s", entry.Text)
				}
			}
		}
	})
}

func TestConsolidator_DefaultInterval(t *testing.T) {
	c := NewConsolidator(ConsolidatorConfig{
		Store:      &memorymock.SessionStore{},
		ContextMgr: NewContextManager(ContextManagerConfig{MaxTokens: 1000, Summariser: &mockSummariser{}}),
		SessionID:  "s1",
	})
	if c.interval != 30*time.Minute {
		t.Errorf("expected default interval of 30m, got %v", c.interval)
	}
}

func TestConsolidator_StartStop(t *testing.T) {
	store := &memorymock.SessionStore{}
	s := &mockSummariser{result: "summary"}
	cm := NewContextManager(ContextManagerConfig{
		MaxTokens:  100000,
		Summariser: s,
	})

	c := NewConsolidator(ConsolidatorConfig{
		Store:      store,
		ContextMgr: cm,
		SessionID:  "session-1",
		Interval:   10 * time.Millisecond, // very short for testing
	})

	_ = cm.AddMessages(context.Background(),
		llm.Message{Role: "user", Content: "Hello"},
	)

	ctx := t.Context()

	c.Start(ctx)

	// Wait long enough for at least one tick.
	time.Sleep(50 * time.Millisecond)

	c.Stop()

	// Should have written at least once.
	if store.CallCount("WriteEntry") == 0 {
		t.Error("expected at least one periodic consolidation")
	}

	// Calling Stop again should not panic.
	c.Stop()
}
