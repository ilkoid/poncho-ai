# WB Campaigns Downloader v2

Downloads basic campaign data from WB Promotion API (3 phases, 6 tables).

Dual-backend: **SQLite** (default) or **PostgreSQL** via `--backend` flag.

## Phases

| Phase | API Endpoint | Tables | Rate Limit |
|-------|-------------|--------|------------|
| 1. Campaigns | `GET /adv/v1/promotion/count` | `campaigns` | 300/min |
| 2. Details | `GET /api/advert/v2/adverts` | `campaigns` (UPDATE) | 300/min |
| 3. Fullstats | `GET /adv/v3/fullstats` | 4 stat tables | **3/min** |

## Tables

| Table | Grain | Source |
|-------|-------|--------|
| `campaigns` | `advert_id` | Phase 1 + 2 |
| `campaign_stats_daily` | `(advert_id, stats_date)` | Phase 3 days[] |
| `campaign_stats_app` | `(advert_id, stats_date, app_type)` | Phase 3 apps[] |
| `campaign_stats_nm` | `(advert_id, stats_date, app_type, nm_id)` | Phase 3 nms[] |
| `campaign_booster_stats` | `(advert_id, stats_date, nm_id)` | Phase 3 boosterStats[] |
| `campaign_products` | `(advert_id, nm_id)` | Post-run rebuild |

## Usage

```bash
# Mock mode (no API, no DB)
go run . --mock

# Real API + SQLite
WB_API_ANALYTICS_AND_PROMO_KEY=xxx go run . --days 7

# Real API + PostgreSQL
WB_API_ANALYTICS_AND_PROMO_KEY=xxx go run . --backend postgres --pg-database wb_data_test

# Resume from last loaded date
go run . --days 30 --resume

# Dry run (API calls, no writes)
go run . --dry-run --days 7
```

## Architecture

V2 dual-backend: business logic in `pkg/campaigns/`, CLI is thin driver (~130 lines).

```
pkg/campaigns/
    types.go          — CampaignsSource (3), CampaignsWriter (5), Options, Result
    source.go         — WBSource adapter + 3 ToolID constants
    downloader.go     — Downloader + Run() (3 phases)
    mock.go           — MockCampaignsSource + DiscardWriter

pkg/storage/sqlite/
    promotion_repo.go — SaveFullstats() wrapper + compile-time assertion

pkg/storage/postgres/
    campaigns_schema.go — PG DDL for 6 tables
    campaigns_repo.go   — PgCampaignsRepo implementing CampaignsWriter
```
