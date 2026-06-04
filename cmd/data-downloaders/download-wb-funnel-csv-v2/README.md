# WB Funnel CSV Downloader v2

Downloads funnel data from WB Analytics via async CSV reports (nm-report API).

Unlike the JSON-based funnel downloader (`download-wb-funnel`), this uses CSV export:
1. **Create report** → WB API queues a CSV export job
2. **Poll status** → wait until report is ready (30s intervals)
3. **Download ZIP** → fetch the archive containing CSV
4. **Parse & Save** → extract rows and write to DB

## V2 Features

- **Dual-backend**: SQLite + PostgreSQL via `--backend` flag
- **Mock safety**: `--mock` uses DiscardWriter (zero DB interaction)
- **Report-level resume**: skips if a successful report already exists for the period
- **Two report types**: `detail` (per nmID per day) and `grouped` (per day)

## Usage

```bash
# Mock mode — no API calls, no DB
go run . --mock

# Mock + specific backend
go run . --mock --backend sqlite --db /tmp/test.db
go run . --mock --backend postgres --pg-database wb_data_test

# Real API, dry-run (no writes)
go run . --dry-run --config config.yaml

# Production — SQLite (default)
go run . --config config.yaml

# Production — PostgreSQL
go run . --config config.yaml --backend postgres --pg-database wb_data_prod
```

## CLI Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `config.yaml` | Path to config file |
| `--days` | `7` | Days from today |
| `--report-type` | `detail` | Report type: detail or grouped |
| `--poll-interval` | `30` | Poll interval in seconds |
| `--poll-timeout` | `30` | Poll timeout in minutes |
| `--mock` | `false` | Use mock source (no API calls) |
| `--dry-run` | `false` | Real API, no DB writes |
| `--backend` | `sqlite` | Storage backend: sqlite or postgres |
| `--db` | config | SQLite database path |
| `--pg-database` | config | PostgreSQL database name |
