// Package config provides the configuration schema, loader, and provider registry
// for the Glyphoxa voice AI system.
package config

// Config is the root configuration structure for Glyphoxa.
// It is typically loaded from a YAML file using [Load] or [LoadFromReader].
type Config struct {
	Server    ServerConfig    `yaml:"server"`
	Providers ProvidersConfig `yaml:"providers"`
	NPCs      []NPCConfig     `yaml:"npcs"`
	Memory    MemoryConfig    `yaml:"memory"`
	MCP       MCPConfig       `yaml:"mcp"`
}

// ServerConfig holds network and logging settings for the Glyphoxa server.
type ServerConfig struct {
	// ListenAddr is the TCP address the server listens on (e.g., ":8080").
	ListenAddr string `yaml:"listen_addr"`

	// LogLevel controls verbosity. Valid values: "debug", "info", "warn", "error".
	LogLevel string `yaml:"log_level"`
}

// ProvidersConfig declares which provider implementation to use for each
// pipeline stage. Each field selects a named provider registered in the [Registry].
type ProvidersConfig struct {
	LLM        ProviderEntry `yaml:"llm"`
	STT        ProviderEntry `yaml:"stt"`
	TTS        ProviderEntry `yaml:"tts"`
	S2S        ProviderEntry `yaml:"s2s"`
	Embeddings ProviderEntry `yaml:"embeddings"`
	VAD        ProviderEntry `yaml:"vad"`
	Audio      ProviderEntry `yaml:"audio"`
}

// ProviderEntry is the common configuration block shared by all provider types.
// The Name field is used to look up the constructor in the [Registry].
type ProviderEntry struct {
	// Name selects the registered provider implementation (e.g., "openai", "deepgram").
	Name string `yaml:"name"`

	// APIKey is the authentication key for the provider's API.
	APIKey string `yaml:"api_key"`

	// BaseURL overrides the provider's default API endpoint.
	// Leave empty to use the provider's built-in default.
	BaseURL string `yaml:"base_url"`

	// Model selects a specific model within the provider (e.g., "gpt-4o", "nova-2").
	Model string `yaml:"model"`

	// Options holds provider-specific configuration values not covered by the
	// standard fields above. Values may be strings, numbers, booleans, or nested maps.
	Options map[string]any `yaml:"options"`
}

// NPCConfig describes a single NPC's personality, voice, and runtime behaviour.
type NPCConfig struct {
	// Name is the NPC's in-world display name (e.g., "Greymantle the Sage").
	Name string `yaml:"name"`

	// Personality is a free-text persona description injected into the LLM system prompt.
	Personality string `yaml:"personality"`

	// Voice configures the TTS voice profile for this NPC.
	Voice VoiceConfig `yaml:"voice"`

	// Engine selects the conversation pipeline mode.
	// Valid values: "cascaded" (STT → LLM → TTS) or "s2s" (end-to-end speech model).
	Engine string `yaml:"engine"`

	// KnowledgeScope lists topic domains the NPC is knowledgeable about.
	// Used for routing player questions and building retrieval queries.
	KnowledgeScope []string `yaml:"knowledge_scope"`

	// Tools lists MCP tool names this NPC is permitted to invoke.
	Tools []string `yaml:"tools"`

	// BudgetTier constrains which tools are offered to the LLM based on latency.
	// Valid values: "fast", "standard", "deep".
	BudgetTier string `yaml:"budget_tier"`
}

// VoiceConfig specifies the TTS voice parameters for an NPC.
type VoiceConfig struct {
	// Provider is the TTS provider name (e.g., "elevenlabs", "google").
	Provider string `yaml:"provider"`

	// VoiceID is the provider-specific voice identifier.
	VoiceID string `yaml:"voice_id"`

	// PitchShift adjusts pitch in the range [-10, +10]. 0 means default.
	PitchShift float64 `yaml:"pitch_shift"`

	// SpeedFactor adjusts speaking rate in the range [0.5, 2.0]. 1.0 means default.
	SpeedFactor float64 `yaml:"speed_factor"`
}

// MemoryConfig holds settings for the long-term memory / semantic retrieval layer.
type MemoryConfig struct {
	// PostgresDSN is the PostgreSQL connection string for the pgvector memory store.
	// Example: "postgres://user:pass@localhost:5432/glyphoxa?sslmode=disable"
	PostgresDSN string `yaml:"postgres_dsn"`

	// EmbeddingDimensions is the vector dimension used for the embeddings column.
	// Must match the model configured in Providers.Embeddings.
	EmbeddingDimensions int `yaml:"embedding_dimensions"`
}

// MCPConfig holds the list of Model Context Protocol servers to connect to.
type MCPConfig struct {
	Servers []MCPServerConfig `yaml:"servers"`
}

// MCPServerConfig describes how to connect to a single MCP tool server.
type MCPServerConfig struct {
	// Name is a unique human-readable identifier for this server (used in logs).
	Name string `yaml:"name"`

	// Transport specifies the connection mechanism.
	// Valid values: "stdio", "http", "sse".
	Transport string `yaml:"transport"`

	// Command is the executable (with optional arguments) launched when
	// Transport is "stdio". Ignored for http/sse transports.
	Command string `yaml:"command"`

	// URL is the endpoint address used when Transport is "http" or "sse".
	// Ignored for stdio transport.
	URL string `yaml:"url"`

	// Env holds additional environment variables injected into the subprocess
	// when Transport is "stdio". May be nil.
	Env map[string]string `yaml:"env"`
}
