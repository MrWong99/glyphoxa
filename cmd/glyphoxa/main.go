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
	"time"

	"github.com/MrWong99/glyphoxa/internal/app"
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
		"listen_addr", cfg.Server.ListenAddr,
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

	// ── Startup summary ───────────────────────────────────────────────────────
	printStartupSummary(cfg)

	// ── Application wiring ────────────────────────────────────────────────────
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	application, err := app.New(ctx, cfg, providers)
	if err != nil {
		slog.Error("failed to initialise application", "err", err)
		return 1
	}

	slog.Info("server ready — press Ctrl+C to shut down")

	if err := application.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("run error", "err", err)
		return 1
	}

	// ── Graceful shutdown ─────────────────────────────────────────────────────
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	slog.Info("shutdown signal received, stopping…")
	if err := application.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown error", "err", err)
		return 1
	}
	slog.Info("goodbye")
	return 0
}

// ── Provider wiring ───────────────────────────────────────────────────────────

// builtinProviders maps provider category names to the implementations that
// ship with Glyphoxa. Used for startup logging.
var builtinProviders = map[string][]string{
	"llm":        {"openai", "anthropic", "ollama"},
	"stt":        {"deepgram", "google", "whisper"},
	"tts":        {"elevenlabs", "google", "piper"},
	"s2s":        {"openai-realtime"},
	"embeddings": {"openai", "cohere"},
	"vad":        {"silero", "webrtc"},
	"audio":      {"discord"},
}

// registerBuiltinProviders prints the registered names as a placeholder.
// Real factory functions will be added when provider packages are implemented.
func registerBuiltinProviders(reg *config.Registry) {
	for kind, names := range builtinProviders {
		for _, name := range names {
			slog.Debug("registered provider", "kind", kind, "name", name)
		}
	}
	_ = reg // wired when real provider factories land
}

// buildProviders instantiates all providers named in cfg using the registry
// and returns them in an [app.Providers] struct for the application to consume.
func buildProviders(cfg *config.Config, reg *config.Registry) (*app.Providers, error) {
	ps := &app.Providers{}

	if name := cfg.Providers.LLM.Name; name != "" {
		p, err := reg.CreateLLM(cfg.Providers.LLM)
		if errors.Is(err, config.ErrProviderNotRegistered) {
			slog.Debug("provider not yet implemented — skipping", "kind", "llm", "name", name)
		} else if err != nil {
			return nil, fmt.Errorf("create llm provider %q: %w", name, err)
		} else {
			ps.LLM = p
			slog.Info("provider created", "kind", "llm", "name", name)
		}
	}

	if name := cfg.Providers.STT.Name; name != "" {
		p, err := reg.CreateSTT(cfg.Providers.STT)
		if errors.Is(err, config.ErrProviderNotRegistered) {
			slog.Debug("provider not yet implemented — skipping", "kind", "stt", "name", name)
		} else if err != nil {
			return nil, fmt.Errorf("create stt provider %q: %w", name, err)
		} else {
			ps.STT = p
			slog.Info("provider created", "kind", "stt", "name", name)
		}
	}

	if name := cfg.Providers.TTS.Name; name != "" {
		p, err := reg.CreateTTS(cfg.Providers.TTS)
		if errors.Is(err, config.ErrProviderNotRegistered) {
			slog.Debug("provider not yet implemented — skipping", "kind", "tts", "name", name)
		} else if err != nil {
			return nil, fmt.Errorf("create tts provider %q: %w", name, err)
		} else {
			ps.TTS = p
			slog.Info("provider created", "kind", "tts", "name", name)
		}
	}

	if name := cfg.Providers.S2S.Name; name != "" {
		p, err := reg.CreateS2S(cfg.Providers.S2S)
		if errors.Is(err, config.ErrProviderNotRegistered) {
			slog.Debug("provider not yet implemented — skipping", "kind", "s2s", "name", name)
		} else if err != nil {
			return nil, fmt.Errorf("create s2s provider %q: %w", name, err)
		} else {
			ps.S2S = p
			slog.Info("provider created", "kind", "s2s", "name", name)
		}
	}

	if name := cfg.Providers.Embeddings.Name; name != "" {
		p, err := reg.CreateEmbeddings(cfg.Providers.Embeddings)
		if errors.Is(err, config.ErrProviderNotRegistered) {
			slog.Debug("provider not yet implemented — skipping", "kind", "embeddings", "name", name)
		} else if err != nil {
			return nil, fmt.Errorf("create embeddings provider %q: %w", name, err)
		} else {
			ps.Embeddings = p
			slog.Info("provider created", "kind", "embeddings", "name", name)
		}
	}

	if name := cfg.Providers.VAD.Name; name != "" {
		p, err := reg.CreateVAD(cfg.Providers.VAD)
		if errors.Is(err, config.ErrProviderNotRegistered) {
			slog.Debug("provider not yet implemented — skipping", "kind", "vad", "name", name)
		} else if err != nil {
			return nil, fmt.Errorf("create vad provider %q: %w", name, err)
		} else {
			ps.VAD = p
			slog.Info("provider created", "kind", "vad", "name", name)
		}
	}

	if name := cfg.Providers.Audio.Name; name != "" {
		p, err := reg.CreateAudio(cfg.Providers.Audio)
		if errors.Is(err, config.ErrProviderNotRegistered) {
			slog.Debug("provider not yet implemented — skipping", "kind", "audio", "name", name)
		} else if err != nil {
			return nil, fmt.Errorf("create audio provider %q: %w", name, err)
		} else {
			ps.Audio = p
			slog.Info("provider created", "kind", "audio", "name", name)
		}
	}

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
	if len(value) > 19 {
		value = value[:16] + "…"
	}
	fmt.Printf("║  %-12s    : %-19s ║\n", kind, value)
}

// ── Logger ─────────────────────────────────────────────────────────────────────

func newLogger(level config.LogLevel) *slog.Logger {
	var lvl slog.Level
	switch level {
	case config.LogDebug:
		lvl = slog.LevelDebug
	case config.LogWarn:
		lvl = slog.LevelWarn
	case config.LogError:
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
}
