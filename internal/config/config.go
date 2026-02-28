// Package config provides the configuration schema, loader, and provider registry
// for the Glyphoxa voice AI system.
package config

import "github.com/MrWong99/glyphoxa/internal/mcp"

// LogLevel controls log verbosity for the Glyphoxa server.
type LogLevel string

const (
	LogDebug LogLevel = "debug"
	LogInfo  LogLevel = "info"
	LogWarn  LogLevel = "warn"
	LogError LogLevel = "error"
)

// IsValid reports whether l is a recognised log level.
func (l LogLevel) IsValid() bool {
	switch l {
	case LogDebug, LogInfo, LogWarn, LogError:
		return true
	}
	return false
}

// Engine selects the conversation pipeline mode for an NPC.
type Engine string

const (
	// EngineCascaded uses the STT → LLM → TTS pipeline.
	EngineCascaded Engine = "cascaded"

	// EngineS2S uses an end-to-end speech model.
	EngineS2S Engine = "s2s"

	// EngineSentenceCascade uses the experimental dual-model sentence cascade
	// (fast opener + strong continuation). See docs/design/05-sentence-cascade.md.
	EngineSentenceCascade Engine = "sentence_cascade"
)

// IsValid reports whether e is a recognised engine mode.
func (e Engine) IsValid() bool {
	return e == EngineCascaded || e == EngineS2S || e == EngineSentenceCascade
}

// CascadeMode controls the behaviour of the sentence cascade engine.
type CascadeMode string

const (
	// CascadeModeOff disables the sentence cascade (default).
	CascadeModeOff CascadeMode = "off"

	// CascadeModeAuto enables the cascade only for complex or high-importance
	// interactions, as determined by the orchestrator.
	CascadeModeAuto CascadeMode = "auto"

	// CascadeModeAlways enables the cascade for every interaction.
	CascadeModeAlways CascadeMode = "always"
)

// IsValid reports whether m is a recognised cascade mode.
func (m CascadeMode) IsValid() bool {
	switch m {
	case CascadeModeOff, CascadeModeAuto, CascadeModeAlways, "":
		return true
	}
	return false
}

// BudgetTier constrains which MCP tools are offered to the LLM based on latency.
type BudgetTier string

const (
	BudgetTierFast     BudgetTier = "fast"
	BudgetTierStandard BudgetTier = "standard"
	BudgetTierDeep     BudgetTier = "deep"
)

// IsValid reports whether b is a recognised budget tier.
func (b BudgetTier) IsValid() bool {
	switch b {
	case BudgetTierFast, BudgetTierStandard, BudgetTierDeep:
		return true
	}
	return false
}

// Config is the root configuration structure for Glyphoxa.
// It is typically loaded from a YAML file using [Load] or [LoadFromReader].
type Config struct {
	Server    ServerConfig    `yaml:"server"`
	Discord   DiscordConfig   `yaml:"discord"`
	Providers ProvidersConfig `yaml:"providers"`
	NPCs      []NPCConfig     `yaml:"npcs"`
	Memory    MemoryConfig    `yaml:"memory"`
	MCP       MCPConfig       `yaml:"mcp"`
	Campaign  CampaignConfig  `yaml:"campaign"`
}

// DiscordConfig holds settings for the Discord bot subsystem.
// When Token is empty, the Discord bot is disabled and Glyphoxa runs
// without a Discord connection (useful for local development with other
// audio platforms).
type DiscordConfig struct {
	// Token is the Discord bot token (e.g., "Bot MTIz...").
	Token string `yaml:"token"`

	// GuildID is the target Discord guild (server) ID.
	// Alpha deployments support a single guild per bot instance.
	GuildID string `yaml:"guild_id"`

	// DMRoleID is the Discord role ID that identifies Dungeon Masters.
	// Users with this role can execute privileged slash commands
	// (/session, /npc, /entity, /campaign). When empty, all users
	// are treated as DMs (useful for development).
	DMRoleID string `yaml:"dm_role_id"`
}

// CampaignConfig holds pre-session entity and campaign data.
type CampaignConfig struct {
	// Name is the campaign's human-readable name (e.g., "Curse of Strahd").
	Name string `yaml:"name"`

	// System identifies the game system (e.g., "dnd5e", "pf2e").
	System string `yaml:"system"`

	// EntityFiles lists paths to YAML files containing entity definitions
	// that are loaded at startup. Paths are resolved relative to the main
	// config file's directory.
	EntityFiles []string `yaml:"entity_files,omitempty"`

	// VTTImports lists paths to VTT export files (Foundry VTT JSON or
	// Roll20 JSON) to import at startup.
	VTTImports []VTTImportConfig `yaml:"vtt_imports,omitempty"`
}

// VTTImportConfig describes a single VTT file to import.
type VTTImportConfig struct {
	// Path is the filesystem path to the VTT export file.
	Path string `yaml:"path"`

	// Format identifies the VTT platform. Supported values: "foundry", "roll20".
	Format string `yaml:"format"`
}

// ServerConfig holds network and logging settings for the Glyphoxa server.
type ServerConfig struct {
	// ListenAddr is the TCP address the server listens on (e.g., ":8080").
	ListenAddr string `yaml:"listen_addr"`

	// LogLevel controls verbosity.
	LogLevel LogLevel `yaml:"log_level"`

	// TLS configures TLS for the server. When nil, the server runs plain HTTP.
	TLS *TLSConfig `yaml:"tls"`
}

// TLSConfig holds TLS certificate paths for enabling HTTPS.
type TLSConfig struct {
	// CertFile is the path to the PEM-encoded TLS certificate.
	CertFile string `yaml:"cert_file"`

	// KeyFile is the path to the PEM-encoded TLS private key.
	KeyFile string `yaml:"key_file"`
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

	// APIKey is the authentication key for the provider's API if any.
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
	Engine Engine `yaml:"engine"`

	// KnowledgeScope lists topic domains the NPC is knowledgeable about.
	// Used for routing player questions and building retrieval queries.
	KnowledgeScope []string `yaml:"knowledge_scope"`

	// Tools lists MCP tool names this NPC is permitted to invoke.
	Tools []string `yaml:"tools"`

	// BudgetTier constrains which tools are offered to the LLM based on latency.
	BudgetTier BudgetTier `yaml:"budget_tier"`

	// CascadeMode controls the dual-model sentence cascade for this NPC.
	// Only effective when Engine is [EngineSentenceCascade]. Defaults to "off".
	CascadeMode CascadeMode `yaml:"cascade_mode"`

	// CascadeConfig holds sentence-cascade-specific settings.
	// Only used when Engine is [EngineSentenceCascade].
	CascadeConfig *CascadeConfig `yaml:"cascade,omitempty"`
}

// CascadeConfig holds configuration for the dual-model sentence cascade engine.
type CascadeConfig struct {
	// FastModel selects the small, fast model for generating the opener sentence.
	// Uses the default LLM provider if empty, with the model specified here.
	FastModel string `yaml:"fast_model"`

	// StrongModel selects the large model for generating the substantive continuation.
	// Uses the default LLM provider if empty, with the model specified here.
	StrongModel string `yaml:"strong_model"`

	// OpenerInstruction is appended to the fast model's system prompt to guide
	// the opening sentence. Defaults to a built-in instruction if empty.
	OpenerInstruction string `yaml:"opener_instruction,omitempty"`
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
	Transport mcp.Transport `yaml:"transport"`

	// Command is the executable (with optional arguments) launched when
	// Transport is "stdio". Ignored for streamable-http transport.
	Command string `yaml:"command"`

	// URL is the MCP endpoint address used when Transport is "streamable-http"
	// (e.g., "https://mcp.example.com/mcp"). Ignored for stdio transport.
	URL string `yaml:"url"`

	// Auth configures authentication for streamable-http servers.
	// Ignored for stdio transport (use Env for credential injection instead).
	// When nil, requests are sent without authentication.
	Auth *MCPAuthConfig `yaml:"auth"`

	// Env holds additional environment variables injected into the subprocess
	// when Transport is "stdio". May be nil.
	Env map[string]string `yaml:"env"`
}

// MCPAuthConfig configures authentication for HTTP-based MCP servers,
// following the MCP authorization specification (OAuth 2.1 Bearer tokens).
type MCPAuthConfig struct {
	// Token is a static Bearer token sent in the Authorization header of every
	// request. Mutually exclusive with the OAuth fields below.
	Token string `yaml:"token"`

	// OAuth configures OAuth 2.1 client-credentials flow for obtaining tokens
	// dynamically. When set, Token is ignored.
	OAuth *MCPOAuthConfig `yaml:"oauth"`
}

// MCPOAuthConfig configures the OAuth 2.1 client-credentials flow for
// obtaining Bearer tokens from an authorization server.
type MCPOAuthConfig struct {
	// ClientID is the OAuth 2.1 client identifier.
	ClientID string `yaml:"client_id"`

	// ClientSecret is the OAuth 2.1 client secret.
	ClientSecret string `yaml:"client_secret"`

	// TokenURL is the authorization server's token endpoint
	// (e.g., "https://auth.example.com/oauth/token").
	TokenURL string `yaml:"token_url"`

	// Scopes lists the OAuth scopes to request. May be empty.
	Scopes []string `yaml:"scopes"`
}
