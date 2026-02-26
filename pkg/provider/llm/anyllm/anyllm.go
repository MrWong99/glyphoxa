// Package anyllm provides a universal LLM provider backed by
// github.com/mozilla-ai/any-llm-go, a unified multi-provider interface that
// supports OpenAI, Anthropic, Gemini, Ollama, DeepSeek, Mistral, Groq, and more.
//
// Usage:
//
//	p, err := anyllm.New("openai", "gpt-4o", anyllmlib.WithAPIKey("sk-..."))
//	p, err := anyllm.NewAnthropic("claude-3-5-sonnet-latest", anyllmlib.WithAPIKey("sk-ant-..."))
package anyllm

import (
	"context"
	"fmt"
	"strings"

	anyllmlib "github.com/mozilla-ai/any-llm-go"
	"github.com/mozilla-ai/any-llm-go/providers/anthropic"
	"github.com/mozilla-ai/any-llm-go/providers/deepseek"
	"github.com/mozilla-ai/any-llm-go/providers/gemini"
	"github.com/mozilla-ai/any-llm-go/providers/groq"
	"github.com/mozilla-ai/any-llm-go/providers/llamacpp"
	"github.com/mozilla-ai/any-llm-go/providers/llamafile"
	"github.com/mozilla-ai/any-llm-go/providers/mistral"
	"github.com/mozilla-ai/any-llm-go/providers/ollama"
	anyllmoai "github.com/mozilla-ai/any-llm-go/providers/openai"

	"github.com/MrWong99/glyphoxa/pkg/provider/llm"
	"github.com/MrWong99/glyphoxa/pkg/types"
)

// Provider implements llm.Provider by wrapping github.com/mozilla-ai/any-llm-go.
type Provider struct {
	backend anyllmlib.Provider
	model   string
}

// New creates a new Provider backed by the given LLM provider name.
//
// providerName is one of: "openai", "anthropic", "gemini", "ollama", "deepseek",
// "mistral", "groq", "llamacpp", "llamafile".
//
// model is the specific model to use (e.g., "gpt-4o", "claude-3-5-sonnet-latest").
//
// opts are any-llm-go configuration options (e.g., anyllmlib.WithAPIKey, anyllmlib.WithBaseURL).
// If no API key option is provided, the provider will fall back to the relevant
// environment variable (e.g., OPENAI_API_KEY, ANTHROPIC_API_KEY, etc.).
func New(providerName string, model string, opts ...anyllmlib.Option) (*Provider, error) {
	if providerName == "" {
		return nil, fmt.Errorf("anyllm: providerName must not be empty")
	}
	if model == "" {
		return nil, fmt.Errorf("anyllm: model must not be empty")
	}

	backend, err := createBackend(providerName, opts...)
	if err != nil {
		return nil, fmt.Errorf("anyllm: create %q backend: %w", providerName, err)
	}

	return &Provider{backend: backend, model: model}, nil
}

// NewOpenAI creates a Provider backed by OpenAI.
// Without options, it reads the OPENAI_API_KEY environment variable.
func NewOpenAI(model string, opts ...anyllmlib.Option) (*Provider, error) {
	return New("openai", model, opts...)
}

// NewAnthropic creates a Provider backed by Anthropic.
// Without options, it reads the ANTHROPIC_API_KEY environment variable.
func NewAnthropic(model string, opts ...anyllmlib.Option) (*Provider, error) {
	return New("anthropic", model, opts...)
}

// NewGemini creates a Provider backed by Google Gemini.
// Without options, it reads the GEMINI_API_KEY or GOOGLE_API_KEY environment variable.
func NewGemini(model string, opts ...anyllmlib.Option) (*Provider, error) {
	return New("gemini", model, opts...)
}

// NewOllama creates a Provider backed by Ollama (local inference).
// Without options, it connects to http://localhost:11434.
func NewOllama(model string, opts ...anyllmlib.Option) (*Provider, error) {
	return New("ollama", model, opts...)
}

// NewDeepSeek creates a Provider backed by DeepSeek.
// Without options, it reads the DEEPSEEK_API_KEY environment variable.
func NewDeepSeek(model string, opts ...anyllmlib.Option) (*Provider, error) {
	return New("deepseek", model, opts...)
}

// NewMistral creates a Provider backed by Mistral AI.
// Without options, it reads the MISTRAL_API_KEY environment variable.
func NewMistral(model string, opts ...anyllmlib.Option) (*Provider, error) {
	return New("mistral", model, opts...)
}

// NewGroq creates a Provider backed by Groq.
// Without options, it reads the GROQ_API_KEY environment variable.
func NewGroq(model string, opts ...anyllmlib.Option) (*Provider, error) {
	return New("groq", model, opts...)
}

// NewLlamaCpp creates a Provider backed by a running llama.cpp server.
// Without options, it connects to http://127.0.0.1:8080/v1.
func NewLlamaCpp(model string, opts ...anyllmlib.Option) (*Provider, error) {
	return New("llamacpp", model, opts...)
}

// NewLlamaFile creates a Provider backed by a running llamafile server.
// Without options, it connects to the default llamafile server.
func NewLlamaFile(model string, opts ...anyllmlib.Option) (*Provider, error) {
	return New("llamafile", model, opts...)
}

// createBackend creates the underlying any-llm-go provider for the given provider name.
func createBackend(providerName string, opts ...anyllmlib.Option) (anyllmlib.Provider, error) {
	switch strings.ToLower(providerName) {
	case "openai":
		return anyllmoai.New(opts...)
	case "anthropic":
		return anthropic.New(opts...)
	case "gemini":
		return gemini.New(opts...)
	case "ollama":
		return ollama.New(opts...)
	case "deepseek":
		return deepseek.New(opts...)
	case "mistral":
		return mistral.New(opts...)
	case "groq":
		return groq.New(opts...)
	case "llamacpp":
		return llamacpp.New(opts...)
	case "llamafile":
		return llamafile.New(opts...)
	default:
		return nil, fmt.Errorf("unsupported provider %q; supported: openai, anthropic, gemini, ollama, deepseek, mistral, groq, llamacpp, llamafile", providerName)
	}
}

// StreamCompletion implements llm.Provider.
func (p *Provider) StreamCompletion(ctx context.Context, req llm.CompletionRequest) (<-chan llm.Chunk, error) {
	params := p.buildParams(req)

	backendChunks, backendErrs := p.backend.CompletionStream(ctx, params)

	ch := make(chan llm.Chunk, 32)
	go func() {
		defer close(ch)

		// Accumulated tool calls keyed by index.
		toolCallAccum := map[int]*types.ToolCall{}

		for chunk := range backendChunks {
			if len(chunk.Choices) == 0 {
				continue
			}
			choice := chunk.Choices[0]
			delta := choice.Delta

			out := llm.Chunk{
				Text:         delta.Content,
				FinishReason: choice.FinishReason,
			}

			// Accumulate tool call fragments by index within this chunk.
			for i, tc := range delta.ToolCalls {
				if _, ok := toolCallAccum[i]; !ok {
					toolCallAccum[i] = &types.ToolCall{
						ID:   tc.ID,
						Name: tc.Function.Name,
					}
				}
				existing := toolCallAccum[i]
				if tc.ID != "" {
					existing.ID = tc.ID
				}
				if tc.Function.Name != "" {
					existing.Name = tc.Function.Name
				}
				existing.Arguments += tc.Function.Arguments
			}

			// On the final chunk, emit accumulated tool calls.
			if choice.FinishReason == anyllmlib.FinishReasonToolCalls ||
				(choice.FinishReason != "" && len(toolCallAccum) > 0) {
				for i := 0; i < len(toolCallAccum); i++ {
					if tc, ok := toolCallAccum[i]; ok {
						out.ToolCalls = append(out.ToolCalls, *tc)
					}
				}
			}

			select {
			case ch <- out:
			case <-ctx.Done():
				return
			}
		}

		// Check for backend errors after the chunk channel is drained.
		if err := <-backendErrs; err != nil {
			select {
			case ch <- llm.Chunk{FinishReason: "error", Text: err.Error()}:
			case <-ctx.Done():
			}
		}
	}()

	return ch, nil
}

// Complete implements llm.Provider.
func (p *Provider) Complete(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
	params := p.buildParams(req)

	resp, err := p.backend.Completion(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("anyllm: completion: %w", err)
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("anyllm: empty choices in response")
	}

	choice := resp.Choices[0]
	result := &llm.CompletionResponse{
		Content: choice.Message.ContentString(),
	}
	if resp.Usage != nil {
		result.Usage = llm.Usage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		}
	}
	for _, tc := range choice.Message.ToolCalls {
		result.ToolCalls = append(result.ToolCalls, types.ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}
	return result, nil
}

// CountTokens implements llm.Provider.
// TODO: replace with a real tokenizer (e.g., tiktoken-go) for accurate per-model counting.
func (p *Provider) CountTokens(messages []types.Message) (int, error) {
	total := 0
	for _, m := range messages {
		// ~4 chars per token is a rough approximation for most models.
		total += (len(m.Content) + 3) / 4
		// Per-message overhead (role + formatting tokens).
		total += 4
	}
	return total, nil
}

// Capabilities implements llm.Provider.
func (p *Provider) Capabilities() types.ModelCapabilities {
	return modelCapabilities(p.model)
}

// buildParams converts our CompletionRequest into anyllm CompletionParams.
func (p *Provider) buildParams(req llm.CompletionRequest) anyllmlib.CompletionParams {
	var messages []anyllmlib.Message

	if req.SystemPrompt != "" {
		messages = append(messages, anyllmlib.Message{
			Role:    anyllmlib.RoleSystem,
			Content: req.SystemPrompt,
		})
	}

	for _, m := range req.Messages {
		messages = append(messages, convertMessage(m))
	}

	params := anyllmlib.CompletionParams{
		Model:    p.model,
		Messages: messages,
	}

	if req.Temperature != 0 {
		t := req.Temperature
		params.Temperature = &t
	}
	if req.MaxTokens > 0 {
		mt := req.MaxTokens
		params.MaxTokens = &mt
	}

	for _, td := range req.Tools {
		params.Tools = append(params.Tools, anyllmlib.Tool{
			Type: "function",
			Function: anyllmlib.Function{
				Name:        td.Name,
				Description: td.Description,
				Parameters:  td.Parameters,
			},
		})
	}

	return params
}

// convertMessage converts our types.Message to anyllm.Message.
func convertMessage(m types.Message) anyllmlib.Message {
	msg := anyllmlib.Message{
		Role:       m.Role,
		Content:    m.Content,
		Name:       m.Name,
		ToolCallID: m.ToolCallID,
	}

	for _, tc := range m.ToolCalls {
		msg.ToolCalls = append(msg.ToolCalls, anyllmlib.ToolCall{
			ID:   tc.ID,
			Type: "function",
			Function: anyllmlib.FunctionCall{
				Name:      tc.Name,
				Arguments: tc.Arguments,
			},
		})
	}

	return msg
}

// modelCapabilities returns ModelCapabilities based on known model names.
// This covers OpenAI, Anthropic, and Gemini model families.
// Unknown models receive sensible defaults.
func modelCapabilities(model string) types.ModelCapabilities {
	// Sensible defaults for unknown models.
	caps := types.ModelCapabilities{
		SupportsToolCalling: true,
		SupportsStreaming:   true,
		SupportsVision:      false,
		ContextWindow:       128_000,
		MaxOutputTokens:     4_096,
	}

	lower := strings.ToLower(model)

	switch {
	// ── OpenAI GPT-4o family ─────────────────────────────────────────────────
	case strings.HasPrefix(lower, "gpt-4o-mini"):
		caps.ContextWindow = 128_000
		caps.MaxOutputTokens = 16_384
		caps.SupportsVision = true

	case strings.HasPrefix(lower, "gpt-4o"):
		caps.ContextWindow = 128_000
		caps.MaxOutputTokens = 16_384
		caps.SupportsVision = true

	case strings.HasPrefix(lower, "gpt-4-turbo"):
		caps.ContextWindow = 128_000
		caps.MaxOutputTokens = 4_096
		caps.SupportsVision = true

	case strings.HasPrefix(lower, "gpt-4"):
		caps.ContextWindow = 8_192
		caps.MaxOutputTokens = 4_096
		caps.SupportsVision = false

	case strings.HasPrefix(lower, "gpt-3.5-turbo"):
		caps.ContextWindow = 16_385
		caps.MaxOutputTokens = 4_096
		caps.SupportsVision = false

	// ── OpenAI o-series reasoning models ─────────────────────────────────────
	case strings.HasPrefix(lower, "o1-mini"):
		caps.ContextWindow = 128_000
		caps.MaxOutputTokens = 65_536
		caps.SupportsVision = false
		caps.SupportsToolCalling = false

	case strings.HasPrefix(lower, "o1"):
		caps.ContextWindow = 200_000
		caps.MaxOutputTokens = 100_000
		caps.SupportsVision = true
		caps.SupportsToolCalling = true

	case strings.HasPrefix(lower, "o3-mini"):
		caps.ContextWindow = 200_000
		caps.MaxOutputTokens = 100_000
		caps.SupportsVision = false
		caps.SupportsToolCalling = true

	case strings.HasPrefix(lower, "o3"):
		caps.ContextWindow = 200_000
		caps.MaxOutputTokens = 100_000
		caps.SupportsVision = true
		caps.SupportsToolCalling = true

	// ── Anthropic Claude models ───────────────────────────────────────────────
	// Matched before generic "claude" to ensure correct ordering.
	case strings.Contains(lower, "claude-3-5-sonnet"),
		strings.Contains(lower, "claude-3-sonnet"):
		caps.ContextWindow = 200_000
		caps.MaxOutputTokens = 8_192
		caps.SupportsVision = true
		caps.SupportsToolCalling = true

	case strings.Contains(lower, "claude-3-5-haiku"),
		strings.Contains(lower, "claude-3-haiku"):
		caps.ContextWindow = 200_000
		caps.MaxOutputTokens = 8_192
		caps.SupportsVision = true
		caps.SupportsToolCalling = true

	case strings.Contains(lower, "claude-3-opus"):
		caps.ContextWindow = 200_000
		caps.MaxOutputTokens = 4_096
		caps.SupportsVision = true
		caps.SupportsToolCalling = true

	case strings.HasPrefix(lower, "claude"):
		// Catch-all for newer or unrecognised Claude models.
		caps.ContextWindow = 200_000
		caps.MaxOutputTokens = 8_192
		caps.SupportsVision = true
		caps.SupportsToolCalling = true

	// ── Google Gemini models ──────────────────────────────────────────────────
	case strings.Contains(lower, "gemini-2.0-flash"):
		caps.ContextWindow = 1_048_576
		caps.MaxOutputTokens = 8_192
		caps.SupportsVision = true
		caps.SupportsToolCalling = true

	case strings.Contains(lower, "gemini-1.5-pro"):
		caps.ContextWindow = 2_097_152
		caps.MaxOutputTokens = 8_192
		caps.SupportsVision = true
		caps.SupportsToolCalling = true

	case strings.Contains(lower, "gemini-1.5-flash"):
		caps.ContextWindow = 1_048_576
		caps.MaxOutputTokens = 8_192
		caps.SupportsVision = true
		caps.SupportsToolCalling = true

	case strings.HasPrefix(lower, "gemini"):
		// Catch-all for other Gemini models.
		caps.ContextWindow = 128_000
		caps.MaxOutputTokens = 8_192
		caps.SupportsVision = true
		caps.SupportsToolCalling = true
	}

	return caps
}
