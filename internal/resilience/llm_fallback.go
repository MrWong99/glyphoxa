package resilience

import (
	"context"

	"github.com/MrWong99/glyphoxa/pkg/provider/llm"
)

// LLMFallback implements [llm.Provider] with automatic failover across multiple
// LLM backends. Each backend has its own circuit breaker; when the primary fails
// or its breaker is open, the next healthy fallback is tried.
type LLMFallback struct {
	group *FallbackGroup[llm.Provider]
}

// Compile-time interface assertion.
var _ llm.Provider = (*LLMFallback)(nil)

// NewLLMFallback creates an [LLMFallback] with primary as the preferred backend.
func NewLLMFallback(primary llm.Provider, primaryName string, cfg FallbackConfig) *LLMFallback {
	return &LLMFallback{
		group: NewFallbackGroup(primary, primaryName, cfg),
	}
}

// AddFallback registers an additional LLM provider as a fallback.
func (f *LLMFallback) AddFallback(name string, provider llm.Provider) {
	f.group.AddFallback(name, provider)
}

// Complete sends the request to the first healthy provider and returns its
// response. If the primary fails, subsequent fallbacks are tried.
func (f *LLMFallback) Complete(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
	return ExecuteWithResult(f.group, func(p llm.Provider) (*llm.CompletionResponse, error) {
		return p.Complete(ctx, req)
	})
}

// StreamCompletion sends the request to the first healthy provider and returns a
// streaming chunk channel. Note: only the initial connection attempt is covered
// by failover; once a stream is established, mid-stream errors are the caller's
// responsibility.
func (f *LLMFallback) StreamCompletion(ctx context.Context, req llm.CompletionRequest) (<-chan llm.Chunk, error) {
	return ExecuteWithResult(f.group, func(p llm.Provider) (<-chan llm.Chunk, error) {
		return p.StreamCompletion(ctx, req)
	})
}

// CountTokens delegates to the first healthy provider's token counter.
func (f *LLMFallback) CountTokens(messages []llm.Message) (int, error) {
	return ExecuteWithResult(f.group, func(p llm.Provider) (int, error) {
		return p.CountTokens(messages)
	})
}

// Capabilities returns the capabilities of the first entry (the primary).
// This does not participate in failover because capabilities are static metadata.
func (f *LLMFallback) Capabilities() llm.ModelCapabilities {
	if len(f.group.entries) > 0 {
		return f.group.entries[0].value.Capabilities()
	}
	return llm.ModelCapabilities{}
}
