package config

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/MrWong99/glyphoxa/internal/mcp"
	"gopkg.in/yaml.v3"
)

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

	// NPCs
	for i, npc := range cfg.NPCs {
		prefix := fmt.Sprintf("npcs[%d]", i)
		if npc.Name == "" {
			errs = append(errs, fmt.Errorf("%s.name is required", prefix))
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

