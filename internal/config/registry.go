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

// providerMap is a typed, concurrency-safe map from provider names to their
// constructor functions.
type providerMap[T any] struct {
	mu        sync.RWMutex
	factories map[string]func(ProviderEntry) (T, error)
}

func newProviderMap[T any]() providerMap[T] {
	return providerMap[T]{factories: make(map[string]func(ProviderEntry) (T, error))}
}

func (m *providerMap[T]) register(name string, factory func(ProviderEntry) (T, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.factories[name] = factory
}

func (m *providerMap[T]) create(kind string, entry ProviderEntry) (T, error) {
	m.mu.RLock()
	factory, ok := m.factories[entry.Name]
	m.mu.RUnlock()
	if !ok {
		var zero T
		return zero, fmt.Errorf("%w: %s/%q", ErrProviderNotRegistered, kind, entry.Name)
	}
	return factory(entry)
}

// Registry maps provider names to their constructor functions for each
// provider type. It is safe for concurrent use.
type Registry struct {
	llm        providerMap[llm.Provider]
	stt        providerMap[stt.Provider]
	tts        providerMap[tts.Provider]
	s2s        providerMap[s2s.Provider]
	embeddings providerMap[embeddings.Provider]
	vad        providerMap[vad.Engine]
	audio      providerMap[audio.Platform]
}

// NewRegistry returns an empty, ready-to-use [Registry].
func NewRegistry() *Registry {
	return &Registry{
		llm:        newProviderMap[llm.Provider](),
		stt:        newProviderMap[stt.Provider](),
		tts:        newProviderMap[tts.Provider](),
		s2s:        newProviderMap[s2s.Provider](),
		embeddings: newProviderMap[embeddings.Provider](),
		vad:        newProviderMap[vad.Engine](),
		audio:      newProviderMap[audio.Platform](),
	}
}

// RegisterLLM registers an LLM provider factory under name.
// Subsequent calls with the same name overwrite the previous registration.
func (r *Registry) RegisterLLM(name string, factory func(ProviderEntry) (llm.Provider, error)) {
	r.llm.register(name, factory)
}

// RegisterSTT registers an STT provider factory under name.
func (r *Registry) RegisterSTT(name string, factory func(ProviderEntry) (stt.Provider, error)) {
	r.stt.register(name, factory)
}

// RegisterTTS registers a TTS provider factory under name.
func (r *Registry) RegisterTTS(name string, factory func(ProviderEntry) (tts.Provider, error)) {
	r.tts.register(name, factory)
}

// RegisterS2S registers an S2S provider factory under name.
func (r *Registry) RegisterS2S(name string, factory func(ProviderEntry) (s2s.Provider, error)) {
	r.s2s.register(name, factory)
}

// RegisterEmbeddings registers an embeddings provider factory under name.
func (r *Registry) RegisterEmbeddings(name string, factory func(ProviderEntry) (embeddings.Provider, error)) {
	r.embeddings.register(name, factory)
}

// RegisterVAD registers a VAD engine factory under name.
func (r *Registry) RegisterVAD(name string, factory func(ProviderEntry) (vad.Engine, error)) {
	r.vad.register(name, factory)
}

// RegisterAudio registers an audio platform factory under name.
func (r *Registry) RegisterAudio(name string, factory func(ProviderEntry) (audio.Platform, error)) {
	r.audio.register(name, factory)
}

// CreateLLM instantiates an LLM provider using the factory registered under entry.Name.
// Returns [ErrProviderNotRegistered] if no factory has been registered for that name.
func (r *Registry) CreateLLM(entry ProviderEntry) (llm.Provider, error) {
	return r.llm.create("llm", entry)
}

// CreateSTT instantiates an STT provider using the factory registered under entry.Name.
func (r *Registry) CreateSTT(entry ProviderEntry) (stt.Provider, error) {
	return r.stt.create("stt", entry)
}

// CreateTTS instantiates a TTS provider using the factory registered under entry.Name.
func (r *Registry) CreateTTS(entry ProviderEntry) (tts.Provider, error) {
	return r.tts.create("tts", entry)
}

// CreateS2S instantiates an S2S provider using the factory registered under entry.Name.
func (r *Registry) CreateS2S(entry ProviderEntry) (s2s.Provider, error) {
	return r.s2s.create("s2s", entry)
}

// CreateEmbeddings instantiates an embeddings provider using the factory registered under entry.Name.
func (r *Registry) CreateEmbeddings(entry ProviderEntry) (embeddings.Provider, error) {
	return r.embeddings.create("embeddings", entry)
}

// CreateVAD instantiates a VAD engine using the factory registered under entry.Name.
func (r *Registry) CreateVAD(entry ProviderEntry) (vad.Engine, error) {
	return r.vad.create("vad", entry)
}

// CreateAudio instantiates an audio platform using the factory registered under entry.Name.
func (r *Registry) CreateAudio(entry ProviderEntry) (audio.Platform, error) {
	return r.audio.create("audio", entry)
}
