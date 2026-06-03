# WB Funnel Downloader v2

Downloads funnel (conversion) data from WB Seller Analytics API v3 (`/api/analytics/v3/sales-funnel/products/history`).

Daily product-level funnel metrics: card views, cart adds, orders, buyouts, and conversion rates. Unlike sales data, funnel includes today (partial data useful for trends).

## Features

- **Dual-backend:** SQLite + PostgreSQL via `config.V2StorageConfig`
- **Safe mock mode:** `--mock` uses DiscardWriter — zero database interaction
- **Article-based filtering:** exclude by article length, filter by production year (2-3 digits)
- **Activity filter:** skip products without recent sales
- **Incremental loading:** skip products loaded within N hours (`incremental_hours`)
- **Refresh window:** recent data is upserted, historical data is frozen (preserved)
- **Adaptive rate limiting:** 3 req/min with automatic 429 recovery

## Usage

```bash
# Mock mode — no API calls, no DB writes
go run . --mock

# Mock + test SQLite database
go run . --mock --db /tmp/test-funnel.db

# Mock + test PostgreSQL database
go run . --mock --backend postgres --pg-database wb_data_test

# Dry-run — real API, no writes
go run . --dry-run --db /tmp/test-funnel.db

# Explicit date range (overrides config days)
go run . --mock --begin 2026-05-27 --end 2026-06-03 --db /tmp/test-funnel.db

# Production (user only!)
go run . --config config.yaml
```

## API Details (Swagger: `11-analytics.yaml`)

| Parameter | Value |
|-----------|-------|
| Endpoint | `POST /api/analytics/v3/sales-funnel/products/history` |
| Server | `seller-analytics-api.wildberries.ru` |
| Rate limit | **3 req/min** (shared with search-report endpoints) |
| Max date range | **1 week** per request |
| Max nmIds per request | **20** |
| Data updates | Once per hour |
| Auth | `WB_API_ANALYTICS_AND_PROMO_KEY` |

## Configuration

See `config.yaml` for full annotated config.

Key config sections:
- `wb` — `config.WBClientConfig` (API key, timeout)
- `funnel` — `config.FunnelConfig` (days, batch_size, rate limits, adaptive tuning)
- `filter` — `config.FunnelFilterConfig` (exclude_lengths, allowed_years, active_days)
- `storage` — `config.V2StorageConfig` (backend, db_path, pg_database)
- `refresh_window` — days within which data is refreshed (outside = frozen)

## Database Schema

### Table: `products` (shared with cards downloader)
- Primary key: `nm_id` (natural key, WB SKU)
- Updated on each funnel run with product metadata

### Table: `funnel_metrics_daily`
- Primary key: `id` (auto-increment)
- Unique constraint: `(nm_id, metric_date)`
- Columns: view/cart/order/buyout counts, financial sums, conversion rates
- Indexes: `(nm_id, metric_date)`, `(metric_date, nm_id)`, `(metric_date, order_count)`, `(metric_date, conversion_buyout)`, `(nm_id, created_at)`

### Refresh Window Logic
- **Within window** (today − refresh_window days): `ON CONFLICT DO UPDATE` — data may change
- **Outside window**: `ON CONFLICT DO NOTHING` — historical data frozen
- Mirrors SQLite `INSERT OR REPLACE` / `INSERT OR IGNORE` pattern

## CLI Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `config.yaml` | Path to config file |
| `--mock` | `false` | Mock mode (no API, no DB) |
| `--dry-run` | `false` | Real API, skip DB writes |
| `--db` | from config | SQLite database path |
| `--backend` | from config | `sqlite` or `postgres` |
| `--pg-database` | from config | PostgreSQL database name |
| `--days` | from config | Days of history (1-365) |
| `--begin` | — | Start date YYYY-MM-DD (overrides config) |
| `--end` | — | End date YYYY-MM-DD (overrides config) |

## Rate Limiting

- **WB Seller Analytics API:** 3 req/min, burst 3 (shared with search-report)
- **Recovery:** desired → 429 backoff → api floor (5 OKs) → probe desired (10 OKs)
- **Max backoff:** 60 seconds

## Pipeline Dependencies

Funnel downloader reads from `sales` table (created by sales downloader):
1. `GetDistinctNmIDs` — queries `sales` for active products
2. `GetSupplierArticlesByNmIDs` — gets articles for filtering
3. `FilterActiveNmIDs` — filters by recent sales activity

**Run order:** sales must be loaded before funnel (Phase 3 → Phase 6 in `download-all.sh`).

## Safety

⚠️ **Mock mode is safe by design:**
- `--mock` = MockFunnelSource + DiscardWriter → **zero DB interaction**
- Writer creation is inside the `else` branch — mock never touches real DB
- All test commands must use `--db /tmp/...` or `--pg-database wb_data_test`
- **NEVER** run without `--mock` against `/var/db/` — production databases are read-only for utilities
