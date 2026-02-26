// Package phonetic implements the [transcript.PhoneticMatcher] interface using
// Double Metaphone phonetic encoding combined with Jaro-Winkler string
// similarity for ranked candidate selection.
//
// The algorithm proceeds in two stages:
//
//  1. Phonetic candidate filtering: Double Metaphone codes are computed for
//     each word in the input and for each known entity. If any code from the
//     input overlaps with any code from an entity, the entity becomes a
//     phonetic candidate.
//
//  2. Jaro-Winkler ranking: Among phonetic candidates, the entity with the
//     highest Jaro-Winkler similarity (computed on the original strings,
//     case-insensitive) is selected — provided its score exceeds the
//     configurable phonetic threshold.
//
//     When no phonetic candidate is found, a secondary pass tests pure
//     Jaro-Winkler similarity against all entities using a higher fuzzy
//     threshold (default 0.85).
//
// Multi-word entities (e.g., "Tower of Whispers") are supported: the matcher
// computes phonetic codes for each word and considers the best pairwise score
// across all word pairs when ranking candidates.
package phonetic

import (
	"strings"

	"github.com/antzucaro/matchr"
)

const (
	defaultPhoneticThreshold = 0.70
	defaultFuzzyThreshold    = 0.85
)

// Option is a functional option for configuring a [Matcher].
type Option func(*Matcher)

// WithPhoneticThreshold sets the minimum Jaro-Winkler score required for a
// phonetically-matched entity to be accepted. Default: 0.70.
func WithPhoneticThreshold(threshold float64) Option {
	return func(m *Matcher) {
		m.phoneticThreshold = threshold
	}
}

// WithFuzzyThreshold sets the minimum Jaro-Winkler score required when no
// phonetic match is found and the matcher falls back to pure string
// similarity. Default: 0.85.
func WithFuzzyThreshold(threshold float64) Option {
	return func(m *Matcher) {
		m.fuzzyThreshold = threshold
	}
}

// Matcher is a phonetic entity matcher. It implements [transcript.PhoneticMatcher].
// All methods are safe for concurrent use — the Matcher is read-only after
// construction.
type Matcher struct {
	phoneticThreshold float64
	fuzzyThreshold    float64
}

// entityInfo holds precomputed data for a single entity.
type entityInfo struct {
	original string
	lower    string
	tokens   []string
	codes    map[string]struct{}
}

// EntitySet holds precomputed entity data for efficient repeated matching.
// Build one with [PrepareEntities] and pass it to [Matcher.MatchPrepared].
type EntitySet struct {
	entries  []entityInfo
	maxWords int
}

// MaxWords returns the maximum number of whitespace-separated words across
// all entities in the set.
func (es *EntitySet) MaxWords() int { return es.maxWords }

// PrepareEntities precomputes lowercased forms, token lists, and Double
// Metaphone codes for each entity. The returned [EntitySet] can be reused
// across multiple [Matcher.MatchPrepared] calls with different input words.
func PrepareEntities(entities []string) *EntitySet {
	es := &EntitySet{
		entries: make([]entityInfo, 0, len(entities)),
	}
	for _, entity := range entities {
		lower := strings.ToLower(strings.TrimSpace(entity))
		if lower == "" {
			continue
		}
		tokens := strings.Fields(lower)
		if n := len(tokens); n > es.maxWords {
			es.maxWords = n
		}
		es.entries = append(es.entries, entityInfo{
			original: entity,
			lower:    lower,
			tokens:   tokens,
			codes:    codesForTokens(tokens),
		})
	}
	return es
}

// New returns a new [Matcher] configured with the supplied options.
// Default thresholds are 0.70 for phonetic matches and 0.85 for fuzzy
// fallback matches.
func New(opts ...Option) *Matcher {
	m := &Matcher{
		phoneticThreshold: defaultPhoneticThreshold,
		fuzzyThreshold:    defaultFuzzyThreshold,
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

// MatchPrepared is like [Matcher.Match] but uses a precomputed [EntitySet]
// to avoid redundant phonetic-code and string-normalization work on each call.
func (m *Matcher) MatchPrepared(word string, es *EntitySet) (corrected string, confidence float64, matched bool) {
	if len(es.entries) == 0 || strings.TrimSpace(word) == "" {
		return word, 0, false
	}

	wordLower := strings.ToLower(strings.TrimSpace(word))
	wordTokens := strings.Fields(wordLower)
	inputCodes := codesForTokens(wordTokens)

	type candidate struct {
		entity   string
		score    float64
		phonetic bool
	}

	var best candidate

	for i := range es.entries {
		ei := &es.entries[i]

		phoneticMatch := codesOverlap(inputCodes, ei.codes)
		jwScore := bestJWScore(wordTokens, ei.tokens, wordLower, ei.lower)

		if phoneticMatch {
			if jwScore >= m.phoneticThreshold {
				if !best.phonetic || jwScore > best.score {
					best = candidate{entity: ei.original, score: jwScore, phonetic: true}
				}
			}
		} else if !best.phonetic {
			if jwScore >= m.fuzzyThreshold && jwScore > best.score {
				best = candidate{entity: ei.original, score: jwScore, phonetic: false}
			}
		}
	}

	if best.entity != "" {
		return best.entity, best.score, true
	}
	return word, 0, false
}

// Match attempts to find the entity from entities that is most phonetically
// similar to word.
//
// word may be a single word or a space-separated phrase (n-gram). When word
// contains multiple tokens, the matcher checks whether any token phonetically
// aligns with any token in a multi-word entity, then ranks by Jaro-Winkler
// on the full strings.
//
// Return values follow the [transcript.PhoneticMatcher] contract: when
// matched is false, corrected equals word unchanged and confidence is 0.
//
// For repeated calls with the same entity list, prefer [PrepareEntities]
// followed by [Matcher.MatchPrepared] to avoid redundant precomputation.
func (m *Matcher) Match(word string, entities []string) (corrected string, confidence float64, matched bool) {
	if len(entities) == 0 || strings.TrimSpace(word) == "" {
		return word, 0, false
	}
	return m.MatchPrepared(word, PrepareEntities(entities))
}

// codesForTokens returns the union of all Double Metaphone codes for the
// given tokens. Empty codes (produced when the word is too short or
// contains no consonants) are excluded.
func codesForTokens(tokens []string) map[string]struct{} {
	codes := make(map[string]struct{}, len(tokens)*2)
	for _, t := range tokens {
		p, s := matchr.DoubleMetaphone(t)
		if p != "" {
			codes[p] = struct{}{}
		}
		if s != "" {
			codes[s] = struct{}{}
		}
	}
	return codes
}

// codesOverlap returns true if the two code sets share at least one code.
func codesOverlap(a, b map[string]struct{}) bool {
	// Iterate over the smaller set for efficiency.
	if len(a) > len(b) {
		a, b = b, a
	}
	for code := range a {
		if _, ok := b[code]; ok {
			return true
		}
	}
	return false
}

// bestJWScore computes the highest Jaro-Winkler similarity between the input
// and the entity using three strategies:
//
//  1. Full-string comparison (e.g., "elder nacks" vs "eldrinax").
//  2. Space-stripped comparison (e.g., "eldernacks" vs "eldrinax").
//  3. Best pairwise word comparison — the maximum JW score between any input
//     token and any entity token (useful when one spoken word corresponds to
//     one entity word).
//
// longTolerance is passed as false to use standard Jaro-Winkler scoring.
func bestJWScore(inputTokens, entityTokens []string, inputFull, entityFull string) float64 {
	// Strategy 1: full strings.
	score := matchr.JaroWinkler(inputFull, entityFull, false)

	// Strategy 2: concatenated (no spaces).
	if len(inputTokens) > 1 || len(entityTokens) > 1 {
		concat1 := strings.Join(inputTokens, "")
		concat2 := strings.Join(entityTokens, "")
		if s := matchr.JaroWinkler(concat1, concat2, false); s > score {
			score = s
		}
	}

	// Strategy 3: best pairwise token score.
	for _, it := range inputTokens {
		for _, et := range entityTokens {
			if s := matchr.JaroWinkler(it, et, false); s > score {
				score = s
			}
		}
	}

	return score
}
