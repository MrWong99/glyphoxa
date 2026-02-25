> *This document is derived from the Glyphoxa Design Document v0.2*

# MCP Tool Integration

Glyphoxa agents use external tools through the Model Context Protocol (MCP). This follows the same plug-and-play philosophy as OpenClaw's ecosystem: tools are self-describing, independently deployable, and discoverable at runtime. The official MCP Go SDK (`modelcontextprotocol/go-sdk`, v1.0.0) provides the client implementation. Fallback: `mark3labs/mcp-go` (8k+ stars, 4 transports including Streamable HTTP and in-process).

## MCP Architecture

| Component | Role |
|---|---|
| MCP Host (Glyphoxa core, Go) | Maintains connections to MCP servers, manages the tool registry, routes tool calls from LLM responses to the correct server, enforces latency budgets |
| MCP Server (each tool) | Standalone process exposing tools. Can be local (stdio), remote (HTTP/SSE), or containerized. Written in any language. |
| Tool Registry | In-memory index of all available tools with schemas and measured latency estimates. Injected into LLM prompts as function definitions. |
| Permission Layer | Per-tool and per-agent permissions. The DM controls which agents can use which tools. An NPC should not web-search; the Q&A agent can. |
| Budget Enforcer | Controls which tools are visible to the LLM based on the active latency tier. Over-budget tools are stripped from function definitions before they reach the LLM — 100% reliable by construction. |

## Performance-Budgeted Tool Execution

> **Design validated.** Research on LLM instruction compliance showed that LLMs do not reliably follow quantitative budget constraints in prompts (70–85% compliance at best, worse for numerical constraints). The enforcement strategy is therefore **hard enforcement**: the orchestrator strips over-budget tools from function definitions before they reach the LLM. The LLM cannot call tools it doesn't know about.

The central insight: in a voice conversation, the user's tolerance for delay depends on what they asked. A casual "what's your name?" demands a sub-second response. A deliberate "tell me everything you know about the Shadowfell conspiracy" can tolerate 4–5 seconds. The orchestrator selects a latency tier based on conversation context and exposes only the tools that fit within that tier's budget.

### Tool Latency Metadata

Every MCP tool declares performance metadata as part of its schema:

| Field | Type | Description |
|---|---|---|
| `estimated_duration_ms` | int | The tool author's declared p50 latency (median expected response time). Used by the Budget Enforcer to assign tools to tiers. |
| `max_duration_ms` | int | The tool author's declared p99 upper bound. Used by the Budget Enforcer as a hard timeout. |
| `measured_duration_ms` | int (auto) | Glyphoxa's own measurement from live calibration. Overrides declared values when available. |
| `measured_p99_ms` | int (auto) | Glyphoxa's measured p99 latency from recent invocations. Rolling window of last 100 calls. |
| `idempotent` | bool | Whether the tool can be safely retried or called speculatively. |
| `cacheable_seconds` | int | How long results can be cached. 0 means never cache. |

### Calibration Protocol

When an MCP server connects, Glyphoxa runs an optional calibration handshake:

1. **Synthetic Probe:** The host calls each tool with a minimal valid input and measures wall-clock latency. This gives a baseline for the current environment (local tool: 5ms, remote API: 800ms).
2. **Ongoing Measurement:** Every real tool invocation is timed. A rolling window (last 100 calls) maintains `measured_duration_ms` (p50) and `measured_p99_ms`. These measured values override the tool author's declared estimates once sufficient data exists.
3. **Health Scoring:** Tools that consistently exceed their `max_duration_ms` or fail frequently get a degraded health score. The Budget Enforcer may temporarily demote a degraded tool to a higher tier (e.g., a normally-FAST tool that starts timing out gets moved to STANDARD-only until it recovers).
4. **Environment Awareness:** Calibration results are stored per-environment. A tool running locally might measure 10ms while the same tool running as a remote API measures 300ms. Glyphoxa tracks both.

### Orchestrator-Enforced Budget Tiers

The Agent Orchestrator selects a latency tier based on conversation context and controls which tools the LLM can see. The LLM receives **no budget instructions** — it simply sees a different set of available tools depending on the tier.

| Tier | Trigger | Max Parallel Latency | Tools Visible to LLM |
|---|---|---|---|
| **FAST** (default) | Normal conversational utterance | ≤ 500ms | dice-roller, memory.query_entities, file-io, music-ambiance |
| **STANDARD** | References to past events, rule questions, keyword detection | ≤ 1500ms | FAST tools + memory.search_sessions, memory.search_facts, rules-lookup, session-manager |
| **DEEP** | DM command, "think carefully," "take your time" | ≤ 4000ms | All tools including image-gen, web-search |

**Tier selection logic** (orchestrator, not LLM):
- **Conversation state:** First greeting → STANDARD (allow memory lookups). Rapid back-and-forth → FAST.
- **Keyword detection:** Lightweight regex/heuristic on STT partials. References to past events, distant locations, or specific rules trigger STANDARD.
- **DM commands:** Explicit upgrade/downgrade via voice command or Discord slash command.
- **Queue depth:** If multiple players are waiting, prefer FAST to reduce latency.
- **Time since last deep response:** Avoid consecutive DEEP responses.

**Why not prompt-based enforcement:** Research ([FollowBench](https://github.com/YJiangcm/FollowBench), [Mind the GAP](https://arxiv.org/html/2602.16943)) shows that LLMs achieve only 70–85% compliance with constraint instructions, dropping further for quantitative/numerical constraints. Tool-calling behavior diverges from text reasoning — a model may "reason" that it should avoid an expensive tool but call it anyway. Hard enforcement eliminates this failure mode entirely.

### Parallel Execution Semantics

When the LLM requests multiple tools in a single response, the orchestrator executes them in parallel. The tier's latency ceiling applies to the **maximum** of all parallel tools, not the sum:

- **Parallel:** `dice-roller` (15ms) + `memory.query_entities` (80ms) + `memory.search_facts` (200ms) → cost = `max(15, 80, 200) = 200ms`. Fits within FAST's 500ms ceiling.
- **Sequential:** If tool B depends on tool A's output, cost = A + B. The LLM is free to request parallel calls when tools are independent.

Since over-budget tools are never exposed to the LLM, no post-selection validation is needed. The orchestrator simply executes whatever the LLM requests.

### Marketplace Performance Scoring

For the eventual tool marketplace, performance data drives visibility and trust:

- **Performance badge:** Tools earn badges based on measured latency: "Lightning" (< 50ms), "Fast" (< 200ms), "Standard" (< 1s), "Slow" (> 1s). Based on aggregate measurements across installations that opt in to telemetry.
- **Leaderboard:** Within a tool category (e.g., "dice rollers"), tools are ranked by measured p50 latency.
- **Compatibility tags:** Tools are tagged with which budget modes they fit: a 3200ms image generator is tagged "DEEP only," while a 15ms dice roller is tagged "FAST compatible."

### Open Questions for Tool Budgets

1. **How accurate are declared latency estimates in practice?** Tool authors have an incentive to under-declare latency. The calibration system mitigates this, but what about tools with bimodal latency distributions (e.g., web search: 200ms cached, 3000ms uncached)? Should the budget use p50 or p95?

2. **Two-stage pipeline for latency masking?** Research suggests generating a quick filler response ("Hmm, let me think...") while tools execute in background, then playing the full response. This could mask STANDARD/DEEP latency without sacrificing tool quality. Needs UX prototyping with real play groups.

## Built-in Tool Servers

| Tool Server | Tools Provided | Est. Latency | Budget Mode |
|---|---|---|---|
| dice-roller | `roll(expression)`, `roll_table(table_name)` | 10–20ms | FAST |
| memory (cold layer) | `search_sessions`, `query_entities`, `get_summary`, `search_facts` | 50–400ms | FAST / STANDARD |
| rules-lookup | `search_rules(query, system)`, `get_rule(id)` | 300–500ms | STANDARD |
| image-gen | `generate_image(prompt, style)` | 2000–4000ms | DEEP only |
| web-search | `search(query)`, `fetch_page(url)` | 1500–3000ms | DEEP only |
| file-io | `write_file(path, content)`, `read_file(path)` | 20–50ms | FAST |
| session-manager | `get_summary(session_id)`, `search_history(query)` | 100–300ms | STANDARD |
| music-ambiance | `play_track(mood)`, `stop_music()` | 50–200ms | FAST |

## MCP Bridge for S2S Engines

S2S APIs (OpenAI Realtime, Gemini Live) support native function/tool calling. Glyphoxa bridges MCP tools into S2S sessions so that tool behavior is identical regardless of engine type.

**Tool declaration.** The orchestrator converts `MCPToolDef` schemas into S2S-native function definitions and declares them on the session via `SetTools`. The S2S model sees these as native tools.

**Orchestrator-enforced budgets.** The orchestrator controls which tools are declared based on the active budget tier. In FAST mode, only `dice-roller` and `memory.query_entities` are visible. In STANDARD, `memory.search_sessions` and `rules-lookup` are added. In DEEP, all tools are available. The LLM never sees over-budget tools.

**Tool execution flow.** When the S2S model calls a declared function, the `ToolCallHandler` executes the corresponding MCP tool and returns the result to the S2S session. For OpenAI Realtime, the model pauses audio output while waiting for the tool result. For Gemini Live, the behavior depends on the calling mode: BLOCKING (model waits), NON_BLOCKING (model continues speaking), or INTERRUPT (model can be interrupted by the result).

**Whole-response routing.** If the orchestrator determines that slow tools will be needed (based on conversation state, keyword detection, or DM command), it routes the entire interaction through the `CascadedEngine` instead. This avoids the complexity of mid-utterance engine switching. See [Providers: VoiceEngine](02-providers.md#voiceengine-interface).

## Custom Tool Development

Third-party developers and DMs can create custom MCP servers in any language (Go, TypeScript, Python, Rust). Glyphoxa auto-discovers tools when a new server is connected:

1. **Write** an MCP server using any official SDK. Include `estimated_duration_ms` and `max_duration_ms` in tool schemas.
2. **Register** it in Glyphoxa's config file (local path for stdio, URL for remote HTTP/SSE).
3. **Auto-discovery:** Glyphoxa connects, discovers tools, calibrates latency, and makes them available to agents immediately. No restart required for remote servers.

---

**See also:** [Architecture](01-architecture.md) · [Memory](03-memory.md) · [Technology](07-technology.md)
