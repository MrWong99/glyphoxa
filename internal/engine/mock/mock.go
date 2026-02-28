// Package mock provides an in-memory mock implementation of [engine.VoiceEngine]
// for use in unit tests.
//
// The mock records every method call and allows the test to configure return values
// via exported fields. It is safe for concurrent use.
//
// Example:
//
//	audioCh := make(chan []byte)
//	close(audioCh)
//	e := &mock.VoiceEngine{
//	    ProcessResult: &engine.Response{
//	        Text:  "Well met, traveller.",
//	        Audio: audioCh,
//	    },
//	}
//	resp, err := e.Process(ctx, frame, prompt)
package mock

import (
	"context"
	"sync"

	"github.com/MrWong99/glyphoxa/internal/engine"
	"github.com/MrWong99/glyphoxa/pkg/audio"
	"github.com/MrWong99/glyphoxa/pkg/memory"
	"github.com/MrWong99/glyphoxa/pkg/provider/llm"
)

// Compile-time interface assertion.
var _ engine.VoiceEngine = (*VoiceEngine)(nil)

// ProcessCall records the arguments of a single [VoiceEngine.Process] call.
type ProcessCall struct {
	// Input is the audio frame passed to Process.
	Input audio.AudioFrame
	// Prompt is the prompt context passed to Process.
	Prompt engine.PromptContext
}

// InjectContextCall records the arguments of a single [VoiceEngine.InjectContext] call.
type InjectContextCall struct {
	// Update is the context update passed to InjectContext.
	Update engine.ContextUpdate
}

// SetToolsCall records the arguments of a single [VoiceEngine.SetTools] call.
type SetToolsCall struct {
	// Tools is the tool list passed to SetTools.
	Tools []llm.ToolDefinition
}

// VoiceEngine is a mock implementation of [engine.VoiceEngine].
// All exported *Result and *Error fields control return values.
// All exported Call* fields accumulate invocation records.
type VoiceEngine struct {
	mu sync.Mutex

	// ProcessResult is returned by [VoiceEngine.Process] (may be nil).
	ProcessResult *engine.Response

	// ProcessError is the error returned by [VoiceEngine.Process].
	ProcessError error

	// InjectContextError is returned by [VoiceEngine.InjectContext].
	InjectContextError error

	// SetToolsError is returned by [VoiceEngine.SetTools].
	SetToolsError error

	// CloseError is returned by [VoiceEngine.Close].
	CloseError error

	// TranscriptsResult is the channel returned by [VoiceEngine.Transcripts].
	// If nil, a pre-closed channel is returned.
	TranscriptsResult <-chan memory.TranscriptEntry

	// ProcessCalls records all Process invocations.
	ProcessCalls []ProcessCall

	// InjectContextCalls records all InjectContext invocations.
	InjectContextCalls []InjectContextCall

	// SetToolsCalls records all SetTools invocations.
	SetToolsCalls []SetToolsCall

	// CallCountOnToolCall records how many times OnToolCall was called.
	CallCountOnToolCall int

	// ToolCallHandlers holds all handlers registered via OnToolCall in registration order.
	ToolCallHandlers []func(name string, args string) (string, error)

	// CallCountClose records how many times Close was called.
	CallCountClose int
}

// Process implements [engine.VoiceEngine].
func (v *VoiceEngine) Process(_ context.Context, input audio.AudioFrame, prompt engine.PromptContext) (*engine.Response, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.ProcessCalls = append(v.ProcessCalls, ProcessCall{Input: input, Prompt: prompt})
	return v.ProcessResult, v.ProcessError
}

// InjectContext implements [engine.VoiceEngine].
func (v *VoiceEngine) InjectContext(_ context.Context, update engine.ContextUpdate) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.InjectContextCalls = append(v.InjectContextCalls, InjectContextCall{Update: update})
	return v.InjectContextError
}

// SetTools implements [engine.VoiceEngine].
func (v *VoiceEngine) SetTools(tools []llm.ToolDefinition) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.SetToolsCalls = append(v.SetToolsCalls, SetToolsCall{Tools: tools})
	return v.SetToolsError
}

// OnToolCall implements [engine.VoiceEngine]. Appends handler to ToolCallHandlers.
func (v *VoiceEngine) OnToolCall(handler func(name string, args string) (string, error)) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.CallCountOnToolCall++
	v.ToolCallHandlers = append(v.ToolCallHandlers, handler)
}

// Transcripts implements [engine.VoiceEngine]. Returns TranscriptsResult.
// If TranscriptsResult is nil, a pre-closed channel is returned.
func (v *VoiceEngine) Transcripts() <-chan memory.TranscriptEntry {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.TranscriptsResult != nil {
		return v.TranscriptsResult
	}
	ch := make(chan memory.TranscriptEntry)
	close(ch)
	return ch
}

// Close implements [engine.VoiceEngine]. Returns CloseError.
func (v *VoiceEngine) Close() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.CallCountClose++
	return v.CloseError
}

// InvokeToolCall calls all registered tool-call handlers with name and args,
// returning the result and error from the last registered handler.
// Use this in tests to simulate the LLM issuing a tool call.
func (v *VoiceEngine) InvokeToolCall(name, args string) (string, error) {
	v.mu.Lock()
	handlers := make([]func(string, string) (string, error), len(v.ToolCallHandlers))
	copy(handlers, v.ToolCallHandlers)
	v.mu.Unlock()

	var (
		result string
		err    error
	)
	for _, h := range handlers {
		result, err = h(name, args)
	}
	return result, err
}
