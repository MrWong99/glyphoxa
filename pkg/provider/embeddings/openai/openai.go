// Package openai provides an embeddings provider backed by the OpenAI API.
package openai

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	oai "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"

	"github.com/MrWong99/glyphoxa/pkg/provider/embeddings"
)

// DefaultModel is the default OpenAI embeddings model.
const DefaultModel = oai.EmbeddingModelTextEmbedding3Small

// Ensure Provider implements the embeddings.Provider interface.
var _ embeddings.Provider = (*Provider)(nil)

// Provider implements embeddings.Provider using the OpenAI API.
type Provider struct {
	client oai.Client
	model  string
}

// config holds optional configuration for the provider.
type config struct {
	baseURL      string
	organization string
	timeout      time.Duration
}

// Option is a functional option for Provider.
type Option func(*config)

// WithBaseURL overrides the default OpenAI API base URL.
func WithBaseURL(url string) Option {
	return func(c *config) {
		c.baseURL = url
	}
}

// WithOrganization sets the OpenAI organization ID on all requests.
func WithOrganization(org string) Option {
	return func(c *config) {
		c.organization = org
	}
}

// WithTimeout sets a per-request HTTP timeout.
func WithTimeout(d time.Duration) Option {
	return func(c *config) {
		c.timeout = d
	}
}

// New constructs a new OpenAI Embeddings Provider.
// If model is empty, DefaultModel (text-embedding-3-small) is used.
func New(apiKey string, model string, opts ...Option) (*Provider, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("openai embeddings: apiKey must not be empty")
	}
	if model == "" {
		model = DefaultModel
	}

	cfg := &config{}
	for _, o := range opts {
		o(cfg)
	}

	reqOpts := []option.RequestOption{
		option.WithAPIKey(apiKey),
	}
	if cfg.baseURL != "" {
		reqOpts = append(reqOpts, option.WithBaseURL(cfg.baseURL))
	}
	if cfg.organization != "" {
		reqOpts = append(reqOpts, option.WithOrganization(cfg.organization))
	}
	if cfg.timeout > 0 {
		reqOpts = append(reqOpts, option.WithHTTPClient(&http.Client{
			Timeout: cfg.timeout,
		}))
	}

	client := oai.NewClient(reqOpts...)
	return &Provider{client: client, model: model}, nil
}

// Embed implements embeddings.Provider.
func (p *Provider) Embed(ctx context.Context, text string) ([]float32, error) {
	resp, err := p.client.Embeddings.New(ctx, oai.EmbeddingNewParams{
		Model: p.model,
		Input: oai.EmbeddingNewParamsInputUnion{
			OfString: param.NewOpt(text),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("openai embeddings: embed: %w", err)
	}
	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("openai embeddings: empty response")
	}
	return float64ToFloat32(resp.Data[0].Embedding), nil
}

// EmbedBatch implements embeddings.Provider.
func (p *Provider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	resp, err := p.client.Embeddings.New(ctx, oai.EmbeddingNewParams{
		Model: p.model,
		Input: oai.EmbeddingNewParamsInputUnion{
			OfArrayOfStrings: texts,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("openai embeddings: embed batch: %w", err)
	}
	if len(resp.Data) != len(texts) {
		return nil, fmt.Errorf("openai embeddings: expected %d embeddings, got %d", len(texts), len(resp.Data))
	}

	result := make([][]float32, len(texts))
	for _, e := range resp.Data {
		if int(e.Index) >= len(texts) {
			return nil, fmt.Errorf("openai embeddings: unexpected index %d", e.Index)
		}
		result[e.Index] = float64ToFloat32(e.Embedding)
	}
	return result, nil
}

// Dimensions implements embeddings.Provider.
func (p *Provider) Dimensions() int {
	return modelDimensions(p.model)
}

// ModelID implements embeddings.Provider.
func (p *Provider) ModelID() string {
	return p.model
}

// modelDimensions returns the embedding dimensions for known OpenAI models.
func modelDimensions(model string) int {
	lower := strings.ToLower(model)
	switch {
	case strings.Contains(lower, "text-embedding-3-large"):
		return 3072
	case strings.Contains(lower, "text-embedding-3-small"):
		return 1536
	case strings.Contains(lower, "text-embedding-ada-002"):
		return 1536
	default:
		return 1536 // sensible default for unknown models
	}
}

// float64ToFloat32 converts a []float64 slice to []float32.
func float64ToFloat32(in []float64) []float32 {
	out := make([]float32, len(in))
	for i, v := range in {
		out[i] = float32(v)
	}
	return out
}
