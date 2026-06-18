# WB Cards Downloader v3

Downloads product cards from WB Content API (`/content/v2/get/cards/list`) with
**preventive substring scrubbing at load time**.

**v3 = v2 architecture + a scrubbing Writer decorator.** It is a separate utility
so that `download-wb-cards-v2` and `pkg/cards` stay untouched. Scrubbing rewrites
sensitive substrings (e.g. brand names) in card string and json.RawMessage fields
before persistence; rules come from a YAML file (`storage.scrub_rules_path`,
see [pkg/scrub](../../../pkg/scrub)). When `scrub_rules_path` is empty, v3 behaves
identically to v2.

Full snapshot of all product cards — cursor-based pagination, replaces previous
snapshot on each run. Light domain (~32k records, 3-5 min download).

## Features

- **Everything v2 has:** dual-backend (SQLite + PostgreSQL), safe mock mode,
  adaptive rate limiting, cursor-based pagination.
- **NEW — preventive scrubbing:** substring masking at load time via a
  `cards.CardsWriter` decorator (`scrub.go`). `pkg/cards` is not modified.

## Usage

```bash
# Mock mode — no API calls, no DB writes (scrub rules load + decorator runs, then discards)
go run . --mock --config <v3-config-with-scrub.yaml>

# Mock + test SQLite database
go run . --mock --db /tmp/test-cards.db

# Dry-run — real API, no writes (NOTE: scrub lives in SaveCards, so --dry-run skips it)
go run . --dry-run --db /tmp/test-cards.db

# Production with scrub (user only!)
go run . --config cmd/.configs/download-all/download-wb-cards-v3-PG.yaml
```

## Scrubbing

Enable by setting `storage.scrub_rules_path` in the config (see
[scrub-rules.yaml](../../.configs/download-all/scrub-rules.yaml)):

```yaml
storage:
  scrub_rules_path: "cmd/.configs/download-all/scrub-rules.yaml"
```

Semantics mirror `cmd/fix-utilities/fix-scrub-substring` (the post-mortem SQL
tool): case-insensitive global literal replace by default. Covers string fields
and `json.RawMessage` (characteristic values), recursing into nested structs.
The two tools are complementary — v3 handles new writes, the SQL tool remains the
backstop for historical data.

## Configuration

See `config.yaml` for full config with comments. Key types:
- `cards` — rate limiting, adaptive tuning, API key env var
- `storage` — `config.V2StorageConfig` (backend, db_path, pg_database, scrub_rules_path)

## Database Schema

Same as v2. Table `cards` (parent, PK `nm_id`) + child tables
(`card_photos`, `card_characteristics`, `card_sizes`, `card_tags`) with CASCADE delete.

## Rate Limiting

- **WB Content API:** 100 req/min, burst 5
- **Pagination:** cursor-based (WB API native), page size 100
- **Full snapshot:** no date range, upsert on each run

## Safety

⚠️ **Mock mode is safe by design:** `--mock` = MockCardsSource + DiscardWriter →
**zero DB interaction**. All test commands must use `--db /tmp/...` or
`--pg-database wb_data_test`. Production runs on `/var/db` or `wb_data_prod` are
**user-only**.
