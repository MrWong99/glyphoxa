package whisper_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/MrWong99/glyphoxa/pkg/provider/stt"
	"github.com/MrWong99/glyphoxa/pkg/provider/stt/whisper"
)

// testModelPath returns the path to a whisper model for integration tests.
// It reads from the WHISPER_MODEL_PATH environment variable. If unset the
// test is skipped.
func testModelPath(t *testing.T) string {
	t.Helper()
	p := os.Getenv("WHISPER_MODEL_PATH")
	if p == "" {
		t.Skip("WHISPER_MODEL_PATH not set; skipping native whisper test")
	}
	return p
}

func TestNewNative_EmptyPath_ReturnsError(t *testing.T) {
	_, err := whisper.NewNative("")
	if err == nil {
		t.Fatal("expected error for empty model path, got nil")
	}
}

func TestNewNative_InvalidPath_ReturnsError(t *testing.T) {
	_, err := whisper.NewNative("/nonexistent/path/to/model.bin")
	if err == nil {
		t.Fatal("expected error for invalid model path, got nil")
	}
}

func TestNewNative_WithOptions_DoesNotError(t *testing.T) {
	modelPath := testModelPath(t)
	p, err := whisper.NewNative(modelPath,
		whisper.WithNativeLanguage("en"),
		whisper.WithNativeSampleRate(16000),
		whisper.WithNativeSilenceThresholdMs(300),
		whisper.WithNativeMaxBufferDurationMs(5000),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer p.Close()
	if p == nil {
		t.Fatal("expected non-nil NativeProvider")
	}
}

func TestNativeStartStream_ReturnsNonNilHandle(t *testing.T) {
	modelPath := testModelPath(t)
	p, err := whisper.NewNative(modelPath)
	if err != nil {
		t.Fatalf("NewNative: %v", err)
	}
	defer p.Close()

	h, err := p.StartStream(context.Background(), stt.StreamConfig{SampleRate: 16000, Channels: 1})
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	defer h.Close()

	if h == nil {
		t.Fatal("StartStream returned nil handle")
	}
	if h.Partials() == nil {
		t.Error("Partials() returned nil channel")
	}
	if h.Finals() == nil {
		t.Error("Finals() returned nil channel")
	}
}

func TestNativeStartStream_CancelledContext_ReturnsError(t *testing.T) {
	modelPath := testModelPath(t)
	p, err := whisper.NewNative(modelPath)
	if err != nil {
		t.Fatalf("NewNative: %v", err)
	}
	defer p.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = p.StartStream(ctx, stt.StreamConfig{SampleRate: 16000, Channels: 1})
	if err == nil {
		t.Fatal("expected error for cancelled context, got nil")
	}
}

func TestNativeSetKeywords_ReturnsError(t *testing.T) {
	modelPath := testModelPath(t)
	p, err := whisper.NewNative(modelPath)
	if err != nil {
		t.Fatalf("NewNative: %v", err)
	}
	defer p.Close()

	h, err := p.StartStream(context.Background(), stt.StreamConfig{SampleRate: 16000, Channels: 1})
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	defer h.Close()

	if err := h.SetKeywords([]stt.KeywordBoost{{Keyword: "test", Boost: 5}}); err == nil {
		t.Fatal("expected error from SetKeywords, got nil")
	}
}

func TestNativeSilenceAloneDoesNotTriggerTranscript(t *testing.T) {
	modelPath := testModelPath(t)
	p, err := whisper.NewNative(modelPath,
		whisper.WithNativeSilenceThresholdMs(50),
		whisper.WithNativeSampleRate(16000),
	)
	if err != nil {
		t.Fatalf("NewNative: %v", err)
	}
	defer p.Close()

	h, err := p.StartStream(context.Background(), stt.StreamConfig{SampleRate: 16000, Channels: 1})
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}

	_ = h.SendAudio(makeSilencePCM(16000))
	time.Sleep(150 * time.Millisecond)
	h.Close()

	select {
	case tr, ok := <-h.Finals():
		if ok {
			t.Errorf("unexpected transcript for silence-only audio: %q", tr.Text)
		}
	default:
	}
}

func TestNativeSpeechFollowedBySilenceTriggersTranscript(t *testing.T) {
	modelPath := testModelPath(t)
	p, err := whisper.NewNative(modelPath,
		whisper.WithNativeLanguage("en"),
		whisper.WithNativeSilenceThresholdMs(100),
		whisper.WithNativeSampleRate(16000),
	)
	if err != nil {
		t.Fatalf("NewNative: %v", err)
	}
	defer p.Close()

	h, err := p.StartStream(context.Background(), stt.StreamConfig{SampleRate: 16000, Channels: 1})
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	defer h.Close()

	// Send speech followed by silence.
	if err := h.SendAudio(makeSpeechPCM(1600)); err != nil {
		t.Fatalf("SendAudio (speech): %v", err)
	}
	if err := h.SendAudio(makeSilencePCM(1600)); err != nil {
		t.Fatalf("SendAudio (silence): %v", err)
	}

	// We expect a transcript (the content depends on the model, so we just
	// verify that something was emitted).
	select {
	case tr := <-h.Finals():
		if !tr.IsFinal {
			t.Error("Finals() transcript should have IsFinal = true")
		}
		t.Logf("transcribed text: %q", tr.Text)
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for final transcript")
	}
}

func TestNativeClose_Idempotent(t *testing.T) {
	modelPath := testModelPath(t)
	p, err := whisper.NewNative(modelPath)
	if err != nil {
		t.Fatalf("NewNative: %v", err)
	}
	defer p.Close()

	h, err := p.StartStream(context.Background(), stt.StreamConfig{SampleRate: 16000, Channels: 1})
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}

	if err := h.Close(); err != nil {
		t.Fatalf("first Close() returned error: %v", err)
	}
	if err := h.Close(); err != nil {
		t.Fatalf("second Close() returned error: %v", err)
	}
}

func TestNativeSendAudio_AfterClose_ReturnsError(t *testing.T) {
	modelPath := testModelPath(t)
	p, err := whisper.NewNative(modelPath)
	if err != nil {
		t.Fatalf("NewNative: %v", err)
	}
	defer p.Close()

	h, err := p.StartStream(context.Background(), stt.StreamConfig{SampleRate: 16000, Channels: 1})
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	h.Close()

	time.Sleep(50 * time.Millisecond)
	if err := h.SendAudio(makeSpeechPCM(100)); err == nil {
		t.Fatal("SendAudio after Close() should return an error")
	}
}

func TestNativeClose_ClosesChannels(t *testing.T) {
	modelPath := testModelPath(t)
	p, err := whisper.NewNative(modelPath)
	if err != nil {
		t.Fatalf("NewNative: %v", err)
	}
	defer p.Close()

	h, err := p.StartStream(context.Background(), stt.StreamConfig{SampleRate: 16000, Channels: 1})
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	h.Close()

	select {
	case _, open := <-h.Partials():
		if open {
			t.Error("Partials channel should be closed after Close()")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Partials channel to close")
	}

	select {
	case _, open := <-h.Finals():
		if open {
			t.Error("Finals channel should be closed after Close()")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Finals channel to close")
	}
}
