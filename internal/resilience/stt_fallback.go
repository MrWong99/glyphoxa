package resilience

import (
	"context"

	"github.com/MrWong99/glyphoxa/pkg/provider/stt"
)

// STTFallback implements [stt.Provider] with automatic failover across multiple
// STT backends. Each backend has its own circuit breaker.
type STTFallback struct {
	group *FallbackGroup[stt.Provider]
}

// Compile-time interface assertion.
var _ stt.Provider = (*STTFallback)(nil)

// NewSTTFallback creates an [STTFallback] with primary as the preferred backend.
func NewSTTFallback(primary stt.Provider, primaryName string, cfg FallbackConfig) *STTFallback {
	return &STTFallback{
		group: NewFallbackGroup(primary, primaryName, cfg),
	}
}

// AddFallback registers an additional STT provider as a fallback.
func (f *STTFallback) AddFallback(name string, provider stt.Provider) {
	f.group.AddFallback(name, provider)
}

// StartStream opens a streaming transcription session against the first healthy
// provider. If the primary fails to start the stream, subsequent fallbacks are
// tried.
func (f *STTFallback) StartStream(ctx context.Context, cfg stt.StreamConfig) (stt.SessionHandle, error) {
	return ExecuteWithResult(f.group, func(p stt.Provider) (stt.SessionHandle, error) {
		return p.StartStream(ctx, cfg)
	})
}
