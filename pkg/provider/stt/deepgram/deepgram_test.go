package deepgram

import (
	"net/url"
	"testing"
	"time"

	"github.com/MrWong99/glyphoxa/pkg/provider/stt"
	"github.com/MrWong99/glyphoxa/pkg/types"
)

// ---- URL / query-param tests ----

func TestBuildURL_Defaults(t *testing.T) {
	p, err := New("test-key")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cfg := stt.StreamConfig{
		SampleRate: 16000,
		Channels:   1,
		Language:   "en",
	}

	rawURL, err := p.buildURL(cfg)
	if err != nil {
		t.Fatalf("buildURL: %v", err)
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	q := u.Query()

	assertEqual(t, "model", "nova-3", q.Get("model"))
	assertEqual(t, "language", "en", q.Get("language"))
	assertEqual(t, "punctuate", "true", q.Get("punctuate"))
	assertEqual(t, "interim_results", "true", q.Get("interim_results"))
	assertEqual(t, "sample_rate", "16000", q.Get("sample_rate"))
	assertEqual(t, "channels", "1", q.Get("channels"))
}

func TestBuildURL_CustomModel(t *testing.T) {
	p, err := New("key", WithModel("base"), WithLanguage("de-DE"), WithSampleRate(48000))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rawURL, err := p.buildURL(stt.StreamConfig{})
	if err != nil {
		t.Fatalf("buildURL: %v", err)
	}

	u, _ := url.Parse(rawURL)
	q := u.Query()

	assertEqual(t, "model", "base", q.Get("model"))
	assertEqual(t, "language", "de-DE", q.Get("language"))
	assertEqual(t, "sample_rate", "48000", q.Get("sample_rate"))
}

func TestBuildURL_LanguageOverridenByCfg(t *testing.T) {
	// cfg.Language should take precedence over the provider-level default.
	p, err := New("key", WithLanguage("en"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rawURL, err := p.buildURL(stt.StreamConfig{Language: "fr-FR", SampleRate: 16000})
	if err != nil {
		t.Fatalf("buildURL: %v", err)
	}

	u, _ := url.Parse(rawURL)
	assertEqual(t, "language", "fr-FR", u.Query().Get("language"))
}

func TestBuildURL_Keywords(t *testing.T) {
	p, err := New("key")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cfg := stt.StreamConfig{
		SampleRate: 16000,
		Keywords: []types.KeywordBoost{
			{Keyword: "Eldrinax", Boost: 5},
			{Keyword: "Zorrath", Boost: 3.5},
		},
	}

	rawURL, err := p.buildURL(cfg)
	if err != nil {
		t.Fatalf("buildURL: %v", err)
	}

	u, _ := url.Parse(rawURL)
	kws := u.Query()["keywords"]
	if len(kws) != 2 {
		t.Fatalf("expected 2 keywords, got %d: %v", len(kws), kws)
	}

	// Both keywords should be present (order may vary).
	found := map[string]bool{}
	for _, kw := range kws {
		found[kw] = true
	}
	if !found["Eldrinax:5"] {
		t.Errorf("expected keyword 'Eldrinax:5', got %v", kws)
	}
	if !found["Zorrath:3.5"] {
		t.Errorf("expected keyword 'Zorrath:3.5', got %v", kws)
	}
}

func TestBuildURL_NoKeywords(t *testing.T) {
	p, err := New("key")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rawURL, err := p.buildURL(stt.StreamConfig{SampleRate: 16000})
	if err != nil {
		t.Fatalf("buildURL: %v", err)
	}

	u, _ := url.Parse(rawURL)
	if _, ok := u.Query()["keywords"]; ok {
		t.Error("expected no 'keywords' param when none provided")
	}
}

// ---- JSON parsing tests ----

func TestParseDeepgramResponse_Final(t *testing.T) {
	raw := []byte(`{
		"type": "Results",
		"is_final": true,
		"channel": {
			"alternatives": [{
				"transcript": "Hello world",
				"confidence": 0.95,
				"words": [
					{"word": "Hello", "start": 0.1, "end": 0.5, "confidence": 0.97},
					{"word": "world", "start": 0.6, "end": 1.0, "confidence": 0.93}
				]
			}]
		}
	}`)

	tr, ok := parseDeepgramResponse(raw)
	if !ok {
		t.Fatal("expected ok=true for valid Results message")
	}

	if !tr.IsFinal {
		t.Error("expected IsFinal=true")
	}
	assertEqual(t, "text", "Hello world", tr.Text)
	if tr.Confidence != 0.95 {
		t.Errorf("expected confidence 0.95, got %f", tr.Confidence)
	}
	if len(tr.Words) != 2 {
		t.Fatalf("expected 2 words, got %d", len(tr.Words))
	}
	assertEqual(t, "word[0]", "Hello", tr.Words[0].Word)
	if tr.Words[0].Start != time.Duration(0.1*float64(time.Second)) {
		t.Errorf("unexpected start: %v", tr.Words[0].Start)
	}
}

func TestParseDeepgramResponse_Partial(t *testing.T) {
	raw := []byte(`{
		"type": "Results",
		"is_final": false,
		"channel": {
			"alternatives": [{
				"transcript": "Hello",
				"confidence": 0.7,
				"words": []
			}]
		}
	}`)

	tr, ok := parseDeepgramResponse(raw)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if tr.IsFinal {
		t.Error("expected IsFinal=false for partial result")
	}
	assertEqual(t, "text", "Hello", tr.Text)
}

func TestParseDeepgramResponse_NonResultsType(t *testing.T) {
	raw := []byte(`{"type":"Metadata","request_id":"abc"}`)
	_, ok := parseDeepgramResponse(raw)
	if ok {
		t.Error("expected ok=false for non-Results message")
	}
}

func TestParseDeepgramResponse_EmptyAlternatives(t *testing.T) {
	raw := []byte(`{"type":"Results","is_final":true,"channel":{"alternatives":[]}}`)
	_, ok := parseDeepgramResponse(raw)
	if ok {
		t.Error("expected ok=false when alternatives is empty")
	}
}

func TestParseDeepgramResponse_InvalidJSON(t *testing.T) {
	_, ok := parseDeepgramResponse([]byte(`{invalid`))
	if ok {
		t.Error("expected ok=false for invalid JSON")
	}
}

// ---- Constructor tests ----

func TestNew_EmptyAPIKey(t *testing.T) {
	_, err := New("")
	if err == nil {
		t.Error("expected error for empty API key")
	}
}

func TestNew_Defaults(t *testing.T) {
	p, err := New("key")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	assertEqual(t, "model", defaultModel, p.model)
	assertEqual(t, "language", defaultLanguage, p.language)
	if p.sampleRate != defaultSampleRate {
		t.Errorf("expected sampleRate %d, got %d", defaultSampleRate, p.sampleRate)
	}
}

// ---- helpers ----

func assertEqual(t *testing.T, label, want, got string) {
	t.Helper()
	if want != got {
		t.Errorf("%s: want %q, got %q", label, want, got)
	}
}
