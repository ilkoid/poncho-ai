# dev_v2_postgres.md — V2 PostgreSQL Migration Guide (Dual-Backend)

**Дата:** 2026-06-03
**Статус:** Актуальный
**Приоритет:** Доменный — побеждает `dev_v2_downloader.md` при конфликте по вопросам PostgreSQL

**Связанные документы:**
- [dev_v2_downloader.md](dev_v2_downloader.md) — **обязательный prerequisite**: v2 архитектура (Source/Writer), mock/dry-run, общие антипаттерны
- [dev_manifest.md](dev_manifest.md) — Port & Adapter (Rule 6), pkg/ vs cmd/
- [dev_best_practices.md](dev_best_practices.md) — общие паттерны, Rule 0: Code Reuse First

---

## Принцип

Практическое руководство миграции v1→v2 с dual-backend (SQLite + PostgreSQL) через Writer-интерфейсы.

**Ключевая идея:** каждый домен получает свой фокусированный Writer-интерфейс (ISP), а CLI выбирает бэкенд через конфиг. PostgreSQL растёт по одному домену, без god-object.

---

## Architecture Overview

```
                        pkg/<domain>/ (business logic)
                    ┌─── types.go ──────────────────┐
                    │ Source interface (1-3 methods) │
                    │ Writer interface (2-7 methods) │
                    │ Downloader + Run()             │
                    └──────┬───────────┬─────────────┘
                           │           │
              ┌────────────┘           └────────────┐
              │                                     │
   pkg/storage/sqlite/               pkg/storage/postgres/
   SQLiteSalesRepository             Pg<Domain>Repo
   (compile-time assertion)          (compile-time assertion)
              │                                     │
              └──────────┬──────────────┘
                         │
              cmd/.../download-<domain>-v2/
              flags → config → select backend → DI → Run
```

---

## 1. Паттерны

### 1.1. ISP: Фокусированные Writer-интерфейсы

**Каждый домен — свой Writer** с минимальной поверхностью.

```go
// cards.CardsWriter — 2 метода
type CardsWriter interface {
    SaveCards(ctx context.Context, cards []wb.ProductCard) (int, error)
    CountCards(ctx context.Context) (int, error)
}
```

**Правило:** Writer содержит **только** методы, реально вызываемые в `Downloader.Run()`. Не god-object с 30 методами.

### 1.2. Compile-time Assertions

```go
// pkg/storage/sqlite/cards_repo.go
var _ cards.CardsWriter = (*SQLiteSalesRepository)(nil)

// pkg/storage/postgres/cards_repo.go
var _ cards.CardsWriter = (*PgCardsRepo)(nil)
```

Если интерфейс меняется — компилятор сразу подсветит все адаптеры.

### 1.3. V2StorageConfig для выбора бэкенда

```go
// pkg/config/pgconfig.go
type V2StorageConfig struct {
    Backend       string `yaml:"backend"`          // "sqlite" | "postgres"
    DbPath        string `yaml:"db_path"`          // SQLite путь
    PgDSN         string `yaml:"pg_dsn"`           // Полный DSN (опционально)
    PgDatabase    string `yaml:"pg_database"`      // Имя базы
    PgPasswordEnv string `yaml:"pg_password_env"`  // Env var с паролем
}
```

CLI создаёт конкретный Writer через switch:
```go
func createCardsWriter(ctx, cfg config.V2StorageConfig) (cards.CardsWriter, func(), error) {
    switch cfg.Backend {
    case "postgres":
        dsn, _ := cfg.GetEffectiveDSN()
        pool, _ := postgres.NewPool(ctx, dsn)
        repo := postgres.NewPgCardsRepo(pool.DB())
        repo.InitSchema(ctx)
        return repo, pool.Close, nil
    default: // "sqlite"
        repo, _ := sqlite.NewSQLiteSalesRepository(cfg.DbPath)
        return repo, func() { repo.Close() }, nil
    }
}
```

**Почему не BackendFactory:** factory — god-interface, нарушающий ISP. CLI знает какой Writer нужен — пусть сам создаёт.

**DSN construction** (`pgconfig.go: GetEffectiveDSN()`):
```
pg_dsn non-empty → used as-is
pg_dsn empty     → BuildPgDSN() from: host (PGHOST || "192.168.10.7"), port (PGPORT || "15432"),
                    user ("postgres"), password (os.Getenv(pg_password_env)), database (pg_database)
```

### 1.4. pgxpool — Native PostgreSQL Driver

```go
import "github.com/jackc/pgx/v5/pgxpool"
pool, err := pgxpool.New(ctx, dsn)  // connection pool из коробки
```

**Не** `database/sql` + `lib/pq` — pgx даёт native pooling, `pgx.ErrNoRows`, binary protocol.

**⚠️ Dual-backend error handling:** `sql.ErrNoRows` (SQLite) и `pgx.ErrNoRows` (PostgreSQL) — разные типы. Используйте `errors.Is()`. Для текущих Writer-интерфейсов (Save/Count) не актуально — они не делают single-row lookups.

### 1.5. dllog для единого вывода

```go
dllog.PrintHeader("WB Cards Downloader v2",
    dllog.HeaderField{Key: "Backend", Value: cfg.Storage.Backend})

dllog.Progress(page, 0, "cards", fmt.Sprintf("%d cards saved (%s)", n, elapsed), start)

dllog.Done(result.Duration, "%d cards, %d pages", result.TotalCards, result.Pages)
```

**Правило:** в `pkg/<domain>/` — ни одного `fmt.Printf`. Одна строка на страницу через `dllog.Progress()`.

### 1.6. Config Reuse

Переиспользуем типы из `pkg/config/`:

```go
type Config struct {
    WB      config.WBClientConfig     `yaml:"wb"`
    Cards   cardsConfig               `yaml:"cards"`
    Storage config.V2StorageConfig    `yaml:"storage"`
    Filter  config.FunnelFilterConfig `yaml:"filter"`
}
```

### 1.7. Config YAML: Полный и Прокомментированный

Каждый v2 загрузчик обязан иметь `config.yaml` с комментариями на каждый параметр. Минимальная структура:

```yaml
# ============================================================================
# WB API клиент
# ============================================================================
wb:
  api_key: ""                  # Прямой API ключ (лучше через env)
  timeout: "30s"

# ============================================================================
# <Домен> — доменная конфигурация
# ============================================================================
<domain>:
  api_key_env: "WB_API_KEY"   # Env var с API ключом
  rate_limit: 100              # Desired: req/min
  burst_limit: 5               # Desired: burst
  api_rate_limit: 100          # API floor: req/min (swagger)
  api_burst_limit: 5           # API floor: burst (swagger)

# ============================================================================
# Хранение — выбор бэкенда
# ============================================================================
storage:
  backend: "postgres"          # "sqlite" (default) | "postgres"
  db_path: "/var/db/wb-sales.db"
  pg_database: "wb_data_prod"
  pg_password_env: "PG_PWD"
```

### 1.8. YAGNI + DiscardWriter (Mock Safety)

Два правила, объединённые потому что наш опыт показал: mock safety без YAGNI приводит к потере данных.

**YAGNI — не добавлять Resume без необходимости:**
- **Cards, stocks, prices** (~30k записей, 3-5 мин): полная перезагрузка. ON CONFLICT upsert безопасен.
- **Sales** (миллионы записей, часы): resume оправдан. Но это решение домена `pkg/sales/`.
- Writer interface без cursor-методов = проще.

**Наш опыт:** cursor persistence для cards (~30k) добавил ~100 строк, JSON-сериализацию курсора, 2 лишних метода — и привёл к потере 391 карточки при `--resume` (32,044 в PG vs 32,435 в SQLite).

**DiscardWriter — `--mock` НИКОГДА не пишет в БД:**

```go
// pkg/<domain>/mock.go
type DiscardWriter struct {
    mu    sync.Mutex
    saved int
}
func (w *DiscardWriter) SaveCards(_ context.Context, cards []wb.ProductCard) (int, error) {
    w.mu.Lock()
    w.saved += len(cards)
    w.mu.Unlock()
    return len(cards), nil
}
```

**CLI wiring — mock НЕ создаёт DB writer:**
```go
if *mockMode {
    writer = &domain.DiscardWriter{}
    cleanup = func() {}
} else {
    writer, cleanup, err = createWriter(ctx, cfg.Storage)
}
```

**Наш опыт:** `--mock` + `rewrite: true` → загрузчик удалил ~30,900 реальных записей и вставил 250 фейковых. Root cause: writer создавался unconditional, mock подменял только Source.

### 1.9. Mandatory README

Каждый v2 загрузчик обязан иметь `README.md`:

```markdown
# WB <Domain> Downloader v2

## Usage
go run . --mock                                    # mock mode, no DB
go run . --mock --db /tmp/test.db                  # mock + test SQLite
go run . --dry-run --db /tmp/test.db --config ...  # real API, no writes
go run . --config config.yaml                      # production (user only!)
```

---

## 2. Антипаттерны (PG-специфичные)

Общие антипаттерны (placeholder mismatch, date off-by-one, doRequest bypass и др.) — в [dev_v2_downloader.md](dev_v2_downloader.md).

### 2.1. ❌ God-Object Repository

```go
// ❌ Один репозиторий на все домены — при добавлении PG нужно реализовать 30 методов сразу
type StorageBackend struct {
    SaveCards(...) SaveSales(...) SaveStocks(...) // ... 30 методов
}
```

**✅ Вместо:** фокусированные интерфейсы по домену — `cards.CardsWriter` (2 метода), `sales.SalesWriter` (7 методов). Каждый адаптер реализует только нужные.

### 2.2. ❌ pgx как Indirect Dependency

```bash
# ❌ go get без использования в коде — pgx остаётся indirect, компиляция ломается
go get github.com/jackc/pgx/v5

# ✅ Написать код с import, потом go mod tidy — промоутит pgx в direct
```

### 2.3. ❌ Hardcoded Boolean Representations

```go
// ❌ boolToInt() — только для SQLite (INTEGER)
needKiz := boolToInt(card.NeedKiz)

// ✅ PostgreSQL native BOOLEAN
_, err := tx.Exec(ctx, insertSQL, card.NeedKiz)  // bool → PG BOOLEAN
```

**Наш опыт:** diff на 1000 строк показал "все разные" — оказалось `true/false` vs `1/0`. Не баг данных, но сбивает при сравнении.

### 2.4. ❌ Expression/Partial Indexes в Multi-Statement Exec

```go
// ❌ pgx.Exec() не парсит CASE/WHERE в multi-statement блоке
const schemaSQL = `
CREATE TABLE ... ;
CREATE INDEX idx ON t(CASE ... END);  -- syntax error at "CASE"
CREATE INDEX idx2 ON t(col) WHERE col IS NOT NULL;
`

// ✅ Expression и partial indexes — отдельные Exec() вызовы
const simpleSchemaSQL = `CREATE TABLE ... ; CREATE INDEX idx_simple ON t(col);`
const exprIndexSQL = `CREATE INDEX idx_expr ON t((CASE ... END))`  // двойные скобки!
const partialIndexSQL = `CREATE INDEX idx_part ON t(col) WHERE col IS NOT NULL`

pool.Exec(ctx, simpleSchemaSQL)
pool.Exec(ctx, exprIndexSQL)
pool.Exec(ctx, partialIndexSQL)
```

**Важно:** expression indexes требуют **двойные скобки** — `ON t((CASE...END))`. Внешние `()` — синтаксис CREATE INDEX, внутренние — группировка выражения. SQLite принимает оба варианта, PG — только двойные.

### 2.5. ❌ Сканирование Aggregate Functions в Value Types

```go
// ❌ MAX() на пустой таблице возвращает NULL → scan в string = panic
var lastDT string
pool.QueryRow(ctx, "SELECT MAX(rr_dt) FROM sales").Scan(&lastDT)

// ✅ Scan в *string, nil при пустой таблице
var lastDT *string
pool.QueryRow(ctx, "SELECT MAX(rr_dt) FROM sales").Scan(&lastDT)
if lastDT == nil {
    return time.Time{}, nil  // пустая таблица
}
t, _ := time.Parse(time.RFC3339, *lastDT)
```

**Общее правило:** `SELECT MAX(...)` / `MIN(...)` / `SUM(...)` — scan в pointer type (`*string`, `*int`). Aggregate function возвращает NULL на пустой таблице, не `ErrNoRows`.

---

## 3. PostgreSQL: SQL Translation Cheat Sheet

| SQLite | PostgreSQL | Примечание |
|--------|-----------|------------|
| `?` placeholders | `$1, $2, $3...` | Позиционные, начинаются с 1 |
| `INSERT OR REPLACE INTO ...` | `INSERT INTO ... ON CONFLICT (...) DO UPDATE SET ... = EXCLUDED ...` | UPSERT |
| `INTEGER PRIMARY KEY AUTOINCREMENT` | `BIGSERIAL PRIMARY KEY` | Auto-increment |
| `INTEGER PRIMARY KEY` (natural key) | `INTEGER PRIMARY KEY` | Без изменений |
| `REAL` | `DOUBLE PRECISION` | 64-bit float |
| `INTEGER` для boolean | `BOOLEAN` | Native |
| `CURRENT_TIMESTAMP` | `TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')` | TEXT-compatible |
| `sql.NullFloat64{Float64: v, Valid: true}` | `*float64` (nil = NULL) | pgx native pointer handling |
| `sql.NullString{String: s, Valid: true}` | `*string` (nil = NULL) | pgx native pointer handling |
| `result.RowsAffected()` (database/sql) | `tag.RowsAffected()` (pgconn.CommandTag) | Delete/update count |
| `PRAGMA synchronous = OFF` | Не нужно | MVCC |
| `INSERT OR REPLACE INTO meta ...` | `INSERT INTO meta ... ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value` | Key-value upsert |
| `CREATE INDEX ... ON t(expr)` | `CREATE INDEX ... ON t((expr))` | Expression indexes: **double parentheses** |
| `CREATE INDEX ... WHERE col > 0` | `CREATE INDEX ... WHERE col IS NOT NULL` | Partial indexes (NULL-safe) |

### Важные нюансы PG-схемы:

1. **Foreign Keys с ON DELETE CASCADE** — при DELETE карточки автоматически чистятся children
2. **UNIQUE constraints** — для дочерних таблиц: `UNIQUE(nm_id, big)` для photos, `UNIQUE(nm_id, char_id)` для characteristics
3. **Indexes** — `CREATE INDEX IF NOT EXISTS` — синтаксис идентичен SQLite
4. **TEXT DEFAULT** — PostgreSQL требует `DEFAULT ''::text` или `NOT NULL DEFAULT ''`
5. **BOOLEAN DEFAULT** — `DEFAULT FALSE`, не `DEFAULT 0`

---

## 4. File Layout: New Domain Migration

```
pkg/<domain>/                                    # NEW: domain package
├── types.go                                     # Source, Writer, Options, Result
├── source.go                                    # WBSource adapter (если нужен)
├── downloader.go                                # Downloader + Run()
├── mock.go                                      # MockSource + DiscardWriter
└── downloader_test.go                           # ≥4 тестов (in-memory mock writers)

pkg/storage/postgres/                            # ADD: PG adapter
├── pool.go                                      # EXISTS: shared pool wrapper
├── <domain>_schema.go                           # NEW: PG DDL
└── <domain>_repo.go                             # NEW: Pg<Domain>Repo

pkg/storage/sqlite/                              # MODIFY: add assertion
└── <domain>_repo.go                             # ADD: var _ <domain>.Writer = (*SQLiteSalesRepository)(nil)

cmd/data-downloaders/download-<domain>-v2/       # NEW: CLI driver
├── main.go                                      # ~120-130 строк
├── config.yaml                                  # V2StorageConfig + domain settings + filter
└── README.md                                    # MANDATORY: usage, schema, safe examples

pkg/config/pgconfig.go                           # EXISTS: V2StorageConfig (reuse as-is)
```

---

## 5. Checklist: миграция нового домена

### pkg/<domain>/ (ядро)
- [ ] `types.go` — Source (1-3 метода) + Writer (2-7 методов), Options, Result
- [ ] Writer содержит **только** методы, вызываемые в `Run()`
- [ ] Нет cursor/resume для лёгких доменов (<100k, <10 мин) — см. §1.8 YAGNI
- [ ] `mock.go` — MockSource + **DiscardWriter** — см. §1.8 Mock Safety
- [ ] Нет `fmt.Printf` — только `OnProgress` callback
- [ ] Нет `*sqlite.*` / `*wb.Client` в полях struct

### pkg/storage/postgres/ (PG adapter)
- [ ] `<domain>_schema.go` — DDL переведён (см. §3 SQL Cheat Sheet)
- [ ] `<domain>_repo.go` — Pg<Domain>Repo implements <domain>.Writer
- [ ] Compile-time assertion: `var _ <domain>.Writer = (*Pg<Domain>Repo)(nil)`
- [ ] `$1, $2...` placeholders, не `?`
- [ ] `ON CONFLICT ... DO UPDATE SET ... = EXCLUDED ...` для upsert
- [ ] `BOOLEAN` вместо `INTEGER` для bool полей (§2.3)
- [ ] `DOUBLE PRECISION` вместо `REAL`
- [ ] `BIGSERIAL PRIMARY KEY` для auto-increment PK
- [ ] Foreign Keys с `ON DELETE CASCADE`
- [ ] Expression indexes — отдельные `Exec()` + двойные скобки (§2.4)
- [ ] Aggregate results — scan в pointer types (§2.5)
- [ ] Чанкинг по 500 записей в транзакции

### pkg/storage/sqlite/ (адаптер)
- [ ] Compile-time assertion: `var _ <domain>.Writer = (*SQLiteSalesRepository)(nil)`

### cmd/.../download-<domain>-v2/ (драйвер)
- [ ] `main.go` ~120-130 строк: flags → config → backend switch → DI → Run
- [ ] `config.yaml` — полный, прокомментированный (§1.7)
- [ ] `README.md` — mandatory (§1.9)
- [ ] **Mock safety:** `--mock` = DiscardWriter, НЕ реальный DB writer (§1.8)
- [ ] Флаги: `--config`, `--db`, `--backend`, `--pg-database`, `--mock`, `--dry-run`, `--limit`
- [ ] Date-based домены: `--days`, `--begin`, `--end` + `resolveDateRange()`
- [ ] `SetRateLimit()` вызывается явно после `wb.New()`
- [ ] Если `*Page()` делает прямой `c.httpClient.Do()` — дублировать timing guardrails (см. [dev_v2_downloader.md](dev_v2_downloader.md) Anti-Pattern #9)
- [ ] `resolveAPIKey()` через `os.Getenv()` (см. [dev_v2_downloader.md](dev_v2_downloader.md) Anti-Pattern #10)
- [ ] `dllog.PrintHeader()` + `dllog.Progress()` + `dllog.Done()`
- [ ] Ctrl+C через `signal.NotifyContext()`
- [ ] **ВСЕГДА** `--db /tmp/test-<domain>.db` в тестовых командах

### Verification
**⚠️ Safety:** ALL test commands use `--db /tmp/test-<domain>.db` — НИКОГДА не `/var/db/`.

- [ ] `go build ./pkg/<domain>/` — компилируется
- [ ] `go test ./pkg/<domain>/ -v` — все PASS (in-memory mock)
- [ ] **Placeholder count check** (mock НЕ ловит!): `?` / `$N` count = колонки − SQL-функции
- [ ] `go run ./cmd/.../ --mock` — DiscardWriter, **no DB file created**
- [ ] `go run ./cmd/.../ --mock --db /tmp/test.db --backend=sqlite`
- [ ] `go run ./cmd/.../ --mock --backend=postgres --pg-database=wb_data_test`
- [ ] `go build ./cmd/.../download-<domain>/` — v1 не сломан
- [ ] Data parity: mock download → сравнить SQLite vs PostgreSQL

---

## 6. Quick Reference: Наши домены

| Домен | Writer методов | Resume? | Pagination | PG адаптер | Mock Safety |
|-------|---------------|---------|------------|------------|-------------|
| cards | 2 | ❌ Нет | Cursor (WB API) | ✅ | ⚠️ Legacy |
| sales | 7 | ✅ Да (date-level) | Date-range iterator | ✅ | ⚠️ Legacy |
| orders | 2 | ❌ Нет | lastChangeDate iterator | ✅ | ✅ DiscardWriter |
| prices | 2 | ❌ Нет | Offset (limit/offset) | ✅ | ✅ DiscardWriter |
| opsales | 2 | ❌ Нет | lastChangeDate iterator | ✅ | ✅ DiscardWriter |
| funnel | 5 | Partial (time-based) | Batch-by-nmIDs | ✅ | ✅ DiscardWriter |
| nmreport | 5 | ✅ Да (report-level) | Async CSV | — | — |
| stocks | — | — | — | — | — |
| feedbacks | — | — | — | — | — |
| region-sales | — | — | — | — | — |
| promotion | — | — | — | — | — |

**⚠️ Known tech debt:** cards и sales v2 используют legacy mock safety (открывают БД в `--mock`). Новые домены обязаны использовать DiscardWriter.

**Порядок миграции:** cards ✅ → orders ✅ → prices ✅ → opsales ✅ → sales ✅ → funnel ✅ → stocks → feedbacks → region-sales → promotion. Мигрируй когда нужен фикс или фича, не массово.

---

**Last Updated:** 2026-06-03
**Version:** 2.0 (restructured: moved general anti-patterns to dev_v2_downloader.md, merged §1.8/§2.2 and §1.9/§2.10, removed migration timeline)
