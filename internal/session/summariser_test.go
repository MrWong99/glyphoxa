package session

import (
	"context"
	"errors"
	"testing"

	llmmock "github.com/MrWong99/glyphoxa/pkg/provider/llm/mock"
	"github.com/MrWong99/glyphoxa/pkg/provider/llm"
)

func TestLLMSummariser_Summarise(t *testing.T) {
	t.Run("empty messages returns empty string", func(t *testing.T) {
		p := &llmmock.Provider{}
		s := NewLLMSummariser(p)

		result, err := s.Summarise(context.Background(), nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != "" {
			t.Errorf("expected empty string, got %q", result)
		}
		if len(p.CompleteCalls) != 0 {
			t.Errorf("expected no LLM calls for empty input, got %d", len(p.CompleteCalls))
		}
	})

	t.Run("summarises messages via LLM", func(t *testing.T) {
		p := &llmmock.Provider{
			CompleteResponse: &llm.CompletionResponse{
				Content: "The party agreed to help the innkeeper.",
			},
		}
		s := NewLLMSummariser(p)

		msgs := []llm.Message{
			{Role: "user", Name: "Player1", Content: "We'll help you, innkeeper."},
			{Role: "assistant", Name: "Grok", Content: "Thank you, brave adventurers!"},
		}

		result, err := s.Summarise(context.Background(), msgs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != "The party agreed to help the innkeeper." {
			t.Errorf("unexpected result: %q", result)
		}

		if len(p.CompleteCalls) != 1 {
			t.Fatalf("expected 1 Complete call, got %d", len(p.CompleteCalls))
		}

		call := p.CompleteCalls[0]
		if call.Req.SystemPrompt != summarisationPrompt {
			t.Errorf("expected summarisation prompt, got %q", call.Req.SystemPrompt)
		}
		if len(call.Req.Messages) != 1 {
			t.Fatalf("expected 1 message in request, got %d", len(call.Req.Messages))
		}
		if call.Req.Messages[0].Role != "user" {
			t.Errorf("expected user role, got %q", call.Req.Messages[0].Role)
		}
	})

	t.Run("uses Name over Role when formatting", func(t *testing.T) {
		p := &llmmock.Provider{
			CompleteResponse: &llm.CompletionResponse{Content: "summary"},
		}
		s := NewLLMSummariser(p)

		msgs := []llm.Message{
			{Role: "user", Name: "Gandalf", Content: "You shall not pass!"},
		}

		_, err := s.Summarise(context.Background(), msgs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		call := p.CompleteCalls[0]
		content := call.Req.Messages[0].Content
		if !contains(content, "[Gandalf]") {
			t.Errorf("expected speaker name Gandalf in content, got %q", content)
		}
	})

	t.Run("propagates LLM errors", func(t *testing.T) {
		p := &llmmock.Provider{
			CompleteErr: errors.New("model overloaded"),
		}
		s := NewLLMSummariser(p)

		msgs := []llm.Message{
			{Role: "user", Content: "Hello"},
		}

		_, err := s.Summarise(context.Background(), msgs)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !contains(err.Error(), "model overloaded") {
			t.Errorf("expected wrapped error, got %v", err)
		}
	})
}

// contains is a test helper that checks substring presence.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
