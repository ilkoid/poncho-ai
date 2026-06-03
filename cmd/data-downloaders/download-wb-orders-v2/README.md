# WB Orders Downloader v2

Downloads order data from WB Statistics API (`/api/v1/supplier/orders`).

Orders provide the earliest signal of customer activity (cart/checkout), updating every 30 minutes with 90-day retention.

## Features

- **Dual-backend:** SQLite + PostgreSQL
- **Safe mock mode:** `--mock` uses DiscardWriter — zero database interaction
- **Article-based filtering:** exclude by article length, filter by production year
- **Adaptive rate limiting:** 1 req/min WB API with automatic 429 recovery
- **Rewrite mode:** optionally delete stale orders before downloading

## Usage

```bash
# Mock mode — no API calls, no DB writes
go run . --mock

# Mock + test SQLite database
go run . --mock --db /tmp/test-orders.db

# Mock + test PostgreSQL database
go run . --mock --backend postgres --pg-database wb_data_test

# Dry-run — real API, no writes
go run . --dry-run --db /tmp/test-orders.db --config cmd/.configs/download-all/download-wb-orders.yaml

# Production (user only!)
go run . --config cmd/.configs/download-all/download-wb-orders.yaml
```

## Configuration

See `cmd/.configs/download-all/download-wb-orders.yaml` for full config with filter section.

Key config types:
- `orders` — `config.DownloadConfig` (days, from/to, rewrite, adaptive tuning)
- `filter` — `config.FunnelFilterConfig` (exclude_lengths, allowed_years)
- `storage` — `config.V2StorageConfig` (backend, db_path)

## Database Schema

Table: `orders`
- Primary key: `srid` (unique order ID from WB)
- Indexes: `nm_id`, `order_date`, `g_number`, `supplier_article`, `last_change_date`, `is_cancel`
- Booleans: `INTEGER NOT NULL DEFAULT 0` (SQLite), `BOOLEAN` (PostgreSQL)

## Rate Limiting

- **WB Statistics API:** 1 req/min, burst 1 (prevents burst-fire 429)
- **Retention:** 90 days
- **Pagination:** by `lastChangeDate` string, max 80,000 rows per page

## Safety

⚠️ **Mock mode is safe by design:**
- `--mock` = MockOrdersSource + DiscardWriter → **zero DB interaction**
- Unlike cards/sales downloaders where `--mock` could still write to the database
- All test commands must use `--db /tmp/...` or `--pg-database wb_data_test`
