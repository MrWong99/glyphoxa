package config

import (
	"errors"
	"fmt"
	"sync"

	"github.com/MrWong99/glyphoxa/pkg/audio"
	"github.com/MrWong99/glyphoxa/pkg/provider/embeddings"
	"github.com/MrWong99/glyphoxa/pkg/provider/llm"
	"github.com/MrWong99/glyphoxa/pkg/provider/s2s"
	"github.com/MrWong99/glyphoxa/pkg/provider/stt"
	"github.com/MrWong99/glyphoxa/pkg/provider/tts"
	"github.com/MrWong99/glyphoxa/pkg/provider/vad"
)

// ErrProviderNotRegistered is returned by Create* methods when no factory has
// been registered under the requested provider name.
var ErrProviderNotRegistered = errors.New("config: provider not registered")

// Registry maps provider names to their constructor functions for each
// provider type. It is safe for concurrent use.
type Registry struct {
	mu         sync.RWMutex
	llm        map[string]func(ProviderEntry) (llm.Provider, error)
	stt        map[string]func(ProviderEntry) (stt.Provider, error)
	tts        map[string]func(ProviderEntry) (tts.Provider, error)
	s2s        map[string]func(ProviderEntry) (s2s.Provider, error)
	embeddings map[string]func(ProviderEntry) (embeddings.Provider, error)
	vad        map[string]func(ProviderEntry) (vad.Engine, error)
	audio      map[string]func(ProviderEntry) (audio.Platform, error)
}

// NewRegistry returns an empty, ready-to-use [Registry].
func NewRegistry() *Registry {
	return &Registry{
		llm:        make(map[string]func(ProviderEntry) (llm.Provider, error)),
		stt:        make(map[string]func(ProviderEntry) (stt.Provider, error)),
		tts:        make(map[string]func(ProviderEntry) (tts.Provider, error)),
		s2s:        make(map[string]func(ProviderEntry) (s2s.Provider, error)),
		embeddings: make(map[string]func(ProviderEntry) (embeddings.Provider, error)),
		vad:        make(map[string]func(ProviderEntry) (vad.Engine, error)),
		audio:      make(map[string]func(ProviderEntry) (audio.Platform, error)),
	}
}

// RegisterLLM registers an LLM provider factory under name.
// Subsequent calls with the same name overwrite the previous registration.
func (r *Registry) RegisterLLM(name string, factory func(ProviderEntry) (llm.Provider, error)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.llm[name] = factory
}

// RegisterSTT registers an STT provider factory under name.
func (r *Registry) RegisterSTT(name string, factory func(ProviderEntry) (stt.Provider, error)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stt[name] = factory
}

// RegisterTTS registers a TTS provider factory under name.
func (r *Registry) RegisterTTS(name string, factory func(ProviderEntry) (tts.Provider, error)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tts[name] = factory
}

// RegisterS2S registers an S2S provider factory under name.
func (r *Registry) RegisterS2S(name string, factory func(ProviderEntry) (s2s.Provider, error)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.s2s[name] = factory
}

// RegisterEmbeddings registers an embeddings provider factory under name.
func (r *Registry) RegisterEmbeddings(name string, factory func(ProviderEntry) (embeddings.Provider, error)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.embeddings[name] = factory
}

// RegisterVAD registers a VAD engine factory under name.
func (r *Registry) RegisterVAD(name string, factory func(ProviderEntry) (vad.Engine, error)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.vad[name] = factory
}

// RegisterAudio registers an audio platform factory under name.
func (r *Registry) RegisterAudio(name string, factory func(ProviderEntry) (audio.Platform, error)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.audio[name] = factory
}

// CreateLLM instantiates an LLM provider using the factory registered under entry.Name.
// Returns [ErrProviderNotRegistered] if no factory has been registered for that name.
func (r *Registry) CreateLLM(entry ProviderEntry) (llm.Provider, error) {
	r.mu.RLock()
	factory, ok := r.llm[entry.Name]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: llm/%q", ErrProviderNotRegistered, entry.Name)
	}
	return factory(entry)
}

// CreateSTT instantiates an STT provider using the factory registered under entry.Name.
func (r *Registry) CreateSTT(entry ProviderEntry) (stt.Provider, error) {
	r.mu.RLock()
	factory, ok := r.stt[entry.Name]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: stt/%q", ErrProviderNotRegistered, entry.Name)
	}
	return factory(entry)
}

// CreateTTS instantiates a TTS provider using the factory registered under entry.Name.
func (r *Registry) CreateTTS(entry ProviderEntry) (tts.Provider, error) {
	r.mu.RLock()
	factory, ok := r.tts[entry.Name]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: tts/%q", ErrProviderNotRegistered, entry.Name)
	}
	return factory(entry)
}

// CreateS2S instantiates an S2S provider using the factory registered under entry.Name.
func (r *Registry) CreateS2S(entry ProviderEntry) (s2s.Provider, error) {
	r.mu.RLock()
	factory, ok := r.s2s[entry.Name]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: s2s/%q", ErrProviderNotRegistered, entry.Name)
	}
	return factory(entry)
}

// CreateEmbeddings instantiates an embeddings provider using the factory registered under entry.Name.
func (r *Registry) CreateEmbeddings(entry ProviderEntry) (embeddings.Provider, error) {
	r.mu.RLock()
	factory, ok := r.embeddings[entry.Name]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: embeddings/%q", ErrProviderNotRegistered, entry.Name)
	}
	return factory(entry)
}

// CreateVAD instantiates a VAD engine using the factory registered under entry.Name.
func (r *Registry) CreateVAD(entry ProviderEntry) (vad.Engine, error) {
	r.mu.RLock()
	factory, ok := r.vad[entry.Name]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: vad/%q", ErrProviderNotRegistered, entry.Name)
	}
	return factory(entry)
}

// CreateAudio instantiates an audio platform using the factory registered under entry.Name.
func (r *Registry) CreateAudio(entry ProviderEntry) (audio.Platform, error) {
	r.mu.RLock()
	factory, ok := r.audio[entry.Name]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: audio/%q", ErrProviderNotRegistered, entry.Name)
	}
	return factory(entry)
}
