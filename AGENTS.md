# AGENTS.md

## Fast Start (Verified)
- Go version is `1.26` (`go.mod`); SQLite uses `mattn/go-sqlite3` (CGO required). `modernc.org/sqlite` (pure-Go) is also a dependency, but the primary SQLite path is CGO `mattn`.
- No Makefile — all build/test is direct `go` / `npm` commands.
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

## Components Beyond the Go WB Utilities
The pipeline scripts above do NOT touch these — they are standalone tools introduced in recent commits. The scraper stack pairs a browser extension with a local Go collector.

### wbscraper snapshot push pipeline
- Library `pkg/wbscraper/` + CLI driver `cmd/data-downloaders/wb-scraper-collector/`. Runbooks: `inst.md` (quick) and `RUNBOOK.md` (long) in that dir.
- A loopback HTTP server (default `127.0.0.1:7780`). The **browser extension** makes the real storefront requests (anti-bot bypass); the server does NOT call the WB API itself. Endpoints: `GET /targets`, `POST /capture` (legacy v1), `POST /snapshot` (v2 push).
- `POST /snapshot` is **PostgreSQL-only** — the SQLite backend returns HTTP 501. `ReplaceSnapshot` does `DELETE WHERE snapshot_ts + INSERT`, so retries are idempotent.
- The `card.json` **decoder lives in the extension TS** (`extensions/poncho-wb-parser/src/decode/content.ts`), NOT in Go. The server persists exactly what the extension sends (`handleSnapshot` in `pkg/wbscraper/server.go`).
- PG schema is auto-created on startup: `search_queries` (DIMENSION, upsert by query text) + 11 append-only fact tables (`search_positions`, `vitrine_ads`, `competitor_cards`, …). Test DB only — `wb_data_test`, never prod `/var/db`.
- Flags: `--backend sqlite|postgres`, `--db` (SQLite path), `--pg-database`, `--generator static|llm`, `--addr`, `--mock`, `--dry-run`. Env: `PG_PWD`, `PGDATABASE`, `SQLITE_PATH`. `server.allowed_ips` is an IP allowlist (empty = allow all; safe only behind firewall/loopback).
- **Gotcha:** browser `query_id` (Dexie autoinc) ≠ server `query_id` (BIGSERIAL). The server re-resolves query text → server ID and remaps every fact row (`NoQuery`=0 → NULL) in `remapSnapshotQueryIDs`.

### Browser extension `poncho-wb-parser` (`extensions/poncho-wb-parser/`)
- Chrome **MV3**, TypeScript + Vite + `@crxjs/vite-plugin` + Dexie (IndexedDB) + xlsx. This is the only `package.json` in the repo.
- Build / test / typecheck:
  ```
  cd extensions/poncho-wb-parser
  npm install
  npm run build       # -> dist/, load unpacked in chrome://extensions (Dev mode)
  npm test            # vitest run
  npm run typecheck   # tsc --noEmit
  ```
- Older plain-JS `extensions/wb-scraper/` is MV3 too but has no build step — load `manifest.json` directly. Its IndexedDB name is `wb-scraper`; the v2 extension's is `poncho_wb_parser` (do not confuse them).
- The DB layer `src/db/` was previously **silently untracked** because `.gitignore` had an unanchored `db/` pattern; fixed to `/db/`. `PonchoDB` is at schema version 5.
- Data flow: `inject.ts (MAIN) →postMessage→ bridge (ISOLATED) → sw → offscreen → decode → db.bulkAdd`. The **offscreen document** is the long-lived MV3 context (service workers die ~30s idle) and owns DB writes.
- **Gotcha (build):** `vite.config.ts` MUST keep `sourcemap: false`. With maps on, `@crxjs` appends the IIFE wrapper close `})()` after the `//# sourceMappingURL=` comment on the same line → silent `SyntaxError: Unexpected end of input` → inject/bridge never run. `@crxjs` is conditionally disabled under vitest (`process.env.VITEST`).
- **Gotcha (dedup):** the `search_positions` dedup key MUST be `${query_id}|${region_dest}|${nm_id}|${page}`. The previous key omitted `query_id`+`region_dest`; since the `seen` set is shared across all queries in a snapshot, popular nms that re-ranked on the same page across similar constructor-generated queries collapsed to "first query only" in reports. Regression test: `tests/write.dedup.test.ts`.
- Optional push to the Go collector: only when `server_url` is configured in extension storage; a snapshot that can't ship stays in a persistent `pending_shipments` queue (deferred, not resolved — supports retroactive push).

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
- promotion-v2: the **active `download-all` configs** (`cmd/.configs/download-all/download-wb-promotion-v2.yaml` and `-PG.yaml`) are deliberately MORE conservative than the WB Swagger limits — e.g. `normquery_stats` 70/8 req·min vs Swagger's 10/min, burst 1. Do not assume the Swagger value is what's configured; document/keep both. `download-wb-promotion-v2/main.go` calls `client.SetRateLimit("normquery_stats", …)` — the ToolID must match (see ToolID-mismatch gotcha below).

## fix-utilities (`cmd/fix-utilities/`)
Point-fix tools that POST to `POST /content/v2/cards/update` (e.g. `fix-certificates`, `fix-card-fields`, plus siblings like `fix-card-dimensions`, `fix-fill-material-upper`, `fix-penalties-dims`, `fix-scrub-substring`). **Only the user runs `--apply`.**
- All use **Smart Merge**: keep every existing characteristic, replace only the target ones, always re-include `brand`/`title`/`description`/`dimensions`/`sizes`. This is mandatory because WB fully overwrites the card (see the "передавать ВСЕ ПОЛЯ" gotcha below).
- Common flags: `--stage`, `--dry-run`, `--diff`, `--apply`, `--check`. `fix-certificates` also has `--reconcile` and `--date DD.MM.YYYY`; `fix-card-fields` has `--list-chars <subject_id>`. Each fixer writes its own staging table (`fix_certificates_staging`, `fix_card_fields_staging`).
- **kizMarked gotcha (load-bearing):** `cards.kiz_marked` is 3-value logic (`*bool`, canonical impl `pkg/cardupdate/cardupdate.go` → `KizMarked *bool`, `LoadFullCard`). WB does NOT return `kizMarked` in `/content/v2/get/cards/list`, so most existing cards are `kiz_marked IS NULL` → the field is omitted (`omitempty`) → WB defaults to `false` → **resets "Честный ЗНАК" marking for `need_kiz=1` goods.** Before any `--apply`, always run:
  ```sql
  SELECT nm_id, vendor_code, subject_name FROM cards WHERE need_kiz=1 AND kiz_marked IS NULL;
  ```
  Start `--apply` with non-marked categories, or pre-populate `cards.kiz_marked=1`. Regression tests: `fix-card-fields/stage_test.go` (`TestKizMarkedPayloadSerialization`), `fix-certificates/apply_test.go`. Memory token: `cardupdate_kizmarked_gap`.
- **Audit wrapper:** root `audit-certificates.sh` + `cmd/fix-utilities/fix-certificates/audit-config.yaml`. It refreshes `cards`/`card_characteristics` + `onec_goods` into `/var/db/wbscraper.db`, runs `--reconcile` + `--stage` (NEVER `--apply`), and dumps `.diff`/`.csv` to `/tmp/audit-certificates-<stamp>/`. `--apply` stays manual via narrow `audit-config.yaml` scope. Env needed: `WB_API_CONTENT_KEY`, `ONEC_API_URL`, `ONEC_PIM_URL`, and (`WB_API_ANALYTICS_AND_PROMO_KEY` OR `WB_API_KEY`). The config blocklists `protected_char_ids` (ТНВЭД/ИКПУ/…) and uses `trash_filter` to exclude "basket" cards — a basket nmId in a batch fails the whole batch with HTTP 400.
- **`ilkoid/cards.yaml`** note: `wb.api_key: "WB_API_CONTENT_KEY"` there is a literal string, but the real resolution key is `cards.api_key_env`. Priority: configured `api_key_env` > `WB_API_KEY` > config value. It feeds the penalties-dims fixer's isolated `fixer.db` (NOT prod `/var/db`); `db_path` is overridden by `ilkoid.sh --db`.

## Development Guides (by scenario)

Full map with priorities → `dev_manifest.md` → "Карта документов разработчика".

| Scenario | Document |
|----------|----------|
| General Go, code review | `dev_solid.md` |
| Project architecture (immutable) | `dev_manifest.md` |
| Code placement, patterns | `dev_best_practices.md` |
| WB API write-utilities, sandboxes | `dev_swagger_reusable_packages.md` |
| Downloader migration v1→v2 | `dev_v2_downloader.md` + `dev_v2_postgres.md` |
| Card scrubbing (v3) | `dev_v3_scrub.md` |
| API tools (internal tool definitions) | `dev_api_tools.md` |
| Ozon integration (reference + roadmap) | `CLAUDE.md` (Ozon API Swagger Docs, env) + `dev_manifest.md` (Multi-Marketplace: Ozon) |

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