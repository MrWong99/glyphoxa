# Glyphoxa Documentation

Welcome to the Glyphoxa documentation. These guides cover how to set up, configure, extend, and operate Glyphoxa — a real-time voice AI framework for TTRPG NPCs.

## Where to Start

New to Glyphoxa? Follow this path:

1. **[Getting Started](getting-started.md)** — Install prerequisites, build, and run your first NPC
2. **[Architecture](architecture.md)** — Understand the system at a glance
3. **[Configuration](configuration.md)** — Configure providers, NPCs, and memory
4. **[Testing](testing.md)** — Run tests and learn conventions before contributing

## Documentation Index

### Setup and Overview

| Document | Description |
|----------|-------------|
| [Getting Started](getting-started.md) | Prerequisites, build, first run, development workflow |
| [Architecture](architecture.md) | System layers, data flow, key packages, latency budget |
| [Configuration](configuration.md) | Complete config field reference, hot-reload, provider options |

### Core Systems

| Document | Description |
|----------|-------------|
| [Providers](providers.md) | Provider interfaces, supported providers, adding new providers, resilience |
| [NPC Agents](npc-agents.md) | NPC definition, entities, campaigns, VTT import, hot context assembly |
| [Memory](memory.md) | 3-layer memory system, PostgreSQL setup, transcript correction, session lifecycle |

### Subsystems

| Document | Description |
|----------|-------------|
| [MCP Tools](mcp-tools.md) | Tool system, built-in tools, building custom tools, budget tiers |
| [Audio Pipeline](audio-pipeline.md) | Audio flow, transports, VAD, engine types, mixer, engine comparison |

### Operations

| Document | Description |
|----------|-------------|
| [Commands](commands.md) | Discord slash commands, voice commands, puppet mode, dashboard |
| [Deployment](deployment.md) | Docker Compose, building from source, GPU setup, production checklist |
| [Observability](observability.md) | Metrics, Prometheus, Grafana dashboards, health endpoints, alerting |

### Quality

| Document | Description |
|----------|-------------|
| [Testing](testing.md) | Running tests, conventions, mocks, provider testing, integration tests |
| [Troubleshooting](troubleshooting.md) | Build issues, provider issues, runtime debugging, diagnostic steps |

## Design Documents

These documents explain the *why* behind each subsystem — design decisions, rationale, and specifications. The guides above explain *how* to work with each subsystem.

| Document | Description |
|----------|-------------|
| [Overview](design/00-overview.md) | Vision, goals, product principles |
| [Architecture](design/01-architecture.md) | System layers and data flow |
| [Providers](design/02-providers.md) | LLM, STT, TTS, Audio platform interfaces |
| [Memory](design/03-memory.md) | Hybrid memory system and knowledge graph |
| [MCP Tools](design/04-mcp-tools.md) | Tool integration and performance budgets |
| [Sentence Cascade](design/05-sentence-cascade.md) | Dual-model cascade (experimental) |
| [NPC Agents](design/06-npc-agents.md) | Agent design and multi-NPC orchestration |
| [Technology](design/07-technology.md) | Technology decisions and latency budget |
| [Open Questions](design/08-open-questions.md) | Resolved and open design questions |
| [Roadmap](design/09-roadmap.md) | Development phases |
| [Knowledge Graph](design/10-knowledge-graph.md) | L3 graph schema and query patterns |

## Other Resources

- [README](../README.md) — Project overview and quick start
- [CONTRIBUTING](../CONTRIBUTING.md) — Development workflow and code style
- [SECURITY](../SECURITY.md) — Vulnerability reporting
- [Docker Compose Deployment](../deployments/compose/README.md) — Detailed Docker Compose guide
- [Example Configuration](../configs/example.yaml) — Annotated example config file
