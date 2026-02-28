---
nav_order: 12
---

# :test_tube: Testing Guide

This guide covers everything you need to know about writing and running tests
for Glyphoxa. If you are reading this before your first contribution, start with
the [Getting Started](getting-started.md) guide, then come back here.

---

## :compass: Overview

Tests are non-negotiable in Glyphoxa. Every public method must be safe for
concurrent use, and the race detector is enabled on **every** test run. The
project leans heavily on:

- **Parallel execution** — `t.Parallel()` is mandatory on all tests and subtests.
- **Table-driven tests** — the primary pattern for input/output coverage.
- **Hand-written mocks** — no codegen; mocks live alongside the code they double.
- **Standard library only** — no third-party assertion frameworks.

The goal is fast, deterministic, race-free tests that run identically on every
developer machine and in CI.

---

## :rocket: Running Tests

### Quick reference

| Command | What it does |
| --------------------- | -------------------------------------------------- |
| `make test` | Run all tests with `-race -count=1` |
| `make test-v` | Same, with verbose output |
| `make test-cover` | Tests + coverage report (`coverage.out`) |
| `make lint` | Run `golangci-lint run ./...` |
| `make vet` | Run `go vet ./...` |
| `make fmt` | Format all Go files with `gofmt` |
| `make check` | Full pre-commit gate: `fmt` + `vet` + `test` |

### Running a specific package

```bash
go test -race -count=1 ./internal/config/...
```

### Running a single test

```bash
go test -race -count=1 -run TestParseExpression_Valid ./internal/mcp/tools/diceroller/
```

### Coverage HTML

```bash
make test-cover
go tool cover -html=coverage.out
```

> :bulb: Always run `make check` before pushing. CI will run the same checks and
> block the PR if anything fails.

---

## :straight_ruler: Testing Conventions

### 1. `t.Parallel()` is mandatory

Every test function and every subtest must call `t.Parallel()`. This ensures the
race detector catches real concurrency bugs and keeps the suite fast.

```go
func TestMyFunction_HappyPath(t *testing.T) {
    t.Parallel()
    // ...
}

func TestMyFunction_EdgeCases(t *testing.T) {
    t.Parallel()

    tests := []struct {
        name string
        // ...
    }{ /* ... */ }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            t.Parallel()
            // ...
        })
    }
}
```

### 2. Table-driven tests

Use table-driven tests whenever you are verifying multiple inputs against
expected outputs. Define a `tests` (or `cases`) slice of anonymous structs, then
range over it with `t.Run`.

Here is a representative pattern adapted from the dice roller tool tests:

```go
func TestParseExpression_Valid(t *testing.T) {
    t.Parallel()

    tests := []struct {
        expr         string
        wantCount    int
        wantSides    int
        wantModifier int
    }{
        {"1d6", 1, 6, 0},
        {"2d6+3", 2, 6, 3},
        {"4d8-1", 4, 8, -1},
        {"d20", 1, 20, 0},   // implicit count
        {"D6", 1, 6, 0},     // case-insensitive
    }

    for _, tt := range tests {
        t.Run(tt.expr, func(t *testing.T) {
            t.Parallel()
            count, sides, modifier, err := parseExpression(tt.expr)
            if err != nil {
                t.Fatalf("parseExpression(%q) unexpected error: %v", tt.expr, err)
            }
            if count != tt.wantCount {
                t.Errorf("count = %d, want %d", count, tt.wantCount)
            }
            if sides != tt.wantSides {
                t.Errorf("sides = %d, want %d", sides, tt.wantSides)
            }
            if modifier != tt.wantModifier {
                t.Errorf("modifier = %d, want %d", modifier, tt.wantModifier)
            }
        })
    }
}
```

For validation-heavy tests with multi-field error checking, see the pattern in
`internal/agent/npcstore/postgres_test.go`:

```go
tests := []struct {
    name    string
    def     NPCDefinition
    wantErr []string // substrings that must appear in the error
}{
    {
        name: "valid minimal",
        def:  NPCDefinition{Name: "Test NPC"},
    },
    {
        name:    "empty name",
        def:     NPCDefinition{},
        wantErr: []string{"name must not be empty"},
    },
    {
        name: "multiple errors",
        def: NPCDefinition{
            Engine:     "warp",
            BudgetTier: "ultra",
        },
        wantErr: []string{"name must not be empty", "engine must be", "budget_tier must be"},
    },
}

for _, tt := range tests {
    t.Run(tt.name, func(t *testing.T) {
        t.Parallel()

        err := tt.def.Validate()
        if len(tt.wantErr) == 0 {
            if err != nil {
                t.Fatalf("Validate() unexpected error: %v", err)
            }
            return
        }
        if err == nil {
            t.Fatal("Validate() expected error, got nil")
        }
        for _, want := range tt.wantErr {
            if !strings.Contains(err.Error(), want) {
                t.Errorf("error = %q, want substring %q", err.Error(), want)
            }
        }
    })
}
```

### 3. Test naming

Follow the pattern **`TestFunctionName_Scenario`**:

```
TestLoadFromReader_Valid
TestLoadFromReader_EmptyIsValid
TestValidate_InvalidLogLevel
TestRegistry_UnknownLLM
TestCircuitBreaker_ClosedToOpen
TestRollHandler_Invalid
TestPostgresStore_Create
```

For subtests inside table-driven tests, use the struct `name` field as the
`t.Run` label. Keep names short but descriptive enough to identify the case at a
glance in verbose output.

### 4. Subtests with `t.Run`

Use `t.Run` for logically grouped scenarios within a single test function. Each
subtest **must** call `t.Parallel()`:

```go
func TestContextManager_AddMessages(t *testing.T) {
    t.Parallel()

    t.Run("adds messages and tracks tokens", func(t *testing.T) {
        t.Parallel()
        // ...
    })

    t.Run("triggers summarisation when threshold exceeded", func(t *testing.T) {
        t.Parallel()
        // ...
    })
}
```

### 5. Use the standard library

Glyphoxa tests use **only** the `testing` package for assertions. No
`testify/assert`, no `gomock`. Use `t.Fatalf` for fatal precondition failures
and `t.Errorf` for non-fatal checks that should continue:

```go
if err != nil {
    t.Fatalf("unexpected error: %v", err)       // stop the test
}
if got != want {
    t.Errorf("Name() = %q, want %q", got, want) // report and continue
}
```

---

## :performing_arts: Mock Conventions

### Directory layout

Every package that exposes an interface has a corresponding `mock/` subdirectory
containing hand-written test doubles:

```
pkg/provider/llm/
    provider.go          # llm.Provider interface
    mock/
        mock.go          # mock.Provider implementation
pkg/provider/stt/
    provider.go          # stt.Provider interface
    mock/
        mock.go          # mock.Provider + mock.Session
internal/engine/
    engine.go            # engine.VoiceEngine interface
    mock/
        mock.go          # mock.VoiceEngine
internal/mcp/
    host.go              # mcp.Host interface
    mock/
        mock.go          # mock.Host
```

### Exported fields for injection

Mocks use exported fields to configure return values and to record calls. This
avoids codegen and keeps tests readable. Here is the pattern from the LLM mock:

```go
type Provider struct {
    mu sync.Mutex

    // Configurable return values — set before calling.
    StreamChunks     []llm.Chunk
    StreamErr        error
    CompleteResponse *llm.CompletionResponse
    CompleteErr      error
    TokenCount       int
    CountTokensErr   error
    ModelCapabilities llm.ModelCapabilities

    // Call records — read after test.
    StreamCalls           []StreamCall
    CompleteCalls         []CompleteCall
    CountTokensCalls      []CountTokensCall
    CapabilitiesCallCount int
}
```

Use the mock in your test like this:

```go
llmProv := &llmmock.Provider{
    StreamChunks: []llm.Chunk{
        {Text: "Well met, traveller.", FinishReason: "stop"},
    },
}

// ... exercise the system under test ...

if len(llmProv.StreamCalls) != 1 {
    t.Errorf("StreamCompletion calls: want 1, got %d", len(llmProv.StreamCalls))
}
```

### Compile-time interface checks

Every mock file must include a compile-time assertion that the mock satisfies the
interface it doubles:

```go
var _ llm.Provider = (*Provider)(nil)
var _ stt.Provider = (*Provider)(nil)
var _ stt.SessionHandle = (*Session)(nil)
var _ engine.VoiceEngine = (*VoiceEngine)(nil)
var _ mcp.Host = (*Host)(nil)
```

Place these at the bottom of the mock file. They cost nothing at runtime and
catch interface drift immediately at compile time.

### Thread safety

All mocks protect their internal state with a `sync.Mutex`. This is required
because tests run in parallel and the system under test may invoke mock methods
from multiple goroutines. Every method must lock, operate, and unlock:

```go
func (p *Provider) Complete(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
    p.mu.Lock()
    defer p.mu.Unlock()
    p.CompleteCalls = append(p.CompleteCalls, CompleteCall{Ctx: ctx, Req: req})
    return p.CompleteResponse, p.CompleteErr
}
```

### When to use mocks vs real implementations

| Situation | Approach |
| ---------------------------------------- | ------------------------------------------- |
| Unit-testing orchestration/business logic | Use mocks from `<pkg>/mock/` |
| Testing a provider adapter (e.g., OpenAI)| Test the adapter directly against its API types |
| Testing database logic | Use mock DB types (function-injection pattern) |
| Integration tests against Postgres | Use Docker Compose (see below) |

---

## :electric_plug: Testing Providers

Provider implementations in `pkg/provider/` follow a consistent pattern. Each
provider package tests:

1. **Constructor validation** — empty keys, missing config, unsupported options.
2. **Message/data conversion** — verifying that internal types map correctly to
   the provider's wire format.
3. **Capabilities** — model capability lookups return correct values.
4. **Error propagation** — API errors are wrapped with the package prefix.

Example from `pkg/provider/llm/anyllm/anyllm_test.go`:

```go
func TestNew_EmptyProviderName(t *testing.T) {
    t.Parallel()
    _, err := New("", "gpt-4o")
    if err == nil {
        t.Fatal("expected error for empty providerName")
    }
}

func TestConvertMessage_AssistantWithToolCalls(t *testing.T) {
    t.Parallel()
    m := llm.Message{
        Role: "assistant",
        ToolCalls: []llm.ToolCall{
            {ID: "call_1", Name: "get_weather", Arguments: `{"city":"Berlin"}`},
        },
    }
    got := convertMessage(m)
    if len(got.ToolCalls) != 1 {
        t.Fatalf("expected 1 tool call, got %d", len(got.ToolCalls))
    }
    if got.ToolCalls[0].Function.Name != "get_weather" {
        t.Errorf("expected function name get_weather, got %q", got.ToolCalls[0].Function.Name)
    }
}
```

When adding a new provider, follow the same structure: constructor tests,
conversion tests, capabilities tests, and error tests. Use the existing provider
test files as templates.

---

## :hammer_and_wrench: Testing MCP Tools

MCP tool handlers follow the function signature
`func(ctx context.Context, args string) (string, error)`. Tests should cover:

1. **Valid inputs** — verify JSON output structure and values.
2. **Invalid inputs** — bad JSON, missing required fields, empty values.
3. **Error propagation** — injected store/graph errors bubble up correctly.
4. **Tool definitions** — the `Tools()` / `NewTools()` function returns the right
   number of tools with non-nil handlers and positive latency declarations.

Pattern from `internal/mcp/tools/diceroller/diceroller_test.go`:

```go
func TestRollHandler_Valid(t *testing.T) {
    t.Parallel()

    tests := []struct {
        name      string
        args      string
        wantCount int
        minTotal  int
        maxTotal  int
    }{
        {"1d1", `{"expression":"1d1"}`, 1, 1, 1},
        {"2d6+3", `{"expression":"2d6+3"}`, 2, 5, 15},
    }

    ctx := context.Background()
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            t.Parallel()
            out, err := rollHandler(ctx, tt.args)
            if err != nil {
                t.Fatalf("rollHandler(%q) unexpected error: %v", tt.args, err)
            }
            var res rollResult
            if err := json.Unmarshal([]byte(out), &res); err != nil {
                t.Fatalf("failed to unmarshal: %v", err)
            }
            if len(res.Rolls) != tt.wantCount {
                t.Errorf("len(Rolls) = %d, want %d", len(res.Rolls), tt.wantCount)
            }
            if res.Total < tt.minTotal || res.Total > tt.maxTotal {
                t.Errorf("Total = %d, want in [%d, %d]", res.Total, tt.minTotal, tt.maxTotal)
            }
        })
    }
}
```

For tools that depend on a store or graph (like the memory tools), inject the
mock from `pkg/memory/mock/`:

```go
func TestSearchSessions_StoreError(t *testing.T) {
    t.Parallel()
    store := &mock.SessionStore{
        SearchErr: errors.New("database unavailable"),
    }
    handler := makeSearchSessionsHandler(store)

    _, err := handler(context.Background(), `{"query":"anything"}`)
    if err == nil {
        t.Error("expected error from store")
    }
}
```

Always verify the tool definition surface area:

```go
func TestTools(t *testing.T) {
    t.Parallel()
    ts := Tools()
    if len(ts) != 2 {
        t.Fatalf("Tools() returned %d tools, want 2", len(ts))
    }
    for _, tool := range ts {
        if tool.Handler == nil {
            t.Errorf("tool %q has nil Handler", tool.Definition.Name)
        }
        if tool.DeclaredP50 <= 0 {
            t.Errorf("tool %q DeclaredP50 should be > 0", tool.Definition.Name)
        }
    }
}
```

---

## :whale: Integration Testing

For tests that require external services (PostgreSQL with pgvector, Ollama,
Coqui TTS), use the Docker Compose stack in `deployments/compose/`:

```bash
# Start PostgreSQL only (default profile)
cd deployments/compose
docker compose up -d postgres

# Run tests that need a real database
GLYPHOXA_TEST_POSTGRES_DSN="postgres://glyphoxa:glyphoxa@localhost:5432/glyphoxa?sslmode=disable" \
    go test -race -count=1 ./pkg/memory/postgres/...

# Start the full local stack (Ollama, Coqui TTS, Whisper models)
docker compose --profile local up -d
```

### Unit tests with mock DB

Most database-touching code is tested with mock DB types rather than a live
database. The pattern uses function injection on a mock struct:

```go
type mockDB struct {
    queryRowFunc func(ctx context.Context, sql string, args ...any) pgx.Row
    queryFunc    func(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
    execFunc     func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func (m *mockDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
    if m.queryRowFunc != nil {
        return m.queryRowFunc(ctx, sql, args...)
    }
    return &mockRow{scanFunc: func(dest ...any) error { return pgx.ErrNoRows }}
}
```

This lets you verify exact SQL queries, argument order, and error paths without
a running database.

### Guard integration tests with build tags or env vars

When writing tests that require a live service, skip them when the service is not
available:

```go
func TestPostgresIntegration(t *testing.T) {
    dsn := os.Getenv("GLYPHOXA_TEST_POSTGRES_DSN")
    if dsn == "" {
        t.Skip("GLYPHOXA_TEST_POSTGRES_DSN not set, skipping integration test")
    }
    t.Parallel()
    // ... connect and test against the real database ...
}
```

---

## :jigsaw: Common Test Patterns

### Context cancellation

Verify that your code respects context cancellation and does not leak
goroutines:

```go
func TestProcess_ContextCancelled(t *testing.T) {
    t.Parallel()

    ctx, cancel := context.WithCancel(context.Background())
    cancel() // cancel immediately

    _, err := engine.Process(ctx, input, prompt)
    if err == nil {
        t.Fatal("expected error for cancelled context")
    }
}
```

### Concurrent access

Test that public methods are safe under concurrent use. The race detector will
flag any issues:

```go
func TestConcurrentProcess(t *testing.T) {
    t.Parallel()

    const numGoroutines = 8
    e := buildTestEngine()
    t.Cleanup(func() { _ = e.Close() })

    var wg sync.WaitGroup
    errs := make([]error, numGoroutines)

    for i := range numGoroutines {
        wg.Add(1)
        go func(idx int) {
            defer wg.Done()
            resp, err := e.Process(context.Background(), input, prompt)
            if err != nil {
                errs[idx] = err
                return
            }
            drainAudio(resp.Audio)
        }(i)
    }

    wg.Wait()
    for i, err := range errs {
        if err != nil {
            t.Errorf("goroutine %d: %v", i, err)
        }
    }
}
```

### Error case testing

Every error path deserves a test. Verify both the error itself and its message
prefix:

```go
func TestCreate_DBError(t *testing.T) {
    t.Parallel()
    db := &mockDB{
        queryRowFunc: func(_ context.Context, _ string, _ ...any) pgx.Row {
            return &mockRow{
                scanFunc: func(_ ...any) error {
                    return errors.New("connection lost")
                },
            }
        },
    }
    store := NewPostgresStore(db)
    err := store.Create(context.Background(), &NPCDefinition{ID: "x", Name: "X"})
    if err == nil {
        t.Fatal("expected error, got nil")
    }
    if !strings.Contains(err.Error(), "npcstore: create:") {
        t.Errorf("error = %q, want prefix 'npcstore: create:'", err.Error())
    }
}
```

### Idempotency

Operations like `Close()` and `Stop()` must be safe to call multiple times:

```go
func TestClose_Idempotent(t *testing.T) {
    t.Parallel()
    e := buildTestEngine()
    for i := range 5 {
        if err := e.Close(); err != nil {
            t.Errorf("Close() call %d: unexpected error: %v", i, err)
        }
    }
}
```

### File watcher / polling tests

When testing components that react to filesystem changes, use `t.TempDir()` for
isolation and `time.After` for timeouts instead of fixed sleeps:

```go
func TestWatcher_DetectsChange(t *testing.T) {
    t.Parallel()
    dir := t.TempDir()
    cfgPath := filepath.Join(dir, "config.yaml")
    writeFile(t, cfgPath, initialYAML)

    called := make(chan struct{}, 1)
    w, err := config.NewWatcher(cfgPath, func(old, new *config.Config) {
        called <- struct{}{}
    }, config.WithInterval(50*time.Millisecond))
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    defer w.Stop()

    time.Sleep(100 * time.Millisecond)
    writeFile(t, cfgPath, updatedYAML)

    select {
    case <-called:
        // success
    case <-time.After(2 * time.Second):
        t.Fatal("callback was not invoked within timeout")
    }
}
```

---

## :link: See also

- [Getting Started](getting-started.md) — prerequisites, build, and first run
- [Providers](design/02-providers.md) — LLM, STT, TTS, Audio provider interfaces
- [Contributing](https://github.com/MrWong99/glyphoxa/blob/main/CONTRIBUTING.md) — development workflow, code style, PR process
