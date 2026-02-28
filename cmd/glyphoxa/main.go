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

	anyllmlib "github.com/mozilla-ai/any-llm-go"

	"github.com/MrWong99/glyphoxa/internal/app"
	"github.com/MrWong99/glyphoxa/internal/config"
	discordbot "github.com/MrWong99/glyphoxa/internal/discord"
	"github.com/MrWong99/glyphoxa/pkg/provider/embeddings"
	ollamaembed "github.com/MrWong99/glyphoxa/pkg/provider/embeddings/ollama"
	oaembed "github.com/MrWong99/glyphoxa/pkg/provider/embeddings/openai"
	"github.com/MrWong99/glyphoxa/pkg/provider/llm"
	"github.com/MrWong99/glyphoxa/pkg/provider/llm/anyllm"
	"github.com/MrWong99/glyphoxa/pkg/provider/s2s"
	geminilive "github.com/MrWong99/glyphoxa/pkg/provider/s2s/gemini"
	oais2s "github.com/MrWong99/glyphoxa/pkg/provider/s2s/openai"
	"github.com/MrWong99/glyphoxa/pkg/provider/stt"
	"github.com/MrWong99/glyphoxa/pkg/provider/stt/deepgram"
	"github.com/MrWong99/glyphoxa/pkg/provider/stt/whisper"
	"github.com/MrWong99/glyphoxa/pkg/provider/tts"
	"github.com/MrWong99/glyphoxa/pkg/provider/tts/coqui"
	"github.com/MrWong99/glyphoxa/pkg/provider/tts/elevenlabs"
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

	// ── Signal context ────────────────────────────────────────────────────────
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// ── Discord bot (optional) ────────────────────────────────────────────────
	var bot *discordbot.Bot
	if cfg.Discord.Token != "" {
		botCfg := discordbot.Config{
			Token:    cfg.Discord.Token,
			GuildID:  cfg.Discord.GuildID,
			DMRoleID: cfg.Discord.DMRoleID,
		}

		bot, err = discordbot.New(ctx, botCfg)
		if err != nil {
			slog.Error("failed to create Discord bot", "err", err)
			return 1
		}
		// Use the bot's audio platform instead of the provider registry's audio.
		providers.Audio = bot.Platform()
		slog.Info("discord bot connected", "guild_id", cfg.Discord.GuildID)
	}

	// ── Startup summary ───────────────────────────────────────────────────────
	printStartupSummary(cfg)

	application, err := app.New(ctx, cfg, providers)
	if err != nil {
		slog.Error("failed to initialise application", "err", err)
		return 1
	}

	// Start the Discord bot interaction loop in a separate goroutine.
	if bot != nil {
		go func() {
			if err := bot.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
				slog.Error("discord bot error", "err", err)
			}
		}()
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

	// Close the Discord bot first (unregister commands, disconnect).
	if bot != nil {
		if err := bot.Close(); err != nil {
			slog.Warn("discord bot close error", "err", err)
		}
	}

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
	"llm":        {"openai", "anthropic", "ollama", "gemini", "deepseek", "mistral", "groq", "llamacpp", "llamafile"},
	"stt":        {"deepgram", "whisper", "whisper-native"},
	"tts":        {"elevenlabs", "coqui"},
	"s2s":        {"openai-realtime", "gemini-live"},
	"embeddings": {"openai", "ollama"},
}

// registerBuiltinProviders wires all built-in provider factories into reg.
// Each factory receives a config.ProviderEntry and constructs the appropriate
// provider from the real implementation packages.
func registerBuiltinProviders(reg *config.Registry) {
	// ── LLM ───────────────────────────────────────────────────────────────────
	// openai, anthropic, gemini, deepseek, mistral, groq, llamacpp, llamafile
	// all share the same pattern: optional APIKey + optional BaseURL.
	for _, providerName := range []string{
		"openai", "anthropic", "gemini",
		"deepseek", "mistral", "groq", "llamacpp", "llamafile",
	} {
		reg.RegisterLLM(providerName, func(entry config.ProviderEntry) (llm.Provider, error) {
			var opts []anyllmlib.Option
			if entry.APIKey != "" {
				opts = append(opts, anyllmlib.WithAPIKey(entry.APIKey))
			}
			if entry.BaseURL != "" {
				opts = append(opts, anyllmlib.WithBaseURL(entry.BaseURL))
			}
			p, err := anyllm.New(providerName, entry.Model, opts...)
			if err != nil {
				return nil, err
			}
			return p, nil
		})
	}

	// ollama is a local server; it uses BaseURL for the address, not an API key.
	reg.RegisterLLM("ollama", func(entry config.ProviderEntry) (llm.Provider, error) {
		var opts []anyllmlib.Option
		if entry.BaseURL != "" {
			opts = append(opts, anyllmlib.WithBaseURL(entry.BaseURL))
		}
		p, err := anyllm.New("ollama", entry.Model, opts...)
		if err != nil {
			return nil, err
		}
		return p, nil
	})

	// ── STT ───────────────────────────────────────────────────────────────────

	reg.RegisterSTT("deepgram", func(entry config.ProviderEntry) (stt.Provider, error) {
		var opts []deepgram.Option
		if entry.Model != "" {
			opts = append(opts, deepgram.WithModel(entry.Model))
		}
		if lang := optString(entry.Options, "language"); lang != "" {
			opts = append(opts, deepgram.WithLanguage(lang))
		}
		return deepgram.New(entry.APIKey, opts...)
	})

	reg.RegisterSTT("whisper", func(entry config.ProviderEntry) (stt.Provider, error) {
		var opts []whisper.Option
		if entry.Model != "" {
			opts = append(opts, whisper.WithModel(entry.Model))
		}
		if lang := optString(entry.Options, "language"); lang != "" {
			opts = append(opts, whisper.WithLanguage(lang))
		}
		return whisper.New(entry.BaseURL, opts...)
	})

	reg.RegisterSTT("whisper-native", func(entry config.ProviderEntry) (stt.Provider, error) {
		modelPath := entry.Model
		if modelPath == "" {
			modelPath = optString(entry.Options, "model_path")
		}
		var opts []whisper.NativeOption
		if lang := optString(entry.Options, "language"); lang != "" {
			opts = append(opts, whisper.WithNativeLanguage(lang))
		}
		return whisper.NewNative(modelPath, opts...)
	})

	// ── TTS ───────────────────────────────────────────────────────────────────

	reg.RegisterTTS("elevenlabs", func(entry config.ProviderEntry) (tts.Provider, error) {
		var opts []elevenlabs.Option
		if entry.Model != "" {
			opts = append(opts, elevenlabs.WithModel(entry.Model))
		}
		if outputFmt := optString(entry.Options, "output_format"); outputFmt != "" {
			opts = append(opts, elevenlabs.WithOutputFormat(outputFmt))
		}
		return elevenlabs.New(entry.APIKey, opts...)
	})

	reg.RegisterTTS("coqui", func(entry config.ProviderEntry) (tts.Provider, error) {
		var opts []coqui.Option
		if lang := optString(entry.Options, "language"); lang != "" {
			opts = append(opts, coqui.WithLanguage(lang))
		}
		if mode := optString(entry.Options, "api_mode"); mode != "" {
			opts = append(opts, coqui.WithAPIMode(coqui.APIMode(mode)))
		}
		return coqui.New(entry.BaseURL, opts...)
	})

	// ── Embeddings ────────────────────────────────────────────────────────────

	reg.RegisterEmbeddings("openai", func(entry config.ProviderEntry) (embeddings.Provider, error) {
		var opts []oaembed.Option
		if entry.BaseURL != "" {
			opts = append(opts, oaembed.WithBaseURL(entry.BaseURL))
		}
		return oaembed.New(entry.APIKey, entry.Model, opts...)
	})

	reg.RegisterEmbeddings("ollama", func(entry config.ProviderEntry) (embeddings.Provider, error) {
		return ollamaembed.New(entry.BaseURL, entry.Model)
	})

	// ── S2S ───────────────────────────────────────────────────────────────────

	reg.RegisterS2S("openai-realtime", func(entry config.ProviderEntry) (s2s.Provider, error) {
		var opts []oais2s.Option
		if entry.Model != "" {
			opts = append(opts, oais2s.WithModel(entry.Model))
		}
		if entry.BaseURL != "" {
			opts = append(opts, oais2s.WithBaseURL(entry.BaseURL))
		}
		return oais2s.New(entry.APIKey, opts...), nil
	})

	reg.RegisterS2S("gemini-live", func(entry config.ProviderEntry) (s2s.Provider, error) {
		var opts []geminilive.Option
		if entry.Model != "" {
			opts = append(opts, geminilive.WithModel(entry.Model))
		}
		if entry.BaseURL != "" {
			opts = append(opts, geminilive.WithBaseURL(entry.BaseURL))
		}
		return geminilive.New(entry.APIKey, opts...), nil
	})

	// Debug log of all registered providers.
	for kind, names := range builtinProviders {
		for _, name := range names {
			slog.Debug("registered provider", "kind", kind, "name", name)
		}
	}
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
	if cfg.Discord.Token != "" {
		fmt.Printf("║  Discord         : %-19s ║\n", "connected")
	} else {
		fmt.Printf("║  Discord         : %-19s ║\n", "(disabled)")
	}
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

// ── Helpers ───────────────────────────────────────────────────────────────────

// optString extracts a string value from a provider Options map[string]any.
// Returns "" if the map is nil, the key is absent, or the value is not a string.
func optString(opts map[string]any, key string) string {
	if opts == nil {
		return ""
	}
	v, ok := opts[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}
