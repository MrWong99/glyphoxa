// Package mock provides a test double for the tts.Provider interface.
//
// Use Provider to feed controlled audio chunks to consumers and to verify that
// the correct VoiceProfile and text fragments are passed to the TTS backend.
//
// Example:
//
//	p := &mock.Provider{
//	    SynthesizeChunks: [][]byte{[]byte("audio1"), []byte("audio2")},
//	    ListVoicesResult: []types.VoiceProfile{{ID: "v1", Name: "Alice"}},
//	}
//	ch, _ := p.SynthesizeStream(ctx, textCh, voice)
package mock

import (
	"context"
	"sync"

	"github.com/MrWong99/glyphoxa/pkg/provider/tts"
	"github.com/MrWong99/glyphoxa/pkg/types"
)

// SynthesizeStreamCall records a single invocation of SynthesizeStream.
type SynthesizeStreamCall struct {
	// Ctx is the context passed to SynthesizeStream.
	Ctx context.Context
	// Text is the text input channel passed to SynthesizeStream.
	Text <-chan string
	// Voice is the VoiceProfile passed to SynthesizeStream.
	Voice types.VoiceProfile
}

// ListVoicesCall records a single invocation of ListVoices.
type ListVoicesCall struct {
	// Ctx is the context passed to ListVoices.
	Ctx context.Context
}

// CloneVoiceCall records a single invocation of CloneVoice.
type CloneVoiceCall struct {
	// Ctx is the context passed to CloneVoice.
	Ctx context.Context
	// Samples is a copy of the audio samples passed to CloneVoice.
	Samples [][]byte
}

// Provider is a mock implementation of tts.Provider.
type Provider struct {
	mu sync.Mutex

	// --- Configurable responses ---

	// SynthesizeChunks is the sequence of audio byte slices emitted on the channel
	// returned by SynthesizeStream.
	SynthesizeChunks [][]byte

	// SynthesizeErr, if non-nil, is returned as the error from SynthesizeStream
	// instead of starting a channel.
	SynthesizeErr error

	// ListVoicesResult is returned by ListVoices.
	ListVoicesResult []types.VoiceProfile

	// ListVoicesErr, if non-nil, is returned as the error from ListVoices.
	ListVoicesErr error

	// CloneVoiceResult is returned by CloneVoice. May be nil.
	CloneVoiceResult *types.VoiceProfile

	// CloneVoiceErr, if non-nil, is returned as the error from CloneVoice.
	CloneVoiceErr error

	// --- Call records ---

	// SynthesizeStreamCalls records every call to SynthesizeStream in order.
	SynthesizeStreamCalls []SynthesizeStreamCall

	// ListVoicesCalls records every call to ListVoices in order.
	ListVoicesCalls []ListVoicesCall

	// CloneVoiceCalls records every call to CloneVoice in order.
	CloneVoiceCalls []CloneVoiceCall
}

// SynthesizeStream records the call and, if SynthesizeErr is nil, returns a
// channel that emits SynthesizeChunks then closes.
func (p *Provider) SynthesizeStream(ctx context.Context, text <-chan string, voice types.VoiceProfile) (<-chan []byte, error) {
	p.mu.Lock()
	if p.SynthesizeErr != nil {
		err := p.SynthesizeErr
		p.SynthesizeStreamCalls = append(p.SynthesizeStreamCalls, SynthesizeStreamCall{Ctx: ctx, Text: text, Voice: voice})
		p.mu.Unlock()
		return nil, err
	}
	chunks := make([][]byte, len(p.SynthesizeChunks))
	copy(chunks, p.SynthesizeChunks)
	p.SynthesizeStreamCalls = append(p.SynthesizeStreamCalls, SynthesizeStreamCall{Ctx: ctx, Text: text, Voice: voice})
	p.mu.Unlock()

	ch := make(chan []byte, len(chunks))
	go func() {
		defer close(ch)
		// Drain the incoming text channel to simulate real behaviour and avoid
		// leaving the caller's goroutine blocked writing to it.
		go func() {
			for range text {
			}
		}()
		for _, audio := range chunks {
			select {
			case <-ctx.Done():
				return
			case ch <- audio:
			}
		}
	}()
	return ch, nil
}

// ListVoices records the call and returns ListVoicesResult, ListVoicesErr.
func (p *Provider) ListVoices(ctx context.Context) ([]types.VoiceProfile, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ListVoicesCalls = append(p.ListVoicesCalls, ListVoicesCall{Ctx: ctx})
	return p.ListVoicesResult, p.ListVoicesErr
}

// CloneVoice records the call and returns CloneVoiceResult, CloneVoiceErr.
func (p *Provider) CloneVoice(ctx context.Context, samples [][]byte) (*types.VoiceProfile, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	samplesCopy := make([][]byte, len(samples))
	copy(samplesCopy, samples)
	p.CloneVoiceCalls = append(p.CloneVoiceCalls, CloneVoiceCall{Ctx: ctx, Samples: samplesCopy})
	return p.CloneVoiceResult, p.CloneVoiceErr
}

// Reset clears all recorded calls. Thread-safe.
func (p *Provider) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.SynthesizeStreamCalls = nil
	p.ListVoicesCalls = nil
	p.CloneVoiceCalls = nil
}

// Ensure Provider implements tts.Provider at compile time.
var _ tts.Provider = (*Provider)(nil)
