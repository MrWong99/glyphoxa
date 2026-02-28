# Contributing to Glyphoxa

Thanks for your interest in contributing to Glyphoxa! This guide covers everything
you need to get started.

## Getting Started

### Prerequisites

- **Go 1.26+** with CGo enabled
- **libopus** — `apt install libopus-dev` (Debian/Ubuntu) · `pacman -S opus` (Arch) · `brew install opus` (macOS)
- **ONNX Runtime** — shared library from [onnxruntime releases](https://github.com/microsoft/onnxruntime/releases) (for Silero VAD)

### Build & Test

```bash
git clone https://github.com/MrWong99/glyphoxa.git
cd glyphoxa

# Build
make build

# Run all tests with race detector
make test

# Lint
make lint

# Full check (lint + vet + test)
make check
```

## Development Workflow

1. **Fork the repo** and create a feature branch from `main`
2. **Write code** following the style guidelines below
3. **Add tests** — every new package needs tests; aim for table-driven, parallel tests
4. **Run `make check`** before pushing
5. **Open a PR** against `main` — fill out the PR template

### Branch Naming

- `feat/short-description` — new features
- `fix/short-description` — bug fixes
- `docs/short-description` — documentation only
- `refactor/short-description` — code cleanup

## Code Style

Glyphoxa follows standard Go conventions with a few project-specific rules:

### Go Conventions

- **`gofmt`** — all code must be formatted with `gofmt`
- **`go vet`** — must pass cleanly
- **Godoc** — all exported symbols must have complete doc comments
- **Error wrapping** — use `%w` with consistent package prefixes (e.g., `"agent: ..."`)
- **No naked returns** — always name what you're returning explicitly
- **No stale loop-var captures** — Go 1.22+ semantics apply

### Testing

- **`t.Parallel()`** on all tests and subtests
- **Table-driven tests** where appropriate
- **`go test -race`** must pass — all public methods must be safe for concurrent use
- **Compile-time interface assertions** — `var _ Interface = (*Impl)(nil)`
- **Mocks** live in `<package>/mock/` subdirectories

For detailed testing patterns, mock conventions, and examples, see the [Testing Guide](docs/testing.md).

### Concurrency

- Thread-safety is non-negotiable — every public method must be safe for concurrent use
- Prefer `sync.Mutex` over channels for protecting shared state
- Never hold a lock during blocking I/O (network calls, channel operations)
- Use `container/heap` for priority queues (not sorted slices)
- Use `slices.SortFunc` over `sort.Slice` (Go 1.21+)

### Packages

- **`pkg/`** — public API; external code may import these packages
- **`internal/`** — application-private; not importable by external code
- **`cmd/`** — entry points

## Architecture

Before contributing a major feature, read the [design documents](docs/design/):

- [Architecture](docs/design/01-architecture.md) — system layers and data flow
- [Providers](docs/design/02-providers.md) — LLM, STT, TTS, Audio interfaces
- [Roadmap](docs/design/09-roadmap.md) — development phases

The core principle: **every external dependency sits behind an interface**. Swapping
providers is a config change, not a rewrite.

## Reporting Issues

- **Bugs** — use the [Bug Report template](.github/ISSUE_TEMPLATE/bug_report.yml)
- **Features** — use the [Feature Request template](.github/ISSUE_TEMPLATE/feature_request.yml)
- **Security** — see [SECURITY.md](SECURITY.md)

## License

By contributing, you agree that your contributions will be licensed under the
[GPL v3](LICENSE).
