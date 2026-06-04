# Audit: download-wb-cards-v2 — Conformance Report

**Date**: 2026-06-04
**Classification**: V2 (full)
**Domain**: cards
**Files reviewed**:
- `cmd/data-downloaders/download-wb-cards-v2/main.go` (198 lines)
- `cmd/data-downloaders/download-wb-cards-v2/config.yaml` (37 lines)
- `cmd/data-downloaders/download-wb-cards-v2/README.md` (65 lines)
- `pkg/cards/types.go` (65 lines)
- `pkg/cards/downloader.go` (143 lines)
- `pkg/cards/source.go` (27 lines)
- `pkg/cards/mock.go` (214 lines)
- `pkg/cards/downloader_test.go` (113 lines)
- `pkg/storage/sqlite/cards_repo.go` (258 lines)
- `pkg/storage/sqlite/cards_schema.go` (160 lines)
- `pkg/storage/postgres/cards_repo.go` (242 lines)
- `pkg/storage/postgres/cards_schema.go` (133 lines)
- `pkg/wb/types.go` (ProductCard struct)
- `pkg/wb/content.go` (GetCardsList method)
- `download-all-v2.sh` (cards integration)

**Compared against**: `download-wb-search-vis-v2` (gold standard V2)
**Backend status**: SQLite + PostgreSQL (dual-backend)
**pkg/cards/ exists**: YES (Source: 1 method, Writer: 2 methods, Reader: N/A)
**Status**: ✅ Production-Ready

---

## V2 Architecture Conformance

### V2-1. Architecture

| # | Check | Status | Note |
|---|-------|--------|------|
| V2-1.1 | pkg/cards/ with Source + Writer interfaces | ✅ | Source: 1 method (`GetCardsPage`), Writer: 2 methods (`SaveCards`, `CountCards`) — ISP compliant |
| V2-1.2 | Only interface fields in Downloader struct | ✅ | `source CardsSource`, `writer CardsWriter`, `opts DownloadOptions` — no concrete types |
| V2-1.3 | Compile-time assertion (SQLite) | ✅ | `var _ cards.CardsWriter = (*SQLiteSalesRepository)(nil)` — cards_repo.go:13 |
| V2-1.3 | Compile-time assertion (PG) | ✅ | `var _ cards.CardsWriter = (*PgCardsRepo)(nil)` — cards_repo.go:16 |
| V2-1.4 | No fmt.Printf / log.* in pkg/cards/ | ✅ | `OnProgress func(msg string)` callback only |
| V2-1.5 | main.go < 200 lines | ✅ | 198 lines — thin CLI driver with wiring only |
| V2-1.6 | YAGNI: no cursor/resume for light domain | ✅ | Cards ~32k records, full reload, no resume |
| V2-1.7 | Reader only when cross-domain deps exist | ✅ | No Reader — cards API doesn't need external data |
| V2-1.8 | No direct client.Post()/client.Get() | ✅ | WBSource delegates to `client.GetCardsList()` |

### V2-2. Configuration

| # | Check | Status | Note |
|---|-------|--------|------|
| V2-2.1 | Config type in pkg/config/utility.go | ⚠️ | Local `cardsConfig` struct in main.go (not in shared pkg) — acceptable: domain-specific |
| V2-2.7 | V2StorageConfig for backend selection | ✅ | `Storage config.V2StorageConfig` with backend/db_path/pg_database/pg_password_env |
| V2-2.8 | Backend switch in CLI | ✅ | `createCardsWriter()` with `switch cfg.Backend` — "postgres" and default "sqlite" |
| V2-2.9 | GetEffectiveDSN() for PG connection | ✅ | `cfg.GetEffectiveDSN()` in createCardsWriter |

### V2-3. Mock Safety (CRITICAL)

| # | Check | Status | Note |
|---|-------|--------|------|
| V2-3.1 | DiscardWriter in pkg/cards/mock.go | ✅ | `sync.Mutex` on `saved` counter, no-op methods |
| V2-3.2 | Mock creates DiscardWriter, NOT real DB | ✅ | Writer created inside `else` branch — mock path uses `cards.NewDiscardWriter()` |
| V2-3.3 | MockReader (if Reader exists) | N/A | No Reader interface for cards |
| V2-3.4 | --mock never opens ANY database | ✅ | `*mockMode == true` path: no `sqlite.New*`, no `postgres.NewPool` |

### V2-4. Rate Limiting

| # | Check | Status | Note |
|---|-------|--------|------|
| V2-4.1 | SetRateLimit for every toolID | ✅ | `wbClient.SetRateLimit(cards.ToolID, ...)` — "get_cards_list" |
| V2-4.2 | ToolID consistency | ✅ | `cards.ToolID = "get_cards_list"` in source.go matches SetRateLimit |
| V2-4.6 | ShareRateLimit for shared limits | N/A | Single endpoint — no shared limits |
| V2-4.7 | WBSource receives rate limit values | ✅ | `cards.NewWBSource(wbClient, cfg.Cards.RateLimit, cfg.Cards.BurstLimit)` |
| V2-4.8 | SetRateLimit called after wb.New() | ✅ | `wb.New(apiKey)` → `wbClient.SetRateLimit(...)` → `cards.NewWBSource(...)` |

### V2-5. Database Operations — Dual Backend

| # | Check | Status | Note |
|---|-------|--------|------|
| V2-5.1 | INSERT OR REPLACE for SQLite upserts | ✅ | Main card + sizes use INSERT OR REPLACE |
| V2-5.2 | UNIQUE constraints match API granularity | ✅ | photos(nm_id,big), sizes(chrt_id), chars(nm_id,char_id), tags(nm_id,tag_id) |
| V2-5.3 | No DELETE FROM inside batch loops | ✅ | DELETE WHERE nm_id = ? — per-card, not table-wide |
| V2-5.5 | SQL placeholder count matches column count | ✅ | SQLite: 21 cols = 20? + CURRENT_TIMESTAMP. PG: $1-$20 + TO_CHAR |
| V2-5.6 | PG schema in separate _schema.go | ✅ | `postgres/cards_schema.go` (133 lines) |
| V2-5.7 | PG uses $1,$2... not ? | ✅ | All SQL uses $N placeholders |
| V2-5.8 | PG uses ON CONFLICT, not INSERT OR REPLACE | ✅ | `ON CONFLICT (nm_id) DO UPDATE SET ... = EXCLUDED` |
| V2-5.9 | PG uses BOOLEAN not INTEGER for bools | ✅ | need_kiz, wholesale_enabled, dim_is_valid → BOOLEAN DEFAULT FALSE |
| V2-5.10 | PG uses DOUBLE PRECISION not REAL | ✅ | dim_length, dim_width, dim_height, dim_weight_brutto → DOUBLE PRECISION |
| V2-5.11 | PG uses BIGSERIAL PRIMARY KEY | ✅ | card_photos, card_characteristics, card_tags → id BIGSERIAL PRIMARY KEY |
| V2-5.14 | PG chunking: 500 per transaction | ✅ | `cardsChunkSize = 500` in both repos |
| V2-5.15 | Context propagation in both backends | ✅ | All Exec/Query use Context variants (SQLite PRAGMA lines exempt) |
| V2-5.16 | SQLite compile-time assertion | ✅ | cards_repo.go:13 |
| V2-5.17 | PG compile-time assertion | ✅ | cards_repo.go:16 |

### V2-6. Error Handling

| # | Check | Status | Note |
|---|-------|--------|------|
| V2-6.1 | Continue on error in batch loops | ⚠️ | Both repos return error on first card failure in chunk — intentional for transaction atomicity |
| V2-6.2 | Ctrl+C handling | ✅ | `signal.NotifyContext` with SIGINT/SIGTERM |
| V2-6.7 | Transaction rollback on error paths | ✅ | `defer tx.Rollback()` after `BeginTx` in both repos |
| V2-6.8 | Error wrapping with context | ✅ | `fmt.Errorf("save chunk at offset %d: %w", i, err)` throughout |
| V2-6.10 | PG pool.Close() in cleanup | ✅ | `return repo, pool.Close, nil` |

### V2-7. CLI Interface

| # | Check | Status | Note |
|---|-------|--------|------|
| V2-7.1 | Standard flags | ✅ | `--config`, `--help` |
| V2-7.3 | Mock mode flag | ✅ | `--mock` |
| V2-7.5 | Backend flags | ✅ | `--backend`, `--db`, `--pg-database` |
| V2-7.6 | dllog for all output | ✅ | PrintHeader + Progress + Done. Only `log.Fatalf` for fatal errors |
| V2-7.7 | signal.NotifyContext | ✅ | `signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)` |
| V2-7.8 | resolveAPIKey returns VALUE | ✅ | Uses `os.Getenv(cfg.Cards.APIKeyEnv)` correctly |

### V2-8. Testing

| # | Check | Status | Note |
|---|-------|--------|------|
| V2-8.1 | E2E test with mock client | ✅ | `downloader_test.go` — 4 test functions |
| V2-8.4 | Minimum 4 test cases | ✅ | Basic, DryRun, Limit, ContextCancel |
| V2-8.5 | Tests use MockSource only | ✅ | No `sqlite.New*`, no `postgres.NewPool` |
| V2-8.6 | Context cancellation uses errors.Is() | ❌ INFO | Bare `err == nil` check — should use `errors.Is(err, context.DeadlineExceeded)` |

### V2-9. Code Reuse

| # | Check | Status | Note |
|---|-------|--------|------|
| V2-9.1 | Config types from pkg/config/ | ✅ | WBClientConfig, V2StorageConfig reused |
| V2-9.2 | Storage ops in storage packages | ✅ | SQL in `pkg/storage/sqlite/` and `pkg/storage/postgres/` |
| V2-9.3 | Typed WB client methods | ✅ | `client.GetCardsList()` — no raw HTTP |
| V2-9.4 | No duplicate constants V1/V2 | ✅ | ToolID in `pkg/cards/source.go`, not duplicated |

### V2-10. Hardcode Audit

| # | Check | Status | Note |
|---|-------|--------|------|
| V2-10.1 | API URLs are constants | ✅ | No hardcoded URLs in pkg/cards/ or main.go |
| V2-10.2 | Rate limits from config | ✅ | `cfg.Cards.RateLimit`, `cfg.Cards.BurstLimit` |
| V2-10.3 | DB paths from config | ✅ | Not hardcoded in main.go |
| V2-10.4 | Timeouts configurable | ✅ | No `time.Sleep` in code |
| V2-10.5 | Table names consistent | ✅ | Same names in schema and queries |
| V2-10.7 | Batch/chunk as named constants | ✅ | `cardsChunkSize = 500` |
| V2-10.9 | Default days from config | N/A | Cards = snapshot, no date range |

### V2-11. YAML Config Completeness

| # | Check | Status | Note |
|---|-------|--------|------|
| V2-11.1 | Every parameter has a comment | ✅ | All YAML keys have `#` comments with purpose |
| V2-11.2 | Config struct matches YAML 1:1 | ❌ WARNING | `filter:` section was parsed but never wired (dead config) — **FIXED**: removed |
| V2-11.3 | Domain section with rate limits + API key env | ✅ | `cards:` section with api_key_env, rate_limit, burst_limit |
| V2-11.4 | Storage section with V2StorageConfig | ✅ | `storage:` with backend, db_path, pg_database, pg_password_env |
| V2-11.5 | Rate limits with Swagger rate comments | ✅ | `# Запросов в минуту (swagger: 100/min)` |
| V2-11.7 | Config file in cmd/.configs/download-all/ | ❌ WARNING | No dedicated v2 config — **FIXED**: created download-wb-cards-v2.yaml |

---

## Anti-Patterns Scan (18 APs)

| AP# | Name | Severity | Status | Evidence |
|-----|------|----------|--------|----------|
| AP-1 | SQL Placeholder Mismatch | CRITICAL | ✅ NOT FOUND | SQLite: 21 cols = 20? + CURRENT_TIMESTAMP. PG: $1-$20 + TO_CHAR |
| AP-2 | io.Reader Reuse on Retry | CRITICAL | ✅ NOT FOUND | Uses `wb.Client.GetCardsList()` — retry handled internally |
| AP-3 | Date Off-By-One | WARNING | N/A | Cards = full snapshot, no date range |
| AP-4 | Missing Error Continuation | WARNING | ✅ ACCEPTABLE | Returns error on first card failure — transaction atomicity requires rollback |
| AP-5 | DELETE ALL Inside Batch | CRITICAL | ✅ NOT FOUND | All DELETEs have `WHERE nm_id = ?` |
| AP-6 | JSON Tags vs Swagger | CRITICAL | ✅ NOT FOUND | All camelCase tags match Swagger: nmID, imtID, vendorCode, createdAt, etc. |
| AP-7 | Shared Rate Limits | WARNING | ✅ NOT FOUND | Single endpoint, single rate config |
| AP-8 | Database Grain Mismatch | CRITICAL | ✅ NOT FOUND | UNIQUEs match API granularity exactly |
| AP-9 | doRequest() Bypass | CRITICAL | ✅ NOT FOUND | WBSource delegates to `client.GetCardsList()` |
| AP-10 | resolveAPIKey Returns Name | CRITICAL | ✅ NOT FOUND | Uses `os.Getenv(cfg.Cards.APIKeyEnv)` |
| AP-11 | Two Lines Per Page | INFO | ✅ NOT FOUND | Uses `dllog.Progress()` — single line per page |
| AP-12 | Type Name Collision | WARNING | ✅ NOT FOUND | Uses `config.V2StorageConfig` |
| AP-13 | Dead YAML Field | WARNING | ❌ FOUND → FIXED | `filter:` section removed from config.yaml and Config struct |
| AP-14 | God-Object Repository | WARNING | ✅ NOT FOUND | PgCardsRepo: 2 interface methods + 1 chunk helper |
| AP-15 | pgx Indirect Dependency | WARNING | ✅ NOT FOUND | Direct `pgx/v5/pgxpool` import |
| AP-16 | Hardcoded Boolean | WARNING | ✅ NOT FOUND | PG schema: `BOOLEAN NOT NULL DEFAULT FALSE` |
| AP-17 | Expression Index Multi-Stmt | WARNING | N/A | No expression indexes |
| AP-18 | Aggregate in Value Type | CRITICAL | N/A | `COUNT(*)` scans into `int` — safe, always returns a row |

---

## Summary Statistics

- **CRITICAL**: 0
- **WARNING**: 3 → 2 FIXED (dead filter config removed, v2 config created), 1 remaining (pipeline activation)
- **INFO**: 4 → 1 FIXED (test assertion), 3 remaining (redundant assertion removed, missing swagger field, line count)
- **GOOD**: 40+

## V2 Architecture Score

| Criterion | Status |
|-----------|--------|
| pkg/cards/ with Source/Writer | ✅ YES |
| Compile-time assertions (SQLite + PG) | ✅ YES |
| DiscardWriter mock safety | ✅ YES |
| Dual-backend parity (schema + upsert) | ✅ YES |
| OnProgress callback (no fmt.Printf) | ✅ YES |
| main.go < 200 lines | ✅ YES (198 lines) |
| README.md present | ✅ YES (65 lines) |
| config.yaml fully commented | ✅ YES |
| Minimum 4 tests | ✅ YES (4: Basic, DryRun, Limit, ContextCancel) |
| download-all-v2.sh integration | ⚠️ PARTIAL → ✅ FIXED (uncommented + v2 config created) |

**Score: 10/10** (after fixes applied)

---

## Fixes Applied

| # | File | Change |
|---|------|--------|
| 1 | `cmd/.../main.go:34-38` | Removed dead `Filter config.FunnelFilterConfig` field |
| 2 | `cmd/.../config.yaml:30-36` | Removed dead `filter:` section + updated header |
| 3 | `pkg/cards/downloader_test.go:110-112` | `errors.Is(err, context.DeadlineExceeded)` instead of `err == nil` |
| 4 | `pkg/storage/postgres/cards_repo.go:242` | Removed redundant `var _ pgx.Tx = (pgx.Tx)(nil)` + unused import |
| 5 | `cmd/.configs/download-all/download-wb-cards-v2.yaml` | NEW — v2 pipeline config with `backend: sqlite` |
| 6 | `download-all-v2.sh:46` | Uncommented cards-v2, fixed config path to v2 yaml |

**Verification**: `go test ./pkg/cards/ -v` — 4/4 PASS. `go vet` clean.
