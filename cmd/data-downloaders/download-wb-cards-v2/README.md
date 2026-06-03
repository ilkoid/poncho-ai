# WB Cards Downloader v2

Downloads product cards from WB Content API (`/content/v2/get/cards/list`).

Full snapshot of all product cards — cursor-based pagination, replaces previous snapshot on each run. Light domain (~32k records, 3-5 min download).

## Features

- **Dual-backend:** SQLite + PostgreSQL
- **Safe mock mode:** `--mock` uses DiscardWriter — zero database interaction
- **Adaptive rate limiting:** 100 req/min WB Content API with automatic 429 recovery
- **Cursor-based pagination:** WB Content API native cursor

## Usage

```bash
# Mock mode — no API calls, no DB writes
go run . --mock

# Mock + test SQLite database
go run . --mock --db /tmp/test-cards.db

# Mock + test PostgreSQL database
go run . --mock --backend postgres --pg-database wb_data_test

# Dry-run — real API, no writes
go run . --dry-run --db /tmp/test-cards.db

# Production (user only!)
go run . --config config.yaml
```

## Configuration

See `config.yaml` for full config with comments.

Key config types:
- `cards` — rate limiting, adaptive tuning, API key env var
- `storage` — `config.V2StorageConfig` (backend, db_path, pg_database)
- `filter` — `config.FunnelFilterConfig` (exclude_lengths, allowed_years)

## Database Schema

Table: `cards` (parent)
- Primary key: `nm_id` (WB article ID)
- Indexes: `vendor_code`, `brand`, `subject_id`

Child tables (CASCADE delete):
- `card_photos` — product images (UNIQUE: nm_id, big)
- `card_characteristics` — product attributes (UNIQUE: nm_id, char_id)
- `card_sizes` — size/SKU variants (UNIQUE: nm_id, chrt_id)
- `card_tags` — seller tags (UNIQUE: nm_id, tag_id)

## Rate Limiting

- **WB Content API:** 100 req/min, burst 5
- **Pagination:** cursor-based (WB API native), page size 100
- **Full snapshot:** no date range, upsert on each run

## Safety

⚠️ **Mock mode is safe by design:**
- `--mock` = MockCardsSource + DiscardWriter → **zero DB interaction**
- All test commands must use `--db /tmp/...` or `--pg-database wb_data_test`
