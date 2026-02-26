package llm

// Message represents a single message in an LLM conversation history.
type Message struct {
	// Role is one of "system", "user", "assistant", or "tool".
	Role string

	// Content is the text content of the message.
	Content string

	// Name is an optional participant name (for multi-speaker contexts).
	Name string

	// ToolCalls contains any tool invocations requested by the assistant.
	ToolCalls []ToolCall

	// ToolCallID is set when Role is "tool", identifying which tool call this responds to.
	ToolCallID string
}

// ToolCall represents a tool/function invocation requested by the LLM.
type ToolCall struct {
	// ID is the unique identifier for this tool call (provider-assigned).
	ID string

	// Name is the tool/function name.
	Name string

	// Arguments is the JSON-encoded arguments string.
	Arguments string
}

// ToolDefinition describes a tool that can be offered to an LLM.
type ToolDefinition struct {
	// Name is the tool's unique identifier.
	Name string

	// Description explains what the tool does (included in LLM prompts).
	Description string

	// Parameters is the JSON Schema describing the tool's input parameters.
	Parameters map[string]any

	// EstimatedDurationMs is the declared p50 latency for budget tier assignment.
	EstimatedDurationMs int

	// MaxDurationMs is the declared p99 upper bound, used as a hard timeout.
	MaxDurationMs int

	// Idempotent indicates whether the tool can be safely retried.
	Idempotent bool

	// CacheableSeconds is how long results can be cached (0 = never).
	CacheableSeconds int
}

// ModelCapabilities describes what an LLM model supports.
type ModelCapabilities struct {
	// ContextWindow is the maximum token count for input + output.
	ContextWindow int

	// MaxOutputTokens is the maximum tokens the model can generate in one completion.
	MaxOutputTokens int

	// SupportsToolCalling indicates native function/tool calling support.
	SupportsToolCalling bool

	// SupportsVision indicates the model can process image inputs.
	SupportsVision bool

	// SupportsStreaming indicates the model supports streaming completions.
	SupportsStreaming bool
}
