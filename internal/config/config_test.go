package config_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MrWong99/glyphoxa/internal/config"
	"github.com/MrWong99/glyphoxa/pkg/audio"
	"github.com/MrWong99/glyphoxa/pkg/provider/embeddings"
	"github.com/MrWong99/glyphoxa/pkg/provider/llm"
	"github.com/MrWong99/glyphoxa/pkg/provider/s2s"
	"github.com/MrWong99/glyphoxa/pkg/provider/stt"
	"github.com/MrWong99/glyphoxa/pkg/provider/tts"
	"github.com/MrWong99/glyphoxa/pkg/provider/vad"
)

// ── helpers ──────────────────────────────────────────────────────────────────

const sampleYAML = `
server:
  listen_addr: ":8080"
  log_level: info

providers:
  llm:
    name: openai
    api_key: sk-test
    model: gpt-4o
  stt:
    name: deepgram
    api_key: dg-test
  tts:
    name: elevenlabs
    api_key: el-test
  s2s:
    name: openai-realtime
    api_key: sk-test
  embeddings:
    name: openai
    api_key: sk-test
    model: text-embedding-3-small
  vad:
    name: silero
  audio:
    name: discord

npcs:
  - name: Greymantle the Sage
    personality: An ancient wizard who speaks in riddles.
    engine: cascaded
    budget_tier: standard
    voice:
      provider: elevenlabs
      voice_id: sage-v1
      pitch_shift: 0
      speed_factor: 0.9
    knowledge_scope:
      - magic
      - history
    tools:
      - lookup_spell

memory:
  postgres_dsn: postgres://user:pass@localhost:5432/glyphoxa?sslmode=disable
  embedding_dimensions: 1536

mcp:
  servers:
    - name: tools
      transport: stdio
      command: /usr/local/bin/mcp-tools
    - name: web
      transport: http
      url: https://tools.example.com/mcp
`

// ── YAML loading ──────────────────────────────────────────────────────────────

func TestLoadFromReader_Valid(t *testing.T) {
	cfg, err := config.LoadFromReader(strings.NewReader(sampleYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Server.ListenAddr != ":8080" {
		t.Errorf("server.listen_addr: got %q, want %q", cfg.Server.ListenAddr, ":8080")
	}
	if cfg.Server.LogLevel != config.LogInfo {
		t.Errorf("server.log_level: got %q, want %q", cfg.Server.LogLevel, config.LogInfo)
	}
	if cfg.Providers.LLM.Name != "openai" {
		t.Errorf("providers.llm.name: got %q, want %q", cfg.Providers.LLM.Name, "openai")
	}
	if len(cfg.NPCs) != 1 {
		t.Fatalf("npcs: got %d, want 1", len(cfg.NPCs))
	}
	if cfg.NPCs[0].Name != "Greymantle the Sage" {
		t.Errorf("npcs[0].name: got %q", cfg.NPCs[0].Name)
	}
	if cfg.NPCs[0].Voice.SpeedFactor != 0.9 {
		t.Errorf("npcs[0].voice.speed_factor: got %.2f, want 0.9", cfg.NPCs[0].Voice.SpeedFactor)
	}
	if cfg.Memory.EmbeddingDimensions != 1536 {
		t.Errorf("memory.embedding_dimensions: got %d, want 1536", cfg.Memory.EmbeddingDimensions)
	}
	if len(cfg.MCP.Servers) != 2 {
		t.Fatalf("mcp.servers: got %d, want 2", len(cfg.MCP.Servers))
	}
}

func TestLoadFromReader_EmptyIsValid(t *testing.T) {
	// An empty config should succeed (no required top-level fields).
	_, err := config.LoadFromReader(strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("unexpected error for empty config: %v", err)
	}
}

// ── Validation ────────────────────────────────────────────────────────────────

func TestValidate_InvalidLogLevel(t *testing.T) {
	yaml := `
server:
  log_level: verbose
`
	_, err := config.LoadFromReader(strings.NewReader(yaml))
	if err == nil {
		t.Fatal("expected error for invalid log_level, got nil")
	}
	if !strings.Contains(err.Error(), "log_level") {
		t.Errorf("error should mention log_level, got: %v", err)
	}
}

func TestValidate_MissingNPCName(t *testing.T) {
	yaml := `
npcs:
  - personality: "No name NPC"
    engine: cascaded
`
	_, err := config.LoadFromReader(strings.NewReader(yaml))
	if err == nil {
		t.Fatal("expected error for missing npc name, got nil")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("error should mention name, got: %v", err)
	}
}

func TestValidate_InvalidEngine(t *testing.T) {
	yaml := `
npcs:
  - name: TestNPC
    engine: turbo
`
	_, err := config.LoadFromReader(strings.NewReader(yaml))
	if err == nil {
		t.Fatal("expected error for invalid engine, got nil")
	}
	if !strings.Contains(err.Error(), "engine") {
		t.Errorf("error should mention engine, got: %v", err)
	}
}

func TestValidate_InvalidBudgetTier(t *testing.T) {
	yaml := `
npcs:
  - name: TestNPC
    budget_tier: platinum
`
	_, err := config.LoadFromReader(strings.NewReader(yaml))
	if err == nil {
		t.Fatal("expected error for invalid budget_tier, got nil")
	}
}

func TestValidate_InvalidSpeedFactor(t *testing.T) {
	yaml := `
npcs:
  - name: TestNPC
    voice:
      speed_factor: 5.0
`
	_, err := config.LoadFromReader(strings.NewReader(yaml))
	if err == nil {
		t.Fatal("expected error for invalid speed_factor, got nil")
	}
}

func TestValidate_MCPMissingCommand(t *testing.T) {
	yaml := `
mcp:
  servers:
    - name: badserver
      transport: stdio
`
	_, err := config.LoadFromReader(strings.NewReader(yaml))
	if err == nil {
		t.Fatal("expected error for missing stdio command, got nil")
	}
}

func TestValidate_MCPMissingURL(t *testing.T) {
	yaml := `
mcp:
  servers:
    - name: webserver
      transport: http
`
	_, err := config.LoadFromReader(strings.NewReader(yaml))
	if err == nil {
		t.Fatal("expected error for missing http url, got nil")
	}
}

func TestValidate_MCPInvalidTransport(t *testing.T) {
	yaml := `
mcp:
  servers:
    - name: badtransport
      transport: grpc
      command: /bin/server
`
	_, err := config.LoadFromReader(strings.NewReader(yaml))
	if err == nil {
		t.Fatal("expected error for invalid transport, got nil")
	}
}

// ── Registry ─────────────────────────────────────────────────────────────────

func TestRegistry_UnknownLLM(t *testing.T) {
	reg := config.NewRegistry()
	_, err := reg.CreateLLM(config.ProviderEntry{Name: "nonexistent"})
	if err == nil {
		t.Fatal("expected error for unknown LLM provider")
	}
	if !errors.Is(err, config.ErrProviderNotRegistered) {
		t.Errorf("expected ErrProviderNotRegistered, got: %v", err)
	}
}

func TestRegistry_UnknownSTT(t *testing.T) {
	reg := config.NewRegistry()
	_, err := reg.CreateSTT(config.ProviderEntry{Name: "nonexistent"})
	if !errors.Is(err, config.ErrProviderNotRegistered) {
		t.Errorf("expected ErrProviderNotRegistered, got: %v", err)
	}
}

func TestRegistry_UnknownTTS(t *testing.T) {
	reg := config.NewRegistry()
	_, err := reg.CreateTTS(config.ProviderEntry{Name: "nonexistent"})
	if !errors.Is(err, config.ErrProviderNotRegistered) {
		t.Errorf("expected ErrProviderNotRegistered, got: %v", err)
	}
}

func TestRegistry_UnknownS2S(t *testing.T) {
	reg := config.NewRegistry()
	_, err := reg.CreateS2S(config.ProviderEntry{Name: "nonexistent"})
	if !errors.Is(err, config.ErrProviderNotRegistered) {
		t.Errorf("expected ErrProviderNotRegistered, got: %v", err)
	}
}

func TestRegistry_UnknownEmbeddings(t *testing.T) {
	reg := config.NewRegistry()
	_, err := reg.CreateEmbeddings(config.ProviderEntry{Name: "nonexistent"})
	if !errors.Is(err, config.ErrProviderNotRegistered) {
		t.Errorf("expected ErrProviderNotRegistered, got: %v", err)
	}
}

func TestRegistry_UnknownVAD(t *testing.T) {
	reg := config.NewRegistry()
	_, err := reg.CreateVAD(config.ProviderEntry{Name: "nonexistent"})
	if !errors.Is(err, config.ErrProviderNotRegistered) {
		t.Errorf("expected ErrProviderNotRegistered, got: %v", err)
	}
}

func TestRegistry_UnknownAudio(t *testing.T) {
	reg := config.NewRegistry()
	_, err := reg.CreateAudio(config.ProviderEntry{Name: "nonexistent"})
	if !errors.Is(err, config.ErrProviderNotRegistered) {
		t.Errorf("expected ErrProviderNotRegistered, got: %v", err)
	}
}

// ── Registry with registered factories ───────────────────────────────────────

func TestRegistry_RegisteredLLM(t *testing.T) {
	reg := config.NewRegistry()
	want := &stubLLM{}
	reg.RegisterLLM("stub", func(e config.ProviderEntry) (llm.Provider, error) {
		return want, nil
	})
	got, err := reg.CreateLLM(config.ProviderEntry{Name: "stub"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Error("returned provider is not the expected instance")
	}
}

func TestRegistry_RegisteredSTT(t *testing.T) {
	reg := config.NewRegistry()
	want := &stubSTT{}
	reg.RegisterSTT("stub", func(e config.ProviderEntry) (stt.Provider, error) {
		return want, nil
	})
	got, err := reg.CreateSTT(config.ProviderEntry{Name: "stub"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Error("returned provider is not the expected instance")
	}
}

func TestRegistry_RegisteredTTS(t *testing.T) {
	reg := config.NewRegistry()
	want := &stubTTS{}
	reg.RegisterTTS("stub", func(e config.ProviderEntry) (tts.Provider, error) {
		return want, nil
	})
	got, err := reg.CreateTTS(config.ProviderEntry{Name: "stub"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Error("returned provider is not the expected instance")
	}
}

func TestRegistry_RegisteredEmbeddings(t *testing.T) {
	reg := config.NewRegistry()
	want := &stubEmbeddings{}
	reg.RegisterEmbeddings("stub", func(e config.ProviderEntry) (embeddings.Provider, error) {
		return want, nil
	})
	got, err := reg.CreateEmbeddings(config.ProviderEntry{Name: "stub"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Error("returned provider is not the expected instance")
	}
}

func TestRegistry_FactoryError(t *testing.T) {
	reg := config.NewRegistry()
	wantErr := errors.New("factory boom")
	reg.RegisterLLM("broken", func(e config.ProviderEntry) (llm.Provider, error) {
		return nil, wantErr
	})
	_, err := reg.CreateLLM(config.ProviderEntry{Name: "broken"})
	if !errors.Is(err, wantErr) {
		t.Errorf("expected factory error %v, got %v", wantErr, err)
	}
}

// ── Stub implementations (satisfy interfaces for the compiler) ────────────────

// stubLLM implements llm.Provider with no-op methods.
type stubLLM struct{}

func (s *stubLLM) StreamCompletion(_ context.Context, _ llm.CompletionRequest) (<-chan llm.Chunk, error) {
	ch := make(chan llm.Chunk)
	close(ch)
	return ch, nil
}
func (s *stubLLM) Complete(_ context.Context, _ llm.CompletionRequest) (*llm.CompletionResponse, error) {
	return &llm.CompletionResponse{}, nil
}
func (s *stubLLM) CountTokens(_ []llm.Message) (int, error) { return 0, nil }
func (s *stubLLM) Capabilities() llm.ModelCapabilities      { return llm.ModelCapabilities{} }

// stubSTT implements stt.Provider.
type stubSTT struct{}

func (s *stubSTT) StartStream(_ context.Context, _ stt.StreamConfig) (stt.SessionHandle, error) {
	return nil, nil
}

// stubTTS implements tts.Provider.
type stubTTS struct{}

func (s *stubTTS) SynthesizeStream(_ context.Context, _ <-chan string, _ tts.VoiceProfile) (<-chan []byte, error) {
	ch := make(chan []byte)
	close(ch)
	return ch, nil
}
func (s *stubTTS) ListVoices(_ context.Context) ([]tts.VoiceProfile, error) { return nil, nil }
func (s *stubTTS) CloneVoice(_ context.Context, _ [][]byte) (*tts.VoiceProfile, error) {
	return nil, nil
}

// stubEmbeddings implements embeddings.Provider.
type stubEmbeddings struct{}

func (s *stubEmbeddings) Embed(_ context.Context, _ string) ([]float32, error) { return nil, nil }
func (s *stubEmbeddings) EmbedBatch(_ context.Context, _ []string) ([][]float32, error) {
	return nil, nil
}
func (s *stubEmbeddings) Dimensions() int  { return 0 }
func (s *stubEmbeddings) ModelID() string  { return "stub" }

// stubS2S implements s2s.Provider.
type stubS2S struct{}

func (s *stubS2S) Connect(_ context.Context, _ s2s.SessionConfig) (s2s.SessionHandle, error) {
	return nil, nil
}
func (s *stubS2S) Capabilities() s2s.S2SCapabilities { return s2s.S2SCapabilities{} }

// stubVAD implements vad.Engine.
type stubVAD struct{}

func (s *stubVAD) NewSession(_ vad.Config) (vad.SessionHandle, error) { return nil, nil }

// stubAudio implements audio.Platform.
type stubAudio struct{}

func (s *stubAudio) Connect(_ context.Context, _ string) (audio.Connection, error) {
	return nil, nil
}
