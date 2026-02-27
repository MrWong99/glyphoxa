// Package cascade implements an experimental dual-model sentence cascade engine.
//
// The cascade reduces perceived voice latency by starting TTS playback with a
// fast model's opening sentence while a stronger model generates the substantive
// continuation. The two outputs are stitched into a single seamless audio stream.
//
// # Architecture
//
//  1. Player finishes speaking → STT finalises transcript.
//  2. Fast model (e.g., GPT-4o-mini, Gemini Flash) generates only the first
//     sentence (~200 ms TTFT).
//  3. TTS starts immediately on the first sentence.
//  4. Strong model (e.g., Claude Sonnet, GPT-4o) receives the same prompt plus
//     the fast model's first sentence as a forced continuation prefix.
//  5. TTS continues with the strong model's output → seamless single utterance.
//
// This is opt-in per NPC via the cascade_mode configuration field and is not
// recommended for simple greetings or combat callouts where a single fast model
// suffices.
package cascade

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/MrWong99/glyphoxa/internal/engine"
	"github.com/MrWong99/glyphoxa/pkg/audio"
	"github.com/MrWong99/glyphoxa/pkg/memory"
	"github.com/MrWong99/glyphoxa/pkg/provider/llm"
	"github.com/MrWong99/glyphoxa/pkg/provider/stt"
	"github.com/MrWong99/glyphoxa/pkg/provider/tts"
)

const (
	// defaultOpenerSuffix is the instruction appended to the fast model's system
	// prompt to constrain it to a brief, in-character opening reaction.
	defaultOpenerSuffix = "Generate only a brief, in-character opening reaction. Do not reveal key information in the first sentence."

	// defaultTranscriptBuf is the default buffer depth of the transcript channel.
	defaultTranscriptBuf = 32

	// defaultTextBuf is the buffer depth of the text channel passed to TTS in the
	// dual-model path. Sized to absorb the opener plus several strong-model sentences
	// without blocking the synthesis goroutine.
	defaultTextBuf = 16
)

// Engine implements [engine.VoiceEngine] using a dual-model sentence cascade.
//
// A fast LLM produces the NPC's opening sentence immediately so TTS can start
// playing within ~500 ms. A strong LLM then generates the continuation, receiving
// the opener as a forced assistant-role prefix so the response sounds seamless.
//
// Engine is safe for concurrent use. Multiple concurrent [Engine.Process] calls
// are allowed; each spawns an independent goroutine for the strong-model stage.
type Engine struct {
	fastLLM      llm.Provider
	strongLLM    llm.Provider
	ttsP         tts.Provider
	voice        tts.VoiceProfile
	sttP         stt.Provider // nil = text-only mode (STT skipped)

	openerSuffix  string
	transcriptBuf int

	mu            sync.Mutex
	toolHandler   func(name, args string) (string, error)
	tools         []llm.ToolDefinition
	pendingUpdate *engine.ContextUpdate
	transcriptCh  chan memory.TranscriptEntry
	done          chan struct{}
	closed        bool

	// wg tracks background goroutines spawned by Process so callers (and tests)
	// can synchronise with the end of the strong-model stage.
	wg sync.WaitGroup
}

// Compile-time assertion that Engine satisfies the engine.VoiceEngine interface.
var _ engine.VoiceEngine = (*Engine)(nil)

// Option is a functional option for configuring an Engine during construction.
type Option func(*Engine)

// WithSTT configures an STT provider for audio input processing.
// When set, [Engine.Process] will transcribe audio frames before LLM generation.
// If nil, audio input is ignored and text from the PromptContext is used directly.
func WithSTT(s stt.Provider) Option {
	return func(e *Engine) { e.sttP = s }
}

// WithTranscriptBuffer sets the buffer capacity of the transcript channel
// returned by [Engine.Transcripts]. Default is 32.
func WithTranscriptBuffer(n int) Option {
	return func(e *Engine) { e.transcriptBuf = n }
}

// WithOpenerPromptSuffix overrides the instruction appended to the fast model's
// system prompt. The default instructs the model to generate only a brief,
// in-character opening reaction without revealing key information.
func WithOpenerPromptSuffix(s string) Option {
	return func(e *Engine) { e.openerSuffix = s }
}

// New constructs a cascade Engine backed by the given providers and voice profile.
// Options are applied after the engine is initialised with its defaults.
func New(fastLLM, strongLLM llm.Provider, ttsP tts.Provider, voice tts.VoiceProfile, opts ...Option) *Engine {
	e := &Engine{
		fastLLM:       fastLLM,
		strongLLM:     strongLLM,
		ttsP:          ttsP,
		voice:         voice,
		openerSuffix:  defaultOpenerSuffix,
		transcriptBuf: defaultTranscriptBuf,
		done:          make(chan struct{}),
	}
	for _, o := range opts {
		o(e)
	}
	// Create transcript channel after options so WithTranscriptBuffer takes effect.
	e.transcriptCh = make(chan memory.TranscriptEntry, e.transcriptBuf)
	return e
}

// ─── VoiceEngine interface ────────────────────────────────────────────────────

// Process handles a complete voice interaction using the dual-model sentence cascade.
//
// It applies any pending [engine.ContextUpdate] from a prior [Engine.InjectContext]
// call, then:
//  1. Sends the prompt to the fast model with an opener instruction.
//  2. Collects the first sentence of the fast model's reply.
//  3. If the fast model's response is a single sentence, synthesises it directly
//     (single-model path — no strong model involved).
//  4. Otherwise, begins TTS on the opener immediately and in a background goroutine
//     calls the strong model with the opener as a forced assistant-role continuation
//     prefix, forwarding its output to the same TTS stream.
//
// The returned [engine.Response] is available as soon as TTS synthesis starts;
// audio continues streaming after Process returns.
func (e *Engine) Process(ctx context.Context, _ audio.AudioFrame, prompt engine.PromptContext) (*engine.Response, error) {
	// Apply and consume any pending context update atomically.
	e.mu.Lock()
	if e.pendingUpdate != nil {
		prompt = mergeContextUpdate(prompt, *e.pendingUpdate)
		e.pendingUpdate = nil
	}
	tools := make([]llm.ToolDefinition, len(e.tools))
	copy(tools, e.tools)
	e.mu.Unlock()

	// ── Stage 1: Fast model → opener ─────────────────────────────────────────

	fastReq := e.buildFastPrompt(prompt)
	fastCh, err := e.fastLLM.StreamCompletion(ctx, fastReq)
	if err != nil {
		return nil, fmt.Errorf("cascade: fast model stream failed: %w", err)
	}

	opener, fastFull := e.collectFirstSentence(ctx, fastCh)
	if opener == "" {
		opener = "..." // guard: prevent silent TTS on empty opener
	}

	// ── Stage 2a: Single-model path (fast model was complete in one sentence) ─

	if fastFull {
		textCh := make(chan string, 1)
		textCh <- opener
		close(textCh)

		audioCh, err := e.ttsP.SynthesizeStream(ctx, textCh, e.voice)
		if err != nil {
			return nil, fmt.Errorf("cascade: TTS start failed: %w", err)
		}
		return &engine.Response{Text: opener, Audio: audioCh}, nil
	}

	// ── Stage 2b: Dual-model path ─────────────────────────────────────────────

	// Create the shared text channel that feeds the TTS stream.
	textCh := make(chan string, defaultTextBuf)
	audioCh, err := e.ttsP.SynthesizeStream(ctx, textCh, e.voice)
	if err != nil {
		return nil, fmt.Errorf("cascade: TTS start failed: %w", err)
	}

	strongReq := e.buildStrongPrompt(prompt, tools, opener)
	resp := &engine.Response{Text: opener, Audio: audioCh}

	// Background goroutine: send opener → strong model → close textCh.
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		defer close(textCh)

		// Deliver the opener to TTS immediately so playback begins.
		select {
		case textCh <- opener:
		case <-ctx.Done():
			return
		}

		// Launch the strong model.
		strongCh, err := e.strongLLM.StreamCompletion(ctx, strongReq)
		if err != nil {
			resp.SetStreamErr(fmt.Errorf("cascade: strong model stream failed: %w", err))
			return
		}

		// Forward the strong model's output as sentence-level chunks to TTS.
		e.forwardSentences(ctx, strongCh, textCh, resp)
	}()

	return resp, nil
}

// InjectContext queues a context update to be merged on the next [Engine.Process]
// call. It is non-blocking and safe to call concurrently.
func (e *Engine) InjectContext(_ context.Context, update engine.ContextUpdate) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pendingUpdate = &update
	return nil
}

// SetTools replaces the tool set offered to the strong model on the next
// [Engine.Process] call. The fast model never receives tools.
// Pass a nil or empty slice to disable tool calling.
func (e *Engine) SetTools(tools []llm.ToolDefinition) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(tools) == 0 {
		e.tools = nil
		return nil
	}
	cp := make([]llm.ToolDefinition, len(tools))
	copy(cp, tools)
	e.tools = cp
	return nil
}

// OnToolCall registers handler as the executor for LLM tool calls issued by the
// strong model. Only the most recently registered handler is active.
func (e *Engine) OnToolCall(handler func(name string, args string) (string, error)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.toolHandler = handler
}

// Transcripts returns a read-only channel that emits [memory.TranscriptEntry]
// values. The channel is closed when the engine is closed.
//
// The returned channel is the same value for the lifetime of the engine —
// it is assigned once in [New] and never mutated — so no lock is required.
func (e *Engine) Transcripts() <-chan memory.TranscriptEntry {
	return e.transcriptCh
}

// Close releases all resources held by the engine and closes the Transcripts
// channel. Close is safe to call multiple times; subsequent calls return nil.
func (e *Engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil
	}
	e.closed = true
	close(e.done)
	close(e.transcriptCh)
	return nil
}

// Wait blocks until all background goroutines spawned by [Engine.Process] have
// finished. This is primarily useful in tests to synchronise before inspecting
// mock call records.
func (e *Engine) Wait() {
	e.wg.Wait()
}

// ─── Internal helpers ─────────────────────────────────────────────────────────

// buildFastPrompt constructs the [llm.CompletionRequest] for the fast model.
// It appends the opener instruction to the system prompt and excludes tools so
// the fast model stays fast and on-topic.
func (e *Engine) buildFastPrompt(prompt engine.PromptContext) llm.CompletionRequest {
	var sb strings.Builder
	sb.WriteString(prompt.SystemPrompt)
	if prompt.HotContext != "" {
		sb.WriteString("\n\n")
		sb.WriteString(prompt.HotContext)
	}
	if e.openerSuffix != "" {
		sb.WriteString("\n\n")
		sb.WriteString(e.openerSuffix)
	}

	msgs := make([]llm.Message, len(prompt.Messages))
	copy(msgs, prompt.Messages)

	return llm.CompletionRequest{
		SystemPrompt: sb.String(),
		Messages:     msgs,
		// Tools intentionally omitted: fast model does not use tools.
	}
}

// buildStrongPrompt constructs the [llm.CompletionRequest] for the strong model.
// It injects the fast model's opener as a forced assistant-role continuation
// prefix so the strong model generates a seamless continuation.
func (e *Engine) buildStrongPrompt(prompt engine.PromptContext, tools []llm.ToolDefinition, opener string) llm.CompletionRequest {
	var sb strings.Builder
	sb.WriteString(prompt.SystemPrompt)
	if prompt.HotContext != "" {
		sb.WriteString("\n\n")
		sb.WriteString(prompt.HotContext)
	}

	// Append existing messages then inject the opener as an assistant prefix.
	msgs := make([]llm.Message, len(prompt.Messages)+1)
	copy(msgs, prompt.Messages)
	msgs[len(prompt.Messages)] = llm.Message{
		Role:    "assistant",
		Content: opener,
	}

	return llm.CompletionRequest{
		SystemPrompt: sb.String(),
		Messages:     msgs,
		Tools:        tools,
	}
}

// collectFirstSentence reads token chunks from ch and returns the first complete
// sentence — defined as text ending with '.', '!', or '?' immediately followed by
// a whitespace character. If the stream ends before a sentence boundary is
// detected, the entire accumulated text is returned with full=true (meaning the
// fast model's response was one sentence or fewer, so the strong model is
// unnecessary).
//
// When full is false, remaining chunks in ch are drained in a background goroutine
// to prevent the provider's goroutine from leaking.
func (e *Engine) collectFirstSentence(ctx context.Context, ch <-chan llm.Chunk) (sentence string, full bool) {
	var buf strings.Builder
	for {
		select {
		case <-ctx.Done():
			return buf.String(), true
		case chunk, ok := <-ch:
			if !ok {
				// Channel closed without a finish-reason chunk.
				return buf.String(), true
			}
			buf.WriteString(chunk.Text)

			// A finish-reason marks the end of the stream — the entire
			// response fits in this buffer, so no strong model is needed.
			if chunk.FinishReason != "" {
				return buf.String(), true
			}

			// Look for a sentence boundary only while the stream is live.
			if idx := firstSentenceBoundary(buf.String()); idx >= 0 {
				s := buf.String()[:idx+1]
				// Drain remaining fast-model output to avoid goroutine leaks.
				go drainChunks(ch)
				return s, false
			}
		}
	}
}

// forwardSentences reads token chunks from ch, accumulates them into complete
// sentences, and writes each sentence to textCh. Any text remaining when the
// stream ends is flushed as a final fragment. Errors are recorded via resp.
func (e *Engine) forwardSentences(ctx context.Context, ch <-chan llm.Chunk, textCh chan<- string, resp *engine.Response) {
	var buf strings.Builder
	for {
		select {
		case <-ctx.Done():
			return
		case chunk, ok := <-ch:
			if !ok {
				// Channel closed: flush remaining text.
				if buf.Len() > 0 {
					select {
					case textCh <- buf.String():
					case <-ctx.Done():
					}
				}
				return
			}

			if chunk.Text != "" {
				buf.WriteString(chunk.Text)
			}

			// Flush complete sentences eagerly for lower TTS latency.
			for {
				idx := firstSentenceBoundary(buf.String())
				if idx < 0 {
					break
				}
				sentence := buf.String()[:idx+1]
				rest := buf.String()[idx+1:]
				buf.Reset()
				buf.WriteString(strings.TrimLeft(rest, " \t\n\r"))
				select {
				case textCh <- sentence:
				case <-ctx.Done():
					return
				}
			}

			// On the final chunk, flush any remaining partial sentence.
			if chunk.FinishReason != "" {
				if buf.Len() > 0 {
					select {
					case textCh <- buf.String():
					case <-ctx.Done():
					}
				}
				return
			}
		}
	}
}

// firstSentenceBoundary returns the index of the first '.', '!', or '?'
// character that is immediately followed by a whitespace character (' ', '\n',
// '\r', or '\t'). Returns -1 if no such boundary exists in s.
func firstSentenceBoundary(s string) int {
	for i := 0; i < len(s)-1; i++ {
		switch s[i] {
		case '.', '!', '?':
			switch s[i+1] {
			case ' ', '\n', '\r', '\t':
				return i
			}
		}
	}
	return -1
}

// drainChunks discards all remaining chunks from ch. Used to prevent the LLM
// provider's internal goroutine from blocking when collectFirstSentence returns
// before the stream is exhausted.
func drainChunks(ch <-chan llm.Chunk) {
	for range ch {
	}
}

// mergeContextUpdate applies a [engine.ContextUpdate] onto a [engine.PromptContext],
// returning the merged result. Zero-value fields in update are ignored.
func mergeContextUpdate(prompt engine.PromptContext, update engine.ContextUpdate) engine.PromptContext {
	if update.Identity != "" {
		prompt.SystemPrompt = update.Identity
	}
	if update.Scene != "" {
		prompt.HotContext = update.Scene
	}
	if len(update.RecentUtterances) > 0 {
		extra := make([]llm.Message, len(update.RecentUtterances))
		for i, u := range update.RecentUtterances {
			role := "user"
			if u.IsNPC() {
				role = "assistant"
			}
			extra[i] = llm.Message{
				Role:    role,
				Content: u.Text,
				Name:    u.SpeakerName,
			}
		}
		msgs := make([]llm.Message, len(prompt.Messages)+len(extra))
		copy(msgs, prompt.Messages)
		copy(msgs[len(prompt.Messages):], extra)
		prompt.Messages = msgs
	}
	return prompt
}
