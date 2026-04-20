# Go Best Practices & Architecture Guide

Companion to [CLAUDE.md](CLAUDE.md) — expands Rule 0 (Code Reuse) and architectural principles.

---

## Rule 0: Code Reuse First

Before writing new code, check existing solutions:

1. **`pkg/config/utility.go`** — shared config types for all `cmd/` utilities (PromotionConfig, DownloadConfig, FeedbacksConfig, FunnelConfig, WBClientConfig, OneCConfig)
2. **`pkg/storage/sqlite/`** — shared SQLite schemas, types, and repo implementations
3. **`pkg/wb/`** — WB client, service layer, mock clients, snapshot testing
4. **`pkg/state/generic.go`** — `GetType[T]()`, `SetType[T]()`, `UpdateType[T]()` for state operations
5. **`pkg/app/tool_setup.go`** — config-driven tool registration (add switch case, not new function)

When adding a new downloader or analyzer, copy structure from an existing one and reuse shared packages.

---

## Go-Specific Patterns

### Interfaces

- **Define interfaces where they're consumed**, not where they're implemented (Go convention)
- **Interface justified when ≥3 implementations** — e.g., `PromptSource` (File, API, Database)
- **Interface NOT justified when only 1 implementation** — e.g., tool categories (config-driven switch is enough)
- Keep interfaces small: `Tool` has 2 methods, `Emitter` has 1, `Provider` has 1

### Error Handling

- No `panic()` in business logic (Rule 7) — return `error` up the stack
- Wrap errors with context: `fmt.Errorf("fetch sales for %s: %w", date, err)`
- Use `errors.Is()` / `errors.As()` for error checking, not string matching
- Guard at system boundaries (user input, external APIs), trust internal code

### Concurrency

- **Thread-safe by default**: all shared state protected with `sync.RWMutex`
- **Use RWMutex** when reads >> writes (CoreState, registries)
- **Buffered channels** for inter-goroutine comms (inputChan size=10)
- **Non-blocking checks**: `select` with `default` case
- **No global mutex** during LLM or tool calls — let concurrent requests flow freely
- `context.Context` propagated everywhere (Rule 11) — enables cancellation and timeouts

### Generics

- Used for type-safe state operations: `GetType[T]()`, `SetType[T]()`, `UpdateType[T]()`
- Prefer generics over `interface{}` / `any` for type-safe helpers

---

## Architectural Principles

### SOLID as Guidelines, Not Dogma

- **SRP**: Functions should have clear single purposes. Extract when >50 lines or mixing concerns
- **OCP**: Config-driven extension (tools, prompts) — add to YAML + switch case, not new functions
- **LSP**: Not heavily applied — Go interfaces handle substitutability naturally
- **ISP**: Small, focused interfaces (`Tool` = 2 methods, `Emitter` = 1 method)
- **DIP**: Dependencies injected, not created inside (`pkg/app/` injects clients into tools)

### Package Structure (Rule 6)

```
pkg/       — Library code. NO imports from internal/. Must be reusable.
internal/  — App-specific logic. Can import pkg/.
cmd/       — Entry points only. Thin wrappers around pkg/ functionality.
```

When in doubt about where code belongs: if it could be used by another Go project, it goes in `pkg/`.

### Dependency Injection

- Clients (WB, S3, LLM) created in `pkg/app/Initialize()`, passed to tools via DI
- WB Client NOT stored in State — passed directly to tools that need it
- Mock clients injected via constructors (`wb.NewMockClient()`) or `SetHTTPClient()`

### Config over Code

- YAML-first: all settings in `config.yaml` with ENV variable overrides
- Go code provides sensible defaults when config is missing
- No hardcoded values for API endpoints, rate limits, or file paths

---

## Common Patterns in This Codebase

### Adding a New Downloader

1. Create `cmd/data-downloaders/download-X/` with `main.go`
2. Reuse config type from `pkg/config/utility.go` or add new one
3. Reuse storage schemas from `pkg/storage/sqlite/` or add new file
4. Call `client.SetRateLimit()` before API methods
5. Use `INSERT OR REPLACE` for idempotent upserts
6. Add to `download-all.sh` if applicable

### Adding a New Tool

1. Implement `Tool` interface in `pkg/tools/std/`
2. Add to `config.yaml` under appropriate category
3. Add case to `registerTool()` switch in `pkg/app/tool_setup.go`

### Adding a New LLM Provider

1. Implement `Provider` interface in `pkg/llm/`
2. Implement `StreamingProvider` if streaming supported
3. Register in model registry via `config.yaml`

---

## Testing Philosophy

- **Unit tests** for internal logic with mocked dependencies (`pkg/`)
- **E2E tests** for downloader workflows with mock clients + SQLite (`cmd/`)
- **Two mock levels**: HTTP-level (tests rate limiting) vs Service-level (tests business logic)
- Rule 9: Use CLI utilities for verification, not just unit tests
