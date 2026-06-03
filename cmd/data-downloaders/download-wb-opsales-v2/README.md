# WB Operational Sales Downloader v2

Downloads operational sales data from WB Statistics API (`/api/v1/supplier/sales`).

Operational sales provide preliminary data about sales and returns, updating every 30 minutes with 90-day retention. This is the **middle layer** of the sales data pipeline:

1. **Orders** (`/api/v1/supplier/orders`) — cart/checkout, earliest signal
2. **OpSales** (`/api/v1/supplier/sales`) ← **this downloader** — preliminary sales/returns
3. **Financial** (`ReportDetailByPeriod`) — full financial realization reports

## Features

- **Dual-backend:** SQLite + PostgreSQL
- **Safe mock mode:** `--mock` uses DiscardWriter — zero database interaction
- **Article-based filtering:** exclude by article length, filter by production year
- **Adaptive rate limiting:** 1 req/min WB API with automatic 429 recovery
- **Rewrite mode:** optionally delete stale sales before downloading

## Usage

```bash
# Mock mode — no API calls, no DB writes
go run . --mock

# Mock + test SQLite database
go run . --mock --db /tmp/test-opsales.db

# Mock + test PostgreSQL database
go run . --mock --backend postgres --pg-database wb_data_test

# Dry-run — real API, no writes
go run . --dry-run --db /tmp/test-opsales.db --config cmd/.configs/download-all/download-wb-opsales.yaml

# Production (user only!)
go run . --config cmd/.configs/download-all/download-wb-opsales.yaml
```

## Configuration

See `cmd/.configs/download-all/download-wb-opsales.yaml` for full config with filter section.

Key config types:
- `opsales` — `config.DownloadConfig` (days, from/to, rewrite, adaptive tuning)
- `filter` — `config.FunnelFilterConfig` (exclude_lengths, allowed_years)
- `storage` — `config.V2StorageConfig` (backend, db_path)

## Database Schema

Table: `operational_sales` (separate from `sales` table used for financial reports)
- Primary key: `sale_id` ("S***" = sale, "R***" = return)
- Indexes: `sale_id`, `nm_id`, `sale_date`, `g_number`, `supplier_article`, `last_change_date`
- Booleans: `INTEGER NOT NULL DEFAULT 0` (SQLite), `BOOLEAN` (PostgreSQL)
- Unique fields vs orders: `payment_sale_amount`, `for_pay` (not in orders)
- Missing vs orders: `is_cancel`, `cancel_date` (not in sales API)

## Rate Limiting

- **WB Statistics API:** 1 req/min, burst 1 (prevents burst-fire 429)
- **Retention:** 90 days
- **Pagination:** by `lastChangeDate` string, max 80,000 rows per page

## Safety

⚠️ **Mock mode is safe by design:**
- `--mock` = MockOpsalesSource + DiscardWriter → **zero DB interaction**
- Unlike cards/sales downloaders where `--mock` could still write to the database
- All test commands must use `--db /tmp/...` or `--pg-database wb_data_test`
