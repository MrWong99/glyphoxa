package audio

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
