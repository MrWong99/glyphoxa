// Package openai implements the s2s.Provider interface for OpenAI's Realtime API.
//
// It establishes a bidirectional WebSocket connection to the OpenAI Realtime
// endpoint and exchanges JSON events according to the Realtime API protocol.
// Audio is transmitted as base64-encoded PCM16 chunks; tool calls are surfaced
// via the ToolCallHandler callback. Mid-session updates (instructions, tools,
// interruption) are fully supported via session.update / response.cancel events.
package openai

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
	defaultModel   = "gpt-4o-realtime-preview"
	defaultBaseURL = "wss://api.openai.com/v1/realtime"
)

// ── Options ────────────────────────────────────────────────────────────────────

// Option is a functional option for configuring a Provider.
type Option func(*Provider)

// WithModel sets the OpenAI model used for sessions.
func WithModel(model string) Option {
	return func(p *Provider) { p.model = model }
}

// WithBaseURL overrides the base WebSocket URL. Primarily used in tests to
// point at a local mock server.
func WithBaseURL(url string) Option {
	return func(p *Provider) { p.baseURL = url }
}

// ── Provider ───────────────────────────────────────────────────────────────────

// Provider implements s2s.Provider for OpenAI's Realtime API.
type Provider struct {
	apiKey  string
	model   string
	baseURL string
}

// New creates a new OpenAI Realtime Provider with the given API key and options.
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

// Capabilities returns static metadata about the OpenAI Realtime provider.
func (p *Provider) Capabilities() s2s.S2SCapabilities {
	return s2s.S2SCapabilities{
		ContextWindow:        128_000,
		MaxSessionDurationMs: 30 * 60 * 1000,
		SupportsResumption:   false,
		Voices: []tts.VoiceProfile{
			{ID: "alloy", Name: "Alloy", Provider: "openai"},
			{ID: "ash", Name: "Ash", Provider: "openai"},
			{ID: "ballad", Name: "Ballad", Provider: "openai"},
			{ID: "coral", Name: "Coral", Provider: "openai"},
			{ID: "echo", Name: "Echo", Provider: "openai"},
			{ID: "sage", Name: "Sage", Provider: "openai"},
			{ID: "shimmer", Name: "Shimmer", Provider: "openai"},
			{ID: "verse", Name: "Verse", Provider: "openai"},
		},
	}
}

// Connect establishes a new OpenAI Realtime session with the given configuration.
// The returned SessionHandle is ready to accept audio immediately after the
// session.update message is sent.
func (p *Provider) Connect(ctx context.Context, cfg s2s.SessionConfig) (s2s.SessionHandle, error) {
	wsURL := fmt.Sprintf("%s?model=%s", p.baseURL, p.model)

	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Authorization": []string{"Bearer " + p.apiKey},
			"OpenAI-Beta":   []string{"realtime=v1"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("openai: dial: %w", err)
	}

	sessCtx, sessCancel := context.WithCancel(context.Background())
	sess := &session{
		conn:        conn,
		audioCh:     make(chan []byte, 64),
		transcripts: make(chan memory.TranscriptEntry, 16),
		ctx:         sessCtx,
		cancel:      sessCancel,
	}

	if err := sess.sendSessionUpdate(cfg.Voice, cfg.Instructions, cfg.Tools); err != nil {
		sessCancel()
		conn.Close(websocket.StatusInternalError, "session update failed")
		return nil, fmt.Errorf("openai: session update: %w", err)
	}

	go sess.receiveLoop()

	return sess, nil
}

// ── Protocol message types (outgoing) ─────────────────────────────────────────

type sessionUpdateMessage struct {
	Type    string        `json:"type"`
	Session sessionParams `json:"session"`
}

type sessionParams struct {
	Voice             string    `json:"voice,omitempty"`
	Instructions      string    `json:"instructions,omitempty"`
	Tools             []oaiTool `json:"tools,omitempty"`
	InputAudioFormat  string    `json:"input_audio_format"`
	OutputAudioFormat string    `json:"output_audio_format"`
}

type oaiTool struct {
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type appendAudioMessage struct {
	Type  string `json:"type"`
	Audio string `json:"audio"` // base64-encoded PCM16
}

type createConversationItemMessage struct {
	Type string           `json:"type"`
	Item conversationItem `json:"item"`
}

type conversationItem struct {
	Type    string             `json:"type"`
	Role    string             `json:"role,omitempty"`
	Content []conversationPart `json:"content,omitempty"`
	CallID  string             `json:"call_id,omitempty"`
	Output  string             `json:"output,omitempty"`
}

type conversationPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// serverErrorDetail represents the nested error object in an OpenAI Realtime
// error event: {"type":"error","error":{"type":"...","code":"...","message":"..."}}.
type serverErrorDetail struct {
	Type    string `json:"type"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

// ── Protocol message types (incoming) ─────────────────────────────────────────

type serverEvent struct {
	Type string `json:"type"`

	// response.audio.delta / response.audio_transcript.delta /
	// conversation.item.input_audio_transcription.completed (field name differs)
	Delta string `json:"delta,omitempty"`

	// conversation.item.input_audio_transcription.completed
	Transcript string `json:"transcript,omitempty"`

	// response.function_call_arguments.done
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	CallID    string `json:"call_id,omitempty"`

	// error event
	Error *serverErrorDetail `json:"error,omitempty"`
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
	closed bool

	// currentTxText accumulates response.audio_transcript.delta events until
	// response.audio_transcript.done is received.
	currentTxText string

	ctx       context.Context
	cancel    context.CancelFunc
	closeOnce sync.Once
}

// sendSessionUpdate sends a session.update event to configure voice, instructions,
// tools and audio formats.
func (s *session) sendSessionUpdate(voice tts.VoiceProfile, instructions string, tools []llm.ToolDefinition) error {
	params := sessionParams{
		InputAudioFormat:  "pcm16",
		OutputAudioFormat: "pcm16",
	}
	if voice.ID != "" {
		params.Voice = voice.ID
	}
	if instructions != "" {
		params.Instructions = instructions
	}
	if len(tools) > 0 {
		params.Tools = toOAITools(tools)
	}
	return s.writeJSON(sessionUpdateMessage{Type: "session.update", Session: params})
}

// writeJSON marshals v and writes it as a text WebSocket message.
func (s *session) writeJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("openai: marshal: %w", err)
	}
	return s.conn.Write(s.ctx, websocket.MessageText, data)
}

// receiveLoop reads events from the WebSocket and dispatches them.
// It owns audioCh and transcripts: it closes both when it exits.
func (s *session) receiveLoop() {
	defer s.closeChannels()

	for {
		_, data, err := s.conn.Read(s.ctx)
		if err != nil {
			if s.ctx.Err() != nil {
				return
			}
			s.setErr(err)
			return
		}

		var evt serverEvent
		if err := json.Unmarshal(data, &evt); err != nil {
			continue
		}

		s.handleServerEvent(&evt)
	}
}

func (s *session) handleServerEvent(evt *serverEvent) {
	switch evt.Type {
	case "response.audio.delta":
		if evt.Delta == "" {
			return
		}
		audioData, err := base64.StdEncoding.DecodeString(evt.Delta)
		if err != nil || len(audioData) == 0 {
			return
		}
		select {
		case s.audioCh <- audioData:
		case <-s.ctx.Done():
		}

	case "response.audio_transcript.delta":
		if evt.Delta == "" {
			return
		}
		s.mu.Lock()
		s.currentTxText += evt.Delta
		s.mu.Unlock()

	case "response.audio_transcript.done":
		s.mu.Lock()
		text := s.currentTxText
		s.currentTxText = ""
		s.mu.Unlock()

		if text == "" {
			return
		}
		entry := memory.TranscriptEntry{
			SpeakerID:   "assistant",
			SpeakerName: "NPC",
			Text:        text,
			NPCID:       "openai",
			Timestamp:   time.Now(),
		}
		select {
		case s.transcripts <- entry:
		case <-s.ctx.Done():
		}

	case "conversation.item.input_audio_transcription.completed":
		if evt.Transcript == "" {
			return
		}
		entry := memory.TranscriptEntry{
			SpeakerID:   "user",
			SpeakerName: "User",
			Text:        evt.Transcript,
			Timestamp:   time.Now(),
		}
		select {
		case s.transcripts <- entry:
		case <-s.ctx.Done():
		}

	case "response.function_call_arguments.done":
		s.handleFunctionCall(evt)

	case "error":
		s.handleErrorEvent(evt)
	}
}

func (s *session) handleErrorEvent(evt *serverEvent) {
	s.mu.Lock()
	handler := s.errorHandler
	s.mu.Unlock()

	if handler == nil {
		return
	}

	msg := "unknown error"
	if evt.Error != nil && evt.Error.Message != "" {
		msg = evt.Error.Message
	}
	handler(fmt.Errorf("openai: %s", msg))
}

func (s *session) handleFunctionCall(evt *serverEvent) {
	s.mu.Lock()
	handler := s.toolHandler
	s.mu.Unlock()

	if handler == nil {
		return
	}

	result, callErr := handler(evt.Name, evt.Arguments)
	if callErr != nil {
		result = fmt.Sprintf(`{"error": %q}`, callErr.Error())
	}

	// Return tool result and trigger the next model response.
	_ = s.writeJSON(createConversationItemMessage{
		Type: "conversation.item.create",
		Item: conversationItem{
			Type:   "function_call_output",
			CallID: evt.CallID,
			Output: result,
		},
	})
	_ = s.writeJSON(map[string]string{"type": "response.create"})
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

// toOAITools converts llm.ToolDefinition slice to OpenAI Realtime tool format.
func toOAITools(tools []llm.ToolDefinition) []oaiTool {
	out := make([]oaiTool, len(tools))
	for i, t := range tools {
		out[i] = oaiTool{
			Type:        "function",
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.Parameters,
		}
	}
	return out
}

// ── SessionHandle methods ──────────────────────────────────────────────────────

// SendAudio delivers a raw PCM16 audio chunk to the model.
func (s *session) SendAudio(chunk []byte) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("openai: session closed")
	}
	s.mu.Unlock()

	encoded := base64.StdEncoding.EncodeToString(chunk)
	return s.writeJSON(appendAudioMessage{
		Type:  "input_audio_buffer.append",
		Audio: encoded,
	})
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

// SetTools replaces the active tools by sending a session.update event.
func (s *session) SetTools(tools []llm.ToolDefinition) error {
	params := sessionParams{
		Tools:             toOAITools(tools),
		InputAudioFormat:  "pcm16",
		OutputAudioFormat: "pcm16",
	}
	return s.writeJSON(sessionUpdateMessage{Type: "session.update", Session: params})
}

// UpdateInstructions replaces the system instructions by sending a session.update
// event.
func (s *session) UpdateInstructions(instructions string) error {
	params := sessionParams{
		Instructions:      instructions,
		InputAudioFormat:  "pcm16",
		OutputAudioFormat: "pcm16",
	}
	return s.writeJSON(sessionUpdateMessage{Type: "session.update", Session: params})
}

// InjectTextContext inserts ContextItems as conversation.item.create events.
func (s *session) InjectTextContext(items []s2s.ContextItem) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("openai: session closed")
	}
	s.mu.Unlock()

	for _, item := range items {
		role := item.Role
		// OpenAI Realtime supports "user", "assistant", and "system" roles for
		// conversation items. Unknown roles are coerced to "user".
		switch role {
		case "assistant", "system":
			// keep as-is
		default:
			role = "user"
		}

		// Choose the content part type based on role: assistant messages use
		// "text", everything else uses "input_text".
		partType := "input_text"
		if role == "assistant" {
			partType = "text"
		}

		msg := createConversationItemMessage{
			Type: "conversation.item.create",
			Item: conversationItem{
				Type: "message",
				Role: role,
				Content: []conversationPart{
					{Type: partType, Text: item.Content},
				},
			},
		}
		if err := s.writeJSON(msg); err != nil {
			return err
		}
	}
	return nil
}

// Interrupt sends a response.cancel event to stop the current model response.
func (s *session) Interrupt() error {
	return s.writeJSON(map[string]string{"type": "response.cancel"})
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

	s.cancel()
	s.conn.Close(websocket.StatusNormalClosure, "session closed")
	return nil
}
