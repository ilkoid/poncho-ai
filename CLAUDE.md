# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Stack
- Language: Go 1.25 (CGo for SQLite via mattn/go-sqlite3)
- Storage: SQLite (WAL mode), S3
- TUI: charmbracelet/bubbletea
- AI: LLM-agnostic via Provider interface (Zai, OpenRouter, OpenAI)
- External APIs: Wildberries (Statistics, Content, Analytics v3, Seller Analytics v2, Feedbacks, Advertising, Supplies), 1C/PIM

## Commands
- Run all tests: `go test ./...`
- Single package: `go test ./pkg/wb/ -v`
- Single test: `go test ./pkg/wb/ -run TestAdaptiveRateLimit_ReducesOn429 -v`
- Coverage: `go test ./... -coverprofile=coverage.out`
- Main TUI: `go run cmd/poncho/main.go`
- Simple agent: `go run cmd/simple-agent/main.go "show categories"`
- Full data refresh: `bash download-all.sh [days]` (6 phases, single DB `/var/db/wb-sales.db`)
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
- `pkg/wb/` — WB API SDK: pagination, rate limiting, response unwrapping, service layer (Sales, Advertising, Feedbacks, Attribution)
- `pkg/orders/` — Orders v2 downloader domain (Statistics API `/api/v1/supplier/orders`)
- `pkg/opsales/` — Operational Sales v2 downloader domain (Statistics API `/api/v1/supplier/sales`)
- `pkg/feedbacks/` — Feedbacks v2 downloader domain (Feedbacks API `/api/v1/feedbacks`, `/api/v1/questions`)
- `pkg/campaigns/` — Campaigns v2 downloader domain (Promotion API `/adv/v1/count`, `/api/advert/v2/adverts`, `/adv/v3/fullstats`)
- `pkg/searchvis/` — Search Visibility v2 downloader domain (Seller Analytics API `/api/v2/search-report/report`, `/api/v2/search-report/product/search-texts`). Unique: requires Reader interface for nmID resolution from DB.
- `pkg/storage/sqlite/` — Repository pattern, ~57 tables across 9 `*_schema.go` files (schema, cards, supply, region_sales, prices, onec, stock_history, orders, opsales)
- `pkg/events/` + `pkg/tui/` — Port & Adapter decoupling (tui implements events interfaces)
- `pkg/chain/bundle_resolver.go` — Token optimization: 100 tools → 10 bundles (~98% savings)
- `pkg/prompts/` — Source pattern with fallback: File → Default → API → Database
- `pkg/analytics/` — HTTP analytics server (KPIs, tables, queries)
- `pkg/dashboard/` — Dashboard rendering (KPI snippets, tables, templates)
- `pkg/progress/` — Progress tracking with ETA
- `pkg/testing/` — Test utilities (wbmock for WB client mocking)

### cmd/ Entry Points
- `cmd/poncho/` — Main TUI agent
- `cmd/simple-agent/` — Headless agent CLI
- `cmd/data-downloaders/` — 22 WB API data collectors (sales, funnel, funnel-agg, promotion, promotion-v2, feedbacks, **feedbacks-v2**, cards, prices, stocks, stock-history, region-sales, **region-sales-v2**, search-visibility, **search-vis-v2**, supplies, 1c-data, all-articles, orders-v2, opsales-v2, **campaigns-v2**, **funnel-csv-v2**)
- `cmd/data-analyzers/` — 6 analysis utilities (feedbacks, SKU snapshots, snapshots, price comparison, DB freshness, 1C mapping)
- `cmd/data-dashboards/` — Web dashboards (sku-analytics)
- `cmd/test-utils/` — API testing utilities (test-wb-raw, test-wb-search, etc.)
- `cmd/fix-utilities/` — One-off migration/repair tools

### download-all.sh Phases
All configs in `cmd/.configs/download-all/`, all data in single DB `/var/db/wb-sales.db`:
1. **Catalog** — cards, prices, 1C/PIM
2. **Feedbacks** — feedbacks & questions
3. **Sales & Revenue** — orders-v2, opsales-v2, sales-v2, region-sales
4. **Stock & Logistics** — stocks, stock-history, supplies
5. **Advertising** — promotion, promotion-v2
6. **Analytics** — funnel, funnel-agg, search-visibility (3 req/min shared limit)

### Extending the Framework
- New platform (e.g., Ozon): add to `config.yaml` + switch case in `registerTool()`
- New downloader: copy structure from `cmd/data-downloaders/`, reuse `pkg/config/utility.go` types, add config to `cmd/.configs/download-all/`
- New tool: implement `Tool` interface, register in `registerTool()`

## WB API Swagger Docs
OpenAPI specs in `docs/wb_api_swagger/` — authoritative reference for all WB API endpoints:

| File | API Domain | Key Endpoints |
|------|-----------|---------------|
| `02-products.yaml` | Content API (`content-api.wildberries.ru`) | Cards CRUD, characteristics, dictionaries (categories, brands, colors, countries, seasons, TNVED) |
| `07-orders-fbw.yaml` | Supplies API (`supplies-api.wildberries.ru`) | Warehouses, transit tariffs, supplies list/details/goods/packages |
| `08-promotion.yaml` | Advertising API (`advert-api.wildberries.ru`) | Campaign CRUD, bids, fullstats, normquery stats/bids/minus, bid recommendations, budget, calendar, min bids |
| `09-communications.yaml` | Feedbacks API (`feedbacks-api.wildberries.ru`) | Feedbacks & questions: list, count, answer, edit, archive, pin |
| `10-tariffs.yaml` | Tariffs API (`common-api.wildberries.ru`) | Commission by category, box/pallet tariffs, return/shipping tariffs (RU/CN/TR/UZ/UAE) |
| `11-analytics.yaml` | Seller Analytics API (`seller-analytics-api.wildberries.ru`) | Funnel (v3), search-report (v2): positions, visibility, search queries, stocks, CSV reports |
| `12-reports.yaml` | Statistics + Reports APIs | Sales, orders, warehouses, penalty reports, self-purchases, dimension checks |
| `13-finances.yaml` | Finance API (`financial-api.wildberries.ru`) | Balance, realization reports (reportDetailByPeriod), document categories & details |

### Search Visibility API (from `11-analytics.yaml`)
Key endpoints for organic search visibility:
- `POST /api/v2/search-report/report` — aggregated positions (avg/median), visibility %, open cards, clusters (top-100/200/below)
- `POST /api/v2/search-report/product/search-texts` — top search queries per product with frequency, position, orders
- `POST /api/v2/search-report/product/orders` — orders and positions by search query text
- Rate: **3 req/min** shared across all search-report endpoints

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
- Analytics v3 API: **3 req/min** — most restrictive endpoint (shared with Seller Analytics v2 search-report)
- `client.IsDemoKey()` returns true for `demo_key`, enables mock responses
- API keys are separate: `WB_API_KEY` (content/analytics/ad), `WB_API_FEEDBACK_KEY` (feedbacks), `WB_STAT_API_KEY` (statistics), WB_API_CONTENT_KEY (только контент)
- Seller Analytics v2 (`seller-analytics-api.wildberries.ru`): search-report endpoints, 3 req/min
- Promotion V2 normquery stats: **10 req/min** (stricter than list/bids/minus at 5/sec)
- Date columns in `sales` table use `_dt` suffix: `order_dt`, `sale_dt`, `rr_dt` (not `sale_date`)
- Statistics API ToolIDs: `wb_orders` (orders), `wb_opsales` (operational sales) — both use `WB_STAT_API_KEY`, 1 req/min
- **`doRequest()` bypass = 429 flood:** `SalesPage()` and `OrdersPage()` must track `lastRequestTime` + call `adaptiveRecoverOK()`. Without these, `burst=10` token bucket allows rapid fire that triggers 429 on every page after the first. Any new `*Page()` method that does direct `c.httpClient.Do()` MUST replicate the two-level guard from `doRequest()` (token bucket + min interval check)
- год выпуска (производства) товара определяется по 2 и 3 символам в артикуле продавца, например: 12345678 -> 2023 год
- в API WB при обновлении карточки товара при записи нужно передавать ВСЕ ПОЛЯ! Иначе, которые не передал, они обнулятся. Это критический момент для любых проверок утилит!
- Поле season в onec_goods — более надёжный фильтр для школьного ассортимента, чем collection

### Sales Data Pipeline (три среза)
Three separate data sources for sales, each with different timing and purpose:

| Layer | API | Table | Updates | Key |
|-------|-----|-------|---------|-----|
| Orders | `/api/v1/supplier/orders` | `orders` | Every 30 min | `srid` |
| OpSales | `/api/v1/supplier/sales` | `operational_sales` | Every 30 min | `sale_id` (S/R prefix) |
| Financial | `reportDetailByPeriod` | `sales` | Daily | composite |

- `orders` = earliest signal (cart/checkout), `opsales` = preliminary sales/returns, `sales` = final financial realization
- **Do NOT confuse** `operational_sales` table (operational) with `sales` table (financial) — they come from different APIs
- All three use `WB_STAT_API_KEY` for orders/opsales, `WB_API_ANALYTICS_AND_PROMO_KEY` for financial reports

## WB API Safety Rules

**MASS WRITES TO WB API ARE FORBIDDEN.** Claude must NEVER generate or execute code that performs bulk updates via WB Content API (`POST /content/v2/cards/update`, `POST /content/v2/cards/upload`, or any other write endpoint).

- Only the user may run write operations against WB API manually
- Claude MAY build staging tables, generate payloads, prepare data, show dry-run output
- Claude MUST NOT call `client.UpdateCards()`, `client.CreateCards()`, or any POST/PUT/DELETE to WB Content API
- This applies to all utilities: fix-utilities, data-downloaders, data-analyzers, tools
- Reason: `POST /content/v2/cards/update` fully overwrites cards — partial updates are NOT supported. A wrong payload destroys existing data.

## Production Database Safety Rules

**КРИТИЧЕСКИЕ БАЗЫ В /var/db — READ-ONLY ДЛЯ CLAUDE. НИКАКИХ ЗАПИСЕЙ НИ В КАКОМ РЕЖИМЕ.**

- `/var/db/wb-sales.db` и `/var/db/sku-analytics.db` — рабочие базы с реальными данными
- Claude НЕ имеет права запускать **никакие** утилиты, которые пишут в эти базы — ни `--mock`, ни `--dry-run`, ни реальные загрузчики
- **`--mock` НЕ является read-only режимом!** Загрузчик в `--mock` режиме с `rewrite: true` в конфиге **удаляет реальные данные и вставляет фейковые**. Это уже привело к потере ~30,900 записей продаж.
- Для тестирования всегда перенаправлять в `/tmp` через `--db /tmp/test.db` или отдельный конфиг с `db_path: "/tmp/test.db"`
- Если нужно проверить данные — только `SELECT` запросы через `sqlite3` или утилиту `check-db-freshness` (она открывает БД в `mode=ro`)
- Реальные загрузчики (download-all.sh, download-wb-sales и т.д.) запускает **только пользователь вручную**

## Utility Development Rules

All utilities in `cmd/` (fix-utilities, data-downloaders, data-analyzers) MUST support:

- **`--mock` mode** — runs without real API calls, uses deterministic fake data. For downloaders: returns mock responses. For fix-utilities: reads/writes only local data without touching external APIs.
- **`--dry-run` mode** — shows exactly what would be sent/changed without executing. Prints payloads, SQL statements, or API requests to stdout. The user reviews before running for real.

No utility should ever require a real API key or live database to test its logic.

## Testing
- Unit: `mockHTTPClient` for wb.Client internals, `MockClient`/`MockPromotionClient` for downloader logic
- E2E: `SnapshotDBClient` reads from SQLite instead of API — `wb.NewSnapshotDBClient("e2e-snapshot.db")`
- E2E collector: `examples/e2e-mock-collector/` — collection order: Sales first → funnel → campaigns → feedbacks
- Downloader testing: `--mock` flag on all downloaders, uses mock client returning deterministic fake data
- Claude MUST NOT generate calls that hit write endpoints on prod

## 1C/PIM Integration
Streaming JSON decode → SQLite: `onec_goods`, `onec_goods_sku`, `onec_prices`, `pim_goods`.
Mapping: `onec_prices(good_guid) → onec_goods(guid) → article → cards.vendor_code → cards.nm_id`

## Environment Variables
| Variable | Purpose |
|----------|---------|
| `ZAI_API_KEY` | LLM provider |
| `WB_API_KEY` | WB Content, Analytics, Advertising APIs |
| `WB_API_ANALYTICS_AND_PROMO_KEY` | Analytics + Advertising (alternative, preferred for downloaders) |
| `WB_API_FEEDBACK_KEY` | WB Feedbacks API (separate key) |
| `WB_STAT_API_KEY` | WB Statistics API |
| `WB_API_MARKET_KEY` | WB Calendar API (dp-calendar-api, for promotion calendar) |
| `S3_ACCESS_KEY` / `S3_SECRET_KEY` | S3 storage |
| `OPENROUTER_API_KEY` | OpenRouter (LLM gateway for analyzers) |
| `ONEC_API_URL` / `ONEC_PIM_URL` | 1C/PIM catalog APIs |

## Compact Instructions
When compacting, preserve: goal, changed files, failing command, current hypothesis, test results, next exact command.

## V2 Downloader Architecture (Dual-Backend: SQLite + PostgreSQL)

V2 utilities support both SQLite and PostgreSQL through focused Writer interfaces. See `dev_v2_postgres.md` for full guide.

**Key packages:**
- `pkg/config/pgconfig.go` — `V2StorageConfig` (backend selection, DSN builder, password injection)
- `pkg/storage/postgres/` — PostgreSQL adapters (`pgxpool`, per-domain repos + schemas)
- `pkg/<domain>/` — v2 domain packages with Source/Writer interfaces (cards ✅, orders ✅, opsales ✅, campaigns ✅, sales/funnel — SQLite only, nmreport ✅ dual-backend)

**Pattern (each domain):**
```
pkg/<domain>/types.go      → Writer interface (2-7 methods, ISP-focused)
pkg/storage/postgres/       → Pg<Domain>Repo implements Writer (compile-time assertion)
pkg/storage/sqlite/         → SQLiteSalesRepository implements Writer (compile-time assertion)
cmd/.../<domain>-v2/main.go → CLI driver: config → backend switch → DI → Run
```

**Critical rules:**
- Writer interface = only methods actually called in `Downloader.Run()`, nothing extra
- No cursor/resume for "light" domains (<100k rows, <10 min download). ON CONFLICT upsert is safe
- PostgreSQL: `$1,$2` placeholders, `ON CONFLICT ... DO UPDATE SET ... = EXCLUDED`, `BOOLEAN` not `INTEGER`, `pgxpool` not `database/sql`
- `resolveAPIKey()` must call `os.Getenv()`, not return the env var name as a string
- CLI creates Writer directly via switch — no god-object BackendFactory
- `dllog.PrintHeader()` + `dllog.Progress()` + `dllog.Done()` — one line per page, no `fmt.Printf` in `pkg/`
- **DiscardWriter pattern:** `--mock` mode must create DiscardWriter (zero DB interaction), NOT open a real database. Writer creation goes INSIDE the else branch — see `download-wb-orders-v2/main.go` for reference

**PostgreSQL setup:** `192.168.10.7:15432`, user `postgres`, password from `$PG_PWD`, databases: `wb_data_prod` / `wb_data_test`

**Migration status:**

| Domain | pkg/ v2 | SQLite adapter | PG adapter | Resume? |
|--------|---------|---------------|------------|---------|
| cards | ✅ `pkg/cards/` | ✅ | ✅ | ❌ No (YAGNI) |
| prices | ✅ `pkg/prices/` | ✅ | ✅ | ❌ No (YAGNI) |
| orders | ✅ `pkg/orders/` | ✅ | ✅ | ❌ No (light) |
| opsales | ✅ `pkg/opsales/` | ✅ | ✅ | ❌ No (light) |
| sales | ✅ `pkg/sales/` | ✅ | ✅ | ✅ Date-level |
| funnel | ✅ `pkg/funnel/` | ✅ | ✅ | Partial |
| stocks | ✅ `pkg/stocks/` | ✅ | ✅ | ❌ No (snapshot) |
| feedbacks | ✅ `pkg/feedbacks/` | ✅ | ✅ | ❌ No (light) |
| region-sales | ✅ `pkg/regionsales/` | ✅ | ✅ | ❌ No (light) |
| campaigns | ✅ `pkg/campaigns/` | ✅ | ✅ | ✅ Date-level |
| searchvis | ✅ `pkg/searchvis/` | ✅ | ✅ | ❌ No (light). Unique: Reader interface for nmID resolution |
| nmreport | ✅ `pkg/nmreport/` | ✅ | ✅ | ✅ Report-level |
| Others (11) | — | v1 only | — | — |

## Development Guides

Project root contains development documents with a clear priority hierarchy (see `dev_manifest.md` → "Карта документов разработчика"):

| Scenario | Document | Read when |
|----------|----------|-----------|
| General Go principles, code review | `dev_solid.md` | Architecture questions |
| Project architecture, immutable rules | `dev_manifest.md` | Any development |
| Code placement, common patterns | `dev_best_practices.md` | "Where do I put X?" |
| WB API write-utilities, sandboxes | `dev_swagger_reusable_packages.md` | Write-utility work, sandbox testing |
| V2 downloader (architecture + anti-patterns) | `dev_v2_downloader.md` | Creating/migrating a downloader |
| V2 dual-backend (SQLite + PostgreSQL) | `dev_v2_postgres.md` | Adding PostgreSQL adapter to a domain |

**Rule:** more specific document overrides more general. For PostgreSQL migration, `dev_v2_postgres.md` overrides `dev_v2_downloader.md`.

## SQLite database placement
production bases, **read-only**: /var/db
- wwb-sales.db - major data-lake based on wb api data
- sku-analytics - major analytics database with calculations and data insights
