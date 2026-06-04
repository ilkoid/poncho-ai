---
name: downloader-audit
description: Audit the quality of WB API data downloaders. Use this skill whenever the user asks to audit a downloader, review downloader code, check downloader quality, verify a new downloader, or mentions downloaders in the context of quality, correctness, bugs, or best practices. Also trigger for rate limiting issues, JSON tag mismatches, SQL placeholder bugs, error handling gaps, mock safety, DiscardWriter, PG adapter correctness, dual-backend consistency, compile-time assertions, or anti-patterns. Covers 29 downloaders. V2 (dual-backend): cards-v2, prices-v2, orders-v2, opsales-v2, sales-v2, stocks-v2, funnel-v2, feedbacks-v2, campaigns-v2, region-sales-v2, search-vis-v2, funnel-csv-v2, promotion-v2. V1 (SQLite-only): cards, prices, feedbacks, funnel, funnel-agg, funnel-csv, stocks, stock-history, sales, region-sales, search-visibility, promotion, supplies, 1c-data, 1c-rests, all-articles.
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
   - `download-wb-promotion-v2` — V2 (full) with dual-backend since 2026-05 (has `V2StorageConfig`, `pkg/promotion/` with Source/Writer/Reader, PG adapter)
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
If V2 → run **V2 Full Checklist** in [docs/v2_full_checklist.md](docs/v2_full_checklist.md) (11 categories, self-contained).

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

*Full V2 checklist with all items (V1 inlined) → [docs/v2_full_checklist.md](docs/v2_full_checklist.md)*

*11 categories, 92 checks, self-contained. No cross-referencing needed during audit.*

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

2. **ISP: Interfaces have 1-7 methods** [dev_solid.md] (→ V2-1.1): Source (1-3), Writer (2-7). If >7, flag as god-interface.
   - Check: count interface methods in types.go

3. **DIP: Dependencies injected via interfaces**: Not created inside constructors via `NewX()`.
   - Check: read `NewDownloader()` — parameters should be interfaces

4. **Error wrapping with context** (→ V2-6.9): `fmt.Errorf("op: %w", err)`, not bare `return err`.
   - Check: scan for `return.*err\b` patterns without `fmt.Errorf`

5. **No `panic()` in business logic**: Only `log.Fatalf` in `cmd/` main().
   - Check: `grep -r "panic(" pkg/<domain>/` → must be empty

6. **`context.Context` propagated everywhere** (→ V2-5.15): All public methods accept ctx.
   - Check: scan method signatures in types.go and repo files

7. **Thread-safe by default** (→ V2-3.1): Shared state uses `sync.RWMutex` (DiscardWriter, MockSource).
   - Check: verify `sync.Mutex` or `sync.RWMutex` in mock.go

8. **Package structure: no `internal/` imports from `pkg/`**: `pkg/` is pure library.
   - Check: `grep -r "internal/" pkg/<domain>/` → must be empty

---

## Report Template

See [examples/report_example.md](examples/report_example.md) for a complete filled sample report.

### Required Sections

1. **Header**: Date, Classification, Domain, Files reviewed, Backend status, pkg/ status
2. **Summary**: 1-2 paragraphs. Production-ready? Biggest risk? Dual-backend parity?
3. **Findings table**: `| # | Category | Severity | Backend | Finding | Location | Fix |`
4. **Anti-Patterns table**: `| AP# | Name | Severity | Status | Evidence |`
5. **Statistics**: `CRITICAL: X  WARNING: Y  INFO: Z  GOOD: W`
6. **V2 Architecture Score** (V2 only): 10-row table — see report_example.md for columns
7. **Recommendations**: Ordered by impact. Each CRITICAL finding includes a ready-to-use Go code snippet.

Status markers: ✅ Compliant | ❌ Violation (include severity) | ⚠️ Partial | N/A

### Report Persistence

Save to `reports/YYYY-MM-DD_<downloader-name>.md` in the project root.

---

## Severity Definitions

- **CRITICAL**: Data loss, silent corruption, or production failure. Fix before next run.
- **WARNING**: Suboptimal pattern, potential future issue, or missing resilience. Fix soon.
- **INFO**: Style inconsistency, deviation from gold standard, or minor improvement opportunity.
- **GOOD**: Correct implementation matching best practices. Acknowledge these too.
