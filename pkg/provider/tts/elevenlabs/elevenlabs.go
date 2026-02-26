// Package elevenlabs provides an ElevenLabs-backed TTS provider using the
// ElevenLabs streaming WebSocket API. It implements the tts.Provider interface.
package elevenlabs

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/MrWong99/glyphoxa/pkg/provider/tts"
	"github.com/coder/websocket"
)

const (
	wsEndpointFmt    = "wss://api.elevenlabs.io/v1/text-to-speech/%s/stream-input?model_id=%s"
	voicesEndpoint   = "https://api.elevenlabs.io/v1/voices"
	defaultModel     = "eleven_flash_v2_5"
	defaultOutputFmt = "pcm_16000"
)

// Option is a functional option for configuring the ElevenLabs Provider.
type Option func(*Provider)

// WithModel sets the ElevenLabs model ID (e.g., "eleven_flash_v2_5").
func WithModel(model string) Option {
	return func(p *Provider) {
		p.model = model
	}
}

// WithOutputFormat sets the audio output format (e.g., "pcm_16000", "pcm_24000").
func WithOutputFormat(format string) Option {
	return func(p *Provider) {
		p.outputFormat = format
	}
}

// Provider implements tts.Provider backed by the ElevenLabs streaming API.
type Provider struct {
	apiKey       string
	model        string
	outputFormat string
	httpClient   *http.Client
}

// New creates a new ElevenLabs Provider. apiKey must be non-empty.
func New(apiKey string, opts ...Option) (*Provider, error) {
	if apiKey == "" {
		return nil, errors.New("elevenlabs: apiKey must not be empty")
	}
	p := &Provider{
		apiKey:       apiKey,
		model:        defaultModel,
		outputFormat: defaultOutputFmt,
		httpClient:   &http.Client{},
	}
	for _, o := range opts {
		o(p)
	}
	return p, nil
}

// ---- WebSocket message types ----

// textMessage is the JSON payload sent to ElevenLabs for each text fragment.
type textMessage struct {
	Text          string        `json:"text"`
	VoiceSettings *voiceSettings `json:"voice_settings,omitempty"`
}

// voiceSettings mirrors the ElevenLabs voice_settings object.
type voiceSettings struct {
	Stability       float64 `json:"stability"`
	SimilarityBoost float64 `json:"similarity_boost"`
}

// audioResponse is the JSON message received from ElevenLabs over the WebSocket.
type audioResponse struct {
	Audio    string `json:"audio"`     // base64-encoded PCM
	IsFinal  bool   `json:"isFinal"`
	Message  string `json:"message,omitempty"` // error or info
}

// boiMessage is used for the initial "begin of input" handshake.
type boiMessage struct {
	Text          string        `json:"text"`
	VoiceSettings *voiceSettings `json:"voice_settings,omitempty"`
	XiAPIKey      string        `json:"xi_api_key"`
	OutputFormat  string        `json:"output_format,omitempty"`
}

// SynthesizeStream opens a WebSocket to ElevenLabs, pipes text fragments from
// the text channel, and returns a channel emitting raw PCM audio chunks.
//
// The returned audio channel is closed when synthesis is complete or ctx is cancelled.
func (p *Provider) SynthesizeStream(ctx context.Context, text <-chan string, voice tts.VoiceProfile) (<-chan []byte, error) {
	if voice.ID == "" {
		return nil, errors.New("elevenlabs: voice.ID must not be empty")
	}

	wsURL := fmt.Sprintf(wsEndpointFmt, voice.ID, p.model)
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("elevenlabs: dial: %w", err)
	}

	// Send the initial BOI message to authenticate and configure the stream.
	boi := boiMessage{
		Text: " ", // ElevenLabs requires a non-empty first text value
		VoiceSettings: &voiceSettings{
			Stability:       0.5,
			SimilarityBoost: 0.75,
		},
		XiAPIKey:     p.apiKey,
		OutputFormat: p.outputFormat,
	}
	boiBytes, _ := json.Marshal(boi)
	if err := conn.Write(ctx, websocket.MessageText, boiBytes); err != nil {
		conn.Close(websocket.StatusInternalError, "failed to send BOI")
		return nil, fmt.Errorf("elevenlabs: send BOI: %w", err)
	}

	audioCh := make(chan []byte, 256)

	go func() {
		defer close(audioCh)
		defer conn.Close(websocket.StatusNormalClosure, "done")

		// Start reader goroutine.
		readDone := make(chan struct{})
		go func() {
			defer close(readDone)
			for {
				_, msg, err := conn.Read(ctx)
				if err != nil {
					return
				}
				var resp audioResponse
				if err := json.Unmarshal(msg, &resp); err != nil {
					continue
				}
				if resp.Audio == "" {
					continue
				}
				pcm, err := base64.StdEncoding.DecodeString(resp.Audio)
				if err != nil {
					continue
				}
				select {
				case audioCh <- pcm:
				case <-ctx.Done():
					return
				}
			}
		}()

		// Write text fragments to ElevenLabs.
		vs := &voiceSettings{Stability: 0.5, SimilarityBoost: 0.75}
		for {
			select {
			case sentence, ok := <-text:
				if !ok {
					// Text channel closed — send flush command.
					flush := textMessage{Text: ""}
					flushBytes, _ := json.Marshal(flush)
					_ = conn.Write(ctx, websocket.MessageText, flushBytes)
					// Wait for the reader to finish draining audio.
					<-readDone
					return
				}
				if sentence == "" {
					continue
				}
				payload := textMessage{Text: sentence, VoiceSettings: vs}
				// Only send voice settings on the first chunk; subsequent chunks can omit them.
				vs = nil
				msgBytes, _ := json.Marshal(payload)
				if err := conn.Write(ctx, websocket.MessageText, msgBytes); err != nil {
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	return audioCh, nil
}

// ---- ListVoices ----

// voicesResponse is the top-level response from GET /v1/voices.
type voicesResponse struct {
	Voices []elevenLabsVoice `json:"voices"`
}

// elevenLabsVoice is a single voice entry from the ElevenLabs API.
type elevenLabsVoice struct {
	VoiceID  string            `json:"voice_id"`
	Name     string            `json:"name"`
	Category string            `json:"category"`
	Labels   map[string]string `json:"labels"`
}

// ListVoices returns all voices available from ElevenLabs for the configured API key.
func (p *Provider) ListVoices(ctx context.Context) ([]tts.VoiceProfile, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, voicesEndpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("elevenlabs: list voices: %w", err)
	}
	req.Header.Set("xi-api-key", p.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("elevenlabs: list voices HTTP: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("elevenlabs: list voices: unexpected status %d", resp.StatusCode)
	}

	var vr voicesResponse
	if err := json.NewDecoder(resp.Body).Decode(&vr); err != nil {
		return nil, fmt.Errorf("elevenlabs: list voices decode: %w", err)
	}

	profiles := make([]tts.VoiceProfile, 0, len(vr.Voices))
	for _, v := range vr.Voices {
		meta := make(map[string]string, len(v.Labels)+1)
		for k, val := range v.Labels {
			meta[k] = val
		}
		if v.Category != "" {
			meta["category"] = v.Category
		}
		profiles = append(profiles, tts.VoiceProfile{
			ID:       v.VoiceID,
			Name:     v.Name,
			Provider: "elevenlabs",
			Metadata: meta,
		})
	}
	return profiles, nil
}

// CloneVoice is not implemented in Phase 1.
// TODO: implement voice cloning via POST /v1/voices/add
func (p *Provider) CloneVoice(_ context.Context, samples [][]byte) (*tts.VoiceProfile, error) {
	_ = samples
	return nil, errors.New("elevenlabs: CloneVoice is not implemented in Phase 1")
}

// ---- helpers ----

// buildWSMessage constructs the JSON text payload for a single text fragment.
// Used by tests to verify the payload shape without opening a real connection.
func buildWSMessage(text string, vs *voiceSettings) ([]byte, error) {
	return json.Marshal(textMessage{Text: text, VoiceSettings: vs})
}

// buildURLForVoice constructs the WebSocket URL for a given voice and model.
func buildURLForVoice(voiceID, model string) string {
	return fmt.Sprintf(wsEndpointFmt, voiceID, model)
}

// parseVoicesResponse parses a raw JSON byte slice (matching the ElevenLabs
// /v1/voices response) into a slice of VoiceProfile values.
func parseVoicesResponse(data []byte) ([]tts.VoiceProfile, error) {
	var vr voicesResponse
	if err := json.Unmarshal(data, &vr); err != nil {
		return nil, err
	}
	profiles := make([]tts.VoiceProfile, 0, len(vr.Voices))
	for _, v := range vr.Voices {
		meta := make(map[string]string, len(v.Labels)+1)
		for k, val := range v.Labels {
			meta[k] = val
		}
		if v.Category != "" {
			meta["category"] = v.Category
		}
		profiles = append(profiles, tts.VoiceProfile{
			ID:       v.VoiceID,
			Name:     v.Name,
			Provider: "elevenlabs",
			Metadata: meta,
		})
	}
	return profiles, nil
}

// Ensure the strings package is used (imported for potential future use).
var _ = strings.Contains
