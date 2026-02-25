// Package embeddings defines the Provider interface for vector embedding backends.
//
// An embeddings provider wraps a service that maps text strings to dense float32
// vectors (e.g., OpenAI text-embedding-3, Cohere embed-v3, or a local sentence
// transformer). These vectors are used by the memory layer for semantic retrieval,
// NPC knowledge-base lookup, and similarity ranking.
//
// Implementations must be safe for concurrent use.
package embeddings

import "context"

// Provider is the abstraction over any text-embedding backend.
//
// All embedding vectors returned by a single Provider instance must share the same
// dimensionality (returned by Dimensions). Callers must not mix vectors from
// different Provider instances in the same similarity computation unless they have
// verified that both use the same model and space.
//
// Implementations must be safe for concurrent use.
type Provider interface {
	// Embed computes the embedding vector for a single text string. Returns a
	// float32 slice of length Dimensions() or an error if the request fails or ctx
	// is cancelled.
	//
	// The input text should be pre-processed according to the model's requirements
	// (e.g., some models expect a "query: " prefix for retrieval tasks). Callers are
	// responsible for any such formatting; the Provider passes text through verbatim.
	Embed(ctx context.Context, text string) ([]float32, error)

	// EmbedBatch computes embedding vectors for a slice of text strings in a single
	// provider call, which is typically far more efficient than calling Embed in a
	// loop. The returned slice has the same length as texts and the i-th element
	// corresponds to texts[i].
	//
	// Returns an error if any single embedding fails or if ctx is cancelled. Partial
	// results are not returned — on error the entire slice is nil.
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)

	// Dimensions returns the fixed length of every embedding vector produced by this
	// provider. The value is determined by the underlying model and is constant for
	// the lifetime of the Provider instance.
	Dimensions() int

	// ModelID returns the provider-specific model identifier used for embeddings
	// (e.g., "text-embedding-3-small", "embed-english-v3.0"). Useful for logging
	// and for ensuring consistent model usage across a session.
	ModelID() string
}
