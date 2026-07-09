# WB-Scraper Collector — Run Book

Two-process system: a **browser extension** (live WB session, human-pace capture,
MV3 service worker) + this **Go collector** (loopback HTTP server, target queue,
decode → SQLite/PostgreSQL, report).

The extension — not this binary — makes the storefront requests, from a real
logged-in browser (the anti-bot bypass). Two extension generations talk to this
one collector over different endpoints:

- **v1 — `extensions/wb-scraper/`** (plain JS, MV3): **pull-driven**. The collector
  hands out targets (`GET /targets`); the extension navigates, intercepts WB
  responses, and pushes the raw bodies (`POST /capture`); the collector decodes
  and persists. SQLite or PostgreSQL backend.
- **v2 — `extensions/poncho-wb-parser/`** (TypeScript + Vite + Dexie, MV3):
  **push-driven**. Builds its own targets, decodes `card.json` itself, persists to
  a local Dexie DB, then pushes a **fully-decoded snapshot** via `POST /snapshot`
  (one HTTP call per session). **PostgreSQL-only** (SQLite backend answers 501):
  replace-by-`snapshot_ts` semantics, so retries are idempotent. v2 does NOT use
  `/targets` or `/capture`. See `inst.md` for the canonical v2 recipe.

> Canonical source: plan `iridescent-shimmying-bee.md`. This file is the operator
> map; the plan is the architecture. For v2, `inst.md` is the quick recipe.

> Canonical source: plan `iridescent-shimmying-bee.md`. This file is the operator
> map; the plan is the architecture.

## A. Prerequisites

- Go 1.25; the repo builds (`go build ./...`).
- Chromium/Edge **logged into WB** (real cookies/region — the whole point of the
  bypass; the extension is indistinguishable from a human).
- v1: extension loaded unpacked from `extensions/wb-scraper/` (`host_permissions`
  includes `http://127.0.0.1/*`). v2: `extensions/poncho-wb-parser/` — build first
  (`cd extensions/poncho-wb-parser && npm install && npm run build`), then load
  unpacked from `dist/`; configure the collector URL in the dashboard → Settings
  (default `http://127.0.0.1:7780`).
- For PostgreSQL: `$PGHOST/$PGPORT/$PG_PWD/$PGDATABASE` set, database `wb_data_test`
  (NOT `wb_data_prod`). v2's `POST /snapshot` is PG-only.

## B. Build

```
go build -o wb-scraper-collector ./cmd/data-downloaders/wb-scraper-collector
```

Or use `go run ./cmd/data-downloaders/wb-scraper-collector` directly.

## C. Modes (DB always `/tmp`, NEVER `/var/db`)

| Mode | Purpose | DB |
|------|---------|----|
| live (default) | Server + extension: pull targets, push captures → write | `/tmp/wbscraper.db` |
| `--mock` | DiscardWriter: exercise transport + decode, **0 DB rows** | not opened |
| `--dry-run` | Decode → print JSON batches to stdout, no persistence | not opened |
| `--report-only` | HTML report from an existing DB (Stage 7) | read existing |

> ⚠️ `--mock` is NOT read-only against a real DB — it uses a DiscardWriter that opens
> no database at all. Never point `--db` at `/var/db` in any mode. CLAUDE.md: the
> production bases under `/var/db` are READ-ONLY for automation.

## D. SQLite live session (canonical scenario)

**Terminal 1 — collector:**
```
go run ./cmd/data-downloaders/wb-scraper-collector \
  --config cmd/.configs/download-all/wb-scraper-collector.yaml \
  --backend sqlite --db /tmp/wbscraper.db \
  --addr 127.0.0.1:7780
```

**Browser:**
1. WB logged in.
2. `chrome://extensions` → Developer mode → Load unpacked → `extensions/wb-scraper/`.
3. Popup → **Collect** → start (or auto-pull once the server is live).
4. SW DevTools (Service Worker → console): `GET /targets` visible (with `query_id`),
   tab navigation, `POST /capture` sends.

**Terminal 2 — monitor:**
```
watch -n2 'curl -s localhost:7780/state'
sqlite3 /tmp/wbscraper.db "select q.query,count(*) from competitor_cards c \
  join search_queries q on q.query_id=c.query_id group by q.query"
# analysis by constructor dimension (column filter, not LIKE over text):
sqlite3 /tmp/wbscraper.db "select q.season,count(*) from search_queries q \
  join competitor_cards c on c.query_id=q.query_id group by q.season"
```

## E. PostgreSQL live session

As D, but `--backend postgres`, database from `$PGDATABASE` (`wb_data_test` —
NEVER `wb_data_prod`), password from `$PG_PWD`. Tables are created by
`PgWbscraperRepo.InitSchema` (`BIGSERIAL`/`BIGINT`/`BOOLEAN`, `$1..$n` placeholders,
`ON CONFLICT(query) DO UPDATE SET query=EXCLUDED.query RETURNING query_id` one-shot
upsert). This is the **required backend for v2 `/snapshot`**: SQLite answers 501.

The committed config (`cmd/.configs/download-all/wb-scraper-collector.yaml`)
already points at `wb_data_test`. `InitSchema` is idempotent and runs the v2
migrations on each start, so a `name` column (and the cartesian-axis columns) are
added to an existing DB via `ADD COLUMN IF NOT EXISTS`.

**`POST /snapshot` (v2 only)** — push a fully-decoded session snapshot. Body is the
`SnapshotDump` envelope (13 keys): `generated_at`, `snapshot`, `counts`, plus
`search_queries` and the 11 fact-table arrays. The handler upserts queries
(browser Dexie `query_id` → server `BIGSERIAL` via query text; unknown browser ids
map to NULL), remaps every fact row's `query_id`, then `ReplaceSnapshot` runs in
one transaction: `DELETE` all rows of `snapshot_ts` across the 11 fact tables, then
bulk-`INSERT`. A re-push of the same `snapshot_ts` yields the same row set
(idempotent — retries are safe). Response: per-table counts keyed
`positions/ads/cards/prices/details/stocks/meta/options/compositions/sizes/colors`.

### card.json content tables (Этап A/B — v2-only)

Five fact tables are populated ONLY by the v2 `/snapshot` path; v1 `/capture`
never writes them:

| Table | Scope |
|-------|-------|
| `competitor_card_meta` | per-nm scalars: vendor code, subject ids, brand, media, description, kinds, … |
| `competitor_card_options` | EAV — one product characteristic per row (Состав / Цвет / Покрой / …) |
| `competitor_card_compositions` | material components (хлопок 60% / полиэстер 40%) |
| `competitor_card_sizes` | one cell of the card.json size grid (prop_name=prop_value per tech_size) |
| `competitor_card_colors` | color-variant nm_ids |

The v2 extension also populates `name` (product title) on `search_positions` and
`competitor_cards`; v1 `/capture` leaves `name` empty (it is decoded and stored
only on the v2 path).

## F. Report (single-file HTML) — Stage 7

```
go run ./cmd/data-downloaders/wb-scraper-collector --report-only \
  --backend sqlite --db /tmp/wbscraper.db
# → /tmp/wb-competitors-<ts>.html — open in a browser, filters (query/brand/season/price) client-side
```

Not implemented in this build; `--report-only` prints an explicit notice.

## G. Stop

`Ctrl-C` → `signal.NotifyContext` fires → `srv.Shutdown` drains in-flight requests →
final buffer flush → exit. The extension sees `done:true` (or sends `POST /done`)
and ends its loop.

## H. HTTP contract

All endpoints on the loopback address (`127.0.0.1:7780` by default). JSON in/out.
The v1 contract (`/targets`, `/capture`, `/done`, `/state`) is consumed by the
`wb-scraper` extension; `POST /snapshot` is the v2 `poncho-wb-parser` path
(documented in §E).

**`GET /targets?n=<int>`** — pull a target batch.
```json
{"items":[{"kind":"search","query_id":1,"query":"бейсболки","url":"https://…search=…","subject":"бейсболки","gender":"","season":"","age":""}],
 "sessionId":"20260702013754","total":300,"served":5,"done":false}
```
Each item already carries its `query_id` (stamped by the collector via `UpsertQuery`
at queue fill). `done:true` once the cursor reaches the end.

**`POST /capture`** — push a batch of intercepted WB responses.
```json
[{"kind":"search","url":"https://…/search…?page=2&dest=8038","query_id":7,"status":200,"body":{…WB response…}}]
```
`body` is the parsed WB JSON (kept raw; decoded per `kind`:
`search`/`brand`→positions, `card_list`/`card_detail`→cards+prices(+details/stocks),
`ad`→vitrine ads). Response:
```json
{"accepted":2,"decoded":{"positions":2,"ads":0,"cards":1,"prices":1,"details":1,"stocks":1}}
```

**`POST /done`** — mark session complete; triggers a final flush.
```json
{"ok":true,"flushed":{"positions":2,"ads":0,"cards":0,"prices":0,"details":0,"stocks":0}}
```

**`GET /state`** — queue progress + counts (for the popup).
```json
{"sessionId":"20260702013754","total":300,"served":5,"remaining":295,"done":false,
 "capturesReceived":1,"counts":{"positions":2,"ads":0,"cards":0,"prices":0,"details":0,"stocks":0}}
```
Counts update on flush (every `collect.flush_interval`, on `POST /done`, and on
shutdown) — not per capture.

## I. CLI flags

| Flag | Default | Notes |
|------|---------|-------|
| `--config` | `cmd/.configs/download-all/wb-scraper-collector.yaml` | YAML config path |
| `--backend` | (config) | `sqlite` \| `postgres`, overrides config |
| `--db` | (config) | SQLite path; **use `/tmp`, never `/var/db`** |
| `--pg-database` | (config) | PostgreSQL database name |
| `--generator` | (config) | `static` \| `llm` (stub), overrides config |
| `--addr` | (config `host:port`) | Listen address, e.g. `127.0.0.1:7780` |
| `--mock` | false | DiscardWriter, no DB |
| `--dry-run` | false | Decode + print, no DB |
| `--report-only` | false | HTML report (Stage 7) |

## J. Troubleshooting

| Symptom | Cause / fix |
|---------|-------------|
| No `POST /capture` logs | The capture tab must be `active:true` (background tabs throttle; the WB SPA doesn't fire requests). Check popup → Collect. |
| `/targets` returns empty | The queue is filled by the static constructor at startup — check `/state` (`total`) and the startup `queue ready` log line. |
| Capture loop stalled | MV3 service worker dies ~30s; the offscreen doc holds the loop and IndexedDB survives SW death. Fully stalled → reload the extension. |
| 429 / captcha | Human-pace 1–3s in the extension; lower `MAX_PAGES`/`DETAIL_K` to go softer. |
| `fetch('http://127.0.0.1')` blocked | `manifest.json` must list `host_permissions: […, "http://127.0.0.1/*"]` (Stage 6 edit). |
| `query_id` empty in a capture | The extension stamps it from the active target; the target must arrive with `query_id` from `/targets` (the collector upserts into `search_queries`). |
| `count` stays 0 in `/state` | Counts update on flush, not per capture. Wait one `flush_interval` (2s default), or `POST /done`. |

## K. Safety (per CLAUDE.md)

- DB: SQLite under `/tmp/*.db`; PostgreSQL `wb_data_test` (**NEVER** `wb_data_prod`,
  **NEVER** `/var/db` — production is READ-ONLY in any mode). The committed config
  already targets `wb_data_test`; override via `$PGDATABASE` only to another test DB.
- The collector **does not call the WB API** (the extension does, in-browser) → the
  "mass writes to WB API are forbidden" rule is satisfied by architecture: there is
  no WB Content API client in this binary.

**Known limitations:**
- `competitor_card_prices.delivery_days` is a dead column — it exists in the schema
  but no producer (v1 `/capture` or v2 `/snapshot`) ever fills it, so it is always
  NULL. Delivery timing lives on `competitor_card_stocks.time1/time2`, captured fully.
- `search_positions.name` and `competitor_cards.name` (product title) are populated
  only by v2 `/snapshot`; v1 `/capture` leaves them `''`.

## L. Smoke test (no extension, no DB)

```
# terminal 1
go run ./cmd/data-downloaders/wb-scraper-collector --mock --addr 127.0.0.1:7780 \
  --config cmd/.configs/download-all/wb-scraper-collector.yaml

# terminal 2
curl -s 'localhost:7780/targets?n=5'         # generated targets with query_id
curl -s -X POST localhost:7780/capture -H 'Content-Type: application/json' \
  -d '[{"kind":"search","url":"https://w/s?page=1&dest=8038","query_id":1,"status":200,"body":{"products":[{"id":1,"sizes":[{"price":{"basic":100000,"product":89900}}]}]}}]'
curl -s 'localhost:7780/state'               # capturesReceived:1, counts 0 until flush
curl -s -X POST localhost:7780/done          # final flush
curl -s 'localhost:7780/state'               # counts.positions:1, done:true
```
