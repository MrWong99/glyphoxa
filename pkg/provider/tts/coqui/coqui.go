// Package coqui provides a local Coqui XTTS-backed TTS provider that connects to
// a Coqui XTTS v2 server via its REST API. It implements the tts.Provider interface.
//
// The Coqui XTTS v2 server is a local HTTP server that exposes a simple REST API
// for batch speech synthesis. Because XTTS operates in batch mode (one HTTP call
// per utterance rather than a streaming socket), SynthesizeStream accumulates
// incoming text fragments into complete sentences and then dispatches concurrent
// HTTP requests with a small lookahead buffer to minimise perceived latency.
//
// Typical usage:
//
//	p := coqui.New("http://localhost:8002",
//	    coqui.WithLanguage("en"),
//	    coqui.WithTimeout(15*time.Second),
//	)
//	audio, err := p.SynthesizeStream(ctx, textCh, voiceProfile)
package coqui

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/MrWong99/glyphoxa/pkg/provider/tts"
	"github.com/MrWong99/glyphoxa/pkg/types"
)

// Compile-time interface assertion.
var _ tts.Provider = (*Provider)(nil)

// ---- constants ----

const (
	defaultLanguage       = "en"
	defaultTimeout        = 30 * time.Second
	ttsEndpoint           = "/tts_to_audio/"
	studioSpeakersEndpoint = "/studio_speakers"
	cloneSpeakerEndpoint  = "/clone_speaker"

	// sentenceLookaheadBuf controls how many concurrent HTTP synthesis requests
	// may be in-flight simultaneously. Higher values reduce perceived latency at
	// the cost of additional server load.
	sentenceLookaheadBuf = 4

	// audioChanBuf is the buffer depth of the returned audio channel.
	audioChanBuf = 256

	// pcmChunkSize is the size of each PCM chunk emitted on the audio channel.
	pcmChunkSize = 4096
)

// ---- options ----

// Option is a functional option for configuring a Coqui Provider.
type Option func(*Provider)

// WithLanguage sets the BCP-47 language code sent to XTTS (e.g., "en", "de", "fr").
// Defaults to "en" if not set.
func WithLanguage(lang string) Option {
	return func(p *Provider) {
		p.language = lang
	}
}

// WithTimeout sets the per-request HTTP timeout for calls to the XTTS server.
// Defaults to 30 s if not set.
func WithTimeout(d time.Duration) Option {
	return func(p *Provider) {
		p.httpClient.Timeout = d
	}
}

// ---- Provider ----

// Provider implements tts.Provider backed by a locally-running Coqui XTTS v2 server.
// It is safe for concurrent use; multiple SynthesizeStream calls may run in parallel.
type Provider struct {
	serverURL  string
	language   string
	httpClient *http.Client
}

// New creates a new Coqui Provider that targets the XTTS server at serverURL
// (e.g., "http://localhost:8002"). serverURL must be non-empty. Functional
// options may override the language and per-request timeout.
func New(serverURL string, opts ...Option) (*Provider, error) {
	if serverURL == "" {
		return nil, errors.New("coqui: serverURL must not be empty")
	}
	p := &Provider{
		serverURL: strings.TrimRight(serverURL, "/"),
		language:  defaultLanguage,
		httpClient: &http.Client{
			Timeout: defaultTimeout,
		},
	}
	for _, o := range opts {
		o(p)
	}
	return p, nil
}

// ---- internal request/response types ----

// ttsRequest is the JSON body sent to POST /tts_to_audio/.
type ttsRequest struct {
	Text       string `json:"text"`
	SpeakerWav string `json:"speaker_wav"`
	Language   string `json:"language"`
}

// audioResult carries a synthesised PCM byte slice or an error from a worker goroutine.
type audioResult struct {
	pcm []byte
	err error
}

// studioSpeakersResponse represents the raw map[name]any returned by GET /studio_speakers.
// We only care about the keys (voice names) so the values are left as json.RawMessage.
type studioSpeakersResponse map[string]json.RawMessage

// cloneSpeakerResponse is the JSON body returned by POST /clone_speaker.
type cloneSpeakerResponse struct {
	Name   string `json:"name"`
	Status string `json:"status,omitempty"`
}

// ---- SynthesizeStream ----

// SynthesizeStream consumes text fragments from the text channel, accumulates them
// into complete sentences (split on '.', '!', '?' followed by whitespace or EOF),
// and for each sentence issues a POST /tts_to_audio/ request to the XTTS server.
// WAV responses are stripped of their file headers and the raw PCM is emitted on
// the returned channel in the original sentence order.
//
// Up to sentenceLookaheadBuf HTTP requests may be in-flight concurrently to hide
// network/server latency while preserving output ordering.
//
// The returned channel is closed when all text has been synthesised or when ctx
// is cancelled. The caller must drain the channel to prevent goroutine leaks.
func (p *Provider) SynthesizeStream(ctx context.Context, text <-chan string, voice types.VoiceProfile) (<-chan []byte, error) {
	if voice.ID == "" {
		return nil, errors.New("coqui: voice.ID must not be empty")
	}

	audioCh := make(chan []byte, audioChanBuf)

	go func() {
		defer close(audioCh)

		// sentences carries complete sentences from the accumulator to the dispatcher.
		sentences := make(chan string, sentenceLookaheadBuf)

		// resultQueue carries ordered future channels so the collector can drain in order.
		resultQueue := make(chan chan audioResult, sentenceLookaheadBuf)

		// --- Accumulator goroutine ---
		// Reads text fragments, buffers them, and emits complete sentences.
		go func() {
			defer close(sentences)
			var buf strings.Builder
			for {
				select {
				case fragment, ok := <-text:
					if !ok {
						// Text channel closed: flush any remaining partial sentence.
						if remaining := strings.TrimSpace(buf.String()); remaining != "" {
							select {
							case sentences <- remaining:
							case <-ctx.Done():
							}
						}
						return
					}
					buf.WriteString(fragment)
					// Drain all complete sentences from the buffer.
					for {
						s := buf.String()
						idx := findSentenceBoundary(s)
						if idx < 0 {
							break
						}
						sentence := strings.TrimSpace(s[:idx+1])
						buf.Reset()
						buf.WriteString(s[idx+1:])
						if sentence == "" {
							continue
						}
						select {
						case sentences <- sentence:
						case <-ctx.Done():
							return
						}
					}
				case <-ctx.Done():
					return
				}
			}
		}()

		// --- Dispatcher goroutine ---
		// Reads sentences and launches a concurrent HTTP request for each, placing
		// an ordered result channel into resultQueue so the collector can drain in order.
		go func() {
			defer close(resultQueue)
			for {
				select {
				case sentence, ok := <-sentences:
					if !ok {
						return
					}
					ch := make(chan audioResult, 1)
					select {
					case resultQueue <- ch:
					case <-ctx.Done():
						return
					}
					// Launch the HTTP call in its own goroutine.
					go func(s string, out chan<- audioResult) {
						pcm, err := p.synthesize(ctx, s, voice)
						out <- audioResult{pcm: pcm, err: err}
					}(sentence, ch)
				case <-ctx.Done():
					return
				}
			}
		}()

		// --- Collector ---
		// Drains resultQueue in-order and emits PCM chunks to the audio channel.
		for {
			select {
			case ch, ok := <-resultQueue:
				if !ok {
					return
				}
				select {
				case result := <-ch:
					if result.err != nil {
						// On synthesis error we stop the stream. The caller can
						// inspect ctx.Err() to distinguish cancellation from provider errors.
						return
					}
					// Emit the PCM in fixed-size chunks.
					pcm := result.pcm
					for len(pcm) > 0 {
						end := pcmChunkSize
						if end > len(pcm) {
							end = len(pcm)
						}
						select {
						case audioCh <- pcm[:end]:
						case <-ctx.Done():
							return
						}
						pcm = pcm[end:]
					}
				case <-ctx.Done():
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	return audioCh, nil
}

// synthesize performs a single POST /tts_to_audio/ call and returns the raw PCM
// (WAV header stripped). It is called concurrently from dispatcher goroutines.
func (p *Provider) synthesize(ctx context.Context, sentence string, voice types.VoiceProfile) ([]byte, error) {
	body := ttsRequest{
		Text:       sentence,
		SpeakerWav: voice.ID,
		Language:   p.language,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("coqui: marshal tts request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.serverURL+ttsEndpoint, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("coqui: create tts request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "audio/wav")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("coqui: POST %s: %w", ttsEndpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("coqui: POST %s returned status %d", ttsEndpoint, resp.StatusCode)
	}

	wav, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("coqui: read WAV response: %w", err)
	}

	offset, err := findWAVDataOffset(wav)
	if err != nil {
		return nil, err
	}

	return wav[offset:], nil
}

// ---- ListVoices ----

// ListVoices retrieves the list of studio speaker voices from the XTTS server via
// GET /studio_speakers and maps each entry to a VoiceProfile.
//
// The XTTS server returns a JSON object whose keys are speaker names. Each voice
// profile's ID and Name are set to the speaker name, and Provider is set to "coqui".
func (p *Provider) ListVoices(ctx context.Context) ([]types.VoiceProfile, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.serverURL+studioSpeakersEndpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("coqui: create list-voices request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("coqui: GET %s: %w", studioSpeakersEndpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("coqui: GET %s returned status %d", studioSpeakersEndpoint, resp.StatusCode)
	}

	var raw studioSpeakersResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("coqui: decode studio speakers: %w", err)
	}

	// Sort keys for deterministic output.
	names := make([]string, 0, len(raw))
	for name := range raw {
		names = append(names, name)
	}
	sort.Strings(names)

	profiles := make([]types.VoiceProfile, 0, len(names))
	for _, name := range names {
		profiles = append(profiles, types.VoiceProfile{
			ID:       name,
			Name:     name,
			Provider: "coqui",
			Metadata: map[string]string{
				"type": "studio",
			},
		})
	}
	return profiles, nil
}

// ---- CloneVoice ----

// CloneVoice creates a new speaker voice by uploading WAV audio samples to the
// XTTS server via POST /clone_speaker. Each element of samples must be a valid
// WAV-encoded audio file.
//
// Returns a VoiceProfile for the cloned voice or an error if the request fails.
// A nil or empty samples slice returns an error rather than sending an empty request.
func (p *Provider) CloneVoice(ctx context.Context, samples [][]byte) (*types.VoiceProfile, error) {
	if len(samples) == 0 {
		return nil, errors.New("coqui: CloneVoice requires at least one audio sample")
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)

	for i, sample := range samples {
		filename := fmt.Sprintf("sample_%02d.wav", i)
		fw, err := mw.CreateFormFile("wav_files", filepath.Base(filename))
		if err != nil {
			return nil, fmt.Errorf("coqui: create form file %s: %w", filename, err)
		}
		if _, err := fw.Write(sample); err != nil {
			return nil, fmt.Errorf("coqui: write form file %s: %w", filename, err)
		}
	}

	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("coqui: close multipart writer: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.serverURL+cloneSpeakerEndpoint, &body)
	if err != nil {
		return nil, fmt.Errorf("coqui: create clone-speaker request: %w", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("coqui: POST %s: %w", cloneSpeakerEndpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("coqui: POST %s returned status %d", cloneSpeakerEndpoint, resp.StatusCode)
	}

	var cloneResp cloneSpeakerResponse
	if err := json.NewDecoder(resp.Body).Decode(&cloneResp); err != nil {
		return nil, fmt.Errorf("coqui: decode clone-speaker response: %w", err)
	}

	if cloneResp.Name == "" {
		return nil, errors.New("coqui: clone-speaker response missing name")
	}

	return &types.VoiceProfile{
		ID:       cloneResp.Name,
		Name:     cloneResp.Name,
		Provider: "coqui",
		Metadata: map[string]string{
			"type": "cloned",
		},
	}, nil
}

// ---- helpers ----

// findSentenceBoundary returns the index of the first sentence-ending character
// ('.', '!', '?') that is either at the end of s or immediately followed by
// whitespace. Returns -1 if no sentence boundary is found.
//
// This ensures that abbreviations like "Dr." or decimal numbers like "3.14" are
// not incorrectly treated as sentence boundaries when followed by a non-space
// character.
func findSentenceBoundary(s string) int {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '.' || c == '!' || c == '?' {
			// Boundary: end of string or followed by whitespace.
			if i+1 >= len(s) || unicode.IsSpace(rune(s[i+1])) {
				return i
			}
		}
	}
	return -1
}

// findWAVDataOffset scans the RIFF/WAVE container in wav and returns the byte
// offset of the first sample in the "data" sub-chunk (i.e., immediately after
// the data chunk header). This is more robust than hardcoding a fixed 44-byte
// offset because the fmt chunk size may vary (e.g., when extension fields are present).
//
// Returns an error if wav is not a valid RIFF/WAVE container or if the data
// chunk cannot be located.
func findWAVDataOffset(wav []byte) (int, error) {
	if len(wav) < 12 {
		return 0, errors.New("coqui: WAV response too short to be a valid RIFF file")
	}
	if string(wav[0:4]) != "RIFF" {
		return 0, errors.New("coqui: WAV response missing RIFF header")
	}
	if string(wav[8:12]) != "WAVE" {
		return 0, errors.New("coqui: WAV response missing WAVE identifier")
	}

	// Walk RIFF chunks starting immediately after the 12-byte RIFF/WAVE header.
	offset := 12
	for offset+8 <= len(wav) {
		chunkID := string(wav[offset : offset+4])
		chunkSize := int(binary.LittleEndian.Uint32(wav[offset+4 : offset+8]))
		if chunkID == "data" {
			// Data starts immediately after the 8-byte chunk header.
			return offset + 8, nil
		}
		// Advance past this chunk (chunks are word-aligned: pad by 1 if odd size).
		offset += 8 + chunkSize
		if chunkSize%2 != 0 {
			offset++
		}
	}
	return 0, errors.New("coqui: WAV response missing data chunk")
}


