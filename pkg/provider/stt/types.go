package stt

import "time"

// Transcript represents a speech-to-text result from an STT provider.
// Both partial (interim) and final transcripts use this type.
type Transcript struct {
	// Text is the transcribed speech content.
	Text string

	// IsFinal indicates whether this is a final (authoritative) or partial (interim) transcript.
	IsFinal bool

	// Confidence is the overall confidence score (0.0–1.0). May be zero if the provider
	// does not report confidence.
	Confidence float64

	// Words contains per-word detail when available (Deepgram, Google).
	// May be nil for providers that don't support word-level output.
	Words []WordDetail

	// SpeakerID identifies the speaker when speaker diarization is active.
	SpeakerID string

	// Timestamp marks when the utterance started, relative to session start.
	Timestamp time.Duration

	// Duration is the length of the utterance.
	Duration time.Duration
}

// WordDetail holds per-word metadata from STT providers that support it.
type WordDetail struct {
	Word       string
	Start      time.Duration
	End        time.Duration
	Confidence float64
}

// KeywordBoost represents a keyword to boost in STT recognition.
// Used to improve recognition of fantasy proper nouns (NPC names, locations, items).
type KeywordBoost struct {
	// Keyword is the text to boost (e.g., "Eldrinax").
	Keyword string

	// Boost is the intensity of the boost (provider-specific scale).
	Boost float64
}
