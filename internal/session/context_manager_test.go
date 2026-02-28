package session

import (
	"context"
	"strings"
	"testing"

	"github.com/MrWong99/glyphoxa/pkg/provider/llm"
)

// mockSummariser is a test double for Summariser.
type mockSummariser struct {
	result string
	err    error
	calls  int
	msgs   [][]llm.Message
}

func (m *mockSummariser) Summarise(_ context.Context, messages []llm.Message) (string, error) {
	m.calls++
	m.msgs = append(m.msgs, messages)
	return m.result, m.err
}

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		name     string
		msg      llm.Message
		wantMin  int
		wantMax  int
	}{
		{
			name:    "empty message",
			msg:     llm.Message{},
			wantMin: 0,
			wantMax: 0,
		},
		{
			name:    "short message",
			msg:     llm.Message{Role: "user", Content: "Hi"},
			wantMin: 1, // 6 chars / 4 = 1
			wantMax: 2,
		},
		{
			name:    "long message",
			msg:     llm.Message{Role: "assistant", Content: strings.Repeat("a", 400)},
			wantMin: 100, // (400+9) / 4 ≈ 102
			wantMax: 110,
		},
		{
			name: "message with tool calls",
			msg: llm.Message{
				Role: "assistant",
				ToolCalls: []llm.ToolCall{
					{ID: "tc_1", Name: "roll_dice", Arguments: `{"sides":20}`},
				},
			},
			wantMin: 5,
			wantMax: 15,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := estimateTokens(tt.msg)
			if got < tt.wantMin || got > tt.wantMax {
				t.Errorf("estimateTokens() = %d, want [%d, %d]", got, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestContextManager_AddMessages(t *testing.T) {
	t.Run("adds messages and tracks tokens", func(t *testing.T) {
		s := &mockSummariser{result: "summary"}
		cm := NewContextManager(ContextManagerConfig{
			MaxTokens:      10000,
			ThresholdRatio: 0.75,
			Summariser:     s,
		})

		err := cm.AddMessages(context.Background(),
			llm.Message{Role: "user", Content: "Hello there!"},
			llm.Message{Role: "assistant", Content: "Greetings, adventurer!"},
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		msgs := cm.Messages()
		if len(msgs) != 2 {
			t.Fatalf("expected 2 messages, got %d", len(msgs))
		}
		if cm.TokenEstimate() == 0 {
			t.Error("expected non-zero token estimate")
		}
		if s.calls != 0 {
			t.Errorf("expected no summarisation calls, got %d", s.calls)
		}
	})

	t.Run("triggers summarisation when threshold exceeded", func(t *testing.T) {
		s := &mockSummariser{result: "condensed"}
		cm := NewContextManager(ContextManagerConfig{
			MaxTokens:      100,   // very small window
			ThresholdRatio: 0.5,   // trigger at 50 tokens
			Summariser:     s,
		})

		// Add enough messages to exceed the threshold.
		longContent := strings.Repeat("x", 200) // 200 chars ≈ 50 tokens
		err := cm.AddMessages(context.Background(),
			llm.Message{Role: "user", Content: longContent},
			llm.Message{Role: "assistant", Content: longContent},
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if s.calls == 0 {
			t.Error("expected summarisation to be triggered")
		}

		msgs := cm.Messages()
		// Should have summary message(s) + remaining messages.
		foundSummary := false
		for _, m := range msgs {
			if strings.Contains(m.Content, "[Previous conversation summary]") {
				foundSummary = true
				break
			}
		}
		if !foundSummary {
			t.Error("expected summary message in output")
		}
	})

	t.Run("default threshold ratio is 0.75", func(t *testing.T) {
		s := &mockSummariser{result: "summary"}
		cm := NewContextManager(ContextManagerConfig{
			MaxTokens:  1000,
			Summariser: s,
		})
		// threshold = 750 tokens
		// Adding small messages should not trigger summarisation.
		err := cm.AddMessages(context.Background(),
			llm.Message{Role: "user", Content: "short"},
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if s.calls != 0 {
			t.Errorf("expected no summarisation, got %d calls", s.calls)
		}
	})
}

func TestContextManager_Messages(t *testing.T) {
	t.Run("returns messages in order with summary prefix", func(t *testing.T) {
		s := &mockSummariser{result: "events happened"}
		cm := NewContextManager(ContextManagerConfig{
			MaxTokens:      40,
			ThresholdRatio: 0.5,
			Summariser:     s,
		})

		// Add messages that will trigger summarisation.
		err := cm.AddMessages(context.Background(),
			llm.Message{Role: "user", Content: strings.Repeat("a", 80)},
			llm.Message{Role: "assistant", Content: strings.Repeat("b", 80)},
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		msgs := cm.Messages()
		if len(msgs) == 0 {
			t.Fatal("expected non-empty messages")
		}

		// First message should be the summary.
		if msgs[0].Role != "system" {
			t.Errorf("expected first message to be system (summary), got %q", msgs[0].Role)
		}
	})
}

func TestContextManager_Reset(t *testing.T) {
	s := &mockSummariser{result: "summary"}
	cm := NewContextManager(ContextManagerConfig{
		MaxTokens:  10000,
		Summariser: s,
	})

	_ = cm.AddMessages(context.Background(),
		llm.Message{Role: "user", Content: "Hello"},
	)

	cm.Reset()

	if cm.TokenEstimate() != 0 {
		t.Errorf("expected 0 tokens after reset, got %d", cm.TokenEstimate())
	}
	msgs := cm.Messages()
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages after reset, got %d", len(msgs))
	}
}

func TestContextManager_TokenEstimate(t *testing.T) {
	s := &mockSummariser{result: "summary"}
	cm := NewContextManager(ContextManagerConfig{
		MaxTokens:  100000,
		Summariser: s,
	})

	before := cm.TokenEstimate()
	if before != 0 {
		t.Errorf("expected 0 initial tokens, got %d", before)
	}

	_ = cm.AddMessages(context.Background(),
		llm.Message{Role: "user", Content: strings.Repeat("x", 100)},
	)

	after := cm.TokenEstimate()
	if after <= before {
		t.Errorf("expected token count to increase, got before=%d after=%d", before, after)
	}
}
