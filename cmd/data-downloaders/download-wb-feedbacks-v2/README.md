# WB Feedbacks Downloader v2

Downloads feedbacks and questions from WB Feedbacks API with dual-backend support (SQLite + PostgreSQL).

## Architecture

V2 dual-backend: business logic in `pkg/feedbacks/`, this is a thin CLI driver.

- **Source** — WB Feedbacks API (`feedbacks-api.wildberries.ru`)
- **Writer** — SQLite or PostgreSQL (configurable)
- **Downloader** — orchestrates two-pass download (unanswered → answered) with pagination + period splitting for questions

## API Endpoints

| Endpoint | Rate | Pagination |
|----------|------|------------|
| `/api/v1/feedbacks` | 3 req/sec | take/skip (max take=5000, max skip=199990) |
| `/api/v1/questions` | 3 req/sec | take/skip (take+skip ≤ 10000, period splitting) |

API key: `WB_API_FEEDBACK_KEY`

## Usage

```bash
# Mock mode — no API calls, no DB writes
go run . --mock

# Mock + test SQLite
go run . --mock --db /tmp/test-feedbacks.db

# Mock + test PostgreSQL
go run . --mock --backend postgres --pg-database wb_data_test

# Dry-run — real API, no writes
WB_API_FEEDBACK_KEY=xxx go run . --dry-run --days=1 --db /tmp/test-feedbacks.db

# Production — last 7 days to SQLite (user only!)
WB_API_FEEDBACK_KEY=xxx go run . --days=7

# Only feedbacks, skip questions
go run . --feedbacks-only --days=7

# Specific date range + PostgreSQL
WB_API_FEEDBACK_KEY=xxx go run . --begin=2026-01-01 --end=2026-01-31 --backend postgres

# Custom config
go run . --config /path/to/config.yaml
```

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `config.yaml` | Path to config file |
| `--db` | from config | Database path (SQLite) |
| `--backend` | `sqlite` | Storage backend: `sqlite` or `postgres` |
| `--pg-database` | from config | PostgreSQL database name |
| `--begin` | | Begin date (YYYY-MM-DD) |
| `--end` | | End date (YYYY-MM-DD) |
| `--days` | `7` | Days from today |
| `--feedbacks-only` | `false` | Download only feedbacks |
| `--questions-only` | `false` | Download only questions |
| `--mock` | `false` | Mock mode (no API, no DB) |
| `--dry-run` | `false` | Real API, skip DB writes |

## Database Schema

### feedbacks (39 data columns)

PK: `id TEXT` — UUID string from API.

Key indexes: `created_date`, `product_nm_id`.

### questions (15 columns)

PK: `id TEXT` — UUID string from API.

Key indexes: `created_date`, `product_nm_id`.

## Two-Pass Download Logic

Each endpoint (feedbacks and questions) is called twice per date range:
1. `isAnswered=false` — fetch unanswered items
2. `isAnswered=true` — fetch answered items

This ensures complete coverage since the API partitions results by answer status.

## Questions Period Splitting

The Questions API has a hard limit: `take + skip ≤ 10000`. When both the first page (skip=0, take=5000) and second page (skip=5000, take=5000) are full, the date range is recursively split in half (max depth 10) to ensure no data is missed.
