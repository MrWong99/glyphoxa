// Package deepgram provides a Deepgram-backed STT provider using the Deepgram
// streaming WebSocket API. It implements the stt.Provider interface.
package deepgram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/MrWong99/glyphoxa/pkg/provider/stt"
	"github.com/MrWong99/glyphoxa/pkg/types"
	"github.com/coder/websocket"
)

const (
	deepgramEndpoint = "wss://api.deepgram.com/v1/listen"
	defaultModel     = "nova-3"
	defaultLanguage  = "en"
	defaultSampleRate = 16000
)

// Option is a functional option for configuring the Deepgram Provider.
type Option func(*Provider)

// WithModel sets the Deepgram model to use (e.g., "nova-3", "base").
func WithModel(model string) Option {
	return func(p *Provider) {
		p.model = model
	}
}

// WithLanguage sets the BCP-47 language code for recognition (e.g., "en", "de-DE").
func WithLanguage(language string) Option {
	return func(p *Provider) {
		p.language = language
	}
}

// WithSampleRate sets the audio sample rate in Hz for the provider-level default.
func WithSampleRate(rate int) Option {
	return func(p *Provider) {
		p.sampleRate = rate
	}
}

// Provider implements stt.Provider backed by the Deepgram streaming API.
type Provider struct {
	apiKey     string
	model      string
	language   string
	sampleRate int
}

// New creates a new Deepgram Provider. apiKey must be non-empty.
func New(apiKey string, opts ...Option) (*Provider, error) {
	if apiKey == "" {
		return nil, errors.New("deepgram: apiKey must not be empty")
	}
	p := &Provider{
		apiKey:     apiKey,
		model:      defaultModel,
		language:   defaultLanguage,
		sampleRate: defaultSampleRate,
	}
	for _, o := range opts {
		o(p)
	}
	return p, nil
}

// StartStream opens a streaming transcription session with Deepgram.
// It respects cfg.SampleRate, cfg.Language, and cfg.Keywords.
func (p *Provider) StartStream(ctx context.Context, cfg stt.StreamConfig) (stt.SessionHandle, error) {
	// Build the WebSocket URL with query parameters.
	wsURL, err := p.buildURL(cfg)
	if err != nil {
		return nil, fmt.Errorf("deepgram: build URL: %w", err)
	}

	headers := http.Header{}
	headers.Set("Authorization", "Token "+p.apiKey)

	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: headers,
	})
	if err != nil {
		return nil, fmt.Errorf("deepgram: dial: %w", err)
	}

	sess := &session{
		conn:     conn,
		partials: make(chan types.Transcript, 64),
		finals:   make(chan types.Transcript, 64),
		audio:    make(chan []byte, 256),
		done:     make(chan struct{}),
	}

	sess.wg.Add(2)
	go sess.readLoop(ctx)
	go sess.writeLoop(ctx)

	return sess, nil
}

// buildURL constructs the Deepgram streaming endpoint URL for the given config.
func (p *Provider) buildURL(cfg stt.StreamConfig) (string, error) {
	u, err := url.Parse(deepgramEndpoint)
	if err != nil {
		return "", err
	}

	lang := cfg.Language
	if lang == "" {
		lang = p.language
	}
	sr := cfg.SampleRate
	if sr == 0 {
		sr = p.sampleRate
	}

	q := u.Query()
	q.Set("model", p.model)
	q.Set("language", lang)
	q.Set("punctuate", "true")
	q.Set("interim_results", "true")
	q.Set("sample_rate", strconv.Itoa(sr))
	if cfg.Channels > 0 {
		q.Set("channels", strconv.Itoa(cfg.Channels))
	}

	for _, kw := range cfg.Keywords {
		// Deepgram keyword format: word:boost (e.g., "Eldrinax:5")
		val := fmt.Sprintf("%s:%g", kw.Keyword, kw.Boost)
		q.Add("keywords", val)
	}

	u.RawQuery = q.Encode()
	return u.String(), nil
}

// ---- session ----

// deepgramResponse is the JSON structure returned by Deepgram for a Results event.
type deepgramResponse struct {
	Type    string `json:"type"`
	IsFinal bool   `json:"is_final"`
	Channel struct {
		Alternatives []struct {
			Transcript string  `json:"transcript"`
			Confidence float64 `json:"confidence"`
			Words      []struct {
				Word       string  `json:"word"`
				Start      float64 `json:"start"`
				End        float64 `json:"end"`
				Confidence float64 `json:"confidence"`
			} `json:"words"`
		} `json:"alternatives"`
	} `json:"channel"`
}

// session is a live Deepgram streaming session. It implements stt.SessionHandle.
type session struct {
	conn     *websocket.Conn
	partials chan types.Transcript
	finals   chan types.Transcript
	audio    chan []byte

	done   chan struct{}
	once   sync.Once
	wg     sync.WaitGroup

	kwMu     sync.RWMutex
	keywords []types.KeywordBoost // stored for reference; Deepgram doesn't support mid-stream updates
}

// SendAudio queues a PCM audio chunk for delivery to Deepgram.
func (s *session) SendAudio(chunk []byte) error {
	select {
	case <-s.done:
		return errors.New("deepgram: session is closed")
	default:
	}
	select {
	case s.audio <- chunk:
		return nil
	case <-s.done:
		return errors.New("deepgram: session is closed")
	}
}

// Partials returns the channel of interim transcripts.
func (s *session) Partials() <-chan types.Transcript { return s.partials }

// Finals returns the channel of final transcripts.
func (s *session) Finals() <-chan types.Transcript { return s.finals }

// SetKeywords records the new keyword list. Deepgram does not support mid-stream
// keyword updates, so this returns stt.ErrNotSupported.
func (s *session) SetKeywords(keywords []types.KeywordBoost) error {
	s.kwMu.Lock()
	s.keywords = keywords
	s.kwMu.Unlock()
	return fmt.Errorf("deepgram: %w", errNotSupported)
}

var errNotSupported = errors.New("mid-session keyword updates are not supported")

// Close terminates the session cleanly.
func (s *session) Close() error {
	s.once.Do(func() {
		close(s.done)
		// Send a close message to Deepgram to flush pending audio.
		_ = s.conn.Write(context.Background(), websocket.MessageText, []byte(`{"type":"CloseStream"}`))
		s.wg.Wait()
		s.conn.Close(websocket.StatusNormalClosure, "session closed")
	})
	return nil
}

// writeLoop reads from the audio channel and sends binary messages to Deepgram.
func (s *session) writeLoop(ctx context.Context) {
	defer s.wg.Done()
	for {
		select {
		case chunk, ok := <-s.audio:
			if !ok {
				return
			}
			if err := s.conn.Write(ctx, websocket.MessageBinary, chunk); err != nil {
				return
			}
		case <-s.done:
			// Drain the audio channel before exiting.
			for {
				select {
				case chunk, ok := <-s.audio:
					if !ok {
						return
					}
					_ = s.conn.Write(ctx, websocket.MessageBinary, chunk)
				default:
					return
				}
			}
		}
	}
}

// readLoop receives JSON messages from Deepgram and dispatches them to the
// partials and finals channels.
func (s *session) readLoop(ctx context.Context) {
	defer s.wg.Done()
	defer close(s.partials)
	defer close(s.finals)

	for {
		_, msg, err := s.conn.Read(ctx)
		if err != nil {
			// Normal close or context cancellation — exit gracefully.
			return
		}

		t, ok := parseDeepgramResponse(msg)
		if !ok {
			continue
		}

		if t.IsFinal {
			select {
			case s.finals <- t:
			case <-s.done:
			}
		} else {
			select {
			case s.partials <- t:
			case <-s.done:
			}
		}
	}
}

// parseDeepgramResponse parses a raw Deepgram WebSocket message into a Transcript.
// Returns (Transcript, true) on success, or (zero, false) if the message should be ignored.
func parseDeepgramResponse(data []byte) (types.Transcript, bool) {
	var resp deepgramResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return types.Transcript{}, false
	}
	if resp.Type != "Results" {
		return types.Transcript{}, false
	}
	if len(resp.Channel.Alternatives) == 0 {
		return types.Transcript{}, false
	}

	alt := resp.Channel.Alternatives[0]
	words := make([]types.WordDetail, 0, len(alt.Words))
	for _, w := range alt.Words {
		words = append(words, types.WordDetail{
			Word:       w.Word,
			Start:      time.Duration(w.Start * float64(time.Second)),
			End:        time.Duration(w.End * float64(time.Second)),
			Confidence: w.Confidence,
		})
	}

	return types.Transcript{
		Text:       alt.Transcript,
		IsFinal:    resp.IsFinal,
		Confidence: alt.Confidence,
		Words:      words,
	}, true
}
