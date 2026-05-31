# dev_v2_postgres.md — V2 Downloader Migration Guide (Dual-Backend: SQLite + PostgreSQL)

**Дата**: 2026-05-31
**Статус**: Актуальный (на основе опыта cards v2 migration)
**Приоритет**: Доменный — побеждает `dev_utils.md` при конфликте по вопросам dual-backend и PostgreSQL

**Связанные документы:**
- [dev_utils.md](dev_utils.md) — базовые правила v2 (Source/Writer, pkg/ core, mock/dry-run)
- [dev_manifest.md](dev_manifest.md) — Port & Adapter (Rule 6), pkg/ vs cmd/
- [dev_best_practices.md](dev_best_practices.md) — общие паттерны, Rule 0: Code Reuse First
- [CLAUDE.md](CLAUDE.md) — WB API, команды, архитектура

---

## Принцип

Этот документ — **практическое руководство** миграции v1→v2 с dual-backend (SQLite + PostgreSQL) через Writer-интерфейсы. Написан на основе реального опыта миграции cards v2, включая все обнаруженные баги и антипаттерны.

**Ключевая идея:** каждый домен (cards, sales, stocks, funnel…) получает свой фокусированный Writer-интерфейс (ISP), а CLI выбирает бэкенд через конфиг. PostgreSQL растёт по одному домену, без god-object.

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

## 1. Паттерны (что делать)

### 1.1. ISP: Фокусированные Writer-интерфейсы

**Каждый домен — свой Writer** с минимальной поверхностью. Не god-object с 30 методами.

```go
// ✅ Правильно: cards.CardsWriter — 2 метода (после нашего рефакторинга)
type CardsWriter interface {
    SaveCards(ctx context.Context, cards []wb.ProductCard) (int, error)
    CountCards(ctx context.Context) (int, error)
}

// ✅ Правильно: sales.SalesWriter — 7 методов, ровно то что нужно Downloader
type SalesWriter interface {
    GetLastSaleDT(ctx) (time.Time, error)
    GetFirstSaleDT(ctx) (time.Time, error)
    DeleteSalesByDateRange(ctx, from, to string) (int64, error)
    // ...
}
```

**Правило:** Writer содержит **только** методы, реально вызываемые в `Downloader.Run()`. Если метод не вызывается в Run() — он не нужен интерфейсу.

### 1.2. Compile-time Assertions

Каждый адаптер проверяется на этапе компиляции:

```go
// pkg/storage/sqlite/cards_repo.go
var _ cards.CardsWriter = (*SQLiteSalesRepository)(nil)

// pkg/storage/postgres/cards_repo.go
var _ cards.CardsWriter = (*PgCardsRepo)(nil)
```

Если интерфейс меняется — компилятор сразу подсветит все адаптеры. Это главная защита от рассинхрона.

### 1.3. V2StorageConfig для выбора бэкенда

Единый конфиг для всех v2-утилит:

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

**Почему не BackendFactory:** factory — god-interface, нарушающий ISP. CLI знает какой Writer ему нужен — пусть сам создаёт.

### 1.4. pgxpool — Native PostgreSQL Driver

```go
import "github.com/jackc/pgx/v5/pgxpool"

pool, err := pgxpool.New(ctx, dsn)  // connection pool из коробки
```

**Не** `database/sql` + `lib/pq` — pgx даёт:
- Native connection pooling (MaxConns=10, MinConns=2)
- `pgx.ErrNoRows` вместо `sql.ErrNoRows`
- Лучшую производительность (binary protocol)

### 1.5. dllog для единого вывода

```go
// Заголовок
dllog.PrintHeader("WB Cards Downloader v2",
    dllog.HeaderField{Key: "Backend", Value: cfg.Storage.Backend},
    dllog.HeaderField{Key: "Mock", Value: fmt.Sprintf("%v", *mockMode)},
)

// Прогресс: одна строка на страницу
dllog.Progress(page, 0, "cards", fmt.Sprintf("%d cards saved (%s)", n, elapsed), start)

// Итог
dllog.Done(result.Duration, "%d cards, %d pages", result.TotalCards, result.Pages)
```

**Правило:** в `pkg/<domain>/` — ни одного `fmt.Printf`. Вывод только через `OnProgress` callback. CLI подключает `dllog`.

### 1.6. Config Reuse

Переиспользуем существующие типы из `pkg/config/`:

```yaml
# config.yaml
wb:
  api_key: ""
  timeout: "60s"

cards:
  api_key_env: "WB_API_CONTENT_KEY"
  rate_limit: 100

storage:
  backend: "postgres"
  pg_database: "wb_data_prod"
  pg_password_env: "PG_PWD"

filter:
  allowed_years: [2024, 2025, 2026]
```

```go
type Config struct {
    WB      config.WBClientConfig     `yaml:"wb"`
    Cards   cardsConfig               `yaml:"cards"`  // domain-specific
    Storage config.V2StorageConfig    `yaml:"storage"` // shared
    Filter  config.FunnelFilterConfig `yaml:"filter"`  // shared
}
```

### 1.7. YAGNI: Не добавлять Resume без необходимости

**Опыт cards:** cursor persistence добавил ~100 строк кода, JSON-сериализацию курсора, отдельную таблицу meta, 2 лишних метода в интерфейсе — и привёл к потере 391 карточки при `--resume`.

**Правило:**
- **Cards, stocks, prices** (~30k записей, 3-5 мин): полная перезагрузка. ON CONFLICT upsert безопасен.
- **Sales** (миллионы записей, часы): resume оправдан. Но это решение домена `pkg/sales/`, не общее.

Если resume не нужен — не добавляй. Writer interface без cursor-методов = проще.

---

## 2. Антипаттерны (чего избегать)

### 2.1. ❌ God-Object Repository

```go
// ❌ НЕ ДЕЛАТЬ: один репозиторий на все домены
type StorageBackend struct {
    SaveCards(...)
    SaveSales(...)
    SaveStocks(...)
    SaveFunnel(...)
    // ... 30 методов
}
```

**Почему плохо:** при добавлении PostgreSQL нужно реализовать все 30 методов сразу. Любое изменение интерфейса ломает все адаптеры.

**✅ Вместо:** фокусированные интерфейсы по домену:
```go
cards.CardsWriter   // 2 метода
sales.SalesWriter   // 7 методов
stocks.StocksWriter // 3 метода
```

Каждый адаптер (SQLite, PostgreSQL) реализует только нужные интерфейсы.

### 2.2. ❌ Курсор Resume для Лёгких Доменов

```go
// ❌ НЕ ДЕЛАТЬ: cursor persistence для cards (~30k, 3 мин)
type CardsWriter interface {
    SaveCards(...)
    GetCardsLastCursor(...)   // ← убрать, если домен "лёгкий"
    SaveCardsCursor(...)      // ← убрать
    CountCards(...)
}
```

**Наш опыт:** PG-загрузка с `--resume` пропустила 391 карточку, потому что курсор стоял на середине данных. Итого: 32,044 в PG vs 32,435 в SQLite. Debug занял 30 минут.

**✅ Вместо:** всегда полная загрузка + ON CONFLICT upsert. Для тяжёлых доменов (sales) — resume через data-level подход (`GetLastSaleDT`), а не через cursor-таблицу.

### 2.3. ❌ Возвращать Имя Env Var Вместо Значения

```go
// ❌ БАГ: resolveAPIKey возвращал "WB_API_CONTENT_KEY" как ключ
func resolveAPIKey(cfg Config) string {
    return cfg.Cards.APIKeyEnv  // "WB_API_CONTENT_KEY" — строка, не значение!
}

// ✅ Правильно:
func resolveAPIKey(cfg Config) string {
    if cfg.WB.APIKey != "" {
        return cfg.WB.APIKey
    }
    return os.Getenv(cfg.Cards.APIKeyEnv)  // os.Getenv("WB_API_CONTENT_KEY")
}
```

**Наш опыт:** 401 Unauthorized при первом реальном запуске. Легко поймать, но неприятно.

### 2.4. ❌ pgx как Indirect Dependency

```bash
# ❌ НЕ ДЕЛАТЬ: go get без использования в коде
go get github.com/jackc/pgx/v5
# pgx остаётся indirect в go.mod, компиляция ломается

# ✅ Правильно: написать код с import, потом go mod tidy
# 1. Написать pkg/storage/postgres/pool.go с import pgxpool
# 2. go mod tidy — промоутит pgx в direct
```

### 2.5. ❌ Две Строки на Страницу

```
# ❌ НЕ ДЕЛАТЬ: "Fetching..." + "100 cards saved" — две строки
[Page 1] Fetching cards...
✅ 100 cards saved (1.615s)

# ✅ Правильно: одна строка через dllog.Progress
16:50:14 [1] cards: 100 cards saved (0s)
```

**Правило:** один вызов `dllog.Progress()` на страницу. "Fetching" не нужно — пользователь видит timestamp.

### 2.6. ❌ Hardcoded Boolean Representations

```go
// ❌ НЕ ДЕЛАТЬ: boolToInt() для PostgreSQL
needKiz := boolToInt(card.NeedKiz)  // только для SQLite (INTEGER)

// ✅ Правильно: PostgreSQL native BOOLEAN
_, err := tx.Exec(ctx, insertSQL, card.NeedKiz)  // bool → PG BOOLEAN
```

**Наш опыт:** diff на 1000 строк показал "все разные" — оказалось `true/false` vs `1/0`. Это не баг данных, но сбивает при сравнении.

### 2.7. ❌ Конфликт Имен Типов

```go
// ❌ НЕ ДЕЛАТЬ: StorageConfig уже существует в pkg/config/config.go
type StorageConfig struct { ... }

// ✅ Правильно: уникальное имя
type V2StorageConfig struct { ... }
```

**Наш опыт:** компиляция сломалась из-за дублирования имён. V2-префикс решает.

### 2.8. ❌ Expression/Patial Indexes в Multi-Statement Exec

```go
// ❌ НЕ ДЕЛАТЬ: pgx.Exec() не парсит CASE/WHERE в multi-statement блоке
const schemaSQL = `
CREATE TABLE ... ;
CREATE INDEX idx ON t(CASE ... END);  -- syntax error at "CASE"
CREATE INDEX idx2 ON t(col) WHERE col IS NOT NULL;
`

// ✅ Правильно: expression и partial indexes — отдельные Exec() вызовы
const simpleSchemaSQL = `
CREATE TABLE ... ;
CREATE INDEX idx_simple ON t(col);
`
const exprIndexSQL = `CREATE INDEX idx_expr ON t((CASE ... END))`
const partialIndexSQL = `CREATE INDEX idx_part ON t(col) WHERE col IS NOT NULL`

func initSchema(ctx, pool) {
    pool.Exec(ctx, simpleSchemaSQL)          // table + simple indexes
    pool.Exec(ctx, exprIndexSQL)             // expression index — отдельный вызов
    pool.Exec(ctx, partialIndexSQL)          // partial index — отдельный вызов
}
```

**Наш опыт (sales v2):** `pgxpool.Exec()` отправляет multi-statement SQL через simple query protocol. Простые `CREATE INDEX` работают в блоке, но выражения (`CASE...END`, `WHERE ... IS NOT NULL`) вызывают `syntax error`. Решение: выполнять каждый сложный индекс отдельным `Exec()`.

**Важно:** expression indexes также требуют **двойные скобки** — `ON t((CASE...END))`, не `ON t(CASE...END)`. Внешние `()` — синтаксис CREATE INDEX, внутренние — группировка выражения. SQLite принимает оба варианта, PG — только двойные.

### 2.9. ❌ Сканирование Aggregate Functions в Value Types

```go
// ❌ НЕ ДЕЛАТЬ: MAX() на пустой таблице возвращает NULL → scan в string = panic
var lastDT string
pool.QueryRow(ctx, "SELECT MAX(rr_dt) FROM sales").Scan(&lastDT)

// ✅ Правильно: scan в *string, nil при пустой таблице
var lastDT *string
pool.QueryRow(ctx, "SELECT MAX(rr_dt) FROM sales").Scan(&lastDT)
if lastDT == nil {
    return time.Time{}, nil  // пустая таблица
}
t, _ := time.Parse(time.RFC3339, *lastDT)
```

**Наш опыт (sales v2):** `GetLastSaleDT()` использует `MAX(rr_dt)` для resume. На пустой таблице aggregate function возвращает NULL (не `ErrNoRows`). pgx при сканировании NULL в `string` вызывает ошибку. Решение: всегда scan в `*string` для aggregate результатов.

**Общее правило:** любой `SELECT MAX(...)` / `SELECT MIN(...)` / `SELECT SUM(...)` — scan в pointer type (`*string`, `*int`, `*float64`). Не-value types сломаются на NULL.

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

Для миграции нового домена (например stocks v2):

```
pkg/<domain>/                                    # NEW: domain package
├── types.go                                     # Source, Writer, Options, Result
├── source.go                                    # WBSource adapter (если нужен)
├── downloader.go                                # Downloader + Run()
├── mock.go                                      # MockSource
└── downloader_test.go                           # ≥4 тестов

pkg/storage/postgres/                            # ADD: PG adapter
├── pool.go                                      # EXISTS: shared pool wrapper
├── <domain>_schema.go                           # NEW: PG DDL
└── <domain>_repo.go                             # NEW: Pg<Domain>Repo

pkg/storage/sqlite/                              # MODIFY: add assertion
└── <domain>_repo.go                             # ADD: var _ <domain>.Writer = (*...)(nil)

cmd/data-downloaders/download-<domain>-v2/       # NEW: CLI driver
├── main.go                                      # ~120 строк
└── config.yaml                                  # V2StorageConfig + domain settings

pkg/config/pgconfig.go                           # EXISTS: V2StorageConfig (reuse as-is)
```

---

## 5. Checklist: миграция нового домена

### pkg/<domain>/ (ядро)
- [ ] `types.go` — Source (1-3 метода) + Writer (2-7 методов), Options, Result
- [ ] Writer содержит **только** методы, вызываемые в `Run()` — не больше
- [ ] Нет cursor/resume методов, если домен "лёгкий" (<100k записей, <10 мин)
- [ ] `downloader.go` — поля: Source interface + Writer interface, не concrete types
- [ ] `mock.go` — MockSource с детерминированными данными
- [ ] `downloader_test.go` — ≥4 кейса (Basic, DryRun, Limit, ContextCancel)
- [ ] Нет `fmt.Printf` / `log.*` — только `OnProgress` callback
- [ ] Нет `*sqlite.*` / `*wb.Client` в полях struct

### pkg/storage/postgres/ (PG adapter)
- [ ] `<domain>_schema.go` — DDL переведён из SQLite (см. SQL Translation Cheat Sheet)
- [ ] `<domain>_repo.go` — Pg<Domain>Repo implements <domain>.Writer
- [ ] Compile-time assertion: `var _ <domain>.Writer = (*Pg<Domain>Repo)(nil)`
- [ ] `$1, $2...` placeholders, не `?`
- [ ] `ON CONFLICT ... DO UPDATE SET ... = EXCLUDED ...` для upsert
- [ ] `BOOLEAN` вместо `INTEGER` для bool полей
- [ ] `DOUBLE PRECISION` вместо `REAL`
- [ ] `BIGSERIAL PRIMARY KEY` для auto-increment PK
- [ ] Foreign Keys с `ON DELETE CASCADE`
- [ ] Чанкинг по 500 записей в транзакции

### pkg/storage/sqlite/ (адаптер)
- [ ] Compile-time assertion: `var _ <domain>.Writer = (*SQLiteSalesRepository)(nil)`

### cmd/.../download-<domain>-v2/ (драйвер)
- [ ] `main.go` ~100-120 строк: flags → config → backend switch → DI → Run
- [ ] `config.yaml` с `V2StorageConfig` для выбора бэкенда
- [ ] Флаги: `--config`, `--db`, `--backend`, `--pg-database`, `--mock`, `--dry-run`, `--limit`
- [ ] Date-based домены: `--days`, `--begin`, `--end` + `resolveDateRange()` (days от вчерашнего дня)
- [ ] Нет `--resume` для лёгких доменов
- [ ] `SetRateLimit()` вызывается явно после `wb.New()`
- [ ] `resolveAPIKey()` использует `os.Getenv()`, не возвращает имя переменной
- [ ] `dllog.PrintHeader()` + `dllog.Progress()` + `dllog.Done()`
- [ ] Ctrl+C через `signal.NotifyContext()`

### Verification
- [ ] `go build ./pkg/<domain>/` — компилируется
- [ ] `go test ./pkg/<domain>/ -v` — все PASS
- [ ] `go run ./cmd/.../download-<domain>-v2/ --mock --backend=sqlite --db=/tmp/test.db`
- [ ] `go run ./cmd/.../download-<domain>-v2/ --mock --backend=postgres --pg-database=wb_data_test`
- [ ] `go build ./cmd/.../download-<domain>/` — v1 не сломан
- [ ] Data parity: mock download → сравнить SQLite vs PostgreSQL

---

## 6. Опыт миграции cards v2: timeline

| Шаг | Результат | Занял |
|-----|-----------|-------|
| Проектирование интерфейсов | CardsWriter (4 метода → потом 2), CardsSource (1 метод) | 1 час |
| pkg/cards/ (types, source, downloader, mock, tests) | 5 файлов, 5 тестов | 2 часа |
| pkg/storage/postgres/ (pool, schema, repo) | 3 файла | 1.5 часа |
| CLI driver + config | main.go ~170 строк, config.yaml | 30 мин |
| Баг: StorageConfig name collision | → V2StorageConfig | 15 мин |
| Баг: pgx indirect dependency | → go mod tidy | 10 мин |
| Баг: resolveAPIKey returns env var name | → os.Getenv() | 20 мин |
| Баг: two-line output per page | → dllog.Progress() | 15 мин |
| Рефакторинг: cursor resume removal | -130 строк, 7 файлов | 30 мин |
| Data comparison (SQLite vs PG) | 0 реальных расхождений, 391 resume-пропущенных | 30 мин |

**Итого:** ~7 часов, из которых ~2 часа — на исправление антипаттернов и багов.

---

## 7. Quick Reference: Наши домены

| Домен | Writer методов | Resume? | Pagination | PG адаптер |
|-------|---------------|---------|------------|------------|
| cards | 2 | ❌ Нет | Cursor (WB API) | ✅ Готов |
| sales | 7 | ✅ Да (date-level) | Date-range iterator | ✅ Готов |
| funnel | 5 | Partial (time-based) | Batch-by-nmIDs | — |
| nmreport | 5 | ✅ Да (report-level) | Async CSV | — |
| stocks | — | — | — | — |
| feedbacks | — | — | — | — |
| region-sales | — | — | — | — |
| promotion | — | — | — | — |

**Порядок миграции:** карточки ✅ → stocks → feedbacks → region-sales → promotion. Следуй правилу из dev_utils.md: мигрируй когда нужен фикс или фича, не массово.

---

**Last Updated:** 2026-05-31
**Version:** 1.2 (sales v2 production config, from/to date support, days-from-yesterday)
