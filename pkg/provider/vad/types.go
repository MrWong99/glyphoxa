package vad

// VADEvent represents a voice activity detection result for a single audio frame.
type VADEvent struct {
	// Type is the detection result.
	Type VADEventType

	// Probability is the speech probability score (0.0–1.0).
	Probability float64
}

// VADEventType enumerates VAD detection states.
type VADEventType int

const (
	// VADSpeechStart indicates speech has just begun.
	VADSpeechStart VADEventType = iota

	// VADSpeechContinue indicates ongoing speech.
	VADSpeechContinue

	// VADSpeechEnd indicates speech has just ended.
	VADSpeechEnd

	// VADSilence indicates no speech detected.
	VADSilence
)
