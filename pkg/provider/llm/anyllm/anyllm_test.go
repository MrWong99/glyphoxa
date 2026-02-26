package anyllm

import (
	"testing"

	anyllmlib "github.com/mozilla-ai/any-llm-go"

	"github.com/MrWong99/glyphoxa/pkg/types"
)

// ── convertMessage ────────────────────────────────────────────────────────────

// TestConvertMessage_System checks that system-role messages are converted correctly.
func TestConvertMessage_System(t *testing.T) {
	m := types.Message{Role: "system", Content: "You are helpful."}
	got := convertMessage(m)
	if got.Role != "system" {
		t.Errorf("expected role system, got %q", got.Role)
	}
	if got.ContentString() != "You are helpful." {
		t.Errorf("expected content %q, got %q", "You are helpful.", got.ContentString())
	}
}

// TestConvertMessage_User checks that user-role messages are converted correctly.
func TestConvertMessage_User(t *testing.T) {
	m := types.Message{Role: "user", Content: "Hello!"}
	got := convertMessage(m)
	if got.Role != "user" {
		t.Errorf("expected role user, got %q", got.Role)
	}
	if got.ContentString() != "Hello!" {
		t.Errorf("expected content %q, got %q", "Hello!", got.ContentString())
	}
}

// TestConvertMessage_Assistant checks that assistant-role messages are converted correctly.
func TestConvertMessage_Assistant(t *testing.T) {
	m := types.Message{Role: "assistant", Content: "Hi there!"}
	got := convertMessage(m)
	if got.Role != "assistant" {
		t.Errorf("expected role assistant, got %q", got.Role)
	}
	if got.ContentString() != "Hi there!" {
		t.Errorf("expected content %q, got %q", "Hi there!", got.ContentString())
	}
}

// TestConvertMessage_AssistantWithToolCalls checks tool call conversion.
func TestConvertMessage_AssistantWithToolCalls(t *testing.T) {
	m := types.Message{
		Role: "assistant",
		ToolCalls: []types.ToolCall{
			{ID: "call_1", Name: "get_weather", Arguments: `{"city":"Berlin"}`},
		},
	}
	got := convertMessage(m)
	if got.Role != "assistant" {
		t.Errorf("expected role assistant, got %q", got.Role)
	}
	if len(got.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(got.ToolCalls))
	}
	tc := got.ToolCalls[0]
	if tc.ID != "call_1" {
		t.Errorf("expected ID call_1, got %q", tc.ID)
	}
	if tc.Function.Name != "get_weather" {
		t.Errorf("expected function name get_weather, got %q", tc.Function.Name)
	}
	if tc.Function.Arguments != `{"city":"Berlin"}` {
		t.Errorf("unexpected arguments: %q", tc.Function.Arguments)
	}
	if tc.Type != "function" {
		t.Errorf("expected type function, got %q", tc.Type)
	}
}

// TestConvertMessage_Tool checks tool-result message conversion.
func TestConvertMessage_Tool(t *testing.T) {
	m := types.Message{Role: "tool", Content: "sunny", ToolCallID: "call_1"}
	got := convertMessage(m)
	if got.Role != "tool" {
		t.Errorf("expected role tool, got %q", got.Role)
	}
	if got.ToolCallID != "call_1" {
		t.Errorf("expected ToolCallID call_1, got %q", got.ToolCallID)
	}
	if got.ContentString() != "sunny" {
		t.Errorf("expected content sunny, got %q", got.ContentString())
	}
}

// TestConvertMessage_WithName checks that the Name field is preserved.
func TestConvertMessage_WithName(t *testing.T) {
	m := types.Message{Role: "user", Content: "Hi", Name: "alice"}
	got := convertMessage(m)
	if got.Name != "alice" {
		t.Errorf("expected name alice, got %q", got.Name)
	}
}

// TestConvertMessage_EmptyToolCalls checks that zero tool calls yield no ToolCalls slice.
func TestConvertMessage_EmptyToolCalls(t *testing.T) {
	m := types.Message{Role: "assistant", Content: "No tools here."}
	got := convertMessage(m)
	if len(got.ToolCalls) != 0 {
		t.Errorf("expected no tool calls, got %d", len(got.ToolCalls))
	}
}

// ── modelCapabilities ─────────────────────────────────────────────────────────

// TestModelCapabilities_GPT4oMini checks gpt-4o-mini capabilities.
func TestModelCapabilities_GPT4oMini(t *testing.T) {
	caps := modelCapabilities("gpt-4o-mini")
	if caps.ContextWindow != 128_000 {
		t.Errorf("gpt-4o-mini: expected context window 128000, got %d", caps.ContextWindow)
	}
	if !caps.SupportsToolCalling {
		t.Error("gpt-4o-mini: expected SupportsToolCalling=true")
	}
	if !caps.SupportsVision {
		t.Error("gpt-4o-mini: expected SupportsVision=true")
	}
	if !caps.SupportsStreaming {
		t.Error("gpt-4o-mini: expected SupportsStreaming=true")
	}
	if caps.MaxOutputTokens != 16_384 {
		t.Errorf("gpt-4o-mini: expected MaxOutputTokens 16384, got %d", caps.MaxOutputTokens)
	}
}

// TestModelCapabilities_GPT4o checks gpt-4o capabilities.
func TestModelCapabilities_GPT4o(t *testing.T) {
	caps := modelCapabilities("gpt-4o")
	if caps.ContextWindow != 128_000 {
		t.Errorf("gpt-4o: expected context window 128000, got %d", caps.ContextWindow)
	}
	if !caps.SupportsToolCalling {
		t.Error("gpt-4o: expected SupportsToolCalling=true")
	}
	if !caps.SupportsVision {
		t.Error("gpt-4o: expected SupportsVision=true")
	}
}

// TestModelCapabilities_GPT4Turbo checks gpt-4-turbo capabilities.
func TestModelCapabilities_GPT4Turbo(t *testing.T) {
	caps := modelCapabilities("gpt-4-turbo")
	if caps.ContextWindow != 128_000 {
		t.Errorf("gpt-4-turbo: expected context window 128000, got %d", caps.ContextWindow)
	}
	if !caps.SupportsVision {
		t.Error("gpt-4-turbo: expected SupportsVision=true")
	}
}

// TestModelCapabilities_GPT4 checks gpt-4 capabilities.
func TestModelCapabilities_GPT4(t *testing.T) {
	caps := modelCapabilities("gpt-4")
	if caps.ContextWindow != 8_192 {
		t.Errorf("gpt-4: expected context window 8192, got %d", caps.ContextWindow)
	}
	if caps.SupportsVision {
		t.Error("gpt-4: expected SupportsVision=false")
	}
}

// TestModelCapabilities_GPT35Turbo checks gpt-3.5-turbo capabilities.
func TestModelCapabilities_GPT35Turbo(t *testing.T) {
	caps := modelCapabilities("gpt-3.5-turbo")
	if caps.ContextWindow != 16_385 {
		t.Errorf("gpt-3.5-turbo: expected context window 16385, got %d", caps.ContextWindow)
	}
	if caps.SupportsVision {
		t.Error("gpt-3.5-turbo: expected SupportsVision=false")
	}
}

// TestModelCapabilities_O1Mini checks o1-mini capabilities.
func TestModelCapabilities_O1Mini(t *testing.T) {
	caps := modelCapabilities("o1-mini")
	if caps.ContextWindow != 128_000 {
		t.Errorf("o1-mini: expected context window 128000, got %d", caps.ContextWindow)
	}
	if caps.SupportsToolCalling {
		t.Error("o1-mini: expected SupportsToolCalling=false")
	}
}

// TestModelCapabilities_O1 checks o1 capabilities.
func TestModelCapabilities_O1(t *testing.T) {
	caps := modelCapabilities("o1")
	if caps.ContextWindow != 200_000 {
		t.Errorf("o1: expected context window 200000, got %d", caps.ContextWindow)
	}
	if !caps.SupportsToolCalling {
		t.Error("o1: expected SupportsToolCalling=true")
	}
	if !caps.SupportsVision {
		t.Error("o1: expected SupportsVision=true")
	}
}

// TestModelCapabilities_Claude35Sonnet checks claude-3-5-sonnet capabilities.
func TestModelCapabilities_Claude35Sonnet(t *testing.T) {
	caps := modelCapabilities("claude-3-5-sonnet-latest")
	if caps.ContextWindow != 200_000 {
		t.Errorf("claude-3-5-sonnet: expected context window 200000, got %d", caps.ContextWindow)
	}
	if !caps.SupportsToolCalling {
		t.Error("claude-3-5-sonnet: expected SupportsToolCalling=true")
	}
	if !caps.SupportsVision {
		t.Error("claude-3-5-sonnet: expected SupportsVision=true")
	}
	if caps.MaxOutputTokens != 8_192 {
		t.Errorf("claude-3-5-sonnet: expected MaxOutputTokens 8192, got %d", caps.MaxOutputTokens)
	}
}

// TestModelCapabilities_ClaudeHaiku checks claude haiku capabilities.
func TestModelCapabilities_ClaudeHaiku(t *testing.T) {
	caps := modelCapabilities("claude-3-haiku-20240307")
	if caps.ContextWindow != 200_000 {
		t.Errorf("claude-3-haiku: expected context window 200000, got %d", caps.ContextWindow)
	}
	if !caps.SupportsVision {
		t.Error("claude-3-haiku: expected SupportsVision=true")
	}
	if !caps.SupportsToolCalling {
		t.Error("claude-3-haiku: expected SupportsToolCalling=true")
	}
}

// TestModelCapabilities_ClaudeOpus checks claude-3-opus capabilities.
func TestModelCapabilities_ClaudeOpus(t *testing.T) {
	caps := modelCapabilities("claude-3-opus-20240229")
	if caps.ContextWindow != 200_000 {
		t.Errorf("claude-3-opus: expected context window 200000, got %d", caps.ContextWindow)
	}
	if caps.MaxOutputTokens != 4_096 {
		t.Errorf("claude-3-opus: expected MaxOutputTokens 4096, got %d", caps.MaxOutputTokens)
	}
}

// TestModelCapabilities_ClaudeGeneric catches generic claude models.
func TestModelCapabilities_ClaudeGeneric(t *testing.T) {
	caps := modelCapabilities("claude-future-model")
	if caps.ContextWindow != 200_000 {
		t.Errorf("claude-generic: expected context window 200000, got %d", caps.ContextWindow)
	}
	if !caps.SupportsVision {
		t.Error("claude-generic: expected SupportsVision=true")
	}
}

// TestModelCapabilities_Gemini20Flash checks gemini-2.0-flash capabilities.
func TestModelCapabilities_Gemini20Flash(t *testing.T) {
	caps := modelCapabilities("gemini-2.0-flash")
	if caps.ContextWindow != 1_048_576 {
		t.Errorf("gemini-2.0-flash: expected context window 1048576, got %d", caps.ContextWindow)
	}
	if !caps.SupportsVision {
		t.Error("gemini-2.0-flash: expected SupportsVision=true")
	}
	if !caps.SupportsToolCalling {
		t.Error("gemini-2.0-flash: expected SupportsToolCalling=true")
	}
}

// TestModelCapabilities_Gemini15Pro checks gemini-1.5-pro capabilities.
func TestModelCapabilities_Gemini15Pro(t *testing.T) {
	caps := modelCapabilities("gemini-1.5-pro")
	if caps.ContextWindow != 2_097_152 {
		t.Errorf("gemini-1.5-pro: expected context window 2097152, got %d", caps.ContextWindow)
	}
}

// TestModelCapabilities_Gemini15Flash checks gemini-1.5-flash capabilities.
func TestModelCapabilities_Gemini15Flash(t *testing.T) {
	caps := modelCapabilities("gemini-1.5-flash")
	if caps.ContextWindow != 1_048_576 {
		t.Errorf("gemini-1.5-flash: expected context window 1048576, got %d", caps.ContextWindow)
	}
}

// TestModelCapabilities_GeminiGeneric catches generic Gemini models.
func TestModelCapabilities_GeminiGeneric(t *testing.T) {
	caps := modelCapabilities("gemini-pro")
	if caps.ContextWindow != 128_000 {
		t.Errorf("gemini-pro: expected context window 128000, got %d", caps.ContextWindow)
	}
}

// TestModelCapabilities_Unknown checks that unknown models return safe defaults.
func TestModelCapabilities_Unknown(t *testing.T) {
	caps := modelCapabilities("my-custom-model")
	if caps.ContextWindow <= 0 {
		t.Error("unknown model: expected positive ContextWindow")
	}
	if caps.MaxOutputTokens <= 0 {
		t.Error("unknown model: expected positive MaxOutputTokens")
	}
	if !caps.SupportsStreaming {
		t.Error("unknown model: expected SupportsStreaming=true")
	}
}

// TestModelCapabilities_CaseInsensitive checks that model name matching is case-insensitive.
func TestModelCapabilities_CaseInsensitive(t *testing.T) {
	lower := modelCapabilities("gpt-4o")
	upper := modelCapabilities("GPT-4O")
	if lower.ContextWindow != upper.ContextWindow {
		t.Errorf("case should not matter: got %d vs %d", lower.ContextWindow, upper.ContextWindow)
	}
}

// ── Constructor ───────────────────────────────────────────────────────────────

// TestNew_EmptyProviderName checks that an empty provider name returns an error.
func TestNew_EmptyProviderName(t *testing.T) {
	_, err := New("", "gpt-4o")
	if err == nil {
		t.Fatal("expected error for empty providerName")
	}
}

// TestNew_EmptyModel checks that an empty model name returns an error.
func TestNew_EmptyModel(t *testing.T) {
	_, err := New("openai", "")
	if err == nil {
		t.Fatal("expected error for empty model")
	}
}

// TestNew_UnsupportedProvider checks that an unsupported provider returns an error.
func TestNew_UnsupportedProvider(t *testing.T) {
	_, err := New("fakecloud", "some-model", anyllmlib.WithAPIKey("dummy"))
	if err == nil {
		t.Fatal("expected error for unsupported provider")
	}
}

// TestNew_OpenAI_WithAPIKey checks that OpenAI provider constructs successfully with an API key.
func TestNew_OpenAI_WithAPIKey(t *testing.T) {
	p, err := New("openai", "gpt-4o", anyllmlib.WithAPIKey("sk-test"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
	if p.model != "gpt-4o" {
		t.Errorf("expected model gpt-4o, got %q", p.model)
	}
}

// TestNew_OpenAI_MissingAPIKey checks that OpenAI returns an error when no API key is available.
// This relies on OPENAI_API_KEY not being set in the test environment.
func TestNew_OpenAI_MissingAPIKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "") // Ensure env var is clear.
	_, err := New("openai", "gpt-4o")
	if err == nil {
		t.Fatal("expected error for missing API key")
	}
}

// TestNew_Anthropic_WithAPIKey checks that Anthropic provider constructs successfully.
func TestNew_Anthropic_WithAPIKey(t *testing.T) {
	p, err := NewAnthropic("claude-3-5-sonnet-latest", anyllmlib.WithAPIKey("sk-ant-test"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
}

// TestNew_Ollama_NoAPIKey checks that Ollama works without an API key.
func TestNew_Ollama_NoAPIKey(t *testing.T) {
	p, err := NewOllama("llama3")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
}

// TestConvenienceConstructors checks all convenience constructors delegate correctly.
func TestConvenienceConstructors(t *testing.T) {
	tests := []struct {
		name string
		fn   func() (*Provider, error)
	}{
		{"NewOpenAI", func() (*Provider, error) { return NewOpenAI("gpt-4o", anyllmlib.WithAPIKey("sk-test")) }},
		{"NewAnthropic", func() (*Provider, error) {
			return NewAnthropic("claude-3-5-sonnet-latest", anyllmlib.WithAPIKey("sk-ant-test"))
		}},
		{"NewOllama", func() (*Provider, error) { return NewOllama("llama3") }},
		{"NewLlamaCpp", func() (*Provider, error) { return NewLlamaCpp("llama3") }},
		{"NewLlamaFile", func() (*Provider, error) { return NewLlamaFile("llama3") }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := tt.fn()
			if err != nil {
				t.Fatalf("%s: unexpected error: %v", tt.name, err)
			}
			if p == nil {
				t.Fatalf("%s: expected non-nil provider", tt.name)
			}
		})
	}
}

// ── CountTokens ───────────────────────────────────────────────────────────────

// TestCountTokens_Estimation checks that token counting returns a reasonable value.
func TestCountTokens_Estimation(t *testing.T) {
	p := &Provider{model: "gpt-4o"}
	msgs := []types.Message{
		{Role: "user", Content: "Hello world"}, // 11 chars → ~3 tokens + 4 overhead = 7
	}
	count, err := p.CountTokens(msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count <= 0 {
		t.Errorf("expected positive token count, got %d", count)
	}
}

// TestCountTokens_Empty checks that an empty message list returns zero tokens.
func TestCountTokens_Empty(t *testing.T) {
	p := &Provider{model: "gpt-4o"}
	count, err := p.CountTokens(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 tokens for empty messages, got %d", count)
	}
}

// TestCountTokens_MultipleMessages checks that multiple messages accumulate correctly.
func TestCountTokens_MultipleMessages(t *testing.T) {
	p := &Provider{model: "gpt-4o"}
	msgs := []types.Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there, how can I help?"},
	}
	count, err := p.CountTokens(msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	singleCount, _ := p.CountTokens(msgs[:1])
	if count <= singleCount {
		t.Errorf("expected more tokens for two messages than one: %d <= %d", count, singleCount)
	}
}

// ── Capabilities ──────────────────────────────────────────────────────────────

// TestCapabilities_ReturnsForModel checks that Capabilities() delegates to modelCapabilities.
func TestCapabilities_ReturnsForModel(t *testing.T) {
	p := &Provider{model: "gpt-4o"}
	caps := p.Capabilities()
	expected := modelCapabilities("gpt-4o")
	if caps.ContextWindow != expected.ContextWindow {
		t.Errorf("expected ContextWindow %d, got %d", expected.ContextWindow, caps.ContextWindow)
	}
	if caps.SupportsVision != expected.SupportsVision {
		t.Errorf("expected SupportsVision %v, got %v", expected.SupportsVision, caps.SupportsVision)
	}
}
