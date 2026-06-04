---
name: downloader-audit
description: Audit the quality of WB API data downloaders. Use this skill whenever the user asks to audit a downloader, review downloader code, check downloader quality, verify a new downloader, or mentions downloaders in the context of quality, correctness, bugs, or best practices. Also trigger for rate limiting issues, JSON tag mismatches, SQL placeholder bugs, error handling gaps, mock safety, DiscardWriter, PG adapter correctness, dual-backend consistency, compile-time assertions, or anti-patterns. Covers 29 downloaders. V2 (dual-backend): cards-v2, prices-v2, orders-v2, opsales-v2, sales-v2, stocks-v2, funnel-v2, feedbacks-v2, campaigns-v2, region-sales-v2, search-vis-v2, funnel-csv-v2. V1 (SQLite-only): cards, prices, feedbacks, funnel, funnel-agg, funnel-csv, stocks, stock-history, sales, region-sales, search-visibility, promotion, promotion-v2, supplies, 1c-data, 1c-rests, all-articles.
---

# Downloader Audit

Systematic quality audit of WB API data downloaders across 6 dimensions:

| Dimension | V1 | V2 | Reference |
|-----------|----|----|-----------|
| Architecture (Source/Writer) | N/A | Full | dev_v2_downloader.md |
| Go best practices (SOLID) | Basic | Full | dev_best_practices.md, dev_solid.md |
| Code duplication / reuse | Basic | Full | dev_swagger_reusable_packages.md |
| Hardcode detection | Full | Full | dev_v2_downloader.md Rule 10 |
| YAML config completeness | Basic | Full | dev_v2_postgres.md Pattern 1.7 |
| Shell scripts | download-all.sh | download-all-v2.sh | download-all.sh |

Auto-detects V1 vs V2 and applies the appropriate checklist depth.

## Output Format (IMPORTANT)

The audit report is the **primary deliverable** — it must be delivered in **TWO places**:

1. **In the chat conversation** — so the requester can read it immediately
2. **In `reports/` file** — persisted at `reports/YYYY-MM-DD_<downloader-name>.md` in the project root

Do NOT write the report to a plan file or suppress it. The report is NOT a refactoring plan — it's a **conformance audit** showing what matches the standard and what doesn't.

**Two-part output:**

### Part 1: Conformance Report (shown in chat)

A readable table with **every checklist item**, not just violations. The requester needs to see what's ✅ good and what's ❌ broken — the full picture.

Structure:

```
## Audit: <name> — Conformance Report

**Classification**: V2 (full) | Domain: cards | Backend: SQLite + PG
**Status**: ✅ Production-Ready | ⚠️ Needs Fixes | ❌ Broken

### V2 Architecture Conformance

| # | Check | Status | Note |
|---|-------|--------|------|
| V2-1.1 | pkg/<domain>/ with Source + Writer | ✅ | Source: 1 method, Writer: 2 methods |
| V2-1.2 | Only interface fields in Downloader | ✅ | No *wb.Client, no *sqlite.* |
| V2-1.3 | Compile-time assertions (SQLite) | ✅ | var _ cards.Writer = (*SQLiteSalesRepository)(nil) |
| V2-1.3 | Compile-time assertions (PG) | ✅ | var _ cards.Writer = (*PgCardsRepo)(nil) |
| V2-1.4 | No fmt.Printf in pkg/<domain>/ | ✅ | OnProgress callback only |
| V2-1.5 | main.go < 200 lines | ✅ | 198 lines |
| ... | ... | ... | ... |
| V2-3.2 | Mock creates DiscardWriter, NOT real DB | ✅ | Writer inside else branch |
| V2-11.2 | Config struct matches YAML 1:1 | ❌ | filter: section is dead — parsed but never wired |
```

**Status markers:**
- ✅ — Compliant with standard
- ❌ — Violation found (include severity: CRITICAL/WARNING/INFO)
- ⚠️ — Partial compliance
- N/A — Not applicable (e.g., date checks for snapshot downloaders)

**Include ALL items** from the V2 Checklist (V2-1 through V2-11) or V1 Checklist, depending on classification. Items that are GOOD still appear in the table — the requester wants the full conformance picture, not just bugs.

### Part 2: Summary + Recommendations (shown in chat, after the table)

```
### Summary Statistics
- CRITICAL: 0 | WARNING: 3 | INFO: 4 | GOOD: 20+

### Anti-Patterns Scan (18 APs)
| AP# | Name | Status | Evidence |
|-----|------|--------|----------|
| AP-1 | SQL Placeholder Mismatch | ✅ NOT FOUND | 21 cols = 20? + CURRENT_TIMESTAMP |
| AP-13 | Dead YAML Field | ❌ FOUND | filter: section not wired |

### V2 Architecture Score
| Criterion | Status |
|-----------|--------|
| Source/Writer interfaces | ✅ |
| Dual-backend parity | ✅ |
| ... | ... |

### Recommendations (ordered by impact)
1. [WARNING] Wire or remove dead filter config — main.go:38
2. [WARNING] Activate in download-all-v2.sh — line 46
3. ...
```

**After showing the report**, ask the requester if they want to proceed with fixes. If yes, implement the recommendations as code changes.

## Reference Documentation (priority order)

More specific document overrides more general:

1. `dev_manifest.md` — Immutable rules (highest priority, Rule 6 etc.)
2. `dev_v2_postgres.md` — PG dual-backend (overrides downloader guide for PG-specific)
3. `dev_v2_downloader.md` — V2 architecture + Rules 1-11 + 13 APs
4. `dev_best_practices.md` — General Go patterns (Rule 0: Code Reuse)
5. `dev_solid.md` — SOLID principles adapted for Go idioms
6. `dev_swagger_reusable_packages.md` — Write-utility safety, readonly interfaces

## Audit Workflow

Execute these phases in order. After each phase, record findings with severity levels.

### Phase 0: Classification (NEW)

Determines V1 vs V2 routing. This is the core routing mechanism.

**Detection rules (applied in order):**

1. **Primary signal:** Does `cmd/data-downloaders/<name>/main.go` import `config.V2StorageConfig`?
   - YES = V2 downloader → run V2 Checklist (11 categories)
   - NO = V1 downloader → run V1 Checklist (6 categories)

2. **Secondary signal (V2 confirmation):** Does `pkg/<domain>/` exist with `types.go` containing Source/Writer interfaces?

3. **Special cases:**
   - `download-wb-promotion-v2` — V1 architecture despite `-v2` suffix (no `V2StorageConfig`, no `pkg/promotion/`)
   - `download-1c-data`, `download-1c-rests`, `download-all-articles` — non-WB (skip Swagger verification, no rate limiting checks)

**Output of Phase 0:**

```
Target: <name>
Classification: V1 | V2 (full) | V2 (partial)
Domain: <domain>
pkg/<domain>/ exists: YES (Source: N, Writer: N, Reader: N) | NO
Dual-backend: YES (SQLite + PG) | NO (SQLite only)
Reader needed: YES | NO
```

### Phase 1: Discovery

1. Read ALL source files in `cmd/data-downloaders/<name>/`
2. **V2 only:** Read `pkg/<domain>/types.go`, `downloader.go`, `mock.go`, `source.go` (if exists), `*_test.go`
3. **V2 only:** Read `pkg/storage/postgres/<domain>_schema.go` and `pkg/storage/postgres/<domain>_repo.go`
4. Read the relevant config type from `pkg/config/utility.go` [Rule 0: dev_best_practices.md]
5. Read `pkg/storage/sqlite/<domain>_repo.go` for SQLite schema
6. Read both config files: `cmd/<name>/config.yaml` and `cmd/.configs/download-all/<name>.yaml`

### Phase 2: Gold Standard Comparison

**V2 gold standard:** `download-wb-search-vis-v2` — most complete V2 (Source + Writer + Reader, dual-backend, MockReader + DiscardWriter).

| Reference File | Pattern Demonstrated |
|----------------|---------------------|
| `pkg/searchvis/types.go` | Source (2 methods), Writer (4 methods), Reader (3 methods), Row types with UNIQUE constraint comments |
| `pkg/searchvis/source.go` | WBSource thin adapter, delegates to typed `client.GetSearchReport()` [Rule 11] |
| `pkg/searchvis/downloader.go` | `Downloader` with interface fields only, `OnProgress` callback [Rule 4] |
| `pkg/searchvis/mock.go` | `MockSource` + `DiscardWriter` (thread-safe) + `MockReader` [Pattern 1.8] |
| `pkg/storage/sqlite/search_visibility_repo.go` | Compile-time assertion: `var _ searchvis.Writer = (*SQLiteSalesRepository)(nil)` |
| `pkg/storage/postgres/searchvis_repo.go` | Dual assertion: Writer + Reader, `$1,$2`, `ON CONFLICT`, `BOOLEAN` |
| `cmd/.../download-wb-search-vis-v2/main.go` | ~160 lines, `createBackend()`, mock safety, `dllog`, `signal.NotifyContext` |

See [examples/v2_gold_standard.go](examples/v2_gold_standard.go) and [examples/v2_cli_driver.go](examples/v2_cli_driver.go) for condensed reference patterns.

**V1 gold standard:** `download-wb-search-visibility` — retained for V1-only audits.

### Phase 3: Checklist Execution

If V1 → run **V1 Checklist** below (6 categories).
If V2 → run **V2 Checklist** below (11 categories, superset).

### Phase 4: Anti-Pattern Scan

Explicitly search the code for all 18 anti-patterns (see Anti-Pattern Catalog below). Use grep to find suspicious patterns. Full WRONG/RIGHT code in [examples/anti_patterns.md](examples/anti_patterns.md).

### Phase 5: Swagger Verification (optional)

If the downloader interacts with WB API endpoints, verify JSON tags against Swagger specs. Skip for non-WB downloaders.

Swagger file mapping:

| Downloader | Swagger File |
|------------|-------------|
| promotion, promotion-v2 | `docs/wb_api_swagger/08-promotion.yaml` |
| funnel, funnel-agg, search-vis-v2, search-visibility | `docs/wb_api_swagger/11-analytics.yaml` |
| cards, cards-v2, prices, prices-v2 | `docs/wb_api_swagger/02-products.yaml` |
| feedbacks, feedbacks-v2 | `docs/wb_api_swagger/09-communications.yaml` |
| sales, stock-history, region-sales, orders-v2, opsales-v2 | `docs/wb_api_swagger/12-reports.yaml` |
| supplies | `docs/wb_api_swagger/07-orders-fbw.yaml` |
| campaigns, campaigns-v2 | `docs/wb_api_swagger/08-promotion.yaml` |

**V2 additional check:** Verify Source interface methods map to typed `pkg/wb/` client methods [Rule 11: dev_v2_downloader.md]. No direct `client.Post()`/`client.Get()` from domain package.

Naming convention rules:
- `/adv/v0/*` = snake_case (`advert_id`, `nm_id`, `norm_query`)
- `/adv/v1/*` = camelCase (`advertId`, `updNum`, `campName`)
- Always verify against the actual Swagger response schema, not guesses

### Phase 6: Report Generation

Output the structured report using the template in [examples/report_example.md](examples/report_example.md).

---

## V1 Checklist

*Preserved for V1-only downloaders. If V2 detected, use V2 Checklist instead.*

### Configuration

1. **Config type in `pkg/config/utility.go`**: Shared config type with `GetDefaults()`.
2. **`GetDefaults()` does NOT default `Days` field**: Default Days in `main.go` only when both From and To empty. [Severity: CRITICAL]
3. **ENV variable expansion**: Config via `config.LoadYAML()`, not raw `yaml.Unmarshal()`.
4. **API key retrieval**: `getAPIKey()` helper with priority chain, or YAML `${ENV}` expansion.
5. **CLI overrides applied**: `--begin`, `--end`, `--days`, `--db` override config.
6. **Date calculation**: `endDate = yesterday` (exclude today). Exception: funnel.

### Rate Limiting

1. **`SetRateLimit()` for every toolID**: Every API endpoint must have corresponding `SetRateLimit()`. [CRITICAL]
2. **ToolID consistency**: String in `SetRateLimit("X")` must match API call `Get(ctx, "X", ...)`. [CRITICAL]
3. **Separate limits for different Swagger rates**: Different endpoints with different rates need separate config fields. [WARNING]
4. **Adaptive params configured**: `SetAdaptiveParams()` called after rate limits.
5. **Rate limits in config.yaml have Swagger rate comments**.

### Database Operations

1. **INSERT OR REPLACE for idempotent upserts**: Not plain INSERT. [WARNING]
2. **UNIQUE constraints match API response granularity**: All varying fields included. [CRITICAL]
3. **No `DELETE FROM table` inside batch save functions**: Per-item or pre-loop only. [CRITICAL]
4. **Schema registered in `initSchema()`**: Table creation in `pkg/storage/sqlite/repository.go`.
5. **SQL placeholder count matches column count**: Count `?` vs columns. [CRITICAL]

### Error Handling

1. **Continue on error in batch loops**: Log + continue, not `return err`. [WARNING]
2. **Ctrl+C handling**: `context.WithCancel` + signal handler.
3. **POST body recreation on retry**: Handled in `pkg/wb/client.go` — verify if custom HTTP.
4. **No manual 5xx retry**: Built into `wb.Client`.
5. **No custom 429 handling**: Built into adaptive rate limiting.
6. **Context propagation**: `ExecContext`/`QueryContext`, not bare `Exec`/`Query`. [WARNING]
7. **Transaction rollback on error paths**: `defer tx.Rollback()` after `BeginTx`.
8. **Error wrapping with context**: `fmt.Errorf("op: %w", err)`, not bare `return err`. [INFO]

### API Integration

1. **JSON tags verified against Swagger**: Check hallucinated fields, missing fields, type mismatches. [CRITICAL]
2. **JSON unmarshal test exists**: Test verifying parsed field values are non-zero.
3. **Pagination handled correctly**: Cursor-based vs offset-based vs no-pagination.
4. **Period splitting for date ranges**: Large ranges split into chunks.
5. **Response unwrapping**: WB API `{"data": ...}` envelope handled.

### CLI Interface

1. **Standard flags**: `--config`, `--help`.
2. **Period flags**: `--days`, `--begin`, `--end`.
3. **Mock mode**: `--mock` flag.
4. **Help text**: Usage with examples and API endpoint info.

### Testing

1. **E2E test with mock client**: `*_test.go` exists.
2. **Database verification**: Tests verify row counts.
3. **Idempotency test**: Re-saving succeeds without errors.

---

## V2 Checklist

*Superset of V1. Run for all V2 downloaders.*

### V2-1. Architecture [Rules 1-11: dev_v2_downloader.md]

1. **`pkg/<domain>/` exists with Source + Writer interfaces**: Source (1-3 methods), Writer (2-7 methods, only what `Run()` calls).
   - Check: read `types.go`, verify interface method counts

2. **Downloader struct has only interface fields**: No `*wb.Client`, no `*sqlite.*`, no `*postgres.*` in struct fields. [Rule 2, Rule 3]
   - Check: read `downloader.go` struct definition

3. **Compile-time assertions in both storage packages** [Pattern 1.2: dev_v2_postgres.md]:
   - SQLite: `var _ domain.Writer = (*SQLiteSalesRepository)(nil)` in `pkg/storage/sqlite/`
   - PG: `var _ domain.Writer = (*PgDomainRepo)(nil)` in `pkg/storage/postgres/`
   - Check: `grep "var _" pkg/storage/sqlite/<domain>* pkg/storage/postgres/<domain>*`

4. **No `fmt.Printf` / `log.*` in `pkg/<domain>/`**: Only `OnProgress func(msg string)` callback [Rule 4]
   - Check: `grep -r "fmt\.Printf\|log\." pkg/<domain>/` → must be empty
   - See: [examples/anti_patterns.md MG-4](examples/anti_patterns.md)

5. **`main.go` is thin driver (~100-160 lines)**: Only wiring: flags, config, DI, Ctrl+C [Rule 1]
   - Check: count lines in main.go excluding comments/imports

6. **YAGNI: no cursor/resume for light domains** [Pattern 1.8: dev_v2_postgres.md]:
   - Light (<100k rows, <10 min): no resume methods in Writer
   - Heavy (millions, hours): resume justified (sales, funnel)
   - Check: compare Writer methods against domain size

7. **Reader interface only when cross-domain deps exist**: e.g., nmIDs for search-vis. Reader reads from SAME backend as Writer.
   - Check: determine if API calls require data from other tables

8. **No direct `client.Post()`/`client.Get()` from domain package** [Rule 11: dev_v2_downloader.md]:
   - WBSource delegates to typed client methods in `pkg/wb/`
   - Check: `grep -r "client\.Post\|client\.Get\b" pkg/<domain>/` → must be empty
   - See: [examples/anti_patterns.md](examples/anti_patterns.md) AP-9

### V2-2. Configuration [Rules 6, 10: dev_v2_downloader.md] [Pattern 1.6: dev_v2_postgres.md]

1-6. **Same as V1 Configuration items 1-6.**

7. **`V2StorageConfig` used for backend selection**: `storage:` section with `backend`, `db_path`, `pg_database`, `pg_password_env` [AP-12: don't use `StorageConfig`]
   - Check: grep for `V2StorageConfig` in main.go and Config struct

8. **Backend switch in CLI**: `createBackend()` or `createWriter()` with `switch cfg.Backend` [Pattern 1.3]
   - Check: find the factory function, verify both "postgres" and default "sqlite" branches

9. **`GetEffectiveDSN()` used for PG connection**: Not manual DSN construction.
   - Check: grep for `GetEffectiveDSN` in main.go

### V2-3. Mock Safety (CRITICAL) [Pattern 1.8: dev_v2_postgres.md]

1. **`DiscardWriter` exists in `pkg/<domain>/mock.go`**: Implements Writer with no-op methods, thread-safe (`sync.Mutex`) counters.
   - Check: read mock.go, verify `sync.Mutex` on counters

2. **Mock mode creates DiscardWriter, NOT real DB writer**: Writer creation is INSIDE `else` branch. [CRITICAL — incident: 30,900 records deleted]
   - Check: read `if *mockMode` block in main.go — writer must NOT be created before this check
   - See: [examples/anti_patterns.md MG-2](examples/anti_patterns.md)

3. **MockReader (if Reader exists)**: Returns synthetic data, NO database interaction.
   - Check: read mock.go for `MockReader` type

4. **`--mock` never opens ANY database**: Not for Writer, not for Reader.
   - Check: trace code path when `*mockMode == true`, verify no `sqlite.New*` or `postgres.NewPool` calls

### V2-4. Rate Limiting [Rule 7: dev_v2_downloader.md]

1-5. **Same as V1 Rate Limiting items 1-5.**

6. **`ShareRateLimit` for endpoints sharing a global limit**: e.g., search_report and search_texts share 3 req/min.
   - Check: grep for `ShareRateLimit` in main.go

7. **WBSource receives rate limit values**: `NewWBSource(client, rateLimit, burst)`, not `NewWBSource(client)` without limits.
   - Check: read WBSource constructor call in main.go

8. **`SetRateLimit()` called explicitly after `wb.New()`**: `wb.New()` does NOT configure rate limiting.
   - Check: find `wb.New(` call, verify `SetRateLimit` follows

### V2-5. Database Operations — Dual Backend [dev_v2_postgres.md]

1-5. **Same as V1 Database Operations items 1-5.**

6. **PG schema in separate `_schema.go` file**: `pkg/storage/postgres/<domain>_schema.go`.
   - Check: file exists

7. **PG uses `$1,$2...` placeholders, not `?`** [CRITICAL — runtime error, mock doesn't catch]
   - Check: `grep '\?' pkg/storage/postgres/<domain>_repo.go` in SQL strings → must be empty
   - See: [examples/anti_patterns.md MG-5](examples/anti_patterns.md)

8. **PG uses `ON CONFLICT ... DO UPDATE SET ... = EXCLUDED`**: Not `INSERT OR REPLACE` (SQLite syntax).
   - Check: grep for `INSERT OR REPLACE` in postgres repo → must be empty

9. **PG uses `BOOLEAN` not `INTEGER` for bool fields** [AP-16]
   - Check: read schema, find boolean columns, verify type is `BOOLEAN DEFAULT FALSE`

10. **PG uses `DOUBLE PRECISION` not `REAL` for float64 fields.**
    - Check: read schema for float columns

11. **PG uses `BIGSERIAL PRIMARY KEY` for auto-increment PKs.**
    - Check: read schema for `id` column

12. **PG expression indexes use double parentheses**: `ON t((expr))` not `ON t(expr)` [AP-17].
    - Check: grep for `CREATE INDEX.*ON.*CASE` in schema file

13. **PG aggregate functions scan into pointer types**: `MAX()`, `MIN()` → scan into `*string`, `*int` [AP-18].
    - Check: find `SELECT MAX|MIN|SUM` in postgres repo, verify Scan targets

14. **PG chunking: 500 records per transaction**: Not single massive INSERT.
    - Check: read Save method for batch/chunk logic

15. **Context propagation in both backends**: `ExecContext`/`QueryContext` in both SQLite and PG.
    - Check: grep for `.Exec(` without `Context` in both adapter files

16. **SQLite compile-time assertion present**.
    - Check: `grep "var _" pkg/storage/sqlite/<domain>_repo.go`

17. **PG compile-time assertion present**.
    - Check: `grep "var _" pkg/storage/postgres/<domain>_repo.go`

### V2-6. Error Handling [dev_best_practices.md]

1-8. **Same as V1 Error Handling items 1-8.**

9. **Error wrapping in both `pkg/<domain>/` and storage adapters**: `fmt.Errorf("operation: %w", err)`.
   - Check: scan for bare `return err` without wrapping context

10. **PG `pool.Close()` in deferred cleanup**: Connection pool properly released.
    - Check: find cleanup function, verify `pool.Close` is included

### V2-7. CLI Interface [Rule 1: dev_v2_downloader.md]

1-4. **Same as V1 CLI Interface items 1-4.**

5. **Backend flags**: `--backend`, `--db`, `--pg-database` for V2 backend selection.
   - Check: read flag definitions

6. **`dllog` used for all output**: `PrintHeader`, `Progress`, `Done`, `Log` — one line per page [AP-11].
   - Check: `grep "fmt\.Printf\|log\.Printf" main.go` → only `log.Fatalf` for fatal errors

7. **`signal.NotifyContext` for Ctrl+C**: Not manual signal handling.
   - Check: grep for `signal.NotifyContext`

8. **`resolveAPIKey` returns VALUE, not env var name** [AP-10]: Uses `os.Getenv()`.
   - Check: read `resolveAPIKey` function

### V2-8. Testing [Rule 8: dev_v2_downloader.md]

1-3. **Same as V1 Testing items 1-3.**

4. **Minimum 4 test cases in `pkg/<domain>/downloader_test.go`**: Basic download, DryRun, Rewrite/Limit, Context cancellation.
   - Check: count test functions

5. **Tests use MockSource + DiscardWriter only**: No real DB, no real API.
   - Check: grep test files for `sqlite.New|postgres.NewPool` → must be empty

6. **Context cancellation test uses `errors.Is()`**: Not direct comparison.
   - Check: grep for `errors.Is` in test file

7. **`DiscardWriter.Saved()` assertions in tests**: Tests verify row counts via counters.
   - Check: read test assertions for `.Saved()` calls

### V2-9. Code Reuse / Duplication [Rule 0: dev_best_practices.md] [dev_swagger_reusable_packages.md]

1. **Config types reused from `pkg/config/`**: Not local Config structs in `cmd/` duplicating shared types.
   - Check: compare Config struct in main.go against types in `pkg/config/utility.go`

2. **Storage operations in storage packages**: Not ad-hoc SQL in `cmd/`.
   - Check: verify SQL operations are in `pkg/storage/sqlite/` and `pkg/storage/postgres/`

3. **Typed WB client methods used**: Not raw HTTP (`http.NewRequest`, `http.DefaultClient`).
   - Check: `grep -r "http\.DefaultClient\|http\.NewRequest" pkg/<domain>/ cmd/<name>/` → must be empty

4. **No duplicate constants between V1 and V2**: If V2 replaces V1, constants in `pkg/<domain>/` or `pkg/wb/`, not duplicated.
   - Check: compare constants between V1 cmd/ and V2 pkg/

### V2-10. Hardcode Audit [Rule 10: dev_v2_downloader.md]

Detect hardcoded values that should be constants, config fields, or named variables.
Full grep patterns and WRONG/RIGHT examples: [examples/anti_patterns.md HC-1..HC-8](examples/anti_patterns.md)

1. **API URLs are constants, not string literals** [HC-1]: In `pkg/wb/` or `pkg/<domain>/`.
   - Check: `grep -rn "wildberries\.ru\|https://.*api.*wb" pkg/<domain>/ cmd/<name>/`

2. **Rate limit values from config, not hardcoded** [HC-2]: Not magic numbers in `SetRateLimit` calls.
   - Check: `grep -n "SetRateLimit.*[0-9].*[0-9]" cmd/<name>/main.go`

3. **Database paths from config, not hardcoded** [HC-3]: Not `/var/db/wb-sales.db` in code.
   - Check: grep for `/var/db` in main.go → should be in config.yaml only

4. **Timeouts configurable** [HC-4]: Not `time.Sleep(5 * time.Second)` or hardcoded HTTP timeouts.
   - Check: `grep -rn "time\.Sleep\|time\.Second\|time\.Minute" pkg/<domain>/ cmd/<name>/main.go`

5. **Table names consistent** [HC-6]: SQL table names same between schema and queries.
   - Check: `grep -oh "INTO [a-z_]*" pkg/storage/sqlite/<domain>* pkg/storage/postgres/<domain>* | sort | uniq -c`

6. **Error messages include context**: Not bare `return nil, err` without operation description.
   - Check: scan for `return nil, err` and `return 0, err` without `fmt.Errorf`

7. **Batch/chunk sizes are named constants** [HC-5]: Not `i += 500` without explanation.
   - Check: `grep -rn "i += [0-9]\|chunk.*[0-9]\|batch.*[0-9]" pkg/<domain>/`

8. **Date formats not repeated** [HC-7]: `"2006-01-02"` more than twice → extract constant (or use `time.DateOnly`).
   - Check: `grep -rn '"2006-01-02"' pkg/<domain>/ cmd/<name>/`

9. **Default days from config or named constant** [HC-8]: Not `days = 7` without explanation.
   - Check: `grep -rn "days.*=.*[0-9]\|Days.*=.*[0-9]" cmd/<name>/main.go`

### V2-11. YAML Config Completeness [Pattern 1.7: dev_v2_postgres.md]

1. **Every parameter has a comment explaining its purpose**: Including type hints and valid ranges.
   - Check: read config.yaml, verify every key has a `#` comment
   - See: [examples/config_complete.yaml](examples/config_complete.yaml) for gold standard

2. **Config struct matches YAML 1:1**: No dead YAML fields [AP-13], no missing YAML keys.
   - Check: compare YAML keys against Go struct `yaml:"..."` tags field-by-field

3. **Domain-specific section with rate limits and API key env var**:
   - Check: verify `<domain>:` section exists with `rate_limits` and `api_key_env`

4. **Storage section with V2StorageConfig fields**: `backend`, `db_path`, `pg_database`, `pg_password_env`.
   - Check: verify `storage:` section present with all fields

5. **Rate limits have comments citing Swagger rates**: e.g., `# swagger: 3 req/min shared`.
   - Check: read rate_limits comments

6. **Example values show typical production settings**: Not empty strings for required fields.
   - Check: verify `api_key_env`, `days`, `limit` have sensible example values

7. **Config file exists in `cmd/.configs/download-all/`**:
   - Check: `ls cmd/.configs/download-all/download-<domain>*.yaml`

8. **Config key matches CLI struct domain name**: YAML key matches struct field name.
   - Check: compare YAML top-level key against Go struct tag

---

## Anti-Pattern Catalog

18 unified anti-patterns. Each entry: Severity, Symptom, Search pattern.
Full WRONG/RIGHT code examples: [examples/anti_patterns.md](examples/anti_patterns.md).

| AP# | Name | Severity | Source | Grep Pattern |
|-----|------|----------|--------|-------------|
| AP-1 | SQL Placeholder Mismatch | CRITICAL | existing | Count `?` or `$N` in VALUES vs column count |
| AP-2 | io.Reader Reuse on Retry | CRITICAL | existing | Custom HTTP code storing `io.Reader` without raw bytes |
| AP-3 | Date Off-By-One | WARNING | existing | `time.Now().Format` as endDate without `AddDate(0,0,-1)` |
| AP-4 | Missing Error Continuation | WARNING | existing | `if err != nil { return ... }` inside batch loop |
| AP-5 | DELETE ALL Inside Batch | CRITICAL | existing | `DELETE FROM` without WHERE in save functions |
| AP-6 | JSON Tags vs Swagger | CRITICAL | existing | Compare `json:"..."` tags against Swagger schema |
| AP-7 | Shared Rate Limits | WARNING | existing | Multiple endpoints, one rate config field |
| AP-8 | Database Grain Mismatch | CRITICAL | existing | UNIQUE constraint missing varying fields |
| AP-9 | doRequest() Bypass → 429 | CRITICAL | v2_downloader | `httpClient.Do()` without timing guardrails |
| AP-10 | resolveAPIKey Returns Name | CRITICAL | v2_downloader | `return cfg.APIKeyEnv` without `os.Getenv()` |
| AP-11 | Two Lines Per Page | INFO | v2_downloader | `fmt.Printf` + second `fmt.Printf` per page |
| AP-12 | Type Name Collision | WARNING | v2_downloader | `StorageConfig` instead of `V2StorageConfig` |
| AP-13 | Dead YAML Field | CRITICAL | v2_downloader | YAML key with no matching struct field |
| AP-14 | God-Object Repository | WARNING | v2_postgres | One repo with 30+ methods across domains |
| AP-15 | pgx Indirect Dependency | WARNING | v2_postgres | `go get` without import code |
| AP-16 | Hardcoded Boolean | WARNING | v2_postgres | `INTEGER DEFAULT 0` for boolean in PG schema |
| AP-17 | Expression Index Multi-Stmt | CRITICAL | v2_postgres | `CREATE INDEX ... CASE` inside multi-statement Exec |
| AP-18 | Aggregate in Value Type | CRITICAL | v2_postgres | `MAX()/MIN()/SUM()` scan into non-pointer type |

**V1→V2 Migration Gotchas** (7 items based on real incidents): [examples/anti_patterns.md MG-1..MG-7](examples/anti_patterns.md)

---

## Shell Scripts Audit [dev_v2_downloader.md]

For `download-all-v2.sh` and related scripts.

1. **Phase ordering matches doc requirements**: Catalog (fast) → Sales (core) → Stock → Advertising → Analytics (slow, 3 req/min).
   - Check: read script phases, verify ordering

2. **Each phase uses correct config file**: Config path matches actual file in `cmd/.configs/download-all/`.
   - Check: compare script `--config` paths against `ls cmd/.configs/download-all/`

3. **Error handling**: `|| exit $?` on each downloader invocation.
   - Check: verify each `(cd ... && go run . ...) || exit $?` line

4. **Lockfile prevents concurrent runs**: `flock` with timeout.
   - Check: verify lockfile pattern at script start

5. **V1 vs V2 classification in comments matches reality**.
   - Check: compare summary comment against actual V2/V1 classification

6. **Commented-out lines are intentional**: Not accidental omissions.
   - Check: verify each commented line has a reason

7. **DAYS parameter propagation**: `${DAYS:+--days=$DAYS}` for date-range downloaders, omitted for snapshot downloaders.
   - Check: verify DAYS is passed to downloaders that support `--days`

8. **Backend flag consistency**: All V2 downloaders use `--backend sqlite` explicitly.
   - Check: verify each V2 invocation includes backend flag

---

## Go Best Practices Audit [dev_best_practices.md] [dev_solid.md]

Applied during Phase 3 for both V1 and V2.

1. **SRP: Methods ≤ 30 lines, functions ≤ 50 lines** [dev_solid.md].
   - Check: scan longest functions in `pkg/<domain>/` and main.go

2. **ISP: Interfaces have 1-7 methods** [dev_solid.md]: Source (1-3), Writer (2-7). If >7, flag as god-interface.
   - Check: count interface methods in types.go

3. **DIP: Dependencies injected via interfaces**: Not created inside constructors via `NewX()`.
   - Check: read `NewDownloader()` — parameters should be interfaces

4. **Error wrapping with context**: `fmt.Errorf("op: %w", err)`, not bare `return err`.
   - Check: scan for `return.*err\b` patterns without `fmt.Errorf`

5. **No `panic()` in business logic**: Only `log.Fatalf` in `cmd/` main().
   - Check: `grep -r "panic(" pkg/<domain>/` → must be empty

6. **`context.Context` propagated everywhere**: All public methods accept ctx.
   - Check: scan method signatures in types.go and repo files

7. **Thread-safe by default**: Shared state uses `sync.RWMutex` (DiscardWriter, MockSource).
   - Check: verify `sync.Mutex` or `sync.RWMutex` in mock.go

8. **Package structure: no `internal/` imports from `pkg/`**: `pkg/` is pure library.
   - Check: `grep -r "internal/" pkg/<domain>/` → must be empty

---

## Report Template

See [examples/report_example.md](examples/report_example.md) for a complete sample report.

```
## Downloader Audit: <name>

**Date**: <today>
**Classification**: V1 | V2 (full) | V2 (partial)
**Domain**: <domain>
**Files reviewed**: <list of all files read>
**Compared against**: <gold standard reference>
**Backend status**: SQLite only | SQLite + PostgreSQL (dual-backend)
**pkg/<domain>/ exists**: YES (Source: N, Writer: N, Reader: N) | NO

### Summary
<1-2 paragraphs. Production-ready? Biggest risk? Dual-backend parity?>

### Findings
| # | Category | Severity | Backend | Finding | Location | Fix |
|---|----------|----------|---------|---------|----------|-----|
| 1 | ... | ... | SQLite/PG/Both/N/A | ... | ... | ... |

### Anti-Patterns Detected
| AP# | Name | Severity | Status | Evidence |
|-----|------|----------|--------|----------|
| AP-N | ... | ... | FOUND/NOT FOUND | ... |

### Statistics
- CRITICAL: X  WARNING: Y  INFO: Z  GOOD: W

### V2 Architecture Score (V2 only)
| Criterion | Status |
|-----------|--------|
| pkg/<domain>/ with Source/Writer | YES/NO |
| Compile-time assertions (SQLite + PG) | YES/NO/PARTIAL |
| DiscardWriter mock safety | YES/NO |
| Dual-backend parity (schema + upsert) | YES/NO |
| OnProgress callback (no fmt.Printf) | YES/NO |
| main.go < 200 lines | YES/NO |
| README.md present | YES/NO |
| config.yaml fully commented | YES/NO/PARTIAL |
| Minimum 4 tests | YES/NO |
| download-all-v2.sh integration | YES/NO |

### Recommendations
1. <Most impactful fix first>
2. ...

For each CRITICAL finding, include a ready-to-use Go code snippet showing the fix.
```

---

## Severity Definitions

- **CRITICAL**: Data loss, silent corruption, or production failure. Fix before next run.
- **WARNING**: Suboptimal pattern, potential future issue, or missing resilience. Fix soon.
- **INFO**: Style inconsistency, deviation from gold standard, or minor improvement opportunity.
- **GOOD**: Correct implementation matching best practices. Acknowledge these too.
