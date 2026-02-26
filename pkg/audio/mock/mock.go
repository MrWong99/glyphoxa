// Package mock provides in-memory mock implementations of the [audio.Platform],
// [audio.Connection], and [audio.Mixer] interfaces for use in unit tests.
//
// All mocks are safe for concurrent use. They record every method call so that
// tests can assert on call counts and arguments, and they expose exported fields
// that the test can set to control return values.
//
// Typical usage:
//
//	out := make(chan audio.AudioFrame, 16)
//	conn := &mock.Connection{
//	    InputStreamsResult: map[string]<-chan audio.AudioFrame{
//	        "user-1": make(chan audio.AudioFrame),
//	    },
//	    OutputStreamResult: out,
//	}
//	platform := &mock.Platform{ConnectResult: conn}
//	got, err := platform.Connect(ctx, "channel-42")
package mock

import (
	"context"
	"sync"
	"time"

	"github.com/MrWong99/glyphoxa/pkg/audio"
)

// ─── Connection ───────────────────────────────────────────────────────────────

// Connection is a mock implementation of [audio.Connection].
// Set the exported Result fields before use; inspect the Call* fields after.
type Connection struct {
	mu sync.Mutex

	// InputStreamsResult is returned by [Connection.InputStreams].
	// Defaults to an empty (non-nil) map if left nil.
	InputStreamsResult map[string]<-chan audio.AudioFrame

	// OutputStreamResult is returned by [Connection.OutputStream].
	OutputStreamResult chan<- audio.AudioFrame

	// DisconnectError is returned by [Connection.Disconnect].
	DisconnectError error

	// CallCountInputStreams records how many times InputStreams was called.
	CallCountInputStreams int

	// CallCountOutputStream records how many times OutputStream was called.
	CallCountOutputStream int

	// CallCountDisconnect records how many times Disconnect was called.
	CallCountDisconnect int

	// CallCountOnParticipantChange records how many times OnParticipantChange was called.
	CallCountOnParticipantChange int

	// RecordedCallbacks holds the callbacks registered via OnParticipantChange,
	// in order of registration.
	RecordedCallbacks []func(audio.Event)
}

// InputStreams implements [audio.Connection]. Returns InputStreamsResult.
// If InputStreamsResult is nil, an empty non-nil map is returned.
func (c *Connection) InputStreams() map[string]<-chan audio.AudioFrame {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.CallCountInputStreams++
	if c.InputStreamsResult == nil {
		return map[string]<-chan audio.AudioFrame{}
	}
	return c.InputStreamsResult
}

// OutputStream implements [audio.Connection]. Returns OutputStreamResult.
func (c *Connection) OutputStream() chan<- audio.AudioFrame {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.CallCountOutputStream++
	return c.OutputStreamResult
}

// OnParticipantChange implements [audio.Connection].
// The callback is appended to RecordedCallbacks. To simulate events in tests,
// call [Connection.EmitEvent].
func (c *Connection) OnParticipantChange(cb func(audio.Event)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.CallCountOnParticipantChange++
	c.RecordedCallbacks = append(c.RecordedCallbacks, cb)
}

// Disconnect implements [audio.Connection]. Returns DisconnectError.
func (c *Connection) Disconnect() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.CallCountDisconnect++
	return c.DisconnectError
}

// EmitEvent calls all registered participant-change callbacks with the given event.
// Use this in tests to simulate participants joining or leaving.
func (c *Connection) EmitEvent(ev audio.Event) {
	c.mu.Lock()
	cbs := make([]func(audio.Event), len(c.RecordedCallbacks))
	copy(cbs, c.RecordedCallbacks)
	c.mu.Unlock()
	for _, cb := range cbs {
		cb(ev)
	}
}

// ─── Platform ─────────────────────────────────────────────────────────────────

// ConnectCall records the arguments of a single [Platform.Connect] invocation.
type ConnectCall struct {
	// ChannelID is the channelID argument passed to Connect.
	ChannelID string
}

// Platform is a mock implementation of [audio.Platform].
type Platform struct {
	mu sync.Mutex

	// ConnectResult is the [audio.Connection] returned by Connect.
	ConnectResult audio.Connection

	// ConnectError is the error returned by Connect.
	ConnectError error

	// ConnectCalls records all Connect invocations.
	ConnectCalls []ConnectCall
}

// Connect implements [audio.Platform]. Records the call and returns ConnectResult / ConnectError.
func (p *Platform) Connect(_ context.Context, channelID string) (audio.Connection, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ConnectCalls = append(p.ConnectCalls, ConnectCall{ChannelID: channelID})
	return p.ConnectResult, p.ConnectError
}

// ─── Mixer ────────────────────────────────────────────────────────────────────

// EnqueueCall records the arguments of a single [Mixer.Enqueue] invocation.
type EnqueueCall struct {
	// Segment is the audio segment passed to Enqueue.
	Segment audio.AudioSegment
	// Priority is the priority argument passed to Enqueue.
	Priority int
}

// InterruptCall records the arguments of a single [Mixer.Interrupt] invocation.
type InterruptCall struct {
	// Reason is the interrupt reason passed to Interrupt.
	Reason audio.InterruptReason
}

// SetGapCall records the arguments of a single [Mixer.SetGap] invocation.
type SetGapCall struct {
	// Duration is the gap duration passed to SetGap.
	Duration time.Duration
}

// Mixer is a mock implementation of [audio.Mixer].
type Mixer struct {
	mu sync.Mutex

	// EnqueueCalls records all Enqueue invocations.
	EnqueueCalls []EnqueueCall

	// InterruptCalls records all Interrupt invocations.
	InterruptCalls []InterruptCall

	// SetGapCalls records all SetGap invocations.
	SetGapCalls []SetGapCall

	// CallCountOnBargeIn records how many times OnBargeIn was called.
	CallCountOnBargeIn int

	// BargeInHandlers holds the handlers registered via OnBargeIn in registration order.
	BargeInHandlers []func(speakerID string)
}

// Enqueue implements [audio.Mixer]. Records the call arguments.
func (m *Mixer) Enqueue(segment audio.AudioSegment, priority int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.EnqueueCalls = append(m.EnqueueCalls, EnqueueCall{Segment: segment, Priority: priority})
}

// Interrupt implements [audio.Mixer]. Records the reason.
func (m *Mixer) Interrupt(reason audio.InterruptReason) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.InterruptCalls = append(m.InterruptCalls, InterruptCall{Reason: reason})
}

// OnBargeIn implements [audio.Mixer]. Appends handler to BargeInHandlers.
func (m *Mixer) OnBargeIn(handler func(speakerID string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.CallCountOnBargeIn++
	m.BargeInHandlers = append(m.BargeInHandlers, handler)
}

// SetGap implements [audio.Mixer]. Records the gap duration.
func (m *Mixer) SetGap(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SetGapCalls = append(m.SetGapCalls, SetGapCall{Duration: d})
}

// TriggerBargeIn calls all registered barge-in handlers with speakerID.
// Use this in tests to simulate a player interrupting an NPC.
func (m *Mixer) TriggerBargeIn(speakerID string) {
	m.mu.Lock()
	handlers := make([]func(string), len(m.BargeInHandlers))
	copy(handlers, m.BargeInHandlers)
	m.mu.Unlock()
	for _, h := range handlers {
		h(speakerID)
	}
}
