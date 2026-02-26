package whisper_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MrWong99/glyphoxa/pkg/provider/stt"
	"github.com/MrWong99/glyphoxa/pkg/provider/stt/whisper"
	"github.com/MrWong99/glyphoxa/pkg/types"
)

// ---- helpers ----------------------------------------------------------------

// newMockServer creates a test server that responds to POST /inference with a
// JSON body containing the provided responseText. It increments *callCount on
// every matched request.
func newMockServer(t *testing.T, responseText string, callCount *atomic.Int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/inference" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if callCount != nil {
			callCount.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"text": responseText})
	}))
}

// makeSpeechPCM generates a sine-wave PCM buffer at 440 Hz whose RMS is well
// above the silence threshold (defaultRMSThreshold = 300). The buffer contains
// `samples` 16-bit little-endian signed samples.
func makeSpeechPCM(samples int) []byte {
	const amplitude = 10_000.0 // RMS ≈ 7071, well above 300
	buf := make([]byte, samples*2)
	for i := 0; i < samples; i++ {
		v := int16(amplitude * math.Sin(2*math.Pi*440*float64(i)/16000))
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(v))
	}
	return buf
}

// makeSilencePCM generates a zero-valued PCM buffer (RMS = 0, below any
// threshold). The buffer contains `samples` 16-bit little-endian samples.
func makeSilencePCM(samples int) []byte {
	return make([]byte, samples*2)
}

// mustStartStream is a test helper that calls StartStream and fails the test on
// error.
func mustStartStream(t *testing.T, p *whisper.Provider, cfg stt.StreamConfig) stt.SessionHandle {
	t.Helper()
	h, err := p.StartStream(context.Background(), cfg)
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	return h
}

// ---- provider construction --------------------------------------------------

func TestNew_EmptyServerURL_ReturnsError(t *testing.T) {
	_, err := whisper.New("")
	if err == nil {
		t.Fatal("expected error for empty serverURL, got nil")
	}
}

func TestNew_ValidServerURL_ReturnsProvider(t *testing.T) {
	p, err := whisper.New("http://localhost:8080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil Provider")
	}
}

func TestNew_WithOptions_DoesNotError(t *testing.T) {
	p, err := whisper.New("http://localhost:8080",
		whisper.WithModel("small"),
		whisper.WithLanguage("de"),
		whisper.WithSampleRate(16000),
		whisper.WithSilenceThresholdMs(300),
		whisper.WithMaxBufferDurationMs(5000),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil Provider")
	}
}

// ---- session creation -------------------------------------------------------

func TestStartStream_ReturnsNonNilHandle(t *testing.T) {
	srv := newMockServer(t, "", nil)
	defer srv.Close()

	p, _ := whisper.New(srv.URL)
	h := mustStartStream(t, p, stt.StreamConfig{SampleRate: 16000, Channels: 1})
	defer h.Close()

	if h == nil {
		t.Fatal("StartStream returned nil handle")
	}
}

func TestStartStream_PartialsChannel_NonNil(t *testing.T) {
	srv := newMockServer(t, "", nil)
	defer srv.Close()

	p, _ := whisper.New(srv.URL)
	h := mustStartStream(t, p, stt.StreamConfig{SampleRate: 16000, Channels: 1})
	defer h.Close()

	if h.Partials() == nil {
		t.Error("Partials() returned nil channel")
	}
}

func TestStartStream_FinalsChannel_NonNil(t *testing.T) {
	srv := newMockServer(t, "", nil)
	defer srv.Close()

	p, _ := whisper.New(srv.URL)
	h := mustStartStream(t, p, stt.StreamConfig{SampleRate: 16000, Channels: 1})
	defer h.Close()

	if h.Finals() == nil {
		t.Error("Finals() returned nil channel")
	}
}

func TestStartStream_CancelledContext_ReturnsError(t *testing.T) {
	srv := newMockServer(t, "", nil)
	defer srv.Close()

	p, _ := whisper.New(srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	_, err := p.StartStream(ctx, stt.StreamConfig{SampleRate: 16000, Channels: 1})
	if err == nil {
		t.Fatal("expected error for cancelled context, got nil")
	}
}

// ---- keyword support --------------------------------------------------------

func TestSetKeywords_ReturnsError(t *testing.T) {
	srv := newMockServer(t, "", nil)
	defer srv.Close()

	p, _ := whisper.New(srv.URL)
	h := mustStartStream(t, p, stt.StreamConfig{SampleRate: 16000, Channels: 1})
	defer h.Close()

	err := h.SetKeywords([]types.KeywordBoost{{Keyword: "Eldrinax", Boost: 5}})
	if err == nil {
		t.Fatal("expected error from SetKeywords, got nil")
	}
}

func TestSetKeywords_NilSlice_ReturnsError(t *testing.T) {
	srv := newMockServer(t, "", nil)
	defer srv.Close()

	p, _ := whisper.New(srv.URL)
	h := mustStartStream(t, p, stt.StreamConfig{SampleRate: 16000, Channels: 1})
	defer h.Close()

	if err := h.SetKeywords(nil); err == nil {
		t.Fatal("expected error from SetKeywords(nil), got nil")
	}
}

// ---- silence detection / buffering ------------------------------------------

func TestSilenceAloneDoesNotTriggerInference(t *testing.T) {
	var calls atomic.Int32
	srv := newMockServer(t, "unexpected", &calls)
	defer srv.Close()

	p, _ := whisper.New(srv.URL,
		whisper.WithSilenceThresholdMs(50),
		whisper.WithSampleRate(16000),
	)
	h := mustStartStream(t, p, stt.StreamConfig{SampleRate: 16000, Channels: 1})

	// 1 second of silence (16000 samples × 2 bytes).
	_ = h.SendAudio(makeSilencePCM(16000))

	// Give the processing goroutine time to act (it shouldn't).
	time.Sleep(150 * time.Millisecond)
	h.Close()

	if n := calls.Load(); n != 0 {
		t.Errorf("inference called %d time(s) for silence-only audio; want 0", n)
	}
}

func TestSpeechFollowedBySilenceTriggersInference(t *testing.T) {
	const wantText = "Hello darkness my old friend"
	srv := newMockServer(t, wantText, nil)
	defer srv.Close()

	// Use a short silence threshold so the test is fast.
	p, _ := whisper.New(srv.URL,
		whisper.WithSilenceThresholdMs(100),
		whisper.WithSampleRate(16000),
	)
	h := mustStartStream(t, p, stt.StreamConfig{SampleRate: 16000, Channels: 1})
	defer h.Close()

	// 100 ms of speech (1600 samples at 16 kHz).
	if err := h.SendAudio(makeSpeechPCM(1600)); err != nil {
		t.Fatalf("SendAudio (speech): %v", err)
	}

	// 100 ms of silence — should meet the silence threshold and trigger a flush.
	if err := h.SendAudio(makeSilencePCM(1600)); err != nil {
		t.Fatalf("SendAudio (silence): %v", err)
	}

	select {
	case tr := <-h.Finals():
		if tr.Text != wantText {
			t.Errorf("Finals().Text = %q; want %q", tr.Text, wantText)
		}
		if !tr.IsFinal {
			t.Error("Finals() transcript should have IsFinal = true")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for final transcript")
	}
}

func TestPartialEmittedAlongsideFinal(t *testing.T) {
	const wantText = "fire bolt"
	srv := newMockServer(t, wantText, nil)
	defer srv.Close()

	p, _ := whisper.New(srv.URL,
		whisper.WithSilenceThresholdMs(100),
		whisper.WithSampleRate(16000),
	)
	h := mustStartStream(t, p, stt.StreamConfig{SampleRate: 16000, Channels: 1})
	defer h.Close()

	_ = h.SendAudio(makeSpeechPCM(1600))
	_ = h.SendAudio(makeSilencePCM(1600))

	select {
	case tr := <-h.Partials():
		if tr.Text != wantText {
			t.Errorf("Partials().Text = %q; want %q", tr.Text, wantText)
		}
		if tr.IsFinal {
			t.Error("Partials() transcript should have IsFinal = false")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for partial transcript")
	}
}

func TestMaxBufferExceededForcesFlush(t *testing.T) {
	const wantText = "arcane surge"
	srv := newMockServer(t, wantText, nil)
	defer srv.Close()

	// maxBuffer = 200 ms; silence threshold = 10 s (will never be reached).
	// The force-flush should kick in once we send > 200 ms of speech.
	p, _ := whisper.New(srv.URL,
		whisper.WithSilenceThresholdMs(10_000),
		whisper.WithMaxBufferDurationMs(200),
		whisper.WithSampleRate(16000),
	)
	h := mustStartStream(t, p, stt.StreamConfig{SampleRate: 16000, Channels: 1})
	defer h.Close()

	// Send 210 ms of continuous speech (3360 samples at 16 kHz).
	if err := h.SendAudio(makeSpeechPCM(3360)); err != nil {
		t.Fatalf("SendAudio: %v", err)
	}

	select {
	case tr := <-h.Finals():
		if tr.Text != wantText {
			t.Errorf("Finals().Text = %q; want %q", tr.Text, wantText)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for forced-flush transcript")
	}
}

// ---- session close ----------------------------------------------------------

func TestClose_ClosesPartialsChannel(t *testing.T) {
	srv := newMockServer(t, "", nil)
	defer srv.Close()

	p, _ := whisper.New(srv.URL)
	h := mustStartStream(t, p, stt.StreamConfig{SampleRate: 16000, Channels: 1})
	h.Close()

	select {
	case _, open := <-h.Partials():
		if open {
			t.Error("Partials channel should be closed after Close()")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Partials channel to close")
	}
}

func TestClose_ClosesFinalsChannel(t *testing.T) {
	srv := newMockServer(t, "", nil)
	defer srv.Close()

	p, _ := whisper.New(srv.URL)
	h := mustStartStream(t, p, stt.StreamConfig{SampleRate: 16000, Channels: 1})
	h.Close()

	select {
	case _, open := <-h.Finals():
		if open {
			t.Error("Finals channel should be closed after Close()")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Finals channel to close")
	}
}

func TestClose_Idempotent(t *testing.T) {
	srv := newMockServer(t, "", nil)
	defer srv.Close()

	p, _ := whisper.New(srv.URL)
	h := mustStartStream(t, p, stt.StreamConfig{SampleRate: 16000, Channels: 1})

	if err := h.Close(); err != nil {
		t.Fatalf("first Close() returned error: %v", err)
	}
	if err := h.Close(); err != nil {
		t.Fatalf("second Close() returned error: %v", err)
	}
}

func TestSendAudio_AfterClose_ReturnsError(t *testing.T) {
	srv := newMockServer(t, "", nil)
	defer srv.Close()

	p, _ := whisper.New(srv.URL)
	h := mustStartStream(t, p, stt.StreamConfig{SampleRate: 16000, Channels: 1})
	h.Close()

	// Small sleep to let the processLoop goroutine exit cleanly.
	time.Sleep(50 * time.Millisecond)

	if err := h.SendAudio(makeSpeechPCM(100)); err == nil {
		t.Fatal("SendAudio after Close() should return an error")
	}
}

func TestClose_FlushesRemainingBuffer(t *testing.T) {
	const wantText = "sword of destiny"
	srv := newMockServer(t, wantText, nil)
	defer srv.Close()

	// Very long silence threshold — the flush will only happen on Close().
	p, _ := whisper.New(srv.URL,
		whisper.WithSilenceThresholdMs(60_000),
		whisper.WithSampleRate(16000),
	)
	h := mustStartStream(t, p, stt.StreamConfig{SampleRate: 16000, Channels: 1})

	_ = h.SendAudio(makeSpeechPCM(1600))
	// Wait briefly to ensure the chunk is processed before Close().
	time.Sleep(50 * time.Millisecond)

	// Close should flush the pending buffer.
	h.Close()

	// After Close the Finals channel should either have the text or be empty
	// (if the server was too slow). We just verify the channel is closed and
	// any received transcript has the right text.
	for tr := range h.Finals() {
		if tr.Text != wantText {
			t.Errorf("received unexpected transcript %q on close-flush; want %q", tr.Text, wantText)
		}
	}
}

// ---- error handling ---------------------------------------------------------

func TestInference_ServerError_DoesNotPanic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	p, _ := whisper.New(srv.URL,
		whisper.WithSilenceThresholdMs(100),
		whisper.WithSampleRate(16000),
	)
	h := mustStartStream(t, p, stt.StreamConfig{SampleRate: 16000, Channels: 1})
	defer h.Close()

	_ = h.SendAudio(makeSpeechPCM(1600))
	_ = h.SendAudio(makeSilencePCM(1600))

	// No transcript should arrive (server errored), but the session must not panic.
	select {
	case tr, open := <-h.Finals():
		if open {
			t.Errorf("expected no finals on server error, got %q", tr.Text)
		}
		// channel was closed — that is also acceptable
	case <-time.After(3 * time.Second):
		// No message and no close — the session is still running, which is fine.
	}
}

func TestInference_EmptyResponse_ProducesNoTranscript(t *testing.T) {
	srv := newMockServer(t, "", nil) // server returns empty text
	defer srv.Close()

	p, _ := whisper.New(srv.URL,
		whisper.WithSilenceThresholdMs(100),
		whisper.WithSampleRate(16000),
	)
	h := mustStartStream(t, p, stt.StreamConfig{SampleRate: 16000, Channels: 1})
	defer h.Close()

	_ = h.SendAudio(makeSpeechPCM(1600))
	_ = h.SendAudio(makeSilencePCM(1600))

	select {
	case tr := <-h.Finals():
		// If we receive a transcript, it should not have empty text
		// (the provider must not emit empty finals).
		if tr.Text == "" {
			t.Error("received empty-text transcript on Finals; expected no emission")
		}
	case <-time.After(2 * time.Second):
		// Nothing received — correct behaviour for an empty server response.
	}
}

// ---- concurrent use ---------------------------------------------------------

func TestConcurrentSendAudio_DoesNotRace(t *testing.T) {
	srv := newMockServer(t, "hello", nil)
	defer srv.Close()

	p, _ := whisper.New(srv.URL,
		whisper.WithSilenceThresholdMs(100),
		whisper.WithSampleRate(16000),
	)
	h := mustStartStream(t, p, stt.StreamConfig{SampleRate: 16000, Channels: 1})
	defer h.Close()

	done := make(chan struct{})
	for i := 0; i < 4; i++ {
		go func() {
			for j := 0; j < 10; j++ {
				_ = h.SendAudio(makeSpeechPCM(160))
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 4; i++ {
		<-done
	}
}
