# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Stack
- Language: Go 1.25 (CGo for SQLite via mattn/go-sqlite3)
- Storage: SQLite (WAL mode), S3
- TUI: charmbracelet/bubbletea
- AI: LLM-agnostic via Provider interface (Zai, OpenRouter, OpenAI)
- External APIs: Wildberries (Statistics, Content, Analytics v3, Feedbacks, Advertising), 1C/PIM

## Commands
- Run all tests: `go test ./...`
- Single package: `go test ./pkg/wb/ -v`
- Single test: `go test ./pkg/wb/ -run TestAdaptiveRateLimit_ReducesOn429 -v`
- Coverage: `go test ./... -coverprofile=coverage.out`
- Main TUI: `go run cmd/poncho/main.go`
- Simple agent: `go run cmd/simple-agent/main.go "show categories"`
- PDF generation: `/tmp/pdf_venv/bin/python reports/database_tables_pdf.py`

## Architecture
- **pkg/** = reusable library, **internal/** = app-specific, **cmd/** = entry points (Rule 6)
- `pkg/` must NOT import from `internal/`. Violation = architecture bug.
- Tool interface is **immutable**: `Definition() ToolDefinition` + `Execute(ctx, argsJSON string) (string, error)`
- "Raw In, String Out" — tools receive raw JSON from LLM, return strings
- Tools register via `Registry.Register()`, never called directly
- LLM access only through `Provider` interface, no direct API calls in business logic
- State is layered + thread-safe (sync.RWMutex), no global variables
- `cmd/` utilities are autonomous — config, prompts, logs live next to executable (Rule 13)

### Data Flow
```
Agent (pkg/agent) → ReActCycle (pkg/chain) → LLM (pkg/llm Provider)
                    ↓ Tool calls
              Registry (pkg/tools) → Tool.Execute()
                    ↓ Events
              Emitter (pkg/events) → Subscriber (pkg/tui)
```

### Key Packages
- `pkg/chain/` — ReAct cycle: immutable template (ReActCycle) + per-call state (ReActExecution)
- `pkg/agent/` — Facade: `client.Run(ctx, query)`
- `pkg/app/` — DI container: `Initialize()` creates all components, wires dependencies
- `pkg/app/tool_setup.go` — Config-driven tool registration (YAML + switch case)
- `pkg/wb/` — WB API SDK: pagination, rate limiting, response unwrapping (not just HTTP client)
- `pkg/storage/sqlite/` — Repository pattern, 30 tables, schemas in `*_schema.go` files
- `pkg/events/` + `pkg/tui/` — Port & Adapter decoupling (tui implements events interfaces)
- `pkg/chain/bundle_resolver.go` — Token optimization: 100 tools → 10 bundles (~98% savings)
- `pkg/prompts/` — Source pattern with fallback: File → Default → API → Database

### Extending the Framework
- New platform (e.g., Ozon): add to `config.yaml` + switch case in `registerTool()`
- New downloader: copy structure from `cmd/data-downloaders/`, reuse `pkg/config/utility.go` types
- New tool: implement `Tool` interface, register in `registerTool()`

## WB Client & Rate Limiting
Setup is **mandatory** for all downloaders:
```go
client := wb.New(apiKey)
client.SetRateLimit("tool_id", desiredRate, desiredBurst, apiRate, apiBurst)
```
`NewFromConfig()` does NOT set adaptive rate limits — always call `SetRateLimit()` explicitly.

Recovery cycle: `desired` → 429 triggers backoff → `api floor` (after 5 OKs) → `probe desired` (after 10 OKs).

### Gotchas
- ToolID mismatch (`SetRateLimit("tool_A")` + `Get("tool_B")`) creates separate limiter with no adaptive state
- Analytics v3 API: **3 req/min** — most restrictive endpoint
- `client.IsDemoKey()` returns true for `demo_key`, enables mock responses
- API keys are separate: `WB_API_KEY` (content/analytics/ad), `WB_API_FEEDBACK_KEY` (feedbacks), `WB_STAT_API_KEY` (statistics)

## Testing
- Unit: `mockHTTPClient` for wb.Client internals, `MockClient`/`MockPromotionClient` for downloader logic
- E2E: `SnapshotDBClient` reads from SQLite instead of API — `wb.NewSnapshotDBClient("e2e-snapshot.db")`
- E2E collector: `examples/e2e-mock-collector/` — collection order: Sales first → funnel → campaigns → feedbacks

## 1C/PIM Integration
Streaming JSON decode → SQLite: `onec_goods`, `onec_goods_sku`, `onec_prices`, `pim_goods`.
Mapping: `onec_prices(good_guid) → onec_goods(guid) → article → cards.vendor_code → cards.nm_id`

## Environment Variables
| Variable | Purpose |
|----------|---------|
| `ZAI_API_KEY` | LLM provider |
| `WB_API_KEY` | WB Content, Analytics, Advertising APIs |
| `WB_API_ANALYTICS_AND_PROMO_KEY` | Analytics + Advertising (alternative) |
| `WB_API_FEEDBACK_KEY` | WB Feedbacks API (separate key) |
| `WB_STAT_API_KEY` | WB Statistics API |
| `S3_ACCESS_KEY` / `S3_SECRET_KEY` | S3 storage |
| `OPENROUTER_API_KEY` | OpenRouter (LLM gateway for analyzers) |
| `ONEC_API_URL` / `ONEC_PIM_URL` | 1C/PIM catalog APIs |

## Compact Instructions
When compacting, preserve: goal, changed files, failing command, current hypothesis, test results, next exact command.
