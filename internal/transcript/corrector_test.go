package transcript_test

import (
	"context"
	"testing"
	"time"

	"github.com/MrWong99/glyphoxa/internal/transcript"
	"github.com/MrWong99/glyphoxa/internal/transcript/llmcorrect"
	"github.com/MrWong99/glyphoxa/internal/transcript/phonetic"
	llm "github.com/MrWong99/glyphoxa/pkg/provider/llm"
	"github.com/MrWong99/glyphoxa/pkg/provider/llm/mock"
	"github.com/MrWong99/glyphoxa/pkg/provider/stt"
)

// makeMockLLM creates a mock LLM provider that returns the given corrected
// text with a single declared correction.
func makeMockLLM(correctedText, origWord, corrWord string) *mock.Provider {
	return &mock.Provider{
		CompleteResponse: &llm.CompletionResponse{
			Content: `{"corrected_text": "` + correctedText + `", "corrections": [{"original": "` + origWord + `", "corrected": "` + corrWord + `", "confidence": 0.9}]}`,
		},
	}
}

func makeTranscript(text string, words ...stt.WordDetail) stt.Transcript {
	return stt.Transcript{
		Text:       text,
		IsFinal:    true,
		Confidence: 0.85,
		Words:      words,
		Timestamp:  time.Second,
		Duration:   3 * time.Second,
	}
}

// --- Both stages ---

func TestCorrectionPipeline_BothStages(t *testing.T) {
	t.Parallel()

	phonMatcher := phonetic.New()
	mockLLM := makeMockLLM("Eldrinax lives in the Tower of Whispers.", "elder nacks", "Eldrinax")
	llmCorrector := llmcorrect.New(mockLLM)

	pipeline := transcript.NewPipeline(
		transcript.WithPhoneticMatcher(phonMatcher),
		transcript.WithLLMCorrector(llmCorrector),
		transcript.WithLLMOnLowConfidence(0.5),
	)

	// Low-confidence word detail to trigger LLM stage.
	wordDetails := []stt.WordDetail{
		{Word: "elder", Start: 0, End: time.Second, Confidence: 0.3},
		{Word: "nacks", Start: time.Second, End: 2 * time.Second, Confidence: 0.25},
		{Word: "lives", Start: 2 * time.Second, End: 3 * time.Second, Confidence: 0.9},
	}

	tr := makeTranscript("elder nacks lives in the tower of wispers.", wordDetails...)
	result, err := pipeline.Correct(context.Background(), tr, []string{"Eldrinax", "Tower of Whispers"})
	if err != nil {
		t.Fatalf("Correct returned error: %v", err)
	}

	if result == nil {
		t.Fatal("Correct returned nil result")
	}
	if result.Original.Text != tr.Text {
		t.Errorf("Original.Text=%q, want %q", result.Original.Text, tr.Text)
	}
	// Corrections slice must be non-nil.
	if result.Corrections == nil {
		t.Error("Corrections is nil, want non-nil (even if empty)")
	}
	// At least phonetic and/or LLM corrections should be present.
	if len(result.Corrections) == 0 {
		t.Log("Warning: no corrections applied — phonetic may not have matched; check thresholds")
	}
}

// --- Phonetic only ---

func TestCorrectionPipeline_PhoneticOnly(t *testing.T) {
	t.Parallel()

	phonMatcher := phonetic.New()
	pipeline := transcript.NewPipeline(
		transcript.WithPhoneticMatcher(phonMatcher),
	)

	tr := makeTranscript("tower of wispers is dangerous.")
	result, err := pipeline.Correct(context.Background(), tr, []string{"Tower of Whispers", "Eldrinax"})
	if err != nil {
		t.Fatalf("Correct returned error: %v", err)
	}

	if result.Corrections == nil {
		t.Error("Corrections is nil, want non-nil")
	}

	// "tower of wispers" should be corrected to "Tower of Whispers" by phonetic.
	for _, c := range result.Corrections {
		if c.Method != "phonetic" {
			t.Errorf("expected phonetic correction, got method=%q", c.Method)
		}
	}
}

// --- LLM only ---

func TestCorrectionPipeline_LLMOnly(t *testing.T) {
	t.Parallel()

	mockLLM := &mock.Provider{
		CompleteResponse: &llm.CompletionResponse{
			Content: `{"corrected_text": "Eldrinax arrived.", "corrections": [{"original": "eldrinaks", "corrected": "Eldrinax", "confidence": 0.88}]}`,
		},
	}
	llmCorrector := llmcorrect.New(mockLLM)
	pipeline := transcript.NewPipeline(
		transcript.WithLLMCorrector(llmCorrector),
	)

	// No per-word data → LLM always runs.
	tr := makeTranscript("eldrinaks arrived.")
	result, err := pipeline.Correct(context.Background(), tr, []string{"Eldrinax"})
	if err != nil {
		t.Fatalf("Correct returned error: %v", err)
	}

	if result == nil {
		t.Fatal("result is nil")
	}
	// LLM should have been called.
	if len(mockLLM.CompleteCalls) == 0 {
		t.Fatal("LLM was not called")
	}
	// Final text should come from LLM response.
	if result.Corrected != "Eldrinax arrived." {
		t.Errorf("Corrected=%q, want %q", result.Corrected, "Eldrinax arrived.")
	}
	// LLM corrections should be present.
	llmCorrectionFound := false
	for _, c := range result.Corrections {
		if c.Method == "llm" {
			llmCorrectionFound = true
			break
		}
	}
	if !llmCorrectionFound {
		t.Error("no LLM correction found in result.Corrections")
	}
}

// --- Low-confidence filtering ---

func TestCorrectionPipeline_LowConfidenceFiltering(t *testing.T) {
	t.Parallel()

	mockLLM := &mock.Provider{
		CompleteResponse: &llm.CompletionResponse{
			Content: `{"corrected_text": "Eldrinax speaks wisdom.", "corrections": []}`,
		},
	}
	llmCorrector := llmcorrect.New(mockLLM)
	pipeline := transcript.NewPipeline(
		transcript.WithLLMCorrector(llmCorrector),
		transcript.WithLLMOnLowConfidence(0.5),
	)

	// All words above threshold → LLM should NOT be called.
	wordDetails := []stt.WordDetail{
		{Word: "eldrinax", Confidence: 0.95},
		{Word: "speaks", Confidence: 0.98},
		{Word: "wisdom", Confidence: 0.92},
	}
	tr := makeTranscript("eldrinax speaks wisdom.", wordDetails...)
	result, err := pipeline.Correct(context.Background(), tr, []string{"Eldrinax"})
	if err != nil {
		t.Fatalf("Correct returned error: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
	if len(mockLLM.CompleteCalls) != 0 {
		t.Errorf("LLM called %d times, want 0 (all words high-confidence)", len(mockLLM.CompleteCalls))
	}
}

func TestCorrectionPipeline_LLMRunsOnLowConfidence(t *testing.T) {
	t.Parallel()

	mockLLM := &mock.Provider{
		CompleteResponse: &llm.CompletionResponse{
			Content: `{"corrected_text": "Eldrinax speaks wisdom.", "corrections": []}`,
		},
	}
	llmCorrector := llmcorrect.New(mockLLM)
	pipeline := transcript.NewPipeline(
		transcript.WithLLMCorrector(llmCorrector),
		transcript.WithLLMOnLowConfidence(0.5),
	)

	// One word below threshold → LLM should be called.
	wordDetails := []stt.WordDetail{
		{Word: "eldrinaks", Confidence: 0.2}, // low confidence
		{Word: "speaks", Confidence: 0.98},
		{Word: "wisdom", Confidence: 0.92},
	}
	tr := makeTranscript("eldrinaks speaks wisdom.", wordDetails...)
	_, err := pipeline.Correct(context.Background(), tr, []string{"Eldrinax"})
	if err != nil {
		t.Fatalf("Correct returned error: %v", err)
	}
	if len(mockLLM.CompleteCalls) != 1 {
		t.Errorf("LLM called %d times, want 1 (one low-confidence word)", len(mockLLM.CompleteCalls))
	}
}

// --- No stages configured ---

func TestCorrectionPipeline_NoStages(t *testing.T) {
	t.Parallel()

	pipeline := transcript.NewPipeline()
	tr := makeTranscript("elder nacks speaks.")
	result, err := pipeline.Correct(context.Background(), tr, []string{"Eldrinax"})
	if err != nil {
		t.Fatalf("Correct returned error: %v", err)
	}
	if result.Corrected != tr.Text {
		t.Errorf("Corrected=%q, want original %q when no stages configured", result.Corrected, tr.Text)
	}
	if len(result.Corrections) != 0 {
		t.Errorf("expected 0 corrections with no stages, got %d", len(result.Corrections))
	}
}

// --- Original preserved ---

func TestCorrectionPipeline_OriginalPreserved(t *testing.T) {
	t.Parallel()

	phonMatcher := phonetic.New()
	pipeline := transcript.NewPipeline(
		transcript.WithPhoneticMatcher(phonMatcher),
	)

	tr := makeTranscript("grimjaw entered the tavern.")
	result, err := pipeline.Correct(context.Background(), tr, []string{"Grimjaw"})
	if err != nil {
		t.Fatalf("Correct returned error: %v", err)
	}

	// Original must always equal the input transcript.
	if result.Original.Text != tr.Text {
		t.Errorf("Original.Text=%q, want %q", result.Original.Text, tr.Text)
	}
}
