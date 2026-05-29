# AGENTS.md

## Fast Start (Verified)
- Go version is `1.25.1` (`go.mod`); SQLite uses `mattn/go-sqlite3` (CGO required).
- Main runnable targets are utility entrypoints under `cmd/` (downloaders, analyzers, dashboards).
- Run full test suite: `go test ./...`
- Run focused WB adaptive limiter test: `go test ./pkg/wb/ -run TestAdaptiveRateLimit_ReducesOn429 -v`
- Build one utility fast: `go build ./cmd/data-analyzers/check-card-consistency/`
- Build all cmd utilities: `go build ./cmd/...`

## Pipeline Commands That Matter
- Full WB refresh: `bash download-all.sh [days]`
  - Uses lock file: `.download-all.lock`
  - Writes to: `/var/db/wb-sales.db`
  - Phase order in script is intentional: Catalog → Feedbacks → Sales & Revenue → Stock & Logistics → Advertising → Analytics
- Daily VPS pipeline: `run-daily-analytics.sh`
  - Expects prebuilt binaries in `bin/`
  - Loads env from `.env`
  - Runs non-fatal `git pull` before downloads
  - Runs MA builder after downloads

## Config & Path Conventions
- Config loader expands env vars directly from YAML (`config.LoadYAML` + `os.ExpandEnv`), so `${VAR}` placeholders are first-class.
- Utility configs for full refresh live in `cmd/.configs/download-all/*.yaml`.
- Most utilities accept `--config` and default to `config.yaml`.
- `pkg/app/standalone.go` strict mode resolves config next to binary (not CWD), validates prompts dir, and fails fast if prompt files are missing.

## Architecture Guardrails (Do Not Break)
- Package boundaries are strict:
  - `pkg/` = reusable library code
  - `internal/` = app-specific logic
  - `cmd/` = entrypoint/orchestration
- Tool interface is immutable:
  - `Definition() ToolDefinition`
  - `Execute(ctx, argsJSON string) (string, error)`
- Tools must be registered through `Registry.Register()` (not called directly).
- LLM-dependent business logic should depend on `llm.Provider`, not provider SDKs directly.
- Prefer error returns over panic in business flow.

## WB Client / Downloader Gotchas
- `wb.NewFromConfig(...)` creates client defaults but per-tool adaptive limiter behavior depends on explicit `SetRateLimit(toolID, ...)`.
- `toolID` must match between limiter setup and request path usage; mismatches create separate limiter state.
- Analytics/search endpoints are configured around strict low limits (notably 3 req/min for funnel/search visibility related flows); keep this in mind when changing batch/concurrency.

## Development Guides (by scenario)

Full map with priorities → `dev_manifest.md` → "Карта документов разработчика".

| Scenario | Document |
|----------|----------|
| General Go, code review | `dev_solid.md` |
| Project architecture (immutable) | `dev_manifest.md` |
| Code placement, patterns | `dev_best_practices.md` |
| WB API write-utilities, sandboxes | `dev_swagger_reusable_packages.md` |
| Downloader migration v1→v2 | `dev_utils.md` |

Rule: more specific document overrides more general.

## WB API Safety

**MASS WRITES TO WB API ARE FORBIDDEN.** Never generate or execute code that performs bulk updates via WB Content API or any write endpoint. Only the user may run write operations manually. For write-utility development see `dev_swagger_reusable_packages.md`.

## DB Reality Check Before Writes
- Scripted downloader target DB is usually `/var/db/wb-sales.db`.
- Some analyzers use other DBs (example: `cmd/data-analyzers/check-card-consistency/config.yaml` points to `/mnt/d/db/card-analysis.db`).
- Before running write operations, confirm the exact DB path in the utility config being used.

## Gotchas
- ToolID mismatch (`SetRateLimit("tool_A")` + `Get("tool_B")`) creates separate limiter with no adaptive state
- Analytics v3 API: **3 req/min** — most restrictive endpoint (shared with Seller Analytics v2 search-report)
- `client.IsDemoKey()` returns true for `demo_key`, enables mock responses
- API keys are separate: `WB_API_KEY` (content/analytics/ad), `WB_API_FEEDBACK_KEY` (feedbacks), `WB_STAT_API_KEY` (statistics), WB_API_CONTENT_KEY (только контент)
- Seller Analytics v2 (`seller-analytics-api.wildberries.ru`): search-report endpoints, 3 req/min
- Promotion V2 normquery stats: **10 req/min** (stricter than list/bids/minus at 5/sec)
- Date columns in `sales` table use `_dt` suffix: `order_dt`, `sale_dt`, `rr_dt` (not `sale_date`)
- год выпуска (производства) товара определяется по 2 и 3 символам в артикуле продавца, например: 12345678 -> 2023 год
- в API WB при обновлении карточки товара при записи нужно передавать ВСЕ ПОЛЯ! Иначе, которые не передал, они обнулятся. Это критический момент для любых проверок утилит!
- Поле season в onec_goods — более надёжный фильтр для школьного ассортимента, чем collection