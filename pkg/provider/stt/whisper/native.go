// This file contains the NativeProvider implementation backed by the
// whisper.cpp CGO bindings. The whisper.cpp static library (libwhisper.a)
// and headers (whisper.h) must be available at link time via LIBRARY_PATH
// and C_INCLUDE_PATH environment variables.

package whisper

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/MrWong99/glyphoxa/pkg/provider/stt"
	whisperlib "github.com/ggerganov/whisper.cpp/bindings/go/pkg/whisper"
)

// Compile-time assertion that NativeProvider satisfies stt.Provider.
var _ stt.Provider = (*NativeProvider)(nil)

// NativeProvider implements stt.Provider using whisper.cpp Go bindings
// (CGO), eliminating HTTP overhead entirely. The model is loaded once at
// startup and shared across all sessions.
type NativeProvider struct {
	model    whisperlib.Model
	language string

	// Same silence-detection parameters as the HTTP provider.
	sampleRate          int
	silenceThresholdMs  int
	maxBufferDurationMs int
}

// NativeOption is a functional option for configuring a NativeProvider.
type NativeOption func(*NativeProvider)

// WithNativeLanguage sets the BCP-47 language code for transcription
// (e.g., "en", "de", "fr"). Defaults to "en".
func WithNativeLanguage(lang string) NativeOption {
	return func(p *NativeProvider) { p.language = lang }
}

// WithNativeSampleRate sets the audio sample rate in Hz. This must match the
// actual sample rate of PCM data delivered via SendAudio. Defaults to 16000.
func WithNativeSampleRate(rate int) NativeOption {
	return func(p *NativeProvider) { p.sampleRate = rate }
}

// WithNativeSilenceThresholdMs sets the consecutive-silence duration (ms) that
// triggers a flush of the accumulated speech buffer to whisper.cpp. Defaults
// to 500 ms.
func WithNativeSilenceThresholdMs(ms int) NativeOption {
	return func(p *NativeProvider) { p.silenceThresholdMs = ms }
}

// WithNativeMaxBufferDurationMs sets the maximum buffered audio duration (ms)
// before a forced flush. Defaults to 10 000 ms (10 s).
func WithNativeMaxBufferDurationMs(ms int) NativeOption {
	return func(p *NativeProvider) { p.maxBufferDurationMs = ms }
}

// NewNative creates a NativeProvider that loads the whisper.cpp model from
// the given file path. The model is loaded once and shared across all
// concurrent sessions. The caller must call Close when the provider is no
// longer needed.
func NewNative(modelPath string, opts ...NativeOption) (*NativeProvider, error) {
	if modelPath == "" {
		return nil, errors.New("whisper: modelPath must not be empty")
	}
	model, err := whisperlib.New(modelPath)
	if err != nil {
		return nil, fmt.Errorf("whisper: load model %q: %w", modelPath, err)
	}

	p := &NativeProvider{
		model:               model,
		language:            defaultLanguage,
		sampleRate:          defaultSampleRate,
		silenceThresholdMs:  defaultSilenceThresholdMs,
		maxBufferDurationMs: defaultMaxBufferDurationMs,
	}
	for _, o := range opts {
		o(p)
	}
	return p, nil
}

// Close releases the whisper model. Must be called when the provider is no
// longer needed.
func (p *NativeProvider) Close() error {
	if p.model != nil {
		return p.model.Close()
	}
	return nil
}

// StartStream opens a new transcription session. The returned SessionHandle is
// ready to accept audio immediately. It respects cfg.SampleRate, cfg.Channels,
// and cfg.Language; if those are zero/empty the provider-level defaults apply.
//
// Each session creates its own whisper.cpp context from the shared model, so
// multiple sessions can run concurrently without interference.
func (p *NativeProvider) StartStream(ctx context.Context, cfg stt.StreamConfig) (stt.SessionHandle, error) {
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

	s := &nativeSession{
		model:               p.model,
		language:            lang,
		sampleRate:          sr,
		channels:            ch,
		silenceThresholdMs:  p.silenceThresholdMs,
		maxBufferDurationMs: p.maxBufferDurationMs,

		audioCh:  make(chan []byte, 256),
		partials: make(chan stt.Transcript, 64),
		finals:   make(chan stt.Transcript, 64),
		done:     make(chan struct{}),
	}

	s.wg.Add(1)
	go s.processLoop(ctx)

	return s, nil
}

// ---- nativeSession ----------------------------------------------------------

// nativeSession is a live whisper transcription session using the CGO bindings.
// It implements stt.SessionHandle. All mutable state that drives silence
// detection and buffering is confined to the processLoop goroutine.
type nativeSession struct {
	// immutable configuration (set once in StartStream)
	model               whisperlib.Model
	language            string
	sampleRate          int
	channels            int
	silenceThresholdMs  int
	maxBufferDurationMs int

	// channels for audio input and transcript output
	audioCh  chan []byte
	partials chan stt.Transcript
	finals   chan stt.Transcript

	// lifecycle
	done chan struct{}
	once sync.Once
	wg   sync.WaitGroup
}

// SendAudio queues a chunk of raw 16-bit little-endian signed PCM audio for
// silence analysis and buffering.
func (s *nativeSession) SendAudio(chunk []byte) error {
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
func (s *nativeSession) Partials() <-chan stt.Transcript { return s.partials }

// Finals returns a read-only channel that emits authoritative Transcript values.
func (s *nativeSession) Finals() <-chan stt.Transcript { return s.finals }

// SetKeywords always returns an error because whisper.cpp does not expose a
// keyword-boosting API.
func (s *nativeSession) SetKeywords(_ []stt.KeywordBoost) error {
	return fmt.Errorf("whisper: %w", errNotSupported)
}

// Close terminates the session, flushes any pending speech audio, closes the
// Partials and Finals channels, and releases all associated resources.
func (s *nativeSession) Close() error {
	s.once.Do(func() {
		close(s.done)
		s.wg.Wait()
	})
	return nil
}

// processLoop is the single goroutine responsible for silence detection, audio
// buffering, and native inference dispatch.
func (s *nativeSession) processLoop(ctx context.Context) {
	defer s.wg.Done()
	defer close(s.partials)
	defer close(s.finals)

	var (
		buffer    []byte
		hadSpeech bool
		silenceMs int
	)

	bytesPerMs := s.sampleRate * s.channels * (bitsPerSample / 8) / 1000
	if bytesPerMs <= 0 {
		bytesPerMs = 32
	}
	maxBufferBytes := s.maxBufferDurationMs * bytesPerMs

	doFlush := func() {
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

		text, err := s.infer(pcm)
		if err != nil {
			slog.Error("whisper native inference failed", "error", err)
			return
		}
		if text == "" {
			return
		}

		select {
		case s.partials <- stt.Transcript{Text: text, IsFinal: false}:
		default:
		}
		select {
		case s.finals <- stt.Transcript{Text: text, IsFinal: true}:
		default:
		}
	}

	for {
		select {
		case <-ctx.Done():
			doFlush()
			return

		case <-s.done:
			doFlush()
			return

		case chunk, ok := <-s.audioCh:
			if !ok {
				doFlush()
				return
			}

			rms := computeRMS(chunk)
			chunkMs := chunkDurationMs(chunk, s.sampleRate, s.channels)

			if rms < defaultRMSThreshold {
				if hadSpeech {
					silenceMs += chunkMs
					buffer = append(buffer, chunk...)
					if silenceMs >= s.silenceThresholdMs {
						doFlush()
					}
				}
			} else {
				hadSpeech = true
				silenceMs = 0
				buffer = append(buffer, chunk...)
				if maxBufferBytes > 0 && len(buffer) >= maxBufferBytes {
					doFlush()
				}
			}
		}
	}
}

// infer converts the buffered PCM audio to float32, runs whisper.cpp
// inference using a fresh context, and returns the concatenated text.
func (s *nativeSession) infer(pcm []byte) (string, error) {
	// Convert PCM to float32 mono samples.
	samples := pcmToFloat32Mono(pcm, s.channels)

	// Create a new whisper context for this inference. Each context is NOT
	// thread-safe, but the model can be shared across goroutines.
	wctx, err := s.model.NewContext()
	if err != nil {
		return "", fmt.Errorf("whisper: create context: %w", err)
	}

	// Set language.
	if err := wctx.SetLanguage(s.language); err != nil {
		slog.Warn("whisper: failed to set language, using default", "language", s.language, "error", err)
	}

	// Run inference.
	if err := wctx.Process(samples, nil, nil, nil); err != nil {
		return "", fmt.Errorf("whisper: process audio: %w", err)
	}

	// Collect segments.
	var parts []string
	for {
		segment, err := wctx.NextSegment()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("whisper: read segment: %w", err)
		}
		text := strings.TrimSpace(segment.Text)
		if text != "" {
			parts = append(parts, text)
		}
	}

	return strings.Join(parts, " "), nil
}

// Compile-time assertion that nativeSession satisfies stt.SessionHandle.
var _ stt.SessionHandle = (*nativeSession)(nil)
