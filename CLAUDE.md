# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Poncho AI is a **Go-based LLM-agnostic, tool-centric framework** for building AI agents with ReAct pattern.

**Key Philosophy**: "Raw In, String Out" — tools receive raw JSON from LLM and return strings.

**Architecture**:
- `pkg/state/CoreState` — Framework core (reusable, e-commerce helpers)
- `pkg/tui/` — TUI components with primitives layer (BaseModel, InterruptionModel)
- `pkg/chain/ReActCycle` — Chain + Agent interfaces
- `pkg/app/components.go` — Context propagation (Rule 11)
- `pkg/app/tool_setup.go` — OCP: Config-driven tool setup
- `pkg/agent/Client` — Simple 2-line agent API (Facade)
- `pkg/events/` — Port & Adapter for UI decoupling
- `pkg/prompts/` — OCP: Prompt loading with source pattern
- `pkg/chain/bundle_resolver.go` — Token optimization (98% savings)
- Rule 6 Compliant: `pkg/` has NO imports from `internal/`

---

## The 13 Immutable Rules

| Rule | Description |
|------|-------------|
| **0: Code Reuse** | Use existing solutions first — see [dev_best_practices.md](dev_best_practices.md) |
| **1: Tool Interface** | NEVER change — `Definition() ToolDefinition`, `Execute(ctx, argsJSON string) (string, error)` |
| **2: Configuration** | YAML with ENV support only |
| **3: Registry** | All tools via `Registry.Register()` |
| **4: LLM Abstraction** | Work through `Provider` interface only |
| **5: State** | Layered, thread-safe, no globals |
| **6: Package Structure** ⭐ | `pkg/` = reusable, `internal/` = app-specific, `cmd/` = entry points |
| **7: Error Handling** | No `panic()` in business logic |
| **8: Extensibility** | Add via tools, LLM adapters, config |
| **9: Testing** | Use CLI utilities in `/examples` for verification |
| **10: Documentation** | Godoc on public APIs |
| **11: Context Propagation** | All long-running ops accept `context.Context` |
| **12: Security & Secrets** | Never hardcode secrets, use ENV, HTTPS only |
| **13: Resource Localization** | Autonomous `/cmd` and `/examples` apps |

### Rule 6: Package Structure (Port & Adapter)

```
pkg/       — Library code, ready for reuse
internal/  — Application-specific logic
cmd/       — Entry points, test utilities only
```

- Library (`pkg/`) defines Port interface (`events.Emitter`, `events.Subscriber`)
- Adapter (`pkg/tui`) implements Port (Rule 6 compliant: no imports from `pkg/agent`, `pkg/chain`)
- Business logic via **callback pattern** from `cmd/`

---

## Architecture

### High-Level Structure

```
poncho-ai/
├── cmd/                    # Production utilities
│   ├── data-downloaders/   # Data collection from APIs
│   ├── data-analyzers/     # Data analysis & computation
│   └── fix-utilities/      # Data fix/cleanup tools
├── examples/              # Verification & demos (Rule 9)
├── internal/              # App-specific logic
├── pkg/                   # Reusable library packages
├── prompts/               # Prompt templates
└── config.yaml            # Main config
```

### Core Data Flow

```
Agent (pkg/agent) → ReActCycle (pkg/chain) → LLM (pkg/llm Provider)
                    ↓ Tool calls
              Registry (pkg/tools) → Tool.Execute(ctx, argsJSON)
                    ↓ Events
              Emitter (pkg/events) → Subscriber (pkg/tui)
```

### Port & Adapter

- `pkg/events` — Port (Emitter, Subscriber interfaces)
- `pkg/tui` — Adapter (implements Subscriber)
- `pkg/agent` — Library (uses Emitter)

### Event System

Event types: `EventThinking`, `EventThinkingChunk`, `EventToolCall`, `EventToolResult`, `EventUserInterruption`, `EventMessage`, `EventError`, `EventDone`

Flow: Emission → Transport (channel) → Subscription → Conversion → Processing → Rendering

---

## Core Components

### Tool System (`pkg/tools/`)

```go
type Tool interface {
    Definition() ToolDefinition
    Execute(ctx context.Context, argsJSON string) (string, error)
}
```

**Categories**: WB API (Content, Analytics v3, Feedbacks, Advertising), S3 (basic/batch/download), Vision, Planner.

**Registration** — Config-driven via `pkg/app/tool_setup.go`:
- `SetupToolsFromConfig()` iterates `cfg.ToolCategories`, calls `registerTool()` for each
- Adding new platform (e.g., Ozon): add to `config.yaml` + add switch case to `registerTool()`

### State Management (`pkg/state/`)

Repository Pattern with type-safe operations.

**Generic Helpers** (`pkg/state/generic.go`): `GetType[T]()`, `SetType[T]()`, `UpdateType[T]()`

**Repository Interfaces**: `MessageRepository`, `FileRepository`, `TodoRepository`, `DictionaryRepository`, `StorageRepository`, `ToolsRepository`

### Client Storage

| Client | Stored In | Access |
|--------|-----------|--------|
| **S3 Client** | `CoreState.store` | `state.GetStorage()` |
| **LLM Providers** | `ModelRegistry` | `modelRegistry.Get()` |
| **WB Client** | DI (not in State) | Passed to tools |

### ReActCycle (`pkg/chain/`)

Template-Execution separation with Observer pattern:
- `ReActCycle` — Immutable template (thread-safe for concurrent `Execute()`)
- `ReActExecution` — Per-call runtime state (never shared)
- `ReActExecutor` — Orchestrator with extracted methods

### Simple Agent API (`pkg/agent/`)

```go
client, _ := agent.New(agent.Config{ConfigPath: "config.yaml"})
result, _ := client.Run(ctx, query)
```

### App Initialization (`pkg/app/`)

```go
components, err := app.Initialize(ctx, cfg, 10, "")
result, err := app.Execute(ctx, components, query, timeout)
```

Returns `Components` struct with: Config, State, ModelRegistry, VisionLLM, WBClient, Orchestrator.

### Bundle System (`pkg/chain/bundle_resolver.go`)

Token optimization: 100 tools (~15K tokens) → 10 bundles (~300 tokens). Flow: LLM calls bundle name → expandBundle() → inject real definitions → re-run LLM.

### Prompt System (`pkg/prompts/`)

Source pattern with fallback chain: File sources (YAML) → Default source (Go code). `PromptSource` interface with implementations: `FileSource`, `DefaultSource`, `APISource`, `DatabaseSource`.

### LLM Abstraction (`pkg/llm/`)

**Options Pattern**:
```go
llm.Generate(ctx, messages, llm.WithModel("glm-4.6"), llm.WithTemperature(0.5))
```

**StreamingProvider** extends base `Provider` with `GenerateStream()` for real-time responses.

**StreamChunk Types**: `ChunkThinking` (reasoning), `ChunkContent` (response), `ChunkError`, `ChunkDone`.

Features: opt-out design (streaming by default), thinking mode support (Zai GLM), thread-safe callback.

### Model Registry (`pkg/models/`)

Centralized LLM provider management with dynamic switching. Thread-safe via `sync.RWMutex`. Fallback: `GetWithFallback(requested, default)`. Runtime model switching via post-prompts.

### Preset System (`pkg/app/presets.go`)

Quick app launch with predefined configurations.

**Built-in Presets**: `simple-cli` (minimal CLI), `interactive-tui` (full TUI + streaming), `full-featured` (all features).

```go
client, _ := agent.NewFromPreset(ctx, "interactive-tui", "config.yaml")
// Custom:
app.RegisterPreset("my-app", &app.PresetConfig{Type: app.AppTypeTUI, ...})
```

### Interruption Mechanism

User can interrupt execution in real-time with a message.

**Flow**: User → TUI → `inputChan` (buffered, size=10) → ReActExecutor (between iterations) → `loadInterruptionPrompt()` (YAML or fallback) → Emit `EventUserInterruption` → TUI

Key: non-blocking checks via `select` with `default`, YAML config at `chains.default.interruption_prompt`.

### Design Patterns

| Pattern | Location | Purpose |
|---------|----------|---------|
| **Facade** | `pkg/agent/Client` | Simple 2-line API |
| **Port & Adapter** | `pkg/events/`, `pkg/tui/` | UI decoupling |
| **Callback** | `pkg/tui/` | Business logic injection (Rule 6) |
| **Repository** | `pkg/state/` | Unified storage |
| **Registry** | `pkg/tools/`, `pkg/models/` | Registration/discovery |
| **Factory** | `pkg/models/`, `pkg/app/tool_setup.go` | LLM/tool creation |
| **Options** | `pkg/llm/` | Runtime parameter overrides |
| **DI** | `pkg/app/`, `pkg/tools/std/` | Client injection |
| **ReAct** | `pkg/chain/` | Agent reasoning loop |
| **Template-Execution** | `pkg/chain/` | Immutable + runtime state |
| **Observer** | `pkg/chain/` | Cross-cutting concerns |
| **Streaming** | `pkg/llm/StreamingProvider` | Real-time responses |
| **Fallback** | `pkg/prompts/source_registry.go` | Prompt source chain |
| **Source** | `pkg/prompts/` | OCP: extensible prompt loading |

---

## Building and Running

```bash
# Main TUI
go run cmd/poncho/main.go

# Simple agent
go run cmd/simple-agent/main.go "show categories"

# Run all tests
go test ./...

# Run specific package
go test ./pkg/wb/ -v

# Run specific test
go test ./pkg/wb/ -run TestAdaptiveRateLimit_ReducesOn429 -v

# Run with coverage
go test ./... -coverprofile=coverage.out
```

### cmd/data-downloaders/ — Data collection from external APIs

| Utility | Purpose |
|---------|---------|
| `download-wb-sales` | Sales + funnel metrics by period |
| `download-wb-promotion` | Campaigns + daily stats (4-level hierarchy) |
| `download-wb-feedbacks` | Feedbacks + questions (39 fields) |
| `download-wb-funnel` | Analytics v3 funnel (daily per product) |
| `download-wb-funnel-agg` | Analytics v3 aggregated funnel |
| `download-wb-cards` | Content API cards (cursor pagination) |
| `download-wb-prices` | Discounts-Prices API (offset pagination) |
| `download-wb-region-sales` | Region-level sales (31-day horizon) |
| `download-wb-stocks` | Warehouse stock snapshots |
| `download-wb-stock-history` | Historical stock CSV reports |
| `download-wb-supplies` | FBW supply tracking |
| `download-1c-data` | 1C/PIM catalog + prices (streaming JSON) |
| `download-all-articles` | S3 article processing |

### cmd/data-analyzers/ — Data analysis & computation

| Utility | Purpose |
|---------|---------|
| `analyze-wb-feedbacks` | Feedback quality analysis via OpenRouter (two-level LLM aggregation) |
| `build-ma-snapshots` | Daily product MA-3/7/14/28 snapshots for PowerBI |
| `build-ma-sku-snapshots` | SKU-level stock analysis with regional MA, risk flags, supply incoming |
| `1c_mktpl_mapping` | Maps products between 1C, PIM, and marketplace systems (barcode + PIM) |
| `compare-wb-1c-prices` | Compares 1C retail prices with WB marketplace prices |
| `check-db-freshness` | Checks data freshness in SQLite tables against configurable thresholds |

### cmd/fix-utilities/

- `fix-fake-png` — Fix PNG/JSON file naming
- `migrate-feedbacks-to-unified` — One-time migration: feedbacks.db + quality_reports.db → unified wb-sales.db

---

## WB Client & Rate Limiting

**Setup** (required for all downloaders):
```go
client := wb.New(apiKey)
client.SetRateLimit("tool_id", desiredRate, desiredBurst, apiRate, apiBurst)
client.SetAdaptiveParams(recoverAfter, probeAfter, maxBackoff)
```

**Critical**: `NewFromConfig()` does NOT use `WBConfig.RateLimit` fields. Always call `SetRateLimit()` explicitly.

**Recovery cycle**: `desired` → 429 → `api floor` (after 5 OKs) → `probe desired` (after 10 OKs) → repeat.

**Common pitfalls** (full guide: [dev_limits.md](dev_limits.md)):
1. Forgetting `SetRateLimit()` → limiter never reduces
2. **ToolID mismatch** — `SetRateLimit("tool_A")` + `Get("tool_B")` creates separate limiter with no adaptive state

**HTTP Methods**: `Get()` (buffered), `GetStream()` (streaming, for large payloads), `Post()` (request bodies).

**Demo Mode**: `client.IsDemoKey()` returns `true` for `demo_key`, enabling mock responses.

**API Rate Limits**:

| API | Rate Limit | Key |
|-----|-----------|-----|
| Statistics | 100/min | `WB_STAT_API_KEY` |
| Content | 100/min | `WB_API_KEY` |
| Analytics v3 | **3/min** (CRITICAL) | `WB_API_KEY` |
| Advertising | 100/min (list), 20/min (stats) | `WB_API_KEY` |
| Feedbacks | 3 req/sec | `WB_API_FEEDBACK_KEY` |

---

## Testing

### Test Layers

**Unit tests** (`pkg/`): Mock HTTP client (`mockHTTPClient`) for wb.Client internals (rate limiting, retries). Mock service clients (`MockClient`, `MockPromotionClient`) for downloader logic.

**E2E tests** (`cmd/`): Test downloader logic with mock clients + SQLite.

### Two Mock Levels

| Mock | Tests | Skips |
|------|-------|-------|
| `mockHTTPClient` (HTTPClient) | `doRequest()` retry loop, `adaptiveReduce()` | Service layer |
| `MockClient` / `MockPromotionClient` (Service) | Batch logic, DB save, resume | Rate limiting |

### E2E Snapshot Testing

`SnapshotDBClient` (`pkg/wb/snapshot_client.go`) reads from SQLite instead of WB API. Use `wb.NewSnapshotDBClient("e2e-snapshot.db")` for fast, deterministic tests.

Collector: `examples/e2e-mock-collector/` — collects real API data into SQLite. **Collection order matters**: Sales first (extracts nmIDs), then funnel, campaigns, feedbacks.

---

## Environment Variables

| Variable | Purpose |
|----------|---------|
| `ZAI_API_KEY` | LLM provider |
| `S3_ACCESS_KEY` / `S3_SECRET_KEY` | Storage |
| `WB_API_KEY` | WB Content, Analytics, Advertising APIs |
| `WB_API_ANALYTICS_AND_PROMO_KEY` | Analytics + Advertising (alternative) |
| `WB_API_FEEDBACK_KEY` | WB Feedbacks API (separate key) |
| `WB_STAT_API_KEY` | WB Statistics API (optional) |
| `OPENROUTER_API_KEY` | OpenRouter (LLM gateway for analyzers) |
| `ONEC_API_URL` | 1C Goods+Prices API URL with basic auth |
| `ONEC_PIM_URL` | PIM Goods API URL with basic auth |

---

## Thread Safety

All core components use `sync.RWMutex`: CoreState, ModelRegistry, ToolsRegistry, WB Client, TodoManager, TUI MainModel.

ReActCycle: multiple `Execute()` calls safe (immutable template). ReActExecution: per-call (never shared). No global mutex during LLM or tool calls.

---

## Database Schema

**SQLite** with 30 tables across 9 categories. Schema files in `pkg/storage/sqlite/`:
`schema.go`, `onec_schema.go`, `cards_schema.go`, `prices_schema.go`, `region_sales_schema.go`, `stock_history_schema.go`

**Design patterns**:
- Composite natural keys for UNIQUE (e.g., `nm_id + date`) → safe upserts via `INSERT OR REPLACE`
- Surrogate keys for FK relationships
- Partial indexes for sparse fields, CASCADE DELETE on card tables

All tables created in `pkg/storage/sqlite/repository.go` via `initSchema()`.

---

## 1C/PIM Integration

Fetches product catalog and prices from 1C accounting + PIM validation systems via streaming JSON decode.

**SQLite tables**: `onec_goods` (product dict), `onec_goods_sku` (size variants), `onec_prices` (25 price types), `pim_goods` (24 columns + values_json)

**1C → WB mapping**: `onec_prices(good_guid) → onec_goods(guid) → article → cards.vendor_code → cards.nm_id`

**Files**: `cmd/data-downloaders/download-1c-data/`, `pkg/storage/sqlite/onec_*.go`

---

## Key Dependencies

- `github.com/charmbracelet/bubbletea` — TUI framework
- `github.com/minio/minio-go/v7` — S3 client
- `github.com/sashabaranov/go-openai` — OpenAI API
- `github.com/mattn/go-sqlite3` — SQLite driver (CGo)
- `golang.org/x/time/rate` — Rate limiting
- `gopkg.in/yaml.v3` — YAML config parsing

---

## PDF Documentation

Use **reportlab** with DejaVu Sans font for Cyrillic. Guide: [`dev_pdf.md`](dev_pdf.md).

```bash
/tmp/pdf_venv/bin/python reports/database_tables_pdf.py  # Generate schema reference PDF
```

---

**Last Updated**: 2026-04-17
**Version**: 14.0 (consolidated, added missing utilities)
