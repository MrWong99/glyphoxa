// Package llmcorrect implements a language-model-based transcript correction
// stage that resolves entity misspellings not caught by the phonetic matcher.
//
// The [Corrector] sends the raw transcript text to an [llm.Provider] along
// with the list of known entity names. The model is instructed (via a
// conservative system prompt) to fix only words that look like misspelled
// entity names and to return a structured JSON response containing the
// corrected text and an itemised list of substitutions.
//
// This stage runs exclusively in the background — never on the real-time
// voice path — so the small latency penalty (100–200 ms) is acceptable.
// When the LLM response cannot be parsed, the corrector returns the original
// text unchanged rather than surfacing an error, ensuring pipeline
// robustness.
package llmcorrect

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	llm "github.com/MrWong99/glyphoxa/pkg/provider/llm"
)

const (
	defaultTemperature = 0.1
)

// systemPromptTemplate is the base system prompt. The entity list is
// appended at call time so each request carries the current session context.
const systemPromptTemplate = `You are a transcript correction assistant for a tabletop role-playing game session.

Your task: fix entity name misspellings in the provided transcript text.

Rules:
- ONLY correct words that appear to be misspelled versions of the known entity names listed below.
- Do NOT change ordinary English words, grammar, punctuation, or sentence structure.
- Be conservative — if you are not confident a word is a misspelled entity name, leave it unchanged.
- Preserve the original capitalisation style of the surrounding text where possible.
- Entity names in the corrected text should match the canonical spelling from the entity list exactly.

Known entities:
%s

Respond with ONLY a JSON object in this exact format (no markdown, no prose):
{
  "corrected_text": "<full corrected transcript>",
  "corrections": [
    {"original": "<original word>", "corrected": "<corrected word>", "confidence": <0.0-1.0>}
  ]
}

If no corrections are needed, return an empty corrections array and corrected_text equal to the input.`

// Correction captures a single word-level substitution produced by the LLM
// corrector. The pipeline maps these to [transcript.Correction] values with
// Method set to "llm".
type Correction struct {
	// Original is the word as it appeared in the input transcript.
	Original string

	// Corrected is the replacement entity name suggested by the LLM.
	Corrected string

	// Confidence is the LLM's reported confidence for this substitution (0.0–1.0).
	Confidence float64
}

// llmResponse is the expected JSON structure returned by the LLM.
type llmResponse struct {
	CorrectedText string `json:"corrected_text"`
	Corrections   []struct {
		Original   string  `json:"original"`
		Corrected  string  `json:"corrected"`
		Confidence float64 `json:"confidence"`
	} `json:"corrections"`
}

// Option is a functional option for configuring a [Corrector].
type Option func(*Corrector)

// WithTemperature sets the LLM sampling temperature. Lower values produce
// more deterministic corrections. Default: 0.1.
func WithTemperature(temp float64) Option {
	return func(c *Corrector) {
		c.temperature = temp
	}
}

// Corrector uses an [llm.Provider] to correct entity name misspellings in
// transcript text. It is safe for concurrent use.
//
// Model selection follows the one-provider-per-model pattern: to use a
// specific model for correction, construct the [llm.Provider] with that
// model configured, rather than overriding per-request.
type Corrector struct {
	llm         llm.Provider
	temperature float64
}

// New returns a new [Corrector] backed by the given [llm.Provider].
// Apply [Option] values to override the default temperature or model.
func New(provider llm.Provider, opts ...Option) *Corrector {
	c := &Corrector{
		llm:         provider,
		temperature: defaultTemperature,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Correct sends text to the LLM with the entity list as context and asks it
// to fix entity name misspellings. lowConfidenceSpans are highlighted in the
// user message as candidate spans that may be misheard.
//
// When the LLM response is unparseable, Correct returns the original text
// unchanged with a nil corrections slice and a nil error (graceful
// degradation — the pipeline must continue).
//
// Context cancellation and network errors are returned as non-nil errors.
func (c *Corrector) Correct(
	ctx context.Context,
	text string,
	entities []string,
	lowConfidenceSpans []string,
) (string, []Correction, error) {
	if len(entities) == 0 {
		return text, nil, nil
	}

	sysPrompt := buildSystemPrompt(entities)

	userMsg := text
	if len(lowConfidenceSpans) > 0 {
		userMsg = fmt.Sprintf(
			"Transcript: %s\n\nLow-confidence spans that may be misheard: %s",
			text,
			strings.Join(lowConfidenceSpans, ", "),
		)
	}

	req := llm.CompletionRequest{
		SystemPrompt: sysPrompt,
		Temperature:  c.temperature,
		Messages: []llm.Message{
			{Role: "user", Content: userMsg},
		},
	}

	resp, err := c.llm.Complete(ctx, req)
	if err != nil {
		return text, nil, fmt.Errorf("llm corrector: complete: %w", err)
	}

	corrected, corrections, parseErr := parseResponse(resp.Content, text)
	if parseErr != nil {
		// Unparseable response: return original unchanged, no error.
		return text, nil, nil //nolint:nilerr // intentional graceful fallback
	}

	return corrected, corrections, nil
}

// buildSystemPrompt formats the system prompt template with the entity list.
func buildSystemPrompt(entities []string) string {
	var sb strings.Builder
	for _, e := range entities {
		sb.WriteString("- ")
		sb.WriteString(e)
		sb.WriteByte('\n')
	}
	return fmt.Sprintf(systemPromptTemplate, sb.String())
}

// parseResponse attempts to unmarshal the LLM output into an [llmResponse].
// It strips markdown code fences before parsing.
func parseResponse(content, originalText string) (string, []Correction, error) {
	cleaned := stripMarkdown(content)

	var r llmResponse
	if err := json.Unmarshal([]byte(cleaned), &r); err != nil {
		return "", nil, fmt.Errorf("llm corrector: parse response: %w", err)
	}

	if r.CorrectedText == "" {
		return originalText, nil, nil
	}

	corrections := make([]Correction, 0, len(r.Corrections))
	for _, c := range r.Corrections {
		if c.Original == c.Corrected || c.Original == "" {
			continue
		}
		corrections = append(corrections, Correction{
			Original:   c.Original,
			Corrected:  c.Corrected,
			Confidence: c.Confidence,
		})
	}

	return r.CorrectedText, corrections, nil
}

// stripMarkdown removes optional markdown code fences (```json ... ```) that
// some models prepend and append to JSON output.
func stripMarkdown(s string) string {
	s = strings.TrimSpace(s)
	for _, prefix := range []string{"```json", "```"} {
		if after, ok := strings.CutPrefix(s, prefix); ok {
			s = after
			break
		}
	}
	if before, ok := strings.CutSuffix(s, "```"); ok {
		s = before
	}
	return strings.TrimSpace(s)
}
