# dev_v2_postgres.md — V2 Downloader Migration Guide (Dual-Backend: SQLite + PostgreSQL)

**Дата**: 2026-06-02
**Статус**: Актуальный (на основе опыта cards v2, sales v2, orders v2)
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

**DSN construction fallback** (`pgconfig.go: GetEffectiveDSN()`):
```
pg_dsn field non-empty → used as-is (full DSN override)
pg_dsn field empty     → BuildPgDSN() assembles from:
    host:     PGHOST env || "192.168.10.7"
    port:     PGPORT env || "15432"
    user:     "postgres"
    password: os.Getenv(pg_password_env)  // e.g. PG_PWD
    database: pg_database field            // e.g. "wb_data_prod"
```

```go
// Example DSN produced by BuildPgDSN():
// postgres://postgres:MySecret@192.168.10.7:15432/wb_data_prod?sslmode=disable
```

### 1.4. pgxpool — Native PostgreSQL Driver

```go
import "github.com/jackc/pgx/v5/pgxpool"

pool, err := pgxpool.New(ctx, dsn)  // connection pool из коробки
```

**Не** `database/sql` + `lib/pq` — pgx даёт:
- Native connection pooling (MaxConns=10, MinConns=2)
- `pgx.ErrNoRows` вместо `sql.ErrNoRows`
- Лучшую производительность (binary protocol)

**⚠️ Dual-backend error handling:** при наличии Reader-интерфейсов (не Writer), `sql.ErrNoRows` (SQLite) и `pgx.ErrNoRows` (PostgreSQL) — разные типы. Используйте `errors.Is()` для portable проверок. Для текущих Writer-интерфейсов (Save/Count) это не актуально — они не делают single-row lookups.

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

### 1.7. Config YAML: Полный и Прокомментированный

Каждый v2 загрузчик **обязан** иметь `config.yaml` с **комментариями на каждый параметр** — без «полупустых» yaml-файлов. Пользователь должен понимать назначение, допустимые значения и дефолты без чтения Go-кода.

**Требования:**
- Каждый параметр — с комментарием (`# ...`) на той же или предыдущей строке
- Для rate limits — указать swagger-источник и двухуровневую логику (desired vs api floor)
- Для env vars — указать имя переменной и что она содержит
- Для storage — описать оба бэкенда и когда какой используется
- Секции разделены `====` заголовками для сканируемости
- Домен-специфичные параметры (например, `days` для orders, `api_key_env` для cards) — с объяснением что и зачем

**Минимальная структура:**
```yaml
# <Domain> Downloader v2 — полная конфигурация
# Используется: <список pkg/config типов>
#
# API: <API name> (<base URL>)
# Endpoint: <method> <path>
# Pagination: <тип>, <max per page>
# Rate: <swagger rate> (swagger)

# ============================================================================
# WB API клиент — общий для всех WB-утилит
# ============================================================================
wb:
  api_key: ""                  # Прямой API ключ (не рекомендуется — лучше через env)
  base_url: ""                 # Переопределение API URL (пусто = дефолт)
  rate_limit: 100              # Базовый rate limit (req/min)
  burst: 5                     # Базовый burst
  timeout: "30s"               # HTTP timeout

# ============================================================================
# <Домен> — доменная конфигурация
# ============================================================================
<domain>:
  api_key_env: "WB_API_KEY"   # Env var с API ключом
                                # Приоритет: api_key > api_key_env > WB_API_KEY

  # Двухуровневый adaptive rate limiting
  rate_limit: 100              # Desired: запросов в минуту
  burst_limit: 5               # Desired: burst
  api_rate_limit: 100          # API floor: запросов в минуту (swagger)
  api_burst_limit: 5           # API floor: burst (swagger)
  adaptive_probe_after: 10    # OKs на api floor перед probe desired
  max_backoff_seconds: 60     # Максимальный backoff при 429

# ============================================================================
# Хранение — выбор бэкенда
# ============================================================================
storage:
  backend: "postgres"          # "sqlite" (default) | "postgres"
  db_path: "/var/db/wb-sales.db"  # Путь к SQLite (при backend=sqlite)
  pg_dsn: ""                   # Полный DSN (пусто = собрать из env vars)
  pg_database: "wb_data_prod"  # Имя PG базы (wb_data_prod | wb_data_test)
  pg_password_env: "PG_PWD"   # Env var с паролем PG
```

**Контрпример (❌ НЕ делать):**
```yaml
# ❌ Полупустой конфиг без комментариев — пользователь не понимает параметры
wb:
  api_key: ""
  timeout: 30s

prices:
  api_key_env: "WB_API_KEY"
  rate_limit: 100

storage:
  backend: "postgres"
```

### 1.8. YAGNI: Не добавлять Resume без необходимости

**Опыт cards:** cursor persistence добавил ~100 строк кода, JSON-сериализацию курсора, отдельную таблицу meta, 2 лишних метода в интерфейсе — и привёл к потере 391 карточки при `--resume`.

**Правило:**
- **Cards, stocks, prices** (~30k записей, 3-5 мин): полная перезагрузка. ON CONFLICT upsert безопасен.
- **Sales** (миллионы записей, часы): resume оправдан. Но это решение домена `pkg/sales/`, не общее.

Если resume не нужен — не добавляй. Writer interface без cursor-методов = проще.

### 1.9. Mock Safety: DiscardWriter

**`--mock` режим НИКОГДА не должен обращаться к базе данных.** Mock подменяет источник данных (API), но Writer должен быть безопасным.

```go
// pkg/<domain>/mock.go — DiscardWriter: считает, но не пишет
type DiscardWriter struct {
    mu    sync.Mutex
    saved int
}
func (w *DiscardWriter) SaveOrders(_ context.Context, items []wb.OrdersItem) (int, error) {
    w.mu.Lock()
    w.saved += len(items)
    w.mu.Unlock()
    return len(items), nil
}
func (w *DiscardWriter) DeleteOrdersOlderThan(_ context.Context, _ time.Time) (int64, error) {
    return 0, nil
}
```

**CLI wiring — mock mode НЕ создаёт DB writer:**
```go
var writer domain.Writer
var cleanup func()

if *mockMode {
    writer = &domain.DiscardWriter{}  // ← no DB at all
    cleanup = func() {}
} else {
    writer, cleanup, err = createWriter(ctx, cfg.Storage)
}
defer cleanup()
```

**Почему:** предыдущие v2 загрузчики (cards, sales) в `--mock` режиме открывают реальную БД и пишут в неё. Это уже привело к потере ~30,900 записей продаж (`--mock` + `rewrite: true` = удалил реальные данные и вставил фейковые).

**Правило:** `--mock` = mock source + DiscardWriter → ноль DB-взаимодействий.

### 1.10. Mandatory README

Каждый v2 загрузчик **обязан** иметь `README.md` с минимальной структурой:

```markdown
# WB <Domain> Downloader v2

Downloads <domain> data from WB <API> API.

## Usage
go run . --mock                                    # mock mode, no DB
go run . --mock --db /tmp/test.db                  # mock + test SQLite
go run . --dry-run --db /tmp/test.db --config ...  # real API, no writes
go run . --config config.yaml                      # production (user only!)

## Configuration
(see config.yaml)

## Database Schema
Table: <table> (...)

## Rate Limiting
<rate> req/min, burst <burst> (WB <API> API)
```

**Цель:** каждый загрузчик самодокументируем. Пользователь видит безопасные примеры запуска без чтения кода.

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

### 2.10. ❌ Mock Mode Writing to Production DB

**Наш опыт:** `--mock` + `rewrite: true` в конфиге → загрузчик удалил ~30,900 реальных записей продаж и вставил 250 фейковых. CLAUDE.md уже предупреждает: "`--mock` НЕ является read-only режимом!"

Root cause: cards/sales загрузчики открывают реальную БД **до** проверки `--mock`. Writer создаётся unconditional — mock подменяет только Source, не Writer.

**Решение:** см. §1.9 (Mock Safety: DiscardWriter) — полный шаблон безопасного wiring. `--mock` = mock source + DiscardWriter → ноль DB-взаимодействий.

**⚠️ Known tech debt:** cards и sales v2 до сих пор используют legacy mock safety (открывают БД в `--mock`). Обратная миграция на DiscardWriter — в backlog.

### 2.11. ❌ Test Commands Without Explicit DB Path

```bash
# ❌ НЕ ДЕЛАТЬ: без --db используется default из конфига (/var/db/wb-sales.db)
go run . --mock
go run . --dry-run

# ✅ Правильно: ВСЕГДА явный путь к тестовой базе
go run . --mock --db /tmp/test-<domain>.db
go run . --dry-run --db /tmp/test-<domain>.db
go run . --mock --backend postgres --pg-database wb_data_test
```

**Правило:** `pkg/config/utility.go` содержит hardcoded default `/var/db/wb-sales.db`. Без `--db` флага любая команда (даже `--mock`!) может попасть в production. CLI `--db` всегда перекрывает default.

**Verification commands** в README и чеклистах обязаны содержать `--db /tmp/...` или `--pg-database <test>`.

### 2.12. ❌ Placeholder Count Mismatch (Mock не ловит)

```go
// ❌ БАГ: 28 колонок, но 26 '?' — sqlite3.PrepareContext падает в runtime
INSERT OR REPLACE INTO orders (
    srid, order_date, last_change_date, ...  -- 28 columns
) VALUES (?,?,?,?,...,?)  -- только 26 '?' + CURRENT_TIMESTAMP = 27 ≠ 28
```

**Mock-тесты НЕ ловят этот баг**, потому что:
1. `DiscardWriter` не выполняет SQL — он просто считает записи
2. Compile-time assertion (`var _ Writer = (*Repo)(nil)`) проверяет только интерфейс, не содержимое SQL
3. In-memory тесты с `sql.Open("sqlite3", ":memory:")` проходят — Prepare не вызывается, если тест мокает Writer

**Наш опыт (orders v2):** `--mock` прошёл успешно, но реальный запуск упал с `27 values for 28 columns`. Тот же баг повторился дважды: сначала в PostgreSQL-адаптере (commit `ccf5fdd`), потом в SQLite.

**✅ Правило: после написания INSERT/UPSERT — сверить count:**

```bash
# Быстрая проверка: подсчитать '?' в VALUES и сравнить с числом колонок
# SQLite
sed -n '/VALUES/,/)/p' pkg/storage/sqlite/<domain>_repo.go | tr -cd '?' | wc -c
# Должно быть: (число колонок) - (число CURRENT_TIMESTAMP/функций)

# PostgreSQL
sed -n '/VALUES/,/)/p' pkg/storage/postgres/<domain>_repo.go | grep -oP '\$\d+' | sort -t'$' -k1 -n | tail -1
# Должно быть: $N где N = (число колонок) - (число SQL-функций)
```

**Или программно — добавить sanity-тест:**
```go
func TestInsertPlaceholdersMatch(t *testing.T) {
    // Считаем колонки в INSERT
    colCount := strings.Count(insertOrderSQL[:strings.Index(insertOrderSQL, "VALUES")], ",") + 1
    // Считаем '?' плейсхолдеры
    qCount := strings.Count(insertOrderSQL[strings.Index(insertOrderSQL, "VALUES"):], "?")
    // CURRENT_TIMESTAMP / TO_CHAR не считаются
    assert.Equal(t, colCount, qCount+1)  // +1 для CURRENT_TIMESTAMP
}
```

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
- [ ] Writer содержит **только** методы, вызываемые в `Run()` — не больше
- [ ] Нет cursor/resume методов, если домен "лёгкий" (<100k записей, <10 мин)
- [ ] `downloader.go` — поля: Source interface + Writer interface, не concrete types
- [ ] `mock.go` — MockSource с детерминированными данными + **DiscardWriter** (no-op, counts only)
- [ ] `downloader_test.go` — ≥4 кейса (Basic, DryRun, Limit/Rewrite, ContextCancel)
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
- [ ] `main.go` ~120-130 строк: flags → config → backend switch → DI → Run
- [ ] `config.yaml` с `V2StorageConfig` для выбора бэкенда + `FunnelFilterConfig` для фильтрации
- [ ] `config.yaml` — **полный и прокомментированный** (§1.7): каждый параметр с комментарием, секции с `====` заголовками, swagger-источник для rate limits, приоритет env vars
- [ ] **Mock safety:** `--mock` режим использует `DiscardWriter`, НЕ создаёт реальный DB writer
- [ ] Флаги: `--config`, `--db`, `--backend`, `--pg-database`, `--mock`, `--dry-run`, `--limit`
- [ ] Date-based домены: `--days`, `--begin`, `--end` + `resolveDateRange()` (days от вчерашнего дня)
- [ ] Нет `--resume` для лёгких доменов
- [ ] `SetRateLimit()` вызывается явно после `wb.New()`
- [ ] `resolveAPIKey()` использует `os.Getenv()`, не возвращает имя переменной
- [ ] `dllog.PrintHeader()` + `dllog.Progress()` + `dllog.Done()`
- [ ] Ctrl+C через `signal.NotifyContext()`
- [ ] **`README.md` — mandatory** (usage, config, schema, rate limits, safe examples)

### Verification
**⚠️ Safety rule:** ALL test commands use `--db /tmp/test-<domain>.db` — НИКОГДА не `/var/db/`.

- [ ] `go build ./pkg/<domain>/` — компилируется
- [ ] `go test ./pkg/<domain>/ -v` — все PASS (in-memory mock writers, no DB)
- [ ] **Placeholder count check:** число `?` (SQLite) / `$N` (PostgreSQL) в VALUES = число колонок минус SQL-функции. Mock-тесты это **не ловят**!
- [ ] `go run ./cmd/.../download-<domain>-v2/ --mock` — DiscardWriter, **no DB file created at all**
- [ ] `go run ./cmd/.../download-<domain>-v2/ --mock --db /tmp/test.db --backend=sqlite`
- [ ] `go run ./cmd/.../download-<domain>-v2/ --mock --backend=postgres --pg-database=wb_data_test`
- [ ] `go run ./cmd/.../download-<domain>-v2/ --dry-run --db /tmp/test.db --config ...`
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

| Домен | Writer методов | Resume? | Pagination | PG адаптер | Mock Safety |
|-------|---------------|---------|------------|------------|-------------|
| cards | 2 | ❌ Нет | Cursor (WB API) | ✅ Готов | ⚠️ Legacy (пишет в БД) |
| sales | 7 | ✅ Да (date-level) | Date-range iterator | ✅ Готов | ⚠️ Legacy (пишет в БД) |
| orders | 2 | ❌ Нет | lastChangeDate iterator | ✅ Готов | ✅ DiscardWriter |
| prices | 2 | ❌ Нет | Offset (limit/offset) | ✅ Готов | ✅ DiscardWriter |
| funnel | 5 | Partial (time-based) | Batch-by-nmIDs | — | — |
| nmreport | 5 | ✅ Да (report-level) | Async CSV | — | — |
| stocks | — | — | — | — | — |
| feedbacks | — | — | — | — | — |
| region-sales | — | — | — | — | — |
| promotion | — | — | — | — | — |

**Примечание:** cards и sales имеют ⚠️ Legacy mock safety — `--mock` режим открывает реальную БД. Новые домены (orders+) обязаны использовать DiscardWriter. Обратная миграция cards/sales на DiscardWriter — в backlog.

**Порядок миграции:** карточки ✅ → stocks → feedbacks → region-sales → promotion. Следуй правилу из dev_utils.md: мигрируй когда нужен фикс или фича, не массово.

---

**Last Updated:** 2026-06-03
**Version:** 1.6 (review fixes: renumber §1.6a→§1.7, deduplicate §2.10/§1.9, swap §2.11/§2.12, add DSN construction, ErrNoRows note, update domain table)
