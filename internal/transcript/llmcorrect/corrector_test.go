package llmcorrect_test

import (
	"context"
	"strings"
	"testing"

	"github.com/MrWong99/glyphoxa/internal/transcript/llmcorrect"
	llm "github.com/MrWong99/glyphoxa/pkg/provider/llm"
	"github.com/MrWong99/glyphoxa/pkg/provider/llm/mock"
)

// validResponse returns a well-formed LLM JSON response correcting one word.
func validResponse(correctedText, orig, corr string, confidence float64) string {
	return `{
  "corrected_text": "` + correctedText + `",
  "corrections": [
    {"original": "` + orig + `", "corrected": "` + corr + `", "confidence": ` + floatStr(confidence) + `}
  ]
}`
}

func floatStr(f float64) string {
	// Simple representation for test literals.
	if f == 0.9 {
		return "0.9"
	}
	if f == 0.85 {
		return "0.85"
	}
	return "0.8"
}

func TestCorrector_CallsLLMWithEntityNames(t *testing.T) {
	t.Parallel()

	provider := &mock.Provider{
		CompleteResponse: &llm.CompletionResponse{
			Content: `{"corrected_text": "The wizard Eldrinax awaits.", "corrections": []}`,
		},
	}
	c := llmcorrect.New(provider)

	entities := []string{"Eldrinax", "Tower of Whispers"}
	_, _, err := c.Correct(context.Background(), "The wizard elder nacks awaits.", entities, nil)
	if err != nil {
		t.Fatalf("Correct returned error: %v", err)
	}

	if len(provider.CompleteCalls) != 1 {
		t.Fatalf("expected 1 Complete call, got %d", len(provider.CompleteCalls))
	}

	req := provider.CompleteCalls[0].Req
	// System prompt must contain each entity name.
	for _, entity := range entities {
		if !strings.Contains(req.SystemPrompt, entity) {
			t.Errorf("system prompt missing entity %q\nprompt:\n%s", entity, req.SystemPrompt)
		}
	}

	// User message must contain the original transcript text.
	if len(req.Messages) == 0 {
		t.Fatal("request has no messages")
	}
	if !strings.Contains(req.Messages[0].Content, "elder nacks") {
		t.Errorf("user message missing original text, got: %s", req.Messages[0].Content)
	}
}

func TestCorrector_ParsesJSONCorrections(t *testing.T) {
	t.Parallel()

	provider := &mock.Provider{
		CompleteResponse: &llm.CompletionResponse{
			Content: validResponse("Eldrinax guards the Tower of Whispers.", "elder nacks", "Eldrinax", 0.9),
		},
	}
	c := llmcorrect.New(provider)

	correctedText, corrections, err := c.Correct(
		context.Background(),
		"elder nacks guards the Tower of Wispers.",
		[]string{"Eldrinax", "Tower of Whispers"},
		[]string{"elder", "nacks"},
	)
	if err != nil {
		t.Fatalf("Correct returned error: %v", err)
	}

	if correctedText != "Eldrinax guards the Tower of Whispers." {
		t.Errorf("correctedText=%q, want %q", correctedText, "Eldrinax guards the Tower of Whispers.")
	}

	if len(corrections) != 1 {
		t.Fatalf("got %d corrections, want 1", len(corrections))
	}
	if corrections[0].Original != "elder nacks" {
		t.Errorf("corrections[0].Original=%q, want %q", corrections[0].Original, "elder nacks")
	}
	if corrections[0].Corrected != "Eldrinax" {
		t.Errorf("corrections[0].Corrected=%q, want %q", corrections[0].Corrected, "Eldrinax")
	}
	if corrections[0].Confidence != 0.9 {
		t.Errorf("corrections[0].Confidence=%f, want 0.9", corrections[0].Confidence)
	}
}

func TestCorrector_FallbackOnUnparseable(t *testing.T) {
	t.Parallel()

	provider := &mock.Provider{
		CompleteResponse: &llm.CompletionResponse{
			// Intentionally invalid JSON.
			Content: "I cannot correct this transcript because it's ambiguous.",
		},
	}
	c := llmcorrect.New(provider)

	originalText := "elder nacks lives in the tower of wispers."
	correctedText, corrections, err := c.Correct(
		context.Background(),
		originalText,
		[]string{"Eldrinax", "Tower of Whispers"},
		nil,
	)
	if err != nil {
		t.Fatalf("Correct returned error on unparseable response: %v", err)
	}

	// Must return original text unchanged.
	if correctedText != originalText {
		t.Errorf("correctedText=%q, want original %q", correctedText, originalText)
	}
	if corrections != nil {
		t.Errorf("corrections=%v, want nil on fallback", corrections)
	}
}

func TestCorrector_MarkdownStripping(t *testing.T) {
	t.Parallel()

	// Some models wrap JSON in markdown fences.
	provider := &mock.Provider{
		CompleteResponse: &llm.CompletionResponse{
			Content: "```json\n" + `{"corrected_text": "Eldrinax waits.", "corrections": []}` + "\n```",
		},
	}
	c := llmcorrect.New(provider)

	correctedText, _, err := c.Correct(
		context.Background(),
		"elder nacks waits.",
		[]string{"Eldrinax"},
		nil,
	)
	if err != nil {
		t.Fatalf("Correct returned error: %v", err)
	}
	if correctedText != "Eldrinax waits." {
		t.Errorf("correctedText=%q, want %q", correctedText, "Eldrinax waits.")
	}
}

func TestCorrector_EmptyEntities(t *testing.T) {
	t.Parallel()

	provider := &mock.Provider{}
	c := llmcorrect.New(provider)

	text := "some text"
	correctedText, corrections, err := c.Correct(context.Background(), text, nil, nil)
	if err != nil {
		t.Fatalf("Correct returned error: %v", err)
	}
	if correctedText != text {
		t.Errorf("correctedText=%q, want original %q when no entities", correctedText, text)
	}
	if len(corrections) != 0 {
		t.Errorf("expected no corrections when entities is nil, got %d", len(corrections))
	}
	// LLM should not be called.
	if len(provider.CompleteCalls) != 0 {
		t.Errorf("expected 0 LLM calls for empty entities, got %d", len(provider.CompleteCalls))
	}
}

func TestCorrector_LLMError(t *testing.T) {
	t.Parallel()

	provider := &mock.Provider{
		CompleteErr: context.DeadlineExceeded,
	}
	c := llmcorrect.New(provider)

	_, _, err := c.Correct(
		context.Background(),
		"some transcript",
		[]string{"Eldrinax"},
		nil,
	)
	if err == nil {
		t.Fatal("expected error from LLM failure, got nil")
	}
}

func TestCorrector_WithModel(t *testing.T) {
	t.Parallel()

	provider := &mock.Provider{
		CompleteResponse: &llm.CompletionResponse{
			Content: `{"corrected_text": "hello", "corrections": []}`,
		},
	}
	c := llmcorrect.New(provider, llmcorrect.WithModel("gpt-4o-mini"))

	_, _, err := c.Correct(context.Background(), "hello", []string{"Eldrinax"}, nil)
	if err != nil {
		t.Fatalf("Correct returned error: %v", err)
	}

	if len(provider.CompleteCalls) == 0 {
		t.Fatal("no Complete calls recorded")
	}
	req := provider.CompleteCalls[0].Req
	// Model directive should appear in the system prompt.
	if !strings.Contains(req.SystemPrompt, "gpt-4o-mini") {
		t.Errorf("system prompt does not contain model directive; prompt:\n%s", req.SystemPrompt)
	}
}

func TestCorrector_WithTemperature(t *testing.T) {
	t.Parallel()

	provider := &mock.Provider{
		CompleteResponse: &llm.CompletionResponse{
			Content: `{"corrected_text": "hello", "corrections": []}`,
		},
	}
	c := llmcorrect.New(provider, llmcorrect.WithTemperature(0.5))

	_, _, err := c.Correct(context.Background(), "hello", []string{"Eldrinax"}, nil)
	if err != nil {
		t.Fatalf("Correct returned error: %v", err)
	}

	if len(provider.CompleteCalls) == 0 {
		t.Fatal("no Complete calls recorded")
	}
	req := provider.CompleteCalls[0].Req
	if req.Temperature != 0.5 {
		t.Errorf("Temperature=%f, want 0.5", req.Temperature)
	}
}

func TestCorrector_LowConfidenceSpansInUserMessage(t *testing.T) {
	t.Parallel()

	provider := &mock.Provider{
		CompleteResponse: &llm.CompletionResponse{
			Content: `{"corrected_text": "Eldrinax speaks.", "corrections": []}`,
		},
	}
	c := llmcorrect.New(provider)

	spans := []string{"elder", "nacks"}
	_, _, err := c.Correct(
		context.Background(),
		"elder nacks speaks.",
		[]string{"Eldrinax"},
		spans,
	)
	if err != nil {
		t.Fatalf("Correct returned error: %v", err)
	}

	if len(provider.CompleteCalls) == 0 {
		t.Fatal("no Complete calls recorded")
	}
	userMsg := provider.CompleteCalls[0].Req.Messages[0].Content
	for _, span := range spans {
		if !strings.Contains(userMsg, span) {
			t.Errorf("user message missing low-confidence span %q; got:\n%s", span, userMsg)
		}
	}
}
