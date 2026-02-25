// Package transcript defines the transcript correction pipeline used by
// Glyphoxa to fix STT errors in domain-specific vocabulary.
//
// Raw speech-to-text output is rarely perfect for fantasy proper nouns —
// NPC names, location names, faction names, and spell names are frequently
// misheard. The [Pipeline] applies a two-stage correction strategy:
//
//  1. Phonetic matching ([PhoneticMatcher]): fast, dictionary-free alignment
//     based on pronunciation similarity (e.g., Soundex, Metaphone, or edit
//     distance on phoneme sequences). Runs in-process with no network calls.
//
//  2. LLM-assisted correction: a language model resolves ambiguous or
//     low-confidence phonetic candidates using session context and the full
//     entity list. Falls back to the phonetic suggestion when confidence is
//     sufficient, or leaves the original word unchanged.
//
// Each [Correction] records which method produced the substitution and its
// confidence, so callers can audit, display, or selectively roll back changes.
//
// Implementations of both interfaces must be safe for concurrent use.
package transcript

import (
	"context"

	"github.com/MrWong99/glyphoxa/pkg/types"
)

// Correction captures a single word-level substitution made by the pipeline.
type Correction struct {
	// Original is the word as produced by the STT provider.
	Original string

	// Corrected is the replacement selected by the pipeline.
	Corrected string

	// Confidence is the pipeline's confidence in this substitution (0.0–1.0).
	// Values above 0.9 are considered high-confidence; values below 0.5
	// indicate the correction is speculative.
	Confidence float64

	// Method describes which correction stage produced this substitution.
	// Well-known values:
	//   "phonetic" — produced by a [PhoneticMatcher].
	//   "llm"      — produced by a language-model correction pass.
	Method string
}

// CorrectedTranscript is the output of a [Pipeline.Correct] call.
// It pairs the original [types.Transcript] with the fully corrected text and
// an itemised record of every substitution that was applied.
type CorrectedTranscript struct {
	// Original is the raw [types.Transcript] as received from the STT provider.
	Original types.Transcript

	// Corrected is the full corrected transcript text with all substitutions
	// applied. Suitable for downstream processing (memory storage, LLM context).
	Corrected string

	// Corrections is the ordered list of word-level substitutions applied to
	// produce Corrected. An empty (non-nil) slice means no corrections were
	// necessary.
	Corrections []Correction
}

// Pipeline applies multi-stage corrections to a raw [types.Transcript],
// resolving STT errors for domain-specific vocabulary.
//
// Implementations must be safe for concurrent use.
type Pipeline interface {
	// Correct processes transcript using the provided entity name list and
	// returns a [CorrectedTranscript] containing the corrected text and an
	// itemised record of every substitution made.
	//
	// entities is the list of known entity names the pipeline should recognise
	// within the transcript text. It may include NPC names, location names,
	// item names, faction names, and other session-specific proper nouns.
	//
	// Returns a non-nil *CorrectedTranscript on success.
	// When no corrections are needed, Corrected equals transcript.Text and
	// Corrections is an empty (non-nil) slice.
	Correct(ctx context.Context, transcript types.Transcript, entities []string) (*CorrectedTranscript, error)
}

// PhoneticMatcher resolves a single word to a known entity name based on
// pronunciation similarity. It is the first stage of the correction pipeline
// and is designed to be fast enough for real-time use — no network calls,
// no LLM round-trips.
//
// Implementations must be safe for concurrent use.
type PhoneticMatcher interface {
	// Match attempts to find the entity name from entities that is most
	// phonetically similar to word.
	//
	// Return values:
	//   corrected  — the best-matching entity name from entities.
	//   confidence — similarity score in [0.0, 1.0] where 1.0 is a perfect match.
	//   matched    — true when a sufficiently similar entity was found.
	//
	// When matched is false, corrected must equal word unchanged and confidence
	// must be 0. Implementations define their own similarity threshold for
	// deciding when a match is "sufficient".
	Match(word string, entities []string) (corrected string, confidence float64, matched bool)
}
