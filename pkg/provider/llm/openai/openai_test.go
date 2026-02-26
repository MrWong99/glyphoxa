package openai

import (
	"testing"

	"github.com/MrWong99/glyphoxa/pkg/types"
)

// TestConvertMessage_System checks that system role is converted correctly.
func TestConvertMessage_System(t *testing.T) {
	msg := types.Message{Role: "system", Content: "You are helpful."}
	param, err := convertMessage(msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if param.OfSystem == nil {
		t.Fatal("expected OfSystem to be set")
	}
}

// TestConvertMessage_User checks that user role is converted correctly.
func TestConvertMessage_User(t *testing.T) {
	msg := types.Message{Role: "user", Content: "Hello!"}
	param, err := convertMessage(msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if param.OfUser == nil {
		t.Fatal("expected OfUser to be set")
	}
}

// TestConvertMessage_Assistant checks that assistant role is converted.
func TestConvertMessage_Assistant(t *testing.T) {
	msg := types.Message{Role: "assistant", Content: "Hi there!"}
	param, err := convertMessage(msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if param.OfAssistant == nil {
		t.Fatal("expected OfAssistant to be set")
	}
}

// TestConvertMessage_AssistantWithToolCalls checks tool call conversion.
func TestConvertMessage_AssistantWithToolCalls(t *testing.T) {
	msg := types.Message{
		Role: "assistant",
		ToolCalls: []types.ToolCall{
			{ID: "call_1", Name: "get_weather", Arguments: `{"city":"Berlin"}`},
		},
	}
	param, err := convertMessage(msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if param.OfAssistant == nil {
		t.Fatal("expected OfAssistant to be set")
	}
	if len(param.OfAssistant.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(param.OfAssistant.ToolCalls))
	}
	tc := param.OfAssistant.ToolCalls[0]
	if tc.ID != "call_1" {
		t.Errorf("expected ID call_1, got %s", tc.ID)
	}
	if tc.Function.Name != "get_weather" {
		t.Errorf("expected function name get_weather, got %s", tc.Function.Name)
	}
	if tc.Function.Arguments != `{"city":"Berlin"}` {
		t.Errorf("unexpected arguments: %s", tc.Function.Arguments)
	}
}

// TestConvertMessage_Tool checks tool response message conversion.
func TestConvertMessage_Tool(t *testing.T) {
	msg := types.Message{Role: "tool", Content: "sunny", ToolCallID: "call_1"}
	param, err := convertMessage(msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if param.OfTool == nil {
		t.Fatal("expected OfTool to be set")
	}
	if param.OfTool.ToolCallID != "call_1" {
		t.Errorf("expected ToolCallID call_1, got %s", param.OfTool.ToolCallID)
	}
}

// TestConvertMessage_UnknownRole checks that unknown roles return an error.
func TestConvertMessage_UnknownRole(t *testing.T) {
	msg := types.Message{Role: "unknown", Content: "test"}
	_, err := convertMessage(msg)
	if err == nil {
		t.Fatal("expected error for unknown role, got nil")
	}
}

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
	if caps.MaxOutputTokens <= 0 {
		t.Error("gpt-4o-mini: expected MaxOutputTokens > 0")
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

// TestModelCapabilities_GPT4 checks gpt-4 capabilities.
func TestModelCapabilities_GPT4(t *testing.T) {
	caps := modelCapabilities("gpt-4")
	if caps.ContextWindow != 8_192 {
		t.Errorf("gpt-4: expected context window 8192, got %d", caps.ContextWindow)
	}
}

// TestModelCapabilities_UnknownModel checks defaults for unrecognised models.
func TestModelCapabilities_UnknownModel(t *testing.T) {
	caps := modelCapabilities("my-custom-model")
	// Should return sensible defaults without panicking.
	if caps.ContextWindow <= 0 {
		t.Error("unknown model: expected positive ContextWindow")
	}
	if caps.MaxOutputTokens <= 0 {
		t.Error("unknown model: expected positive MaxOutputTokens")
	}
}

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

// TestNew_MissingAPIKey ensures constructor rejects an empty API key.
func TestNew_MissingAPIKey(t *testing.T) {
	_, err := New("", "gpt-4o")
	if err == nil {
		t.Fatal("expected error for empty API key")
	}
}

// TestNew_MissingModel ensures constructor rejects an empty model.
func TestNew_MissingModel(t *testing.T) {
	_, err := New("sk-test", "")
	if err == nil {
		t.Fatal("expected error for empty model")
	}
}

// TestNew_Options checks that optional settings are accepted without error.
func TestNew_Options(t *testing.T) {
	_, err := New("sk-test", "gpt-4o",
		WithBaseURL("https://custom.example.com"),
		WithOrganization("org-123"),
	)
	if err != nil {
		t.Fatalf("unexpected error with valid options: %v", err)
	}
}
