package transcript

import (
	"context"
	"strings"

	"github.com/MrWong99/glyphoxa/internal/transcript/llmcorrect"
	"github.com/MrWong99/glyphoxa/internal/transcript/phonetic"
	"github.com/MrWong99/glyphoxa/pkg/provider/stt"
)

const (
	defaultLLMConfidenceThreshold = 0.5
)

// PipelineOption is a functional option for configuring a [CorrectionPipeline].
type PipelineOption func(*CorrectionPipeline)

// WithPhoneticMatcher attaches a [PhoneticMatcher] as the first correction
// stage. When nil (the default), the phonetic stage is skipped entirely.
func WithPhoneticMatcher(m PhoneticMatcher) PipelineOption {
	return func(p *CorrectionPipeline) {
		p.phonetic = m
	}
}

// WithLLMCorrector attaches an [llmcorrect.Corrector] as the second correction
// stage. When nil (the default), the LLM stage is skipped entirely.
func WithLLMCorrector(c *llmcorrect.Corrector) PipelineOption {
	return func(p *CorrectionPipeline) {
		p.llmCorrector = c
	}
}

// WithLLMOnLowConfidence sets the STT word-confidence threshold below which a
// word is flagged as a low-confidence span and passed to the LLM corrector
// (when one is configured). Default: 0.5.
//
// Words with [stt.WordDetail.Confidence] below this value that were NOT
// already corrected by the phonetic stage are submitted to the LLM for review.
// Words without any confidence data (i.e., the transcript has no Words slice)
// are always submitted when the LLM corrector is configured.
func WithLLMOnLowConfidence(threshold float64) PipelineOption {
	return func(p *CorrectionPipeline) {
		p.llmThreshold = threshold
	}
}

// CorrectionPipeline is the two-stage transcript correction implementation of
// [Pipeline]. Stages are optional and are applied in order:
//
//  1. [PhoneticMatcher] — fast, in-process phonetic entity alignment.
//  2. [llmcorrect.Corrector] — LLM-assisted correction for low-confidence spans.
//
// CorrectionPipeline is safe for concurrent use.
type CorrectionPipeline struct {
	phonetic     PhoneticMatcher
	llmCorrector *llmcorrect.Corrector
	llmThreshold float64
}

// Ensure CorrectionPipeline satisfies the Pipeline interface at compile time.
var _ Pipeline = (*CorrectionPipeline)(nil)

// NewPipeline constructs a [CorrectionPipeline] with the supplied options.
// By default both stages are disabled (nil); use [WithPhoneticMatcher] and
// [WithLLMCorrector] to activate them.
func NewPipeline(opts ...PipelineOption) *CorrectionPipeline {
	p := &CorrectionPipeline{
		llmThreshold: defaultLLMConfidenceThreshold,
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

// Correct applies the configured correction stages to transcript and returns a
// [CorrectedTranscript].
//
// Pipeline flow:
//  1. The transcript text is tokenised into whitespace-separated word tokens.
//  2. When a [PhoneticMatcher] is configured, every single-word token is tested
//     against the entity list. Additionally, n-gram windows (up to the maximum
//     entity word count) are tested to match multi-word entities.
//  3. Words that carry a [stt.WordDetail] confidence score below the LLM
//     threshold AND were not corrected by the phonetic stage are collected as
//     low-confidence spans.
//  4. When an [llmcorrect.Corrector] is configured and at least one
//     low-confidence span exists (or no per-word confidence data is available),
//     the LLM corrector is invoked on the phonetic-corrected text.
//  5. Phonetic and LLM corrections are merged into the final
//     [CorrectedTranscript].
//
// Context cancellation is respected: if ctx is Done before the LLM stage
// completes, an error is returned.
func (p *CorrectionPipeline) Correct(
	ctx context.Context,
	t stt.Transcript,
	entities []string,
) (*CorrectedTranscript, error) {
	result := &CorrectedTranscript{
		Original:    t,
		Corrections: []Correction{},
	}

	// --- Stage 1: phonetic matching ---
	workingText := t.Text
	var phoneticCorrections []Correction

	if p.phonetic != nil && len(entities) > 0 {
		correctedText, corrections := p.applyPhonetic(t.Text, t.Words, entities)
		workingText = correctedText
		phoneticCorrections = corrections
	}

	// Build set of positions already corrected by phonetic stage (by original word).
	phoneticCorrectedWords := make(map[string]struct{}, len(phoneticCorrections))
	for _, c := range phoneticCorrections {
		phoneticCorrectedWords[strings.ToLower(c.Original)] = struct{}{}
	}

	// --- Stage 2: LLM correction ---
	var llmCorrections []Correction

	if p.llmCorrector != nil && len(entities) > 0 {
		lowConfSpans := p.collectLowConfidenceSpans(t.Words, phoneticCorrectedWords)

		// When there is no per-word confidence data, we always run the LLM.
		// When there IS per-word data, we only run if there are flagged spans.
		if len(t.Words) == 0 || len(lowConfSpans) > 0 {
			correctedText, rawCorrections, err := p.llmCorrector.Correct(
				ctx,
				workingText,
				entities,
				lowConfSpans,
			)
			if err != nil {
				return nil, err
			}
			workingText = correctedText
			for _, rc := range rawCorrections {
				llmCorrections = append(llmCorrections, Correction{
					Original:   rc.Original,
					Corrected:  rc.Corrected,
					Confidence: rc.Confidence,
					Method:     "llm",
				})
			}
		}
	}

	// --- Merge results ---
	result.Corrected = workingText
	result.Corrections = append(result.Corrections, phoneticCorrections...)
	result.Corrections = append(result.Corrections, llmCorrections...)

	return result, nil
}

// applyPhonetic runs the phonetic matching stage over the transcript text.
// It returns the corrected text and the list of corrections applied.
//
// The algorithm:
//  1. Tokenise the text into words.
//  2. Determine the maximum number of words in any entity name.
//  3. At each token position, try n-gram windows from maxEntityWords down to 1.
//     Accept the longest n-gram match so that multi-word entities take
//     precedence over partial single-word matches.
//  4. Append matched (or unmatched) tokens to the output and advance the
//     cursor by the number of tokens consumed.
func (p *CorrectionPipeline) applyPhonetic(
	text string,
	wordDetails []stt.WordDetail,
	entities []string,
) (string, []Correction) {
	tokens := strings.Fields(text)
	if len(tokens) == 0 {
		return text, nil
	}

	// When the matcher supports precomputation, prepare entity data once
	// and use the fast path for all window comparisons.
	var matchFn func(string) (string, float64, bool)
	var maxEntityWords int

	if pm, ok := p.phonetic.(*phonetic.Matcher); ok {
		es := phonetic.PrepareEntities(entities)
		maxEntityWords = es.MaxWords()
		matchFn = func(word string) (string, float64, bool) {
			return pm.MatchPrepared(word, es)
		}
	} else {
		maxEntityWords = maxWordCount(entities)
		matchFn = func(word string) (string, float64, bool) {
			return p.phonetic.Match(word, entities)
		}
	}

	if maxEntityWords == 0 {
		return text, nil
	}

	var output []string
	var corrections []Correction

	i := 0
	for i < len(tokens) {
		// Clamp window size to remaining tokens.
		maxN := maxEntityWords
		if i+maxN > len(tokens) {
			maxN = len(tokens) - i
		}

		matched := false
		for n := maxN; n >= 1; n-- {
			window := strings.Join(tokens[i:i+n], " ")
			entity, conf, ok := matchFn(window)
			if !ok {
				continue
			}

			// Emit the entity tokens and record the correction.
			entityTokens := strings.Fields(entity)
			output = append(output, entityTokens...)
			corrections = append(corrections, Correction{
				Original:   window,
				Corrected:  entity,
				Confidence: conf,
				Method:     "phonetic",
			})
			i += n
			matched = true
			break
		}

		if !matched {
			output = append(output, tokens[i])
			i++
		}
	}

	return strings.Join(output, " "), corrections
}

// collectLowConfidenceSpans returns the words whose STT confidence is below
// the configured threshold and that were not already corrected by the phonetic
// stage.
func (p *CorrectionPipeline) collectLowConfidenceSpans(
	wordDetails []stt.WordDetail,
	alreadyCorrected map[string]struct{},
) []string {
	var spans []string
	for _, wd := range wordDetails {
		wordLower := strings.ToLower(wd.Word)
		if _, corrected := alreadyCorrected[wordLower]; corrected {
			continue
		}
		if wd.Confidence < p.llmThreshold {
			spans = append(spans, wd.Word)
		}
	}
	return spans
}

// maxWordCount returns the maximum number of whitespace-separated words in
// any entity string. Returns 1 when entities is empty.
func maxWordCount(entities []string) int {
	max := 1
	for _, e := range entities {
		n := len(strings.Fields(e))
		if n > max {
			max = n
		}
	}
	return max
}
