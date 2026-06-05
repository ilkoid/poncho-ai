# WB Warehouse Remains Downloader v2

Downloads warehouse remains reports from WB Seller Analytics API (`/api/v1/warehouse_remains`).

## API Details

| Step | Endpoint | Rate Limit | Description |
|------|----------|------------|-------------|
| Create | `GET /api/v1/warehouse_remains` | 1/min, burst 5 | Initiate async report |
| Poll | `GET /api/v1/warehouse_remains/tasks/{id}/status` | 12/min, burst 5 | Check status |
| Download | `GET /api/v1/warehouse_remains/tasks/{id}/download` | 1/min, burst 1 | Get data (bare JSON array) |

Base URL: `https://seller-analytics-api.wildberries.ru`

## Usage

```bash
# Mock mode (no API, no DB)
go run . --mock

# Mock with test SQLite
go run . --mock --db /tmp/test-whremains.db

# Mock with PostgreSQL
go run . --mock --backend postgres --pg-database wb_data_test

# Dry-run (real API, no writes)
go run . --dry-run --db /tmp/test-whremains.db

# Production (user only!)
go run . --config config.yaml
```

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `config.yaml` | Path to config file |
| `--db` | (from config) | SQLite database path |
| `--backend` | `sqlite` | Storage backend: `sqlite` or `postgres` |
| `--pg-database` | (from config) | PostgreSQL database name |
| `--date` | today | Snapshot date (YYYY-MM-DD) |
| `--mock` | `false` | Mock mode (no API, no DB) |
| `--dry-run` | `false` | Real API, no DB writes |

## Architecture

V2 dual-backend: `pkg/whremains/` → Source/Writer interfaces → SQLite or PostgreSQL.

```
pkg/whremains/          → Domain logic (Downloader, interfaces, mocks)
pkg/wb/warehouse_remains.go → Client methods (Create, Poll, Download)
pkg/storage/sqlite/     → SQLite adapter (INSERT OR REPLACE, 50K chunks)
pkg/storage/postgres/   → PostgreSQL adapter (ON CONFLICT, 500 chunks)
cmd/.../main.go         → CLI driver (~130 lines)
```

## Table Schema

```sql
CREATE TABLE warehouse_remains (
    id INTEGER PRIMARY KEY AUTOINCREMENT,  -- BIGSERIAL for PG
    snapshot_date TEXT NOT NULL,
    nm_id INTEGER NOT NULL,                -- BIGINT for PG
    barcode TEXT NOT NULL DEFAULT '',
    tech_size TEXT NOT NULL DEFAULT '0',
    warehouse_name TEXT NOT NULL DEFAULT '',
    brand TEXT NOT NULL DEFAULT '',
    subject_name TEXT NOT NULL DEFAULT '',
    vendor_code TEXT NOT NULL DEFAULT '',
    volume REAL NOT NULL DEFAULT 0,        -- DOUBLE PRECISION for PG
    quantity INTEGER NOT NULL DEFAULT 0,   -- BIGINT for PG
    created_at TEXT DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(snapshot_date, nm_id, tech_size, warehouse_name)
);
```
