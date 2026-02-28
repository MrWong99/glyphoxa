package resilience

import (
	"context"
	"errors"
	"testing"

	"github.com/MrWong99/glyphoxa/pkg/provider/tts"
	ttsmock "github.com/MrWong99/glyphoxa/pkg/provider/tts/mock"
)

func TestTTSFallback_SynthesizeStream_PrimarySuccess(t *testing.T) {
	primary := &ttsmock.Provider{
		SynthesizeChunks: [][]byte{[]byte("audio1"), []byte("audio2")},
	}
	secondary := &ttsmock.Provider{
		SynthesizeChunks: [][]byte{[]byte("fallback-audio")},
	}

	fb := NewTTSFallback(primary, "primary", FallbackConfig{
		CircuitBreaker: CircuitBreakerConfig{MaxFailures: 3},
	})
	fb.AddFallback("secondary", secondary)

	textCh := make(chan string, 1)
	textCh <- "hello"
	close(textCh)

	audioCh, err := fb.SynthesizeStream(context.Background(), textCh, tts.VoiceProfile{
		ID:   "v1",
		Name: "TestVoice",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var chunks [][]byte
	for chunk := range audioCh {
		chunks = append(chunks, chunk)
	}
	if len(chunks) != 2 {
		t.Fatalf("got %d chunks, want 2", len(chunks))
	}
	if string(chunks[0]) != "audio1" {
		t.Fatalf("chunk[0] = %q, want audio1", string(chunks[0]))
	}
	if len(primary.SynthesizeStreamCalls) != 1 {
		t.Fatalf("primary called %d times, want 1", len(primary.SynthesizeStreamCalls))
	}
	if len(secondary.SynthesizeStreamCalls) != 0 {
		t.Fatalf("secondary called %d times, want 0", len(secondary.SynthesizeStreamCalls))
	}
}

func TestTTSFallback_SynthesizeStream_Failover(t *testing.T) {
	primary := &ttsmock.Provider{
		SynthesizeErr: errors.New("primary down"),
	}
	secondary := &ttsmock.Provider{
		SynthesizeChunks: [][]byte{[]byte("fallback-audio")},
	}

	fb := NewTTSFallback(primary, "primary", FallbackConfig{
		CircuitBreaker: CircuitBreakerConfig{MaxFailures: 3},
	})
	fb.AddFallback("secondary", secondary)

	textCh := make(chan string, 1)
	textCh <- "hello"
	close(textCh)

	audioCh, err := fb.SynthesizeStream(context.Background(), textCh, tts.VoiceProfile{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var chunks [][]byte
	for chunk := range audioCh {
		chunks = append(chunks, chunk)
	}
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1", len(chunks))
	}
	if string(chunks[0]) != "fallback-audio" {
		t.Fatalf("chunk[0] = %q, want fallback-audio", string(chunks[0]))
	}
}

func TestTTSFallback_SynthesizeStream_AllFail(t *testing.T) {
	primary := &ttsmock.Provider{SynthesizeErr: errors.New("primary down")}
	secondary := &ttsmock.Provider{SynthesizeErr: errors.New("secondary down")}

	fb := NewTTSFallback(primary, "primary", FallbackConfig{
		CircuitBreaker: CircuitBreakerConfig{MaxFailures: 3},
	})
	fb.AddFallback("secondary", secondary)

	textCh := make(chan string)
	close(textCh)

	_, err := fb.SynthesizeStream(context.Background(), textCh, tts.VoiceProfile{})
	if !errors.Is(err, ErrAllFailed) {
		t.Fatalf("err = %v, want ErrAllFailed", err)
	}
}

func TestTTSFallback_ListVoices_Failover(t *testing.T) {
	primary := &ttsmock.Provider{
		ListVoicesErr: errors.New("primary down"),
	}
	secondary := &ttsmock.Provider{
		ListVoicesResult: []tts.VoiceProfile{
			{ID: "v1", Name: "Alice"},
			{ID: "v2", Name: "Bob"},
		},
	}

	fb := NewTTSFallback(primary, "primary", FallbackConfig{
		CircuitBreaker: CircuitBreakerConfig{MaxFailures: 3},
	})
	fb.AddFallback("secondary", secondary)

	voices, err := fb.ListVoices(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(voices) != 2 {
		t.Fatalf("got %d voices, want 2", len(voices))
	}
	if voices[0].Name != "Alice" {
		t.Fatalf("voices[0].Name = %q, want Alice", voices[0].Name)
	}
}

func TestTTSFallback_CloneVoice_Failover(t *testing.T) {
	primary := &ttsmock.Provider{
		CloneVoiceErr: errors.New("primary down"),
	}
	secondary := &ttsmock.Provider{
		CloneVoiceResult: &tts.VoiceProfile{ID: "cloned-v1", Name: "ClonedVoice"},
	}

	fb := NewTTSFallback(primary, "primary", FallbackConfig{
		CircuitBreaker: CircuitBreakerConfig{MaxFailures: 3},
	})
	fb.AddFallback("secondary", secondary)

	voice, err := fb.CloneVoice(context.Background(), [][]byte{[]byte("sample-audio")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if voice.ID != "cloned-v1" {
		t.Fatalf("voice.ID = %q, want cloned-v1", voice.ID)
	}
}
