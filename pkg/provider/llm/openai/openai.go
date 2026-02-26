// Package openai provides an LLM provider backed by the OpenAI API.
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
	"github.com/openai/openai-go/shared"

	"github.com/MrWong99/glyphoxa/pkg/provider/llm"
	"github.com/MrWong99/glyphoxa/pkg/types"
)

// Provider implements llm.Provider using the OpenAI API.
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

// New constructs a new OpenAI LLM Provider.
func New(apiKey string, model string, opts ...Option) (*Provider, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("openai: apiKey must not be empty")
	}
	if model == "" {
		return nil, fmt.Errorf("openai: model must not be empty")
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

// StreamCompletion implements llm.Provider.
func (p *Provider) StreamCompletion(ctx context.Context, req llm.CompletionRequest) (<-chan llm.Chunk, error) {
	params, err := p.buildParams(req)
	if err != nil {
		return nil, fmt.Errorf("openai: build params: %w", err)
	}

	stream := p.client.Chat.Completions.NewStreaming(ctx, params)
	if err := stream.Err(); err != nil {
		return nil, fmt.Errorf("openai: start stream: %w", err)
	}

	ch := make(chan llm.Chunk, 32)
	go func() {
		defer close(ch)
		defer stream.Close()

		// accumulated tool calls keyed by index
		toolCallAccum := map[int]*types.ToolCall{}

		for stream.Next() {
			chunk := stream.Current()
			if len(chunk.Choices) == 0 {
				continue
			}
			choice := chunk.Choices[0]
			delta := choice.Delta

			out := llm.Chunk{
				Text:         delta.Content,
				FinishReason: choice.FinishReason,
			}

			// Accumulate tool call fragments.
			for _, tc := range delta.ToolCalls {
				idx := int(tc.Index)
				if _, ok := toolCallAccum[idx]; !ok {
					toolCallAccum[idx] = &types.ToolCall{
						ID:   tc.ID,
						Name: tc.Function.Name,
					}
				}
				existing := toolCallAccum[idx]
				if tc.ID != "" {
					existing.ID = tc.ID
				}
				if tc.Function.Name != "" {
					existing.Name = tc.Function.Name
				}
				existing.Arguments += tc.Function.Arguments
			}

			// On the final chunk emit accumulated tool calls.
			if choice.FinishReason == "tool_calls" || (choice.FinishReason != "" && len(toolCallAccum) > 0) {
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

		if err := stream.Err(); err != nil {
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
	params, err := p.buildParams(req)
	if err != nil {
		return nil, fmt.Errorf("openai: build params: %w", err)
	}

	resp, err := p.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("openai: chat completion: %w", err)
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("openai: empty choices in response")
	}

	choice := resp.Choices[0]
	result := &llm.CompletionResponse{
		Content: choice.Message.Content,
		Usage: llm.Usage{
			PromptTokens:     int(resp.Usage.PromptTokens),
			CompletionTokens: int(resp.Usage.CompletionTokens),
			TotalTokens:      int(resp.Usage.TotalTokens),
		},
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
// TODO: replace with tiktoken-go for accurate per-model token counting.
func (p *Provider) CountTokens(messages []types.Message) (int, error) {
	total := 0
	for _, m := range messages {
		// ~4 chars per token is a rough GPT-series approximation.
		total += (len(m.Content) + 3) / 4
		// Add overhead per message (role + formatting).
		total += 4
	}
	return total, nil
}

// Capabilities implements llm.Provider.
func (p *Provider) Capabilities() types.ModelCapabilities {
	return modelCapabilities(p.model)
}

// modelCapabilities returns ModelCapabilities for known OpenAI model names.
func modelCapabilities(model string) types.ModelCapabilities {
	caps := types.ModelCapabilities{
		SupportsToolCalling: true,
		SupportsStreaming:   true,
		SupportsVision:      false,
		ContextWindow:       128_000,
		MaxOutputTokens:     4_096,
	}

	lower := strings.ToLower(model)
	switch {
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
	}
	return caps
}

// buildParams converts a CompletionRequest into OpenAI SDK params.
func (p *Provider) buildParams(req llm.CompletionRequest) (oai.ChatCompletionNewParams, error) {
	var messages []oai.ChatCompletionMessageParamUnion

	if req.SystemPrompt != "" {
		messages = append(messages, oai.SystemMessage(req.SystemPrompt))
	}

	for _, m := range req.Messages {
		msg, err := convertMessage(m)
		if err != nil {
			return oai.ChatCompletionNewParams{}, err
		}
		messages = append(messages, msg)
	}

	params := oai.ChatCompletionNewParams{
		Model:    shared.ChatModel(p.model),
		Messages: messages,
	}

	if req.Temperature != 0 {
		params.Temperature = param.NewOpt(req.Temperature)
	}
	if req.MaxTokens > 0 {
		params.MaxCompletionTokens = param.NewOpt(int64(req.MaxTokens))
	}

	for _, td := range req.Tools {
		toolParam := oai.ChatCompletionToolParam{
			Function: shared.FunctionDefinitionParam{
				Name:        td.Name,
				Description: param.NewOpt(td.Description),
				Parameters:  shared.FunctionParameters(td.Parameters),
			},
		}
		params.Tools = append(params.Tools, toolParam)
	}

	return params, nil
}

// convertMessage converts a types.Message to an OpenAI SDK message param.
func convertMessage(m types.Message) (oai.ChatCompletionMessageParamUnion, error) {
	switch m.Role {
	case "system":
		return oai.SystemMessage(m.Content), nil

	case "user":
		return oai.UserMessage(m.Content), nil

	case "assistant":
		asst := oai.ChatCompletionAssistantMessageParam{}
		if m.Content != "" {
			asst.Content.OfString = oai.String(m.Content)
		}
		if m.Name != "" {
			asst.Name = oai.String(m.Name)
		}
		for _, tc := range m.ToolCalls {
			asst.ToolCalls = append(asst.ToolCalls, oai.ChatCompletionMessageToolCallParam{
				ID: tc.ID,
				Function: oai.ChatCompletionMessageToolCallFunctionParam{
					Name:      tc.Name,
					Arguments: tc.Arguments,
				},
			})
		}
		return oai.ChatCompletionMessageParamUnion{OfAssistant: &asst}, nil

	case "tool":
		msg := oai.ToolMessage(m.Content, m.ToolCallID)
		return msg, nil

	default:
		return oai.ChatCompletionMessageParamUnion{}, fmt.Errorf("openai: unknown message role %q", m.Role)
	}
}
