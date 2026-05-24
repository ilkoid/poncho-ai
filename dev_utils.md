# dev_utils.md — Правила создания утилит v2 (pkg/-based)

Утилиты v2 — это загрузчики и анализаторы данных, бизнес-логика которых вынесена в `pkg/` и готова к использованию как через CLI, так и через Tool-обёртку агента. Документ описывает правила миграции и создания новых утилит по архитектуре v2.

**Связанные документы:**
- [dev_downloader_development.md](dev_downloader_development.md) — **v1 (DEPRECATED)**: правила для монолитных загрузчиков в `cmd/`
- [dev_manifest.md](dev_manifest.md) — общие архитектурные правила (Rule 6, Rule 13)
- [raw-data-downloaders-as-tools-plan.md](plans/raw-data-downloaders-as-tools-plan.md) — стратегия миграции v1 → v2
  - [persistence-интерфейсов стратегия](plans/data-downloaders-persistence-interfaces-strategy.md) — Writer интерфейсы для всех доменов
  - [sales-downloader-v2-audit-fix-plan.md](plans/sales-downloader-v2-audit-fix-plan.md) — технические шаги исправлений аудита

---

## Принцип

v1: бизнес-логика заперта в `package main` каждого `cmd/data-downloaders/*`.
v2: бизнес-логика в `pkg/<domain>/`, CLI — тонкий драйвер в ~100 строк.

```
pkg/sales/                          ← ядро (Source + Writer интерфейсы, Downloader)
cmd/.../download-wb-sales-v2/       ← тонкий драйвер (flags, config, wiring)
pkg/tools/std/download_sales_tool.go ← Tool-обёртка (фаза 3)
```

---

## Migration Status (2026-05-25)

| Downloader | v2 pkg/ | v2 cmd/ | Mock | Tests |
|---|---|---|---|---|
| download-wb-sales | `pkg/sales/` | sales-v2 | Yes | Yes |
| download-wb-funnel | `pkg/funnel/` | funnel-v2 | Yes | Yes |
| download-wb-funnel-csv | `pkg/nmreport/` | (same cmd/) | Yes | Yes |
| Others (16) | -- | v1 only | Partial | Partial |

## When to Migrate v1 → v2

Do NOT mass-migrate all downloaders. Migrate when:
1. Bug fix needed → fix in v1, then create v2
2. New feature needed → implement in v2 directly
3. Active analysis usage → prioritize v2 for testability

---

## Правила

### 1. Rule of pkg/ Core

Бизнес-логика скачивания/анализа живёт в `pkg/<domain>/`. CLI в `cmd/` — только wiring: flags, config, DI, Ctrl+C.

**Структура `pkg/<domain>/`:**
```
pkg/<domain>/
├── types.go          # публичные типы: *Source, *Writer интерфейсы, Options, Result
├── downloader.go     # Downloader struct + Run() метод
├── mock.go           # Mock*Source для --mock и тестов
└── downloader_test.go
```

CLI-драйвер:
```
cmd/<family>/<name>/
├── main.go           # ~100 строк: flags → config → DI → Run
└── config.yaml
```

`★ Проверка`: если из `pkg/<domain>/` убран `main.go` и код всё ещё компилируется и тестируется — архитектура верна.

### 2. Rule of Source Interface

Даунлоудер зависит от минимального интерфейса источника данных, а не от `*wb.Client`.

```go
// pkg/sales/types.go — потребитель определяет порт
type SalesSource interface {
    ReportDetailByPeriodIteratorWithTime(ctx, baseURL, rateLimit, burst, from, to, cb) (int, error)
    ReportDetailByPeriodIterator(ctx, baseURL, rateLimit, burst, from, to, cb) (int, error)
}
```

**Почему:** `*wb.Client` удовлетворяет интерфейсу через structural typing — без адаптера. Мок подставляется напрямую. Tool может обёрнуть другой источник.

**Что не является Source:** интерфейс с 20 методами «на всякий случай». Только методы, реально вызываемые `Downloader.Run()`.

### 3. Rule of Writer Interface

Даунлоудер зависит от узкого интерфейса записи, а не от конкретного репозитория.

```go
// pkg/sales/types.go — порт объявляет потребитель
type SalesWriter interface {
    GetLastSaleDT(ctx) (time.Time, error)
    GetFirstSaleDT(ctx) (time.Time, error)
    Save(ctx, []Row) error
    Exists(ctx, id) (bool, error)
    DeleteByDateRange(ctx, from, to) (int64, error)
    // ...
}
```

**Compile-time assertion** в пакете-реализации:
```go
// pkg/storage/sqlite/repository.go
var _ sales.SalesWriter = (*SQLiteSalesRepository)(nil)
```

**Принцип:** интерфейс объявлен в `pkg/<domain>/` (потребитель), реализован в `pkg/storage/sqlite/` (адаптер). Не наоборот. (dev_manifest Rule 6: Port & Adapter)

### 4. Rule of OnProgress Callback

В `pkg/<domain>/` — ни одного прямого `fmt.Printf`, `fmt.Println`, `log.*`. Вывод — через callback.

```go
type DownloadOptions struct {
    // ...
    OnProgress func(msg string) // nil = silent (Tool mode)
}
```

CLI-драйвер передаёт `fmt.Println`. Tool передаёт `nil` или свой emitter.

**Как проверить:** `grep -r "fmt\.Print" pkg/<domain>/` → 0 результатов (кроме helper-метода `progress()`).

### 5. Rule of Mandatory Modes

Каждая утилита v2 обязана поддерживать:

| Флаг | Назначение | Поведение |
|-----------------|-----------|
| `--mock` | Нет реальных API-вызовов | `MockSource` возвращает детерминированные данные |
| `--dry-run` | Нет записи в DB | API вызовы происходят, но `Writer.Save()` и `Writer.Delete*()` пропускаются |

Реализация `--mock`:
```go
var source domain.Source
if *mockMode {
    source = &domain.MockSource{}
} else {
    client := wb.New(apiKey)
    client.SetRateLimit(...)
    source = client // *wb.Client удовлетворяет Source
}
```

Реализация `--dry-run`:
```go
type DownloadOptions struct {
    DryRun bool
}
// В saveRows: if d.opts.DryRun { count; return nil }
```

### 6. Rule of Config Reuse

Использовать типы из `pkg/config/utility.go`, а не локальные struct-ы.

```go
type Config struct {
    WB       config.WBClientConfig  `yaml:"wb"`       // API key, rate limit, timeout
    Download config.DownloadConfig   `yaml:"download"`  // days, resume, rewrite, adaptive
    Storage  StorageConfig           `yaml:"storage"`   // domain-specific
}
```

**`config.DownloadConfig`** уже содержит: `Days`, `Resume`, `Rewrite`, `SkipServiceRecords`, `AdaptiveRecoverAfter`, `AdaptiveProbeAfter`, `MaxBackoffSeconds`.

Если нужен domain-specific config — добавить в `pkg/config/utility.go` с `GetDefaults()`.

### 7. Rule of Rate Limiting Setup

`wb.New()` не настраивает rate limiting. Вызвать явно:

```go
client := wb.New(apiKey)
client.SetRateLimit("tool_id",
    desiredRate, desiredBurst,  // целевая скорость
    apiRate, apiBurst,          // swagger-documented floor
)
client.SetAdaptiveParams(
    recoverAfter,  // OKs before restore to api floor (0 = deprecated)
    probeAfter,    // OKs at api floor before probing desired
    maxBackoff,    // cap for exponential backoff (seconds)
)
```

**Значения из конфига**, не хардкод:
```yaml
wb:
  rate_limit: 1
  burst_limit: 2
download:
  adaptive_recover_after: 5
  adaptive_probe_after: 10
  max_backoff_seconds: 60
```

### 8. Rule of Testing with Mocks

Тесты в `pkg/<domain>/` используют моки, а не реальный SQLite или API.

```go
type mockWriter struct { /* implements SalesWriter */ }

func TestDownloader_Run_Basic(t *testing.T) {
    writer := newMockWriter()
    source := &MockSource{RowCount: 3}
    dl := NewDownloader(source, writer, opts)
    result, err := dl.Run(ctx, ranges, false, false)
    // assert result + writer state
}
```

**Минимум 4 теста:**
1. Basic download (save + count)
2. DryRun (no writes)
3. Rewrite (delete before save)
4. Resume (skip existing)
5. Context cancellation

### 9. Rule of No Dead Code

Нет неиспользуемым типам, интерфейсам, функциям. Если определили `Source` interface — wire его в `Downloader`. Если определили `Job` struct — используйте как аргумент `Run()`. Если не используете — удалите.

**Проверка:** `go build` не ловит неиспользуемые exported типы. Проверять глазами и grep-ом.

### 10. Rule of Constants, not Hardcodes

API URL, default timeout, max days per period — константы пакета, не строковые литералы в теле функций.

```go
const statsAPIURL = "https://statistics-api.wildberries.ru"
```

---

## Структура Downloader (каноническая)

```go
// pkg/<domain>/downloader.go
type Downloader struct {
    source Source   // интерфейс, не *wb.Client
    writer Writer   // интерфейс, не *sqlite.SQLiteSalesRepository
    opts   Options
}

func NewDownloader(source Source, writer Writer, opts Options) *Downloader
func (d *Downloader) Run(ctx context.Context, ranges []wb.DateRange, resume, rewrite bool) (*Result, error)
```

**Конструктор принимает интерфейсы**, а CLI-драйвер передаёт конкретные реализации:
```go
dl := domain.NewDownloader(wbClient, repo, opts)  // wbClient → Source, repo → Writer
```

---

## Чеклист: создание утилиты v2

### pkg/<domain>/ (ядро)
- [ ] `types.go` — `Source` + `Writer` интерфейсы, `Options`, `Result`
- [ ] `downloader.go` — `Downloader` struct + `Run()`, все поля — интерфейсы
- [ ] `mock.go` — `MockSource` для `--mock` и тестов
- [ ] `downloader_test.go` — тесты с mock Writer + mock Source (≥4 кейса)
- [ ] Нет `fmt.Printf` / `log.*` (только через `OnProgress`)
- [ ] Нет `*sqlite.*` / `*wb.Client` в полях struct (только интерфейсы)
- [ ] Constants для URL и дефолтов, не хардкод

### cmd/<family>/<name>/ (драйвер)
- [ ] `main.go` ~100 строк: flags → config → DI → Run
- [ ] `config.yaml` с `${ENV}` expansion
- [ ] Флаги: `--config`, `--mock`, `--dry-run`, `--days`, `--resume`, `--rewrite`
- [ ] `SetRateLimit()` + `SetAdaptiveParams()` вызываются явно
- [ ] Config struct использует типы из `pkg/config/utility.go`
- [ ] `dllog.PrintHeader()` / `dllog.Done()` для консистентного вывода
- [ ] Ctrl+C через `signal.NotifyContext()`

### pkg/storage/sqlite/ (адаптер)
- [ ] Compile-time assertion: `var _ domain.Writer = (*SQLiteSalesRepository)(nil)`
- [ ] Методы Writer реализованы на существующем репозитории

---

## Карта миграции: v1 → v2

| Шаг | Действие | Проверка |
|-----|----------|----------|
| 1 | Создать `pkg/<domain>/types.go` — Source, Writer, Options, Result | `go build ./pkg/<domain>/` |
| 2 | Создать `pkg/<domain>/downloader.go` — Downloader + Run() | Интерфейсы wired, нет concrete types |
| 3 | Создать `pkg/<domain>/mock.go` — MockSource | `go build` + `go test` |
| 4 | Создать `pkg/<domain>/downloader_test.go` | `go test ./pkg/<domain>/ -v` → все PASS |
| 5 | Создать тонкий `cmd/.../<name>-v2/main.go` | `go build ./cmd/.../<name>-v2/` |
| 6 | Добавить compile-time assertion в `pkg/storage/sqlite/` | `var _ domain.Writer = (*SQLiteSalesRepository)(nil)` |
| 7 | (опционально) Tool wrapper в `pkg/tools/std/` | Tool interface: `Definition() + Execute()` |

**v1 остаётся нетронутым** пока v2 не пройдёт ручное тестирование на реальных данных.
