// Package whisper provides a local whisper.cpp-backed STT provider.
//
// It connects to a running whisper-server binary (which exposes a REST API at
// POST /inference) and simulates streaming behaviour by buffering incoming PCM
// audio, applying an energy-based silence detector to segment utterances, and
// submitting each completed utterance as a batch inference request.
//
// Because whisper.cpp is a batch (non-streaming) transcription engine the
// provider cannot emit true low-latency partials. Instead it emits a partial
// and a final for the same text as soon as each utterance is committed to the
// server. This is still useful for driving UI activity indicators while the
// primary Finals channel is used for session logging and LLM input.
//
// Usage:
//
//	p, err := whisper.New("http://localhost:8080",
//	    whisper.WithLanguage("en"),
//	    whisper.WithSilenceThresholdMs(500),
//	)
//	handle, err := p.StartStream(ctx, cfg)
//	handle.SendAudio(pcmChunk)
//	transcript := <-handle.Finals()
//	handle.Close()
package whisper

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"sync"
	"time"

	"github.com/MrWong99/glyphoxa/pkg/provider/stt"
	"github.com/MrWong99/glyphoxa/pkg/types"
)

const (
	// bitsPerSample is fixed at 16 for the 16-bit signed little-endian PCM
	// audio that whisper.cpp expects.
	bitsPerSample = 16

	// defaultRMSThreshold is the root-mean-square energy level (in 16-bit PCM
	// units) below which audio is considered silent. The maximum possible value
	// for 16-bit audio is 32 767; 300 corresponds to near-silence.
	defaultRMSThreshold = 300.0

	defaultLanguage           = "en"
	defaultSampleRate         = 16000
	defaultSilenceThresholdMs = 500
	defaultMaxBufferDurationMs = 10_000
)

// Compile-time assertion that Provider implements stt.Provider.
var _ stt.Provider = (*Provider)(nil)

// errNotSupported is returned by SetKeywords because whisper.cpp does not
// expose a keyword boosting API.
var errNotSupported = errors.New("keyword boosting is not supported by whisper.cpp")

// Option is a functional option for configuring a Provider.
type Option func(*Provider)

// WithModel sets the model identifier forwarded to the whisper.cpp server
// (e.g., "base.en", "small"). When empty the server uses whichever model it
// was started with — this is the default.
func WithModel(model string) Option {
	return func(p *Provider) {
		p.model = model
	}
}

// WithLanguage sets the BCP-47 language code sent to the whisper.cpp server
// (e.g., "en", "de", "fr"). Defaults to "en".
func WithLanguage(lang string) Option {
	return func(p *Provider) {
		p.language = lang
	}
}

// WithSampleRate sets the audio sample rate in Hz. This must match the actual
// sample rate of PCM data delivered via SendAudio and is used to calculate
// buffer durations and silence windows. Defaults to 16000.
func WithSampleRate(rate int) Option {
	return func(p *Provider) {
		p.sampleRate = rate
	}
}

// WithSilenceThresholdMs sets the consecutive-silence duration (in
// milliseconds) that triggers a flush of the accumulated speech buffer to
// whisper.cpp. Shorter values produce more responsive transcription at the
// cost of potentially splitting utterances. Defaults to 500 ms.
func WithSilenceThresholdMs(ms int) Option {
	return func(p *Provider) {
		p.silenceThresholdMs = ms
	}
}

// WithMaxBufferDurationMs sets the maximum duration of audio (in milliseconds)
// that may accumulate before a flush is forced regardless of silence. This
// prevents unbounded memory growth during continuous speech. Defaults to
// 10 000 ms (10 s).
func WithMaxBufferDurationMs(ms int) Option {
	return func(p *Provider) {
		p.maxBufferDurationMs = ms
	}
}

// Provider implements stt.Provider backed by a local whisper.cpp HTTP server.
// Multiple sessions may be open simultaneously; each session maintains its own
// audio buffer and goroutine.
type Provider struct {
	serverURL           string
	model               string
	language            string
	sampleRate          int
	silenceThresholdMs  int
	maxBufferDurationMs int
	httpClient          *http.Client
}

// New creates a new Provider that connects to the whisper.cpp HTTP server at
// serverURL (e.g., "http://localhost:8080"). serverURL must be non-empty.
// Functional options may be provided to override defaults.
func New(serverURL string, opts ...Option) (*Provider, error) {
	if serverURL == "" {
		return nil, errors.New("whisper: serverURL must not be empty")
	}
	p := &Provider{
		serverURL:           serverURL,
		model:               "",
		language:            defaultLanguage,
		sampleRate:          defaultSampleRate,
		silenceThresholdMs:  defaultSilenceThresholdMs,
		maxBufferDurationMs: defaultMaxBufferDurationMs,
		httpClient:          &http.Client{Timeout: 30 * time.Second},
	}
	for _, o := range opts {
		o(p)
	}
	return p, nil
}

// StartStream opens a new transcription session. The returned SessionHandle is
// ready to accept audio immediately. It respects cfg.SampleRate, cfg.Channels,
// and cfg.Language; if those are zero/empty the provider-level defaults apply.
//
// Returns an error only if the context is already cancelled; no network
// connection is established until the first flush.
func (p *Provider) StartStream(ctx context.Context, cfg stt.StreamConfig) (stt.SessionHandle, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("whisper: context already cancelled: %w", err)
	}

	lang := cfg.Language
	if lang == "" {
		lang = p.language
	}
	sr := cfg.SampleRate
	if sr <= 0 {
		sr = p.sampleRate
	}
	ch := cfg.Channels
	if ch <= 0 {
		ch = 1
	}

	s := &session{
		serverURL:           p.serverURL,
		model:               p.model,
		language:            lang,
		sampleRate:          sr,
		channels:            ch,
		silenceThresholdMs:  p.silenceThresholdMs,
		maxBufferDurationMs: p.maxBufferDurationMs,
		httpClient:          p.httpClient,

		audioCh:  make(chan []byte, 256),
		partials: make(chan types.Transcript, 64),
		finals:   make(chan types.Transcript, 64),
		done:     make(chan struct{}),
	}

	s.wg.Add(1)
	go s.processLoop(ctx)

	return s, nil
}

// ---- session ----------------------------------------------------------------

// session is a live whisper transcription session. It implements
// stt.SessionHandle. All mutable state that drives silence detection and
// buffering is confined to the processLoop goroutine to avoid data races.
type session struct {
	// immutable configuration (set once in StartStream)
	serverURL           string
	model               string
	language            string
	sampleRate          int
	channels            int
	silenceThresholdMs  int
	maxBufferDurationMs int
	httpClient          *http.Client

	// channels for audio input and transcript output
	audioCh  chan []byte
	partials chan types.Transcript
	finals   chan types.Transcript

	// lifecycle
	done chan struct{}
	once sync.Once
	wg   sync.WaitGroup
}

// SendAudio queues a chunk of raw 16-bit little-endian signed PCM audio for
// silence analysis and buffering. The chunk's sample rate and channel count
// must match the values agreed in StreamConfig (or the provider defaults).
//
// Calling SendAudio after Close returns an error.
func (s *session) SendAudio(chunk []byte) error {
	select {
	case <-s.done:
		return errors.New("whisper: session is closed")
	default:
	}
	select {
	case s.audioCh <- chunk:
		return nil
	case <-s.done:
		return errors.New("whisper: session is closed")
	}
}

// Partials returns a read-only channel that emits interim Transcript values.
// For whisper.cpp each partial is emitted simultaneously with its corresponding
// final (they carry identical text). The channel is closed when the session ends.
func (s *session) Partials() <-chan types.Transcript { return s.partials }

// Finals returns a read-only channel that emits authoritative Transcript values.
// These should be written to the session log and passed to the LLM.
// The channel is closed when the session ends.
func (s *session) Finals() <-chan types.Transcript { return s.finals }

// SetKeywords always returns an error because whisper.cpp does not expose a
// keyword-boosting API. The caller should treat this as a best-effort hint;
// the session remains usable after this call.
func (s *session) SetKeywords(_ []types.KeywordBoost) error {
	return fmt.Errorf("whisper: %w", errNotSupported)
}

// Close terminates the session, flushes any pending speech audio to
// whisper.cpp for a final transcription, closes the Partials and Finals
// channels, and releases all associated resources. Calling Close more than
// once is safe and returns nil.
func (s *session) Close() error {
	s.once.Do(func() {
		close(s.done)
		s.wg.Wait()
	})
	return nil
}

// processLoop is the single goroutine responsible for silence detection,
// audio buffering, and inference dispatch. Confining all mutable buffer state
// here avoids the need for additional synchronisation.
func (s *session) processLoop(ctx context.Context) {
	defer s.wg.Done()
	defer close(s.partials)
	defer close(s.finals)

	var (
		buffer    []byte // accumulated PCM for the current utterance
		hadSpeech bool   // true once any high-energy chunk has been buffered
		silenceMs int    // consecutive silence accumulated after speech (ms)
	)

	// bytesPerMs: PCM bytes corresponding to 1 ms of audio.
	bytesPerMs := s.sampleRate * s.channels * (bitsPerSample / 8) / 1000
	if bytesPerMs <= 0 {
		bytesPerMs = 32 // safe fallback (16 kHz, mono, 16-bit → 32 B/ms)
	}
	maxBufferBytes := s.maxBufferDurationMs * bytesPerMs

	// doFlush encodes the current buffer as WAV and calls the whisper.cpp
	// inference endpoint. It resets the buffer state regardless of outcome.
	doFlush := func(flushCtx context.Context) {
		if len(buffer) == 0 || !hadSpeech {
			buffer = nil
			hadSpeech = false
			silenceMs = 0
			return
		}

		pcm := buffer
		buffer = nil
		hadSpeech = false
		silenceMs = 0

		text, err := s.infer(flushCtx, pcm)
		if err != nil || text == "" {
			return
		}

		// Non-blocking sends: channels are buffered (64 elements). If they are
		// somehow full we skip rather than deadlock during shutdown.
		select {
		case s.partials <- types.Transcript{Text: text, IsFinal: false}:
		default:
		}
		select {
		case s.finals <- types.Transcript{Text: text, IsFinal: true}:
		default:
		}
	}

	// flushWithTimeout performs a final flush using a fresh background context
	// with a generous timeout, independent of the caller-supplied ctx which may
	// already be cancelled.
	flushWithTimeout := func() {
		fc, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		doFlush(fc)
	}

	for {
		select {
		case <-ctx.Done():
			flushWithTimeout()
			return

		case <-s.done:
			flushWithTimeout()
			return

		case chunk, ok := <-s.audioCh:
			if !ok {
				// Channel closed externally (unusual but handled).
				flushWithTimeout()
				return
			}

			rms := computeRMS(chunk)
			chunkMs := chunkDurationMs(chunk, s.sampleRate, s.channels)

			if rms < defaultRMSThreshold {
				// Silent chunk: only relevant once speech has started.
				if hadSpeech {
					silenceMs += chunkMs
					buffer = append(buffer, chunk...)
					if silenceMs >= s.silenceThresholdMs {
						doFlush(ctx)
					}
				}
				// Leading silence before any speech is discarded.
			} else {
				// Speech chunk.
				hadSpeech = true
				silenceMs = 0
				buffer = append(buffer, chunk...)
				// Force flush if the buffer has grown past the size limit.
				if maxBufferBytes > 0 && len(buffer) >= maxBufferBytes {
					doFlush(ctx)
				}
			}
		}
	}
}

// infer encodes pcm as a WAV file and POSTs it to the whisper.cpp /inference
// endpoint as multipart/form-data. It returns the transcribed text or an error.
func (s *session) infer(ctx context.Context, pcm []byte) (string, error) {
	wav := encodeWAV(pcm, s.sampleRate, s.channels)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)

	// Primary audio field.
	fw, err := mw.CreateFormFile("file", "audio.wav")
	if err != nil {
		return "", fmt.Errorf("whisper: create form file: %w", err)
	}
	if _, err := fw.Write(wav); err != nil {
		return "", fmt.Errorf("whisper: write wav data: %w", err)
	}

	// Optional hint fields.
	if s.language != "" {
		if err := mw.WriteField("language", s.language); err != nil {
			return "", fmt.Errorf("whisper: write language field: %w", err)
		}
	}
	if s.model != "" {
		if err := mw.WriteField("model", s.model); err != nil {
			return "", fmt.Errorf("whisper: write model field: %w", err)
		}
	}

	if err := mw.Close(); err != nil {
		return "", fmt.Errorf("whisper: close multipart writer: %w", err)
	}

	endpoint := s.serverURL + "/inference"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return "", fmt.Errorf("whisper: create request: %w", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("whisper: http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("whisper: server returned HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("whisper: read response body: %w", err)
	}

	var result struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("whisper: parse JSON response: %w", err)
	}

	return result.Text, nil
}

// ---- helpers ----------------------------------------------------------------

// encodeWAV wraps raw 16-bit signed little-endian PCM data in a standard
// RIFF/WAV container. The returned byte slice is suitable for direct inclusion
// in a multipart form upload. No external dependencies are required.
func encodeWAV(pcm []byte, sampleRate, channels int) []byte {
	bps := bitsPerSample
	byteRate := sampleRate * channels * bps / 8
	blockAlign := channels * bps / 8
	dataSize := len(pcm)

	buf := make([]byte, 44+dataSize)

	// RIFF chunk descriptor
	copy(buf[0:4], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:8], uint32(36+dataSize)) // file size − 8
	copy(buf[8:12], "WAVE")

	// fmt sub-chunk
	copy(buf[12:16], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:20], 16)                  // sub-chunk size (PCM)
	binary.LittleEndian.PutUint16(buf[20:22], 1)                   // audio format: PCM
	binary.LittleEndian.PutUint16(buf[22:24], uint16(channels))    // num channels
	binary.LittleEndian.PutUint32(buf[24:28], uint32(sampleRate))  // sample rate
	binary.LittleEndian.PutUint32(buf[28:32], uint32(byteRate))    // byte rate
	binary.LittleEndian.PutUint16(buf[32:34], uint16(blockAlign))  // block align
	binary.LittleEndian.PutUint16(buf[34:36], uint16(bps))         // bits per sample

	// data sub-chunk
	copy(buf[36:40], "data")
	binary.LittleEndian.PutUint32(buf[40:44], uint32(dataSize))
	copy(buf[44:], pcm)

	return buf
}

// computeRMS returns the root-mean-square energy of a 16-bit signed
// little-endian PCM buffer. Returns 0 for buffers shorter than one sample.
// The result is expressed in the same units as PCM sample values (0–32 767).
func computeRMS(pcm []byte) float64 {
	n := len(pcm) / 2 // number of 16-bit samples
	if n == 0 {
		return 0
	}
	var sum float64
	for i := 0; i < n; i++ {
		sample := int16(binary.LittleEndian.Uint16(pcm[i*2 : i*2+2]))
		v := float64(sample)
		sum += v * v
	}
	return math.Sqrt(sum / float64(n))
}

// chunkDurationMs returns the duration of a PCM audio chunk in milliseconds,
// based on the sample rate and channel count. Returns 0 for invalid inputs.
func chunkDurationMs(chunk []byte, sampleRate, channels int) int {
	if sampleRate <= 0 || channels <= 0 {
		return 0
	}
	bytesPerSec := sampleRate * channels * (bitsPerSample / 8)
	return len(chunk) * 1000 / bytesPerSec
}
