// Package glyphoxa defines the shared types used across all Glyphoxa packages.
//
// These types form the lingua franca between providers, engines, memory layers,
// and the orchestrator. They are intentionally minimal — each package defines its
// own domain types, but cross-cutting data structures live here to avoid circular imports.
package types

import "time"

// AudioFrame represents a single frame of audio data flowing through the pipeline.
// Frames are the atomic unit of audio transport — captured from input streams,
// processed by VAD, encoded/decoded by codecs, and played through output streams.
type AudioFrame struct {
	// PCM audio data. Sample rate and channel count are determined by the pipeline config.
	Data []byte

	// SampleRate in Hz (e.g., 48000 for Discord Opus, 16000 for STT).
	SampleRate int

	// Channels: 1 for mono (STT input), 2 for stereo (Discord output).
	Channels int

	// Timestamp marks when this frame was captured, relative to stream start.
	Timestamp time.Duration
}

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

// Message represents a single message in an LLM conversation history.
type Message struct {
	// Role is one of "system", "user", "assistant", or "tool".
	Role string

	// Content is the text content of the message.
	Content string

	// Name is an optional participant name (for multi-speaker contexts).
	Name string

	// ToolCalls contains any tool invocations requested by the assistant.
	ToolCalls []ToolCall

	// ToolCallID is set when Role is "tool", identifying which tool call this responds to.
	ToolCallID string
}

// ToolCall represents a tool/function invocation requested by the LLM.
type ToolCall struct {
	// ID is the unique identifier for this tool call (provider-assigned).
	ID string

	// Name is the tool/function name.
	Name string

	// Arguments is the JSON-encoded arguments string.
	Arguments string
}

// ToolDefinition describes a tool that can be offered to an LLM.
type ToolDefinition struct {
	// Name is the tool's unique identifier.
	Name string

	// Description explains what the tool does (included in LLM prompts).
	Description string

	// Parameters is the JSON Schema describing the tool's input parameters.
	Parameters map[string]any

	// EstimatedDurationMs is the declared p50 latency for budget tier assignment.
	EstimatedDurationMs int

	// MaxDurationMs is the declared p99 upper bound, used as a hard timeout.
	MaxDurationMs int

	// Idempotent indicates whether the tool can be safely retried.
	Idempotent bool

	// CacheableSeconds is how long results can be cached (0 = never).
	CacheableSeconds int
}

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

// ModelCapabilities describes what an LLM model supports.
type ModelCapabilities struct {
	// ContextWindow is the maximum token count for input + output.
	ContextWindow int

	// MaxOutputTokens is the maximum tokens the model can generate in one completion.
	MaxOutputTokens int

	// SupportsToolCalling indicates native function/tool calling support.
	SupportsToolCalling bool

	// SupportsVision indicates the model can process image inputs.
	SupportsVision bool

	// SupportsStreaming indicates the model supports streaming completions.
	SupportsStreaming bool
}

// KeywordBoost represents a keyword to boost in STT recognition.
// Used to improve recognition of fantasy proper nouns (NPC names, locations, items).
type KeywordBoost struct {
	// Keyword is the text to boost (e.g., "Eldrinax").
	Keyword string

	// Boost is the intensity of the boost (provider-specific scale).
	Boost float64
}

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

// BudgetTier controls which MCP tools are visible to the LLM based on latency constraints.
type BudgetTier int

const (
	// BudgetFast allows only tools with ≤ 500ms estimated latency.
	BudgetFast BudgetTier = iota

	// BudgetStandard allows tools with ≤ 1500ms estimated latency.
	BudgetStandard

	// BudgetDeep allows all tools regardless of latency.
	BudgetDeep
)

// String returns the human-readable name of the budget tier.
func (t BudgetTier) String() string {
	switch t {
	case BudgetFast:
		return "FAST"
	case BudgetStandard:
		return "STANDARD"
	case BudgetDeep:
		return "DEEP"
	default:
		return "UNKNOWN"
	}
}

// MaxLatencyMs returns the maximum parallel tool latency for this tier.
func (t BudgetTier) MaxLatencyMs() int {
	switch t {
	case BudgetFast:
		return 500
	case BudgetStandard:
		return 1500
	case BudgetDeep:
		return 4000
	default:
		return 500
	}
}
