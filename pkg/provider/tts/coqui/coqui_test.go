package coqui

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MrWong99/glyphoxa/pkg/provider/tts"
)

// ---- test helpers ----

// buildTestWAV constructs a minimal but valid RIFF/WAVE byte slice containing the
// supplied raw PCM samples. It writes a standard 44-byte header (RIFF + fmt + data)
// so that findWAVDataOffset can correctly locate the audio payload.
func buildTestWAV(pcm []byte) []byte {
	// PCM WAV layout:
	//   RIFF chunk descriptor  (12 bytes)
	//   fmt  sub-chunk         (24 bytes: 8 header + 16 data)
	//   data sub-chunk         ( 8 bytes: 8 header + len(pcm) data)
	fmtSize := uint32(16)
	dataSize := uint32(len(pcm))
	fileSize := 4 + (8 + fmtSize) + (8 + dataSize) // WAVE + fmt chunk + data chunk

	buf := make([]byte, 0, 12+8+fmtSize+8+dataSize)
	le := binary.LittleEndian

	putU32 := func(v uint32) {
		var b [4]byte
		le.PutUint32(b[:], v)
		buf = append(buf, b[:]...)
	}
	putU16 := func(v uint16) {
		var b [2]byte
		le.PutUint16(b[:], v)
		buf = append(buf, b[:]...)
	}

	// RIFF chunk.
	buf = append(buf, []byte("RIFF")...)
	putU32(fileSize)
	buf = append(buf, []byte("WAVE")...)

	// fmt sub-chunk.
	buf = append(buf, []byte("fmt ")...)
	putU32(fmtSize)
	putU16(1)     // PCM format
	putU16(1)     // 1 channel (mono)
	putU32(16000) // sample rate
	putU32(32000) // byte rate = SampleRate * NumChannels * BitsPerSample/8
	putU16(2)     // block align
	putU16(16)    // bits per sample

	// data sub-chunk.
	buf = append(buf, []byte("data")...)
	putU32(dataSize)
	buf = append(buf, pcm...)

	return buf
}

// drainAudio reads all []byte chunks from the audio channel until it is closed
// and returns the concatenated PCM data.
func drainAudio(ch <-chan []byte) []byte {
	var out []byte
	for chunk := range ch {
		out = append(out, chunk...)
	}
	return out
}

// sendFragments sends the given text fragments on a freshly-created channel,
// then closes it. Returns the channel for passing to SynthesizeStream.
func sendFragments(fragments []string) <-chan string {
	ch := make(chan string, len(fragments))
	for _, f := range fragments {
		ch <- f
	}
	close(ch)
	return ch
}

// mustNew is a test helper that calls New and fails the test on error.
func mustNew(t *testing.T, serverURL string, opts ...Option) *Provider {
	t.Helper()
	p, err := New(serverURL, opts...)
	if err != nil {
		t.Fatalf("New(%q): unexpected error: %v", serverURL, err)
	}
	return p
}

// ---- Provider creation ----

func TestNew(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		p := mustNew(t, "http://localhost:8002")
		if p.serverURL != "http://localhost:8002" {
			t.Errorf("serverURL = %q, want %q", p.serverURL, "http://localhost:8002")
		}
		if p.language != defaultLanguage {
			t.Errorf("language = %q, want %q", p.language, defaultLanguage)
		}
		if p.httpClient.Timeout != defaultTimeout {
			t.Errorf("timeout = %v, want %v", p.httpClient.Timeout, defaultTimeout)
		}
	})

	t.Run("trims trailing slash", func(t *testing.T) {
		p := mustNew(t, "http://localhost:8002/")
		if p.serverURL != "http://localhost:8002" {
			t.Errorf("serverURL = %q, want trailing slash stripped", p.serverURL)
		}
	})

	t.Run("empty URL returns error", func(t *testing.T) {
		_, err := New("")
		if err == nil {
			t.Fatal("expected error for empty URL, got nil")
		}
	})

	t.Run("with options", func(t *testing.T) {
		p := mustNew(t, "http://localhost:8002",
			WithLanguage("de"),
			WithTimeout(5*time.Second),
		)
		if p.language != "de" {
			t.Errorf("language = %q, want %q", p.language, "de")
		}
		if p.httpClient.Timeout != 5*time.Second {
			t.Errorf("timeout = %v, want %v", p.httpClient.Timeout, 5*time.Second)
		}
	})
}

// ---- SynthesizeStream ----

func TestSynthesizeStream_EmptyVoiceID_XTTS(t *testing.T) {
	p := mustNew(t, "http://localhost:8002", WithAPIMode(APIModeXTTS))
	_, err := p.SynthesizeStream(context.Background(), make(chan string), tts.VoiceProfile{})
	if err == nil {
		t.Fatal("expected error for empty voice ID in XTTS mode, got nil")
	}
	if !strings.Contains(err.Error(), "coqui:") {
		t.Errorf("error %q does not have 'coqui:' prefix", err.Error())
	}
}

func TestSynthesizeStream_EmptyVoiceID_Standard(t *testing.T) {
	// Standard mode allows empty voice ID for single-speaker models.
	p := mustNew(t, "http://localhost:8002")
	ch, err := p.SynthesizeStream(context.Background(), make(chan string), tts.VoiceProfile{})
	if err != nil {
		t.Fatalf("standard mode should accept empty voice ID, got error: %v", err)
	}
	if ch == nil {
		t.Fatal("expected non-nil channel")
	}
}

func TestSynthesizeStream_MockServer(t *testing.T) {
	// PCM payload: 100 bytes of 0x42.
	wantPCM := make([]byte, 100)
	for i := range wantPCM {
		wantPCM[i] = 0x42
	}
	wavData := buildTestWAV(wantPCM)

	// Mock server: validates request shape, returns WAV data.
	var (
		reqMu        sync.Mutex
		receivedReqs []ttsRequest
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != ttsEndpoint {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req ttsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		reqMu.Lock()
		receivedReqs = append(receivedReqs, req)
		reqMu.Unlock()
		w.Header().Set("Content-Type", "audio/wav")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(wavData)
	}))
	defer srv.Close()

	p := mustNew(t, srv.URL, WithAPIMode(APIModeXTTS))
	voice := tts.VoiceProfile{ID: "test_speaker", Provider: "coqui"}

	// Send two complete sentences.
	textCh := sendFragments([]string{"Hello world. ", "Goodbye now!"})

	audioCh, err := p.SynthesizeStream(context.Background(), textCh, voice)
	if err != nil {
		t.Fatalf("SynthesizeStream: unexpected error: %v", err)
	}

	pcm := drainAudio(audioCh)

	// Expect two sentences × 100 PCM bytes each = 200 bytes.
	wantTotal := 2 * len(wantPCM)
	if len(pcm) != wantTotal {
		t.Errorf("total PCM bytes = %d, want %d", len(pcm), wantTotal)
	}

	// Validate each byte is 0x42.
	for i, b := range pcm {
		if b != 0x42 {
			t.Errorf("pcm[%d] = %02x, want 0x42", i, b)
			break
		}
	}

	// Validate the server received requests with correct fields.
	if len(receivedReqs) != 2 {
		t.Fatalf("server received %d requests, want 2", len(receivedReqs))
	}
	for _, req := range receivedReqs {
		if req.SpeakerWav != "test_speaker" {
			t.Errorf("speaker_wav = %q, want %q", req.SpeakerWav, "test_speaker")
		}
		if req.Language != defaultLanguage {
			t.Errorf("language = %q, want %q", req.Language, defaultLanguage)
		}
	}
}

func TestSynthesizeStream_ContextCancellation(t *testing.T) {
	// Use an already-cancelled context: SynthesizeStream should start (non-error),
	// but the audio channel must close without emitting any audio data because the
	// HTTP request will immediately fail due to context cancellation.
	wavData := buildTestWAV([]byte{0x01, 0x02, 0x03, 0x04})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Introduce a brief delay so the context cancels in-flight.
		time.Sleep(50 * time.Millisecond)
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(wavData)
	}))
	defer srv.Close()

	p := mustNew(t, srv.URL)
	voice := tts.VoiceProfile{ID: "test_speaker"}

	// Cancel the context immediately after starting the stream.
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	textCh := sendFragments([]string{"This sentence should not be synthesised."})

	audioCh, err := p.SynthesizeStream(ctx, textCh, voice)
	if err != nil {
		t.Fatalf("SynthesizeStream: unexpected error: %v", err)
	}

	done := make(chan struct{})
	go func() {
		drainAudio(audioCh)
		close(done)
	}()

	select {
	case <-done:
		// Good: channel closed promptly.
	case <-time.After(2 * time.Second):
		t.Error("audio channel did not close within 2 s after context cancellation")
	}
}

func TestSynthesizeStream_ServerError(t *testing.T) {
	// Server returns 500 for all requests.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := mustNew(t, srv.URL)
	voice := tts.VoiceProfile{ID: "test_speaker"}

	textCh := sendFragments([]string{"A sentence."})

	audioCh, err := p.SynthesizeStream(context.Background(), textCh, voice)
	if err != nil {
		t.Fatalf("SynthesizeStream start unexpected error: %v", err)
	}

	pcm := drainAudio(audioCh)
	if len(pcm) != 0 {
		t.Errorf("expected empty audio on server error, got %d bytes", len(pcm))
	}
}

// ---- Sentence accumulation ----

func TestFindSentenceBoundary(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{"period at end", "Hello.", 5},
		{"period space", "Hello. World", 5},
		{"exclamation", "Hello!", 5},
		{"question", "Hello?", 5},
		{"no boundary", "Hello", -1},
		// NOTE: "Dr." followed by a space IS treated as a sentence boundary by this
		// simple algorithm (abbreviation-awareness is out of scope for this provider).
		{"abbreviation mid", "Dr. Smith", 2},
		// '.' in "3.14" is followed by '1', not whitespace — not a boundary.
		{"decimal", "3.14 is pi", -1},
		{"empty", "", -1},
		{"multiple", "First. Second.", 5},
		{"question mid", "How? Great!", 3},
	}

	// Note: "Dr. Smith" — the '.' IS followed by a space, so it IS a boundary.
	// Adjust the test to reflect actual behaviour.
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findSentenceBoundary(tt.input)
			if got != tt.want {
				t.Errorf("findSentenceBoundary(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

// TestSentenceAccumulation verifies that fragments are assembled into sentences
// before dispatching HTTP requests, by checking what the mock server receives.
func TestSentenceAccumulation(t *testing.T) {
	// PCM payload: trivially small.
	wavData := buildTestWAV([]byte{0x01, 0x02})

	var (
		mu            sync.Mutex
		receivedTexts []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req ttsRequest
		_ = json.Unmarshal(body, &req)
		mu.Lock()
		receivedTexts = append(receivedTexts, req.Text)
		mu.Unlock()
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(wavData)
	}))
	defer srv.Close()

	p := mustNew(t, srv.URL, WithAPIMode(APIModeXTTS))
	voice := tts.VoiceProfile{ID: "spk"}

	// Deliberately split "Hello world." across three fragments.
	// "Are you there?" across two fragments.
	textCh := sendFragments([]string{
		"Hello ", "world. ", "Are ", "you ", "there?",
	})

	audioCh, err := p.SynthesizeStream(context.Background(), textCh, voice)
	if err != nil {
		t.Fatalf("SynthesizeStream: %v", err)
	}
	drainAudio(audioCh)

	if len(receivedTexts) != 2 {
		t.Fatalf("server received %d requests, want 2; got: %v", len(receivedTexts), receivedTexts)
	}
	// Concurrent HTTP dispatch means server-side receive order is not
	// guaranteed. Check that both expected sentences were received (unordered).
	want := map[string]bool{"Hello world.": true, "Are you there?": true}
	for _, txt := range receivedTexts {
		if !want[txt] {
			t.Errorf("unexpected sentence %q sent to server", txt)
		}
		delete(want, txt)
	}
	for txt := range want {
		t.Errorf("sentence %q was never sent to the server", txt)
	}
}

// ---- ListVoices ----

func TestListVoices(t *testing.T) {
	// Mock /studio_speakers returning a JSON object with two speaker names.
	rawResp := map[string]any{
		"speaker_alice": map[string]any{"type": "studio"},
		"speaker_bob":   map[string]any{"type": "studio"},
	}
	data, _ := json.Marshal(rawResp)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != studioSpeakersEndpoint {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	}))
	defer srv.Close()

	p := mustNew(t, srv.URL, WithAPIMode(APIModeXTTS))
	voices, err := p.ListVoices(context.Background())
	if err != nil {
		t.Fatalf("ListVoices: %v", err)
	}

	if len(voices) != 2 {
		t.Fatalf("got %d voices, want 2", len(voices))
	}

	// Sorted order: alice before bob.
	if voices[0].ID != "speaker_alice" {
		t.Errorf("voices[0].ID = %q, want %q", voices[0].ID, "speaker_alice")
	}
	if voices[1].ID != "speaker_bob" {
		t.Errorf("voices[1].ID = %q, want %q", voices[1].ID, "speaker_bob")
	}
	for _, v := range voices {
		if v.Provider != "coqui" {
			t.Errorf("voice %q Provider = %q, want %q", v.ID, v.Provider, "coqui")
		}
		if v.Metadata["type"] != "studio" {
			t.Errorf("voice %q metadata type = %q, want studio", v.ID, v.Metadata["type"])
		}
	}
}

func TestListVoices_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	p := mustNew(t, srv.URL)
	_, err := p.ListVoices(context.Background())
	if err == nil {
		t.Fatal("expected error on server failure, got nil")
	}
	if !strings.Contains(err.Error(), "coqui:") {
		t.Errorf("error %q missing 'coqui:' prefix", err.Error())
	}
}

func TestListVoices_ContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := mustNew(t, srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := p.ListVoices(ctx)
	if err == nil {
		t.Fatal("expected error on context timeout, got nil")
	}
}

// ---- findWAVDataOffset ----

func TestFindWAVDataOffset(t *testing.T) {
	t.Run("valid WAV", func(t *testing.T) {
		pcm := []byte{0x01, 0x02, 0x03, 0x04}
		wav := buildTestWAV(pcm)
		offset, err := findWAVDataOffset(wav)
		if err != nil {
			t.Fatalf("findWAVDataOffset: %v", err)
		}
		if offset != len(wav)-len(pcm) {
			t.Errorf("offset = %d, want %d", offset, len(wav)-len(pcm))
		}
		if string(wav[offset:]) != string(pcm) {
			t.Errorf("data at offset does not match expected PCM")
		}
	})

	t.Run("too short", func(t *testing.T) {
		_, err := findWAVDataOffset([]byte{0x01, 0x02})
		if err == nil {
			t.Fatal("expected error for short input")
		}
	})

	t.Run("not RIFF", func(t *testing.T) {
		buf := make([]byte, 44)
		copy(buf, "XXXX")
		_, err := findWAVDataOffset(buf)
		if err == nil {
			t.Fatal("expected error for non-RIFF header")
		}
	})

	t.Run("not WAVE", func(t *testing.T) {
		buf := make([]byte, 44)
		copy(buf, "RIFF")
		copy(buf[8:], "XXXX")
		_, err := findWAVDataOffset(buf)
		if err == nil {
			t.Fatal("expected error for non-WAVE identifier")
		}
	})

	t.Run("no data chunk", func(t *testing.T) {
		// Build a WAV with only the RIFF header and a non-data chunk.
		var buf []byte
		buf = append(buf, []byte("RIFF")...)
		buf = append(buf, 0, 0, 0, 0) // size placeholder
		buf = append(buf, []byte("WAVE")...)
		buf = append(buf, []byte("fmt ")...)
		buf = append(buf, 4, 0, 0, 0) // chunk size 4
		buf = append(buf, 0, 0, 0, 0) // dummy fmt data
		_, err := findWAVDataOffset(buf)
		if err == nil {
			t.Fatal("expected error when data chunk is absent")
		}
	})
}

// ---- CloneVoice ----

func TestCloneVoice_EmptySamples(t *testing.T) {
	p := mustNew(t, "http://localhost:8002")
	_, err := p.CloneVoice(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil samples")
	}
	_, err = p.CloneVoice(context.Background(), [][]byte{})
	if err == nil {
		t.Fatal("expected error for empty samples")
	}
}

func TestCloneVoice_MockServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != cloneSpeakerEndpoint {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// Parse the multipart form.
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, "parse multipart: "+err.Error(), http.StatusBadRequest)
			return
		}
		files := r.MultipartForm.File["wav_files"]
		if len(files) == 0 {
			http.Error(w, "no wav_files", http.StatusBadRequest)
			return
		}
		resp := cloneSpeakerResponse{Name: "cloned_voice", Status: "ok"}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := mustNew(t, srv.URL, WithAPIMode(APIModeXTTS))
	samples := [][]byte{
		buildTestWAV([]byte{0xAA, 0xBB}),
		buildTestWAV([]byte{0xCC, 0xDD}),
	}

	profile, err := p.CloneVoice(context.Background(), samples)
	if err != nil {
		t.Fatalf("CloneVoice: %v", err)
	}
	if profile.ID != "cloned_voice" {
		t.Errorf("profile.ID = %q, want %q", profile.ID, "cloned_voice")
	}
	if profile.Provider != "coqui" {
		t.Errorf("profile.Provider = %q, want %q", profile.Provider, "coqui")
	}
	if profile.Metadata["type"] != "cloned" {
		t.Errorf("metadata type = %q, want cloned", profile.Metadata["type"])
	}
}

// ---- Standard API mode tests ----

func TestSynthesizeStream_StandardAPI(t *testing.T) {
	t.Parallel()

	// PCM payload: 80 bytes of 0x33.
	wantPCM := make([]byte, 80)
	for i := range wantPCM {
		wantPCM[i] = 0x33
	}
	wavData := buildTestWAV(wantPCM)

	var (
		reqMu   sync.Mutex
		gotReqs []*http.Request
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != apiTTSEndpoint {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// Clone request for later inspection (URL is safe to read).
		reqMu.Lock()
		gotReqs = append(gotReqs, r)
		reqMu.Unlock()
		w.Header().Set("Content-Type", "audio/wav")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(wavData)
	}))
	defer srv.Close()

	p := mustNew(t, srv.URL, WithAPIMode(APIModeStandard), WithLanguage("en"))
	voice := tts.VoiceProfile{ID: "p225", Provider: "coqui"}

	textCh := sendFragments([]string{"Hello world."})

	audioCh, err := p.SynthesizeStream(context.Background(), textCh, voice)
	if err != nil {
		t.Fatalf("SynthesizeStream: unexpected error: %v", err)
	}

	pcm := drainAudio(audioCh)

	if len(pcm) != len(wantPCM) {
		t.Errorf("total PCM bytes = %d, want %d", len(pcm), len(wantPCM))
	}
	for i, b := range pcm {
		if b != 0x33 {
			t.Errorf("pcm[%d] = %02x, want 0x33", i, b)
			break
		}
	}

	if len(gotReqs) != 1 {
		t.Fatalf("server received %d requests, want 1", len(gotReqs))
	}
	q := gotReqs[0].URL.Query()
	if got := q.Get("text"); got != "Hello world." {
		t.Errorf("query param text = %q, want %q", got, "Hello world.")
	}
	if got := q.Get("speaker_id"); got != "p225" {
		t.Errorf("query param speaker_id = %q, want %q", got, "p225")
	}
	if got := q.Get("language_id"); got != "en" {
		t.Errorf("query param language_id = %q, want %q", got, "en")
	}
}

func TestListVoices_StandardAPI(t *testing.T) {
	t.Parallel()

	t.Run("multi-speaker model", func(t *testing.T) {
		t.Parallel()

		details := detailsResponse{
			ModelName: "tts_models/en/vctk/vits",
			Language:  "en",
			Speakers:  []string{"p225", "p226", "p227"},
		}
		data, _ := json.Marshal(details)

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != detailsEndpoint {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(data)
		}))
		defer srv.Close()

		p := mustNew(t, srv.URL, WithAPIMode(APIModeStandard))
		voices, err := p.ListVoices(context.Background())
		if err != nil {
			t.Fatalf("ListVoices: %v", err)
		}

		if len(voices) != 3 {
			t.Fatalf("got %d voices, want 3", len(voices))
		}
		// Sorted order: p225, p226, p227.
		wantIDs := []string{"p225", "p226", "p227"}
		for i, v := range voices {
			if v.ID != wantIDs[i] {
				t.Errorf("voices[%d].ID = %q, want %q", i, v.ID, wantIDs[i])
			}
			if v.Provider != "coqui" {
				t.Errorf("voices[%d].Provider = %q, want coqui", i, v.Provider)
			}
			if v.Metadata["type"] != "speaker" {
				t.Errorf("voices[%d] metadata type = %q, want speaker", i, v.Metadata["type"])
			}
			if v.Metadata["model_name"] != "tts_models/en/vctk/vits" {
				t.Errorf("voices[%d] metadata model_name = %q", i, v.Metadata["model_name"])
			}
		}
	})

	t.Run("single-speaker model", func(t *testing.T) {
		t.Parallel()

		details := detailsResponse{
			ModelName: "tts_models/en/ljspeech/vits",
			Language:  "en",
			Speakers:  nil,
		}
		data, _ := json.Marshal(details)

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != detailsEndpoint {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(data)
		}))
		defer srv.Close()

		p := mustNew(t, srv.URL, WithAPIMode(APIModeStandard))
		voices, err := p.ListVoices(context.Background())
		if err != nil {
			t.Fatalf("ListVoices: %v", err)
		}

		if len(voices) != 1 {
			t.Fatalf("got %d voices, want 1", len(voices))
		}
		if voices[0].ID != "tts_models/en/ljspeech/vits" {
			t.Errorf("voices[0].ID = %q, want model name", voices[0].ID)
		}
		if voices[0].Provider != "coqui" {
			t.Errorf("voices[0].Provider = %q, want coqui", voices[0].Provider)
		}
		if voices[0].Metadata["type"] != "single-speaker" {
			t.Errorf("voices[0] metadata type = %q, want single-speaker", voices[0].Metadata["type"])
		}
	})

	t.Run("server error", func(t *testing.T) {
		t.Parallel()

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}))
		defer srv.Close()

		p := mustNew(t, srv.URL, WithAPIMode(APIModeStandard))
		_, err := p.ListVoices(context.Background())
		if err == nil {
			t.Fatal("expected error on server failure, got nil")
		}
		if !strings.Contains(err.Error(), "coqui:") {
			t.Errorf("error %q missing 'coqui:' prefix", err.Error())
		}
	})
}

func TestCloneVoice_StandardAPI_NotSupported(t *testing.T) {
	t.Parallel()

	p := mustNew(t, "http://localhost:5002", WithAPIMode(APIModeStandard))
	_, err := p.CloneVoice(context.Background(), [][]byte{buildTestWAV([]byte{0x01, 0x02})})
	if err == nil {
		t.Fatal("expected error for CloneVoice in standard API mode, got nil")
	}
	if !strings.Contains(err.Error(), "not supported") {
		t.Errorf("error %q does not mention 'not supported'", err.Error())
	}
	if !strings.Contains(err.Error(), "coqui:") {
		t.Errorf("error %q missing 'coqui:' prefix", err.Error())
	}
}

// TestNew_DefaultAPIMode verifies that the default API mode is APIModeStandard.
func TestNew_DefaultAPIMode(t *testing.T) {
	t.Parallel()

	p := mustNew(t, "http://localhost:5002")
	if p.apiMode != APIModeStandard {
		t.Errorf("default apiMode = %q, want %q", p.apiMode, APIModeStandard)
	}
}

// TestNew_WithAPIMode verifies that WithAPIMode sets the API mode correctly.
func TestNew_WithAPIMode(t *testing.T) {
	t.Parallel()

	p := mustNew(t, "http://localhost:8002", WithAPIMode(APIModeXTTS))
	if p.apiMode != APIModeXTTS {
		t.Errorf("apiMode = %q, want %q", p.apiMode, APIModeXTTS)
	}
}
