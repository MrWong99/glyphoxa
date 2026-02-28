---
nav_order: 5
---

# 🎭 NPC Agents, Entities & Campaigns

NPC agents are the core of the Glyphoxa experience. Each NPC is an autonomous AI character with its own personality, voice, knowledge, and behaviour rules. During a live TTRPG session, the orchestrator listens for player speech, routes it to the appropriate NPC, and streams a voiced response back to the voice channel -- all in real time.

This document covers how to define NPCs, orchestrate multiple characters simultaneously, manage game-world entities, load campaigns, import data from VTT platforms, and how the system builds prompt context before every LLM call.

---

## 🧩 Overview

An NPC agent is a Go struct implementing the `NPCAgent` interface (`internal/agent/agent.go`). Each agent:

1. **Owns a voice engine** -- either a cascaded STT-LLM-TTS pipeline, a speech-to-speech model, or the experimental sentence cascade.
2. **Receives player utterances** routed by the orchestrator's address detection.
3. **Assembles hot context** (recent transcript, scene data, entity knowledge) before every LLM call.
4. **Produces voiced responses** streamed to the audio mixer for playback.
5. **Records exchanges** in conversation history for cross-NPC awareness and memory persistence.

```
Player speaks
    │
    ▼
┌──────────────────────┐
│  Orchestrator/Router │  ◄── address detection, mute checks
├──────────────────────┤
│    NPC Agent #N      │
│  ┌────────────────┐  │
│  │ Hot Context    │  │  ◄── assembler + formatter + prefetch
│  │ Assembly       │  │
│  ├────────────────┤  │
│  │ Voice Engine   │  │  ◄── cascaded | s2s | sentence_cascade
│  ├────────────────┤  │
│  │ Audio Mixer    │  │  ◄── enqueue response for playback
│  └────────────────┘  │
└──────────────────────┘
```

Key interfaces and types:

| Type | Package | Description |
|------|---------|-------------|
| `NPCAgent` | `internal/agent` | Main agent interface: `HandleUtterance`, `UpdateScene`, `SpeakText` |
| `NPCIdentity` | `internal/agent` | Static persona: name, personality, voice profile, knowledge scope, behaviour rules |
| `SceneContext` | `internal/agent` | Current location, time of day, present entities, active quests |
| `Router` | `internal/agent` | Routes utterances to the correct NPC; supports mute/unmute |
| `Orchestrator` | `internal/agent/orchestrator` | Concrete `Router` with address detection, cross-NPC awareness, DM override |
| `NPCDefinition` | `internal/agent/npcstore` | Persistent NPC definition for database storage |
| `Store` | `internal/agent/npcstore` | CRUD + list + upsert for NPC definitions (PostgreSQL-backed) |

---

## 📝 Defining an NPC

NPCs are defined in the `npcs` section of your Glyphoxa config file. Each entry maps to the `NPCConfig` struct in `internal/config/config.go`.

### Configuration Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `name` | `string` | *required* | In-world display name (e.g., `"Greymantle the Sage"`) |
| `personality` | `string` | `""` | Free-text persona injected into the LLM system prompt |
| `voice` | `VoiceConfig` | -- | TTS voice profile (provider, voice_id, pitch, speed) |
| `engine` | `string` | `"cascaded"` | Voice engine: `"cascaded"`, `"s2s"`, or `"sentence_cascade"` |
| `knowledge_scope` | `[]string` | `[]` | Topic domains the NPC is knowledgeable about |
| `tools` | `[]string` | `[]` | MCP tool names the NPC may invoke |
| `budget_tier` | `string` | `"fast"` | Tool latency budget: `"fast"`, `"standard"`, or `"deep"` |
| `cascade_mode` | `string` | `"off"` | Sentence cascade mode: `"off"`, `"auto"`, or `"always"` |
| `cascade` | `CascadeConfig` | `nil` | Sentence cascade settings (fast_model, strong_model, opener_instruction) |

### Voice Config

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `provider` | `string` | -- | TTS provider name (e.g., `"elevenlabs"`, `"google"`) |
| `voice_id` | `string` | -- | Provider-specific voice identifier |
| `pitch_shift` | `float64` | `0` | Pitch adjustment in semitones, range `[-10, +10]` |
| `speed_factor` | `float64` | `1.0` | Speaking rate, range `[0.5, 2.0]` |

### Annotated YAML Example

```yaml
npcs:
  # ── A gruff dwarven blacksmith ──────────────────────────────────────────
  - name: "Grimjaw the Blacksmith"
    personality: |
      You are Grimjaw, a 147-year-old dwarf blacksmith in the town of Ironhold.
      You are gruff but kind. You speak in short, clipped sentences and avoid
      eye contact when lying. You have a thick Scottish accent and pepper your
      speech with dwarven expressions.
    voice:
      provider: elevenlabs         # TTS provider
      voice_id: "pNInz6obpgDQGcFmaJgB"  # ElevenLabs "Adam" voice
      pitch_shift: -2              # slightly deeper
      speed_factor: 0.9            # slightly slower delivery
    engine: cascaded               # STT → LLM → TTS pipeline
    knowledge_scope:
      - ironhold                   # knows about Ironhold and its surroundings
      - blacksmithing              # expert in weapons and armour
      - missing_shipment_quest     # involved in the missing shipment quest
    tools:
      - dice-roller                # can roll dice for crafting checks
      - memory.*                   # can read/write session memory
    budget_tier: standard          # allows moderate-latency tools

  # ── A mysterious elven sage using speech-to-speech ──────────────────────
  - name: "Greymantle the Sage"
    personality: |
      You are Greymantle, an ancient elven sage who speaks in riddles.
      You know much but reveal little. Your voice is soft and measured.
    voice:
      provider: google
      voice_id: "en-GB-Wavenet-B"
      pitch_shift: 0
      speed_factor: 1.0
    engine: s2s                    # end-to-end speech model
    knowledge_scope:
      - ancient_history
      - arcane_lore
      - prophecies
    tools:
      - rules-lookup               # can look up game rules
    budget_tier: fast              # only low-latency tools

  # ── Experimental sentence cascade NPC ───────────────────────────────────
  - name: "Barkley the Tavern Keep"
    personality: |
      Jovial halfling tavern keeper. Talks too much. Always tries to sell
      you his "famous" stew.
    voice:
      provider: elevenlabs
      voice_id: "VR6AewLTigWG4xSOukaG"
      speed_factor: 1.2            # fast talker
    engine: sentence_cascade       # dual-model cascade
    cascade_mode: always
    cascade:
      fast_model: "gpt-4o-mini"   # quick opener sentence
      strong_model: "gpt-4o"      # substantive continuation
    knowledge_scope:
      - tavern
      - local_gossip
    tools: []                      # no tools needed
    budget_tier: fast
```

### Engine Types

| Engine | Value | Description |
|--------|-------|-------------|
| Cascaded | `"cascaded"` | Traditional STT → LLM → TTS pipeline. Most flexible, supports all tools. |
| Speech-to-Speech | `"s2s"` | End-to-end speech model (OpenAI Realtime, Gemini Live). Lowest latency. |
| Sentence Cascade | `"sentence_cascade"` | Experimental: fast model generates an opener, strong model continues. Perceived <600ms voice onset. |

### Budget Tiers

Budget tiers control which MCP tools the LLM is offered, based on acceptable latency:

| Tier | Value | Behaviour |
|------|-------|-----------|
| Fast | `"fast"` | Only low-latency tools (< 200ms). Default. |
| Standard | `"standard"` | Moderate-latency tools included (< 2s). |
| Deep | `"deep"` | All tools available, including high-latency ones. |

---

## 🎙️ Multi-NPC Orchestration

The `Orchestrator` (`internal/agent/orchestrator/orchestrator.go`) implements the `Router` interface and manages all active NPC agents within a session. It handles address detection, cross-NPC awareness, turn management, and DM overrides.

### Address Detection

When a player speaks, the orchestrator must determine which NPC was addressed. The `AddressDetector` (`internal/agent/orchestrator/address.go`) applies a priority chain of heuristics:

| Priority | Strategy | Description |
|----------|----------|-------------|
| 1 | **Explicit name match** | Scans the transcript for NPC names and name fragments (case-insensitive). Longer, more specific names match first. |
| 2 | **DM puppet override** | If the DM has activated puppet mode for this speaker, routes to the puppet target. |
| 3 | **Last-speaker continuation** | Routes to whichever NPC spoke most recently (conversational continuity). |
| 4 | **Single-NPC fallback** | If exactly one unmuted NPC exists, routes there automatically. |
| 5 | **No match** | Returns `ErrNoTarget`; the utterance is not dispatched. |

**Name indexing:** The detector builds a lowercase index of every NPC's full name plus individual words of 3+ characters. For example, `"Grimjaw the Blacksmith"` produces index entries for `"grimjaw the blacksmith"`, `"grimjaw"`, and `"blacksmith"` (the word `"the"` is too short). The index is pre-sorted by descending key length so that more specific names always match first.

### Cross-NPC Awareness

NPCs in the same scene share a recent-utterance buffer (see [Utterance Buffer](#-utterance-buffer) below). Before dispatching an utterance to the target NPC, the orchestrator:

1. Snapshots the buffer (excluding the target NPC's own entries).
2. Injects the cross-NPC utterances into the target agent's engine via `InjectContext`.
3. The NPC can now reference what other characters have said.

### Turn-Taking and Mute Control

The orchestrator provides granular control over which NPCs can speak:

| Method | Description |
|--------|-------------|
| `MuteAgent(id)` | Silences a specific NPC -- utterances routed to it are dropped. |
| `UnmuteAgent(id)` | Re-enables an NPC for routing. |
| `MuteAll()` | Atomically mutes all NPCs. Returns count of state changes. |
| `UnmuteAll()` | Atomically unmutes all NPCs. Returns count of state changes. |
| `IsMuted(id)` | Checks whether an NPC is currently muted. |

### DM Override / Puppet Mode

The DM can take direct control of any NPC:

- **`SetPuppet(speaker, npcID)`** -- Forces all utterances from `speaker` to route to the specified NPC, bypassing address detection. Used for DM puppeteering via `/npc speak`.
- **`SetPuppet(speaker, "")`** -- Clears the override, restoring normal address detection.
- **`SpeakText(text)`** -- Synthesises pre-written text in the NPC's voice without running it through the LLM. Used by the `/npc speak` command.

### Agent Lifecycle

| Method | Description |
|--------|-------------|
| `AddAgent(agent)` | Registers a new NPC at runtime. Rebuilds the address detector's name index. |
| `RemoveAgent(id)` | Unregisters an NPC. Clears last-speaker and puppet overrides pointing to it. |
| `AgentByName(name)` | Case-insensitive name lookup across all registered agents. |
| `BroadcastScene(scene)` | Pushes a scene update to all unmuted NPCs simultaneously. |

---

## 🗂️ Entity Management

Entities represent everything in the game world that NPCs might know about. The `entity` package (`internal/entity/`) provides a CRUD layer for defining entities before they are loaded into the knowledge graph at session start.

### Entity Types

| Type | Constant | Description |
|------|----------|-------------|
| NPC | `"npc"` | Non-player characters |
| Location | `"location"` | Places in the game world |
| Item | `"item"` | Physical objects or artifacts |
| Faction | `"faction"` | Organisations, guilds, or factions |
| Quest | `"quest"` | Quests, missions, or story hooks |
| Lore | `"lore"` | Historical records, journal entries, world lore |

### Entity Definition Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `id` | `string` | No | Unique identifier. Auto-generated (32-char hex) if empty. |
| `name` | `string` | Yes | Entity display name. |
| `type` | `EntityType` | Yes | One of the entity types above. |
| `description` | `string` | No | Free-text description. |
| `properties` | `map[string]string` | No | Arbitrary key-value metadata. |
| `relationships` | `[]RelationshipDef` | No | Connections to other entities. |
| `tags` | `[]string` | No | Searchable labels for categorisation. |
| `visibility` | `[]string` | No | Which NPC IDs can see this entity. Empty means visible to all. |

### Relationships

Relationships define connections between entities:

| Field | Type | Description |
|-------|------|-------------|
| `target_id` | `string` | ID of the related entity. |
| `target_name` | `string` | Alternative to `target_id` -- resolved by name during import. |
| `type` | `string` | Relationship label (e.g., `"lives_in"`, `"owns"`, `"allied_with"`). |
| `bidirectional` | `bool` | Whether the relationship applies in both directions. |

### YAML Format

Entities are defined in campaign YAML files:

```yaml
campaign:
  name: "The Lost Mine of Phandelver"
  system: "dnd5e"
  description: "A classic starter adventure."

entities:
  - name: "Gundren Rockseeker"
    type: npc
    description: "A dwarf merchant hiring adventurers to escort a wagon."
    properties:
      race: dwarf
      occupation: merchant
    relationships:
      - target_name: "Phandalin"
        type: lives_in
      - target_name: "Sildar Hallwinter"
        type: allied_with
        bidirectional: true
    tags: [quest_giver, wave_echo_cave]

  - name: "Phandalin"
    type: location
    description: "A small frontier town nestled in the foothills."
    properties:
      region: Sword Coast
      population: "~400"
    tags: [town, sword_coast]

  - name: "Sildar Hallwinter"
    type: npc
    description: "A human warrior and agent of the Lords' Alliance."
    properties:
      race: human
      occupation: warrior
    tags: [lords_alliance]

  - name: "Glass Staff"
    type: item
    description: "A staff of glass that functions as a staff of defense."
    tags: [magical, weapon]

  - name: "Redbrands"
    type: faction
    description: "A gang of ruffians terrorising Phandalin."
    properties:
      leader: "Glasstaff"
      alignment: "lawful evil"
    tags: [antagonist]

  - name: "Wave Echo Cave"
    type: quest
    description: "Find the lost mine of the Phandelver Pact."
    properties:
      status: active
      difficulty: "5"
    tags: [main_quest]
    visibility: []  # visible to all NPCs
```

### CRUD Operations

The `entity.Store` interface provides:

| Operation | Method | Description |
|-----------|--------|-------------|
| Create | `Add(ctx, entity)` | Creates a new entity. Auto-generates ID if empty. Returns `ErrDuplicateID` on conflict. |
| Read | `Get(ctx, id)` | Retrieves a single entity by ID. Returns `ErrNotFound` if missing. |
| List | `List(ctx, opts)` | Lists entities with optional type and tag filters. |
| Update | `Update(ctx, entity)` | Replaces an existing entity. Returns `ErrNotFound` if missing. |
| Delete | `Remove(ctx, id)` | Deletes an entity by ID. Returns `ErrNotFound` if missing. |
| Bulk Import | `BulkImport(ctx, entities)` | Imports multiple entities atomically. Returns count + first error. |

Filtering with `ListOptions`:

```go
// List all NPCs
npcs, err := store.List(ctx, entity.ListOptions{Type: entity.EntityNPC})

// List all entities tagged "foundry"
foundryEntities, err := store.List(ctx, entity.ListOptions{Tags: []string{"foundry"}})
```

The built-in `MemStore` (`internal/entity/memstore.go`) provides a thread-safe, in-memory implementation suitable for single-session use and testing.

### Validation

The `Validate()` function enforces:
- `Name` must be non-empty.
- `Type` must be a recognised `EntityType`.
- Every `RelationshipDef` must have a non-empty `Type`.

---

## 🏕️ Campaign Management

Campaigns group NPCs and entities together under a named game session. The `CampaignConfig` struct in `internal/config/config.go` ties everything together.

### Campaign Config Structure

```yaml
campaign:
  name: "Curse of Strahd"           # human-readable campaign name
  system: "dnd5e"                    # game system identifier
  entity_files:                      # YAML entity files loaded at startup
    - entities/barovia.yaml
    - entities/npcs.yaml
    - entities/items.yaml
  vtt_imports:                       # VTT exports imported at startup
    - path: exports/foundry-world.json
      format: foundry
    - path: exports/roll20-campaign.json
      format: roll20
```

| Field | Type | Description |
|-------|------|-------------|
| `name` | `string` | Campaign display name (e.g., `"Curse of Strahd"`). |
| `system` | `string` | Game system identifier (e.g., `"dnd5e"`, `"pf2e"`). |
| `entity_files` | `[]string` | Paths to YAML entity files. Resolved relative to the main config file's directory. |
| `vtt_imports` | `[]VTTImportConfig` | VTT export files to import at startup. |

### Loading Campaigns

At startup, Glyphoxa processes the campaign config in order:

1. **Entity files** -- Each path in `entity_files` is parsed as a `CampaignFile` YAML document and bulk-imported into the entity store via `ImportCampaign()`.
2. **VTT imports** -- Each entry in `vtt_imports` is imported using the format-appropriate importer (`ImportFoundryVTT` or `ImportRoll20`).
3. **NPC definitions** -- The `npcs` array in the root config is loaded into the NPC store.
4. **Knowledge graph sync** -- Imported entities are loaded into the knowledge graph for runtime retrieval.

### Switching Campaigns

To switch campaigns, update the `campaign` section of your config file and restart the server. Hot-reloading of campaign data is not currently supported -- a restart ensures a clean knowledge graph state.

---

## 🗺️ VTT Import

Glyphoxa can import game entities from two popular virtual tabletop platforms. The import functions live in `internal/entity/vttimport.go`.

### Supported Formats

| Platform | Format Value | Input Format | Entities Extracted |
|----------|-------------|--------------|-------------------|
| Foundry VTT | `"foundry"` | JSON (world export) | Actors -> NPC, Items -> Item, Journal entries -> Lore |
| Roll20 | `"roll20"` | JSON (campaign export) | Characters -> NPC, Handouts -> Lore |

### Foundry VTT Import

Foundry VTT world exports contain `actors`, `items`, and `journal` arrays. The importer maps them as follows:

| Foundry Field | Entity Type | Properties Extracted |
|---------------|-------------|---------------------|
| `actors[]` | `npc` | `actor_type`, `img` |
| `items[]` | `item` | `item_type`, `img` |
| `journal[]` | `lore` | Content from inline text or first page (HTML stripped) |

Imported entities are tagged automatically: `["foundry", "actor"]`, `["foundry", "item"]`, or `["foundry", "journal"]`.

### Roll20 Import

Roll20 campaign exports contain `characters` and `handouts` arrays:

| Roll20 Field | Entity Type | Properties Extracted |
|--------------|-------------|---------------------|
| `characters[]` | `npc` | All character attributes (name/current pairs) |
| `handouts[]` | `lore` | Notes text (HTML stripped), falls back to GM notes |

Imported entities are tagged: `["roll20", "character"]` or `["roll20", "handout"]`.

### Import Workflow

**Via config (recommended):**

```yaml
campaign:
  name: "My Campaign"
  system: "dnd5e"
  vtt_imports:
    - path: exports/foundry-world.json
      format: foundry
    - path: exports/roll20-campaign.json
      format: roll20
```

**Via code:**

```go
f, _ := os.Open("foundry-world.json")
defer f.Close()

n, err := entity.ImportFoundryVTT(ctx, store, f)
// n = number of entities imported
```

Both importers are best-effort: if a single entity fails to store, the error is returned along with the count of entities imported so far. Unknown JSON fields are silently ignored.

---

## 🧠 Hot Context Assembly

Before every LLM call, the NPC's prompt must include up-to-date context about the conversation, scene, and world. The `hotctx` package (`internal/hotctx/`) handles this with three components: the **assembler**, the **formatter**, and the **prefetcher**.

### Assembler

The `Assembler` (`internal/hotctx/assembler.go`) concurrently fetches three pieces of context and combines them into a `HotContext`:

```
            ┌──────────────────────────┐
            │    Assembler.Assemble()  │
            └────────┬─────────────────┘
        ┌────────────┼────────────────┐
        ▼            ▼                ▼
 ┌─────────────┐ ┌──────────┐ ┌──────────────┐
 │  Identity   │ │ Recent   │ │   Scene      │
 │  Snapshot   │ │Transcript│ │  Context     │
 │  (L3 graph) │ │(L1 store)│ │(graph rels)  │
 └─────────────┘ └──────────┘ └──────────────┘
```

| Component | Source | Description |
|-----------|--------|-------------|
| **Identity snapshot** | Knowledge graph (L3) | NPC's entity node with attributes and relationships. |
| **Recent transcript** | Session store (L1) | Last N minutes of conversation (default: 5 min, max 50 entries). |
| **Scene context** | Knowledge graph relationships | Current location (via `LOCATED_AT`), present entities (1-hop neighbours), active quests (via `QUEST_GIVER`/`PARTICIPATED_IN`). |

All three fetches run in parallel via `errgroup`. Target assembly latency is **< 50ms**.

Configuration options:

```go
assembler := hotctx.NewAssembler(sessionStore, graph,
    hotctx.WithRecentDuration(5 * time.Minute),  // how far back to look
    hotctx.WithMaxTranscriptEntries(50),          // cap on transcript entries
)
```

### Formatter

The `FormatSystemPrompt()` function (`internal/hotctx/formatter.go`) is a pure function that converts a `HotContext` into a structured system prompt string. It renders the following sections, omitting any that are empty:

1. **Opening line** -- `"You are <NPC name>. <personality>"`.
2. **Identity section** -- Entity name, type, well-known attributes (occupation, appearance, speaking style, personality, alignment), then any extra attributes.
3. **Relationships section** -- Human-readable list: `"You know <name> (a <type>). Your relationship: <rel_type>"`.
4. **Scene section** -- Current location (with description), entities also present, active quests (with status).
5. **Recent conversation** -- Transcript entries with relative timestamps: `[2m ago] Player: "Hello, Grimjaw"`.

### PreFetcher

The `PreFetcher` (`internal/hotctx/prefetch.go`) speculatively pre-loads entity data from the knowledge graph based on partial STT transcripts. This eliminates cold-layer round-trips during prompt assembly.

**How it works:**

1. At session start, `RefreshEntityList()` loads all entity names from the knowledge graph and builds a lowercase name-to-ID index (including individual words of 4+ characters for partial matching).
2. As partial STT transcripts stream in, `ProcessPartial(partial)` scans the text for known entity names.
3. Matched entities are fetched from the graph and cached in memory.
4. By the time the assembler runs, the relevant entities are already warm.

```go
prefetcher := hotctx.NewPreFetcher(graph)
prefetcher.RefreshEntityList(ctx)

// Called on each STT partial result:
newEntities := prefetcher.ProcessPartial(ctx, "I want to talk to Grimjaw")
// newEntities contains the Grimjaw entity, now cached

// Reset at the start of each new voice turn:
prefetcher.Reset()
```

Pre-fetch errors are silently swallowed so that a transient graph failure never blocks the real-time voice path.

---

## 📢 Utterance Buffer

The `UtteranceBuffer` (`internal/agent/orchestrator/utterance_buffer.go`) maintains a shared buffer of recent utterances from all NPCs and players. It serves two purposes:

1. **Cross-NPC awareness** -- Each NPC can see what other NPCs and players have said recently, enabling natural multi-character conversations.
2. **Conversational context** -- The orchestrator injects buffer contents into the target NPC's engine before each utterance is processed.

### Buffer Behaviour

| Property | Default | Description |
|----------|---------|-------------|
| Max entries | 20 | Maximum number of utterances retained. |
| Max age | 5 minutes | Entries older than this are evicted. |

Each `BufferEntry` contains:

| Field | Description |
|-------|-------------|
| `SpeakerID` | Player user-ID or NPC agent ID. |
| `SpeakerName` | Human-readable speaker name. |
| `Text` | The utterance text. |
| `NPCID` | Non-empty when the utterance was produced by an NPC. |
| `Timestamp` | When the utterance occurred. |

### Eviction

Eviction runs on every `Add()` call:

1. **By age** -- Entries older than `maxAge` are removed.
2. **By size** -- If the buffer still exceeds `maxSize`, the oldest entries are trimmed.
3. **Memory safety** -- Surviving entries are copied to a fresh backing array so evicted entries can be garbage collected.

### NPC-Specific Filtering

When an NPC reads the buffer, its own entries are excluded via `Recent(excludeNPCID, maxEntries)`. This prevents the NPC from treating its own previous responses as "cross-NPC context" while still seeing everything that other NPCs and players have said.

### Configuration

```go
orchestrator.New(agents,
    orchestrator.WithBufferSize(30),                    // retain more entries
    orchestrator.WithBufferDuration(10 * time.Minute),  // longer memory window
)
```

---

## 🔗 NPC Persistence

The `npcstore` package (`internal/agent/npcstore/`) provides persistent storage for NPC definitions in PostgreSQL with JSONB columns for structured sub-fields.

### Database Schema

```sql
CREATE TABLE npc_definitions (
    id               TEXT PRIMARY KEY,
    campaign_id      TEXT NOT NULL DEFAULT '',
    name             TEXT NOT NULL,
    personality      TEXT NOT NULL DEFAULT '',
    engine           TEXT NOT NULL DEFAULT 'cascaded',
    voice            JSONB NOT NULL DEFAULT '{}',
    knowledge_scope  JSONB NOT NULL DEFAULT '[]',
    secret_knowledge JSONB NOT NULL DEFAULT '[]',
    behavior_rules   JSONB NOT NULL DEFAULT '[]',
    tools            JSONB NOT NULL DEFAULT '[]',
    budget_tier      TEXT NOT NULL DEFAULT 'fast',
    attributes       JSONB NOT NULL DEFAULT '{}',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

### Store Operations

| Operation | Method | Description |
|-----------|--------|-------------|
| Create | `Create(ctx, def)` | Insert new NPC. Validates first. Returns error on duplicate ID. |
| Read | `Get(ctx, id)` | Retrieve by ID. Returns `(nil, nil)` if not found. |
| Update | `Update(ctx, def)` | Replace existing NPC. Validates first. Returns error if not found. |
| Delete | `Delete(ctx, id)` | Remove by ID. Deleting a non-existent NPC is not an error. |
| List | `List(ctx, campaignID)` | List all NPCs, optionally filtered by campaign. Ordered by name. |
| Upsert | `Upsert(ctx, def)` | Create or replace (useful for YAML import). |

The `ToIdentity()` helper converts an `NPCDefinition` into the runtime `agent.NPCIdentity` type used by the orchestrator.

---

## See also

- [configuration.md](configuration.md) -- Complete configuration reference
- [memory.md](memory.md) -- Three-layer memory system and knowledge graph
- [mcp-tools.md](mcp-tools.md) -- MCP tool system, budget tiers, building custom tools
- [commands.md](commands.md) -- Discord slash commands including `/npc`, `/entity`, `/campaign`
- [design/06-npc-agents.md](design/06-npc-agents.md) -- Design rationale for the NPC agent architecture
