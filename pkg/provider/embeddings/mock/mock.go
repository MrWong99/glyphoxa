// Package mock provides a test double for the embeddings.Provider interface.
//
// Use Provider to return pre-canned embedding vectors without a live model
// and to verify that the correct texts are submitted for embedding.
//
// Example:
//
//	p := &mock.Provider{
//	    EmbedResult:     []float32{0.1, 0.2, 0.3},
//	    DimensionsValue: 3,
//	    ModelIDValue:    "test-embed-v1",
//	}
//	vec, _ := p.Embed(ctx, "hello world")
package mock

import (
	"context"
	"sync"

	"github.com/MrWong99/glyphoxa/pkg/provider/embeddings"
)

// EmbedCall records a single invocation of Embed.
type EmbedCall struct {
	// Ctx is the context passed to Embed.
	Ctx context.Context
	// Text is the string passed to Embed.
	Text string
}

// EmbedBatchCall records a single invocation of EmbedBatch.
type EmbedBatchCall struct {
	// Ctx is the context passed to EmbedBatch.
	Ctx context.Context
	// Texts is a copy of the string slice passed to EmbedBatch.
	Texts []string
}

// Provider is a mock implementation of embeddings.Provider.
type Provider struct {
	mu sync.Mutex

	// --- Configurable responses ---

	// EmbedResult is returned by Embed. If nil, a zero-length slice is returned.
	EmbedResult []float32

	// EmbedErr, if non-nil, is returned as the error from Embed.
	EmbedErr error

	// EmbedBatchResult is returned by EmbedBatch. If nil, an empty slice of slices
	// is returned (one per input text, each nil).
	EmbedBatchResult [][]float32

	// EmbedBatchErr, if non-nil, is returned as the error from EmbedBatch.
	EmbedBatchErr error

	// DimensionsValue is returned by Dimensions.
	DimensionsValue int

	// ModelIDValue is returned by ModelID.
	ModelIDValue string

	// --- Call records ---

	// EmbedCalls records every call to Embed in order.
	EmbedCalls []EmbedCall

	// EmbedBatchCalls records every call to EmbedBatch in order.
	EmbedBatchCalls []EmbedBatchCall

	// DimensionsCallCount is the number of times Dimensions was called.
	DimensionsCallCount int

	// ModelIDCallCount is the number of times ModelID was called.
	ModelIDCallCount int
}

// Embed records the call and returns EmbedResult, EmbedErr.
func (p *Provider) Embed(ctx context.Context, text string) ([]float32, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.EmbedCalls = append(p.EmbedCalls, EmbedCall{Ctx: ctx, Text: text})
	return p.EmbedResult, p.EmbedErr
}

// EmbedBatch records the call and returns EmbedBatchResult, EmbedBatchErr.
// If EmbedBatchResult is nil, it returns a slice of nil slices matching the
// length of texts.
func (p *Provider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	cp := make([]string, len(texts))
	copy(cp, texts)
	p.EmbedBatchCalls = append(p.EmbedBatchCalls, EmbedBatchCall{Ctx: ctx, Texts: cp})
	if p.EmbedBatchErr != nil {
		return nil, p.EmbedBatchErr
	}
	if p.EmbedBatchResult != nil {
		return p.EmbedBatchResult, nil
	}
	// Return a slice of nil slices so the caller gets the right length.
	result := make([][]float32, len(texts))
	return result, nil
}

// Dimensions records the call and returns DimensionsValue.
func (p *Provider) Dimensions() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.DimensionsCallCount++
	return p.DimensionsValue
}

// ModelID records the call and returns ModelIDValue.
func (p *Provider) ModelID() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ModelIDCallCount++
	return p.ModelIDValue
}

// Reset clears all recorded calls. Thread-safe.
func (p *Provider) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.EmbedCalls = nil
	p.EmbedBatchCalls = nil
	p.DimensionsCallCount = 0
	p.ModelIDCallCount = 0
}

// Ensure Provider implements embeddings.Provider at compile time.
var _ embeddings.Provider = (*Provider)(nil)
