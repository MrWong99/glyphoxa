// Command glyphoxa is the main entry point for the Glyphoxa voice AI server.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/MrWong99/glyphoxa/internal/config"
)

func main() {
	os.Exit(run())
}

func run() int {
	// ── CLI flags ──────────────────────────────────────────────────────────────
	configPath := flag.String("config", "config.yaml", "path to the YAML configuration file")
	flag.Parse()

	// ── Load configuration ────────────────────────────────────────────────────
	cfg, err := config.Load(*configPath)
	if err != nil {
		// If the file simply doesn't exist (common during dev), show a helpful hint.
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "glyphoxa: config file %q not found — copy configs/example.yaml to get started\n", *configPath)
		} else {
			fmt.Fprintf(os.Stderr, "glyphoxa: %v\n", err)
		}
		return 1
	}

	// ── Logger ────────────────────────────────────────────────────────────────
	logger := newLogger(cfg.Server.LogLevel)
	slog.SetDefault(logger)

	slog.Info("glyphoxa starting",
		"config", *configPath,
		"listen_addr", cfg.Server.LogLevel,
		"log_level", cfg.Server.LogLevel,
	)

	// ── Provider registry ─────────────────────────────────────────────────────
	reg := config.NewRegistry()
	registerBuiltinProviders(reg)

	// ── Instantiate providers ─────────────────────────────────────────────────
	providers, err := buildProviders(cfg, reg)
	if err != nil {
		slog.Error("failed to build providers", "err", err)
		return 1
	}
	_ = providers // providers will be wired into the engine in a later phase

	// ── Startup summary ───────────────────────────────────────────────────────
	printStartupSummary(cfg)

	// ── Graceful shutdown ─────────────────────────────────────────────────────
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	slog.Info("server ready — press Ctrl+C to shut down")
	<-ctx.Done()

	slog.Info("shutdown signal received, stopping…")
	// Future: close engine, disconnect audio platform, etc.
	slog.Info("goodbye")
	return 0
}

// ── Provider wiring ───────────────────────────────────────────────────────────

// builtinProviders lists the provider names that ship with Glyphoxa.
// The actual factory functions will be filled in as implementations are added.
var builtinProviders = struct {
	llm        []string
	stt        []string
	tts        []string
	s2s        []string
	embeddings []string
	vad        []string
	audio      []string
}{
	llm:        []string{"openai", "anthropic", "ollama"},
	stt:        []string{"deepgram", "google", "whisper"},
	tts:        []string{"elevenlabs", "google", "piper"},
	s2s:        []string{"openai-realtime"},
	embeddings: []string{"openai", "cohere"},
	vad:        []string{"silero", "webrtc"},
	audio:      []string{"discord"},
}

// registerBuiltinProviders prints the registered names as a placeholder.
// Real factory functions will be added when provider packages are implemented.
func registerBuiltinProviders(reg *config.Registry) {
	for _, name := range builtinProviders.llm {
		n := name // capture for closure
		slog.Debug("registered LLM provider", "name", n)
		// reg.RegisterLLM(n, openai.NewProvider) — wired in Phase 2
	}
	for _, name := range builtinProviders.stt {
		slog.Debug("registered STT provider", "name", name)
	}
	for _, name := range builtinProviders.tts {
		slog.Debug("registered TTS provider", "name", name)
	}
	for _, name := range builtinProviders.s2s {
		slog.Debug("registered S2S provider", "name", name)
	}
	for _, name := range builtinProviders.embeddings {
		slog.Debug("registered Embeddings provider", "name", name)
	}
	for _, name := range builtinProviders.vad {
		slog.Debug("registered VAD provider", "name", name)
	}
	for _, name := range builtinProviders.audio {
		slog.Debug("registered Audio provider", "name", name)
	}
}

// providerSet holds the instantiated providers for this run.
// Unexported fields will be populated as factory implementations land.
type providerSet struct {
	// All fields are interface values; nil means the provider is not configured.
	// Concrete types come in Phase 2.
}

// buildProviders instantiates all providers named in cfg using the registry.
// For providers whose factory is not yet registered, a debug log is emitted
// and the slot is left nil (graceful stub behaviour for Phase 1).
func buildProviders(cfg *config.Config, reg *config.Registry) (*providerSet, error) {
	ps := &providerSet{}

	tryCreate := func(kind, name string, create func() error) {
		if name == "" {
			return
		}
		if err := create(); err != nil {
			if errors.Is(err, config.ErrProviderNotRegistered) {
				slog.Debug("provider not yet implemented — skipping", "kind", kind, "name", name)
			} else {
				slog.Warn("failed to create provider", "kind", kind, "name", name, "err", err)
			}
		} else {
			slog.Info("provider created", "kind", kind, "name", name)
		}
	}

	tryCreate("llm", cfg.Providers.LLM.Name, func() error {
		_, err := reg.CreateLLM(cfg.Providers.LLM)
		return err
	})
	tryCreate("stt", cfg.Providers.STT.Name, func() error {
		_, err := reg.CreateSTT(cfg.Providers.STT)
		return err
	})
	tryCreate("tts", cfg.Providers.TTS.Name, func() error {
		_, err := reg.CreateTTS(cfg.Providers.TTS)
		return err
	})
	tryCreate("s2s", cfg.Providers.S2S.Name, func() error {
		_, err := reg.CreateS2S(cfg.Providers.S2S)
		return err
	})
	tryCreate("embeddings", cfg.Providers.Embeddings.Name, func() error {
		_, err := reg.CreateEmbeddings(cfg.Providers.Embeddings)
		return err
	})
	tryCreate("vad", cfg.Providers.VAD.Name, func() error {
		_, err := reg.CreateVAD(cfg.Providers.VAD)
		return err
	})
	tryCreate("audio", cfg.Providers.Audio.Name, func() error {
		_, err := reg.CreateAudio(cfg.Providers.Audio)
		return err
	})

	return ps, nil
}

// ── Startup summary ───────────────────────────────────────────────────────────

func printStartupSummary(cfg *config.Config) {
	fmt.Println("╔═══════════════════════════════════════╗")
	fmt.Println("║         Glyphoxa — startup summary    ║")
	fmt.Println("╠═══════════════════════════════════════╣")
	printProvider("LLM", cfg.Providers.LLM.Name, cfg.Providers.LLM.Model)
	printProvider("STT", cfg.Providers.STT.Name, cfg.Providers.STT.Model)
	printProvider("TTS", cfg.Providers.TTS.Name, cfg.Providers.TTS.Model)
	printProvider("S2S", cfg.Providers.S2S.Name, cfg.Providers.S2S.Model)
	printProvider("Embeddings", cfg.Providers.Embeddings.Name, cfg.Providers.Embeddings.Model)
	printProvider("VAD", cfg.Providers.VAD.Name, "")
	printProvider("Audio", cfg.Providers.Audio.Name, "")
	fmt.Printf("║  NPCs configured : %-19d ║\n", len(cfg.NPCs))
	fmt.Printf("║  MCP servers     : %-19d ║\n", len(cfg.MCP.Servers))
	if cfg.Server.ListenAddr != "" {
		fmt.Printf("║  Listen addr     : %-19s ║\n", cfg.Server.ListenAddr)
	}
	fmt.Println("╚═══════════════════════════════════════╝")
}

func printProvider(kind, name, model string) {
	value := name
	if value == "" {
		value = "(not configured)"
	} else if model != "" {
		value = name + " / " + model
	}
	// Truncate to fit the box (19 chars)
	if len(value) > 19 {
		value = value[:16] + "…"
	}
	fmt.Printf("║  %-12s    : %-19s ║\n", kind, value)
}

// ── Logger ─────────────────────────────────────────────────────────────────────

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
}
