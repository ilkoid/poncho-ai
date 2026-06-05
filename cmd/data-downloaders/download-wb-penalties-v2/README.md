# WB Measurement Penalties Downloader v2

Downloads dimension penalty data from WB Seller Analytics API (`/api/analytics/v1/measurement-penalties`).

Penalties are charged when actual warehouse measurements exceed declared product card dimensions. Each record includes both actual and declared measurements, penalty amounts, and cancellation status.

## Features

- **Dual-backend:** SQLite + PostgreSQL
- **Safe mock mode:** `--mock` uses DiscardWriter — zero database interaction
- **Adaptive rate limiting:** 1 req/min WB API with automatic 429 recovery
- **Rewrite mode:** optionally delete stale penalties before downloading
- **Light domain:** no cross-domain dependencies, no resume/cursor needed

## Usage

```bash
# Mock mode — no API calls, no DB writes
go run . --mock

# Mock + test SQLite database
go run . --mock --db /tmp/test-penalties.db

# Mock + test PostgreSQL database
go run . --mock --backend postgres --pg-database wb_data_test

# Dry-run — real API, no writes
go run . --dry-run --config config.yaml

# Production (user only!)
go run . --config config.yaml
```

## Configuration

Key config types:
- `penalties` — `config.DownloadConfig` (days, from/to, rewrite, adaptive tuning)
- `storage` — `config.V2StorageConfig` (backend, db_path)

## Database Schema

Table: `measurement_penalties`
- Primary key: `dim_id` (unique measurement ID, natural key)
- Indexes: `nm_id`, `dt_bonus`, `is_valid`
- Booleans: `INTEGER NOT NULL DEFAULT 1` (SQLite), `BOOLEAN` (PostgreSQL)

Key columns:
- Actual measurements: `volume`, `width`, `length`, `height` (warehouse)
- Declared dimensions: `volume_sup`, `width_sup`, `length_sup`, `height_sup` (product card)
- Penalty info: `penalty_amount`, `reversal_amount`, `is_valid`, `prc_over`

## Rate Limiting

- **WB Seller Analytics API:** 1 req/min, burst 1 (prevents burst-fire 429)
- **Retention:** 90 days
- **Pagination:** offset-based, max 1000 rows per page

## Safety

⚠️ **Mock mode is safe by design:**
- `--mock` = MockPenaltiesSource + DiscardWriter → **zero DB interaction**
- All test commands must use `--db /tmp/...` or `--pg-database wb_data_test`
