# dev_swagger_reusable_packages.md — WB API: песочницы, write-безопасность, переиспользуемые пакеты

**Дата**: 2026-05-25
**Статус**: Целевая архитектура (To Be) — см. секцию «Текущий статус реализации».
**Связанные документы**:
- [dev_v2_downloader.md](dev_v2_downloader.md) — v2-архитектура утилит (Source/Writer, Downloader в pkg/, тонкие драйверы)
- [CLAUDE.md](CLAUDE.md) — WB API Swagger Docs, safety rules
- [dev_manifest.md](dev_manifest.md) — Port & Adapter (Rule 6), pkg/ vs cmd/

---

## Принцип

Этот документ решает одну задачу: **сделать безопасными утилиты, которые пишут в WB API**.

Read-only даунлоадеры (sales, stocks, funnel…) — низкий риск. Для их миграции достаточно [dev_v2_downloader.md](dev_v2_downloader.md).
Write-утилиты (карточки, цены, кампании, ответы на отзывы) — **высокий риск**. Ошибка в payload для `POST /content/v2/cards/update` полностью перезаписывает карточку товара. Кривая скидка в `discounts-prices` убивает маржу. Неправильная ставка в `advert` сливает бюджет.

Поэтому правила в этом документе **асимметричны**:

| | Read-only утилиты | Write-утилиты |
|---|---|---|
| Руководство | `dev_v2_downloader.md` | `dev_v2_downloader.md` + **этот документ** |
| Swagger-комментарии | Желательно | Обязательно, строгий формат |
| Sandbox-константы | Не нужны | Обязательно |
| Readonly-интерфейсы | Не нужны | Обязательно |
| `//go:build wb_sandbox` | Не нужны | Обязательно |
| Dry-run | Обязательно | Обязательно + payload diff |
| Многоуровневое тестирование | Unit + mock | Unit + mock + sandbox + e2e-snapshot |
| Guard от прод-мутаций | — | Обязательно |

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

## Текущий статус реализации

| Компонент | Статус | Что есть |
|-----------|--------|----------|
| Sandbox-константы | 1 из 6 | Только `CardsSandboxURL` в `pkg/wb/content.go` |
| Readonly-интерфейсы | Не начато | Тулы используют полный `*wb.Client` |
| Swagger-комментарии | Частично | Есть rate/endpoint, нет строгого формата `(file.yaml, tag: …)` |
| `//go:build wb_sandbox` | Не начато | Нет sandbox-тестов с build tag |
| Domain-пакеты v2 | 3 из 16 | `pkg/sales/`, `pkg/funnel/`, `pkg/nmreport/` |
| Adaptive rate limiting | Готово | Двухуровневый в `pkg/config/utility.go` |
| `--mock` / `--dry-run` | Готово | В v2-пакетах и большинстве v1-даунлоадеров |
| `pkg/cardupdate/` | Готово | Мутации карточек с dry-run |
| `wb.SnapshotDBClient` | Готово | E2E-тесты через SQLite-снапшоты |

---

## Фундамент (сделать один раз, до миграций)

Эти задачи создают инфраструктуру, которую потом используют все write-утилиты. Делаются отдельными маленькими PR'ами.

### 1. Sandbox-константы в `pkg/wb/`

Для каждого домена из таблицы песочниц — пара констант:

```go
// pkg/wb/content.go (уже есть)
const CardsSandboxURL = "https://content-api-sandbox.wildberries.ru"

// pkg/wb/advert.go (TO BE)
const AdvertProdURL    = "https://advert-api.wildberries.ru"
const AdvertSandboxURL = "https://advert-api-sandbox.wildberries.ru"

// pkg/wb/feedbacks.go (TO BE)
const FeedbacksSandboxURL = "https://feedbacks-api-sandbox.wildberries.ru"

// и т.д. для discounts-prices, marketplace, supplies, statistics
```

### 2. Readonly-интерфейсы в `pkg/wb/`

Узкие интерфейсы, содержащие **только** read-методы. Write-утилиты используют полный клиент, но tools/agents — только readonly.

```go
// pkg/wb/interfaces.go (TO BE)

// ContentReadonlySource — только чтение карточек/справочников.
// Используется в pkg/tools/std/ и pkg/app/tool_setup.go.
type ContentReadonlySource interface {
    GetCardsList(ctx context.Context, cursor, limit int) ([]Card, int, error)
    GetParentCategories(ctx context.Context) ([]ParentCategory, error)
    // ... только readonly методы
}
```

**Зачем:** compile-time гарантия, что agent/tool не может случайно вызвать `UpdateCards`. Не runtime guard, а невозможность на уровне типов.

### 3. Build tag для sandbox-тестов

```go
// pkg/<domain>/<name>_sandbox_test.go
//go:build wb_sandbox

func TestCards_UpdateSandbox(t *testing.T) {
    // Реальный HTTP-запрос в песочницу
    // Никогда не запускается в обычном go test / CI
}
```

CI не включает тэг `wb_sandbox`. Эти тесты запускаются **только вручную** на тестовом ключе.

---

## Read-only утилиты: миграция

Для даунлоадеров (sales, stocks, funnel, feedbacks, region-sales и т.д.) — **руководствуйся `dev_v2_downloader.md`**. Этот документ не добавляет требований.

Единственное, что стоит сделать попутно при миграции read-only утилиты:
- Swagger-комментарии на методы в `pkg/wb/` — желательно, но не блокирует.

---

## Write-утилиты: полный процесс

**Для кого:** `pkg/cardupdate/`, `cmd/fix-utilities/*`, будущие утилиты для цен, кампаний, ответов на отзывы.

**Почему так тяжело:** `POST /content/v2/cards/update` полностью перезаписывает карточку — частичные обновления не поддерживаются. Ошибочный payload уничтожает существующие данные. Цена ошибки — потерянные продажи, убитая маржа, слитый рекламный бюджет.

### Слои архитектуры (write-утилита)

```
docs/wb_api_swagger/02-products.yaml …          ← источник правды для payload-структур
       ↓
pkg/wb/                                         ← transport + sandbox-константы
  ├── content.go         // методы с swagger-комментариями
  │                      // const CardsSandboxURL = "..."
  └── interfaces.go      // ContentReadonlySource (для tools)
       ↓
pkg/cardupdate/                                 ← бизнес-ядро (уже существует)
  ├── types.go           // Source + Writer интерфейсы, Options, Result
  ├── cardupdate.go      // Downloader + Run() — только интерфейсы в полях
  ├── merge.go           // payload-формирование (критичный код!)
  ├── mock.go            // MockSource для --mock
  └── cardupdate_test.go // unit-тесты
       ↓
pkg/storage/sqlite/     ← адаптер (compile-time var _ cardupdate.Writer = (*Repo)(nil))
       ↓
cmd/fix-utilities/fix-*/main.go  ← тонкий драйвер (~100 строк)
pkg/tools/std/wb_*_tool.go       ← ТОЛЬКО readonly-обёртка (никаких мутаторов)
```

### Swagger-комментарии (обязательно для write-методов)

Каждый write-метод — с точной ссылкой на swagger, rate limit и documented behaviour:

```go
// UpdateCards — POST /content/v2/cards/update (02-products.yaml, tag: Карточки товаров)
// Rate: 100/min, burst 5 (swagger)
// WARNING: полностью перезаписывает карточку — частичные обновления НЕ поддерживаются.
// Dry-run: выводит payload в stdout без отправки.
func (c *Client) UpdateCards(ctx context.Context, cards []CardUpdate) error
```

### Dry-run с payload diff (обязательно для write-утилит)

Обычный dry-run пропускает `Writer.Save()`. Для write-утилит этого недостаточно — нужно показать **что именно улетит в API**:

```
$ go run cmd/fix-utilities/fix-card-fields/main.go --dry-run

[DRY-RUN] Card "Кроссовки мужские" (nm_id: 12345678)
  Материал верха: "Натуральная кожа" → "Искусственная кожа"
  Цвет: не меняется
  Payload:
    {"vendor_code": "...", "characteristics": [{"id": 22, "value": "Искусственная кожа"}]}

  Изменено полей: 1 из 8
  Карточек к обновлению: 42
  Отправка НЕ будет выполнена (--dry-run).
```

### Guard от прод-мутаций (обязательно)

Два уровня защиты:

**Уровень типов** — readonly-интерфейсы:
```go
// pkg/tools/std/ — ТОЛЬКО readonly source
type WbCatalogTool struct {
    source wb.ContentReadonlySource // не *wb.Client, не полный интерфейс
}
```

**Уровень runtime** — guard в мутаторах:
```go
// pkg/cardupdate/cardupdate.go
func (d *Downloader) Run(ctx context.Context, opts Options) (*Result, error) {
    if !opts.DryRun && !opts.SandboxMode && !opts.AllowProdWrite {
        return nil, fmt.Errorf("write operations require explicit --allow-prod-write flag")
    }
    // ...
}
```

---

## Чеклисты

### Фундамент (один раз, до write-утилит)

- [ ] Добавить sandbox-константы для оставшихся 5 доменов в `pkg/wb/`
- [ ] Создать `pkg/wb/interfaces.go` с readonly-интерфейсами
- [ ] Обновить этот файл (статус реализации) + CLAUDE.md

### Read-only миграция (по `dev_v2_downloader.md`)

- [ ] `pkg/<domain>/types.go` — Source + Writer интерфейсы
- [ ] `pkg/<domain>/downloader.go` — Downloader + Run()
- [ ] `pkg/<domain>/mock.go` — MockSource
- [ ] `pkg/<domain>/downloader_test.go` — ≥4 кейса
- [ ] Тонкий `cmd/...-v2/main.go` (~100 строк)
- [ ] Compile-time assertion в `pkg/storage/sqlite/`
- [ ] `--mock` + `--dry-run` работают

### Write-утилита (на этом документе)

**pkg/wb/ (transport):**
- [ ] Swagger-комментарии на write-методы: `(file.yaml, tag: …)` + WARNING
- [ ] Sandbox-константы для домена
- [ ] Rate limits из swagger (двухуровневые)

**pkg/<domain>/ (ядро):**
- [ ] Source + Writer интерфейсы (по dev_v2_downloader.md)
- [ ] Payload-формирование в отдельном файле (`merge.go` / `payload.go`)
- [ ] `--dry-run` с payload diff (не просто пропуск Save)
- [ ] `--sandbox` режим → sandbox URL вместо prod
- [ ] Guard: прод-мутации требуют явный `--allow-prod-write`
- [ ] Mock + unit-тесты (≥4 кейса)

**Sandbox-тесты:**
- [ ] `*_sandbox_test.go` с `//go:build wb_sandbox`
- [ ] Тест write-операции против реальной песочницы
- [ ] Тест read-after-write для верификации результата
- [ ] Не запускаются в CI — только вручную на тестовом ключе

**Tools / Agents:**
- [ ] Tool-обёртка использует **только** readonly-интерфейс
- [ ] Мутаторы не регистрируются в `tool_categories`
- [ ] `grep -r "Update\|Create\|Delete" pkg/tools/std/` → только в readonly-контексте

**Документация:**
- [ ] Обновить этот файл (статус) + CLAUDE.md

---

## Эволюция и миграция (v1 → v2)

| Шаг | Что делаем | Проверка |
|-----|-----------|----------|
| 0 | Фундамент: sandbox-константы + readonly-интерфейсы в `pkg/wb/` | `go build`, grep по swagger |
| 1 | Миграция read-only утилиты по `dev_v2_downloader.md` | `go test ./pkg/<domain>/ -v` |
| 2 | (опционально) Tool-обёртка для read-only | `go run cmd/simple-agent ...` |
| 3 | Write-утилита: ядро + payload + dry-run + sandbox-mode | `--dry-run` показывает diff |
| 4 | Sandbox-тесты (wb_sandbox) | Только вручную, на тест-ключе |
| 5 | (только после п.3-4) Prod-запуск с `--allow-prod-write` | Ручная верификация на 1 карточке |

**Порядок строгий:** write-утилита не идёт в прод без прохождения sandbox-тестов.

---

## Проверки для ревью PR

### Для read-only:
- `grep -r "fmt\.Print\|log\." pkg/<domain>/` → 0 (кроме helper)
- Нет импортов `pkg/<domain>` из `internal/`
- Source содержит только методы, реально вызываемые в `Run()`

### Для write-утилиты (дополнительно):
- `grep -r "UpdateCards\|CreateCards\|DeleteCards" pkg/tools/std/` → 0
- `--dry-run` показывает полный payload, а не просто "skipped"
- `--sandbox` переключает URL на sandbox-константу
- Нет пути запуска мутации без явного флага (`--allow-prod-write` / `--sandbox`)
- Swagger-комментарии на всех write-методах
- `*_sandbox_test.go` имеет build tag `wb_sandbox`
