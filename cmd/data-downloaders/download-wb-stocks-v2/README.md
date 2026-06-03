# WB Stocks Downloader v2

Downloads warehouse stock snapshots from WB Analytics API (`/api/analytics/v1/stocks-report/wb-warehouses`).

Full snapshot of all product stocks across warehouses — offset-based pagination with 250K rows per page. Snapshot data is upserted per date, enabling historical tracking.

## Features

- **Dual-backend:** SQLite + PostgreSQL via `config.V2StorageConfig`
- **Safe mock mode:** `--mock` uses DiscardWriter — zero database interaction
- **Gap detection:** checks for missing snapshot dates since `first_date` (informational only)
- **Adaptive rate limiting:** 3 req/min with automatic 429 recovery
- **Chunked writes:** 500 rows per transaction (PG), 50K per transaction (SQLite)

## Usage

```bash
# Mock mode — no API calls, no DB writes
go run . --mock

# Mock + test SQLite database
go run . --mock --db /tmp/test-stocks.db

# Mock + test PostgreSQL database
go run . --mock --backend postgres --pg-database wb_data_test

# Dry-run — real API, no writes
go run . --dry-run --db /tmp/test-stocks.db

# Specific snapshot date
go run . --mock --date 2026-05-28 --db /tmp/test-stocks.db

# Production (user only!)
go run . --config config.yaml
```

## API Details (Swagger: `11-analytics.yaml`)

| Parameter | Value |
|-----------|-------|
| Endpoint | `POST /api/analytics/v1/stocks-report/wb-warehouses` |
| Server | `seller-analytics-api.wildberries.ru` |
| Rate limit | **3 req/min**, burst 1 |
| Pagination | Offset-based (limit/offset), max 250K rows per page |
| Request body | `{"limit": 250000, "offset": 0}` |
| Auth | `WB_API_ANALYTICS_AND_PROMO_KEY` |

## Configuration

See `config.yaml` for full annotated config.

Key config sections:
- `wb` — `config.WBClientConfig` (API key, timeout)
- `stocks` — `config.StocksConfig` (first_date, rate_limits, adaptive tuning)
- `storage` — `config.V2StorageConfig` (backend, db_path, pg_database)

## Database Schema

### Table: `stocks_daily_warehouses`

| Column | Type | Description |
|--------|------|-------------|
| `id` | BIGSERIAL PK | Auto-increment |
| `snapshot_date` | TEXT NOT NULL | YYYY-MM-DD |
| `nm_id` | BIGINT NOT NULL | WB SKU |
| `chrt_id` | BIGINT NOT NULL | Size/variant ID |
| `warehouse_id` | BIGINT NOT NULL | Warehouse ID |
| `warehouse_name` | TEXT | Warehouse name |
| `region_name` | TEXT | Region |
| `quantity` | BIGINT | Stock quantity |
| `in_way_to_client` | BIGINT | In transit to client |
| `in_way_from_client` | BIGINT | In transit from client |
| `created_at` | TEXT | Auto-generated |

**UNIQUE constraint:** `(snapshot_date, nm_id, chrt_id, warehouse_id)` — upsert preserves data integrity.

**Indexes:**
- `(nm_id, warehouse_id, snapshot_date)` — time-series queries
- `(snapshot_date)` — gap detection, date filtering
- `(warehouse_id, snapshot_date)` — warehouse aggregation

## CLI Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `config.yaml` | Path to config file |
| `--mock` | `false` | Mock mode (no API, no DB) |
| `--dry-run` | `false` | Real API, skip DB writes |
| `--date` | today | Snapshot date YYYY-MM-DD |
| `--db` | from config | SQLite database path |
| `--backend` | from config | `sqlite` or `postgres` |
| `--pg-database` | from config | PostgreSQL database name |

## Rate Limiting

- **WB Analytics API:** 3 req/min, burst 1
- **Recovery:** desired → 429 backoff → api floor (5 OKs) → probe desired (10 OKs)
- **Max backoff:** 60 seconds
- **Pagination:** 250K rows per page (typically 1 page for ~30K products)

## Safety

⚠️ **Mock mode is safe by design:**
- `--mock` = MockStocksSource + DiscardWriter → **zero DB interaction**
- Writer creation is inside the `else` branch — mock never touches real DB
- All test commands must use `--db /tmp/...` or `--pg-database wb_data_test`
- **NEVER** run without `--mock` against `/var/db/` — production databases are read-only for utilities
