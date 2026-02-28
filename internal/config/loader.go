package config

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"

	"github.com/MrWong99/glyphoxa/internal/mcp"
	"gopkg.in/yaml.v3"
)

// ValidProviderNames lists known provider names per provider kind.
// Used by [Validate] to warn about unrecognised provider names.
var ValidProviderNames = map[string][]string{
	"llm":        {"openai", "anthropic", "ollama", "gemini", "deepseek", "mistral", "groq", "llamacpp", "llamafile"},
	"stt":        {"deepgram", "whisper", "whisper-native"},
	"tts":        {"elevenlabs", "coqui"},
	"s2s":        {"openai-realtime", "gemini-live"},
	"embeddings": {"openai", "ollama"},
	"vad":        {"silero"},
	"audio":      {"discord"},
}

// Load reads the YAML configuration file at path and returns a validated [Config].
// It is a convenience wrapper around [LoadFromReader] and [Validate].
func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("config: open %q: %w", path, err)
	}
	defer f.Close()

	cfg, err := LoadFromReader(f)
	if err != nil {
		return nil, fmt.Errorf("config: parse %q: %w", path, err)
	}
	return cfg, nil
}

// LoadFromReader decodes a YAML config from r and validates the result.
// Useful in tests where configs are constructed from string literals.
func LoadFromReader(r io.Reader) (*Config, error) {
	cfg := &Config{}
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		return nil, fmt.Errorf("config: decode yaml: %w", err)
	}
	if err := Validate(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Validate checks that cfg contains a coherent set of values.
// It returns a joined error listing all validation failures found.
func Validate(cfg *Config) error {
	var errs []error

	// Server
	if cfg.Server.LogLevel != "" && !cfg.Server.LogLevel.IsValid() {
		errs = append(errs, fmt.Errorf("server.log_level %q is invalid; valid values: debug, info, warn, error", cfg.Server.LogLevel))
	}

	// Provider name validation — warn for unknown provider names.
	validateProviderName("llm", cfg.Providers.LLM.Name)
	validateProviderName("stt", cfg.Providers.STT.Name)
	validateProviderName("tts", cfg.Providers.TTS.Name)
	validateProviderName("s2s", cfg.Providers.S2S.Name)
	validateProviderName("embeddings", cfg.Providers.Embeddings.Name)
	validateProviderName("vad", cfg.Providers.VAD.Name)
	validateProviderName("audio", cfg.Providers.Audio.Name)

	// Provider availability warnings
	if cfg.Providers.LLM.Name == "" && cfg.Providers.S2S.Name == "" {
		if len(cfg.NPCs) > 0 {
			slog.Warn("no LLM or S2S provider configured; NPCs will not be able to generate responses")
		}
	}

	// Embeddings ↔ memory dimensions
	if cfg.Providers.Embeddings.Name != "" && cfg.Memory.EmbeddingDimensions <= 0 {
		slog.Warn("providers.embeddings is configured but memory.embedding_dimensions is not set; defaulting to 1536")
	}

	// Memory availability
	if cfg.Memory.PostgresDSN == "" && len(cfg.NPCs) > 0 {
		slog.Warn("memory.postgres_dsn is empty; long-term memory will not be available for NPCs")
	}

	// NPC duplicate name detection
	npcNamesSeen := make(map[string]int, len(cfg.NPCs))

	// NPCs
	for i, npc := range cfg.NPCs {
		prefix := fmt.Sprintf("npcs[%d]", i)
		if npc.Name == "" {
			errs = append(errs, fmt.Errorf("%s.name is required", prefix))
		} else {
			if prev, ok := npcNamesSeen[npc.Name]; ok {
				errs = append(errs, fmt.Errorf("%s.name %q is a duplicate of npcs[%d]", prefix, npc.Name, prev))
			}
			npcNamesSeen[npc.Name] = i
		}
		if npc.Engine != "" && !npc.Engine.IsValid() {
			errs = append(errs, fmt.Errorf("%s.engine %q is invalid; valid values: cascaded, s2s", prefix, npc.Engine))
		}
		if npc.BudgetTier != "" && !npc.BudgetTier.IsValid() {
			errs = append(errs, fmt.Errorf("%s.budget_tier %q is invalid; valid values: fast, standard, deep", prefix, npc.BudgetTier))
		}
		if npc.Voice.SpeedFactor != 0 {
			if npc.Voice.SpeedFactor < 0.5 || npc.Voice.SpeedFactor > 2.0 {
				errs = append(errs, fmt.Errorf("%s.voice.speed_factor %.2f is out of range [0.5, 2.0]", prefix, npc.Voice.SpeedFactor))
			}
		}
		if npc.Voice.PitchShift < -10 || npc.Voice.PitchShift > 10 {
			errs = append(errs, fmt.Errorf("%s.voice.pitch_shift %.2f is out of range [-10, 10]", prefix, npc.Voice.PitchShift))
		}

		// Engine ↔ provider cross-validation
		engine := npc.Engine
		if engine == EngineCascaded || engine == EngineSentenceCascade {
			if cfg.Providers.LLM.Name == "" {
				errs = append(errs, fmt.Errorf("%s: engine %q requires an LLM provider but providers.llm is not configured", prefix, engine))
			}
			if cfg.Providers.TTS.Name == "" {
				errs = append(errs, fmt.Errorf("%s: engine %q requires a TTS provider but providers.tts is not configured", prefix, engine))
			}
		}
		if engine == EngineS2S {
			if cfg.Providers.S2S.Name == "" {
				errs = append(errs, fmt.Errorf("%s: engine %q requires an S2S provider but providers.s2s is not configured", prefix, engine))
			}
		}

		// Voice provider ↔ TTS provider cross-validation
		if npc.Voice.Provider != "" && cfg.Providers.TTS.Name != "" && npc.Voice.Provider != cfg.Providers.TTS.Name {
			slog.Warn("NPC voice provider does not match configured TTS provider",
				"npc", npc.Name,
				"voice_provider", npc.Voice.Provider,
				"tts_provider", cfg.Providers.TTS.Name,
			)
		}
	}

	// MCP servers
	for i, srv := range cfg.MCP.Servers {
		prefix := fmt.Sprintf("mcp.servers[%d]", i)
		if srv.Name == "" {
			errs = append(errs, fmt.Errorf("%s.name is required", prefix))
		}
		if srv.Transport != "" && !srv.Transport.IsValid() {
			errs = append(errs, fmt.Errorf("%s.transport %q is invalid; valid values: stdio, streamable-http", prefix, srv.Transport))
		}
		if srv.Transport == mcp.TransportStdio && srv.Command == "" {
			errs = append(errs, fmt.Errorf("%s.command is required when transport is stdio", prefix))
		}
		if srv.Transport == mcp.TransportStreamableHTTP && srv.URL == "" {
			errs = append(errs, fmt.Errorf("%s.url is required when transport is streamable-http", prefix))
		}
	}

	return errors.Join(errs...)
}

// validateProviderName logs a warning if name is non-empty and not found in
// the [ValidProviderNames] list for the given kind.
func validateProviderName(kind, name string) {
	if name == "" {
		return
	}
	known, ok := ValidProviderNames[kind]
	if !ok {
		return
	}
	if slices.Contains(known, name) {
		return
	}
	slog.Warn("unknown provider name — may be a typo or third-party provider",
		"kind", kind,
		"name", name,
		"known", known,
	)
}
