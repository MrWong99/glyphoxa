// Package s2s provides a [engine.VoiceEngine] implementation that wraps an
// [s2s.Provider], bridging the turn-based VoiceEngine.Process API with the
// streaming S2S session interface.
//
// An [Engine] lazily opens an S2S session on the first [Engine.Process] call
// and keeps it alive across subsequent calls. If the session dies (its Err()
// method returns non-nil), the next [Engine.Process] call transparently
// reconnects. Transcript entries are fanned-out from the session to a stable
// channel returned by [Engine.Transcripts].
//
// This package is internal because it encapsulates application-private voice
// pipeline logic and is not intended for import by external code.
package s2s

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/MrWong99/glyphoxa/internal/engine"
	"github.com/MrWong99/glyphoxa/pkg/audio"
	"github.com/MrWong99/glyphoxa/pkg/memory"
	"github.com/MrWong99/glyphoxa/pkg/provider/llm"
	providers2s "github.com/MrWong99/glyphoxa/pkg/provider/s2s"
)

// Compile-time assertion that Engine satisfies the engine.VoiceEngine interface.
var _ engine.VoiceEngine = (*Engine)(nil)

const (
	// defaultTurnTimeout is the silence duration after the last audio chunk that
	// signals the end of an S2S response turn. After this timeout forwardAudio
	// closes the per-turn audio channel so callers know generation is complete.
	defaultTurnTimeout = 2 * time.Second

	// defaultTranscriptBuf is the default buffer depth of the fan-out transcript
	// channel returned by [Engine.Transcripts].
	defaultTranscriptBuf = 64

	// defaultAudioBuf is the buffer depth of the per-turn audio channels created
	// inside [Engine.Process].
	defaultAudioBuf = 64
)

// Option is a functional option for configuring an [Engine].
type Option func(*Engine)

// WithTranscriptBuffer sets the buffer capacity of the fan-out transcript
// channel returned by [Engine.Transcripts]. Larger buffers reduce the chance
// of dropping entries when the consumer is slow. The default is 64.
func WithTranscriptBuffer(n int) Option {
	return func(e *Engine) {
		e.transcriptBuf = n
	}
}

// WithTurnTimeout overrides the default silence timeout used by [Engine.Process]
// to detect the end of an S2S response turn. Useful in tests to keep suite
// execution fast.
func WithTurnTimeout(d time.Duration) Option {
	return func(e *Engine) {
		e.turnTimeout = d
	}
}

// Engine is a [engine.VoiceEngine] implementation that wraps an [providers2s.Provider].
// It manages session lifecycle, fans-out transcripts, and bridges per-turn audio
// channels from the continuous S2S audio stream.
//
// Engine is safe for concurrent use, though the S2S audio stream is shared across
// all concurrent [Engine.Process] callers.
type Engine struct {
	provider      providers2s.Provider
	sessionCfg    providers2s.SessionConfig
	transcriptBuf int
	turnTimeout   time.Duration

	mu          sync.Mutex
	session     providers2s.SessionHandle
	toolHandler func(name string, args string) (string, error)
	tools       []llm.ToolDefinition

	transcriptCh chan memory.TranscriptEntry
	done         chan struct{}
	closed       bool

	// wg tracks all background goroutines spawned by the engine:
	//   - forwardTranscripts goroutines (one per session)
	//   - forwardAudio goroutines (one per Process call)
	// Close() waits for all of them to finish before closing transcriptCh to
	// prevent send-on-closed-channel panics.
	wg sync.WaitGroup
}

// New creates a new Engine wrapping provider and pre-configured with cfg.
// Options are applied in order. The engine does not connect to the provider
// until the first [Engine.Process] call.
func New(provider providers2s.Provider, cfg providers2s.SessionConfig, opts ...Option) *Engine {
	e := &Engine{
		provider:      provider,
		sessionCfg:    cfg,
		transcriptBuf: defaultTranscriptBuf,
		turnTimeout:   defaultTurnTimeout,
		done:          make(chan struct{}),
	}
	for _, opt := range opts {
		opt(e)
	}
	e.transcriptCh = make(chan memory.TranscriptEntry, e.transcriptBuf)
	return e
}

// ensureSessionLocked opens a new S2S session if one does not exist or if the
// current session has died (Err() != nil). It must be called with e.mu held.
//
// When creating a new session, any stored tools and tool handler are applied
// immediately and a transcript-forwarding goroutine is started.
func (e *Engine) ensureSessionLocked(ctx context.Context) error {
	if e.closed {
		return fmt.Errorf("s2s: engine is closed")
	}

	// Fast path: healthy session already open.
	if e.session != nil && e.session.Err() == nil {
		return nil
	}

	// Close dead session if one exists.
	if e.session != nil {
		_ = e.session.Close()
		e.session = nil
	}

	// Connect a new session.
	sess, err := e.provider.Connect(ctx, e.sessionCfg)
	if err != nil {
		return fmt.Errorf("s2s: connect: %w", err)
	}

	// Apply any previously registered tools and handler.
	if len(e.tools) > 0 {
		_ = sess.SetTools(e.tools)
	}
	if e.toolHandler != nil {
		sess.OnToolCall(e.toolHandler)
	}

	sess.OnError(func(err error) {
		slog.Warn("s2s non-fatal error", "err", err)
	})

	e.session = sess

	// Fan-out transcripts from the new session into the stable engine channel.
	e.wg.Add(1)
	go e.forwardTranscripts(sess.Transcripts())

	return nil
}

// Process implements [engine.VoiceEngine]. It lazily opens an S2S session,
// injects context from prompt, sends input audio, and returns a [engine.Response]
// whose Audio channel streams the model's spoken reply.
//
// Audio forwarding continues until the session produces no audio for
// [defaultTurnTimeout] (silence timeout), the session's audio channel closes,
// or the engine is closed — whichever comes first.
func (e *Engine) Process(ctx context.Context, input audio.AudioFrame, prompt engine.PromptContext) (*engine.Response, error) {
	// Hold the lock only long enough to ensure a healthy session exists and to
	// capture a stable local reference plus the session's audio channel.
	// Blocking I/O (UpdateInstructions, InjectTextContext, SendAudio) must NOT
	// be performed under e.mu: those calls can block on network I/O and would
	// starve concurrent InjectContext / SetTools / OnToolCall callers.
	e.mu.Lock()
	if err := e.ensureSessionLocked(ctx); err != nil {
		e.mu.Unlock()
		return nil, fmt.Errorf("s2s: ensure session: %w", err)
	}
	session := e.session
	// Capture the audio channel while holding the lock so that a racing Close()
	// cannot nil out e.session before we read it.
	sessionAudioCh := e.session.Audio()
	e.mu.Unlock()

	// Inject prompt context updates. SessionHandle methods are concurrency-safe
	// and may block on network I/O, so they are called without holding e.mu.
	if prompt.SystemPrompt != "" {
		_ = session.UpdateInstructions(prompt.SystemPrompt)
	}
	if prompt.HotContext != "" {
		_ = session.InjectTextContext([]providers2s.ContextItem{
			{Role: "system", Content: prompt.HotContext},
		})
	}

	// Send audio frame to the session.
	if len(input.Data) > 0 {
		if err := session.SendAudio(input.Data); err != nil {
			return nil, fmt.Errorf("s2s: send audio: %w", err)
		}
	}

	// Create a per-turn audio channel and wire it to the session's output.
	audioCh := make(chan []byte, defaultAudioBuf)
	resp := &engine.Response{
		Audio: audioCh,
	}

	e.wg.Go(func() {
		e.forwardAudio(audioCh, sessionAudioCh)
	})

	return resp, nil
}

// forwardAudio reads audio chunks from src (the session's shared audio channel)
// and writes them to dst (the per-turn channel). It closes dst when any of the
// following occur:
//   - The engine is closed (e.done is closed).
//   - src is closed (session ended).
//   - No audio chunk arrives within e.turnTimeout (silence = end of turn).
func (e *Engine) forwardAudio(dst chan<- []byte, src <-chan []byte) {
	defer close(dst)

	if src == nil {
		return
	}

	timer := time.NewTimer(e.turnTimeout)
	defer timer.Stop()

	for {
		select {
		case <-e.done:
			return

		case chunk, ok := <-src:
			if !ok {
				return
			}
			// Reset the silence timer on each received chunk. Drain the timer
			// channel first to avoid a spurious immediate expiry on the next
			// iteration (per the time.Timer documentation).
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(e.turnTimeout)

			select {
			case dst <- chunk:
			case <-e.done:
				return
			}

		case <-timer.C:
			// Silence timeout: treat as end of turn.
			return
		}
	}
}

// forwardTranscripts reads TranscriptEntry values from src (a session's
// Transcripts channel) and forwards them to e.transcriptCh. It exits when
// src closes or the engine is closed.
func (e *Engine) forwardTranscripts(src <-chan memory.TranscriptEntry) {
	defer e.wg.Done()

	for {
		select {
		case <-e.done:
			return
		case entry, ok := <-src:
			if !ok {
				return
			}
			select {
			case e.transcriptCh <- entry:
			case <-e.done:
				return
			}
		}
	}
}

// InjectContext implements [engine.VoiceEngine]. It pushes an out-of-band
// context update into the running session. If no session is open yet the
// update is silently dropped (it will be applied via Process's prompt parameter
// on the next turn).
func (e *Engine) InjectContext(_ context.Context, update engine.ContextUpdate) error {
	e.mu.Lock()
	session := e.session
	e.mu.Unlock()

	if session == nil {
		return nil
	}

	var items []providers2s.ContextItem
	if update.Identity != "" {
		items = append(items, providers2s.ContextItem{Role: "system", Content: update.Identity})
	}
	if update.Scene != "" {
		items = append(items, providers2s.ContextItem{Role: "system", Content: update.Scene})
	}
	for _, u := range update.RecentUtterances {
		items = append(items, providers2s.ContextItem{Role: "user", Content: u.Text})
	}

	if len(items) > 0 {
		return session.InjectTextContext(items)
	}
	return nil
}

// SetTools implements [engine.VoiceEngine]. It replaces the tool list and
// forwards it to the active session if one is open. The list is stored and
// applied to any future session created by ensureSessionLocked.
func (e *Engine) SetTools(tools []llm.ToolDefinition) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.tools = tools
	if e.session != nil {
		return e.session.SetTools(tools)
	}
	return nil
}

// OnToolCall implements [engine.VoiceEngine]. It stores handler and registers
// it on the active session if one is open. The handler is also applied to any
// future session created by ensureSessionLocked.
func (e *Engine) OnToolCall(handler func(name string, args string) (string, error)) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.toolHandler = handler
	if e.session != nil {
		e.session.OnToolCall(handler)
	}
}

// Transcripts implements [engine.VoiceEngine]. It returns a stable read-only
// channel that receives TranscriptEntry values from all sessions (including
// reconnected ones). The channel is closed when [Engine.Close] is called.
func (e *Engine) Transcripts() <-chan memory.TranscriptEntry {
	return e.transcriptCh
}

// Close implements [engine.VoiceEngine]. It closes the active session, signals
// all background goroutines to stop, waits for them to finish, and closes the
// Transcripts channel. Subsequent calls are no-ops and return nil.
func (e *Engine) Close() error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	e.closed = true
	close(e.done)
	session := e.session
	e.session = nil
	e.mu.Unlock()

	if session != nil {
		_ = session.Close()
	}

	// Wait for all transcript-forwarding goroutines to exit before closing the
	// channel. This prevents a send-on-closed-channel panic.
	e.wg.Wait()
	close(e.transcriptCh)

	return nil
}
