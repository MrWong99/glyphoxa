package cascade_test

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	enginepkg "github.com/MrWong99/glyphoxa/internal/engine"
	"github.com/MrWong99/glyphoxa/internal/engine/cascade"
	"github.com/MrWong99/glyphoxa/pkg/audio"
	"github.com/MrWong99/glyphoxa/pkg/memory"
	"github.com/MrWong99/glyphoxa/pkg/provider/llm"
	llmmock "github.com/MrWong99/glyphoxa/pkg/provider/llm/mock"
	"github.com/MrWong99/glyphoxa/pkg/provider/tts"
	ttsmock "github.com/MrWong99/glyphoxa/pkg/provider/tts/mock"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// drainAudio reads the audio channel to completion so engine goroutines are
// not left blocked.
func drainAudio(ch <-chan []byte) {
	for range ch {
	}
}

// newTTS returns a TTS mock that emits a single "audio" chunk per call.
func newTTS() *ttsmock.Provider {
	return &ttsmock.Provider{
		SynthesizeChunks: [][]byte{[]byte("audio")},
	}
}

// emptyAudioFrame is a zero-value audio frame used in tests that do not
// exercise the STT path.
var emptyAudioFrame = audio.AudioFrame{}

// ─── TestProcess_FastModelOnly ────────────────────────────────────────────────

// TestProcess_FastModelOnly verifies that when the fast model returns a response
// that ends with a finish reason (no sentence boundary detected before stream end),
// the strong model is never called.
func TestProcess_FastModelOnly(t *testing.T) {
	t.Parallel()

	fastLLM := &llmmock.Provider{
		StreamChunks: []llm.Chunk{
			{Text: "Well met, traveller.", FinishReason: "stop"},
		},
	}
	strongLLM := &llmmock.Provider{}
	ttsProv := newTTS()

	e := cascade.New(fastLLM, strongLLM, ttsProv, tts.VoiceProfile{})
	t.Cleanup(func() { _ = e.Close() })

	resp, err := e.Process(context.Background(), emptyAudioFrame, enginepkg.PromptContext{
		SystemPrompt: "You are an innkeeper.",
	})
	if err != nil {
		t.Fatalf("Process: unexpected error: %v", err)
	}
	drainAudio(resp.Audio)
	e.Wait()

	// Fast model must have been called exactly once.
	if len(fastLLM.StreamCalls) != 1 {
		t.Errorf("fastLLM StreamCompletion calls: want 1, got %d", len(fastLLM.StreamCalls))
	}
	// Strong model must NOT have been called.
	if len(strongLLM.StreamCalls) != 0 {
		t.Errorf("strongLLM StreamCompletion calls: want 0, got %d", len(strongLLM.StreamCalls))
	}
	// TTS must have been invoked exactly once.
	if len(ttsProv.SynthesizeStreamCalls) != 1 {
		t.Errorf("TTS SynthesizeStream calls: want 1, got %d", len(ttsProv.SynthesizeStreamCalls))
	}
	// Response text should be the fast model's output.
	if resp.Text != "Well met, traveller." {
		t.Errorf("resp.Text: want %q, got %q", "Well met, traveller.", resp.Text)
	}
	if err := resp.Err(); err != nil {
		t.Errorf("resp.Err(): unexpected error: %v", err)
	}
}

// ─── TestProcess_DualModel ────────────────────────────────────────────────────

// TestProcess_DualModel verifies that when the fast model emits a sentence
// boundary (punctuation followed by a space), both the fast and the strong model
// are called, and TTS receives the merged stream.
func TestProcess_DualModel(t *testing.T) {
	t.Parallel()

	fastLLM := &llmmock.Provider{
		StreamChunks: []llm.Chunk{
			// "! " triggers a sentence boundary → opener = "Ah, traveller!"
			{Text: "Ah, traveller! "},
			// This chunk is drained in the background (never used by the engine).
			{Text: "and more text", FinishReason: "stop"},
		},
	}
	strongLLM := &llmmock.Provider{
		StreamChunks: []llm.Chunk{
			{Text: "What brings you here?", FinishReason: "stop"},
		},
	}
	ttsProv := newTTS()

	e := cascade.New(fastLLM, strongLLM, ttsProv, tts.VoiceProfile{})
	t.Cleanup(func() { _ = e.Close() })

	resp, err := e.Process(context.Background(), emptyAudioFrame, enginepkg.PromptContext{
		SystemPrompt: "You are a guild master.",
	})
	if err != nil {
		t.Fatalf("Process: unexpected error: %v", err)
	}
	drainAudio(resp.Audio)
	e.Wait()

	if len(fastLLM.StreamCalls) != 1 {
		t.Errorf("fastLLM StreamCompletion calls: want 1, got %d", len(fastLLM.StreamCalls))
	}
	if len(strongLLM.StreamCalls) != 1 {
		t.Errorf("strongLLM StreamCompletion calls: want 1, got %d", len(strongLLM.StreamCalls))
	}
	if len(ttsProv.SynthesizeStreamCalls) != 1 {
		t.Errorf("TTS SynthesizeStream calls: want 1, got %d", len(ttsProv.SynthesizeStreamCalls))
	}
	if resp.Err() != nil {
		t.Errorf("resp.Err(): unexpected error: %v", resp.Err())
	}
}

// ─── TestProcess_OpenerSentenceDetection ─────────────────────────────────────

// TestProcess_OpenerSentenceDetection verifies the sentence-boundary heuristic
// across a range of common NPC speech patterns.
func TestProcess_OpenerSentenceDetection(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		fastChunks   []llm.Chunk
		wantOpener   string
		wantFastFull bool // true → strong model should NOT be called
	}{
		{
			name: "exclamation with trailing space",
			fastChunks: []llm.Chunk{
				{Text: "Hello! "},
				{Text: "Come in.", FinishReason: "stop"},
			},
			wantOpener:   "Hello!",
			wantFastFull: false,
		},
		{
			name: "period with trailing space",
			fastChunks: []llm.Chunk{
				{Text: "The blacksmith strokes his beard. "},
				{Text: "Then he speaks.", FinishReason: "stop"},
			},
			wantOpener:   "The blacksmith strokes his beard.",
			wantFastFull: false,
		},
		{
			name: "question mark with trailing space",
			fastChunks: []llm.Chunk{
				{Text: "What do you seek? "},
				{Text: "Speak.", FinishReason: "stop"},
			},
			wantOpener:   "What do you seek?",
			wantFastFull: false,
		},
		{
			name: "single sentence finish reason no boundary",
			fastChunks: []llm.Chunk{
				{Text: "Indeed.", FinishReason: "stop"},
			},
			wantOpener:   "Indeed.",
			wantFastFull: true,
		},
		{
			name: "multi-token single sentence",
			fastChunks: []llm.Chunk{
				{Text: "Greet"},
				{Text: "ings, friend.", FinishReason: "stop"},
			},
			wantOpener:   "Greetings, friend.",
			wantFastFull: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fastLLM := &llmmock.Provider{StreamChunks: tc.fastChunks}
			strongLLM := &llmmock.Provider{
				StreamChunks: []llm.Chunk{
					{Text: " continuation.", FinishReason: "stop"},
				},
			}
			ttsProv := newTTS()

			e := cascade.New(fastLLM, strongLLM, ttsProv, tts.VoiceProfile{})
			t.Cleanup(func() { _ = e.Close() })

			resp, err := e.Process(context.Background(), emptyAudioFrame, enginepkg.PromptContext{
				SystemPrompt: "NPC persona.",
			})
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			drainAudio(resp.Audio)
			e.Wait()

			// Verify opener text.
			if resp.Text != tc.wantOpener {
				t.Errorf("resp.Text: want %q, got %q", tc.wantOpener, resp.Text)
			}

			// Verify whether strong model was called.
			strongCalled := len(strongLLM.StreamCalls) > 0
			if tc.wantFastFull && strongCalled {
				t.Error("strong model was called but fast model response was complete (fastFull=true)")
			}
			if !tc.wantFastFull && !strongCalled {
				t.Error("strong model was not called but fast model returned a sentence boundary (fastFull=false)")
			}

			// If dual-model, the strong model's first request message must end with
			// the opener as an assistant prefix.
			if !tc.wantFastFull && strongCalled {
				calls := strongLLM.StreamCalls
				msgs := calls[0].Req.Messages
				if len(msgs) == 0 {
					t.Fatal("strong model received empty messages slice")
				}
				last := msgs[len(msgs)-1]
				if last.Role != "assistant" {
					t.Errorf("last message role: want %q, got %q", "assistant", last.Role)
				}
				if last.Content != tc.wantOpener {
					t.Errorf("last message content: want %q, got %q", tc.wantOpener, last.Content)
				}
			}
		})
	}
}

// ─── TestProcess_ForcedPrefix ────────────────────────────────────────────────

// TestProcess_ForcedPrefix verifies that the strong model receives the opener
// as an assistant-role message appended after the conversation history, acting
// as a forced continuation prefix.
func TestProcess_ForcedPrefix(t *testing.T) {
	t.Parallel()

	fastLLM := &llmmock.Provider{
		StreamChunks: []llm.Chunk{
			// "! " triggers a sentence boundary.
			{Text: "Ah, the artifact! "},
			{Text: "remaining", FinishReason: "stop"},
		},
	}
	strongLLM := &llmmock.Provider{
		StreamChunks: []llm.Chunk{
			{Text: "It was forged long ago.", FinishReason: "stop"},
		},
	}
	ttsProv := newTTS()

	history := []llm.Message{
		{Role: "user", Content: "Tell me about the artifact."},
	}

	e := cascade.New(fastLLM, strongLLM, ttsProv, tts.VoiceProfile{})
	t.Cleanup(func() { _ = e.Close() })

	resp, err := e.Process(context.Background(), emptyAudioFrame, enginepkg.PromptContext{
		SystemPrompt: "You are a wise sage.",
		Messages:     history,
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	drainAudio(resp.Audio)
	e.Wait()

	if len(strongLLM.StreamCalls) != 1 {
		t.Fatalf("strong model calls: want 1, got %d", len(strongLLM.StreamCalls))
	}

	req := strongLLM.StreamCalls[0].Req

	// The request must contain the original history plus the opener prefix.
	wantMsgCount := len(history) + 1
	if len(req.Messages) != wantMsgCount {
		t.Fatalf("strong model message count: want %d, got %d", wantMsgCount, len(req.Messages))
	}

	// Last message must be the opener as an assistant role.
	last := req.Messages[len(req.Messages)-1]
	if last.Role != "assistant" {
		t.Errorf("forced-prefix role: want %q, got %q", "assistant", last.Role)
	}
	wantOpener := "Ah, the artifact!"
	if last.Content != wantOpener {
		t.Errorf("forced-prefix content: want %q, got %q", wantOpener, last.Content)
	}

	// Original history must be preserved before the prefix.
	if req.Messages[0].Content != history[0].Content {
		t.Errorf("history[0] content: want %q, got %q", history[0].Content, req.Messages[0].Content)
	}
}

// ─── TestProcess_FastModelInstructionAppended ─────────────────────────────────

// TestProcess_FastModelInstructionAppended verifies that the opener instruction
// is appended to the fast model's system prompt and that the strong model's
// system prompt does NOT contain it.
func TestProcess_FastModelInstructionAppended(t *testing.T) {
	t.Parallel()

	const customSuffix = "CUSTOM_OPENER_INSTRUCTION"

	fastLLM := &llmmock.Provider{
		StreamChunks: []llm.Chunk{
			{Text: "Greetings! "},
			{Text: "Welcome.", FinishReason: "stop"},
		},
	}
	strongLLM := &llmmock.Provider{
		StreamChunks: []llm.Chunk{
			{Text: "How can I help?", FinishReason: "stop"},
		},
	}
	ttsProv := newTTS()

	e := cascade.New(
		fastLLM, strongLLM, ttsProv, tts.VoiceProfile{},
		cascade.WithOpenerPromptSuffix(customSuffix),
	)
	t.Cleanup(func() { _ = e.Close() })

	_, err := e.Process(context.Background(), emptyAudioFrame, enginepkg.PromptContext{
		SystemPrompt: "You are an NPC.",
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	e.Wait()

	if len(fastLLM.StreamCalls) == 0 {
		t.Fatal("fast model was not called")
	}
	fastSysPrompt := fastLLM.StreamCalls[0].Req.SystemPrompt
	if !strings.Contains(fastSysPrompt, customSuffix) {
		t.Errorf("fast model system prompt does not contain opener instruction %q; got: %q", customSuffix, fastSysPrompt)
	}
	if !strings.Contains(fastSysPrompt, "You are an NPC.") {
		t.Errorf("fast model system prompt missing original system prompt; got: %q", fastSysPrompt)
	}

	// The strong model's system prompt must NOT contain the opener instruction.
	if len(strongLLM.StreamCalls) > 0 {
		strongSysPrompt := strongLLM.StreamCalls[0].Req.SystemPrompt
		if strings.Contains(strongSysPrompt, customSuffix) {
			t.Errorf("strong model system prompt must not contain opener instruction, got: %q", strongSysPrompt)
		}
	}
}

// ─── TestInjectContext_StoresUpdate ──────────────────────────────────────────

// TestInjectContext_StoresUpdate verifies that a context update injected via
// InjectContext is applied on the next Process call and consumed thereafter.
func TestInjectContext_StoresUpdate(t *testing.T) {
	t.Parallel()

	fastLLM := &llmmock.Provider{
		StreamChunks: []llm.Chunk{
			{Text: "Updated greeting.", FinishReason: "stop"},
		},
	}
	strongLLM := &llmmock.Provider{}
	ttsProv := newTTS()

	e := cascade.New(fastLLM, strongLLM, ttsProv, tts.VoiceProfile{})
	t.Cleanup(func() { _ = e.Close() })

	// Inject a context update with a new identity.
	updatedIdentity := "You are now a wizard named Aldric."
	err := e.InjectContext(context.Background(), enginepkg.ContextUpdate{
		Identity: updatedIdentity,
	})
	if err != nil {
		t.Fatalf("InjectContext: %v", err)
	}

	// Process with a different system prompt — the injected identity should win.
	originalPrompt := enginepkg.PromptContext{
		SystemPrompt: "You are an innkeeper.",
	}
	resp, err := e.Process(context.Background(), emptyAudioFrame, originalPrompt)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	drainAudio(resp.Audio)
	e.Wait()

	if len(fastLLM.StreamCalls) == 0 {
		t.Fatal("fast model was not called")
	}
	sysPrompt := fastLLM.StreamCalls[0].Req.SystemPrompt
	if !strings.Contains(sysPrompt, updatedIdentity) {
		t.Errorf("fast model system prompt: want %q, got %q", updatedIdentity, sysPrompt)
	}

	// Reset call records to test that the update was consumed.
	fastLLM.Reset()

	// Second Process call: update must not be re-applied.
	resp2, err := e.Process(context.Background(), emptyAudioFrame, originalPrompt)
	if err != nil {
		t.Fatalf("second Process: %v", err)
	}
	drainAudio(resp2.Audio)
	e.Wait()

	if len(fastLLM.StreamCalls) == 0 {
		t.Fatal("fast model was not called on second Process")
	}
	sysPrompt2 := fastLLM.StreamCalls[0].Req.SystemPrompt
	if !strings.Contains(sysPrompt2, "You are an innkeeper.") {
		t.Errorf("second call: system prompt should revert to original, got %q", sysPrompt2)
	}
}

// ─── TestSetTools_OnlyStrongModel ────────────────────────────────────────────

// TestSetTools_OnlyStrongModel verifies that tools set via SetTools are forwarded
// to the strong model only, and that the fast model never receives them.
func TestSetTools_OnlyStrongModel(t *testing.T) {
	t.Parallel()

	fastLLM := &llmmock.Provider{
		StreamChunks: []llm.Chunk{
			// Boundary triggers dual-model path.
			{Text: "Let me check. "},
			{Text: "One moment.", FinishReason: "stop"},
		},
	}
	strongLLM := &llmmock.Provider{
		StreamChunks: []llm.Chunk{
			{Text: "Here is the answer.", FinishReason: "stop"},
		},
	}
	ttsProv := newTTS()

	e := cascade.New(fastLLM, strongLLM, ttsProv, tts.VoiceProfile{})
	t.Cleanup(func() { _ = e.Close() })

	tools := []llm.ToolDefinition{
		{Name: "query_lore", Description: "Queries the lore database."},
	}
	if err := e.SetTools(tools); err != nil {
		t.Fatalf("SetTools: %v", err)
	}

	resp, err := e.Process(context.Background(), emptyAudioFrame, enginepkg.PromptContext{
		SystemPrompt: "You are a lore keeper.",
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	drainAudio(resp.Audio)
	e.Wait()

	if len(fastLLM.StreamCalls) == 0 {
		t.Fatal("fast model not called")
	}
	if len(strongLLM.StreamCalls) == 0 {
		t.Fatal("strong model not called")
	}

	// Fast model must receive no tools.
	if len(fastLLM.StreamCalls[0].Req.Tools) != 0 {
		t.Errorf("fast model tools: want 0, got %d", len(fastLLM.StreamCalls[0].Req.Tools))
	}

	// Strong model must receive the configured tools.
	strongTools := strongLLM.StreamCalls[0].Req.Tools
	if len(strongTools) != 1 {
		t.Fatalf("strong model tools: want 1, got %d", len(strongTools))
	}
	if strongTools[0].Name != "query_lore" {
		t.Errorf("strong model tool name: want %q, got %q", "query_lore", strongTools[0].Name)
	}
}

// ─── TestOnToolCall_RegistersHandler ─────────────────────────────────────────

// TestOnToolCall_RegistersHandler verifies that OnToolCall does not panic, can
// be called multiple times (replacing the previous handler each time), and that
// the engine remains functional after registration.
func TestOnToolCall_RegistersHandler(t *testing.T) {
	t.Parallel()

	fastLLM := &llmmock.Provider{
		StreamChunks: []llm.Chunk{
			{Text: "One moment.", FinishReason: "stop"},
		},
	}
	strongLLM := &llmmock.Provider{}
	ttsProv := newTTS()

	e := cascade.New(fastLLM, strongLLM, ttsProv, tts.VoiceProfile{})
	t.Cleanup(func() { _ = e.Close() })

	var callCount int32

	// Register first handler.
	e.OnToolCall(func(name, args string) (string, error) {
		atomic.AddInt32(&callCount, 1)
		return "result-1", nil
	})

	// Register second handler — must replace the first.
	e.OnToolCall(func(name, args string) (string, error) {
		atomic.AddInt32(&callCount, 10)
		return "result-2", nil
	})

	// Engine must still process correctly after handler registration.
	resp, err := e.Process(context.Background(), emptyAudioFrame, enginepkg.PromptContext{
		SystemPrompt: "You are an NPC.",
	})
	if err != nil {
		t.Fatalf("Process after OnToolCall: %v", err)
	}
	drainAudio(resp.Audio)
	e.Wait()

	// No tool calls were issued by the LLM in this test, so callCount stays 0.
	if n := atomic.LoadInt32(&callCount); n != 0 {
		t.Errorf("tool handler called unexpectedly: count=%d", n)
	}
}

// ─── TestClose_Idempotent ─────────────────────────────────────────────────────

// TestClose_Idempotent verifies that calling Close multiple times is safe and
// always returns nil.
func TestClose_Idempotent(t *testing.T) {
	t.Parallel()

	e := cascade.New(
		&llmmock.Provider{},
		&llmmock.Provider{},
		&ttsmock.Provider{},
		tts.VoiceProfile{},
	)

	for i := range 5 {
		if err := e.Close(); err != nil {
			t.Errorf("Close() call %d: unexpected error: %v", i, err)
		}
	}

	// Transcripts channel must be closed after the first Close.
	ch := e.Transcripts()
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("Transcripts channel was not closed after Close()")
		}
	default:
		// Channel might buffer — read it.
		for range ch {
		}
	}
}

// ─── TestConcurrentProcess ────────────────────────────────────────────────────

// TestConcurrentProcess verifies that concurrent Process calls do not race or
// deadlock. It runs several goroutines calling Process simultaneously and expects
// all of them to succeed.
func TestConcurrentProcess(t *testing.T) {
	t.Parallel()

	const numGoroutines = 8

	fastLLM := &llmmock.Provider{
		StreamChunks: []llm.Chunk{
			{Text: "Hello! "},
			{Text: "Continuation.", FinishReason: "stop"},
		},
	}
	strongLLM := &llmmock.Provider{
		StreamChunks: []llm.Chunk{
			{Text: "The answer is here.", FinishReason: "stop"},
		},
	}
	ttsProv := newTTS()

	e := cascade.New(fastLLM, strongLLM, ttsProv, tts.VoiceProfile{})
	t.Cleanup(func() { _ = e.Close() })

	var wg sync.WaitGroup
	errs := make([]error, numGoroutines)

	for i := range numGoroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			resp, err := e.Process(context.Background(), emptyAudioFrame, enginepkg.PromptContext{
				SystemPrompt: "Concurrent NPC.",
			})
			if err != nil {
				errs[idx] = err
				return
			}
			drainAudio(resp.Audio)
		}(i)
	}

	wg.Wait()
	e.Wait() // wait for all strong-model goroutines to finish

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: Process error: %v", i, err)
		}
	}
}

// ─── TestTranscripts_ChannelClosedOnClose ────────────────────────────────────

// TestTranscripts_ChannelClosedOnClose is an additional smoke-test verifying
// that the Transcripts channel is consistently the same channel and is closed
// when the engine is closed.
func TestTranscripts_ChannelClosedOnClose(t *testing.T) {
	t.Parallel()

	e := cascade.New(
		&llmmock.Provider{},
		&llmmock.Provider{},
		&ttsmock.Provider{},
		tts.VoiceProfile{},
	)

	ch1 := e.Transcripts()
	ch2 := e.Transcripts()
	if ch1 != ch2 {
		t.Error("Transcripts() must return the same channel on every call")
	}

	if err := e.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Channel must be closed.
	_, ok := <-ch1
	if ok {
		t.Error("Transcripts channel should be closed after Close()")
	}
}

// ─── TestWithTranscriptBuffer ────────────────────────────────────────────────

// TestWithTranscriptBuffer verifies that WithTranscriptBuffer configures the
// channel capacity. We cannot inspect channel capacity directly from outside the
// package, but we can verify that n entries can be sent without blocking by
// publishing n entries from inside (here we just exercise the option and verify
// the engine still builds and runs cleanly).
func TestWithTranscriptBuffer(t *testing.T) {
	t.Parallel()

	e := cascade.New(
		&llmmock.Provider{StreamChunks: []llm.Chunk{{Text: "Hi.", FinishReason: "stop"}}},
		&llmmock.Provider{},
		newTTS(),
		tts.VoiceProfile{},
		cascade.WithTranscriptBuffer(128),
	)
	t.Cleanup(func() { _ = e.Close() })

	resp, err := e.Process(context.Background(), emptyAudioFrame, enginepkg.PromptContext{})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	drainAudio(resp.Audio)
	e.Wait()
}

// ─── TestWithSTT_OptionStored ─────────────────────────────────────────────────

// TestWithSTT_OptionStored verifies that WithSTT is accepted without panicking.
// Full STT integration is out of scope for unit tests; this test ensures the
// option wires correctly and the engine processes a text-mode request normally.
func TestWithSTT_OptionStored(t *testing.T) {
	t.Parallel()

	// A nil STT is acceptable for text-only mode.
	e := cascade.New(
		&llmmock.Provider{StreamChunks: []llm.Chunk{{Text: "Greetings.", FinishReason: "stop"}}},
		&llmmock.Provider{},
		newTTS(),
		tts.VoiceProfile{},
		cascade.WithSTT(nil),
	)
	t.Cleanup(func() { _ = e.Close() })

	resp, err := e.Process(context.Background(), emptyAudioFrame, enginepkg.PromptContext{
		SystemPrompt: "You are an NPC.",
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	drainAudio(resp.Audio)
	e.Wait()
}

// ─── TestInjectContext_SceneAndUtterances ─────────────────────────────────────

// TestInjectContext_SceneAndUtterances verifies that Scene and RecentUtterances
// from a ContextUpdate are applied to the prompt sent to the fast model.
func TestInjectContext_SceneAndUtterances(t *testing.T) {
	t.Parallel()

	fastLLM := &llmmock.Provider{
		StreamChunks: []llm.Chunk{
			{Text: "Indeed.", FinishReason: "stop"},
		},
	}
	strongLLM := &llmmock.Provider{}
	ttsProv := newTTS()

	e := cascade.New(fastLLM, strongLLM, ttsProv, tts.VoiceProfile{})
	t.Cleanup(func() { _ = e.Close() })

	err := e.InjectContext(context.Background(), enginepkg.ContextUpdate{
		Scene: "The player stands in a dark dungeon.",
		RecentUtterances: []memory.TranscriptEntry{
			{SpeakerID: "player1", SpeakerName: "Hero", Text: "Is anyone there?"},
		},
	})
	if err != nil {
		t.Fatalf("InjectContext: %v", err)
	}

	resp, err := e.Process(context.Background(), emptyAudioFrame, enginepkg.PromptContext{
		SystemPrompt: "You are a dungeon guardian.",
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	drainAudio(resp.Audio)
	e.Wait()

	if len(fastLLM.StreamCalls) == 0 {
		t.Fatal("fast model not called")
	}

	req := fastLLM.StreamCalls[0].Req

	// HotContext (Scene) must appear in the system prompt.
	if !strings.Contains(req.SystemPrompt, "dark dungeon") {
		t.Errorf("system prompt missing scene context, got: %q", req.SystemPrompt)
	}

	// RecentUtterances must appear as a user message in the conversation history.
	found := false
	for _, msg := range req.Messages {
		if msg.Role == "user" && strings.Contains(msg.Content, "Is anyone there?") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("recent utterance not found in messages: %+v", req.Messages)
	}
}
