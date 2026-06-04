# dev_v2_downloader.md — V2 Downloader Guide (Source/Writer + Anti-Patterns)

Утилиты v2 — загрузчики и анализаторы данных, бизнес-логика которых вынесена в `pkg/` и готова к использованию как через CLI, так и через Tool-обёртку агента.

**Связанные документы:**
- [dev_manifest.md](dev_manifest.md) — общие архитектурные правила (Rule 6, Rule 13)
- [dev_v2_postgres.md](dev_v2_postgres.md) — PostgreSQL адаптер (dual-backend, SQL cheat sheet)
- [dev_best_practices.md](dev_best_practices.md) — общие паттерны, Rule 0: Code Reuse First

**Приоритет:** доменный — побеждает `dev_best_practices.md` при конфликте. Для PostgreSQL — `dev_v2_postgres.md` побеждает этот документ.

---

## Принцип

v1: бизнес-логика заперта в `package main` каждого `cmd/data-downloaders/*`.
v2: бизнес-логика в `pkg/<domain>/`, CLI — тонкий драйвер в ~100 строк.

```
pkg/sales/                          ← ядро (Source + Writer интерфейсы, Downloader)
cmd/.../download-wb-sales-v2/       ← тонкий драйвер (flags, config, wiring)
pkg/tools/std/download_sales_tool.go ← Tool-обёртка (фаза 3)
```

## When to Migrate v1 → v2

Do NOT mass-migrate. Migrate when:
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
├── mock.go           # Mock*Source + DiscardWriter для --mock и тестов
└── downloader_test.go
```

CLI-драйвер:
```
cmd/<family>/<name>/
├── main.go           # ~100 строк: flags → config → DI → Run
├── config.yaml       # полный, прокомментированный (§1.7 в dev_v2_postgres.md)
└── README.md         # mandatory: usage, schema, safe examples
```

`★ Проверка`: если из `pkg/<domain>/` убран `main.go` и код всё ещё компилируется и тестируется — архитектура верна.

### 2. Rule of Source Interface

Даунлоудер зависит от минимального интерфейса источника данных, а не от `*wb.Client`.

```go
type SalesSource interface {
    ReportDetailByPeriodIterator(ctx, baseURL, rateLimit, burst, from, to, cb) (int, error)
}
```

**Почему:** `*wb.Client` удовлетворяет интерфейсу через structural typing — без адаптера. Мок подставляется напрямую.

**Что не является Source:** интерфейс с 20 методами «на всякий случай». Только методы, реально вызываемые `Downloader.Run()`.

### 3. Rule of Writer Interface

Даунлоудер зависит от узкого интерфейса записи, а не от конкретного репозитория.

```go
type SalesWriter interface {
    GetLastSaleDT(ctx) (time.Time, error)
    Save(ctx, []Row) error
    // ... только методы, вызываемые в Run()
}
```

**Compile-time assertion** в пакете-реализации:
```go
var _ sales.SalesWriter = (*SQLiteSalesRepository)(nil)  // sqlite
var _ sales.SalesWriter = (*PgSalesRepo)(nil)             // postgres
```

**Принцип:** интерфейс объявлен в `pkg/<domain>/` (потребитель), реализован в `pkg/storage/` (адаптер). (dev_manifest Rule 6)

### 4. Rule of OnProgress Callback

В `pkg/<domain>/` — ни одного `fmt.Printf`, `log.*`. Вывод — через callback + `dllog` в CLI.

```go
type DownloadOptions struct {
    OnProgress func(msg string) // nil = silent (Tool mode)
}
```

CLI-драйвер подключает `dllog.PrintHeader()` / `dllog.Progress()` / `dllog.Done()`.

### 5. Rule of Mandatory Modes

| Флаг | Поведение |
|------|-----------|
| `--mock` | `MockSource` + `DiscardWriter` — нет API-вызовов, нет DB-взаимодействий |
| `--dry-run` | API вызовы происходят, но `Writer.Save()` / `Writer.Delete*()` пропускаются |

**`--mock` wiring:**
```go
var writer domain.Writer
if *mockMode {
    source = &domain.MockSource{}
    writer = &domain.DiscardWriter{}  // ← no DB at all
    cleanup = func() {}
} else {
    client := wb.New(apiKey)
    client.SetRateLimit(...)
    source = client
    writer, cleanup, err = createWriter(ctx, cfg.Storage)
}
defer cleanup()
```

**⚠️ Критично:** `--mock` НЕ открывает реальную БД. Предыдущие загрузчики в `--mock` + `rewrite: true` удалили ~30,900 реальных записей.

### 6. Rule of Config Reuse

Использовать типы из `pkg/config/utility.go`, а не локальные struct-ы.

```go
type Config struct {
    WB       config.WBClientConfig  `yaml:"wb"`
    Download config.DownloadConfig  `yaml:"download"`
    Storage  config.V2StorageConfig `yaml:"storage"`  // dual-backend
}
```

**`config.DownloadConfig`** содержит: `Days`, `From`/`To` (приоритет над Days), `Resume`, `Rewrite`, `AdaptiveProbeAfter`, `MaxBackoffSeconds`.

**⚠️ Не дефолтить `Days` в `GetDefaults()`** — это перезапишет явные `from`/`to` из конфига. Дефолтить `Days` в `main.go` только когда оба `From` и `To` пустые.

### 7. Rule of Rate Limiting Setup

`wb.New()` не настраивает rate limiting. Вызвать явно:

```go
client := wb.New(apiKey)
client.SetRateLimit("tool_id", desiredRate, desiredBurst, apiRate, apiBurst)
client.SetAdaptiveParams(0, probeAfter, maxBackoff)
```

**Значения из конфига**, не хардкод. Swagger rate — для каждого endpoint индивидуально (не один на всю API family).

### 8. Rule of Testing with Mocks

Тесты в `pkg/<domain>/` используют моки, а не реальный SQLite или API.

**Минимум 4 теста:**
1. Basic download (save + count)
2. DryRun (no writes)
3. Rewrite (delete before save) / Limit
4. Context cancellation

### 9. Rule of No Dead Code

Нет неиспользуемым типам, интерфейсам, функциям. Если определили `Source` — wire его. Если не используете — удалите.

### 10. Rule of Constants, not Hardcodes

API URL, default timeout, max days per period — константы пакета.

```go
const statsAPIURL = "https://statistics-api.wildberries.ru"
```

### 11. Rule of Client Method First

**Каждый новый WB API endpoint обязан иметь typed method в `pkg/wb/`.** WBSource в domain package — только thin delegation + конвертация WB types → domain types.

**Запрещено:** вызывать `client.Post()` / `client.Get()` напрямую из WBSource.

**Почему:** «толстый» WBSource, вызывающий `client.Post()` напрямую:
- не проходит через demo key check → `--mock` mode ломается
- дублирует URL/path/constants, уже существующие в клиенте
- скрывает rate limit wiring — баг с `0, 0` обнаруживается только на проде

```go
// ❌ Wrong — thick WBSource calls client.Post() directly
type WBSource struct { client *wb.Client }
func (s *WBSource) FetchPositions(ctx, req) ([]Row, error) {
    var resp map[string]any
    s.client.Post(ctx, "search_report", baseURL, 0, 0, path, body, &resp)  // ← rate limit bug
    return parseResponse(resp), nil
}

// ✅ Right — thin WBSource delegates to typed client method
type WBSource struct { client *wb.Client; rateLimit, burst int }
func (s *WBSource) FetchPositions(ctx, req) ([]Row, error) {
    resp, err := s.client.GetSearchReport(ctx, nmIDs, begin, end, pastBegin, pastEnd, s.rateLimit, s.burst)
    return parseResponse(resp.Data), nil  // ← domain type conversion only
}
```

**Паттерн для нового endpoint'а:**
1. Создать typed method в `pkg/wb/<domain>.go` (response struct + `func (c *Client) Get...()`)
2. Добавить mock в том же файле (`if c.IsDemoKey() { return c.getMock...() }`)
3. WBSource делегирует, добавляя только domain-специфичную конвертацию

---

## Структура Downloader (каноническая)

```go
type Downloader struct {
    source Source   // интерфейс, не *wb.Client
    writer Writer   // интерфейс, не *sqlite.SQLiteSalesRepository
    opts   Options
}

func NewDownloader(source Source, writer Writer, opts Options) *Downloader
func (d *Downloader) Run(ctx context.Context, ranges []wb.DateRange, resume, rewrite bool) (*Result, error)
```

---

## Антипаттерны

Опыт реальных багов из v1/v2 разработки. Каждый — concise: что сломалось → как правильно → как предотвратить.

### 1. SQL Placeholder Mismatch

Mock-тесты НЕ ловят — `DiscardWriter` не выполняет SQL.

❌ **Wrong** — 28 колонок, но 26 `?`:
```sql
INSERT OR REPLACE INTO orders (srid, order_date, ...,  -- 28 columns
) VALUES (?,?,?,?,...,?)  -- только 26 '?' + CURRENT_TIMESTAMP = 27 ≠ 28
```

✅ **Correct** — сверить count:
```bash
# Быстрая проверка: число '?' в VALUES = число колонок минус SQL-функции
sed -n '/VALUES/,/)/p' pkg/storage/sqlite/<domain>_repo.go | tr -cd '?' | wc -c
```

**Prevention:** после написания INSERT/UPSERT — программно сверить `strings.Count(sql, "?")` vs число колонок.

### 2. io.Reader Cannot Be Reused on Retry

❌ **Wrong** — Reader consumed on first attempt, empty on retry → `400 Bad Request: "Request body is empty"`:
```go
// doRequest() retry loop — req.body is at EOF on retry
httpReq, _ := http.NewRequestWithContext(ctx, req.method, req.url, req.body)
```

✅ **Correct** — store raw `[]byte`, recreate Reader on each retry:
```go
type httpRequest struct {
    bodyBytes []byte  // raw payload for retry
    // ...
}
// In retry loop:
bodyReader := bytes.NewReader(req.bodyBytes)  // Fresh Reader!
```

### 3. Date Off-By-One

❌ **Wrong** — `endDate = time.Now()` (today may be incomplete data).

✅ **Correct** — `endDate = time.Now().AddDate(0, 0, -1)` (yesterday). Exception: funnel uses today (partial data useful for trends).

### 4. Missing Error Continuation in Batch Loops

❌ **Wrong** — `return nil, err` stops entire download on single batch failure.

✅ **Correct** — `log error + continue`:
```go
if err != nil {
    fmt.Printf("❌ Ошибка: %v\n", err)
    continue  // Skip failed batch, process remaining
}
```

### 5. DELETE ALL Inside Batch Loop (Data Loss)

❌ **Wrong** — `DELETE FROM table` inside `SaveItems()` wipes previous batches:
```go
// Batch 2 deletes Batch 1's data!
for _, batch := range batches {
    SaveItems(ctx, batch)  // calls DELETE FROM inside
}
```

✅ **Correct** — per-item `DELETE WHERE key = ?` or `INSERT OR REPLACE` with correct UNIQUE constraint. If full snapshot replacement needed → ONE `DELETE` before the loop, not inside.

### 6. JSON Tags Not Matching Swagger (Silent Data Loss)

WB API uses **mixed naming conventions** across versions. `json.Unmarshal` silently ignores wrong fields → `Type=""`, `Value=0` → **all data lost with no error**.

| API Version | Style | Examples |
|-------------|-------|---------|
| `/adv/v0/*` | **snake_case** | `advert_id`, `nm_id` |
| `/adv/v1/*` | **camelCase** | `advertId`, `campName` |

✅ **Prevention:** copy-paste field names from `docs/wb_api_swagger/`. Add JSON unmarshal test:
```go
func TestParsing(t *testing.T) {
    raw := `{"nm_id": 123, "placement": "search", "bid": 250}`
    var resp MinBidsResponse
    json.Unmarshal([]byte(raw), &resp)
    if resp.Bids[0].Placement != "search" { t.Fatal("JSON tags don't match Swagger") }
}
```

### 7. Shared Rate Limits for Different Swagger Limits

Endpoints within the same API family can have drastically different limits.

❌ **Wrong** — stats uses 300/min like list/bids (but Swagger says **10 req/min** for stats):
```go
SetRateLimit("normquery_stats", rl.Normquery, ...)  // 300/min!
```

✅ **Correct** — separate config for each distinct Swagger limit:
```yaml
rate_limits:
  normquery: 300           # list/bids/minus
  normquery_stats: 10      # stats: 30x stricter!
```

### 8. Database Grain Mismatch with API Granularity

❌ **Wrong** — `UNIQUE(advert_id, nm_id)` but API returns multiple items per (advert_id, nm_id) with different `norm_query` → UNIQUE violation or data overwritten.

✅ **Correct** — match UNIQUE to all varying fields: `UNIQUE(advert_id, nm_id, normquery)`.

### 9. doRequest() Bypass → 429 Flood

`*Page()` methods calling `c.httpClient.Do()` directly bypass two-level rate limiting. With `burst=10`, token bucket allows rapid fire → 429 on every page after the first.

❌ **Wrong** — only token bucket, no min-interval check:
```go
func (c *Client) SomePage(...) {
    limiter.Wait(ctx)  // ← only token bucket
    resp, _ := c.httpClient.Do(httpReq)  // ← bypasses doRequest() guards
}
```

✅ **Correct** — use `client.Get()` / `client.Post()`, or replicate all three guardrails:
```go
func (c *Client) SomePage(...) {
    limiter.Wait(ctx)
    // 1. Min-interval check
    if elapsed := time.Since(c.lastRequestTime[toolID]); elapsed < minInterval {
        time.Sleep(minInterval - elapsed)
    }
    resp, _ := c.httpClient.Do(httpReq)
    // 2. Update timing
    c.adaptiveRecoverOK(toolID)
    c.lastRequestTime[toolID] = time.Now()
}
```

### 10. resolveAPIKey Returns Env Var Name, Not Value

❌ **Wrong** — returns `"WB_API_CONTENT_KEY"` as the key:
```go
return cfg.Cards.APIKeyEnv  // "WB_API_CONTENT_KEY" — string, not value!
```

✅ **Correct:**
```go
if cfg.WB.APIKey != "" { return cfg.WB.APIKey }
return os.Getenv(cfg.Cards.APIKeyEnv)  // os.Getenv("WB_API_CONTENT_KEY")
```

### 11. Two Lines Per Page → dllog.Progress()

❌ **Wrong:**
```
[Page 1] Fetching cards...
✅ 100 cards saved (1.615s)
```

✅ **Correct** — one line:
```
16:50:14 [1] cards: 100 cards saved (0s)
```

Use `dllog.Progress()` — one call per page.

### 12. Type Name Collision

❌ **Wrong** — `StorageConfig` already exists in `pkg/config/config.go`:
```go
type StorageConfig struct { ... }  // compilation error!
```

✅ **Correct** — unique name: `V2StorageConfig`.

### 13. Dead YAML Field (Config ↔ Struct Mismatch)

`encoding/yaml` silently ignores fields present in YAML but missing from Go struct. Config looks correct, value never reaches code.

❌ **Wrong** — YAML has `api_key_env`, struct doesn't:
```yaml
# config.yaml
wb:
  api_key_env: "WB_STAT"    # ← silently ignored!
```
```go
type WBClientConfig struct {
    APIKey string `yaml:"api_key"`   // no APIKeyEnv field
}
```

✅ **Correct** — struct matches YAML 1:1:
```go
type WBClientConfig struct {
    APIKey    string `yaml:"api_key"`      // direct key
    APIKeyEnv string `yaml:"api_key_env"`  // env var name
}
```

**Prevention:** при добавлении поля в YAML — сразу добавить в struct. При добавлении в struct — сразу добавить в config.yaml с комментарием.

---

## Чеклист: создание утилиты v2

### pkg/<domain>/ (ядро)
- [ ] `types.go` — `Source` + `Writer` интерфейсы, `Options`, `Result`
- [ ] Writer содержит **только** методы, вызываемые в `Run()` — не больше
- [ ] Нет cursor/resume методов, если домен "лёгкий" (<100k записей, <10 мин)
- [ ] `downloader.go` — `Downloader` struct + `Run()`, все поля — интерфейсы
- [ ] `mock.go` — `MockSource` + **DiscardWriter** (no-op, counts only)
- [ ] `downloader_test.go` — ≥4 кейса (Basic, DryRun, Limit/Rewrite, ContextCancel)
- [ ] Нет `fmt.Printf` / `log.*` (только через `OnProgress`)
- [ ] Нет `*sqlite.*` / `*wb.Client` в полях struct (только интерфейсы)
- [ ] Constants для URL и дефолтов, не хардкод

### cmd/<family>/<name>/ (драйвер)
- [ ] `main.go` ~100 строк: flags → config → DI → Run
- [ ] `config.yaml` — полный, прокомментированный (каждый параметр с комментарием)
- [ ] `README.md` — mandatory (usage, config, schema, safe examples)
- [ ] Флаги: `--config`, `--mock`, `--dry-run`, `--days`, `--begin`, `--end`
- [ ] `--mock` использует DiscardWriter, НЕ создаёт реальный DB writer
- [ ] `SetRateLimit()` вызывается явно после `wb.New()`
- [ ] `resolveAPIKey()` использует `os.Getenv()`, не возвращает имя переменной
- [ ] `dllog.PrintHeader()` / `dllog.Progress()` / `dllog.Done()`
- [ ] Ctrl+C через `signal.NotifyContext()`
- [ ] **ВСЕГДА** явный `--db /tmp/test-<domain>.db` в тестовых командах

### pkg/storage/sqlite/ (адаптер)
- [ ] Compile-time assertion: `var _ domain.Writer = (*SQLiteSalesRepository)(nil)`

### Verification
- [ ] `go test ./pkg/<domain>/ -v` — все PASS (in-memory mock writers, no DB)
- [ ] **Placeholder count check:** число `?` в VALUES = число колонок минус SQL-функции
- [ ] `go run ./cmd/.../ --mock` — DiscardWriter, **no DB file created**
- [ ] `go run ./cmd/.../ --mock --db /tmp/test.db --backend=sqlite`

---

## Карта миграции: v1 → v2

| Шаг | Действие | Проверка |
|-----|----------|----------|
| 1 | Создать `pkg/<domain>/types.go` — Source, Writer, Options, Result | `go build ./pkg/<domain>/` |
| 2 | Создать `pkg/<domain>/downloader.go` — Downloader + Run() | Интерфейсы wired, нет concrete types |
| 3 | Создать `pkg/<domain>/mock.go` — MockSource + DiscardWriter | `go build` + `go test` |
| 4 | Создать `pkg/<domain>/downloader_test.go` | `go test ./pkg/<domain>/ -v` → все PASS |
| 5 | Создать тонкий `cmd/.../<name>-v2/main.go` | `go build ./cmd/.../<name>-v2/` |
| 6 | Compile-time assertion в `pkg/storage/sqlite/` | `var _ domain.Writer = (*SQLiteSalesRepository)(nil)` |
| 7 | (опционально) Tool wrapper в `pkg/tools/std/` | Tool interface: `Definition() + Execute()` |

**v1 остаётся нетронутым** пока v2 не пройдёт ручное тестирование на реальных данных.
