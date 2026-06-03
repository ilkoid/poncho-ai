# WB Prices Downloader v2

Downloads product prices from WB Discounts & Prices API (`/api/v2/list/goods/filter`).

Full snapshot of all product prices — no date range, replaces previous snapshot on each run. Light domain (~32k records, 3-5 min download).

## Features

- **Dual-backend:** SQLite + PostgreSQL
- **Safe mock mode:** `--mock` uses DiscardWriter — zero database interaction
- **Adaptive rate limiting:** 100 req/min WB API with automatic 429 recovery
- **Offset pagination:** limit/offset, page size 1000

## Usage

```bash
# Mock mode — no API calls, no DB writes
go run . --mock

# Mock + test SQLite database
go run . --mock --db /tmp/test-prices.db

# Mock + test PostgreSQL database
go run . --mock --backend postgres --pg-database wb_data_test

# Dry-run — real API, no writes
go run . --dry-run --db /tmp/test-prices.db

# Production (user only!)
go run . --config config.yaml
```

## Configuration

See `config.yaml` for full config with comments.

Key config types:
- `prices` — rate limiting, adaptive tuning, API key env var
- `storage` — `config.V2StorageConfig` (backend, db_path, pg_database)

## Database Schema

Table: `product_prices`
- Primary key: `(nm_id, snapshot_date)` — one price row per product per day
- Indexes: `snapshot_date`, `vendor_code`
- Fields: price, discounted_price, club_discounted_price, discount, club_discount, vendor_code, currency

## Rate Limiting

- **WB Discounts & Prices API:** 100 req/min, burst 5
- **Pagination:** offset-based, page size 1000
- **Full snapshot:** no date range, upsert on each run

## Safety

⚠️ **Mock mode is safe by design:**
- `--mock` = MockPricesSource + DiscardWriter → **zero DB interaction**
- All test commands must use `--db /tmp/...` or `--pg-database wb_data_test`
