package resilience

import (
	"context"

	"github.com/MrWong99/glyphoxa/pkg/provider/tts"
)

// TTSFallback implements [tts.Provider] with automatic failover across multiple
// TTS backends. Each backend has its own circuit breaker.
type TTSFallback struct {
	group *FallbackGroup[tts.Provider]
}

// Compile-time interface assertion.
var _ tts.Provider = (*TTSFallback)(nil)

// NewTTSFallback creates a [TTSFallback] with primary as the preferred backend.
func NewTTSFallback(primary tts.Provider, primaryName string, cfg FallbackConfig) *TTSFallback {
	return &TTSFallback{
		group: NewFallbackGroup(primary, primaryName, cfg),
	}
}

// AddFallback registers an additional TTS provider as a fallback.
func (f *TTSFallback) AddFallback(name string, provider tts.Provider) {
	f.group.AddFallback(name, provider)
}

// SynthesizeStream consumes text fragments and returns a channel of audio bytes,
// trying the first healthy provider. Only the initial stream setup is covered by
// failover; mid-stream errors are the caller's responsibility.
func (f *TTSFallback) SynthesizeStream(ctx context.Context, text <-chan string, voice tts.VoiceProfile) (<-chan []byte, error) {
	return ExecuteWithResult(f.group, func(p tts.Provider) (<-chan []byte, error) {
		return p.SynthesizeStream(ctx, text, voice)
	})
}

// ListVoices returns available voices from the first healthy provider.
func (f *TTSFallback) ListVoices(ctx context.Context) ([]tts.VoiceProfile, error) {
	return ExecuteWithResult(f.group, func(p tts.Provider) ([]tts.VoiceProfile, error) {
		return p.ListVoices(ctx)
	})
}

// CloneVoice creates a new voice profile using the first healthy provider.
func (f *TTSFallback) CloneVoice(ctx context.Context, samples [][]byte) (*tts.VoiceProfile, error) {
	return ExecuteWithResult(f.group, func(p tts.Provider) (*tts.VoiceProfile, error) {
		return p.CloneVoice(ctx, samples)
	})
}
