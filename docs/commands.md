# Discord Command Reference

Glyphoxa provides two types of commands for controlling NPC agents during a live TTRPG session:

- **Slash commands** -- typed into Discord's chat bar (e.g., `/session start`). These are registered as Discord Application Commands with autocomplete, modals, and permission checks.
- **Voice commands** -- spoken aloud by the DM into the voice channel. The voice command filter intercepts the DM's speech-to-text output before it reaches the NPC routing pipeline, executing orchestrator actions in real time.

All slash commands that modify session state or NPC behaviour require the **DM role** (configured via `dm_role_id` in the bot config). If `dm_role_id` is left empty, all users are treated as DMs -- useful during development.

---

## Table of Contents

- [Slash Commands](#-slash-commands)
  - [/session](#session)
  - [/npc](#npc)
  - [/entity](#entity)
  - [/campaign](#campaign)
  - [/feedback](#feedback)
- [Voice Commands](#-voice-commands)
- [Puppet Mode](#-puppet-mode)
- [Dashboard](#-dashboard)
- [See Also](#-see-also)

---

## :zap: Slash Commands

### `/session`

Manage voice sessions. The DM starts a session to connect Glyphoxa to a voice channel, enabling NPC agents to listen and speak.

#### `/session start`

Start a voice session in the DM's current voice channel.

```
/session start
```

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| *(none)* | -- | -- | The bot joins whichever voice channel you are currently in. |

**Permissions:** DM role required.

**Behaviour:**
- The DM must be in a voice channel when invoking this command.
- If a session is already active, the command fails with the existing session ID.
- On success, the bot joins the voice channel and responds with the session ID, campaign name, and channel link.

**Example output:**
```
Session started!
Session ID: abc123-def456
Campaign: Lost Mines of Phandelver
Channel: #tavern-voice
```

---

#### `/session stop`

Stop the active voice session.

```
/session stop
```

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| *(none)* | -- | -- | -- |

**Permissions:** DM role required.

**Behaviour:**
- If no session is active, the command fails with a message.
- On success, the bot disconnects from the voice channel and reports the session ID and total duration.

**Example output:**
```
Session abc123-def456 stopped.
Duration: 1h23m45s
```

---

#### `/session recap`

Show a recap of the current or most recent session, including an AI-generated summary of the transcript.

```
/session recap
```

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| *(none)* | -- | -- | -- |

**Permissions:** DM role required.

**Behaviour:**
- If no session data is available, prompts you to start a session first.
- Retrieves the session transcript and, if an LLM summariser is configured, generates a narrative summary. Falls back to a raw chronological transcript if summarisation is unavailable or fails.
- Responds with a rich embed containing:
  - Campaign name, session ID, status (Active / Ended)
  - Who started the session, duration, voice channel
  - List of NPCs (with mute state)
  - Transcript entry count
  - AI-generated summary or raw transcript

**Example output (embed):**
```
Session Recap
Campaign: Lost Mines     Status: Active     Session ID: abc123
Started By: @DungeonMaster   Duration: 45m30s   Channel: #tavern
NPCs:
- Grimjaw
- Elara (muted)
Transcript Entries: 127

[AI-generated narrative summary of the session...]
```

---

### `/npc`

Manage NPC agents during an active session. All subcommands require an active session and the DM role.

#### `/npc list`

List all NPCs with their current mute status.

```
/npc list
```

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| *(none)* | -- | -- | -- |

**Permissions:** DM role required.

**Behaviour:**
- Returns a rich embed listing each NPC with a speaker icon (unmuted) or muted icon, their display name, and internal ID.

**Example output (embed):**
```
NPC Agents
:speaker: Grimjaw (ID: grimjaw-001)
:mute: Elara (ID: elara-002)
```

---

#### `/npc mute`

Mute a specific NPC. A muted NPC stops responding to player speech.

```
/npc mute name:<npc_name>
```

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `name` | String | Yes | NPC name (autocomplete-enabled). |

**Permissions:** DM role required.

**Behaviour:**
- Uses autocomplete to suggest active NPC names as you type.
- If the NPC is not found, responds with an error.
- On success, confirms the NPC has been muted.

---

#### `/npc unmute`

Unmute a previously muted NPC, allowing it to respond to player speech again.

```
/npc unmute name:<npc_name>
```

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `name` | String | Yes | NPC name (autocomplete-enabled). |

**Permissions:** DM role required.

---

#### `/npc speak`

Make an NPC speak pre-written text in its own TTS voice, bypassing the LLM entirely. This is the **puppet mode** slash command variant -- the DM writes the words, and the NPC's voice says them.

```
/npc speak name:<npc_name> text:<text>
```

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `name` | String | Yes | NPC name (autocomplete-enabled). |
| `text` | String | Yes | The exact text for the NPC to speak aloud. |

**Permissions:** DM role required.

**Behaviour:**
- The text is synthesised using the NPC's configured TTS voice and enqueued in the audio mixer.
- A transcript entry is recorded with the NPC as the speaker.
- The LLM is **not** involved -- the NPC says exactly what you typed.

**Example:**
```
/npc speak name:Grimjaw text:Welcome to my tavern, adventurers!
```
**Response:**
```
Grimjaw is speaking: "Welcome to my tavern, adventurers!"
```

---

#### `/npc muteall`

Mute all NPCs at once.

```
/npc muteall
```

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| *(none)* | -- | -- | -- |

**Permissions:** DM role required.

**Behaviour:**
- Mutes every active NPC and reports the count.
- Useful for cutting off all NPC chatter during narration or a dramatic moment.

**Example output:**
```
Muted 4 NPC(s).
```

---

#### `/npc unmuteall`

Unmute all NPCs at once.

```
/npc unmuteall
```

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| *(none)* | -- | -- | -- |

**Permissions:** DM role required.

**Example output:**
```
Unmuted 4 NPC(s).
```

---

### `/entity`

Manage campaign entities (NPCs, locations, items, factions, quests, lore). Entities are the persistent world-knowledge that NPC agents draw upon during conversations.

#### `/entity add`

Add a new entity via a Discord modal form.

```
/entity add
```

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| *(none -- opens a modal)* | -- | -- | -- |

**Permissions:** DM role required.

**Behaviour:**
- Opens a modal with four fields:

| Modal Field | Required | Max Length | Description |
|-------------|----------|------------|-------------|
| Name | Yes | 100 | Entity name (e.g., "Gundren Rockseeker"). |
| Type | Yes | 20 | One of: `npc`, `location`, `item`, `faction`, `quest`, `lore`. |
| Description | No | 2000 | Free-text description of the entity. |
| Tags | No | 200 | Comma-separated tags (e.g., "ally, phandalin, quest-giver"). |

- On submit, validates the entity type and creates the entity in the store.

**Example output:**
```
Entity created!
Name: Gundren Rockseeker
Type: npc
ID: ent_abc123
```

---

#### `/entity list`

List all entities, optionally filtered by type.

```
/entity list [type:<entity_type>]
```

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `type` | String (choice) | No | Filter by entity type. Choices: `NPC`, `Location`, `Item`, `Faction`, `Quest`, `Lore`. |

**Permissions:** DM role required.

**Behaviour:**
- Returns a rich embed with up to 25 entities (Discord embed field limit).
- Each entity shows its name, type, truncated description, and tags.
- The footer shows the total entity count.

---

#### `/entity remove`

Remove an entity by name, with a confirmation prompt.

```
/entity remove name:<entity_name>
```

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `name` | String | Yes | Entity name (autocomplete-enabled, searches across all types). |

**Permissions:** DM role required.

**Behaviour:**
- Autocomplete suggests entity names as you type, showing the type in parentheses.
- After selecting an entity, a confirmation embed appears with **Cancel** and **Confirm Remove** buttons.
- Removal is permanent and cannot be undone.

---

#### `/entity import`

Import entities from a YAML or JSON file attachment.

```
/entity import
```

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| *(file attachment)* | Attachment | Yes | Attach a `.yaml`, `.yml`, or `.json` file to the command message. |

**Permissions:** DM role required.

**Behaviour:**
- Maximum file size: 10 MB.
- YAML files are parsed as Glyphoxa campaign format (via `entity.LoadCampaignFromReader`).
- JSON files are parsed as Foundry VTT export format (via `entity.ImportFoundryVTT`).
- Reports the number of entities successfully imported.

**Example output:**
```
Import complete. 42 entities imported from campaign.yaml.
```

---

### `/campaign`

Manage the active campaign configuration.

#### `/campaign info`

Display metadata about the currently loaded campaign.

```
/campaign info
```

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| *(none)* | -- | -- | -- |

**Permissions:** DM role required.

**Behaviour:**
- Shows an embed with: campaign name, game system, total entity count, and a breakdown of entities by type (NPC, Location, Item, Faction, Quest, Lore).

**Example output (embed):**
```
Campaign Info
Name: Lost Mines     System: D&D 5e     Total Entities: 42
Entity Breakdown:
npc: 12
location: 8
item: 15
faction: 3
quest: 4
```

---

#### `/campaign load`

Load a campaign from a YAML file attachment. Requires no active session.

```
/campaign load
```

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| *(file attachment)* | Attachment | Yes | Attach a campaign YAML file (`.yaml` or `.yml`). |

**Permissions:** DM role required.

**Behaviour:**
- If a session is currently active, the command fails -- stop the session first with `/session stop`.
- Only YAML format is accepted for campaign files (not JSON).
- Maximum file size: 10 MB.
- Parses the campaign YAML and imports all entities into the store.

**Example output:**
```
Campaign loaded!
Name: Curse of Strahd
System: D&D 5e
Entities imported: 67
```

---

#### `/campaign switch`

Switch to a different campaign configuration. Requires no active session.

```
/campaign switch name:<campaign_name>
```

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `name` | String | Yes | Campaign name (autocomplete-enabled from available campaigns). |

**Permissions:** DM role required.

**Behaviour:**
- If a session is active, the command fails -- stop the session first.
- Autocomplete suggests available campaign names from the configuration.

---

### `/feedback`

Submit post-session feedback via a modal form. Available to any user after at least one session has been run.

```
/feedback
```

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| *(none -- opens a modal)* | -- | -- | -- |

**Permissions:** Any user (no DM role required). A session must have been run previously.

**Behaviour:**
- Opens a modal with five fields:

| Modal Field | Required | Range | Description |
|-------------|----------|-------|-------------|
| Voice latency | Yes | 1-5 | Rate voice response speed (1 = terrible, 5 = great). |
| NPC personality | Yes | 1-5 | Rate how well NPCs stayed in character. |
| Memory accuracy | Yes | 1-5 | Rate how well NPCs remembered past events. |
| DM workflow | Yes | 1-5 | Rate the overall DM workflow experience. |
| Comments | No | up to 1000 chars | Free-text feedback. |

- On submit, saves the feedback and responds with the average rating.

**Example output:**
```
Thank you for your feedback! Average rating: 4.3/5

Session: abc123-def456
```

---

## :studio_microphone: Voice Commands

Voice commands are spoken aloud by the DM during a live session. The voice command filter runs on the DM's speech-to-text output *before* it reaches the NPC routing pipeline, so these phrases are intercepted and never forwarded to NPC agents.

Voice commands are **case-insensitive** and only processed for the configured DM user.

| Spoken Phrase | Pattern | Action | Example |
|---------------|---------|--------|---------|
| `mute <name>` | `^mute\s+(.+)$` | Mutes the named NPC. | *"Mute Grimjaw"* |
| `<name>, be quiet` | `^(.+?),?\s+be\s+quiet$` | Mutes the named NPC (natural speech variant). | *"Grimjaw, be quiet"* |
| `unmute <name>` | `^unmute\s+(.+)$` | Unmutes the named NPC. | *"Unmute Elara"* |
| `everyone, stop` | `^everyone,?\s+stop$` | Mutes **all** NPCs at once. | *"Everyone, stop"* |
| `everyone, continue` | `^everyone,?\s+continue$` | Unmutes **all** NPCs at once. | *"Everyone, continue"* |
| `<name>, say <text>` | `^(.+?),?\s+say\s+(.+)$` | Makes the named NPC speak the given text (puppet mode). | *"Grimjaw, say welcome to my tavern"* |

**Notes:**
- `<name>` is matched against active NPC names (case-insensitive).
- The comma after the NPC name is optional (e.g., both *"Grimjaw be quiet"* and *"Grimjaw, be quiet"* work).
- Patterns are tested in order from top to bottom; the first match wins.
- If the named NPC is not found, the command fails silently (logged as a warning).

---

## :performing_arts: Puppet Mode

Puppet mode lets the DM speak *as* an NPC -- the DM's words come out in the NPC's voice, bypassing the LLM entirely. This is useful when:

- The DM wants precise control over what an NPC says (e.g., reading boxed text, delivering a dramatic monologue).
- An NPC needs to say something the AI might not generate on its own (plot-critical revelations, specific item descriptions).
- The DM wants to override the AI's response in the moment.

### How It Works

There are three ways to use puppet mode:

#### 1. Slash Command: `/npc speak`

Type the exact text you want the NPC to say:

```
/npc speak name:Grimjaw text:The treasure lies beneath the old well.
```

The text is synthesised using the NPC's configured TTS voice and played into the voice channel. A transcript entry is recorded with the NPC as the speaker.

#### 2. Voice Command: `<name>, say <text>`

Speak the command aloud during a live session:

> *"Grimjaw, say the treasure lies beneath the old well"*

The DM's spoken phrase is intercepted by the voice command filter, and the captured text is fed through the NPC's TTS. The DM's original speech is **not** forwarded to any NPC agent.

#### 3. Routing Override: `SetPuppet`

The orchestrator's `SetPuppet(speaker, npcID)` API forces all subsequent utterances from a given speaker to be routed to a specific NPC, bypassing normal address detection. This is a lower-level mechanism used programmatically:

- **Set override:** `orchestrator.SetPuppet("dm-user-id", "grimjaw-001")` -- all DM speech now routes to Grimjaw.
- **Clear override:** `orchestrator.SetPuppet("dm-user-id", "")` -- returns to normal address detection.

The routing priority order is:
1. Explicit name match (NPC name spoken in the utterance)
2. DM puppet override (`SetPuppet`)
3. Last-speaker continuation
4. Single-NPC fallback
5. No match (error)

### Puppet Mode vs. Normal AI Responses

| Aspect | Normal Mode | Puppet Mode (`/npc speak` or voice `say`) |
|--------|-------------|---------------------------------------------|
| Text source | LLM generates the response | DM provides the exact text |
| Voice | NPC's configured TTS voice | NPC's configured TTS voice (same) |
| Transcript | Recorded as NPC speech | Recorded as NPC speech (same) |
| Conversation history | LLM response added to context | Puppet text added to context |
| Latency | STT + LLM + TTS pipeline | TTS only (faster) |

---

## :bar_chart: Dashboard

The session dashboard is a live-updating Discord embed that displays real-time metrics for the active session. It is automatically posted to the text channel when a session starts and updated every **10 seconds**.

### Information Displayed

The dashboard embed includes:

| Field | Description |
|-------|-------------|
| **Campaign** | Name of the active campaign. |
| **Session ID** | Unique identifier for the current session. |
| **Duration** | Elapsed time since session start (updates live). |
| **Active NPCs** | Count of active NPCs, with muted count if any are muted (e.g., "4 (2 muted)"). |
| **Utterances** | Total number of processed player utterances. |
| **Errors** | Total pipeline errors encountered. |
| **Pipeline Latency** | P50 and P95 latencies for each pipeline stage (when data is available): |
| | - **STT** -- Speech-to-text latency |
| | - **LLM** -- Language model inference latency |
| | - **TTS** -- Text-to-speech synthesis latency |
| | - **Total** -- End-to-end speech-to-speech latency |
| **Memory Entries** | Number of transcript entries stored in the session memory. |

### Lifecycle

- **Created** when `/session start` is run. The embed is posted as a new message in the text channel.
- **Updated in place** every 10 seconds by editing the same message (no chat spam).
- **Finalized** when `/session stop` is run. The embed colour changes from green to red, the footer changes to "Session ended", and updates stop.

### Embed Appearance

**During an active session** -- green sidebar (`#2ECC71`), footer reads "Live session", timestamp updates with each refresh.

**After session ends** -- red sidebar (`#E74C3C`), description reads "Session has ended.", footer reads "Session ended".

### Pipeline Latency Format

Latency values are computed from a rolling window of recent samples (default: last 100 observations per stage) and displayed as:

```
STT: p50=45.2ms p95=120.8ms
LLM: p50=230.1ms p95=510.3ms
TTS: p50=85.7ms p95=190.2ms
Total: p50=380.5ms p95=820.1ms
```

---

## :link: See Also

- [npc-agents.md](npc-agents.md) -- NPC agent configuration, voice settings, and personality authoring.
- [configuration.md](configuration.md) -- Bot configuration reference (`token`, `guild_id`, `dm_role_id`, campaign YAML format).
- [getting-started.md](getting-started.md) -- Quick-start guide for setting up Glyphoxa and running your first session.
