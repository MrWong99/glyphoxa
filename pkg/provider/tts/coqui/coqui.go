// Package coqui provides a local Coqui TTS-backed TTS provider that connects to
// either a Coqui XTTS v2 server or a standard Coqui TTS server via its REST API.
// It implements the tts.Provider interface.
//
// Two API modes are supported:
//
//   - APIModeStandard (default): targets the standard Coqui TTS server
//     (ghcr.io/coqui-ai/tts-cpu). Synthesis is performed via GET /api/tts with
//     URL query parameters; voice catalogue is retrieved from GET /details.
//
//   - APIModeXTTS: targets the Coqui XTTS v2 API server. Synthesis is performed
//     via POST /tts_to_audio/ with a JSON body; voice catalogue is retrieved from
//     GET /studio_speakers; voice cloning is available via POST /clone_speaker.
//
// Because both servers operate in batch mode (one HTTP call per utterance rather
// than a streaming socket), SynthesizeStream accumulates incoming text fragments
// into complete sentences and then dispatches concurrent HTTP requests with a
// small lookahead buffer to minimise perceived latency.
//
// Typical usage (standard server):
//
//	p := coqui.New("http://localhost:5002",
//	    coqui.WithLanguage("en"),
//	    coqui.WithTimeout(15*time.Second),
//	    // APIModeStandard is the default; this line is optional:
//	    coqui.WithAPIMode(coqui.APIModeStandard),
//	)
//	audio, err := p.SynthesizeStream(ctx, textCh, voiceProfile)
//
// Typical usage (XTTS v2 server):
//
//	p := coqui.New("http://localhost:8002",
//	    coqui.WithLanguage("en"),
//	    coqui.WithAPIMode(coqui.APIModeXTTS),
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
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/MrWong99/glyphoxa/pkg/provider/tts"
)

// Compile-time interface assertion.
var _ tts.Provider = (*Provider)(nil)

// ---- constants ----

const (
	defaultLanguage        = "en"
	defaultTimeout         = 30 * time.Second
	ttsEndpoint            = "/tts_to_audio/"
	studioSpeakersEndpoint = "/studio_speakers"
	cloneSpeakerEndpoint   = "/clone_speaker"
	apiTTSEndpoint         = "/api/tts"
	detailsEndpoint        = "/details"

	// sentenceLookaheadBuf controls how many concurrent HTTP synthesis requests
	// may be in-flight simultaneously. Higher values reduce perceived latency at
	// the cost of additional server load.
	sentenceLookaheadBuf = 4

	// audioChanBuf is the buffer depth of the returned audio channel.
	audioChanBuf = 256

	// pcmChunkSize is the size of each PCM chunk emitted on the audio channel.
	pcmChunkSize = 4096
)

// ---- APIMode ----

// APIMode selects which Coqui server API the provider will target.
type APIMode string

const (
	// APIModeXTTS targets the Coqui XTTS v2 API server (/tts_to_audio/).
	// It supports voice cloning via /clone_speaker and voice listing via
	// /studio_speakers.
	APIModeXTTS APIMode = "xtts"

	// APIModeStandard targets the standard Coqui TTS server (/api/tts).
	// This is the default mode. Voice listing is performed via /details.
	// Voice cloning is not supported in this mode.
	APIModeStandard APIMode = "standard"
)

// ---- options ----

// Option is a functional option for configuring a Coqui Provider.
type Option func(*Provider)

// WithLanguage sets the BCP-47 language code sent to the TTS server (e.g., "en",
// "de", "fr"). Defaults to "en" if not set.
func WithLanguage(lang string) Option {
	return func(p *Provider) {
		p.language = lang
	}
}

// WithTimeout sets the per-request HTTP timeout for calls to the TTS server.
// Defaults to 30 s if not set.
func WithTimeout(d time.Duration) Option {
	return func(p *Provider) {
		p.httpClient.Timeout = d
	}
}

// WithAPIMode sets the server API mode. Use APIModeStandard (default) for the
// standard Coqui TTS Docker image (ghcr.io/coqui-ai/tts-cpu) or APIModeXTTS for
// the XTTS v2 API server.
func WithAPIMode(mode APIMode) Option {
	return func(p *Provider) {
		p.apiMode = mode
	}
}

// WithOutputSampleRate configures the provider to resample synthesised PCM to
// the given sample rate (e.g., 48000 for Discord). When set to 0 (default),
// no resampling is performed and PCM is emitted at the model's native rate.
func WithOutputSampleRate(rate int) Option {
	return func(p *Provider) {
		p.outputRate = rate
	}
}

// ---- Provider ----

// Provider implements tts.Provider backed by a locally-running Coqui TTS server.
// It is safe for concurrent use; multiple SynthesizeStream calls may run in parallel.
type Provider struct {
	serverURL  string
	language   string
	httpClient *http.Client
	apiMode    APIMode
	outputRate int // target sample rate; 0 = no resampling
}

// New creates a new Coqui Provider that targets the TTS server at serverURL
// (e.g., "http://localhost:5002"). serverURL must be non-empty. Functional
// options may override the language, per-request timeout, and API mode.
// The default API mode is APIModeStandard.
func New(serverURL string, opts ...Option) (*Provider, error) {
	if serverURL == "" {
		return nil, errors.New("coqui: serverURL must not be empty")
	}
	p := &Provider{
		serverURL: strings.TrimRight(serverURL, "/"),
		language:  defaultLanguage,
		apiMode:   APIModeStandard,
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

// ttsRequest is the JSON body sent to POST /tts_to_audio/ (XTTS mode).
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

// detailsResponse is the JSON body returned by GET /details (standard mode).
// Speakers is nil for single-speaker models and non-nil for multi-speaker models.
type detailsResponse struct {
	ModelName string   `json:"model_name"`
	Language  string   `json:"language"`
	Speakers  []string `json:"speakers"`
}

// ---- SynthesizeStream ----

// SynthesizeStream consumes text fragments from the text channel, accumulates them
// into complete sentences (split on '.', '!', '?' followed by whitespace or EOF),
// and for each sentence issues an HTTP synthesis request to the Coqui server.
// WAV responses are stripped of their file headers and the raw PCM is emitted on
// the returned channel in the original sentence order.
//
// Up to sentenceLookaheadBuf HTTP requests may be in-flight concurrently to hide
// network/server latency while preserving output ordering.
//
// The returned channel is closed when all text has been synthesised or when ctx
// is cancelled. The caller must drain the channel to prevent goroutine leaks.
func (p *Provider) SynthesizeStream(ctx context.Context, text <-chan string, voice tts.VoiceProfile) (<-chan []byte, error) {
	// XTTS mode always requires a voice ID (speaker_wav). Standard mode works
	// without one for single-speaker models, so only enforce the check for XTTS.
	if voice.ID == "" && p.apiMode == APIModeXTTS {
		return nil, errors.New("coqui: voice.ID must not be empty (required for XTTS mode)")
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
						end := min(pcmChunkSize, len(pcm))
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

// synthesize dispatches to the appropriate implementation based on the configured
// API mode.
func (p *Provider) synthesize(ctx context.Context, sentence string, voice tts.VoiceProfile) ([]byte, error) {
	if p.apiMode == APIModeStandard {
		return p.synthesizeStandard(ctx, sentence, voice)
	}
	return p.synthesizeXTTS(ctx, sentence, voice)
}

// synthesizeXTTS performs a single POST /tts_to_audio/ call (XTTS v2 mode) and
// returns the raw PCM (WAV header stripped).
func (p *Provider) synthesizeXTTS(ctx context.Context, sentence string, voice tts.VoiceProfile) ([]byte, error) {
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

	info, err := parseWAV(wav)
	if err != nil {
		return nil, err
	}

	pcm := wav[info.DataOffset:]
	if p.outputRate > 0 && info.SampleRate != p.outputRate && info.Channels == 1 {
		pcm = resampleMono16(pcm, info.SampleRate, p.outputRate)
	}
	return pcm, nil
}

// synthesizeStandard performs a single GET /api/tts request (standard server mode)
// using URL query parameters and returns the raw PCM (WAV header stripped).
func (p *Provider) synthesizeStandard(ctx context.Context, sentence string, voice tts.VoiceProfile) ([]byte, error) {
	params := url.Values{}
	params.Set("text", sentence)
	if voice.ID != "" {
		params.Set("speaker_id", voice.ID)
	}
	if p.language != "" {
		params.Set("language_id", p.language)
	}

	reqURL := p.serverURL + apiTTSEndpoint + "?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("coqui: create tts request: %w", err)
	}
	req.Header.Set("Accept", "audio/wav")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("coqui: GET %s: %w", apiTTSEndpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("coqui: GET %s returned status %d", apiTTSEndpoint, resp.StatusCode)
	}

	wav, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("coqui: read WAV response: %w", err)
	}

	info, err := parseWAV(wav)
	if err != nil {
		return nil, err
	}

	pcm := wav[info.DataOffset:]
	if p.outputRate > 0 && info.SampleRate != p.outputRate && info.Channels == 1 {
		pcm = resampleMono16(pcm, info.SampleRate, p.outputRate)
	}
	return pcm, nil
}

// ---- ListVoices ----

// ListVoices retrieves the list of available voices from the Coqui server.
//
// In APIModeXTTS, it calls GET /studio_speakers and maps each entry to a
// VoiceProfile. In APIModeStandard, it calls GET /details and returns one
// VoiceProfile per speaker for multi-speaker models, or a single VoiceProfile
// (identified by model name) for single-speaker models.
func (p *Provider) ListVoices(ctx context.Context) ([]tts.VoiceProfile, error) {
	if p.apiMode == APIModeStandard {
		return p.listVoicesStandard(ctx)
	}
	return p.listVoicesXTTS(ctx)
}

// listVoicesXTTS retrieves the list of studio speaker voices from the XTTS server via
// GET /studio_speakers and maps each entry to a VoiceProfile.
func (p *Provider) listVoicesXTTS(ctx context.Context) ([]tts.VoiceProfile, error) {
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

	profiles := make([]tts.VoiceProfile, 0, len(names))
	for _, name := range names {
		profiles = append(profiles, tts.VoiceProfile{
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

// listVoicesStandard retrieves model info from the standard Coqui TTS server via
// GET /details. For multi-speaker models it returns one VoiceProfile per speaker;
// for single-speaker models it returns a single VoiceProfile identified by the
// model name.
func (p *Provider) listVoicesStandard(ctx context.Context) ([]tts.VoiceProfile, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.serverURL+detailsEndpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("coqui: create list-voices request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("coqui: GET %s: %w", detailsEndpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("coqui: GET %s returned status %d", detailsEndpoint, resp.StatusCode)
	}

	var details detailsResponse
	if err := json.NewDecoder(resp.Body).Decode(&details); err != nil {
		return nil, fmt.Errorf("coqui: decode details response: %w", err)
	}

	// Multi-speaker model: return one profile per speaker.
	if len(details.Speakers) > 0 {
		// Sort for deterministic output.
		speakers := make([]string, len(details.Speakers))
		copy(speakers, details.Speakers)
		sort.Strings(speakers)

		profiles := make([]tts.VoiceProfile, 0, len(speakers))
		for _, spk := range speakers {
			profiles = append(profiles, tts.VoiceProfile{
				ID:       spk,
				Name:     spk,
				Provider: "coqui",
				Metadata: map[string]string{
					"type":       "speaker",
					"model_name": details.ModelName,
				},
			})
		}
		return profiles, nil
	}

	// Single-speaker model: return one profile identified by the model name.
	name := details.ModelName
	if name == "" {
		name = "default"
	}
	return []tts.VoiceProfile{
		{
			ID:       name,
			Name:     name,
			Provider: "coqui",
			Metadata: map[string]string{
				"type":       "single-speaker",
				"model_name": name,
			},
		},
	}, nil
}

// ---- CloneVoice ----

// CloneVoice creates a new speaker voice by uploading WAV audio samples to the
// XTTS server via POST /clone_speaker. Each element of samples must be a valid
// WAV-encoded audio file.
//
// Voice cloning is only supported in APIModeXTTS. In APIModeStandard, this method
// always returns an error.
//
// Returns a VoiceProfile for the cloned voice or an error if the request fails.
// A nil or empty samples slice returns an error rather than sending an empty request.
func (p *Provider) CloneVoice(ctx context.Context, samples [][]byte) (*tts.VoiceProfile, error) {
	if p.apiMode == APIModeStandard {
		return nil, errors.New("coqui: voice cloning is not supported in standard API mode")
	}

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

	return &tts.VoiceProfile{
		ID:       cloneResp.Name,
		Name:     cloneResp.Name,
		Provider: "coqui",
		Metadata: map[string]string{
			"type": "cloned",
		},
	}, nil
}

// ---- resampling ----

// resampleMono16 resamples 16-bit mono PCM from srcRate to dstRate using linear
// interpolation. The input must be little-endian int16 samples. If srcRate ==
// dstRate, the input is returned unchanged.
func resampleMono16(pcm []byte, srcRate, dstRate int) []byte {
	if srcRate == dstRate || len(pcm) < 2 {
		return pcm
	}
	srcSamples := len(pcm) / 2
	dstSamples := int(int64(srcSamples) * int64(dstRate) / int64(srcRate))
	if dstSamples == 0 {
		return nil
	}

	out := make([]byte, dstSamples*2)
	ratio := float64(srcRate) / float64(dstRate)

	for i := 0; i < dstSamples; i++ {
		srcPos := float64(i) * ratio
		srcIdx := int(srcPos)
		frac := srcPos - float64(srcIdx)

		s0 := int16(pcm[srcIdx*2]) | int16(pcm[srcIdx*2+1])<<8
		var s1 int16
		if srcIdx+1 < srcSamples {
			s1 = int16(pcm[(srcIdx+1)*2]) | int16(pcm[(srcIdx+1)*2+1])<<8
		} else {
			s1 = s0
		}

		interpolated := int16(float64(s0)*(1-frac) + float64(s1)*frac)
		out[i*2] = byte(interpolated)
		out[i*2+1] = byte(interpolated >> 8)
	}
	return out
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

// wavInfo holds the format metadata extracted from a RIFF/WAVE header.
type wavInfo struct {
	DataOffset int // byte offset of the first PCM sample
	SampleRate int // samples per second (e.g., 22050, 44100, 48000)
	Channels   int // 1 = mono, 2 = stereo
}

// parseWAV scans the RIFF/WAVE container in wav and returns the data offset
// and audio format from the "fmt " sub-chunk. This is more robust than
// hardcoding a fixed 44-byte offset because the fmt chunk size may vary.
//
// Returns an error if wav is not a valid RIFF/WAVE container or if the fmt
// or data chunk cannot be located.
func parseWAV(wav []byte) (wavInfo, error) {
	if len(wav) < 12 {
		return wavInfo{}, errors.New("coqui: WAV response too short to be a valid RIFF file")
	}
	if string(wav[0:4]) != "RIFF" {
		return wavInfo{}, errors.New("coqui: WAV response missing RIFF header")
	}
	if string(wav[8:12]) != "WAVE" {
		return wavInfo{}, errors.New("coqui: WAV response missing WAVE identifier")
	}

	var info wavInfo
	foundFmt := false

	// Walk RIFF chunks starting immediately after the 12-byte RIFF/WAVE header.
	offset := 12
	for offset+8 <= len(wav) {
		chunkID := string(wav[offset : offset+4])
		chunkSize := int(binary.LittleEndian.Uint32(wav[offset+4 : offset+8]))

		switch chunkID {
		case "fmt ":
			if chunkSize >= 16 && offset+8+16 <= len(wav) {
				fmtData := wav[offset+8:]
				info.Channels = int(binary.LittleEndian.Uint16(fmtData[2:4]))
				info.SampleRate = int(binary.LittleEndian.Uint32(fmtData[4:8]))
				foundFmt = true
			}
		case "data":
			info.DataOffset = offset + 8
			if !foundFmt {
				// fmt chunk should appear before data, but be defensive.
				info.SampleRate = 22050
				info.Channels = 1
			}
			return info, nil
		}

		// Advance past this chunk (chunks are word-aligned: pad by 1 if odd size).
		offset += 8 + chunkSize
		if chunkSize%2 != 0 {
			offset++
		}
	}
	return wavInfo{}, errors.New("coqui: WAV response missing data chunk")
}

// findWAVDataOffset is a convenience wrapper around parseWAV that returns only
// the data offset. Retained for backward compatibility with tests.
func findWAVDataOffset(wav []byte) (int, error) {
	info, err := parseWAV(wav)
	if err != nil {
		return 0, err
	}
	return info.DataOffset, nil
}
