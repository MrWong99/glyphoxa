package llmcorrect

import (
	"strings"
	"testing"
)

func TestVerifyCorrectedText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		original        string
		corrected       string
		corrections     []Correction
		wantText        string
		wantCorrections int
	}{
		{
			name:            "identical text",
			original:        "the wizard awaits",
			corrected:       "the wizard awaits",
			corrections:     nil,
			wantText:        "the wizard awaits",
			wantCorrections: 0,
		},
		{
			name:      "single verified correction",
			original:  "eldrinaks arrived",
			corrected: "Eldrinax arrived",
			corrections: []Correction{
				{Original: "eldrinaks", Corrected: "Eldrinax", Confidence: 0.9},
			},
			wantText:        "Eldrinax arrived",
			wantCorrections: 1,
		},
		{
			name:      "multi-word correction",
			original:  "elder nacks guards the gate",
			corrected: "Eldrinax guards the gate",
			corrections: []Correction{
				{Original: "elder nacks", Corrected: "Eldrinax", Confidence: 0.9},
			},
			wantText:        "Eldrinax guards the gate",
			wantCorrections: 1,
		},
		{
			name:            "unverified change reverted",
			original:        "the cat sits quietly",
			corrected:       "the dog sits quietly",
			corrections:     nil,
			wantText:        "the cat sits quietly",
			wantCorrections: 0,
		},
		{
			name:      "mixed verified and unverified",
			original:  "elder nacks lives in the nice tower",
			corrected: "Eldrinax lives in the beautiful tower",
			corrections: []Correction{
				{Original: "elder nacks", Corrected: "Eldrinax", Confidence: 0.9},
			},
			wantText:        "Eldrinax lives in the nice tower",
			wantCorrections: 1,
		},
		{
			name:            "empty corrections with changed text reverts fully",
			original:        "the wizard speaks wisdom",
			corrected:       "the mage speaks truth",
			corrections:     []Correction{},
			wantText:        "the wizard speaks wisdom",
			wantCorrections: 0,
		},
		{
			name:      "punctuation attached to tokens",
			original:  "Tower of Wispers.",
			corrected: "Tower of Whispers.",
			corrections: []Correction{
				{Original: "Wispers", Corrected: "Whispers", Confidence: 0.85},
			},
			wantText:        "Tower of Whispers.",
			wantCorrections: 1,
		},
		{
			name:      "multiple verified corrections",
			original:  "elder nacks guards the Tower of Wispers.",
			corrected: "Eldrinax guards the Tower of Whispers.",
			corrections: []Correction{
				{Original: "elder nacks", Corrected: "Eldrinax", Confidence: 0.9},
				{Original: "Wispers", Corrected: "Whispers", Confidence: 0.85},
			},
			wantText:        "Eldrinax guards the Tower of Whispers.",
			wantCorrections: 2,
		},
		{
			name:      "case insensitive lookup",
			original:  "ELDRINAKS arrived",
			corrected: "Eldrinax arrived",
			corrections: []Correction{
				{Original: "eldrinaks", Corrected: "Eldrinax", Confidence: 0.9},
			},
			wantText:        "Eldrinax arrived",
			wantCorrections: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotText, gotCorr := verifyCorrectedText(tt.original, tt.corrected, tt.corrections)
			if gotText != tt.wantText {
				t.Errorf("text = %q, want %q", gotText, tt.wantText)
			}
			if len(gotCorr) != tt.wantCorrections {
				t.Errorf("corrections count = %d, want %d", len(gotCorr), tt.wantCorrections)
			}
		})
	}
}

func TestTokenLCS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		a, b    []string
		wantLen int
	}{
		{"both empty", nil, nil, 0},
		{"a empty", nil, strings.Fields("hello world"), 0},
		{"b empty", strings.Fields("hello world"), nil, 0},
		{"identical", strings.Fields("a b c"), strings.Fields("a b c"), 3},
		{"no common", strings.Fields("a b"), strings.Fields("c d"), 0},
		{"partial overlap", strings.Fields("a b c d"), strings.Fields("a x c d"), 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			anchors := tokenLCS(tt.a, tt.b)
			if len(anchors) != tt.wantLen {
				t.Errorf("LCS length = %d, want %d", len(anchors), tt.wantLen)
			}
		})
	}
}

func TestExtractChangeSpans(t *testing.T) {
	t.Parallel()

	orig := strings.Fields("a X c Y e")
	corr := strings.Fields("a B c D e")
	anchors := tokenLCS(orig, corr)
	spans := extractChangeSpans(orig, corr, anchors)

	if len(spans) != 2 {
		t.Fatalf("got %d spans, want 2", len(spans))
	}
	if strings.Join(spans[0].origTokens, " ") != "X" {
		t.Errorf("span[0].orig = %q, want %q", strings.Join(spans[0].origTokens, " "), "X")
	}
	if strings.Join(spans[0].corrTokens, " ") != "B" {
		t.Errorf("span[0].corr = %q, want %q", strings.Join(spans[0].corrTokens, " "), "B")
	}
	if strings.Join(spans[1].origTokens, " ") != "Y" {
		t.Errorf("span[1].orig = %q, want %q", strings.Join(spans[1].origTokens, " "), "Y")
	}
	if strings.Join(spans[1].corrTokens, " ") != "D" {
		t.Errorf("span[1].corr = %q, want %q", strings.Join(spans[1].corrTokens, " "), "D")
	}
}
