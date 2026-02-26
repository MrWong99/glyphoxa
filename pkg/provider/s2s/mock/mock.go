// Package mock provides test doubles for the s2s package interfaces.
//
// Use Provider to verify Connect calls and feed controlled S2S sessions.
// Use Session to drive the bidirectional audio/transcript streams and inspect
// which methods were invoked by the orchestrator.
//
// Example:
//
//	sess := &mock.Session{
//	    AudioCh:       make(chan []byte, 8),
//	    TranscriptsCh: make(chan memory.TranscriptEntry, 4),
//	}
//	p := &mock.Provider{Session: sess}
//	handle, _ := p.Connect(ctx, cfg)
package mock

import (
	"context"
	"sync"

	"github.com/MrWong99/glyphoxa/pkg/memory"
	"github.com/MrWong99/glyphoxa/pkg/provider/llm"
	"github.com/MrWong99/glyphoxa/pkg/provider/s2s"
)

// ConnectCall records a single invocation of Provider.Connect.
type ConnectCall struct {
	// Ctx is the context passed to Connect.
	Ctx context.Context
	// Cfg is the SessionConfig passed to Connect.
	Cfg s2s.SessionConfig
}

// Provider is a mock implementation of s2s.Provider.
type Provider struct {
	mu sync.Mutex

	// Session is the SessionHandle returned by Connect. If nil, Connect returns
	// a new default Session with buffered channels.
	Session s2s.SessionHandle

	// ConnectErr, if non-nil, is returned as the error from Connect.
	ConnectErr error

	// ProviderCapabilities is returned by Capabilities.
	ProviderCapabilities s2s.S2SCapabilities

	// ConnectCalls records every call to Connect in order.
	ConnectCalls []ConnectCall

	// CapabilitiesCallCount is the number of times Capabilities was called.
	CapabilitiesCallCount int
}

// Connect records the call and returns Session, ConnectErr.
func (p *Provider) Connect(ctx context.Context, cfg s2s.SessionConfig) (s2s.SessionHandle, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ConnectCalls = append(p.ConnectCalls, ConnectCall{Ctx: ctx, Cfg: cfg})
	if p.ConnectErr != nil {
		return nil, p.ConnectErr
	}
	if p.Session != nil {
		return p.Session, nil
	}
	return &Session{
		AudioCh:       make(chan []byte, 64),
		TranscriptsCh: make(chan memory.TranscriptEntry, 16),
	}, nil
}

// Capabilities records the call and returns ProviderCapabilities.
func (p *Provider) Capabilities() s2s.S2SCapabilities {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.CapabilitiesCallCount++
	return p.ProviderCapabilities
}

// Reset clears all recorded calls. Thread-safe.
func (p *Provider) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ConnectCalls = nil
	p.CapabilitiesCallCount = 0
}

// Ensure Provider implements s2s.Provider at compile time.
var _ s2s.Provider = (*Provider)(nil)

// SendAudioCall records a single invocation of Session.SendAudio.
type SendAudioCall struct {
	// Chunk is a copy of the audio bytes that were passed to SendAudio.
	Chunk []byte
}

// SetToolsCall records a single invocation of Session.SetTools.
type SetToolsCall struct {
	// Tools is a copy of the tool definitions passed to SetTools.
	Tools []llm.ToolDefinition
}

// UpdateInstructionsCall records a single invocation of Session.UpdateInstructions.
type UpdateInstructionsCall struct {
	// Instructions is the string passed to UpdateInstructions.
	Instructions string
}

// InjectTextContextCall records a single invocation of Session.InjectTextContext.
type InjectTextContextCall struct {
	// Items is a copy of the context items passed to InjectTextContext.
	Items []s2s.ContextItem
}

// Session is a mock implementation of s2s.SessionHandle.
// Callers should pre-populate AudioCh and TranscriptsCh, then close them to
// signal end-of-session.
type Session struct {
	mu sync.Mutex

	// AudioCh is the channel returned by Audio(). Callers own this channel.
	AudioCh chan []byte

	// TranscriptsCh is the channel returned by Transcripts(). Callers own this
	// channel.
	TranscriptsCh chan memory.TranscriptEntry

	// toolCallHandler is the currently registered ToolCallHandler.
	toolCallHandler s2s.ToolCallHandler

	// --- Configurable errors ---

	// SendAudioErr, if non-nil, is returned by every SendAudio call.
	SendAudioErr error

	// SetToolsErr, if non-nil, is returned by every SetTools call.
	SetToolsErr error

	// UpdateInstructionsErr, if non-nil, is returned by every UpdateInstructions call.
	UpdateInstructionsErr error

	// InjectTextContextErr, if non-nil, is returned by every InjectTextContext call.
	InjectTextContextErr error

	// InterruptErr, if non-nil, is returned by every Interrupt call.
	InterruptErr error

	// CloseErr, if non-nil, is returned by Close.
	CloseErr error

	// --- Call records ---

	// SendAudioCalls records every call to SendAudio in order.
	SendAudioCalls []SendAudioCall

	// SetToolsCalls records every call to SetTools in order.
	SetToolsCalls []SetToolsCall

	// UpdateInstructionsCalls records every call to UpdateInstructions in order.
	UpdateInstructionsCalls []UpdateInstructionsCall

	// InjectTextContextCalls records every call to InjectTextContext in order.
	InjectTextContextCalls []InjectTextContextCall

	// InterruptCallCount is the number of times Interrupt was called.
	InterruptCallCount int

	// CloseCallCount is the number of times Close was called.
	CloseCallCount int

	// OnToolCallSetCount is the number of times OnToolCall was called.
	OnToolCallSetCount int
}

// SendAudio records the call and returns SendAudioErr.
func (s *Session) SendAudio(chunk []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]byte, len(chunk))
	copy(cp, chunk)
	s.SendAudioCalls = append(s.SendAudioCalls, SendAudioCall{Chunk: cp})
	return s.SendAudioErr
}

// Audio returns AudioCh.
func (s *Session) Audio() <-chan []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.AudioCh
}

// Transcripts returns TranscriptsCh.
func (s *Session) Transcripts() <-chan memory.TranscriptEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.TranscriptsCh
}

// OnToolCall stores the handler and increments OnToolCallSetCount.
func (s *Session) OnToolCall(handler s2s.ToolCallHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.toolCallHandler = handler
	s.OnToolCallSetCount++
}

// Handler returns the currently registered ToolCallHandler. Thread-safe.
// Useful in tests to verify the correct handler was registered.
func (s *Session) Handler() s2s.ToolCallHandler {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.toolCallHandler
}

// SetTools records the call and returns SetToolsErr.
func (s *Session) SetTools(tools []llm.ToolDefinition) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]llm.ToolDefinition, len(tools))
	copy(cp, tools)
	s.SetToolsCalls = append(s.SetToolsCalls, SetToolsCall{Tools: cp})
	return s.SetToolsErr
}

// UpdateInstructions records the call and returns UpdateInstructionsErr.
func (s *Session) UpdateInstructions(instructions string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.UpdateInstructionsCalls = append(s.UpdateInstructionsCalls, UpdateInstructionsCall{Instructions: instructions})
	return s.UpdateInstructionsErr
}

// InjectTextContext records the call and returns InjectTextContextErr.
func (s *Session) InjectTextContext(items []s2s.ContextItem) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]s2s.ContextItem, len(items))
	copy(cp, items)
	s.InjectTextContextCalls = append(s.InjectTextContextCalls, InjectTextContextCall{Items: cp})
	return s.InjectTextContextErr
}

// Interrupt records the call and returns InterruptErr.
func (s *Session) Interrupt() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.InterruptCallCount++
	return s.InterruptErr
}

// Close records the call and returns CloseErr.
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.CloseCallCount++
	return s.CloseErr
}

// ResetCalls clears all recorded calls. Thread-safe.
func (s *Session) ResetCalls() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.SendAudioCalls = nil
	s.SetToolsCalls = nil
	s.UpdateInstructionsCalls = nil
	s.InjectTextContextCalls = nil
	s.InterruptCallCount = 0
	s.CloseCallCount = 0
	s.OnToolCallSetCount = 0
}

// Ensure Session implements s2s.SessionHandle at compile time.
var _ s2s.SessionHandle = (*Session)(nil)
