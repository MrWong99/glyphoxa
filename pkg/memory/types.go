package memory

import "time"

// TranscriptEntry is a complete exchange record written to the session log.
// It captures both the speaker's utterance and optionally the NPC's response,
// forming the atomic unit of session history.
type TranscriptEntry struct {
	// SpeakerID identifies who spoke (player user ID or NPC name).
	SpeakerID string

	// SpeakerName is the human-readable speaker name.
	SpeakerName string

	// Text is the (possibly corrected) transcript text.
	Text string

	// RawText is the original uncorrected STT output. Preserved for debugging.
	RawText string

	// IsNPC indicates whether this entry is from an AI-controlled NPC.
	IsNPC bool

	// NPCID identifies the NPC agent if IsNPC is true.
	NPCID string

	// Timestamp is when this entry was recorded.
	Timestamp time.Time

	// Duration is the length of the utterance.
	Duration time.Duration
}
