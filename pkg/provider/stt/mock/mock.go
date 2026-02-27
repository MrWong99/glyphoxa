// Package mock provides test doubles for the stt package interfaces.
//
// Use Provider to verify that the caller starts sessions with the expected
// StreamConfig. Use Session to feed controlled Transcript values and inspect
// which audio chunks were delivered.
//
// Example:
//
//	sess := &mock.Session{
//	    PartialsCh: make(chan stt.Transcript, 1),
//	    FinalsCh:   make(chan stt.Transcript, 1),
//	}
//	p := &mock.Provider{Session: sess}
//	handle, _ := p.StartStream(ctx, cfg)
package mock

import (
	"context"
	"sync"

	"github.com/MrWong99/glyphoxa/pkg/provider/stt"
)

// StartStreamCall records a single invocation of Provider.StartStream.
type StartStreamCall struct {
	// Ctx is the context passed to StartStream.
	Ctx context.Context
	// Cfg is the StreamConfig passed to StartStream.
	Cfg stt.StreamConfig
}

// Provider is a mock implementation of stt.Provider.
type Provider struct {
	mu sync.Mutex

	// Session is the SessionHandle returned by StartStream. If nil, StartStream
	// returns a new default Session with buffered channels.
	Session stt.SessionHandle

	// StartStreamErr, if non-nil, is returned as the error from StartStream.
	StartStreamErr error

	// StartStreamCalls records every call to StartStream.
	StartStreamCalls []StartStreamCall
}

// StartStream records the call and returns Session, StartStreamErr.
func (p *Provider) StartStream(ctx context.Context, cfg stt.StreamConfig) (stt.SessionHandle, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.StartStreamCalls = append(p.StartStreamCalls, StartStreamCall{Ctx: ctx, Cfg: cfg})
	if p.StartStreamErr != nil {
		return nil, p.StartStreamErr
	}
	if p.Session != nil {
		return p.Session, nil
	}
	return &Session{
		PartialsCh: make(chan stt.Transcript, 16),
		FinalsCh:   make(chan stt.Transcript, 16),
	}, nil
}

// Reset clears all recorded calls. Thread-safe.
func (p *Provider) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.StartStreamCalls = nil
}

// Ensure Provider implements stt.Provider at compile time.
var _ stt.Provider = (*Provider)(nil)

// SendAudioCall records a single invocation of Session.SendAudio.
type SendAudioCall struct {
	// Chunk is a copy of the audio bytes that were passed to SendAudio.
	Chunk []byte
}

// SetKeywordsCall records a single invocation of Session.SetKeywords.
type SetKeywordsCall struct {
	// Keywords is a copy of the keyword list passed to SetKeywords.
	Keywords []stt.KeywordBoost
}

// Session is a mock implementation of stt.SessionHandle.
// Callers should pre-populate PartialsCh and FinalsCh with the Transcript values
// they want the consumer to receive, then close them when done.
type Session struct {
	mu sync.Mutex

	// PartialsCh is the channel returned by Partials(). Callers own this channel
	// and are responsible for sending to and closing it in tests.
	PartialsCh chan stt.Transcript

	// FinalsCh is the channel returned by Finals(). Callers own this channel.
	FinalsCh chan stt.Transcript

	// SendAudioErr, if non-nil, is returned by every SendAudio call.
	SendAudioErr error

	// SetKeywordsErr, if non-nil, is returned by every SetKeywords call.
	SetKeywordsErr error

	// CloseErr, if non-nil, is returned by Close.
	CloseErr error

	// --- Call records ---

	// SendAudioCalls records every call to SendAudio in order.
	SendAudioCalls []SendAudioCall

	// SetKeywordsCalls records every call to SetKeywords in order.
	SetKeywordsCalls []SetKeywordsCall

	// CloseCallCount is the number of times Close was called.
	CloseCallCount int
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

// Partials returns PartialsCh. The caller must have initialised PartialsCh before
// calling this method.
func (s *Session) Partials() <-chan stt.Transcript {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.PartialsCh
}

// Finals returns FinalsCh.
func (s *Session) Finals() <-chan stt.Transcript {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.FinalsCh
}

// SetKeywords records the call and returns SetKeywordsErr.
func (s *Session) SetKeywords(keywords []stt.KeywordBoost) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	kw := make([]stt.KeywordBoost, len(keywords))
	copy(kw, keywords)
	s.SetKeywordsCalls = append(s.SetKeywordsCalls, SetKeywordsCall{Keywords: kw})
	return s.SetKeywordsErr
}

// SendAudioCallCount returns the number of SendAudio calls. Thread-safe.
func (s *Session) SendAudioCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.SendAudioCalls)
}

// Close records the call and returns CloseErr.
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.CloseCallCount++
	return s.CloseErr
}

// Reset clears all recorded calls. Thread-safe.
func (s *Session) ResetCalls() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.SendAudioCalls = nil
	s.SetKeywordsCalls = nil
	s.CloseCallCount = 0
}

// Ensure Session implements stt.SessionHandle at compile time.
var _ stt.SessionHandle = (*Session)(nil)
