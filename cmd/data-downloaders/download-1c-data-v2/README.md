# 1C Data Downloader v2

Downloads product catalog, prices, and PIM attributes from 1C/PIM internal APIs.
Supports both SQLite and PostgreSQL backends.

## Usage

```bash
# Mock mode (no API calls, no DB) — safe for testing
go run . --mock

# Mock mode with test SQLite database
go run . --mock --db /tmp/test-1c.db

# Mock mode with test PostgreSQL
go run . --mock --backend postgres --pg-database wb_data_test

# Dry-run (real API, no DB writes)
go run . --dry-run --db /tmp/test-1c.db --config config.yaml

# Production (user only!)
go run . --config config.yaml

# Clean all 1C/PIM tables before loading
go run . --config config.yaml --clean
```

## Flags

| Flag | Description |
|------|-------------|
| `--config` | Path to config.yaml (default: config.yaml) |
| `--db` | Database path, overrides config (SQLite only) |
| `--backend` | Storage backend: `sqlite` (default) or `postgres` |
| `--pg-database` | PostgreSQL database name, overrides config |
| `--mock` | Use mock source — no API calls, no DB writes |
| `--dry-run` | Real API calls but skip DB writes |
| `--clean` | Clear all 1C/PIM tables before loading |

## Environment Variables

| Variable | Purpose |
|----------|---------|
| `ONEC_API_URL` | 1C Goods+Prices API base URL (with basic auth) |
| `ONEC_PIM_URL` | PIM Goods API URL (with basic auth) |
| `PG_PWD` | PostgreSQL password (for backend=postgres) |

## Schema

5 tables in the selected backend:

| Table | Rows | Grain | Source API |
|-------|------|-------|-----------|
| `onec_goods` | ~27K | 1 per guid | /feeds/ones/goods/ |
| `onec_goods_sku` | ~140K | 1 per (sku_guid, guid) | /feeds/ones/goods/ (nested) |
| `onec_dimensions` | ~140K | 1 per (good_guid, sku_guid) | Derived from SKU data |
| `onec_prices` | ~660K | 1 per (good_guid, snapshot_date, type_guid) | /feeds/ones/prices/ |
| `pim_goods` | ~25K | 1 per identifier | /feeds/pim/goods/ |
