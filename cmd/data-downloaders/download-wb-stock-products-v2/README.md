# WB Stock Products Downloader v2

Downloads product-level stock metrics from WB Seller Analytics API.

**Endpoint:** `POST /api/v2/stocks-report/products/products`
**Rate limit:** 3 req/min (shared with other stocks-report/search-report endpoints)
**Data:** orders, buyouts, sale rate, availability, lost orders, price range per product.

## Usage

```bash
# Mock mode (no API calls, no DB)
go run . --mock

# Dry run (real API, no writes)
go run . --dry-run --config config.yaml

# Production (user only!)
go run . --config config.yaml

# Specific date
go run . --config config.yaml --date 2026-06-01

# PostgreSQL backend
go run . --config config.yaml --backend postgres --pg-database wb_data_test
```

## Flags

| Flag | Description |
|------|-------------|
| `--config` | Config YAML path (default: `config.yaml`) |
| `--db` | SQLite database path (overrides config) |
| `--backend` | `sqlite` or `postgres` (overrides config) |
| `--pg-database` | PostgreSQL database name (overrides config) |
| `--date` | Snapshot date YYYY-MM-DD (default: yesterday) |
| `--mock` | Mock mode (no API, no DB) |
| `--dry-run` | Real API, skip DB writes |
