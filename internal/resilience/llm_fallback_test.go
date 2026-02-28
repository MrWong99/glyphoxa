package resilience

import (
	"context"
	"errors"
	"testing"

	"github.com/MrWong99/glyphoxa/pkg/provider/llm"
	llmmock "github.com/MrWong99/glyphoxa/pkg/provider/llm/mock"
)

func TestLLMFallback_Complete_PrimarySuccess(t *testing.T) {
	primary := &llmmock.Provider{
		CompleteResponse: &llm.CompletionResponse{Content: "hello from primary"},
	}
	secondary := &llmmock.Provider{
		CompleteResponse: &llm.CompletionResponse{Content: "hello from secondary"},
	}

	fb := NewLLMFallback(primary, "primary", FallbackConfig{
		CircuitBreaker: CircuitBreakerConfig{MaxFailures: 3},
	})
	fb.AddFallback("secondary", secondary)

	resp, err := fb.Complete(context.Background(), llm.CompletionRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "hello from primary" {
		t.Fatalf("content = %q, want 'hello from primary'", resp.Content)
	}
	if len(primary.CompleteCalls) != 1 {
		t.Fatalf("primary called %d times, want 1", len(primary.CompleteCalls))
	}
	if len(secondary.CompleteCalls) != 0 {
		t.Fatalf("secondary called %d times, want 0", len(secondary.CompleteCalls))
	}
}

func TestLLMFallback_Complete_Failover(t *testing.T) {
	primary := &llmmock.Provider{
		CompleteErr: errors.New("primary down"),
	}
	secondary := &llmmock.Provider{
		CompleteResponse: &llm.CompletionResponse{Content: "hello from secondary"},
	}

	fb := NewLLMFallback(primary, "primary", FallbackConfig{
		CircuitBreaker: CircuitBreakerConfig{MaxFailures: 3},
	})
	fb.AddFallback("secondary", secondary)

	resp, err := fb.Complete(context.Background(), llm.CompletionRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "hello from secondary" {
		t.Fatalf("content = %q, want 'hello from secondary'", resp.Content)
	}
}

func TestLLMFallback_Complete_AllFail(t *testing.T) {
	primary := &llmmock.Provider{CompleteErr: errors.New("primary down")}
	secondary := &llmmock.Provider{CompleteErr: errors.New("secondary down")}

	fb := NewLLMFallback(primary, "primary", FallbackConfig{
		CircuitBreaker: CircuitBreakerConfig{MaxFailures: 3},
	})
	fb.AddFallback("secondary", secondary)

	_, err := fb.Complete(context.Background(), llm.CompletionRequest{})
	if !errors.Is(err, ErrAllFailed) {
		t.Fatalf("err = %v, want ErrAllFailed", err)
	}
}

func TestLLMFallback_StreamCompletion_Failover(t *testing.T) {
	primary := &llmmock.Provider{
		StreamErr: errors.New("stream failed"),
	}
	secondary := &llmmock.Provider{
		StreamChunks: []llm.Chunk{{Text: "chunk1"}, {Text: "chunk2", FinishReason: "stop"}},
	}

	fb := NewLLMFallback(primary, "primary", FallbackConfig{
		CircuitBreaker: CircuitBreakerConfig{MaxFailures: 3},
	})
	fb.AddFallback("secondary", secondary)

	ch, err := fb.StreamCompletion(context.Background(), llm.CompletionRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var chunks []llm.Chunk
	for c := range ch {
		chunks = append(chunks, c)
	}
	if len(chunks) != 2 {
		t.Fatalf("got %d chunks, want 2", len(chunks))
	}
	if chunks[0].Text != "chunk1" {
		t.Fatalf("chunk[0].Text = %q, want chunk1", chunks[0].Text)
	}
}

func TestLLMFallback_CountTokens(t *testing.T) {
	primary := &llmmock.Provider{CountTokensErr: errors.New("count failed")}
	secondary := &llmmock.Provider{TokenCount: 42}

	fb := NewLLMFallback(primary, "primary", FallbackConfig{
		CircuitBreaker: CircuitBreakerConfig{MaxFailures: 3},
	})
	fb.AddFallback("secondary", secondary)

	count, err := fb.CountTokens([]llm.Message{{Role: "user", Content: "test"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 42 {
		t.Fatalf("count = %d, want 42", count)
	}
}

func TestLLMFallback_Capabilities(t *testing.T) {
	primary := &llmmock.Provider{
		ModelCapabilities: llm.ModelCapabilities{
			ContextWindow:       128000,
			SupportsToolCalling: true,
		},
	}

	fb := NewLLMFallback(primary, "primary", FallbackConfig{
		CircuitBreaker: CircuitBreakerConfig{MaxFailures: 3},
	})

	caps := fb.Capabilities()
	if caps.ContextWindow != 128000 {
		t.Fatalf("ContextWindow = %d, want 128000", caps.ContextWindow)
	}
	if !caps.SupportsToolCalling {
		t.Fatal("SupportsToolCalling should be true")
	}
}
