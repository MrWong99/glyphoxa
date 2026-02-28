package openai_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MrWong99/glyphoxa/pkg/provider/llm"
	"github.com/MrWong99/glyphoxa/pkg/provider/s2s"
	"github.com/MrWong99/glyphoxa/pkg/provider/s2s/openai"
	"github.com/MrWong99/glyphoxa/pkg/provider/tts"
	"github.com/coder/websocket"
)

// ── Compile-time interface assertions ─────────────────────────────────────────

// TestInterfaceSatisfaction verifies that the exported types satisfy the s2s
// interfaces at compile time (the real assertions are blank-identifier vars
// inside openai.go).
func TestInterfaceSatisfaction(t *testing.T) {
	t.Parallel()
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// wsURL converts an httptest server HTTP URL to a WebSocket URL.
func wsURL(srv *httptest.Server) string {
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

// startOpenAIServer launches a test WebSocket server. The handler receives the
// accepted conn. The server is automatically closed when the test finishes.
func startOpenAIServer(t *testing.T, handler func(conn *websocket.Conn, r *http.Request)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")
		handler(conn, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// readJSON reads one WebSocket text frame and decodes it into v.
func readJSON(t *testing.T, conn *websocket.Conn, v any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("readJSON: %v", err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("readJSON unmarshal: %v", err)
	}
}

// writeJSON marshals v and sends it as a text frame.
func writeJSON(t *testing.T, conn *websocket.Conn, v any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	data, _ := json.Marshal(v)
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		t.Logf("writeJSON: %v (may be expected on close)", err)
	}
}

// ── Option constructor tests ───────────────────────────────────────────────────

func TestNew_DefaultValues(t *testing.T) {
	t.Parallel()
	p := openai.New("my-key")
	if p == nil {
		t.Fatal("New returned nil")
	}
}

func TestWithModel_SetsModel(t *testing.T) {
	t.Parallel()

	modelInURL := make(chan string, 1)

	srv := startOpenAIServer(t, func(conn *websocket.Conn, r *http.Request) {
		modelInURL <- r.URL.Query().Get("model")
		var raw map[string]any
		readJSON(t, conn, &raw)
		<-conn.CloseRead(context.Background()).Done()
	})

	p := openai.New("key", openai.WithModel("gpt-4o-mini-realtime"), openai.WithBaseURL(wsURL(srv)))
	handle, err := p.Connect(context.Background(), s2s.SessionConfig{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer handle.Close()

	select {
	case m := <-modelInURL:
		if m != "gpt-4o-mini-realtime" {
			t.Errorf("model in URL = %q; want gpt-4o-mini-realtime", m)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}
}

func TestWithBaseURL_SetsBaseURL(t *testing.T) {
	t.Parallel()
	connected := make(chan struct{}, 1)

	srv := startOpenAIServer(t, func(conn *websocket.Conn, _ *http.Request) {
		var raw map[string]any
		readJSON(t, conn, &raw)
		connected <- struct{}{}
		<-conn.CloseRead(context.Background()).Done()
	})

	p := openai.New("key", openai.WithBaseURL(wsURL(srv)))
	handle, err := p.Connect(context.Background(), s2s.SessionConfig{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer handle.Close()

	select {
	case <-connected:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout: server never received connection")
	}
}

// ── TestCapabilities ───────────────────────────────────────────────────────────

func TestCapabilities_NonEmpty(t *testing.T) {
	t.Parallel()
	p := openai.New("key")
	caps := p.Capabilities()
	if caps.ContextWindow == 0 {
		t.Error("ContextWindow should be non-zero")
	}
	if len(caps.Voices) == 0 {
		t.Error("Voices should be non-empty")
	}
}

// ── TestConnect_SendsSessionUpdate ────────────────────────────────────────────

func TestConnect_SendsSessionUpdate(t *testing.T) {
	t.Parallel()

	type sessionUpdateMsg struct {
		Type    string `json:"type"`
		Session struct {
			Voice        string `json:"voice"`
			Instructions string `json:"instructions"`
			Tools        []struct {
				Type string `json:"type"`
				Name string `json:"name"`
			} `json:"tools"`
			InputAudioFormat  string `json:"input_audio_format"`
			OutputAudioFormat string `json:"output_audio_format"`
		} `json:"session"`
	}

	received := make(chan sessionUpdateMsg, 1)

	srv := startOpenAIServer(t, func(conn *websocket.Conn, _ *http.Request) {
		var msg sessionUpdateMsg
		readJSON(t, conn, &msg)
		received <- msg
		<-conn.CloseRead(context.Background()).Done()
	})

	p := openai.New("key", openai.WithBaseURL(wsURL(srv)))
	cfg := s2s.SessionConfig{
		Voice:        tts.VoiceProfile{ID: "alloy"},
		Instructions: "You are a helpful NPC.",
		Tools:        []llm.ToolDefinition{{Name: "attack", Description: "Attacks an enemy"}},
	}
	handle, err := p.Connect(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer handle.Close()

	select {
	case msg := <-received:
		if msg.Type != "session.update" {
			t.Errorf("type = %q; want session.update", msg.Type)
		}
		if msg.Session.Voice != "alloy" {
			t.Errorf("voice = %q; want alloy", msg.Session.Voice)
		}
		if msg.Session.Instructions != "You are a helpful NPC." {
			t.Errorf("instructions = %q", msg.Session.Instructions)
		}
		if msg.Session.InputAudioFormat != "pcm16" {
			t.Errorf("input_audio_format = %q; want pcm16", msg.Session.InputAudioFormat)
		}
		if msg.Session.OutputAudioFormat != "pcm16" {
			t.Errorf("output_audio_format = %q; want pcm16", msg.Session.OutputAudioFormat)
		}
		if len(msg.Session.Tools) == 0 {
			t.Error("tools should be non-empty")
		} else if msg.Session.Tools[0].Name != "attack" {
			t.Errorf("tool[0].name = %q; want attack", msg.Session.Tools[0].Name)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for session.update")
	}
}

func TestConnect_SendsAuthHeaders(t *testing.T) {
	t.Parallel()

	authHeader := make(chan string, 1)

	srv := startOpenAIServer(t, func(conn *websocket.Conn, r *http.Request) {
		authHeader <- r.Header.Get("Authorization")
		var raw map[string]any
		readJSON(t, conn, &raw)
		<-conn.CloseRead(context.Background()).Done()
	})

	p := openai.New("my-secret-token", openai.WithBaseURL(wsURL(srv)))
	handle, err := p.Connect(context.Background(), s2s.SessionConfig{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer handle.Close()

	select {
	case auth := <-authHeader:
		if auth != "Bearer my-secret-token" {
			t.Errorf("Authorization = %q; want Bearer my-secret-token", auth)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}
}

// ── TestSendAudio ──────────────────────────────────────────────────────────────

func TestSendAudio_EncodesAndSends(t *testing.T) {
	t.Parallel()

	type appendMsg struct {
		Type  string `json:"type"`
		Audio string `json:"audio"`
	}

	audioMsg := make(chan appendMsg, 1)

	srv := startOpenAIServer(t, func(conn *websocket.Conn, _ *http.Request) {
		// Consume session.update.
		var raw map[string]any
		readJSON(t, conn, &raw)

		// Read audio append message.
		var msg appendMsg
		readJSON(t, conn, &msg)
		audioMsg <- msg

		<-conn.CloseRead(context.Background()).Done()
	})

	p := openai.New("key", openai.WithBaseURL(wsURL(srv)))
	handle, err := p.Connect(context.Background(), s2s.SessionConfig{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer handle.Close()

	wantPCM := []byte{0x10, 0x20, 0x30, 0x40}
	if err := handle.SendAudio(wantPCM); err != nil {
		t.Fatalf("SendAudio: %v", err)
	}

	select {
	case msg := <-audioMsg:
		if msg.Type != "input_audio_buffer.append" {
			t.Errorf("type = %q; want input_audio_buffer.append", msg.Type)
		}
		got, err := base64.StdEncoding.DecodeString(msg.Audio)
		if err != nil {
			t.Fatalf("base64 decode: %v", err)
		}
		if string(got) != string(wantPCM) {
			t.Errorf("decoded audio = %v; want %v", got, wantPCM)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for audio append message")
	}
}

func TestSendAudio_AfterClose_ReturnsError(t *testing.T) {
	t.Parallel()

	srv := startOpenAIServer(t, func(conn *websocket.Conn, _ *http.Request) {
		var raw map[string]any
		readJSON(t, conn, &raw)
		<-conn.CloseRead(context.Background()).Done()
	})

	p := openai.New("key", openai.WithBaseURL(wsURL(srv)))
	handle, err := p.Connect(context.Background(), s2s.SessionConfig{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	_ = handle.Close()

	if err := handle.SendAudio([]byte{1, 2, 3}); err == nil {
		t.Fatal("SendAudio after Close should return an error")
	}
}

// ── TestAudio ──────────────────────────────────────────────────────────────────

func TestAudio_DeliversDecodedPCM(t *testing.T) {
	t.Parallel()

	wantPCM := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	encoded := base64.StdEncoding.EncodeToString(wantPCM)

	srv := startOpenAIServer(t, func(conn *websocket.Conn, _ *http.Request) {
		var raw map[string]any
		readJSON(t, conn, &raw)

		writeJSON(t, conn, map[string]any{
			"type":  "response.audio.delta",
			"delta": encoded,
		})

		<-conn.CloseRead(context.Background()).Done()
	})

	p := openai.New("key", openai.WithBaseURL(wsURL(srv)))
	handle, err := p.Connect(context.Background(), s2s.SessionConfig{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer handle.Close()

	select {
	case chunk, ok := <-handle.Audio():
		if !ok {
			t.Fatal("Audio channel closed unexpectedly")
		}
		if string(chunk) != string(wantPCM) {
			t.Errorf("audio chunk = %v; want %v", chunk, wantPCM)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for audio chunk")
	}
}

func TestAudio_ChannelNotNil(t *testing.T) {
	t.Parallel()

	srv := startOpenAIServer(t, func(conn *websocket.Conn, _ *http.Request) {
		var raw map[string]any
		readJSON(t, conn, &raw)
		<-conn.CloseRead(context.Background()).Done()
	})

	p := openai.New("key", openai.WithBaseURL(wsURL(srv)))
	handle, err := p.Connect(context.Background(), s2s.SessionConfig{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer handle.Close()

	if handle.Audio() == nil {
		t.Error("Audio() returned nil channel")
	}
}

// ── TestTranscripts ────────────────────────────────────────────────────────────

func TestTranscripts_AssemblesFromDeltas(t *testing.T) {
	t.Parallel()

	srv := startOpenAIServer(t, func(conn *websocket.Conn, _ *http.Request) {
		var raw map[string]any
		readJSON(t, conn, &raw)

		// Send two deltas followed by done.
		writeJSON(t, conn, map[string]any{"type": "response.audio_transcript.delta", "delta": "Hello "})
		writeJSON(t, conn, map[string]any{"type": "response.audio_transcript.delta", "delta": "world!"})
		writeJSON(t, conn, map[string]any{"type": "response.audio_transcript.done"})

		<-conn.CloseRead(context.Background()).Done()
	})

	p := openai.New("key", openai.WithBaseURL(wsURL(srv)))
	handle, err := p.Connect(context.Background(), s2s.SessionConfig{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer handle.Close()

	select {
	case entry, ok := <-handle.Transcripts():
		if !ok {
			t.Fatal("Transcripts channel closed unexpectedly")
		}
		if entry.Text != "Hello world!" {
			t.Errorf("transcript text = %q; want %q", entry.Text, "Hello world!")
		}
		if !entry.IsNPC() {
			t.Error("expected NPC transcript (NPCID should be set)")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for transcript")
	}
}

func TestTranscripts_UserSpeechTranscription(t *testing.T) {
	t.Parallel()

	srv := startOpenAIServer(t, func(conn *websocket.Conn, _ *http.Request) {
		var raw map[string]any
		readJSON(t, conn, &raw)

		// Send a user speech transcription completed event.
		writeJSON(t, conn, map[string]any{
			"type":       "conversation.item.input_audio_transcription.completed",
			"transcript": "Cast a fireball!",
		})

		<-conn.CloseRead(context.Background()).Done()
	})

	p := openai.New("key", openai.WithBaseURL(wsURL(srv)))
	handle, err := p.Connect(context.Background(), s2s.SessionConfig{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer handle.Close()

	select {
	case entry, ok := <-handle.Transcripts():
		if !ok {
			t.Fatal("Transcripts channel closed unexpectedly")
		}
		if entry.Text != "Cast a fireball!" {
			t.Errorf("transcript text = %q; want %q", entry.Text, "Cast a fireball!")
		}
		if entry.IsNPC() {
			t.Error("user transcription should not have NPCID set")
		}
		if entry.SpeakerID != "user" {
			t.Errorf("speakerID = %q; want user", entry.SpeakerID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for user transcription")
	}
}

func TestTranscripts_ChannelNotNil(t *testing.T) {
	t.Parallel()

	srv := startOpenAIServer(t, func(conn *websocket.Conn, _ *http.Request) {
		var raw map[string]any
		readJSON(t, conn, &raw)
		<-conn.CloseRead(context.Background()).Done()
	})

	p := openai.New("key", openai.WithBaseURL(wsURL(srv)))
	handle, err := p.Connect(context.Background(), s2s.SessionConfig{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer handle.Close()

	if handle.Transcripts() == nil {
		t.Error("Transcripts() returned nil channel")
	}
}

// ── TestOnError ─────────────────────────────────────────────────────────────────────

func TestOnError_InvokesHandler(t *testing.T) {
	t.Parallel()

	srv := startOpenAIServer(t, func(conn *websocket.Conn, _ *http.Request) {
		var raw map[string]any
		readJSON(t, conn, &raw)

		// Send an error event.
		writeJSON(t, conn, map[string]any{
			"type": "error",
			"error": map[string]any{
				"type":    "invalid_request_error",
				"code":    "audio_unintelligible",
				"message": "Could not understand audio.",
			},
		})

		<-conn.CloseRead(context.Background()).Done()
	})

	p := openai.New("key", openai.WithBaseURL(wsURL(srv)))
	handle, err := p.Connect(context.Background(), s2s.SessionConfig{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer handle.Close()

	errCh := make(chan error, 1)
	handle.OnError(func(e error) {
		errCh <- e
	})

	select {
	case gotErr := <-errCh:
		if gotErr == nil {
			t.Fatal("OnError handler called with nil error")
		}
		if !strings.Contains(gotErr.Error(), "Could not understand audio") {
			t.Errorf("error = %q; want substring %q", gotErr, "Could not understand audio")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for OnError handler to be called")
	}
}

func TestOnError_NilHandlerIgnoresError(t *testing.T) {
	t.Parallel()

	errorSent := make(chan struct{}, 1)

	srv := startOpenAIServer(t, func(conn *websocket.Conn, _ *http.Request) {
		var raw map[string]any
		readJSON(t, conn, &raw)

		writeJSON(t, conn, map[string]any{
			"type": "error",
			"error": map[string]any{
				"type":    "server_error",
				"message": "Transient failure",
			},
		})
		close(errorSent)

		time.Sleep(200 * time.Millisecond)
		<-conn.CloseRead(context.Background()).Done()
	})

	p := openai.New("key", openai.WithBaseURL(wsURL(srv)))
	handle, err := p.Connect(context.Background(), s2s.SessionConfig{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer handle.Close()

	// No handler registered — should not panic.
	select {
	case <-errorSent:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}
	time.Sleep(50 * time.Millisecond)
}

// ── TestOnToolCall ─────────────────────────────────────────────────────────────

func TestOnToolCall_RoutesToolCallToHandler(t *testing.T) {
	t.Parallel()

	toolResponseReceived := make(chan string, 1)

	srv := startOpenAIServer(t, func(conn *websocket.Conn, _ *http.Request) {
		// Consume session.update.
		var raw map[string]any
		readJSON(t, conn, &raw)

		// Send a function_call_arguments.done event.
		writeJSON(t, conn, map[string]any{
			"type":      "response.function_call_arguments.done",
			"name":      "cast_spell",
			"arguments": `{"spell":"fireball"}`,
			"call_id":   "call-42",
		})

		// Read conversation.item.create (tool result).
		var resp map[string]any
		readJSON(t, conn, &resp)
		data, _ := json.Marshal(resp)
		toolResponseReceived <- string(data)

		<-conn.CloseRead(context.Background()).Done()
	})

	p := openai.New("key", openai.WithBaseURL(wsURL(srv)))
	handle, err := p.Connect(context.Background(), s2s.SessionConfig{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer handle.Close()

	handlerCalled := make(chan string, 1)
	handle.OnToolCall(func(name, args string) (string, error) {
		handlerCalled <- name + ":" + args
		return `{"result":"spell cast"}`, nil
	})

	select {
	case call := <-handlerCalled:
		if !strings.HasPrefix(call, "cast_spell:") {
			t.Errorf("handler called with %q; want prefix cast_spell:", call)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for handler to be called")
	}

	select {
	case respStr := <-toolResponseReceived:
		if !strings.Contains(respStr, "conversation.item.create") {
			t.Errorf("expected conversation.item.create in response, got %q", respStr)
		}
		if !strings.Contains(respStr, "call-42") {
			t.Errorf("expected call_id call-42 in response, got %q", respStr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for tool response")
	}
}

func TestOnToolCall_NilHandlerSkipsCall(t *testing.T) {
	t.Parallel()

	sent := make(chan struct{}, 1)

	srv := startOpenAIServer(t, func(conn *websocket.Conn, _ *http.Request) {
		var raw map[string]any
		readJSON(t, conn, &raw)

		writeJSON(t, conn, map[string]any{
			"type":      "response.function_call_arguments.done",
			"name":      "do_thing",
			"arguments": `{}`,
			"call_id":   "c1",
		})
		close(sent)

		time.Sleep(200 * time.Millisecond)
		<-conn.CloseRead(context.Background()).Done()
	})

	p := openai.New("key", openai.WithBaseURL(wsURL(srv)))
	handle, err := p.Connect(context.Background(), s2s.SessionConfig{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer handle.Close()

	// No handler — should not panic.
	select {
	case <-sent:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}
	time.Sleep(50 * time.Millisecond)
}

// ── TestSetTools ───────────────────────────────────────────────────────────────

func TestSetTools_SendsSessionUpdate(t *testing.T) {
	t.Parallel()

	type sessionUpdateMsg struct {
		Type    string `json:"type"`
		Session struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"session"`
	}

	updates := make(chan sessionUpdateMsg, 2)

	srv := startOpenAIServer(t, func(conn *websocket.Conn, _ *http.Request) {
		// Initial session.update.
		var initial sessionUpdateMsg
		readJSON(t, conn, &initial)
		updates <- initial

		// SetTools session.update.
		var second sessionUpdateMsg
		readJSON(t, conn, &second)
		updates <- second

		<-conn.CloseRead(context.Background()).Done()
	})

	p := openai.New("key", openai.WithBaseURL(wsURL(srv)))
	handle, err := p.Connect(context.Background(), s2s.SessionConfig{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer handle.Close()

	// Drain initial update.
	select {
	case <-updates:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for initial session.update")
	}

	newTools := []llm.ToolDefinition{{Name: "new_power", Description: "A new ability"}}
	if err := handle.SetTools(newTools); err != nil {
		t.Fatalf("SetTools: %v", err)
	}

	select {
	case msg := <-updates:
		if msg.Type != "session.update" {
			t.Errorf("type = %q; want session.update", msg.Type)
		}
		if len(msg.Session.Tools) == 0 {
			t.Fatal("expected tools in session.update")
		}
		if msg.Session.Tools[0].Name != "new_power" {
			t.Errorf("tool name = %q; want new_power", msg.Session.Tools[0].Name)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for SetTools session.update")
	}
}

// ── TestUpdateInstructions ────────────────────────────────────────────────────

func TestUpdateInstructions_SendsSessionUpdate(t *testing.T) {
	t.Parallel()

	type sessionUpdateMsg struct {
		Type    string `json:"type"`
		Session struct {
			Instructions string `json:"instructions"`
		} `json:"session"`
	}

	updates := make(chan sessionUpdateMsg, 2)

	srv := startOpenAIServer(t, func(conn *websocket.Conn, _ *http.Request) {
		var initial sessionUpdateMsg
		readJSON(t, conn, &initial)
		updates <- initial

		var second sessionUpdateMsg
		readJSON(t, conn, &second)
		updates <- second

		<-conn.CloseRead(context.Background()).Done()
	})

	p := openai.New("key", openai.WithBaseURL(wsURL(srv)))
	handle, err := p.Connect(context.Background(), s2s.SessionConfig{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer handle.Close()

	// Drain initial.
	select {
	case <-updates:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for initial update")
	}

	if err := handle.UpdateInstructions("Be more aggressive."); err != nil {
		t.Fatalf("UpdateInstructions: %v", err)
	}

	select {
	case msg := <-updates:
		if msg.Type != "session.update" {
			t.Errorf("type = %q; want session.update", msg.Type)
		}
		if msg.Session.Instructions != "Be more aggressive." {
			t.Errorf("instructions = %q; want %q", msg.Session.Instructions, "Be more aggressive.")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for UpdateInstructions session.update")
	}
}

// ── TestInjectTextContext ──────────────────────────────────────────────────────

func TestInjectTextContext_SendsConversationItems(t *testing.T) {
	t.Parallel()

	type itemMsg struct {
		Type string `json:"type"`
		Item struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"item"`
	}

	items := make(chan itemMsg, 2)

	srv := startOpenAIServer(t, func(conn *websocket.Conn, _ *http.Request) {
		// Consume session.update.
		var raw map[string]any
		readJSON(t, conn, &raw)

		// Read two conversation.item.create messages.
		var msg1, msg2 itemMsg
		readJSON(t, conn, &msg1)
		items <- msg1
		readJSON(t, conn, &msg2)
		items <- msg2

		<-conn.CloseRead(context.Background()).Done()
	})

	p := openai.New("key", openai.WithBaseURL(wsURL(srv)))
	handle, err := p.Connect(context.Background(), s2s.SessionConfig{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer handle.Close()

	ctxItems := []s2s.ContextItem{
		{Role: "user", Content: "Dragon spotted!"},
		{Role: "assistant", Content: "I'll handle it."},
	}
	if err := handle.InjectTextContext(ctxItems); err != nil {
		t.Fatalf("InjectTextContext: %v", err)
	}

	for i, want := range ctxItems {
		select {
		case msg := <-items:
			if msg.Type != "conversation.item.create" {
				t.Errorf("item[%d] type = %q; want conversation.item.create", i, msg.Type)
			}
			if len(msg.Item.Content) == 0 {
				t.Errorf("item[%d] has no content", i)
				continue
			}
			if msg.Item.Content[0].Text != want.Content {
				t.Errorf("item[%d] text = %q; want %q", i, msg.Item.Content[0].Text, want.Content)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("timeout waiting for conversation item %d", i)
		}
	}
}

func TestInjectTextContext_AfterClose_ReturnsError(t *testing.T) {
	t.Parallel()

	srv := startOpenAIServer(t, func(conn *websocket.Conn, _ *http.Request) {
		var raw map[string]any
		readJSON(t, conn, &raw)
		<-conn.CloseRead(context.Background()).Done()
	})

	p := openai.New("key", openai.WithBaseURL(wsURL(srv)))
	handle, err := p.Connect(context.Background(), s2s.SessionConfig{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	_ = handle.Close()

	if err := handle.InjectTextContext([]s2s.ContextItem{{Role: "user", Content: "hi"}}); err == nil {
		t.Error("InjectTextContext after Close should return an error")
	}
}

// ── TestInterrupt ──────────────────────────────────────────────────────────────

func TestInterrupt_SendsResponseCancel(t *testing.T) {
	t.Parallel()

	type cancelMsg struct {
		Type string `json:"type"`
	}

	cancelReceived := make(chan cancelMsg, 1)

	srv := startOpenAIServer(t, func(conn *websocket.Conn, _ *http.Request) {
		var raw map[string]any
		readJSON(t, conn, &raw)

		var msg cancelMsg
		readJSON(t, conn, &msg)
		cancelReceived <- msg

		<-conn.CloseRead(context.Background()).Done()
	})

	p := openai.New("key", openai.WithBaseURL(wsURL(srv)))
	handle, err := p.Connect(context.Background(), s2s.SessionConfig{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer handle.Close()

	if err := handle.Interrupt(); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}

	select {
	case msg := <-cancelReceived:
		if msg.Type != "response.cancel" {
			t.Errorf("type = %q; want response.cancel", msg.Type)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for response.cancel")
	}
}

// ── TestClose_Idempotent ───────────────────────────────────────────────────────

func TestClose_Idempotent(t *testing.T) {
	t.Parallel()

	srv := startOpenAIServer(t, func(conn *websocket.Conn, _ *http.Request) {
		var raw map[string]any
		readJSON(t, conn, &raw)
		<-conn.CloseRead(context.Background()).Done()
	})

	p := openai.New("key", openai.WithBaseURL(wsURL(srv)))
	handle, err := p.Connect(context.Background(), s2s.SessionConfig{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	if err := handle.Close(); err != nil {
		t.Fatalf("first Close() returned error: %v", err)
	}
	if err := handle.Close(); err != nil {
		t.Fatalf("second Close() returned error: %v", err)
	}
}

func TestClose_ClosesAudioChannel(t *testing.T) {
	t.Parallel()

	srv := startOpenAIServer(t, func(conn *websocket.Conn, _ *http.Request) {
		var raw map[string]any
		readJSON(t, conn, &raw)
		<-conn.CloseRead(context.Background()).Done()
	})

	p := openai.New("key", openai.WithBaseURL(wsURL(srv)))
	handle, err := p.Connect(context.Background(), s2s.SessionConfig{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	_ = handle.Close()

	select {
	case _, open := <-handle.Audio():
		if open {
			t.Error("Audio channel should be closed after Close()")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for Audio channel to close")
	}
}

func TestClose_ClosesTranscriptsChannel(t *testing.T) {
	t.Parallel()

	srv := startOpenAIServer(t, func(conn *websocket.Conn, _ *http.Request) {
		var raw map[string]any
		readJSON(t, conn, &raw)
		<-conn.CloseRead(context.Background()).Done()
	})

	p := openai.New("key", openai.WithBaseURL(wsURL(srv)))
	handle, err := p.Connect(context.Background(), s2s.SessionConfig{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	_ = handle.Close()

	select {
	case _, open := <-handle.Transcripts():
		if open {
			t.Error("Transcripts channel should be closed after Close()")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for Transcripts channel to close")
	}
}

// ── TestConcurrentSendAudio ────────────────────────────────────────────────────

func TestConcurrentSendAudio_DoesNotRace(t *testing.T) {
	t.Parallel()

	srv := startOpenAIServer(t, func(conn *websocket.Conn, _ *http.Request) {
		var raw map[string]any
		readJSON(t, conn, &raw)

		ctx := context.Background()
		for {
			_, _, err := conn.Read(ctx)
			if err != nil {
				return
			}
		}
	})

	p := openai.New("key", openai.WithBaseURL(wsURL(srv)))
	handle, err := p.Connect(context.Background(), s2s.SessionConfig{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer handle.Close()

	const goroutines = 8
	const chunksPerGoroutine = 16

	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			for range chunksPerGoroutine {
				_ = handle.SendAudio([]byte{0xCA, 0xFE, 0xBA, 0xBE})
			}
		})
	}
	wg.Wait()
}

// ── TestErr ────────────────────────────────────────────────────────────────────

func TestErr_NilBeforeError(t *testing.T) {
	t.Parallel()

	srv := startOpenAIServer(t, func(conn *websocket.Conn, _ *http.Request) {
		var raw map[string]any
		readJSON(t, conn, &raw)
		<-conn.CloseRead(context.Background()).Done()
	})

	p := openai.New("key", openai.WithBaseURL(wsURL(srv)))
	handle, err := p.Connect(context.Background(), s2s.SessionConfig{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer handle.Close()

	if got := handle.Err(); got != nil {
		t.Errorf("Err() = %v; want nil before any error", got)
	}
}

// ── TestConnect_CancelledContext ───────────────────────────────────────────────

func TestConnect_CancelledContext_ReturnsError(t *testing.T) {
	t.Parallel()

	srv := startOpenAIServer(t, func(conn *websocket.Conn, _ *http.Request) {
		<-conn.CloseRead(context.Background()).Done()
	})

	p := openai.New("key", openai.WithBaseURL(wsURL(srv)))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := p.Connect(ctx, s2s.SessionConfig{})
	if err == nil {
		t.Fatal("Connect with cancelled context should return an error")
	}
}

// ── TestSerializationRoundtrip ────────────────────────────────────────────────

func TestSerializationRoundtrip_AudioDelta(t *testing.T) {
	t.Parallel()

	// Verify that base64 encoding/decoding is symmetric.
	raw := []byte("test audio data 12345")
	encoded := base64.StdEncoding.EncodeToString(raw)
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(decoded) != string(raw) {
		t.Errorf("roundtrip mismatch: got %q, want %q", decoded, raw)
	}
}
