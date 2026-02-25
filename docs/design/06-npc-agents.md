> *This document is derived from the Glyphoxa Design Document v0.2*

# NPC Agent Design

## Agent Anatomy

Each NPC agent is a Go struct implementing the `NPCAgent` interface, instantiated by the Agent Orchestrator as goroutines.

| Property | Description | Example |
|---|---|---|
| `identity` | Name, race, class, occupation, age, appearance | Grimjaw, dwarf, blacksmith, 147 years old, missing left eye |
| `personality` | Behavioral directives, speaking style, emotional baseline, quirks | Gruff but kind. Speaks in short sentences. Avoids eye contact when lying. |
| `voice_profile` | TTS voice ID, pitch adjustment, speed, accent | ElevenLabs voice: "Antoni", pitch: -2, speed: 0.9 |
| `knowledge_scope` | What this NPC knows. References knowledge graph entities and facts. | Knows all entities in "Ironhold" location subgraph. Knows about the missing shipment quest. |
| `secret_knowledge` | Facts the NPC knows but will only reveal under specific conditions | Knows the mayor is corrupt. Will only tell trusted allies. |
| `behavior_rules` | Constraints on NPC behavior. Triggers, escalations, refusals. | Never reveals the password. Becomes hostile if accused of theft. |
| `tools` | Which [MCP tools](04-mcp-tools.md) this agent can invoke | dice-roller, memory.*. Cannot use web-search or image-gen. |
| `engine` | Which [voice engine](02-providers.md#voiceengine-interface) this NPC uses | `"cascaded"` (STT→LLM→TTS pipeline) or `"s2s"` (speech-to-speech via OpenAI Realtime or Gemini Live) |

## Multi-NPC Orchestration

- **Address detection:** Determining which NPC was addressed (by name, conversational context, or the DM's explicit routing command).
- **Turn-taking:** Preventing NPCs from talking over each other. A priority queue based on conversation relevance and a small random delay simulates natural group dynamics.
- **Cross-NPC awareness:** NPCs in the same scene share a recent-utterance buffer. The orchestrator injects other NPCs' recent utterances into each agent's context so they can react to each other.
- **DM override:** The DM can mute, redirect, or override any NPC at any time via voice commands or Discord slash commands.

---

**See also:** [Architecture](01-architecture.md) · [Memory](03-memory.md) · [Sentence Cascade](05-sentence-cascade.md) · [Providers](02-providers.md)
