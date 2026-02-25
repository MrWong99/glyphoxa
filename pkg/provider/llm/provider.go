// Package llm defines the Provider interface for Large Language Model backends.
//
// An LLM provider wraps a remote or local model API (e.g., OpenAI GPT-4, Anthropic
// Claude, or a local Ollama instance) and exposes a uniform interface for the
// Glyphoxa orchestrator to perform completions, count tokens, and inspect model
// capabilities without coupling to any specific SDK.
//
// Implementors must be safe for concurrent use. Channels returned by
// StreamCompletion must be closed by the implementation when the stream ends or
// when the supplied context is cancelled.
package llm

import (
	"context"

	"github.com/MrWong99/glyphoxa/pkg/types"
)

// Usage holds token accounting information returned by the LLM backend.
// All counts are in the model's native token unit and may differ between providers
// for the same textual content.
type Usage struct {
	// PromptTokens is the number of tokens consumed by the input messages and system
	// prompt. This value directly affects billing and context-window budget tracking.
	PromptTokens int

	// CompletionTokens is the number of tokens generated in the response.
	CompletionTokens int

	// TotalTokens is PromptTokens + CompletionTokens. Provided as a convenience;
	// some providers return it directly rather than computing it from the parts.
	TotalTokens int
}

// CompletionRequest carries everything the LLM needs to produce a response.
// Callers should treat a zero-value request as invalid; at minimum Messages must
// be non-empty.
type CompletionRequest struct {
	// Messages is the ordered conversation history. The last message is typically
	// from the "user" role and drives the response.
	Messages []types.Message

	// Tools is the set of function/tool definitions offered to the model. The model
	// may choose to call one or more of them in its response.
	// Providers that do not support tool calling should return an error or ignore this
	// field — callers should check Capabilities().SupportsToolCalling first.
	Tools []types.ToolDefinition

	// Temperature controls output randomness in the range [0.0, 2.0]. Lower values
	// produce more deterministic outputs; higher values increase creativity. A value
	// of 0.0 typically requests greedy (argmax) decoding.
	Temperature float64

	// MaxTokens caps the number of completion tokens the model may generate.
	// Zero means use the provider default (usually the model's MaxOutputTokens).
	MaxTokens int

	// SystemPrompt is an optional high-priority instruction injected before the
	// conversation history. Many providers give this special treatment (e.g.,
	// OpenAI's "system" role, Anthropic's separate system field). If the provider
	// does not natively support a dedicated system prompt, implementors should
	// prepend it as a "system"-role message.
	SystemPrompt string
}

// Chunk is a single token or fragment emitted by a streaming completion.
// Consumers must handle all three fields; a single chunk may carry text, a finish
// signal, tool calls, or any combination thereof.
type Chunk struct {
	// Text is the incremental text content of this chunk. May be empty if the chunk
	// carries only ToolCalls or a FinishReason.
	Text string

	// FinishReason is set on the final chunk and indicates why generation stopped.
	// Common values are "stop" (natural end), "length" (MaxTokens reached),
	// "tool_calls" (model wants to invoke tools), and "" (non-final chunk).
	FinishReason string

	// ToolCalls contains any tool invocations the model is requesting. For streaming
	// providers this may be accumulated across multiple chunks by the caller.
	ToolCalls []types.ToolCall
}

// CompletionResponse is returned by the non-streaming Complete method.
type CompletionResponse struct {
	// Content is the full text of the assistant's reply. Empty when the model
	// responds exclusively with tool calls.
	Content string

	// ToolCalls lists all tool invocations requested by the model. The caller is
	// responsible for executing them and appending the results to the conversation.
	ToolCalls []types.ToolCall

	// Usage contains token accounting for this request/response pair.
	Usage Usage
}

// Provider is the abstraction over any LLM backend.
//
// Implementations must be safe for concurrent use from multiple goroutines. Each
// method should propagate context cancellation promptly: when ctx is cancelled the
// method must return (or close its channel) as quickly as possible.
type Provider interface {
	// StreamCompletion sends req to the model and returns a read-only channel that
	// emits Chunk values as they arrive. The channel is closed by the implementation
	// when generation finishes or when ctx is cancelled.
	//
	// Callers must drain the channel to avoid goroutine leaks. Errors that occur
	// after the channel is opened are surfaced as a Chunk with a special FinishReason
	// value of "error"; the initial error return is non-nil only for failures that
	// prevent the stream from starting (e.g., invalid credentials, malformed request).
	//
	// The returned channel must never be nil when error is nil.
	StreamCompletion(ctx context.Context, req CompletionRequest) (<-chan Chunk, error)

	// Complete sends req to the model and waits for the full response. It is a
	// convenience wrapper around StreamCompletion for callers that do not need
	// incremental output and do not want to manage a channel.
	//
	// Returns an error if the request fails or if ctx is cancelled before
	// the completion arrives.
	Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error)

	// CountTokens estimates the number of tokens that the given message list would
	// consume in the model's context window. This is used by the orchestrator to
	// enforce context budget limits before sending a request.
	//
	// Implementations may call the provider's tokenisation API or perform a local
	// approximation. The result need not be exact but should not undercount.
	CountTokens(messages []types.Message) (int, error)

	// Capabilities returns static metadata describing what this provider's underlying
	// model supports. The result is assumed to be constant for the lifetime of the
	// Provider instance.
	Capabilities() types.ModelCapabilities
}
