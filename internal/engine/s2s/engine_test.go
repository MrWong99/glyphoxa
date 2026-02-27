package s2s_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	enginepkg "github.com/MrWong99/glyphoxa/internal/engine"
	"github.com/MrWong99/glyphoxa/internal/engine/s2s"
	"github.com/MrWong99/glyphoxa/pkg/audio"
	"github.com/MrWong99/glyphoxa/pkg/memory"
	"github.com/MrWong99/glyphoxa/pkg/provider/llm"
	providers2s "github.com/MrWong99/glyphoxa/pkg/provider/s2s"
	s2smock "github.com/MrWong99/glyphoxa/pkg/provider/s2s/mock"
)

// shortTimeout replaces defaultTurnTimeout in tests so the audio-silence window
// is tiny and test suites remain fast.
const shortTimeout = 20 * time.Millisecond

// newTestEngine builds an Engine with shortTimeout and any extra opts applied.
func newTestEngine(p *s2smock.Provider, opts ...s2s.Option) *s2s.Engine {
	all := append([]s2s.Option{s2s.WithTurnTimeout(shortTimeout)}, opts...)
	return s2s.New(p, providers2s.SessionConfig{}, all...)
}

// newSession builds a fully-initialised mock session with buffered channels.
func newSession() *s2smock.Session {
	return &s2smock.Session{
		AudioCh:       make(chan []byte, 64),
		TranscriptsCh: make(chan memory.TranscriptEntry, 16),
	}
}

// drainAudio reads from ch until it is closed and discards all chunks.
func drainAudio(ch <-chan []byte) {
	for range ch {
	}
}

// mustProcess calls Process and fatals if it returns an error.
func mustProcess(t *testing.T, e *s2s.Engine, data []byte) *enginepkg.Response {
	t.Helper()
	frame := audio.AudioFrame{Data: data, SampleRate: 16000, Channels: 1}
	resp, err := e.Process(context.Background(), frame, enginepkg.PromptContext{})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	return resp
}

// ─── TestProcess_SendsAudioToSession ─────────────────────────────────────────

func TestProcess_SendsAudioToSession(t *testing.T) {
	t.Parallel()

	sess := newSession()
	p := &s2smock.Provider{Session: sess}
	e := newTestEngine(p)
	t.Cleanup(func() { _ = e.Close() })

	want := []byte("hello audio")
	resp := mustProcess(t, e, want)
	// Drain so forwardAudio goroutine is not stuck.
	go drainAudio(resp.Audio)

	// SendAudio is called synchronously inside Process before it returns,
	// so SendAudioCalls is already updated by now — no sleep needed.
	calls := sess.SendAudioCalls
	if len(calls) != 1 {
		t.Fatalf("want 1 SendAudio call, got %d", len(calls))
	}
	if string(calls[0].Chunk) != string(want) {
		t.Fatalf("SendAudio chunk: want %q, got %q", want, calls[0].Chunk)
	}
}

// ─── TestProcess_LazySessionCreation ─────────────────────────────────────────

func TestProcess_LazySessionCreation(t *testing.T) {
	t.Parallel()

	p := &s2smock.Provider{}
	e := newTestEngine(p)
	t.Cleanup(func() { _ = e.Close() })

	if n := len(p.ConnectCalls); n != 0 {
		t.Fatalf("want 0 ConnectCalls before Process, got %d", n)
	}

	resp := mustProcess(t, e, []byte("hi"))
	go drainAudio(resp.Audio)

	// Connect is called synchronously inside Process.
	if n := len(p.ConnectCalls); n != 1 {
		t.Fatalf("want 1 ConnectCall after Process, got %d", n)
	}
}

// ─── TestProcess_SessionReconnect ────────────────────────────────────────────

func TestProcess_SessionReconnect(t *testing.T) {
	t.Parallel()

	// A session that is dead from the start (Err() returns non-nil).
	deadSess := &s2smock.Session{
		AudioCh:       make(chan []byte, 64),
		TranscriptsCh: make(chan memory.TranscriptEntry, 16),
		ErrResult:     errors.New("session died"),
	}
	p := &s2smock.Provider{Session: deadSess}
	e := newTestEngine(p)
	t.Cleanup(func() { _ = e.Close() })

	// First Process: session is nil → Connect → get dead session.
	resp1 := mustProcess(t, e, []byte("turn1"))
	go drainAudio(resp1.Audio)

	if n := len(p.ConnectCalls); n != 1 {
		t.Fatalf("want 1 ConnectCall after first Process, got %d", n)
	}

	// Second Process: session.Err() != nil → reconnect → second Connect.
	resp2 := mustProcess(t, e, []byte("turn2"))
	go drainAudio(resp2.Audio)

	if n := len(p.ConnectCalls); n != 2 {
		t.Fatalf("want 2 ConnectCalls (reconnect), got %d", n)
	}
}

// ─── TestProcess_InjectsPromptContext ─────────────────────────────────────────

func TestProcess_InjectsPromptContext(t *testing.T) {
	t.Parallel()

	sess := newSession()
	p := &s2smock.Provider{Session: sess}
	e := newTestEngine(p)
	t.Cleanup(func() { _ = e.Close() })

	prompt := enginepkg.PromptContext{
		SystemPrompt: "You are Gandalf.",
		HotContext:   "The player is at the bridge.",
	}
	frame := audio.AudioFrame{Data: []byte("audio"), SampleRate: 16000, Channels: 1}
	resp, err := e.Process(context.Background(), frame, prompt)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	go drainAudio(resp.Audio)

	// Both calls are synchronous inside Process — no sleep needed.
	instrCalls := sess.UpdateInstructionsCalls
	ctxCalls := sess.InjectTextContextCalls

	if len(instrCalls) != 1 {
		t.Fatalf("want 1 UpdateInstructions call, got %d", len(instrCalls))
	}
	if instrCalls[0].Instructions != prompt.SystemPrompt {
		t.Fatalf("UpdateInstructions: want %q, got %q", prompt.SystemPrompt, instrCalls[0].Instructions)
	}

	if len(ctxCalls) != 1 {
		t.Fatalf("want 1 InjectTextContext call, got %d", len(ctxCalls))
	}
	if len(ctxCalls[0].Items) != 1 {
		t.Fatalf("want 1 context item, got %d", len(ctxCalls[0].Items))
	}
	if ctxCalls[0].Items[0].Content != prompt.HotContext {
		t.Fatalf("context item content: want %q, got %q", prompt.HotContext, ctxCalls[0].Items[0].Content)
	}
	if ctxCalls[0].Items[0].Role != "system" {
		t.Fatalf("context item role: want %q, got %q", "system", ctxCalls[0].Items[0].Role)
	}
}

// ─── TestInjectContext_ForwardsToSession ──────────────────────────────────────

func TestInjectContext_ForwardsToSession(t *testing.T) {
	t.Parallel()

	sess := newSession()
	p := &s2smock.Provider{Session: sess}
	e := newTestEngine(p)
	t.Cleanup(func() { _ = e.Close() })

	// Open a session first.
	resp := mustProcess(t, e, nil)
	go drainAudio(resp.Audio)

	update := enginepkg.ContextUpdate{
		Identity: "I am Gandalf.",
		Scene:    "Standing at the bridge.",
		RecentUtterances: []memory.TranscriptEntry{
			{SpeakerName: "Player", Text: "You shall not pass!"},
		},
	}
	if err := e.InjectContext(context.Background(), update); err != nil {
		t.Fatalf("InjectContext: %v", err)
	}

	// Process had no HotContext so InjectTextContext was only called once (by InjectContext).
	calls := sess.InjectTextContextCalls
	if len(calls) != 1 {
		t.Fatalf("want 1 InjectTextContext call, got %d", len(calls))
	}
	items := calls[0].Items
	if len(items) != 3 {
		t.Fatalf("want 3 items (identity + scene + utterance), got %d", len(items))
	}
	if items[0].Content != update.Identity {
		t.Fatalf("identity item: want %q, got %q", update.Identity, items[0].Content)
	}
	if items[1].Content != update.Scene {
		t.Fatalf("scene item: want %q, got %q", update.Scene, items[1].Content)
	}
	if items[2].Content != update.RecentUtterances[0].Text {
		t.Fatalf("utterance item: want %q, got %q", update.RecentUtterances[0].Text, items[2].Content)
	}
}

// ─── TestInjectContext_NilSessionNoop ─────────────────────────────────────────

func TestInjectContext_NilSessionNoop(t *testing.T) {
	t.Parallel()

	p := &s2smock.Provider{}
	e := newTestEngine(p)
	t.Cleanup(func() { _ = e.Close() })

	// No session exists yet (Process never called).
	update := enginepkg.ContextUpdate{Identity: "Ghost", Scene: "Haunted house"}
	if err := e.InjectContext(context.Background(), update); err != nil {
		t.Fatalf("InjectContext with no session should return nil, got: %v", err)
	}
}

// ─── TestSetTools_ForwardedToSession ──────────────────────────────────────────

func TestSetTools_ForwardedToSession(t *testing.T) {
	t.Parallel()

	sess := newSession()
	p := &s2smock.Provider{Session: sess}
	e := newTestEngine(p)
	t.Cleanup(func() { _ = e.Close() })

	// Open a session.
	resp := mustProcess(t, e, nil)
	go drainAudio(resp.Audio)

	tools := []llm.ToolDefinition{
		{Name: "fireball", Description: "Cast fireball spell"},
	}
	if err := e.SetTools(tools); err != nil {
		t.Fatalf("SetTools: %v", err)
	}

	// SetTools on engine is synchronous and forwards to session synchronously.
	calls := sess.SetToolsCalls
	if len(calls) == 0 {
		t.Fatal("want at least 1 SetTools call on session, got 0")
	}
	last := calls[len(calls)-1]
	if len(last.Tools) != 1 || last.Tools[0].Name != "fireball" {
		t.Fatalf("SetTools: unexpected tools: %+v", last.Tools)
	}
}

// ─── TestSetTools_StoredForFutureSession ──────────────────────────────────────

func TestSetTools_StoredForFutureSession(t *testing.T) {
	t.Parallel()

	sess := newSession()
	p := &s2smock.Provider{Session: sess}
	e := newTestEngine(p)
	t.Cleanup(func() { _ = e.Close() })

	// SetTools before any session is open.
	tools := []llm.ToolDefinition{
		{Name: "icebolt", Description: "Cast ice bolt"},
	}
	if err := e.SetTools(tools); err != nil {
		t.Fatalf("SetTools (pre-session): %v", err)
	}

	// Process opens the session; ensureSessionLocked applies stored tools.
	resp := mustProcess(t, e, nil)
	go drainAudio(resp.Audio)

	// SetTools was applied inside ensureSessionLocked (same goroutine as Process).
	calls := sess.SetToolsCalls
	if len(calls) == 0 {
		t.Fatal("want SetTools applied on session open, got 0 calls")
	}
	found := false
	for _, c := range calls {
		for _, td := range c.Tools {
			if td.Name == "icebolt" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("tool 'icebolt' not found in any SetTools call: %+v", calls)
	}
}

// ─── TestOnToolCall_RegistersHandler ──────────────────────────────────────────

func TestOnToolCall_RegistersHandler(t *testing.T) {
	t.Parallel()

	sess := newSession()
	p := &s2smock.Provider{Session: sess}
	e := newTestEngine(p)
	t.Cleanup(func() { _ = e.Close() })

	// Open session first.
	resp := mustProcess(t, e, nil)
	go drainAudio(resp.Audio)

	handler := func(name, args string) (string, error) { return "ok", nil }
	e.OnToolCall(handler)

	// OnToolCall is synchronous — count is updated by now.
	if sess.OnToolCallSetCount == 0 {
		t.Fatal("OnToolCall not forwarded to session (OnToolCallSetCount == 0)")
	}

	// Verify the handler is actually registered on the session.
	if sess.Handler() == nil {
		t.Fatal("session Handler() is nil after OnToolCall")
	}
}

// ─── TestTranscripts_ForwardsFromSession ──────────────────────────────────────

func TestTranscripts_ForwardsFromSession(t *testing.T) {
	t.Parallel()

	transcriptsCh := make(chan memory.TranscriptEntry, 16)
	sess := &s2smock.Session{
		AudioCh:       make(chan []byte, 64),
		TranscriptsCh: transcriptsCh,
	}
	p := &s2smock.Provider{Session: sess}
	e := newTestEngine(p)
	t.Cleanup(func() { _ = e.Close() })

	// Open session — this starts the forwardTranscripts goroutine.
	resp := mustProcess(t, e, nil)
	go drainAudio(resp.Audio)

	want := memory.TranscriptEntry{SpeakerName: "Player", Text: "Hello!"}

	// Give forwardTranscripts goroutine a moment to begin blocking on the channel.
	time.Sleep(5 * time.Millisecond)
	transcriptsCh <- want

	select {
	case got := <-e.Transcripts():
		if got.Text != want.Text || got.SpeakerName != want.SpeakerName {
			t.Fatalf("transcript: want %+v, got %+v", want, got)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for transcript entry from engine")
	}
}

// ─── TestClose_Idempotent ─────────────────────────────────────────────────────

func TestClose_Idempotent(t *testing.T) {
	t.Parallel()

	sess := newSession()
	p := &s2smock.Provider{Session: sess}
	e := newTestEngine(p)

	resp := mustProcess(t, e, nil)
	go drainAudio(resp.Audio)

	for i := range 5 {
		if err := e.Close(); err != nil {
			t.Fatalf("Close() call %d returned error: %v", i+1, err)
		}
	}
}

// ─── TestClose_StopsForwarding ────────────────────────────────────────────────

func TestClose_StopsForwarding(t *testing.T) {
	t.Parallel()

	audioCh := make(chan []byte, 64)
	sess := &s2smock.Session{
		AudioCh:       audioCh,
		TranscriptsCh: make(chan memory.TranscriptEntry, 16),
	}
	p := &s2smock.Provider{Session: sess}
	e := newTestEngine(p)

	resp := mustProcess(t, e, nil)

	// Close the engine immediately; the forwardAudio goroutine must stop.
	if err := e.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Drain resp.Audio — it must close in reasonable time.
	done := make(chan struct{})
	go func() {
		drainAudio(resp.Audio)
		close(done)
	}()

	select {
	case <-done:
		// Audio channel closed promptly. ✓
	case <-time.After(500 * time.Millisecond):
		t.Fatal("audio channel not closed within 500ms after engine Close()")
	}
}

// ─── TestConcurrentProcessCalls ───────────────────────────────────────────────

func TestConcurrentProcessCalls(t *testing.T) {
	t.Parallel()

	p := &s2smock.Provider{}
	e := newTestEngine(p)
	t.Cleanup(func() { _ = e.Close() })

	const goroutines = 20

	var wg sync.WaitGroup
	errs := make([]error, goroutines)

	for i := range goroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			frame := audio.AudioFrame{
				Data:       fmt.Appendf(nil, "audio-%d", idx),
				SampleRate: 16000,
				Channels:   1,
			}
			resp, err := e.Process(context.Background(), frame, enginepkg.PromptContext{})
			if err != nil {
				errs[idx] = err
				return
			}
			// Must drain to prevent the forwardAudio goroutine from blocking.
			go drainAudio(resp.Audio)
		}(i)
	}

	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: Process error: %v", i, err)
		}
	}
}
