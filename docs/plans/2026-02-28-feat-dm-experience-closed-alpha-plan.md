---
title: "feat: DM Experience and Closed Alpha"
type: feat
status: completed
date: 2026-02-28
phase: "7"
---

# DM Experience and Closed Alpha

## Overview

Phase 7 builds the DM-facing control surface for Glyphoxa and validates the system with real play groups. This includes:

1. **Discord bot adapter** — both the `audio.Platform` implementation for voice transport and a separate bot layer for slash commands, interactions, and embeds
2. **Slash command interface** — NPC management, entity management, campaign management, and session lifecycle
3. **Voice commands** — keyword detection on STT partials for low-latency DM control
4. **Session dashboard** — Discord embed showing live session stats
5. **Closed alpha program** — 3–5 DMs running 2–4 hour sessions with telemetry and structured feedback

## Problem Statement

Glyphoxa's voice pipeline, orchestrator, memory subsystem, and MCP tools are functionally complete (Phases 1–6). But the system has **no user-facing control surface** — everything is config-file-driven with a single `glyphoxa serve` entrypoint. A DM cannot:

- Start or stop sessions dynamically
- Manage NPCs during a live session (mute, unmute, puppet)
- Add entities or switch campaigns without editing YAML and restarting
- Control NPCs via voice commands
- See real-time performance metrics

Without this control layer, the system cannot be validated by real users.

## Proposed Solution

Build a Discord bot layer that owns the `discordgo.Session` and exposes both an `audio.Platform` adapter (for voice transport) and a command handler framework (for slash commands). Separate the application lifecycle into two phases:

1. **Bot phase** — long-running daemon connected to Discord, registered slash commands, listening for interactions
2. **Session phase** — voice pipeline active, NPCs loaded, audio processing running

This separation allows the bot to accept commands (`/session start`, `/entity add`) while no voice session is active, and to manage the session lifecycle without process restarts.

## Technical Approach

### Architecture

```
┌─────────────────────────────────────────────────────┐
│                  cmd/glyphoxa/main.go                │
│   config.Load → Registry → buildProviders → Bot.Run │
└───────────────────────┬─────────────────────────────┘
                        │
        ┌───────────────┴───────────────┐
        │     internal/discord/bot.go   │
        │   DiscordBot struct           │
        │   - *discordgo.Session        │
        │   - CommandRouter             │
        │   - PermissionChecker         │
        │   - audio.Platform (embed)    │
        └───┬──────────┬──────────┬─────┘
            │          │          │
   ┌────────┴──┐  ┌────┴────┐  ┌─┴──────────┐
   │ Commands  │  │ Voice   │  │ Dashboard  │
   │ /npc      │  │ Command │  │ Updater    │
   │ /entity   │  │ Filter  │  │            │
   │ /session  │  │         │  │            │
   │ /campaign │  │         │  │            │
   └─────┬─────┘  └────┬────┘  └─────┬──────┘
         │             │              │
   ┌─────┴─────────────┴──────────────┴──────┐
   │           internal/app/app.go            │
   │  SessionManager (new)                    │
   │  - Orchestrator                          │
   │  - EntityStore, NpcStore, SessionStore   │
   │  - MCPHost, Metrics                      │
   └──────────────────────────────────────────┘
```

### Implementation Phases

#### Phase 7.1: Discord Bot Foundation

**Goal:** Build the Discord bot adapter, implement `audio.Platform` for Discord voice, and wire slash command registration.

**Deliverables:**

- `pkg/audio/discord/` — Discord voice transport implementing `audio.Platform`
- `internal/discord/` — Discord bot layer (command routing, permission checking)
- Config schema additions for Discord bot settings
- Bot lifecycle in `main.go` (connect, register commands, listen)

##### `pkg/audio/discord/platform.go`

```go
// Package discord implements audio.Platform for Discord voice channels
// using the discordgo library.
package discord

// Platform implements audio.Platform using a discordgo voice connection.
// It requires an active *discordgo.Session (owned by the bot layer).
type Platform struct {
    session *discordgo.Session
}

// New creates a Discord audio platform from an existing bot session.
func New(session *discordgo.Session) *Platform

// Connect joins the voice channel and returns an audio.Connection.
func (p *Platform) Connect(ctx context.Context, channelID string) (audio.Connection, error)
```

The `audio.Connection` implementation wraps `discordgo.VoiceConnection` and adapts its Opus audio frames to Glyphoxa's `AudioFrame` type, handling encoding/decoding, per-participant demuxing, and the output stream.

##### `internal/discord/bot.go`

```go
// Package discord provides the Discord bot layer for Glyphoxa.
// It owns the discordgo.Session and exposes both the audio.Platform
// (for voice transport) and the command handler framework (for slash commands).
package discord

// Bot owns the Discord gateway connection and routes interactions
// to registered command handlers.
type Bot struct {
    session   *discordgo.Session
    platform  *discordaudio.Platform  // audio.Platform adapter
    commands  *CommandRouter
    perms     *PermissionChecker
    guildID   string                  // target guild (single-guild for alpha)
}

// Config holds Discord bot configuration.
type Config struct {
    Token      string   `yaml:"token"`
    GuildID    string   `yaml:"guild_id"`
    DMRoleID   string   `yaml:"dm_role_id"`   // Discord role ID for DM permissions
}

// New creates a Bot, connects to Discord, and registers slash commands.
func New(ctx context.Context, cfg Config) (*Bot, error)

// Platform returns the audio.Platform for voice channel connections.
func (b *Bot) Platform() audio.Platform

// Run starts the interaction handler loop. Blocks until ctx is cancelled.
func (b *Bot) Run(ctx context.Context) error

// Close disconnects from Discord and unregisters commands.
func (b *Bot) Close() error
```

##### `internal/discord/permissions.go`

```go
// PermissionChecker validates that a Discord user has the DM role
// before executing privileged slash commands.
type PermissionChecker struct {
    dmRoleID string
}

// IsDM checks whether the interaction author has the configured DM role.
func (p *PermissionChecker) IsDM(i *discordgo.InteractionCreate) bool
```

**Design decision — DM identification:** Use a Discord role (configured via `discord.dm_role_id` in YAML). Any guild member with this role can execute DM commands. This is flexible (multiple co-DMs), auditable (Discord role management), and requires no Glyphoxa-specific auth system.

##### Config schema additions

```yaml
# New top-level section in config.yaml
discord:
  token: "Bot ..."           # Discord bot token
  guild_id: "123456789"      # Target guild
  dm_role_id: "987654321"    # Role ID for DM permissions
```

This replaces the current `providers.audio.name: discord` pattern. The Discord bot is a first-class subsystem, not just an audio provider.

##### Tasks

- [x] `pkg/audio/discord/platform.go` — `Platform` struct, `New()`, `Connect()`
- [x] `pkg/audio/discord/connection.go` — `Connection` struct wrapping `discordgo.VoiceConnection`
- [x] `pkg/audio/discord/opus.go` — Opus encode/decode helpers, per-participant demux
- [x] `internal/discord/bot.go` — `Bot` struct, `New()`, `Run()`, `Close()`
- [x] `internal/discord/router.go` — `CommandRouter` for slash command dispatch
- [x] `internal/discord/permissions.go` — `PermissionChecker` with DM role check
- [x] `internal/config/config.go` — Add `DiscordConfig` struct
- [x] `cmd/glyphoxa/main.go` — Register `discord` audio factory, wire bot lifecycle
- [x] `pkg/audio/discord/platform_test.go` — Compliance tests against `audio.Platform` interface
- [x] `internal/discord/bot_test.go` — Command registration, permission checks
- [x] `internal/discord/mock/` — Hand-written mock for `discordgo.Session` interactions

**Success criteria:**
- Bot connects to Discord, registers slash commands, responds to pings
- Voice platform passes the existing `audio.Platform` compliance test suite
- DM role check correctly gates all privileged commands
- `make test` and `make lint` pass with `-race`

---

#### Phase 7.2: Session Manager and Lifecycle Refactor

**Goal:** Separate the bot lifecycle from the voice session lifecycle so that sessions can be started/stopped via slash commands.

**Deliverables:**

- `internal/app/session_manager.go` — manages session start/stop, holds orchestrator and agents
- Refactored `App` struct to support "bot running, no session" state
- `/session start`, `/session stop` slash command handlers

##### `internal/app/session_manager.go`

```go
// SessionManager manages the lifecycle of a single voice session within
// a running Bot. It owns the orchestrator, agents, audio connection,
// and transcript recording goroutines.
type SessionManager struct {
    mu        sync.Mutex
    active    bool
    sessionID string
    startedAt time.Time
    startedBy string // Discord user ID of the DM who started the session

    // Subsystem references (shared with App)
    cfg       *config.Config
    providers *Providers
    mcpHost   mcp.Host
    entities  entity.Store
    sessions  memory.SessionStore
    graph     memory.KnowledgeGraph

    // Per-session state (created on Start, destroyed on Stop)
    conn         audio.Connection
    agents       []agent.NPCAgent
    router       *orchestrator.Orchestrator
    consolidator *session.Consolidator
    mixer        audio.Mixer
    cancel       context.CancelFunc
}

// Start connects to the voice channel, loads NPCs, and begins audio processing.
func (sm *SessionManager) Start(ctx context.Context, channelID string, dmUserID string) error

// Stop gracefully tears down the session: consolidates, disconnects, closes engines.
func (sm *SessionManager) Stop(ctx context.Context) error

// IsActive reports whether a voice session is currently running.
func (sm *SessionManager) IsActive() bool

// Orchestrator returns the active session's orchestrator, or nil if no session.
func (sm *SessionManager) Orchestrator() *orchestrator.Orchestrator

// SessionInfo returns metadata about the active session (for dashboard).
func (sm *SessionManager) SessionInfo() *SessionInfo
```

##### Key design decisions

1. **One session at a time per bot.** `SessionManager` enforces mutual exclusion — calling `Start()` while a session is active returns an error.

2. **Immediate consolidation on Stop.** `Stop()` calls `Consolidator.ConsolidateNow()` before teardown, ensuring no conversation history is lost.

3. **Session ID format:** `session-{campaign}-{timestamp}` (e.g., `session-ironhold-20260228T1930Z`). Includes timestamp for uniqueness across restarts.

4. **DM tracking:** The user who ran `/session start` is recorded as the session owner. This enables "only the DM who started can stop" enforcement if desired (but initially any DM-role user can stop).

##### `/session start` handler

```go
// handleSessionStart handles the /session start slash command.
// It connects to the DM's current voice channel and starts audio processing.
func (b *Bot) handleSessionStart(i *discordgo.InteractionCreate) {
    // 1. Check DM permission
    // 2. Resolve the DM's current voice channel
    // 3. Call SessionManager.Start(ctx, channelID, userID)
    // 4. Respond with session info embed
}
```

The voice channel is auto-detected from the DM's current voice state — no channel ID argument needed.

##### `/session stop` handler

```go
// handleSessionStop handles the /session stop slash command.
// It consolidates the session, disconnects audio, and tears down agents.
func (b *Bot) handleSessionStop(i *discordgo.InteractionCreate) {
    // 1. Check DM permission
    // 2. Call SessionManager.Stop(ctx)
    // 3. Respond with session summary (duration, utterance count, etc.)
}
```

##### Tasks

- [x] `internal/app/session_manager.go` — `SessionManager` struct and lifecycle methods
- [x] `internal/app/app.go` — Refactor `App` to delegate session state to `SessionManager`
- [x] `internal/discord/commands/session.go` — `/session start` and `/session stop` handlers
- [x] `internal/app/session_manager_test.go` — Start/stop lifecycle, concurrent access, consolidation
- [x] `internal/discord/commands/session_test.go` — Command handling with mocked session manager

**Success criteria:**
- `/session start` connects to the DM's voice channel and begins audio processing
- `/session stop` consolidates and cleanly disconnects
- Starting a session while one is active returns a clear error
- Stopping a non-existent session is a no-op with a helpful message
- All conversation history is persisted before teardown

---

#### Phase 7.3: NPC Management Slash Commands

**Goal:** Implement `/npc list`, `/npc mute`, `/npc unmute`, `/npc speak` slash commands.

**Deliverables:**

- Orchestrator extensions (`IsMuted`, `MuteAll`, `AgentByName`)
- NPC name resolution via Discord autocomplete
- Text-puppet synthesis path

##### Orchestrator extensions

```go
// internal/agent/orchestrator/orchestrator.go — new methods

// IsMuted reports whether the agent with the given ID is muted.
func (o *Orchestrator) IsMuted(id string) (bool, error)

// MuteAll mutes all agents atomically.
func (o *Orchestrator) MuteAll() int

// UnmuteAll unmutes all agents atomically.
func (o *Orchestrator) UnmuteAll() int

// AgentByName returns the first agent whose Name() matches name
// (case-insensitive). Returns nil if no match.
func (o *Orchestrator) AgentByName(name string) agent.NPCAgent
```

##### NPC name resolution

Slash command arguments for NPC names use Discord's autocomplete feature. When the DM types `/npc mute G...`, the bot queries `Orchestrator.ActiveAgents()` and returns matching names as autocomplete choices. The selected choice carries the NPC's ID as its value, avoiding fuzzy matching at execution time.

```go
// internal/discord/commands/npc.go

// handleNPCAutocomplete populates autocomplete choices from active agents.
func (b *Bot) handleNPCAutocomplete(i *discordgo.InteractionCreate) {
    agents := b.session.Orchestrator().ActiveAgents()
    // Filter by user's partial input, return name→ID pairs
}
```

##### Text-puppet synthesis path

`/npc speak <name> <text>` needs to synthesize TTS from text without going through the full STT→LLM→TTS pipeline. This is implemented as a new method on `NPCAgent`:

```go
// internal/agent/agent.go — new method on NPCAgent interface

// SpeakText synthesizes the given text using this NPC's TTS voice and
// enqueues the audio for playback via the mixer. Used for DM puppet mode.
// The text is recorded in the session transcript as an NPC utterance.
SpeakText(ctx context.Context, text string) error
```

Implementation in `internal/agent/npc.go`:

1. Call `ttsProvider.Synthesize(ctx, text, voice)` directly
2. Enqueue audio frames in the mixer
3. Write a transcript entry with `SpeakerName: npc.Name()` and the puppet text

This bypasses the engine (no LLM inference, no tool calling) and directly uses the TTS provider. The NPC's voice profile is applied so the synthesized speech sounds like the NPC.

##### Slash command definitions

| Command | Arguments | Permission | Backend call |
|---|---|---|---|
| `/npc list` | — | DM role | `Orchestrator.ActiveAgents()` + `IsMuted()` |
| `/npc mute <name>` | name (autocomplete) | DM role | `Orchestrator.MuteAgent(id)` |
| `/npc unmute <name>` | name (autocomplete) | DM role | `Orchestrator.UnmuteAgent(id)` |
| `/npc speak <name> <text>` | name (autocomplete), text (string) | DM role | `NPCAgent.SpeakText(ctx, text)` |

##### Error handling

All NPC commands respond ephemerally (only visible to the DM):

- NPC not found → "No NPC named `{name}` is active."
- No active session → "No session is running. Use `/session start` first."
- TTS provider down → "Failed to synthesize speech: {error}. Check provider status."
- Already muted/unmuted → "Grimjaw is already muted." (informational, not error)

##### Tasks

- [x] `internal/agent/orchestrator/orchestrator.go` — Add `IsMuted()`, `MuteAll()`, `UnmuteAll()`, `AgentByName()`
- [x] `internal/agent/agent.go` — Add `SpeakText()` to `NPCAgent` interface
- [x] `internal/agent/npc.go` — Implement `SpeakText()` on `liveAgent`
- [x] `internal/discord/commands/npc.go` — All `/npc` command handlers + autocomplete
- [x] `internal/agent/orchestrator/orchestrator_test.go` — Tests for new orchestrator methods
- [x] `internal/agent/npc_test.go` — Test `SpeakText()` with mock TTS
- [x] `internal/discord/commands/npc_test.go` — Command handler tests

**Success criteria:**
- `/npc list` shows all NPCs with mute state in a formatted embed
- `/npc mute Grimjaw` mutes by name via autocomplete, NPC stops responding
- `/npc speak Grimjaw "Hello adventurers"` synthesizes and plays audio in Grimjaw's voice
- Autocomplete returns matching NPC names within 100ms
- All error cases return clear, ephemeral messages

---

#### Phase 7.4: Entity and Campaign Management

**Goal:** Implement `/entity` and `/campaign` slash commands.

##### Entity commands

| Command | Arguments | Permission | Backend call |
|---|---|---|---|
| `/entity add` | name, type, description | DM role | `entity.Store.Add()` |
| `/entity list` | type (optional) | DM role | `entity.Store.List()` |
| `/entity remove <name>` | name (autocomplete) | DM role | `entity.Store.Remove()` + confirmation |
| `/entity import` | file (attachment) | DM role | Format detection → importer |

##### `/entity add` — Discord modal

Uses a Discord modal (popup form) for multi-field input:

```
┌─────────────────────────────────┐
│ Add Entity                      │
├─────────────────────────────────┤
│ Name:  [________________]       │
│ Type:  [NPC ▼]                  │
│ Description:                    │
│ [_____________________________] │
│ [_____________________________] │
│ Tags:  [________________]       │
│                                 │
│      [Cancel]  [Add Entity]     │
└─────────────────────────────────┘
```

##### `/entity import <file>` — attachment handling

1. Download Discord attachment URL to memory (max 10MB)
2. Detect format: `.yaml`/`.yml` → campaign file, `.json` → inspect for Foundry VTT or Roll20 structure
3. Call appropriate importer: `entity.LoadCampaignFromReader()`, `entity.ImportFoundryVTT()`, or `entity.ImportRoll20()`
4. Respond with import summary: `"Imported 23 entities (5 NPCs, 8 locations, 10 items)"`

**File validation:**
- Reject files > 10MB
- Validate entity definitions before bulk import
- Report parsing errors with line numbers where possible

##### `/entity remove` — confirmation

Destructive operation. Use a Discord button confirmation:

```
"Remove entity **Grimjaw the Blacksmith**? This cannot be undone."
[Cancel]  [Confirm Remove]
```

##### Mid-session entity propagation

When an entity is added mid-session, propagate to the live knowledge graph:

1. `entity.Store.Add()` persists the definition
2. Convert `EntityDefinition` to `memory.Entity` and call `KnowledgeGraph.AddEntity()`
3. Add the entity name to the STT keyword boost list (if supported by the active STT provider)
4. Add the entity name to the phonetic name index in the transcript correction pipeline

This ensures new entities are immediately available for NPC memory queries and transcript correction.

##### Campaign commands

| Command | Arguments | Permission | Backend call |
|---|---|---|---|
| `/campaign info` | — | DM role | Display current campaign metadata |
| `/campaign load` | file (attachment) | DM role | Parse YAML, re-init entities + NPCs |
| `/campaign switch` | name (autocomplete) | DM role | Requires session stop first |

**`/campaign switch` guard:** If a session is active, respond with "Stop the current session first with `/session stop` before switching campaigns." This avoids the architectural complexity of hot-swapping NPC agents mid-session.

**`/campaign load`:** Loads a campaign YAML file, imports its entities, and stores the campaign config for future `/campaign switch`. Does NOT automatically start a session — the DM must run `/session start` after loading.

##### Tasks

- [x] `internal/discord/commands/entity.go` — `/entity add`, `/entity list`, `/entity remove`, `/entity import`
- [x] `internal/discord/commands/entity_modal.go` — Discord modal for entity creation
- [x] `internal/discord/commands/campaign.go` — `/campaign info`, `/campaign load`, `/campaign switch`
- [x] `internal/discord/commands/attachment.go` — Discord attachment download + format detection
- [x] `internal/app/session_manager.go` — `PropagateEntity()` for mid-session entity addition
- [x] Tests for all command handlers with mocked stores
- [x] Tests for attachment download and format detection

**Success criteria:**
- `/entity add` opens a modal, creates entity, and confirms with embed
- `/entity import` accepts YAML and VTT JSON files up to 10MB
- `/entity remove` requires confirmation before deletion
- `/campaign switch` is gated on no active session
- Mid-session entity additions are immediately visible to NPC memory queries

---

#### Phase 7.5: Voice Commands

**Goal:** Implement keyword detection on STT partials for DM voice shortcuts.

##### Architecture

Voice commands are a pre-routing filter in the audio pipeline. They intercept STT partials **before** `Orchestrator.Route()` is called, and only for the DM's audio stream.

```
DM audio → VAD → STT → [VoiceCommandFilter] → Orchestrator.Route()
                              ↓ (match)
                        Execute command
                        Suppress utterance
```

##### `internal/discord/voicecmd/filter.go`

```go
// Package voicecmd provides keyword-based voice command detection
// for the DM's audio stream.
package voicecmd

// Filter intercepts STT partials and detects DM voice commands.
// When a command is detected, the filter executes it and signals
// the caller to suppress the utterance from NPC routing.
type Filter struct {
    patterns []Pattern
    orch     *orchestrator.Orchestrator
    dmUserID string   // only process commands from this user
}

// Pattern defines a voice command trigger and its action.
type Pattern struct {
    // Regex matches against the STT partial text.
    Regex    *regexp.Regexp
    // Action is called when the pattern matches.
    Action   func(ctx context.Context, matches []string) error
}

// Check tests the partial transcript against all patterns.
// Returns (true, nil) if a command was matched and executed.
// Returns (false, nil) if no command was detected (pass through to NPC routing).
func (f *Filter) Check(ctx context.Context, userID string, text string) (bool, error)
```

##### Command patterns

| Voice command | Pattern | Action |
|---|---|---|
| "Grimjaw, be quiet" | `(?i)^(\w+),?\s+be\s+quiet$` | `MuteAgent(byName)` |
| "Mute Grimjaw" | `(?i)^mute\s+(\w+)$` | `MuteAgent(byName)` |
| "Unmute Grimjaw" | `(?i)^unmute\s+(\w+)$` | `UnmuteAgent(byName)` |
| "Everyone, stop" | `(?i)^everyone,?\s+stop$` | `MuteAll()` |
| "Everyone, continue" | `(?i)^everyone,?\s+continue$` | `UnmuteAll()` |
| "Grimjaw, say ..." | `(?i)^(\w+),?\s+say\s+(.+)$` | `SpeakText(name, text)` |

Patterns are checked against **final** transcripts (not partials) to avoid false positives from incomplete speech. The filter runs on the DM's audio stream only — identified by matching `userID` against the configured DM Discord user ID.

##### Integration with audio pipeline

Modify `App.handleSTTFinals()` (`internal/app/app.go:478`) to check the voice command filter before routing:

```go
func (a *App) handleSTTFinals(ctx context.Context, userID string, sess stt.SessionHandle) {
    for {
        select {
        case t, ok := <-sess.Finals():
            if !ok { return }
            if t.Text == "" { continue }

            // Voice command filter (DM only)
            if a.voiceFilter != nil {
                matched, err := a.voiceFilter.Check(ctx, userID, t.Text)
                if err != nil {
                    slog.Warn("voice command error", "err", err)
                }
                if matched {
                    continue // suppress from NPC routing
                }
            }

            // Normal NPC routing
            npc, err := a.router.Route(ctx, userID, t)
            // ...
        }
    }
}
```

##### DM identification for voice commands

The `VoiceCommandFilter` needs to know which Discord user is the DM. This is set when `/session start` is called — the user who starts the session is recorded as the DM. Additionally, any user with the DM role can be recognized as a potential command issuer.

##### Tasks

- [x] `internal/discord/voicecmd/filter.go` — `Filter` struct with pattern matching
- [x] `internal/discord/voicecmd/patterns.go` — Default command patterns (patterns integrated into filter.go)
- [x] `internal/discord/voicecmd/filter_test.go` — Pattern matching tests (table-driven)
- [x] `internal/app/app.go` — Integrate voice command filter in `handleSTTFinals()`
- [x] `internal/app/session_manager.go` — Pass DM user ID to voice command filter on session start

**Success criteria:**
- "Grimjaw, be quiet" mutes Grimjaw within one STT final latency (no LLM round-trip)
- "Everyone, stop" mutes all NPCs atomically
- Voice commands from non-DM users are ignored (passed to NPC routing normally)
- Voice command text is NOT routed to any NPC
- False positive rate is < 1% on natural conversation (validated with sample transcripts)

---

#### Phase 7.6: Session Dashboard

**Goal:** Discord embed showing live session stats, updated periodically.

##### Dashboard embed layout

```
╔══════════════════════════════════════╗
║  Glyphoxa Session Dashboard         ║
╠══════════════════════════════════════╣
║  Campaign: Ironhold Expedition      ║
║  Duration: 1h 23m                   ║
║  Session ID: session-ironhold-...   ║
╠══════════════════════════════════════╣
║  Active NPCs                        ║
║  ● Grimjaw ............... unmuted  ║
║  ● Merchant Carla ........ muted    ║
║  ● Sage Morvaine ......... unmuted  ║
╠══════════════════════════════════════╣
║  Pipeline Latency (last 5 min)      ║
║  STT  p50: 120ms  p95: 250ms       ║
║  LLM  p50: 340ms  p95: 890ms       ║
║  TTS  p50: 180ms  p95: 310ms       ║
║  Total p50: 640ms  p95: 1.4s       ║
╠══════════════════════════════════════╣
║  Utterances: 47  │  Errors: 2       ║
║  Memory: 1,240 entries              ║
╚══════════════════════════════════════╝
```

##### `internal/discord/dashboard.go`

```go
// Dashboard maintains a periodically-updated Discord embed message
// showing live session metrics.
type Dashboard struct {
    session   *discordgo.Session
    channelID string
    messageID string // created on first update, edited thereafter
    interval  time.Duration

    // Data sources
    sessionMgr *app.SessionManager
    metrics    *observe.Metrics
}

// Start begins the periodic update loop. The dashboard message is created
// on the first tick and edited on subsequent ticks.
func (d *Dashboard) Start(ctx context.Context)

// Stop halts updates and optionally posts a final "session ended" embed.
func (d *Dashboard) Stop(ctx context.Context)
```

##### Update frequency

- Default interval: **10 seconds**
- Discord rate limit: ~5 edits per 5 seconds per channel — 10s interval is well within limits
- Dashboard is created when `/session start` succeeds and torn down when `/session stop` is called

##### Metrics data sources

| Dashboard field | Source |
|---|---|
| Active NPCs + mute state | `SessionManager.Orchestrator().ActiveAgents()` + `IsMuted()` |
| Campaign name, session ID | `SessionManager.SessionInfo()` |
| Duration | `time.Since(SessionInfo.StartedAt)` |
| Pipeline latency | `observe.Metrics` histograms (STT, LLM, TTS) — read via OTel SDK |
| Utterance count | `observe.Metrics.NPCUtterances` counter |
| Error count | `observe.Metrics.ProviderErrors` counter |
| Memory entry count | `SessionStore.EntryCount(ctx, sessionID)` (new method) |

##### `/session recap` command

Separate from the dashboard. When invoked:

1. Retrieve all transcript entries from `SessionStore.GetRecent(ctx, sessionID, sessionDuration)`
2. Pass to `session.Summariser.Summarise(ctx, entries)` for LLM-powered summarization
3. Respond with the summary as a Discord embed
4. If summary exceeds Discord embed limits (4096 chars), split across multiple embeds

##### Tasks

- [x] `internal/discord/dashboard.go` — `Dashboard` struct with periodic update loop
- [x] `internal/discord/dashboard_embed.go` — Embed builder (integrated into dashboard.go)
- [x] `internal/discord/commands/session.go` — Add `/session recap` handler
- [x] `pkg/memory/store.go` — Add `EntryCount()` to `SessionStore` interface
- [x] `pkg/memory/postgres/session_store.go` — Implement `EntryCount()`
- [x] Tests for dashboard embed formatting and update cycle

**Success criteria:**
- Dashboard embed appears within 5 seconds of `/session start`
- Updates every 10 seconds with current NPC states and latency stats
- Dashboard is cleaned up (final "session ended" embed) on `/session stop`
- `/session recap` generates a coherent 200–500 word summary
- No Discord rate limit errors during normal operation

---

#### Phase 7.7: Closed Alpha Infrastructure

**Goal:** Set up telemetry, feedback collection, and operational infrastructure for 3–5 alpha DM sessions.

##### Telemetry dashboards

Deploy Grafana + Prometheus stack consuming the existing OpenTelemetry metrics from `internal/observe/metrics.go`:

**Pre-built Grafana dashboards:**

1. **Session Overview** — active sessions, NPC count, utterance rate, error rate
2. **Latency Breakdown** — STT/LLM/TTS/total histograms with p50/p95/p99
3. **Provider Health** — request counts, error rates, circuit breaker states
4. **Tool Usage** — tool call counts, execution latency, tier distribution

All metrics already exist in `observe.Metrics`. Grafana dashboards are JSON config files stored in `deployments/grafana/`.

##### Feedback collection

Post-session feedback via a structured Discord command:

```
/feedback
```

Opens a Discord modal with:
- Voice latency rating (1–5)
- NPC personality rating (1–5)
- Memory accuracy rating (1–5)
- DM workflow rating (1–5)
- Free-text comments
- Session duration and ID (auto-filled)

Feedback is stored in PostgreSQL (new `feedback` table) and exported to the Grafana dashboard.

##### Alpha operational setup

- **Deployment:** Docker Compose on a cloud VM (already partially defined in `deployments/compose/`)
- **One bot per guild:** Each alpha DM gets their own bot instance pointing to their Discord server
- **Consolidation interval:** Reduced to 5 minutes for alpha (from 30 minutes) to minimize data loss on crashes
- **Logging:** `log_level: debug` for alpha instances, with log shipping to a central aggregator
- **Monitoring:** Alerting on error rate spikes and provider circuit breaker trips

##### Alpha success criteria

| Metric | Target |
|---|---|
| Crash-free session rate | > 90% (at least 9 out of 10 sessions complete without process crash) |
| End-to-end latency p95 | < 1.5 seconds |
| DM workflow rating (avg) | > 3.5 / 5 |
| NPC personality rating (avg) | > 3.5 / 5 |
| Memory accuracy rating (avg) | > 3.0 / 5 |
| Session duration | At least 3 sessions lasting > 2 hours |

##### Tasks

- [x] `deployments/grafana/` — Grafana dashboard JSON files (4 dashboards)
- [x] `deployments/compose/docker-compose.yml` — Add Grafana + Prometheus services
- [x] `deployments/compose/prometheus.yml` — Scrape config for Glyphoxa metrics endpoint
- [x] `internal/discord/commands/feedback.go` — `/feedback` command with modal
- [x] `internal/feedback/store.go` — Feedback storage (PostgreSQL)
- [ ] `internal/config/config.go` — Add alpha-mode config (reduced consolidation interval)
- [ ] `docs/alpha/` — Alpha DM onboarding guide, known limitations, support contacts

**Success criteria:**
- Grafana dashboards show real-time metrics from a running session
- `/feedback` command collects and persists structured feedback
- Alpha deployment runs stable for > 4 hours in load testing
- All alpha success metric targets are met across alpha sessions

---

## Alternative Approaches Considered

### 1. HTTP API instead of Discord slash commands

**Rejected.** The DM is already in Discord during a session. Adding an HTTP API would require a separate client (browser, CLI) and split the DM's attention. Discord slash commands are native to the platform and require no additional UI.

### 2. Single monolithic `/glyphoxa` command with subcommands

**Rejected.** Discord supports command groups natively (`/npc mute`, `/entity add`). A single command with string-based subcommand parsing loses autocomplete, validation, and discoverability.

### 3. Voice-only control (no slash commands)

**Rejected.** Voice commands are imprecise and cannot handle operations that require structured input (entity creation, file imports, campaign loading). Slash commands provide the reliable control surface; voice commands are a convenience layer on top.

### 4. Hot-swap campaigns mid-session

**Rejected for alpha.** Swapping NPC agents, entities, and knowledge graphs mid-session is architecturally complex and error-prone. Requiring `/session stop` before `/campaign switch` is safer and simpler. Can be revisited post-alpha if DMs need this.

### 5. Web UI for dashboard

**Deferred to Phase 8.** Discord embeds are sufficient for alpha. A web UI adds deployment complexity (frontend framework, auth, WebSocket for real-time updates) that is not justified until alpha feedback confirms the need.

## Acceptance Criteria

### Functional Requirements

- [x] Discord bot connects to guild, registers all slash commands, and responds to interactions
- [x] Discord voice adapter implements `audio.Platform` and passes compliance tests
- [x] `/session start` connects to voice channel, loads NPCs, begins audio processing
- [x] `/session stop` consolidates conversation history and disconnects cleanly
- [x] `/session recap` generates an LLM-powered session summary
- [x] `/npc list` shows all NPCs with mute state
- [x] `/npc mute <name>` and `/npc unmute <name>` work via autocomplete
- [x] `/npc speak <name> <text>` synthesizes text in the NPC's voice
- [x] `/entity add`, `/entity list`, `/entity remove`, `/entity import` manage entities
- [x] `/campaign info`, `/campaign load`, `/campaign switch` manage campaigns
- [x] Voice commands (mute, unmute, mute all, speak) work for the DM
- [x] Session dashboard shows live metrics, updated every 10 seconds
- [x] DM role check gates all privileged commands
- [x] `/feedback` collects structured post-session feedback

### Non-Functional Requirements

- [x] All new code follows project conventions: `t.Parallel()`, table-driven tests, hand-written mocks, `sync.Mutex`, compile-time interface assertions, `slog` structured logging
- [x] `make test` passes with `-race` for all new packages
- [ ] `make lint` passes (golangci-lint) — requires full lint run
- [ ] End-to-end latency p95 < 1.5 seconds with Discord voice transport — requires live testing
- [ ] Dashboard updates do not trigger Discord rate limit errors — requires live testing
- [ ] Voice command false positive rate < 1% — requires live testing

### Quality Gates

- [x] All `audio.Platform` compliance tests pass for the Discord adapter
- [x] All new orchestrator methods have tests covering happy path, error cases, and concurrency
- [x] All slash command handlers have tests with mocked Discord interactions
- [x] Voice command filter has table-driven tests for all patterns plus adversarial inputs
- [ ] Alpha session of > 2 hours completes without crash — requires live testing

## Dependencies & Prerequisites

| Dependency | Status | Notes |
|---|---|---|
| `discordgo` library | Available | `github.com/bwmarrin/discordgo` — mature Discord API client for Go |
| Discord bot token | Requires setup | Create bot application at discord.com/developers |
| Discord guild with voice channels | Requires setup | Alpha DMs need their own servers |
| PostgreSQL with pgvector | Exists | Used by memory subsystem (Phases 1–3) |
| OpenTelemetry metrics | Exists | `internal/observe/metrics.go` |
| Prometheus + Grafana | Needs deployment | For alpha telemetry dashboards |
| Phases 1–6 completion | Done | Orchestrator, entity store, NPC store, MCP, memory, session lifecycle |

## Risk Analysis & Mitigation

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Discord API rate limiting on embed updates | Medium | Low | 10-second update interval is within limits; add exponential backoff |
| Voice command false positives | Medium | Medium | Conservative regex patterns; only process DM's audio; run against sample transcripts |
| Process crash during alpha session | Medium | High | 5-minute consolidation interval; `/session recap` works from persisted data |
| discordgo Opus audio quality issues | Low | High | Discord uses 48kHz/stereo Opus — verify encoding matches Glyphoxa's AudioFrame format early |
| DM role misconfiguration | Low | Medium | Clear error message if `dm_role_id` is not set; `/session start` fails fast |
| Alpha DM availability | Medium | Medium | Over-recruit (5 DMs for 3 needed); flexible scheduling |

## Future Considerations

- **Multi-guild support:** Alpha is single-guild per bot instance. Production will need multi-guild with isolated session state.
- **Web UI companion:** Deferred to Phase 8. Will provide richer dashboard, NPC editor, and campaign browser.
- **Voice command localization:** English-only for alpha. Future: configurable command patterns per language.
- **Campaign persistence:** Currently file-based. Future: campaign store in PostgreSQL with version history.
- **Session resume:** Currently sessions cannot be resumed after stop. Future: checkpoint and resume with full state recovery.

## Documentation Plan

- [ ] Update `docs/design/09-roadmap.md` — mark Phase 7 items as in-progress/complete
- [ ] `docs/alpha/onboarding.md` — setup guide for alpha DMs (bot invite, role config, first session)
- [ ] `docs/alpha/known-limitations.md` — documented limitations for alpha testers
- [ ] `configs/example.yaml` — add Discord config section with comments
- [ ] Godoc on all new exported types and functions

## References & Research

### Internal References

- Orchestrator: `internal/agent/orchestrator/orchestrator.go` — existing mute/puppet/route methods
- Entity store: `internal/entity/store.go` — CRUD + BulkImport interface
- NPC store: `internal/agent/npcstore/store.go` — persistent NPC definitions
- App wiring: `internal/app/app.go` — current monolithic lifecycle
- Audio platform: `pkg/audio/platform.go` — `Platform` and `Connection` interfaces
- Config schema: `internal/config/config.go` — YAML config structure
- Observability: `internal/observe/metrics.go` — OTel metrics instruments
- Session lifecycle: `internal/session/consolidator.go` — periodic conversation flush
- Roadmap: `docs/design/09-roadmap.md` — Phase 7 specification (lines 309–342)

### External References

- discordgo library: `github.com/bwmarrin/discordgo`
- Discord API slash commands: developer documentation for application commands
- Discord voice protocol: Opus encoding, WebSocket voice gateway
- OpenTelemetry Go SDK: metrics read API for dashboard data

---

*Generated with Claude Code on 2026-02-28*
