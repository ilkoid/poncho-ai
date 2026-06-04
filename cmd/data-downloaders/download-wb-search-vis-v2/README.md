# WB Search Visibility Downloader v2

Downloads organic search positions and top queries from WB Seller Analytics API.

## Features

- **Dual-backend**: SQLite and PostgreSQL via `--backend` flag
- **Two phases**: Positions (visibility %, avg position) → Queries (top search texts)
- **Smart filtering**: By article length, year digits, sales activity
- **Mock mode**: `--mock` runs with fake data, zero DB interaction
- **Dry-run**: `--dry-run` shows what would be saved without writing

## API Endpoints

| Endpoint | Method | Batch | Rate |
|----------|--------|-------|------|
| `/api/v2/search-report/report` | POST | ≤100 nmIDs | 3 req/min |
| `/api/v2/search-report/product/search-texts` | POST | ≤50 nmIDs | 3 req/min |

Both endpoints share a global 3 req/min rate limit.

## Usage

```bash
# Mock mode (no API, no DB)
go run . --mock

# Mock with specific backend
go run . --mock --backend postgres --pg-database wb_data_test

# Production — SQLite (default)
go run . --config config.yaml

# Production — PostgreSQL
go run . --config config.yaml --backend postgres

# Specific products with higher query limit
go run . --nm-ids=123456,789012 --limit=50

# Positions only (skip slow query download)
go run . --skip-queries --days=7

# Dry-run (real API, no writes)
go run . --dry-run --config config.yaml
```

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `config.yaml` | Config file path |
| `--backend` | `sqlite` | Storage backend: sqlite\|postgres |
| `--db` | from config | Database path (SQLite) |
| `--pg-database` | from config | PostgreSQL database name |
| `--mock` | false | Use mock source (no API calls, no DB) |
| `--dry-run` | false | Real API, skip DB writes |
| `--nm-ids` | auto | Comma-separated nmID list |
| `--begin` | from config | Begin date (YYYY-MM-DD) |
| `--end` | from config | End date (YYYY-MM-DD) |
| `--days` | 7 | Days from today |
| `--limit` | 30 | Max queries per product |
| `--skip-positions` | false | Skip positions download |
| `--skip-queries` | false | Skip queries download |

## Architecture

```
pkg/searchvis/          — Source, Writer, Reader interfaces + Downloader
pkg/storage/sqlite/     — SQLite adapter (Writer + Reader)
pkg/storage/postgres/   — PostgreSQL adapter (Writer + Reader)
cmd/.../main.go         — CLI driver (~160 lines)
```

Reader interface provides nmIDs from the same backend as Writer (dual-backend consistency).
