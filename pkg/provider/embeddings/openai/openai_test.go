package openai

import (
	"testing"
)

// TestModelDimensions_TextEmbedding3Small verifies 1536 dims for 3-small.
func TestModelDimensions_TextEmbedding3Small(t *testing.T) {
	d := modelDimensions("text-embedding-3-small")
	if d != 1536 {
		t.Errorf("text-embedding-3-small: expected 1536 dimensions, got %d", d)
	}
}

// TestModelDimensions_TextEmbedding3Large verifies 3072 dims for 3-large.
func TestModelDimensions_TextEmbedding3Large(t *testing.T) {
	d := modelDimensions("text-embedding-3-large")
	if d != 3072 {
		t.Errorf("text-embedding-3-large: expected 3072 dimensions, got %d", d)
	}
}

// TestModelDimensions_Ada002 verifies 1536 dims for ada-002.
func TestModelDimensions_Ada002(t *testing.T) {
	d := modelDimensions("text-embedding-ada-002")
	if d != 1536 {
		t.Errorf("text-embedding-ada-002: expected 1536 dimensions, got %d", d)
	}
}

// TestModelDimensions_Unknown verifies that unknown models return a positive default.
func TestModelDimensions_Unknown(t *testing.T) {
	d := modelDimensions("some-future-model")
	if d <= 0 {
		t.Errorf("unknown model: expected positive dimensions, got %d", d)
	}
}

// TestDimensions_MethodMatchesHelper verifies Provider.Dimensions() matches modelDimensions().
func TestDimensions_MethodMatchesHelper(t *testing.T) {
	cases := []string{
		"text-embedding-3-small",
		"text-embedding-3-large",
		"text-embedding-ada-002",
	}
	for _, model := range cases {
		p := &Provider{model: model}
		if got := p.Dimensions(); got != modelDimensions(model) {
			t.Errorf("model %s: Dimensions() = %d, want %d", model, got, modelDimensions(model))
		}
	}
}

// TestModelID verifies that ModelID returns the model string as-is.
func TestModelID(t *testing.T) {
	cases := []string{
		"text-embedding-3-small",
		"text-embedding-3-large",
		"text-embedding-ada-002",
		"my-custom-embeddings-model",
	}
	for _, model := range cases {
		p := &Provider{model: model}
		if got := p.ModelID(); got != model {
			t.Errorf("ModelID() = %q, want %q", got, model)
		}
	}
}

// TestNew_DefaultModel verifies that an empty model string defaults to text-embedding-3-small.
func TestNew_DefaultModel(t *testing.T) {
	p, err := New("sk-test", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.ModelID() != DefaultModel {
		t.Errorf("expected default model %s, got %s", DefaultModel, p.ModelID())
	}
}

// TestNew_MissingAPIKey checks that an empty API key is rejected.
func TestNew_MissingAPIKey(t *testing.T) {
	_, err := New("", "text-embedding-3-small")
	if err == nil {
		t.Fatal("expected error for empty API key")
	}
}

// TestNew_Options verifies that options are accepted without error.
func TestNew_Options(t *testing.T) {
	_, err := New("sk-test", "text-embedding-3-small",
		WithBaseURL("https://custom.example.com"),
		WithOrganization("org-123"),
	)
	if err != nil {
		t.Fatalf("unexpected error with valid options: %v", err)
	}
}

// TestFloat64ToFloat32 verifies the conversion helper.
func TestFloat64ToFloat32(t *testing.T) {
	in := []float64{1.0, 2.5, -0.5}
	out := float64ToFloat32(in)
	if len(out) != len(in) {
		t.Fatalf("expected %d elements, got %d", len(in), len(out))
	}
	for i, v := range out {
		expected := float32(in[i])
		if v != expected {
			t.Errorf("index %d: expected %v, got %v", i, expected, v)
		}
	}
}
