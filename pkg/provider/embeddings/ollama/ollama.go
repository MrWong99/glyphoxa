// Package ollama provides an embeddings provider backed by a local Ollama server.
//
// Ollama (https://ollama.com) hosts local large language models and embedding
// models. This package uses Ollama's native /api/embed endpoint to generate
// dense float32 vectors with models such as nomic-embed-text, mxbai-embed-large,
// and all-minilm.
//
// Example usage:
//
//	p, err := ollama.New("", "nomic-embed-text") // connects to http://localhost:11434
//	if err != nil {
//	    log.Fatal(err)
//	}
//	vec, err := p.Embed(ctx, "query: Hello, world!")
//
// Only standard library packages are used — no additional dependencies are
// required beyond Go's net/http and encoding/json.
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/MrWong99/glyphoxa/pkg/provider/embeddings"
)

// DefaultBaseURL is the default base URL for a locally running Ollama instance.
const DefaultBaseURL = "http://localhost:11434"

// Ensure Provider implements the embeddings.Provider interface at compile time.
var _ embeddings.Provider = (*Provider)(nil)

// Provider implements embeddings.Provider using a local Ollama server.
//
// Dimension resolution happens in this order:
//  1. Value supplied via WithDimensions option (highest priority).
//  2. Look-up in the built-in knownDimensions table for recognised model names.
//  3. Auto-detection: a single probe embed is issued on the first Dimensions call
//     and the length of the returned vector is cached for the lifetime of the
//     Provider.
//
// Provider is safe for concurrent use.
type Provider struct {
	baseURL    string
	model      string
	httpClient *http.Client

	// dimensions holds the resolved vector length. When zero after construction,
	// it is populated lazily by detectOnce.
	dimensions int
	detectOnce sync.Once
	detectErr  error
}

// config holds optional configuration collected from functional options.
type config struct {
	timeout    time.Duration
	dimensions int
}

// Option is a functional option for Provider.
type Option func(*config)

// WithTimeout sets a per-request HTTP timeout on the underlying HTTP client.
// A zero or negative value means no timeout (the default).
func WithTimeout(d time.Duration) Option {
	return func(c *config) {
		c.timeout = d
	}
}

// WithDimensions pre-sets the embedding dimension, bypassing the look-up table
// and avoiding the probe request that Dimensions() would otherwise issue for
// unknown models on first call. Use this when you know the dimension in advance.
func WithDimensions(dims int) Option {
	return func(c *config) {
		c.dimensions = dims
	}
}

// New constructs a new Ollama Provider.
//
// baseURL is the base URL of the Ollama server (e.g., "http://localhost:11434").
// If empty, DefaultBaseURL is used. A trailing slash is stripped automatically.
//
// model is the Ollama model name to use for embeddings (e.g., "nomic-embed-text").
// It must not be empty.
//
// Optional configuration is applied via functional options (see WithTimeout and
// WithDimensions).
func New(baseURL string, model string, opts ...Option) (*Provider, error) {
	if model == "" {
		return nil, fmt.Errorf("ollama embeddings: model must not be empty")
	}
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	// Strip trailing slash for consistent URL construction.
	baseURL = strings.TrimRight(baseURL, "/")

	cfg := &config{}
	for _, o := range opts {
		o(cfg)
	}

	httpClient := &http.Client{}
	if cfg.timeout > 0 {
		httpClient.Timeout = cfg.timeout
	}

	p := &Provider{
		baseURL:    baseURL,
		model:      model,
		httpClient: httpClient,
		dimensions: cfg.dimensions,
	}

	// Pre-populate from the known-models table when no explicit dimension was
	// provided, to avoid a probe request for well-known models.
	if p.dimensions == 0 {
		p.dimensions = knownDimensions(model)
	}

	return p, nil
}

// embedRequest is the JSON request body sent to Ollama's /api/embed endpoint.
type embedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

// embedResponse is the JSON response body returned by Ollama's /api/embed endpoint.
type embedResponse struct {
	Model      string      `json:"model"`
	Embeddings [][]float32 `json:"embeddings"`
}

// Embed implements embeddings.Provider by computing the embedding vector for a
// single text string.
//
// The text is forwarded verbatim to Ollama. Any model-specific prompt formatting
// (e.g., a "query: " or "passage: " prefix required by nomic-embed-text) is the
// caller's responsibility.
//
// Returns an error if the HTTP request fails, the server returns a non-200 status,
// the response cannot be decoded, or ctx is cancelled.
func (p *Provider) Embed(ctx context.Context, text string) ([]float32, error) {
	vecs, err := p.callEmbed(ctx, []string{text})
	if err != nil {
		return nil, fmt.Errorf("ollama embeddings: embed: %w", err)
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("ollama embeddings: embed: empty response")
	}
	return vecs[0], nil
}

// EmbedBatch implements embeddings.Provider by computing embedding vectors for
// a slice of texts in a single Ollama /api/embed request.
//
// The returned slice has the same length as texts and is ordered identically
// (result[i] corresponds to texts[i]). On any error, nil is returned — partial
// results are not exposed.
//
// Passing a nil or empty texts slice returns (nil, nil) without issuing any
// network request.
func (p *Provider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	vecs, err := p.callEmbed(ctx, texts)
	if err != nil {
		return nil, fmt.Errorf("ollama embeddings: embed batch: %w", err)
	}
	if len(vecs) != len(texts) {
		return nil, fmt.Errorf("ollama embeddings: embed batch: expected %d embeddings, got %d", len(texts), len(vecs))
	}
	return vecs, nil
}

// Dimensions implements embeddings.Provider by returning the fixed vector length
// produced by this provider.
//
// The value is resolved in the following order:
//  1. Explicitly configured value (via WithDimensions).
//  2. Built-in table for known model names (nomic-embed-text → 768, etc.).
//  3. Auto-detection: a probe embed is issued once against the live server and
//     the dimension is inferred from the vector length. The result is cached;
//     if the probe fails, 0 is returned.
func (p *Provider) Dimensions() int {
	if p.dimensions != 0 {
		return p.dimensions
	}
	// Auto-detect by issuing a single probe request against the real server.
	p.detectOnce.Do(func() {
		vecs, err := p.callEmbed(context.Background(), []string{"probe"})
		if err != nil {
			p.detectErr = err
			return
		}
		if len(vecs) > 0 {
			p.dimensions = len(vecs[0])
		}
	})
	return p.dimensions
}

// ModelID implements embeddings.Provider by returning the Ollama model name
// supplied at construction time (e.g., "nomic-embed-text").
func (p *Provider) ModelID() string {
	return p.model
}

// callEmbed is the internal helper that sends a POST /api/embed request to the
// Ollama server and returns the raw embedding vectors.
//
// It respects context cancellation via http.NewRequestWithContext.
func (p *Provider) callEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	body, err := json.Marshal(embedRequest{
		Model: p.model,
		Input: texts,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	var result embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if len(result.Embeddings) == 0 {
		return nil, fmt.Errorf("empty embeddings in response")
	}
	return result.Embeddings, nil
}

// knownDimensions returns the well-known output dimension for recognised Ollama
// embedding model names. Returns 0 for unknown models, which triggers
// auto-detection on the first Dimensions() call.
func knownDimensions(model string) int {
	lower := strings.ToLower(model)
	switch {
	case strings.Contains(lower, "nomic-embed-text"):
		return 768
	case strings.Contains(lower, "mxbai-embed-large"):
		return 1024
	case strings.Contains(lower, "all-minilm"):
		return 384
	default:
		return 0 // will be probed on first Dimensions() call
	}
}
