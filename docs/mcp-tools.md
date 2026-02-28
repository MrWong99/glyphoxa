---
nav_order: 7
---

# :wrench: MCP Tools

## :globe_with_meridians: Overview

Glyphoxa uses the [Model Context Protocol (MCP)](https://modelcontextprotocol.io/) to give NPC agents access to tools -- dice rolling, rule lookups, world state queries, file I/O, and more. MCP is a vendor-neutral JSON-RPC protocol that lets any language implement a "tool server" that Glyphoxa discovers, connects to, and exposes to LLMs at runtime.

The architecture follows a **Host / Server** split:

| Component | Role |
|---|---|
| **MCP Host** (`internal/mcp/mcphost`) | Connects to MCP servers, maintains the tool registry, routes tool calls from LLM responses to the correct server, enforces latency budgets. |
| **MCP Server** (each tool) | A standalone process (or in-process Go function) that exposes one or more tools over the MCP protocol. Can be local (stdio), remote (streamable HTTP), or a built-in Go handler. |
| **Tool Registry** | In-memory index of all available tools with schemas and measured latency estimates. Tools are injected into LLM prompts as function definitions. |
| **Budget Enforcer** (`mcphost.BudgetEnforcer`) | Controls which tools are visible to the LLM based on the active latency tier. Over-budget tools are stripped from function definitions before they reach the LLM -- 100% reliable by construction. |
| **Tier Selector** (`internal/mcp/tier`) | Lightweight heuristic (keyword detection, conversation state) that picks the right budget tier for each player utterance. Runs in sub-millisecond time, no LLM calls. |
| **S2S Bridge** (`internal/mcp/bridge`) | Translates between the MCP Host's tool catalogue and an S2S session's native function-calling interface. |

The MCP Go SDK (`github.com/modelcontextprotocol/go-sdk`) provides the client implementation. Two transports are supported:

- **stdio** -- spawns a subprocess and communicates over stdin/stdout.
- **streamable-http** -- communicates via the MCP Streamable HTTP protocol to a remote endpoint.

### Host Lifecycle

```
1. RegisterServer(cfg)   -- connect to each MCP server, import its tool catalogue
2. RegisterBuiltin(tool) -- register in-process Go tools (no network overhead)
3. Calibrate(ctx)        -- measure real tool latencies, assign budget tiers
4. AvailableTools(tier)  -- enumerate tools valid for a budget tier
5. ExecuteTool(ctx, ...)  -- run a tool on behalf of an NPC agent
6. Close()               -- release all connections and resources
```

---

## :hammer: Built-in Tools

Built-in tools are implemented as Go functions that run in-process, bypassing MCP protocol overhead entirely. They are defined in `internal/mcp/tools/` and registered with the host via `RegisterBuiltin`. Each sub-package exports a constructor that returns a `[]tools.Tool` slice.

### :game_die: Dice Roller (`internal/mcp/tools/diceroller`)

| Tool | Description | Parameters | P50 / Max | Tier |
|---|---|---|---|---|
| `roll` | Evaluate a dice expression (e.g. `2d6+3`, `1d20`, `4d8-1`). Returns individual die results and total. | `expression` (string, required) | 5ms / 20ms | FAST |
| `roll_table` | Roll on a named random table. Available tables: `wild_magic`, `treasure_hoard`, `random_encounter`. | `table_name` (string enum, required) | 5ms / 20ms | FAST |

**Example call and response:**

```json
// Input
{"expression": "2d6+3"}

// Output
{"expression": "2d6+3", "rolls": [4, 2], "total": 9}
```

```json
// Input
{"table_name": "wild_magic"}

// Output
{"table": "wild_magic", "roll": 3, "result": "You cast Fireball centred on yourself."}
```

### :brain: Memory Tools (`internal/mcp/tools/memorytool`)

These tools expose Glyphoxa's three-layer memory architecture (L1 session transcripts, L2 semantic index, L3 knowledge graph) to NPC agents. Constructed via `memorytool.NewTools(sessions, index, graph)`.

| Tool | Description | Parameters | P50 / Max | Tier |
|---|---|---|---|---|
| `search_sessions` | Full-text search across L1 session transcripts. Optionally restrict to a single session. | `query` (string, required), `session_id` (string, optional) | 100ms / 500ms | FAST |
| `query_entities` | Find entities in the L3 knowledge graph by name and/or type. Supports NPCs, locations, factions, items. | `name` (string, optional), `type` (string, optional) | 50ms / 200ms | FAST |
| `get_summary` | Retrieve a full identity snapshot for a knowledge-graph entity, including attributes, relationships, and connected entities. | `entity_id` (string, required) | 80ms / 300ms | FAST |
| `search_facts` | Full-text search for facts across session history. Falls back to L1 transcript search when no embedding provider is available. | `query` (string, required), `top_k` (int, optional, default 10) | 200ms / 800ms | FAST |

### :scroll: Rules Lookup (`internal/mcp/tools/ruleslookup`)

Provides keyword-based search over an embedded D&D 5e SRD dataset. The dataset is stored in-process (no external I/O) and includes conditions, combat rules, spells, and general rules.

| Tool | Description | Parameters | P50 / Max | Tier |
|---|---|---|---|---|
| `search_rules` | Keyword search across the embedded SRD rules. Optionally filter by game system. | `query` (string, required), `system` (string, optional, e.g. `"dnd5e"`) | 30ms / 100ms | FAST |
| `get_rule` | Retrieve a specific rule by its unique ID. Use `search_rules` first to discover IDs. | `id` (string, required, e.g. `"condition-blinded"`, `"spell-fireball"`) | 5ms / 20ms | FAST |

**Available rule categories:** condition, combat, spell, general.

### :file_folder: File I/O (`internal/mcp/tools/fileio`)

Sandboxed file reading and writing. All paths are resolved relative to a configured base directory; path traversal attempts (`../`) are rejected. Constructed via `fileio.NewTools(baseDir)`.

| Tool | Description | Parameters | P50 / Max | Tier |
|---|---|---|---|---|
| `write_file` | Write text content to a file within the sandbox. Creates parent directories automatically. | `path` (string, required), `content` (string, required) | 20ms / 100ms | FAST |
| `read_file` | Read a file from the sandbox. Files larger than 1 MiB are rejected. | `path` (string, required) | 20ms / 100ms | FAST |

---

## :zap: Performance Budgets

### The Problem

In a voice conversation, user tolerance for delay depends on context. A casual "what's your name?" demands a sub-second response. A deliberate "tell me everything you know about the Shadowfell conspiracy" can tolerate several seconds. Glyphoxa solves this with **orchestrator-enforced budget tiers**: the system selects a latency tier based on conversation context and exposes only the tools that fit within that tier's budget.

### Why Not Prompt-Based Enforcement?

Research ([FollowBench](https://github.com/YJiangcm/FollowBench), [Mind the GAP](https://arxiv.org/html/2602.16943)) shows LLMs achieve only 70--85% compliance with constraint instructions, dropping further for quantitative constraints. A model may "reason" that it should avoid an expensive tool but call it anyway. Hard enforcement eliminates this failure mode entirely: **the LLM never sees over-budget tools**.

### Budget Tiers

| Tier | String | Max Parallel Latency | Typical Triggers | Example Tools |
|---|---|---|---|---|
| **FAST** | `"fast"` | 500 ms | Normal conversational utterance; high queue depth (3+ players waiting) | `roll`, `roll_table`, `query_entities`, `get_rule`, `read_file`, `write_file` |
| **STANDARD** | `"standard"` | 1,500 ms | References to past events, rule questions, keyword detection ("remember", "rules", "quest"), first conversation turn | FAST tools + `search_sessions`, `search_facts`, `search_rules` |
| **DEEP** | `"deep"` | 4,000 ms | DM command, "think carefully", "take your time", "deep search" | All tools including image-gen, web-search (when available) |

Tier assignment is defined in `internal/mcp/types.go`:

```go
const (
    BudgetFast     BudgetTier = iota  // p50 <= 500ms
    BudgetStandard                     // p50 <= 1500ms
    BudgetDeep                         // all remaining tools
)
```

### Tier Selection Logic (`internal/mcp/tier/selector.go`)

The `Selector` uses lightweight heuristics (no LLM calls, sub-millisecond execution) to pick the tier for each utterance. Priority order (highest first):

1. **Explicit DM override** -- a non-zero `dmOverride` value wins unconditionally.
2. **DEEP keyword match** -- keywords like "think carefully", "take your time", "deep search", "generate image", "search the web". Subject to anti-spam: if a DEEP selection was made less than 30 seconds ago, the request is demoted to STANDARD.
3. **High queue depth** -- if 3+ players are waiting, FAST is returned to reduce latency (overrides STANDARD keywords but not DEEP matches).
4. **STANDARD keyword match** -- keywords like "remember", "last time", "rules", "quest", "who is", "tell me about".
5. **First conversation turn** -- STANDARD is returned to allow memory lookups for the initial greeting.
6. **Default** -- FAST.

```go
sel := tier.NewSelector(
    tier.WithMinDeepInterval(30 * time.Second),
    tier.WithDeepKeywords("think carefully", "deep search"),
    tier.WithStandardKeywords("remember", "rules", "quest"),
)

budget := sel.Select(transcriptText, dmOverride)
tools := host.AvailableTools(budget)
```

### Calibration (`internal/mcp/mcphost/calibrate.go`)

Calibration measures real tool latencies and adjusts tier assignments based on observed performance rather than declared estimates.

**How it works:**

1. **Synthetic probes** -- Glyphoxa calls each tool with empty args (`"{}"`) and measures wall-clock latency. Probes run concurrently via `errgroup`.
2. **Rolling window** -- A ring buffer of the last 100 measurements per tool maintains P50 and P99 latencies (see `internal/mcp/mcphost/metrics.go`).
3. **Tier reassignment** -- After each measurement:
   - P50 <= 500ms --> FAST
   - P50 <= 1500ms --> STANDARD
   - Otherwise --> DEEP
4. **Health demotion** -- If a tool's error rate within the window exceeds 30%, its tier is bumped up by one level (e.g. FAST --> STANDARD) until it recovers.

```go
// Run calibration at startup (or periodically)
if err := host.Calibrate(ctx); err != nil {
    log.Printf("calibration failed: %v", err)
}
```

Calibration is optional. Without it, tools retain their declared P50 values for tier assignment.

### Parallel Execution Semantics

When the LLM requests multiple tools in a single response, the orchestrator executes them in parallel. The tier's latency ceiling applies to the **maximum** of all parallel tools, not the sum:

```
dice-roller (15ms) + query_entities (80ms) + search_facts (200ms)
--> cost = max(15, 80, 200) = 200ms  (fits within FAST's 500ms ceiling)
```

---

## :gear: Configuring MCP Servers

External MCP servers are declared in `glyphoxa.yaml` under the `mcp.servers` key. The configuration is modelled by `config.MCPServerConfig` in `internal/config/config.go`.

### Stdio Transport (Local Subprocess)

```yaml
mcp:
  servers:
    - name: dice-extended
      transport: stdio
      command: /usr/local/bin/mcp-dice-server --config /etc/dice.json
      env:
        DICE_MAX_SIDES: "100"
        LOG_LEVEL: debug
```

The `command` string is split on whitespace into executable + arguments. `env` injects additional environment variables into the subprocess.

### Streamable HTTP Transport (Remote Server)

```yaml
mcp:
  servers:
    - name: world-search
      transport: streamable-http
      url: https://tools.example.com/mcp
```

### Authentication (OAuth 2.1)

HTTP-based MCP servers can be authenticated using either a static Bearer token or OAuth 2.1 client credentials flow:

**Static token:**

```yaml
mcp:
  servers:
    - name: premium-tools
      transport: streamable-http
      url: https://premium.example.com/mcp
      auth:
        token: "Bearer eyJhbGciOiJSUzI1NiIs..."
```

**OAuth 2.1 client credentials:**

```yaml
mcp:
  servers:
    - name: enterprise-tools
      transport: streamable-http
      url: https://enterprise.example.com/mcp
      auth:
        oauth:
          client_id: glyphoxa-prod
          client_secret: "${MCP_OAUTH_SECRET}"
          token_url: https://auth.example.com/oauth/token
          scopes:
            - tools:read
            - tools:execute
```

When `oauth` is set, the `token` field is ignored. Glyphoxa obtains and refreshes Bearer tokens automatically via the client-credentials flow.

### Full Configuration Reference

```yaml
mcp:
  servers:
    - name: string           # unique identifier (required)
      transport: string      # "stdio" or "streamable-http" (required)
      command: string        # executable + args for stdio (required if stdio)
      url: string            # endpoint URL for streamable-http (required if streamable-http)
      env:                   # additional env vars for stdio subprocess (optional)
        KEY: value
      auth:                  # authentication for streamable-http (optional)
        token: string        # static Bearer token
        oauth:               # OAuth 2.1 client-credentials (overrides token)
          client_id: string
          client_secret: string
          token_url: string
          scopes: [string]
```

---

## :building_construction: Building a Custom MCP Tool

This section walks through creating a built-in Go tool. The same pattern applies to all tools in `internal/mcp/tools/`.

### Step 1: Define the Tool Package

Create a new package under `internal/mcp/tools/`:

```
internal/mcp/tools/weathertool/
    weathertool.go
    weathertool_test.go
```

### Step 2: Define Input/Output Types

```go
package weathertool

import (
    "context"
    "encoding/json"
    "fmt"

    "github.com/MrWong99/glyphoxa/internal/mcp/tools"
    "github.com/MrWong99/glyphoxa/pkg/provider/llm"
)

// getWeatherArgs is the JSON-decoded input for the "get_weather" tool.
type getWeatherArgs struct {
    Location string `json:"location"`
    Season   string `json:"season,omitempty"`
}

// getWeatherResult is the JSON-encoded output of the "get_weather" tool.
type getWeatherResult struct {
    Location    string `json:"location"`
    Conditions  string `json:"conditions"`
    Temperature string `json:"temperature"`
}
```

### Step 3: Implement the Handler

The handler receives a `context.Context` and a JSON-encoded arguments string. It must be safe for concurrent use and respect context cancellation.

```go
func getWeatherHandler(_ context.Context, args string) (string, error) {
    var a getWeatherArgs
    if err := json.Unmarshal([]byte(args), &a); err != nil {
        return "", fmt.Errorf("weather: failed to parse arguments: %w", err)
    }
    if a.Location == "" {
        return "", fmt.Errorf("weather: location must not be empty")
    }

    // Your tool logic here. This example returns fictional weather.
    result := getWeatherResult{
        Location:    a.Location,
        Conditions:  "overcast with arcane lightning",
        Temperature: "cool",
    }

    res, err := json.Marshal(result)
    if err != nil {
        return "", fmt.Errorf("weather: failed to encode result: %w", err)
    }
    return string(res), nil
}
```

### Step 4: Export the Tool Constructor

Return a `[]tools.Tool` with the tool definition, handler, and declared latency estimates:

```go
// Tools returns the weather tools ready for registration with the MCP Host.
func Tools() []tools.Tool {
    return []tools.Tool{
        {
            Definition: llm.ToolDefinition{
                Name:        "get_weather",
                Description: "Get the current in-world weather for a location. " +
                    "Returns conditions and temperature.",
                Parameters: map[string]any{
                    "type": "object",
                    "properties": map[string]any{
                        "location": map[string]any{
                            "type":        "string",
                            "description": "The in-world location name.",
                        },
                        "season": map[string]any{
                            "type":        "string",
                            "description": "Current season (spring, summer, autumn, winter).",
                        },
                    },
                    "required": []string{"location"},
                },
                EstimatedDurationMs: 10,
                MaxDurationMs:       50,
                Idempotent:          true,
                CacheableSeconds:    60,
            },
            Handler:     getWeatherHandler,
            DeclaredP50: 10,
            DeclaredMax: 50,
        },
    }
}
```

### Step 5: Register with the MCP Host

Convert each `tools.Tool` into an `mcphost.BuiltinTool` and register it:

```go
host := mcphost.New()

for _, t := range weathertool.Tools() {
    if err := host.RegisterBuiltin(mcphost.BuiltinTool{
        Definition:  t.Definition,
        Handler:     t.Handler,
        DeclaredP50: t.DeclaredP50,
        DeclaredMax: t.DeclaredMax,
    }); err != nil {
        log.Fatalf("failed to register tool %q: %v", t.Definition.Name, err)
    }
}
```

### Step 6: Write Tests

Use the standard `testing` package. Tool handlers are plain functions -- no MCP protocol overhead to mock:

```go
func TestGetWeatherHandler(t *testing.T) {
    result, err := getWeatherHandler(context.Background(),
        `{"location": "Neverwinter", "season": "winter"}`)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }

    var got getWeatherResult
    if err := json.Unmarshal([]byte(result), &got); err != nil {
        t.Fatalf("failed to decode result: %v", err)
    }
    if got.Location != "Neverwinter" {
        t.Errorf("expected location Neverwinter, got %q", got.Location)
    }
}
```

### Tool Definition Fields

| Field | Type | Description |
|---|---|---|
| `Name` | `string` | Unique tool identifier. Must match across the entire host. |
| `Description` | `string` | Human-readable explanation shown to the LLM. Be specific. |
| `Parameters` | `map[string]any` | JSON Schema for input parameters. |
| `EstimatedDurationMs` | `int` | Declared p50 latency. Drives initial budget tier assignment. |
| `MaxDurationMs` | `int` | Declared p99 upper bound. Used as a hard timeout. |
| `Idempotent` | `bool` | Whether the tool can be safely retried or called speculatively. |
| `CacheableSeconds` | `int` | How long results can be cached. `0` means never. |

---

## :busts_in_silhouette: Wiring Tools to NPCs

### Per-NPC Tool Lists

Each NPC declares which tools it can access in its YAML configuration. The `tools` field lists MCP tool names by their registered identifiers:

```yaml
npcs:
  - name: Greymantle the Sage
    personality: "An ancient elven scholar with vast knowledge..."
    engine: cascaded
    budget_tier: standard
    tools:
      - roll
      - search_rules
      - get_rule
      - query_entities
      - get_summary
      - search_sessions

  - name: Bartok the Barkeep
    personality: "A jovial halfling who runs the tavern..."
    engine: s2s
    budget_tier: fast
    tools:
      - roll
      - roll_table
      - query_entities
```

### Budget Tier Assignment

The `budget_tier` field sets the **maximum** tier this NPC can access. Combined with the dynamic tier selector, the effective tier for any given utterance is:

```
effective_tier = min(npc.budget_tier, selector.Select(transcript, dmOverride))
```

This means a FAST-tier NPC will never see STANDARD or DEEP tools, even if the player says "think carefully."

### Permissions Model

The permissions model follows a whitelist approach:

1. **Tool whitelist** -- only tools listed in an NPC's `tools` field are available to that NPC. An NPC that lacks `search_rules` in its list will never see rules-lookup tools.
2. **Budget ceiling** -- the NPC's `budget_tier` caps the dynamic tier selector. A shopkeeper NPC with `budget_tier: fast` stays responsive even during complex conversations.
3. **DM override** -- the DM can temporarily elevate any NPC's budget tier via voice command or Discord slash command. This is the `dmOverride` parameter in `Selector.Select()`.

### Engine Compatibility

Both `cascaded` and `s2s` engines support tools. The wiring differs:

- **Cascaded engine** -- tools are passed as `llm.ToolDefinition` slices directly to the LLM provider. Tool calls in the LLM response are executed via `host.ExecuteTool()` and results fed back as tool-role messages.
- **S2S engine** -- tools are declared on the S2S session via the MCP Bridge (see next section). The S2S model sees them as native functions.
- **Sentence cascade engine** -- tools are available to the strong (continuation) model only. The fast (opener) model generates a quick filler response while tools execute.

---

## :bridge_at_night: S2S / MCP Bridge

Speech-to-speech engines (OpenAI Realtime, Gemini Live) have native function-calling interfaces but know nothing about MCP. The **Bridge** (`internal/mcp/bridge`) translates between the two worlds.

### How It Works

```
Player speaks --> S2S Engine --> Model requests tool call
                                       |
                                       v
                              Bridge.handleToolCall()
                                       |
                                       v
                              host.ExecuteTool(ctx, name, args)
                                       |
                                       v
                              Result returned to S2S session
                                       |
                                       v
                              Model incorporates result, resumes speaking
```

### Bridge Lifecycle

```go
// Create the bridge when the session starts.
b, err := bridge.NewBridge(host, session, mcp.BudgetFast)
if err != nil {
    return err
}
defer b.Close()

// Mid-conversation, the DM requests deeper tools.
if err := b.UpdateTier(ctx, mcp.BudgetDeep); err != nil {
    log.Printf("tier update failed: %v", err)
}
```

On creation, `NewBridge`:

1. Calls `host.AvailableTools(tier)` to get the budget-appropriate tool definitions.
2. Calls `session.SetTools(tools)` to declare them as native functions on the S2S session.
3. Registers a `ToolCallHandler` via `session.OnToolCall(b.handleToolCall)` that routes all tool calls back through the MCP Host.

### Tool Execution Timeout

Each tool execution within the bridge is bounded by a configurable timeout (default: 30 seconds). Since the S2S `ToolCallHandler` callback does not propagate a caller context, the bridge creates its own:

```go
b, err := bridge.NewBridge(host, session, mcp.BudgetFast,
    bridge.WithToolTimeout(15 * time.Second),
)
```

### Dynamic Tier Updates

The bridge supports mid-conversation tier changes via `UpdateTier()`. This re-fetches the tool set from the host at the new tier and updates the S2S session:

```go
// Player says "tell me everything about the conspiracy"
// Tier selector returns BudgetDeep
if err := b.UpdateTier(ctx, mcp.BudgetDeep); err != nil {
    log.Printf("failed to update tier: %v", err)
}
```

The S2S model immediately sees the expanded tool set (or reduced set if downgrading) on its next turn.

### Close Behavior

`Bridge.Close()` deregisters the `ToolCallHandler` from the session by calling `session.OnToolCall(nil)`. It does **not** close the underlying S2S session or the MCP Host -- callers are responsible for their own lifecycle management.

---

## :link: See Also

- [`architecture.md`](architecture.md) -- system architecture and component overview
- [`configuration.md`](configuration.md) -- full YAML configuration reference
- [`npc-agents.md`](npc-agents.md) -- NPC agent configuration and behavior
- [`providers.md`](providers.md) -- provider interfaces (STT, LLM, TTS, S2S)
- [`design/04-mcp-tools.md`](design/04-mcp-tools.md) -- original design rationale and research references
