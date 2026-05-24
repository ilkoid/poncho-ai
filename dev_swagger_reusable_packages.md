# dev_swagger_reusable_packages.md — Переиспользуемые пакеты WB API строго по Swagger (руководство v2+)

**Дата**: 2026-05-25  
**Статус**: Актуальное руководство  
**Связанные документы**:
- [dev_utils.md](dev_utils.md) — v2-архитектура утилит (Source/Writer, Downloader в pkg/, тонкие драйверы)
- [CLAUDE.md](CLAUDE.md) — WB API Swagger Docs, safety rules, sandbox
- [dev_manifest.md](dev_manifest.md) — Port & Adapter (Rule 6), pkg/ vs cmd/
- https://github.com/anomalyco/opencode/issues — для фидбека по подходу

---

## Принцип

**Единственный источник правды — файлы `docs/wb_api_swagger/*.yaml`** (и ваша таблица песочниц).

- Весь код пакетов, типов, rate-limit, URL, sandbox-констант и даже тестовых сценариев **выводится только из Swagger + x-расширений** (x-readonly-method, x-category и др.).
- Никакого codegen / oapi-codegen: **100 % hand-written** (по вашему решению). Методы и типы пишутся вручную, но строго по спецификации (1:1), с комментарием-ссылкой на файл/тег/path.
- Один и тот же код переиспользуется:
  - в утилитах (`cmd/.../download-*` и `fix-*` через тонкий драйвер)
  - в agentic tools (`pkg/tools/std/*.go` — тонкие `Tool.Execute` обёртки)
- Архитектура повторяет победивший паттерн `dev_utils.md` (pkg/<domain>/ + Source + Writer + Downloader).

**Ключевое правило безопасности**:
> **НИКОГДА** автоматические POST/PUT/DELETE/обновления на боевом продакшен-API WB из агентов, тулов или тестов.  
> Только песочницы (см. таблицу ниже) + ручной запуск + dry-run.

---

## Таблица песочниц WB (авторитетная)

Раздел / Ограничения / Sandbox URL (все методы в песочнице — **максимум 1 req/s суммарно**, если не указано иное):

| Раздел                              | Ключевые ограничения в sandbox                                                                 | Sandbox URL                              |
|-------------------------------------|------------------------------------------------------------------------------------------------|------------------------------------------|
| Категории, предметы, характеристики<br>Карточки, медиа, ярлыки | Нет locale; карточка создаётся сразу; нет allowedCategoriesOnly; 1 req/s | `https://content-api-sandbox.wildberries.ru` |
| Цены и скидки<br>Календарь акций    | Можно править цены после создания карточки в контент-песочнице; 1 req/s                      | `https://discounts-prices-api-sandbox.wildberries.ru` |
| Сборочные задания (FBS/DBS/Самовывоз)<br>Метаданные, поставки, склады, остатки | Есть тестовые заказы; стикеры/статусы кроссбордера — пусто; 1 req/s                           | `https://marketplace-api-sandbox.wildberries.ru` |
| Поставки FBW                        | Можно использовать баркод из контент-песочницы; 1 req/s per method                             | `https://supplies-api-sandbox.wildberries.ru` |
| Кампании, создание/управление, финансы, параметры, статистика | Пополнение тестового баланса; статистика только за 30 дней, статус 9, тип 8/9, 9 раз/сутки   | `https://advert-api-sandbox.wildberries.ru` |
| Вопросы и отзывы                    | Тестовые Q/A создаются, удаляются через 5 дней с последней правки                              | `https://feedbacks-api-sandbox.wildberries.ru` |
| Основные + финансовые отчёты        | dateFrom/dateTo — максимум 4 месяца назад                                                      | `https://statistics-api-sandbox.wildberries.ru` |

---

## Слои архитектуры (от Swagger к Tool/CLI)

```
docs/wb_api_swagger/02-products.yaml …          ← единственный источник
       ↓ (ручное отображение + x-расширения)

pkg/wb/                                         ← транспорт + hand-written SDK
  ├── client.go          (doRequest, rate, adaptive, unwrap {data,error})
  ├── content.go         // POST /content/v2/get/cards/list (из 02-products.yaml)
  ├── content_sandbox.go // const CardsSandboxURL = "https://content-api-sandbox..."
  └── ...
       ↓ (structural typing — *wb.Client удовлетворяет интерфейсам)

pkg/<domain>/                                   ← бизнес-ядро (v2 по dev_utils.md)
  ├── types.go           // XXXSource (минимальный), XXXWriter, Options, Result
  ├── downloader.go      // NewDownloader + Run() — только интерфейсы
  ├── mock.go            // MockSource для --mock и unit-тестов
  └── downloader_test.go // ≥4 обязательных теста

pkg/storage/sqlite/     ← адаптер (compile-time var _ domain.Writer = (*Repo)(nil))

cmd/.../download-xxx-v2/main.go   ← тонкий драйвер (~100 строк: флаги → DI → Run)
pkg/tools/std/wb_xxx_tool.go      ← тонкая Tool-обёртка (Definition + Execute)
```

**Почему именно так (преимущества для переиспользования + тестируемости)**:
- Source/Writer порты объявляются в потребителе (`pkg/<domain>/`) — Port&Adapter.
- *wb.Client* «магически» реализует Source без прослойки (позволяет мок подменять напрямую).
- Tools получают тот же `Downloader`, но с `OnProgress=nil` + readonly Source по умолчанию.
- Драйверы в cmd/ и tools полностью независимы от деталей HTTP/Swagger.

---

## Правила создания пакета (строго по Swagger)

1. **Rule of Swagger Fidelity**  
   Каждый метод/тип/константа/URL — с комментарием:
   ```go
   // GetCardsList — POST /content/v2/get/cards/list (02-products.yaml, tag: Карточки товаров)
   // Rate: 100/min, burst 5 (swagger)
   func (c *Client) GetCardsList(...)
   ```
   Новые endpoint появляются **только** после обновления yaml + PR с ревью.

2. **Rule of ReadOnly by Default (для Tools & Agents)**  
   - Создавайте узкие интерфейсы `ContentReadonlySource`, `MarketplaceReadonlySource` и т.д.
   - В `pkg/tools` и agent-регистрации используйте **только readonly**-варианты (даже если под капотом полный клиент).
   - Полные мутаторы (`UpdateCards`, `CreateCards`...) — только в `cmd/fix-*`, `pkg/cardupdate` (с dry-run) или ручных скриптах.
   - Никогда не регистрируйте мутаторы в обычном `tool_categories` агента.

3. **Rule of Generalized Sandbox** (расширение CardsSandboxURL)  
   В `pkg/wb/` заводите пару констант для **каждого** домена из таблицы:
   ```go
   const ContentProdURL    = "https://content-api.wildberries.ru"
   const ContentSandboxURL = "https://content-api-sandbox.wildberries.ru"
   // аналогично для discounts-prices, marketplace, advert, feedbacks, statistics, supplies…
   ```
   Документируйте в коде и в CLAUDE.md ограничения песочницы (1 req/s, отсутствие locale и т.д.).

4. **Rule of Rate Limits from Swagger**  
   Все `SetRateLimit(toolID, desired, burst, apiFloor, apiFloorBurst)` — только из swagger + вашей таблицы.  
   `pkg/config/utility.go` остаётся единственным местом defaults (уже содержит большинство).

5. **Rule of Mandatory Test Modes & Layers**  
   Каждый новый `<domain>` пакет **обязан** поддерживать:
   - `--mock` → `MockSource`
   - `--dry-run` → пропуск Writer.Save/Delete
   - Unit-тесты с моками (минимум 4–5 кейсов)
   - E2E со `wb.SnapshotDBClient`
   - `//go:build wb_sandbox` интеграционные тесты **только против песочниц** таблицы (отдельный `*_sandbox_test.go`, никогда не запускаются в обычном `go test`)

6. **Rule of No Prod Mutations in Agents**  
   - В `pkg/tools/std` и `pkg/app/tool_setup` — по умолчанию readonly Source.
   - Мутаторы имеют явный guard: `if !allowWrites || !isSandbox { return "forbidden" }`
   - CI никогда не включает тэг `wb_sandbox`.
   - Дополнение к CLAUDE.md: "Claude MUST NOT generate calls that hit write endpoints on prod".

7. **Rule of No Dead Code & Minimal Interfaces**  
   (как в dev_utils) — только реально используемые методы в Source.

8. **Rule of Documentation**  
   При добавлении нового домена обновить:
   - этот файл (добавить в таблицу/примеры)
   - CLAUDE.md (sandbox + safety)
   - `pkg/wb/README` или godoc
   - AGENTS.md (если нужно)

---

## Структура примера нового package (на основе 02-products.yaml, readonly)

```go
// pkg/catalog/types.go
type CatalogSource interface {
    // GetParentCategories — GET /content/v2/object/parent/all (02-products.yaml)
    GetParentCategories(ctx, baseURL string, rate, burst int) ([]ParentCategory, error)
    // GetCardsListCursor — POST /content/v2/get/cards/list (cursor pagination)
    GetCardsListCursor(...) (cursor, cards, error)
}

type CatalogReadOnlyDownloader struct { source CatalogSource; opts ... }

func NewCatalogReadOnlyDownloader(src CatalogSource, opts) *...
```

```go
// pkg/catalog/mock.go
type MockCatalogSource struct { ... } // детерминированные данные

// pkg/catalog/downloader_test.go
func TestCatalogDownloader_*(t *testing.T) { ... } // + sandbox build tag test
```

```go
// cmd/data-downloaders/download-wb-catalog-v2/main.go (~100 строк)
client := wb.New(...)
client.SetRateLimit("catalog", ...)
src := client // или варианты с sandbox URL
dl := catalog.NewCatalogReadOnlyDownloader(src, opts)
dl.Run(ctx, ...)
```

```go
// pkg/tools/std/wb_catalog_readonly_tool.go
type WbCatalogReadOnlyTool struct { downloader *catalog.CatalogReadOnlyDownloader }
func (t *WbCatalogReadOnlyTool) Execute(ctx, argsJSON) (string, error) {
    // прогресс = nil; только readonly client
}
```

**Compile-time assertion** в sqlite (если persist) + в моках.

---

## Чеклист создания нового переиспользуемого пакета по Swagger

### 1. Обновление источника
- [ ] Проверить / добавить sandbox-URL + ограничения в `docs/...yaml` (x-расширения)
- [ ] Добавить константы `XxxSandboxURL` / `XxxProdURL` в `pkg/wb/`
- [ ] Обновить таблицу в этом файле + CLAUDE.md

### 2. Transport слой (`pkg/wb/`)
- [ ] Новые методы с точными ссылками на swagger (только readonly сначала)
- [ ] Обработка песочничных особенностей (комментарий)
- [ ] Rate limits в двух уровнях (desired + api floor)

### 3. Ядро пакета `pkg/<domain>/` (обязательно!)
- [ ] `types.go` — минимальные интерфейсы Source (readonly) + Writer
- [ ] `downloader.go` / `job.go` — чистые интерфейсы в полях
- [ ] `mock.go` + детерминированные генераторы (или переиспользовать `pkg/testing/wbmock`)
- [ ] `downloader_test.go` ≥ 4–5 кейсов (basic, dry, resume, rewrite, cancel)
- [ ] Нет `fmt.Print`, нет прямых `*wb.Client` в полях, нет хардкода URL
- [ ] build-tag `_sandbox_test.go` (только против песочниц)

### 4. Адаптеры и драйверы
- [ ] Compile-time assertions в `pkg/storage/sqlite`
- [ ] Тонкий `cmd/...-v2/main.go` + `config.yaml`
- [ ] Тонкая Tool-обёртка в `pkg/tools/std/` (регистрация в tool_categories через config)
- [ ] `--mock` / `--dry-run` / `--sandbox` флаги работают

### 5. Тестирование и безопасность
- [ ] `go test ./pkg/<domain>/ -v` — все проходят
- [ ] `go test -tags=wb_sandbox ./pkg/<domain>/...` — только в ручном режиме
- [ ] Dry-run + mock покрывают 100% путей мутации (если есть)
- [ ] Запуск утилиты в `--dry-run` и `--mock` без ключей и без сети
- [ ] Ревью: "никаких путей из pkg/tools в мутаторы на прод"

### 6. Документация
- [ ] Обновить этот файл (пример + ссылка)
- [ ] CLAUDE.md (sandbox + запреты)
- [ ] AGENTS.md (при необходимости)
- [ ] README godoc в пакете

---

## Эволюция и миграция (как в dev_utils: v1 → v2)

| Шаг | Что делаем                             | Проверка                              |
|-----|----------------------------------------|---------------------------------------|
| 1   | Добавить sandbox-константы + readonly-методы в pkg/wb по yaml | `go build`, grep по файлу swagger |
| 2   | Создать `pkg/<domain>/` (типы/мок/тесты) | `go test ./pkg/<domain>/ -v` |
| 3   | Перевести read-only часть одного даунлоадера на v2 | `--mock` + `--dry-run` работают |
| 4   | Tool-обёртка + регистрация в config | `go run cmd/simple-agent ... "покажи категории"` |
| 5   | Sandbox-тесты (wb_sandbox)             | Только вручную, на тест-ключе |
| 6   | (позже) Тонкая миграция старых cmd     | v1 остаётся нетронутым до ручного прогона |

**Мутации (карточки, кампании, ответы на отзывы)** переводятся **только после** полноценного покрытия песочницей + dry-run в инструментах.

---

## Часто задаваемые проверки (чек для ревью PR)

- `grep -r "fmt\.Print\|log\." pkg/<domain>/` → 0 (кроме helper)
- Нет импортов `pkg/<domain>` из `internal/`
- Интерфейс Source содержит **только** методы, реально вызываемые в Run()
- Tool использует readonly-вариант
- Тесты sandbox имеют build-tag и никогда не выполняются в обычном CI
- Все URL/rate из swagger (прокомментировано)
- Пример запуска песочницы задокументирован в `_test.go` или README пакета

---

**Этот подход гарантирует**:
- Полное покрытие Swagger → код (исключает drift)
- Одно ядро для CLI + агентов
- 5-уровневое тестирование (unit / mock / replay / e2e-snapshot / реальная песочница)
- Абсолютную невозможность случайной поломки прода через агента

Пишите новые пакеты **только** следуя этому файлу и `dev_utils.md`.

При возникновении вопросов / улучшений — создавайте issue.
