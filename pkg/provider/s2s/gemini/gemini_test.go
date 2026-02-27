package gemini_test

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
	"github.com/MrWong99/glyphoxa/pkg/provider/s2s/gemini"
	"github.com/MrWong99/glyphoxa/pkg/provider/tts"
	"github.com/coder/websocket"
)

// ── Compile-time interface assertions ─────────────────────────────────────────

// TestInterfaceSatisfaction verifies that the exported types satisfy the s2s
// interfaces at compile time. The real assertions are the blank-identifier
// variables in gemini.go; this test ensures those vars exist and the package
// compiles cleanly.
func TestInterfaceSatisfaction(t *testing.T) {
	t.Parallel()
	// Nothing to do at runtime – the compiler enforces the contracts.
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// wsURL converts an httptest server HTTP URL to a WebSocket URL.
func wsURL(srv *httptest.Server) string {
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

// startGeminiServer launches a test WebSocket server. The handler function
// receives the accepted *websocket.Conn. The server is automatically closed
// when the test finishes.
func startGeminiServer(t *testing.T, handler func(conn *websocket.Conn, r *http.Request)) *httptest.Server {
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

// sendSetupComplete sends the server-side setupComplete ack.
func sendSetupComplete(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	writeJSON(t, conn, map[string]any{"setupComplete": map[string]any{}})
}

// newProvider creates a Provider pointing at the given test server.
func newProvider(srv *httptest.Server) *gemini.Provider {
	return gemini.New("test-api-key", gemini.WithBaseURL(wsURL(srv)))
}

// ── Option constructor tests ───────────────────────────────────────────────────

func TestNew_DefaultValues(t *testing.T) {
	t.Parallel()
	p := gemini.New("my-key")
	if p == nil {
		t.Fatal("New returned nil")
	}
}

func TestWithModel_SetsModel(t *testing.T) {
	t.Parallel()

	modelCh := make(chan string, 1)

	srv := startGeminiServer(t, func(conn *websocket.Conn, _ *http.Request) {
		var msg struct {
			Setup struct {
				Model string `json:"model"`
			} `json:"setup"`
		}
		readJSON(t, conn, &msg)
		modelCh <- msg.Setup.Model
		sendSetupComplete(t, conn)
		<-conn.CloseRead(context.Background()).Done()
	})

	p := gemini.New("key", gemini.WithModel("custom-model"), gemini.WithBaseURL(wsURL(srv)))
	handle, err := p.Connect(context.Background(), s2s.SessionConfig{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer handle.Close()

	select {
	case model := <-modelCh:
		if want := "models/custom-model"; model != want {
			t.Errorf("model = %q; want %q", model, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for model in setup message")
	}
}

func TestWithBaseURL_SetsBaseURL(t *testing.T) {
	t.Parallel()
	connected := make(chan struct{}, 1)

	srv := startGeminiServer(t, func(conn *websocket.Conn, _ *http.Request) {
		var raw map[string]any
		readJSON(t, conn, &raw)
		sendSetupComplete(t, conn)
		connected <- struct{}{}
		<-conn.CloseRead(context.Background()).Done()
	})

	p := gemini.New("key", gemini.WithBaseURL(wsURL(srv)))
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
	p := gemini.New("key")
	caps := p.Capabilities()
	if caps.ContextWindow == 0 {
		t.Error("ContextWindow should be non-zero")
	}
	if len(caps.Voices) == 0 {
		t.Error("Voices should be non-empty")
	}
}

// ── TestConnect_SendsSetup ─────────────────────────────────────────────────────

func TestConnect_SendsSetup(t *testing.T) {
	t.Parallel()

	type setupMsg struct {
		Setup struct {
			Model             string `json:"model"`
			SystemInstruction *struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"systemInstruction"`
			Tools []struct {
				FunctionDeclarations []struct {
					Name string `json:"name"`
				} `json:"functionDeclarations"`
			} `json:"tools"`
		} `json:"setup"`
	}

	received := make(chan setupMsg, 1)

	srv := startGeminiServer(t, func(conn *websocket.Conn, _ *http.Request) {
		var msg setupMsg
		readJSON(t, conn, &msg)
		received <- msg
		sendSetupComplete(t, conn)
		<-conn.CloseRead(context.Background()).Done()
	})

	p := newProvider(srv)
	cfg := s2s.SessionConfig{
		Instructions: "You are a wizard.",
		Voice:        tts.VoiceProfile{ID: "Aoede"},
		Tools: []llm.ToolDefinition{
			{Name: "cast_spell", Description: "Casts a spell"},
		},
	}
	handle, err := p.Connect(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer handle.Close()

	select {
	case msg := <-received:
		if !strings.HasPrefix(msg.Setup.Model, "models/") {
			t.Errorf("model %q should start with 'models/'", msg.Setup.Model)
		}
		if msg.Setup.SystemInstruction == nil {
			t.Fatal("systemInstruction is nil")
		}
		if len(msg.Setup.SystemInstruction.Parts) == 0 || msg.Setup.SystemInstruction.Parts[0].Text != "You are a wizard." {
			t.Errorf("unexpected system instruction: %+v", msg.Setup.SystemInstruction)
		}
		if len(msg.Setup.Tools) == 0 {
			t.Error("tools should be non-empty")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for setup message")
	}
}

func TestConnect_IncludesAPIKeyInURL(t *testing.T) {
	t.Parallel()

	urlPath := make(chan string, 1)

	srv := startGeminiServer(t, func(conn *websocket.Conn, r *http.Request) {
		urlPath <- r.URL.RawQuery
		var raw map[string]any
		readJSON(t, conn, &raw)
		sendSetupComplete(t, conn)
		<-conn.CloseRead(context.Background()).Done()
	})

	p := gemini.New("secret-key", gemini.WithBaseURL(wsURL(srv)))
	handle, err := p.Connect(context.Background(), s2s.SessionConfig{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer handle.Close()

	select {
	case q := <-urlPath:
		if !strings.Contains(q, "key=secret-key") {
			t.Errorf("URL query %q should contain key=secret-key", q)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}
}

// ── TestSendAudio ──────────────────────────────────────────────────────────────

func TestSendAudio_EncodesAndSends(t *testing.T) {
	t.Parallel()

	type realtimeInput struct {
		RealtimeInput struct {
			MediaChunks []struct {
				MIMEType string `json:"mimeType"`
				Data     string `json:"data"`
			} `json:"mediaChunks"`
		} `json:"realtimeInput"`
	}

	audioMsg := make(chan realtimeInput, 1)

	srv := startGeminiServer(t, func(conn *websocket.Conn, _ *http.Request) {
		// Consume setup.
		var raw map[string]any
		readJSON(t, conn, &raw)
		sendSetupComplete(t, conn)

		// Read audio message.
		var msg realtimeInput
		readJSON(t, conn, &msg)
		audioMsg <- msg

		<-conn.CloseRead(context.Background()).Done()
	})

	p := newProvider(srv)
	handle, err := p.Connect(context.Background(), s2s.SessionConfig{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer handle.Close()

	wantPCM := []byte{0x01, 0x02, 0x03, 0x04}
	if err := handle.SendAudio(wantPCM); err != nil {
		t.Fatalf("SendAudio: %v", err)
	}

	select {
	case msg := <-audioMsg:
		chunks := msg.RealtimeInput.MediaChunks
		if len(chunks) == 0 {
			t.Fatal("no media chunks in realtimeInput")
		}
		if chunks[0].MIMEType != "audio/pcm;rate=16000" {
			t.Errorf("mimeType = %q; want audio/pcm;rate=16000", chunks[0].MIMEType)
		}
		got, err := base64.StdEncoding.DecodeString(chunks[0].Data)
		if err != nil {
			t.Fatalf("base64 decode: %v", err)
		}
		if string(got) != string(wantPCM) {
			t.Errorf("decoded audio = %v; want %v", got, wantPCM)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for audio message")
	}
}

func TestSendAudio_AfterClose_ReturnsError(t *testing.T) {
	t.Parallel()

	srv := startGeminiServer(t, func(conn *websocket.Conn, _ *http.Request) {
		var raw map[string]any
		readJSON(t, conn, &raw)
		sendSetupComplete(t, conn)
		<-conn.CloseRead(context.Background()).Done()
	})

	p := newProvider(srv)
	handle, err := p.Connect(context.Background(), s2s.SessionConfig{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := handle.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if err := handle.SendAudio([]byte{1, 2, 3}); err == nil {
		t.Fatal("SendAudio after Close should return an error")
	}
}

// ── TestAudio ──────────────────────────────────────────────────────────────────

func TestAudio_DeliversDecodedPCM(t *testing.T) {
	t.Parallel()

	wantPCM := []byte{0xAA, 0xBB, 0xCC, 0xDD}
	encoded := base64.StdEncoding.EncodeToString(wantPCM)

	srv := startGeminiServer(t, func(conn *websocket.Conn, _ *http.Request) {
		var raw map[string]any
		readJSON(t, conn, &raw)
		sendSetupComplete(t, conn)

		// Send serverContent with audio.
		writeJSON(t, conn, map[string]any{
			"serverContent": map[string]any{
				"modelTurn": map[string]any{
					"parts": []map[string]any{
						{
							"inlineData": map[string]any{
								"mimeType": "audio/pcm;rate=24000",
								"data":     encoded,
							},
						},
					},
				},
			},
		})

		<-conn.CloseRead(context.Background()).Done()
	})

	p := newProvider(srv)
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

	srv := startGeminiServer(t, func(conn *websocket.Conn, _ *http.Request) {
		var raw map[string]any
		readJSON(t, conn, &raw)
		sendSetupComplete(t, conn)
		<-conn.CloseRead(context.Background()).Done()
	})

	p := newProvider(srv)
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

func TestTranscripts_ModelTextPart(t *testing.T) {
	t.Parallel()

	srv := startGeminiServer(t, func(conn *websocket.Conn, _ *http.Request) {
		var raw map[string]any
		readJSON(t, conn, &raw)
		sendSetupComplete(t, conn)

		writeJSON(t, conn, map[string]any{
			"serverContent": map[string]any{
				"modelTurn": map[string]any{
					"parts": []map[string]any{
						{"text": "Hello adventurer!"},
					},
				},
			},
		})

		<-conn.CloseRead(context.Background()).Done()
	})

	p := newProvider(srv)
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
		if entry.Text != "Hello adventurer!" {
			t.Errorf("transcript text = %q; want %q", entry.Text, "Hello adventurer!")
		}
		if !entry.IsNPC() {
			t.Error("expected NPC transcript (NPCID should be set)")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for transcript")
	}
}

func TestTranscripts_InputTranscription(t *testing.T) {
	t.Parallel()

	srv := startGeminiServer(t, func(conn *websocket.Conn, _ *http.Request) {
		var raw map[string]any
		readJSON(t, conn, &raw)
		sendSetupComplete(t, conn)

		writeJSON(t, conn, map[string]any{
			"serverContent": map[string]any{
				"inputTranscription": map[string]any{
					"text": "Cast fireball!",
				},
			},
		})

		<-conn.CloseRead(context.Background()).Done()
	})

	p := newProvider(srv)
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
		if entry.Text != "Cast fireball!" {
			t.Errorf("transcript text = %q; want %q", entry.Text, "Cast fireball!")
		}
		if entry.IsNPC() {
			t.Error("user transcript should not have NPCID set")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for transcript")
	}
}

func TestTranscripts_ChannelNotNil(t *testing.T) {
	t.Parallel()

	srv := startGeminiServer(t, func(conn *websocket.Conn, _ *http.Request) {
		var raw map[string]any
		readJSON(t, conn, &raw)
		sendSetupComplete(t, conn)
		<-conn.CloseRead(context.Background()).Done()
	})

	p := newProvider(srv)
	handle, err := p.Connect(context.Background(), s2s.SessionConfig{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer handle.Close()

	if handle.Transcripts() == nil {
		t.Error("Transcripts() returned nil channel")
	}
}

// ── TestOnToolCall ─────────────────────────────────────────────────────────────

func TestOnToolCall_RoutesToolCallToHandler(t *testing.T) {
	t.Parallel()

	toolCallSent := make(chan struct{}, 1)
	handlerResult := make(chan string, 1)

	srv := startGeminiServer(t, func(conn *websocket.Conn, _ *http.Request) {
		var raw map[string]any
		readJSON(t, conn, &raw)
		sendSetupComplete(t, conn)

		// Send a tool call.
		writeJSON(t, conn, map[string]any{
			"toolCall": map[string]any{
				"functionCalls": []map[string]any{
					{
						"id":   "fc-1",
						"name": "cast_spell",
						"args": map[string]any{"spell": "fireball"},
					},
				},
			},
		})

		// Read the tool response.
		var resp map[string]any
		readJSON(t, conn, &resp)
		data, _ := json.Marshal(resp)
		handlerResult <- string(data)
		close(toolCallSent)

		<-conn.CloseRead(context.Background()).Done()
	})

	p := newProvider(srv)
	handle, err := p.Connect(context.Background(), s2s.SessionConfig{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer handle.Close()

	handlerCalled := make(chan string, 1)
	handle.OnToolCall(func(name, args string) (string, error) {
		handlerCalled <- name + ":" + args
		return `{"result": "fireball cast"}`, nil
	})

	select {
	case <-toolCallSent:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for tool response exchange")
	}

	select {
	case call := <-handlerCalled:
		if !strings.HasPrefix(call, "cast_spell:") {
			t.Errorf("handler called with %q; want prefix cast_spell:", call)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for handler to be called")
	}

	select {
	case respStr := <-handlerResult:
		if !strings.Contains(respStr, "toolResponse") {
			t.Errorf("expected toolResponse in %q", respStr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for tool response")
	}
}

func TestOnToolCall_NilHandlerSkipsToolCall(t *testing.T) {
	t.Parallel()

	toolCallSent := make(chan struct{}, 1)

	srv := startGeminiServer(t, func(conn *websocket.Conn, _ *http.Request) {
		var raw map[string]any
		readJSON(t, conn, &raw)
		sendSetupComplete(t, conn)

		writeJSON(t, conn, map[string]any{
			"toolCall": map[string]any{
				"functionCalls": []map[string]any{
					{"id": "fc-1", "name": "do_thing", "args": map[string]any{}},
				},
			},
		})
		close(toolCallSent)

		// Keep the connection alive; no response is expected.
		time.Sleep(200 * time.Millisecond)
		<-conn.CloseRead(context.Background()).Done()
	})

	p := newProvider(srv)
	handle, err := p.Connect(context.Background(), s2s.SessionConfig{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer handle.Close()

	// No handler registered — should not panic.
	select {
	case <-toolCallSent:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}
	// Give the receive loop time to process without a handler.
	time.Sleep(50 * time.Millisecond)
}

// ── TestSetTools ───────────────────────────────────────────────────────────────

func TestSetTools_ReturnsError(t *testing.T) {
	t.Parallel()

	srv := startGeminiServer(t, func(conn *websocket.Conn, _ *http.Request) {
		var raw map[string]any
		readJSON(t, conn, &raw)
		sendSetupComplete(t, conn)
		<-conn.CloseRead(context.Background()).Done()
	})

	p := newProvider(srv)
	handle, err := p.Connect(context.Background(), s2s.SessionConfig{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer handle.Close()

	if err := handle.SetTools([]llm.ToolDefinition{{Name: "new_tool"}}); err == nil {
		t.Error("SetTools should return an error for Gemini (not supported)")
	}
}

// ── TestUpdateInstructions ────────────────────────────────────────────────────

func TestUpdateInstructions_ReturnsError(t *testing.T) {
	t.Parallel()

	srv := startGeminiServer(t, func(conn *websocket.Conn, _ *http.Request) {
		var raw map[string]any
		readJSON(t, conn, &raw)
		sendSetupComplete(t, conn)
		<-conn.CloseRead(context.Background()).Done()
	})

	p := newProvider(srv)
	handle, err := p.Connect(context.Background(), s2s.SessionConfig{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer handle.Close()

	if err := handle.UpdateInstructions("new instructions"); err == nil {
		t.Error("UpdateInstructions should return an error for Gemini (not supported)")
	}
}

// ── TestInjectTextContext ──────────────────────────────────────────────────────

func TestInjectTextContext_SendsClientContent(t *testing.T) {
	t.Parallel()

	type clientContentMsg struct {
		ClientContent struct {
			Turns []struct {
				Role  string `json:"role"`
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"turns"`
			TurnComplete bool `json:"turnComplete"`
		} `json:"clientContent"`
	}

	contextMsg := make(chan clientContentMsg, 1)

	srv := startGeminiServer(t, func(conn *websocket.Conn, _ *http.Request) {
		// Consume setup.
		var raw map[string]any
		readJSON(t, conn, &raw)
		sendSetupComplete(t, conn)

		// Read the clientContent message.
		var msg clientContentMsg
		readJSON(t, conn, &msg)
		contextMsg <- msg

		<-conn.CloseRead(context.Background()).Done()
	})

	p := newProvider(srv)
	handle, err := p.Connect(context.Background(), s2s.SessionConfig{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer handle.Close()

	items := []s2s.ContextItem{
		{Role: "user", Content: "The dragon is to the north."},
		{Role: "assistant", Content: "I shall guide you."},
	}
	if err := handle.InjectTextContext(items); err != nil {
		t.Fatalf("InjectTextContext: %v", err)
	}

	select {
	case msg := <-contextMsg:
		turns := msg.ClientContent.Turns
		if len(turns) != 2 {
			t.Fatalf("expected 2 turns; got %d", len(turns))
		}
		if turns[0].Role != "user" {
			t.Errorf("turn[0] role = %q; want user", turns[0].Role)
		}
		if turns[1].Role != "model" {
			t.Errorf("turn[1] role = %q; want model (mapped from assistant)", turns[1].Role)
		}
		if turns[0].Parts[0].Text != "The dragon is to the north." {
			t.Errorf("turn[0] text = %q", turns[0].Parts[0].Text)
		}
		if !msg.ClientContent.TurnComplete {
			t.Error("turnComplete should be true")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for clientContent message")
	}
}

func TestInjectTextContext_AfterClose_ReturnsError(t *testing.T) {
	t.Parallel()

	srv := startGeminiServer(t, func(conn *websocket.Conn, _ *http.Request) {
		var raw map[string]any
		readJSON(t, conn, &raw)
		sendSetupComplete(t, conn)
		<-conn.CloseRead(context.Background()).Done()
	})

	p := newProvider(srv)
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

func TestInterrupt_ReturnsError(t *testing.T) {
	t.Parallel()

	srv := startGeminiServer(t, func(conn *websocket.Conn, _ *http.Request) {
		var raw map[string]any
		readJSON(t, conn, &raw)
		sendSetupComplete(t, conn)
		<-conn.CloseRead(context.Background()).Done()
	})

	p := newProvider(srv)
	handle, err := p.Connect(context.Background(), s2s.SessionConfig{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer handle.Close()

	if err := handle.Interrupt(); err == nil {
		t.Error("Interrupt should return an error for Gemini (not supported)")
	}
}

// ── TestClose_Idempotent ───────────────────────────────────────────────────────

func TestClose_Idempotent(t *testing.T) {
	t.Parallel()

	srv := startGeminiServer(t, func(conn *websocket.Conn, _ *http.Request) {
		var raw map[string]any
		readJSON(t, conn, &raw)
		sendSetupComplete(t, conn)
		<-conn.CloseRead(context.Background()).Done()
	})

	p := newProvider(srv)
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

	srv := startGeminiServer(t, func(conn *websocket.Conn, _ *http.Request) {
		var raw map[string]any
		readJSON(t, conn, &raw)
		sendSetupComplete(t, conn)
		<-conn.CloseRead(context.Background()).Done()
	})

	p := newProvider(srv)
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

	srv := startGeminiServer(t, func(conn *websocket.Conn, _ *http.Request) {
		var raw map[string]any
		readJSON(t, conn, &raw)
		sendSetupComplete(t, conn)
		<-conn.CloseRead(context.Background()).Done()
	})

	p := newProvider(srv)
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

	srv := startGeminiServer(t, func(conn *websocket.Conn, _ *http.Request) {
		// Consume setup, then drain all messages.
		var raw map[string]any
		readJSON(t, conn, &raw)
		sendSetupComplete(t, conn)

		ctx := context.Background()
		for {
			_, _, err := conn.Read(ctx)
			if err != nil {
				return
			}
		}
	})

	p := newProvider(srv)
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
				_ = handle.SendAudio([]byte{0x01, 0x02, 0x03, 0x04})
			}
		})
	}
	wg.Wait()
}

// ── TestErr ────────────────────────────────────────────────────────────────────

func TestErr_NilBeforeClose(t *testing.T) {
	t.Parallel()

	srv := startGeminiServer(t, func(conn *websocket.Conn, _ *http.Request) {
		var raw map[string]any
		readJSON(t, conn, &raw)
		sendSetupComplete(t, conn)
		<-conn.CloseRead(context.Background()).Done()
	})

	p := newProvider(srv)
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

	srv := startGeminiServer(t, func(conn *websocket.Conn, _ *http.Request) {
		<-conn.CloseRead(context.Background()).Done()
	})

	p := newProvider(srv)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	_, err := p.Connect(ctx, s2s.SessionConfig{})
	if err == nil {
		t.Fatal("Connect with cancelled context should return an error")
	}
}
