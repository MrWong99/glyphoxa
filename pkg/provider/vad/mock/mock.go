// Package mock provides test doubles for the vad package interfaces.
//
// Use Engine to verify that sessions are created with the expected Config.
// Use Session to inject VADEvent responses and inspect the frames that were
// submitted for processing.
//
// Example:
//
//	sess := &mock.Session{
//	    EventResult: types.VADEvent{Type: types.VADSpeechStart, Probability: 0.9},
//	}
//	eng := &mock.Engine{Session: sess}
//	handle, _ := eng.NewSession(cfg)
package mock

import (
	"sync"

	"github.com/MrWong99/glyphoxa/pkg/provider/vad"
	"github.com/MrWong99/glyphoxa/pkg/types"
)

// NewSessionCall records a single invocation of Engine.NewSession.
type NewSessionCall struct {
	// Cfg is the Config passed to NewSession.
	Cfg vad.Config
}

// Engine is a mock implementation of vad.Engine.
type Engine struct {
	mu sync.Mutex

	// Session is the SessionHandle returned by NewSession. If nil, NewSession
	// returns a new default Session.
	Session vad.SessionHandle

	// NewSessionErr, if non-nil, is returned as the error from NewSession.
	NewSessionErr error

	// NewSessionCalls records every call to NewSession in order.
	NewSessionCalls []NewSessionCall
}

// NewSession records the call and returns Session, NewSessionErr.
func (e *Engine) NewSession(cfg vad.Config) (vad.SessionHandle, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.NewSessionCalls = append(e.NewSessionCalls, NewSessionCall{Cfg: cfg})
	if e.NewSessionErr != nil {
		return nil, e.NewSessionErr
	}
	if e.Session != nil {
		return e.Session, nil
	}
	return &Session{}, nil
}

// Reset clears all recorded calls. Thread-safe.
func (e *Engine) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.NewSessionCalls = nil
}

// Ensure Engine implements vad.Engine at compile time.
var _ vad.Engine = (*Engine)(nil)

// ProcessFrameCall records a single invocation of Session.ProcessFrame.
type ProcessFrameCall struct {
	// Frame is a copy of the bytes passed to ProcessFrame.
	Frame []byte
}

// Session is a mock implementation of vad.SessionHandle.
type Session struct {
	mu sync.Mutex

	// EventResult is returned by every ProcessFrame call.
	EventResult types.VADEvent

	// ProcessFrameErr, if non-nil, is returned by every ProcessFrame call.
	ProcessFrameErr error

	// CloseErr, if non-nil, is returned by Close.
	CloseErr error

	// --- Call records ---

	// ProcessFrameCalls records every call to ProcessFrame in order.
	ProcessFrameCalls []ProcessFrameCall

	// ResetCallCount is the number of times Reset was called.
	ResetCallCount int

	// CloseCallCount is the number of times Close was called.
	CloseCallCount int
}

// ProcessFrame records the call and returns EventResult, ProcessFrameErr.
func (s *Session) ProcessFrame(frame []byte) (types.VADEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]byte, len(frame))
	copy(cp, frame)
	s.ProcessFrameCalls = append(s.ProcessFrameCalls, ProcessFrameCall{Frame: cp})
	return s.EventResult, s.ProcessFrameErr
}

// Reset records the call by incrementing ResetCallCount.
func (s *Session) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ResetCallCount++
}

// Close records the call and returns CloseErr.
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.CloseCallCount++
	return s.CloseErr
}

// ResetCalls clears all recorded call history. Thread-safe.
func (s *Session) ResetCalls() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ProcessFrameCalls = nil
	s.ResetCallCount = 0
	s.CloseCallCount = 0
}

// Ensure Session implements vad.SessionHandle at compile time.
var _ vad.SessionHandle = (*Session)(nil)
