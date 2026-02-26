package tts

// VoiceProfile describes a TTS voice configuration for an NPC.
type VoiceProfile struct {
	// ID is the provider-specific voice identifier.
	ID string

	// Name is the human-readable voice name.
	Name string

	// Provider identifies which TTS provider this voice belongs to.
	Provider string

	// PitchShift adjusts pitch (-10 to +10, 0 = default).
	PitchShift float64

	// SpeedFactor adjusts speaking rate (0.5–2.0, 1.0 = default).
	SpeedFactor float64

	// Metadata holds provider-specific voice attributes (gender, age, accent, etc.).
	Metadata map[string]string
}
