package resilience

import (
	"context"
	"errors"
	"testing"

	"github.com/MrWong99/glyphoxa/pkg/provider/stt"
	sttmock "github.com/MrWong99/glyphoxa/pkg/provider/stt/mock"
)

func TestSTTFallback_StartStream_PrimarySuccess(t *testing.T) {
	sess := &sttmock.Session{
		PartialsCh: make(chan stt.Transcript, 1),
		FinalsCh:   make(chan stt.Transcript, 1),
	}
	primary := &sttmock.Provider{Session: sess}
	secondary := &sttmock.Provider{}

	fb := NewSTTFallback(primary, "primary", FallbackConfig{
		CircuitBreaker: CircuitBreakerConfig{MaxFailures: 3},
	})
	fb.AddFallback("secondary", secondary)

	handle, err := fb.StartStream(context.Background(), stt.StreamConfig{
		SampleRate: 16000,
		Channels:   1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if handle == nil {
		t.Fatal("handle is nil")
	}
	if len(primary.StartStreamCalls) != 1 {
		t.Fatalf("primary called %d times, want 1", len(primary.StartStreamCalls))
	}
	if len(secondary.StartStreamCalls) != 0 {
		t.Fatalf("secondary called %d times, want 0", len(secondary.StartStreamCalls))
	}
	_ = handle.Close()
}

func TestSTTFallback_StartStream_Failover(t *testing.T) {
	primary := &sttmock.Provider{
		StartStreamErr: errors.New("primary down"),
	}
	secondarySess := &sttmock.Session{
		PartialsCh: make(chan stt.Transcript, 1),
		FinalsCh:   make(chan stt.Transcript, 1),
	}
	secondary := &sttmock.Provider{Session: secondarySess}

	fb := NewSTTFallback(primary, "primary", FallbackConfig{
		CircuitBreaker: CircuitBreakerConfig{MaxFailures: 3},
	})
	fb.AddFallback("secondary", secondary)

	handle, err := fb.StartStream(context.Background(), stt.StreamConfig{
		SampleRate: 16000,
		Channels:   1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if handle == nil {
		t.Fatal("handle is nil")
	}
	if len(secondary.StartStreamCalls) != 1 {
		t.Fatalf("secondary called %d times, want 1", len(secondary.StartStreamCalls))
	}
	_ = handle.Close()
}

func TestSTTFallback_StartStream_AllFail(t *testing.T) {
	primary := &sttmock.Provider{StartStreamErr: errors.New("primary down")}
	secondary := &sttmock.Provider{StartStreamErr: errors.New("secondary down")}

	fb := NewSTTFallback(primary, "primary", FallbackConfig{
		CircuitBreaker: CircuitBreakerConfig{MaxFailures: 3},
	})
	fb.AddFallback("secondary", secondary)

	_, err := fb.StartStream(context.Background(), stt.StreamConfig{})
	if !errors.Is(err, ErrAllFailed) {
		t.Fatalf("err = %v, want ErrAllFailed", err)
	}
}
