// Package gemini implements the s2s.Provider interface for Google's Gemini Live API.
//
// It establishes a bidirectional WebSocket connection to the Gemini Live endpoint
// and exchanges JSON messages according to the BidiGenerateContent protocol.
// Audio is transmitted as base64-encoded PCM chunks; tool calls are surfaced via
// the ToolCallHandler callback.
package gemini

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/MrWong99/glyphoxa/pkg/memory"
	"github.com/MrWong99/glyphoxa/pkg/provider/llm"
	"github.com/MrWong99/glyphoxa/pkg/provider/s2s"
	"github.com/MrWong99/glyphoxa/pkg/provider/tts"
	"github.com/coder/websocket"
)

// Compile-time assertions that Provider and session satisfy the s2s interfaces.
var _ s2s.Provider = (*Provider)(nil)
var _ s2s.SessionHandle = (*session)(nil)

const (
	defaultModel   = "gemini-2.0-flash-live-001"
	defaultBaseURL = "wss://generativelanguage.googleapis.com/ws"

	keepaliveInterval = 20 * time.Second
	keepaliveTimeout  = 5 * time.Second
)

// ── Options ────────────────────────────────────────────────────────────────────

// Option is a functional option for configuring a Provider.
type Option func(*Provider)

// WithModel sets the Gemini model used for sessions.
func WithModel(model string) Option {
	return func(p *Provider) { p.model = model }
}

// WithBaseURL overrides the base WebSocket URL. Primarily used in tests to
// point at a local mock server.
func WithBaseURL(url string) Option {
	return func(p *Provider) { p.baseURL = url }
}

// ── Provider ───────────────────────────────────────────────────────────────────

// Provider implements s2s.Provider for Google's Gemini Live API.
type Provider struct {
	apiKey  string
	model   string
	baseURL string
}

// New creates a new Gemini Live Provider with the given API key and options.
func New(apiKey string, opts ...Option) *Provider {
	p := &Provider{
		apiKey:  apiKey,
		model:   defaultModel,
		baseURL: defaultBaseURL,
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

// Capabilities returns static metadata about the Gemini Live provider.
func (p *Provider) Capabilities() s2s.S2SCapabilities {
	return s2s.S2SCapabilities{
		ContextWindow:        1_000_000,
		MaxSessionDurationMs: 15 * 60 * 1000,
		SupportsResumption:   false,
		Voices: []tts.VoiceProfile{
			{ID: "Aoede", Name: "Aoede", Provider: "gemini"},
			{ID: "Charon", Name: "Charon", Provider: "gemini"},
			{ID: "Fenrir", Name: "Fenrir", Provider: "gemini"},
			{ID: "Kore", Name: "Kore", Provider: "gemini"},
			{ID: "Puck", Name: "Puck", Provider: "gemini"},
		},
	}
}

// Connect establishes a new Gemini Live session with the given configuration.
// The returned SessionHandle is ready to accept audio immediately after the
// setup message is sent.
func (p *Provider) Connect(ctx context.Context, cfg s2s.SessionConfig) (s2s.SessionHandle, error) {
	wsURL := fmt.Sprintf(
		"%s/google.ai.generativelanguage.v1beta.GenerativeService.BidiGenerateContent?key=%s",
		p.baseURL, p.apiKey,
	)

	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Content-Type": []string{"application/json"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("gemini: dial: %w", err)
	}

	sessCtx, sessCancel := context.WithCancel(context.Background())
	sess := &session{
		conn:        conn,
		audioCh:     make(chan []byte, 64),
		transcripts: make(chan memory.TranscriptEntry, 16),
		done:        make(chan struct{}),
		ctx:         sessCtx,
		cancel:      sessCancel,
	}

	if err := sess.sendSetup(p.model, cfg); err != nil {
		sessCancel()
		conn.Close(websocket.StatusInternalError, "setup failed")
		return nil, fmt.Errorf("gemini: setup: %w", err)
	}

	go sess.receiveLoop()
	go sess.keepaliveLoop()

	return sess, nil
}

// ── Protocol message types (outgoing) ─────────────────────────────────────────

type setupMessage struct {
	Setup setupConfig `json:"setup"`
}

type setupConfig struct {
	Model             string             `json:"model"`
	GenerationConfig  generationConfig   `json:"generationConfig"`
	SystemInstruction *systemInstruction `json:"systemInstruction,omitempty"`
	Tools             []geminiTool       `json:"tools,omitempty"`
}

type generationConfig struct {
	ResponseModalities []string      `json:"responseModalities"`
	SpeechConfig       *speechConfig `json:"speechConfig,omitempty"`
}

type speechConfig struct {
	VoiceConfig voiceConfig `json:"voiceConfig"`
}

type voiceConfig struct {
	PrebuiltVoiceConfig prebuiltVoiceConfig `json:"prebuiltVoiceConfig"`
}

type prebuiltVoiceConfig struct {
	VoiceName string `json:"voiceName"`
}

type systemInstruction struct {
	Parts []part `json:"parts"`
}

type part struct {
	Text       string      `json:"text,omitempty"`
	InlineData *inlineData `json:"inlineData,omitempty"`
}

type inlineData struct {
	MIMEType string `json:"mimeType"`
	Data     string `json:"data"` // base64-encoded
}

type geminiTool struct {
	FunctionDeclarations []functionDeclaration `json:"functionDeclarations,omitempty"`
}

type functionDeclaration struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type realtimeInputMessage struct {
	RealtimeInput realtimeInput `json:"realtimeInput"`
}

type realtimeInput struct {
	MediaChunks []mediaChunk `json:"mediaChunks"`
}

type mediaChunk struct {
	MIMEType string `json:"mimeType"`
	Data     string `json:"data"` // base64-encoded
}

type clientContentMessage struct {
	ClientContent clientContent `json:"clientContent"`
}

type clientContent struct {
	Turns        []contentTurn `json:"turns"`
	TurnComplete bool          `json:"turnComplete"`
}

type contentTurn struct {
	Role  string `json:"role"`
	Parts []part `json:"parts"`
}

type toolResponseMessage struct {
	ToolResponse toolResponse `json:"toolResponse"`
}

type toolResponse struct {
	FunctionResponses []functionResponse `json:"functionResponses"`
}

type functionResponse struct {
	ID       string         `json:"id,omitempty"`
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

// ── Protocol message types (incoming) ─────────────────────────────────────────

type serverMessage struct {
	SetupComplete        *json.RawMessage `json:"setupComplete,omitempty"`
	ServerContent        *serverContent   `json:"serverContent,omitempty"`
	ToolCall             *toolCallMsg     `json:"toolCall,omitempty"`
	ToolCallCancellation *json.RawMessage `json:"toolCallCancellation,omitempty"`
	Error                *geminiError     `json:"error,omitempty"`
}

type geminiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status,omitempty"`
}

type serverContent struct {
	ModelTurn           *modelTurn     `json:"modelTurn,omitempty"`
	TurnComplete        bool           `json:"turnComplete,omitempty"`
	Interrupted         bool           `json:"interrupted,omitempty"`
	InputTranscription  *transcription `json:"inputTranscription,omitempty"`
	OutputTranscription *transcription `json:"outputTranscription,omitempty"`
}

type modelTurn struct {
	Parts []part `json:"parts"`
}

type transcription struct {
	Text string `json:"text"`
}

type toolCallMsg struct {
	FunctionCalls []functionCall `json:"functionCalls"`
}

type functionCall struct {
	ID   string         `json:"id"`
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

// ── session ────────────────────────────────────────────────────────────────────

type session struct {
	conn         *websocket.Conn
	audioCh      chan []byte
	transcripts  chan memory.TranscriptEntry
	toolHandler  s2s.ToolCallHandler
	errorHandler func(error)

	mu     sync.Mutex
	errVal error
	done   chan struct{}
	closed bool

	ctx       context.Context
	cancel    context.CancelFunc
	closeOnce sync.Once
}

// sendSetup sends the initial BidiGenerateContent setup message.
func (s *session) sendSetup(model string, cfg s2s.SessionConfig) error {
	msg := setupMessage{
		Setup: setupConfig{
			Model: fmt.Sprintf("models/%s", model),
			GenerationConfig: generationConfig{
				ResponseModalities: []string{"audio"},
			},
		},
	}

	if cfg.Instructions != "" {
		msg.Setup.SystemInstruction = &systemInstruction{
			Parts: []part{{Text: cfg.Instructions}},
		}
	}

	if cfg.Voice.ID != "" {
		msg.Setup.GenerationConfig.SpeechConfig = &speechConfig{
			VoiceConfig: voiceConfig{
				PrebuiltVoiceConfig: prebuiltVoiceConfig{VoiceName: cfg.Voice.ID},
			},
		}
	}

	if len(cfg.Tools) > 0 {
		decls := make([]functionDeclaration, len(cfg.Tools))
		for i, t := range cfg.Tools {
			decls[i] = functionDeclaration{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			}
		}
		msg.Setup.Tools = []geminiTool{{FunctionDeclarations: decls}}
	}

	return s.writeJSON(msg)
}

// writeJSON marshals v and writes it as a text WebSocket message.
func (s *session) writeJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("gemini: marshal: %w", err)
	}
	return s.conn.Write(s.ctx, websocket.MessageText, data)
}

// receiveLoop reads messages from the WebSocket and dispatches them.
// It owns audioCh and transcripts: it closes both when it exits.
func (s *session) receiveLoop() {
	defer s.closeChannels()

	for {
		_, data, err := s.conn.Read(s.ctx)
		if err != nil {
			// If the session context was cancelled, exit cleanly.
			if s.ctx.Err() != nil {
				return
			}
			s.setErr(err)
			return
		}

		var msg serverMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue // skip malformed frames
		}

		s.handleServerMessage(&msg)
	}
}

func (s *session) handleServerMessage(msg *serverMessage) {
	if msg.Error != nil {
		s.handleError(msg.Error)
	}
	if msg.ServerContent != nil {
		s.handleServerContent(msg.ServerContent)
	}
	if msg.ToolCall != nil {
		s.handleToolCall(msg.ToolCall)
	}
}

func (s *session) handleError(ge *geminiError) {
	s.mu.Lock()
	handler := s.errorHandler
	s.mu.Unlock()

	if handler == nil {
		return
	}

	msg := "unknown error"
	if ge.Message != "" {
		msg = ge.Message
	}
	handler(fmt.Errorf("gemini: %s", msg))
}

func (s *session) handleServerContent(sc *serverContent) {
	if sc.ModelTurn != nil {
		// Emit audio chunks and text transcript parts in a single pass.
		for _, p := range sc.ModelTurn.Parts {
			if p.InlineData != nil {
				audioData, err := base64.StdEncoding.DecodeString(p.InlineData.Data)
				if err != nil || len(audioData) == 0 {
					continue
				}
				select {
				case s.audioCh <- audioData:
				case <-s.ctx.Done():
					return
				}
			}
			if p.Text != "" {
				entry := memory.TranscriptEntry{
					SpeakerID:   "model",
					SpeakerName: "NPC",
					Text:        p.Text,
					NPCID:       "gemini",
					Timestamp:   time.Now(),
				}
				select {
				case s.transcripts <- entry:
				case <-s.ctx.Done():
					return
				}
			}
		}
	}

	// User speech recognition result.
	if sc.InputTranscription != nil && sc.InputTranscription.Text != "" {
		entry := memory.TranscriptEntry{
			SpeakerID:   "user",
			SpeakerName: "User",
			Text:        sc.InputTranscription.Text,
			Timestamp:   time.Now(),
		}
		select {
		case s.transcripts <- entry:
		case <-s.ctx.Done():
			return
		}
	}

	// Model output transcription (text version of audio output).
	if sc.OutputTranscription != nil && sc.OutputTranscription.Text != "" {
		entry := memory.TranscriptEntry{
			SpeakerID:   "model",
			SpeakerName: "NPC",
			Text:        sc.OutputTranscription.Text,
			NPCID:       "gemini",
			Timestamp:   time.Now(),
		}
		select {
		case s.transcripts <- entry:
		case <-s.ctx.Done():
			return
		}
	}
}

func (s *session) handleToolCall(tc *toolCallMsg) {
	s.mu.Lock()
	handler := s.toolHandler
	s.mu.Unlock()

	if handler == nil {
		return
	}

	for _, fc := range tc.FunctionCalls {
		argsJSON, err := json.Marshal(fc.Args)
		if err != nil {
			continue
		}

		result, callErr := handler(fc.Name, string(argsJSON))
		if callErr != nil {
			result = fmt.Sprintf(`{"error": %q}`, callErr.Error())
		}

		// Attempt to parse result as JSON; fall back to wrapping in {"output":...}.
		var respObj map[string]any
		if jsonErr := json.Unmarshal([]byte(result), &respObj); jsonErr != nil {
			respObj = map[string]any{"output": result}
		}

		resp := toolResponseMessage{
			ToolResponse: toolResponse{
				FunctionResponses: []functionResponse{
					{
						ID:       fc.ID,
						Name:     fc.Name,
						Response: respObj,
					},
				},
			},
		}
		_ = s.writeJSON(resp) // best-effort; ignore write errors after close
	}
}

// keepaliveLoop sends WebSocket pings to keep the Gemini Live connection alive.
func (s *session) keepaliveLoop() {
	ticker := time.NewTicker(keepaliveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.done:
			return
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(s.ctx, keepaliveTimeout)
			_ = s.conn.Ping(pingCtx)
			cancel()
		}
	}
}

func (s *session) setErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.errVal == nil {
		s.errVal = err
	}
}

func (s *session) closeChannels() {
	s.closeOnce.Do(func() {
		close(s.audioCh)
		close(s.transcripts)
	})
}

// ── SessionHandle methods ──────────────────────────────────────────────────────

// SendAudio delivers a raw PCM audio chunk (16 kHz, s16le, mono) to the model.
func (s *session) SendAudio(chunk []byte) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("gemini: session closed")
	}
	s.mu.Unlock()

	encoded := base64.StdEncoding.EncodeToString(chunk)
	msg := realtimeInputMessage{
		RealtimeInput: realtimeInput{
			MediaChunks: []mediaChunk{
				{MIMEType: "audio/pcm;rate=16000", Data: encoded},
			},
		},
	}
	return s.writeJSON(msg)
}

// Audio returns the channel on which the model's synthesised audio arrives.
func (s *session) Audio() <-chan []byte { return s.audioCh }

// Err returns the first non-nil error that caused the session to terminate.
func (s *session) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.errVal
}

// Transcripts returns the channel on which transcript entries arrive.
func (s *session) Transcripts() <-chan memory.TranscriptEntry { return s.transcripts }

// OnError registers a callback for non-fatal error events from the provider.
func (s *session) OnError(handler func(error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.errorHandler = handler
}

// OnToolCall registers a callback for tool invocations from the model.
func (s *session) OnToolCall(handler s2s.ToolCallHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.toolHandler = handler
}

// SetTools is not supported by the Gemini Live protocol; an error is always
// returned. Tool definitions can only be set at session creation time via
// [SessionConfig.Tools].
func (s *session) SetTools(_ []llm.ToolDefinition) error {
	return fmt.Errorf("gemini: mid-session tool updates are not supported")
}

// UpdateInstructions is not supported by the Gemini Live protocol; an error is
// always returned.
func (s *session) UpdateInstructions(_ string) error {
	return fmt.Errorf("gemini: mid-session instruction updates are not supported")
}

// InjectTextContext inserts ContextItems into the session as clientContent turns.
func (s *session) InjectTextContext(items []s2s.ContextItem) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("gemini: session closed")
	}
	s.mu.Unlock()

	if len(items) == 0 {
		return nil
	}

	turns := make([]contentTurn, len(items))
	for i, item := range items {
		role := item.Role
		switch role {
		case "assistant":
			role = "model"
		case "model":
			// already correct
		default:
			role = "user"
		}
		turns[i] = contentTurn{
			Role:  role,
			Parts: []part{{Text: item.Content}},
		}
	}

	msg := clientContentMessage{
		ClientContent: clientContent{
			Turns:        turns,
			TurnComplete: true,
		},
	}
	return s.writeJSON(msg)
}

// Interrupt is not supported by the Gemini Live protocol; an error is always
// returned.
func (s *session) Interrupt() error {
	return fmt.Errorf("gemini: interrupt not supported")
}

// Close terminates the session and releases all resources. Idempotent.
func (s *session) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()

	s.cancel()    // unblocks receiveLoop and keepaliveLoop
	close(s.done) // signals keepaliveLoop via done channel
	s.conn.Close(websocket.StatusNormalClosure, "session closed")
	return nil
}
