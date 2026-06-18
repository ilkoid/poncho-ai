# dev_v3_scrub.md — V3 Downloader Guide (превентивный scrub на этапе загрузки)

> **Связанные документы:**
> - [dev_manifest.md](dev_manifest.md) — общие архитектурные правила (Rule 6, Rule 13)
> - [dev_v2_downloader.md](dev_v2_downloader.md) — создание v2 даунлоадера (Source/Writer). **v3 = v2 + scrub-декоратор.**
> - [dev_v2_postgres.md](dev_v2_postgres.md) — PostgreSQL адаптер (dual-backend)
>
> **Приоритет:** доменный — для v3-scrub побеждает `dev_v2_downloader.md`. Если конфликт — этот документ описывает scrub-надстройку, остальная архитектура наследуется из v2.

---

## 0. TL;DR

**v3 = v2 + scrub-декоратор Writer.** Поколение загрузчиков, которое режет чувствительные подстроки (бренды и т.п.) **на этапе загрузки**, до записи в БД.

- **Не трогает** v2-утилиты и `pkg/<domain>/` — они замораживаются (`git diff` пуст).
- Scrub вынесен в **декоратор Writer**, живущий внутри нового cmd `download-wb-<domain>-v3/`.
- Общая логика — в изолированном [pkg/scrub/](pkg/scrub/), правила — в [scrub-rules.yaml](cmd/.configs/download-all/scrub-rules.yaml), включение — через `storage.scrub_rules_path`.
- **Комплементарно** `cmd/fix-utilities/fix-scrub-substring` (постфактум SQL): v3 кроет новые записи, SQL-утиль — исторические.

Канонический эталон-реализация: [download-wb-cards-v3/](cmd/data-downloaders/download-wb-cards-v3/).

---

## 1. Контекст — зачем отдельное поколение

`fix-scrub-substring` работает **постфактум**: гоняет `regexp_replace(col,$1,$2,'gi')` по текстовым/json колонкам уже лежащих в БД данных. Это догоняющий инструмент — данные уже попали в базу.

**Превентивный скраб** режет подстроку прямо при загрузке, до персиста. Зачем именно **новое поколение v3**, а не правка v2:

| Свойство | In-place в v2 | v3 (отдельное поколение) |
|---|---|---|
| Риск рабочему пайплайну | трогает рабочую утилиту | нулевой (v2 заморожен) |
| A/B сравнение | невозможно | v2 vs v3 рядом |
| Откат | откат кода | просто не запускать v3 |
| Provenance | «данные поскраблены?» — непонятно | v3 = «скраб был» |
| Blast radius | общая библиотека | изоляция в новом cmd |

**Вывод архитектурного аудита:** единой точки-chokepoint в потоке данных v2 **нет** (ISP: каждый домен имеет свой `Downloader.Run()` и свой `Writer`). Общая точка только на уровне **конфига** (`V2StorageConfig`). `postgres.NewPool()` общий, но это уровень сокета — перехват там = хрупкая магия над бинарным протоколом pgx (см. антипаттерн §7). Поэтому scrub реализован как **декоратор Writer** — единственный способ добавить скраб, не трогая ни v2, ни общую `pkg/<domain>`.

---

## 2. Архитектура v3

```
                ┌─────────────────────────────────────────────────────┐
                │  download-wb-<domain>-v3/  (новый cmd, ~v2 main.go)  │
                │                                                     │
  WB API ──► Source ──► pkg/<domain>.Downloader ──► scrubXxxWriter ──► Repo ──► БД
                │                                  (декоратор)   (pg/sqlite)
                │                                       │
                │                              pkg/scrub.Replacer
                └──────────────────────────────────────────────┬──────┘
                                                                 │
   scrub-rules.yaml  ◄── scrub_rules_path (V2StorageConfig) ◄────┘
```

**Компоненты:**

| Компонент | Где | Роль |
|---|---|---|
| `pkg/scrub/` | [pkg/scrub/](pkg/scrub/) | Общая логика: компиляция правил, reflection-обход, `ApplySlice`. Без доменных зависимостей. |
| `Replacer` | [pkg/scrub/replacer.go](pkg/scrub/replacer.go) | `Load(path)`, `ApplyString`, `ApplySlice`. Compile-once, thread-safe. |
| `scrub-rules.yaml` | [cmd/.configs/download-all/scrub-rules.yaml](cmd/.configs/download-all/scrub-rules.yaml) | Правила find→replace (общие для всех v3-утилит). |
| `V2StorageConfig.ScrubRulesPath` | [pkg/config/pgconfig.go](pkg/config/pgconfig.go) | Поле конфига (аддитивное, empty=no-op). Единственная правка общей инфры. |
| Декоратор Writer | `download-wb-<domain>-v3/scrub.go` | Реализует интерфейс Writer домена: скрабит батч → делегирует. |
| v3 cmd | `download-wb-<domain>-v3/main.go` | Копия v2 main.go + загрузка scrubber + обёртка writer. |

**Поток данных** идентичен v2, кроме одной точки: writer оборачивается декоратором перед передачей в `NewDownloader`. Downloader ничего не знает про scrub — он видит обычный `cards.CardsWriter`.

---

## 3. Паттерн миграции (пошагово)

Канон — cards. Для другого домена подставь свой интерфейс Writer (см. референс §5).

### Шаг 1. Создать `download-wb-<domain>-v3/` и скопировать main.go

```bash
cp -r cmd/data-downloaders/download-wb-cards-v2 cmd/data-downloaders/download-wb-cards-v3
```
Отредактируй package-doc и баннер (`v2` → `v3`, добавь упоминание scrub). Остальное (флаги, конфиг, source/writer, Run) — без изменений.

### Шаг 2. Декоратор `scrub.go`

Эталон: [download-wb-cards-v3/scrub.go](cmd/data-downloaders/download-wb-cards-v3/scrub.go).

```go
package main

import (
	"context"

	"github.com/ilkoid/poncho-ai/pkg/cards"
	"github.com/ilkoid/poncho-ai/pkg/scrub"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Compile-time assertion: декоратор удовлетворяет интерфейсу Writer домена.
// Если в pkg/<domain> интерфейс изменится — v3 перестанет собираться (явный сигнал).
var _ cards.CardsWriter = (*scrubCardsWriter)(nil)

// scrubCardsWriter режет подстроки в полях карт ДО делегирования в нижележащий writer.
// pkg/cards остаётся нетронутым — Downloader принимает любой cards.CardsWriter.
type scrubCardsWriter struct {
	inner cards.CardsWriter
	r     *scrub.Replacer
}

func newScrubCardsWriter(inner cards.CardsWriter, r *scrub.Replacer) *scrubCardsWriter {
	return &scrubCardsWriter{inner: inner, r: r}
}

// Data-batch метод: скраб → делегирование.
func (w *scrubCardsWriter) SaveCards(ctx context.Context, cs []wb.ProductCard) (int, error) {
	w.r.ApplySlice(cs)
	return w.inner.SaveCards(ctx, cs)
}

// Housekeeping метод: делегирование без изменений.
func (w *scrubCardsWriter) CountCards(ctx context.Context) (int, error) {
	return w.inner.CountCards(ctx)
}
```

**Правило декоратора:** для **каждого** data-batch метода (того, что принимает `[]T`) — `w.r.ApplySlice(slice)` затем `w.inner.<method>(...)`. Для **housekeeping** методов (`Count*`, `Delete*`, `Get*`, `Exists`, schema-init) — просто делегировать.

### Шаг 3. Wiring в `main.go`

После загрузки конфига — загрузить scrubber; после создания writer — обернуть.

```go
// импорт: "github.com/ilkoid/poncho-ai/pkg/scrub"

var scrubber *scrub.Replacer
if cfg.Storage.ScrubRulesPath != "" {
	scrubber, err = scrub.Load(cfg.Storage.ScrubRulesPath)
	if err != nil {
		log.Fatalf("scrub rules: %v", err)
	}
	dllog.Log("scrub: %s (%d rules)", cfg.Storage.ScrubRulesPath, scrubber.Len())
}

// ... создание source + writer (как в v2) ...

// Обернуть writer декоратором (работает и для mock, и для реального writer).
if scrubber != nil {
	writer = newScrubCardsWriter(writer, scrubber)
}
defer cleanup()
```

### Шаг 4. v3-конфиги со `scrub_rules_path`

Эталоны: [download-wb-cards-v3.yaml](cmd/.configs/download-all/download-wb-cards-v3.yaml) + [-PG.yaml](cmd/.configs/download-all/download-wb-cards-v3-PG.yaml).

```yaml
storage:
  backend: "postgres"
  pg_database: "wb_data_prod"
  pg_password_env: "PG_PWD"
  scrub_rules_path: "cmd/.configs/download-all/scrub-rules.yaml"  # пусто = выкл
```

### Шаг 5. v2 и `pkg/<domain>` НЕ трогать

После миграции `git diff pkg/<domain>/ cmd/data-downloaders/download-wb-cards-v2/` должен быть **пуст**. Это критерий заморозки — v2 байт-идентичен рабочей версии.

### Шаг 6. Верификация — см. §10.

---

## 4. Почему декоратор (а не поле в `pkg/<domain>`)

В сессии был опробован альтернативный подход **α — opt-in поле `Scrubber` в `DownloadOptions`** общего `pkg/<domain>` + одна строка в цикле `Run()`. Он работоспособен и DRY-ше, но **отвергнут** в пользу **β — декоратора**:

| | α (поле в pkg) | β (декоратор) ← выбран |
|---|---|---|
| Трогает `pkg/<domain>` | да (additive поле + строка) | **нет** |
| Трогает v2 cmd | нет | нет |
| Source-агностик | нет (поле в цикле страниц; для iterator — в callback) | **да** (scrub на границе `Save`) |
| Покрытие iterator-доменов | нужно 2 точки вставки | **1 точка** (Writer) |
| Дублирование | одно поле + одна строка/домен | один декоратор (~15 строк)/домен |

**Решающий аргумент β:** scrub стоит на границе `Writer.Save`, а значит **не зависит** от того, как данные пришли — paged (`GetCardsPage`) или streaming (`OrdersIterator(callback)`). Пока callback маршрутизирует батч через `Save`, декоратор кроет оба стиля. Для α пришлось бы вставлять скраб и в цикл страниц, и в тело callback'а.

---

## 5. Референс: Writer-интерфейсы всех v2-доменов

Декоратор реализует интерфейс **конкретного домена**. Имена и методы разные. Сигнатуры проверены по `pkg/<domain>/types.go`.

| Домен | Интерфейс | Файл | Data-batch методы (скрабить) | Housekeeping (делегировать) | Стиль |
|---|---|---|---|---|---|
| cards | `CardsWriter` | [pkg/cards/types.go](pkg/cards/types.go) | `SaveCards([]wb.ProductCard)` | `CountCards` | paged |
| feedbacks | `Writer` | [pkg/feedbacks/types.go](pkg/feedbacks/types.go) | `SaveFeedbacks([]wb.FeedbackFull)`, `SaveQuestions([]wb.QuestionFull)` | `CountFeedbacks`, `CountQuestions` | paged |
| onec | `Writer` | [pkg/onec/types.go](pkg/onec/types.go) | `SaveGoods`, `SaveSKUs`, `SaveDimensions`, `SaveOneCPrices([]PriceRow,snapshotDate)`, `SavePIMGoods` | `CleanAll` | paged |
| campaigns | `CampaignsWriter` | [pkg/campaigns/types.go](pkg/campaigns/types.go) | `SaveCampaigns`, `SaveCampaignDetails`, `SaveFullstats(wb.FlattenAllResult)` ⚠️ struct, не слайс | `GetLastCampaignStatsDateAll`, `PopulateCampaignProducts` | paged |
| searchvis | `Writer` | [pkg/searchvis/types.go](pkg/searchvis/types.go) | `SaveSearchPositions`, `SaveSearchQueries` | `CountSearchPositions`, `CountSearchQueries` | paged |
| orders | `OrdersWriter` | [pkg/orders/types.go](pkg/orders/types.go) | `SaveOrders([]wb.OrdersItem)` | `DeleteOrdersOlderThan` | **iterator** |
| opsales | `OpsalesWriter` | [pkg/opsales/types.go](pkg/opsales/types.go) | `SaveSales([]wb.SalesItem)` | `DeleteSalesOlderThan` | **iterator** |
| sales | `SalesWriter` | [pkg/sales/types.go](pkg/sales/types.go) | `Save([]wb.RealizationReportRow)`, `SaveServiceRecords` | `GetLastSaleDT`, `GetFirstSaleDT`, `DeleteSalesByDateRange`, `Exists` | **iterator** |
| prices | `PricesWriter` | [pkg/prices/types.go](pkg/prices/types.go) | `SavePrices([]wb.ProductPrice,snapshotDate)` | `CountPrices` | paged |
| stocks | `StocksWriter` | [pkg/stocks/types.go](pkg/stocks/types.go) | `SaveStocks(snapshotDate,[]wb.StockWarehouseItem)` | `CountStocks`, `CountStocksForDate`, `GetDistinctSnapshotDates` | paged |
| funnel | `FunnelWriter` | [pkg/funnel/types.go](pkg/funnel/types.go) | `SaveFunnelHistoryWithWindow(meta,[]rows,refreshDays)` | `GetDistinctNmIDs`, `GetSupplierArticlesByNmIDs`, `FilterActiveNmIDs`, `GetRecentlyLoadedNmIDs` | paged |
| regionsales | `RegionSalesWriter` | [pkg/regionsales/types.go](pkg/regionsales/types.go) | `SaveRegionSales(dateFrom,dateTo,[]wb.RegionSaleItem)` | — | paged |
| supplies | `Writer` | [pkg/supplies/types.go](pkg/supplies/types.go) | `SaveWarehouses`, `SaveTransitTariffs`, `SaveSupplies`, `SaveSupplyGoods`, `SaveSupplyPackages` | `CountSupplies`, `CountSupplyGoods` | paged |
| nmreport | `NmReportWriter` | [pkg/nmreport/types.go](pkg/nmreport/types.go) | `SaveFunnelMetricsDetail([]rows,refreshDays)`, `SaveFunnelMetricsGrouped` | `GetNmReport`, `SaveNmReport`, `UpdateNmReportStatus` | paged |

### Сигнатурные подводные камни
- **`campaigns.SaveFullstats(wb.FlattenAllResult)`** — принимает **struct**, не слайс. `ApplySlice` на нём не вызвать; либо `ApplyStruct(&flat)`, либо пропустить (если в struct нет текстовых утечек). Решай по содержимому типа.
- **`prices.SavePrices` / `stocks.SaveStocks`** — таскают `snapshotDate`. Декоратор прокидывает все параметры, скрабит только слайс.
- **`funnel.SaveFunnelHistoryWithWindow`** — `(meta, []rows, refreshDays)`. Скрабить только `rows`.
- **Iterator-домены (orders/opsales/sales):** scrub на границе `Save` работает — callback вызывает `Save*` через writer. **Обязательно проверь**, что callback маршрутизирует **весь** батч через `Save*`, а не агрегирует/считает в обход Writer (см. антипаттерн §7, «iterator-ловушка»).

---

## 6. Паттерны (хорошие)

1. **Декоратор + compile-time assertion.** `var _ cards.CardsWriter = (*scrubCardsWriter)(nil)` — если интерфейс Writer изменится, v3 не соберётся. Явный сигнал, а не runtime-сюрприз.
2. **Поколенная изоляция.** `git diff pkg/<domain>/ cmd/.../<domain>-v2/` пуст = v2 байт-идентичен рабочей версии = гарантия, что поведение не сломано. Это сильнейшее доказательство заморозки.
3. **Scrub на границе Writer → source-агностик.** Неважно, paged или iterator — пока данные идут через `Save`, декоратор кроет одним кодом.
4. **Opt-in конфиг.** `scrub_rules_path` пуст = no-op. v3 без правила ведёт себя как v2. Это позволяет выкатить v3-утилиту безопасно.
5. **`json.RawMessage`-скраб для паритета.** Значения характеристик лежат в `CardCharacteristic.ValueRaw` (`json.RawMessage`, **не** string). Reflection по одним string-полям их пропустит — `pkg/scrub` режет и `string`, и `json.RawMessage` (по типу). Без этого — дыра в покрытии.
6. **Reflection-дисциплина** (см. §9): zero-boxing (`applyValue(reflect.Value)`), no-match gate (`FindStringIndex`), кэш `typePlan` по типу.
7. **`pkg/scrub` без доменных зависимостей.** Пакет импортирует только stdlib (+ `pkg/config` для `LoadYAML`). Реusable, без циклов.

---

## 7. Антипаттерны (плохие) — шрамы сессии

> Почти каждый пункт — промах, реально совершённый и исправленный в этой сессии.

1. **НЕ править v2/`pkg/<domain>` in-place.** Делали (поле в `pkg/cards` + wiring в cards-v2) — откатили через `git checkout HEAD --`. Рабочий пайплайн не должен нести экспериментальный код.
2. **НЕ скрабить на уровне `NewPool`/сокета.** Общий пул — это уровень бинарного протокола pgx. Перехват параметров там = парсинг протокола вслепую, риск угробить JSON. Хрупкая магия.
3. **НЕ добавлять поле `Scrubber` в общий `pkg/<domain>`.** См. §4 (α vs β). Декоратор чище и source-агностик.
4. **НЕ забывать `json.RawMessage`.** Reflection по string-полям пропустит значения характеристик (`ValueRaw`) — главную утечку бренда. `pkg/scrub` режет оба типа.
5. **НЕ `ReplaceAllString` → `ReplaceAllLiteralString`.** Обычный `ReplaceAllString` трактует replacement как шаблон: `$1`/`\` ломают вывод. Только `Literal`-варианты (и для string, и для `[]byte`).
6. **НЕ аллоцировать на no-match.** `ApplyString` сначала `FindStringIndex` (nil → вернуть как есть), `ReplaceAll*` — только при матче. Иначе 99% не-матчащих полей (URL, subject) аллоцируют пустые строки.
7. **НЕ рекурсить через `any`.** Рекурсивное ядро `applyValue(reflect.Value)`, не `applyValue(any)`. Иначе каждый спуск в поле = interface-boxing = heap-аллокация на поле.
8. **НЕ добавлять v3 в `download-all.sh`.** v3 — отдельная manual-утилита для поскрабленных данных. Пайплайн (`download-all.sh` → v2) не меняется.
9. **НЕ путать WB API v3 с поколением загрузчиков v3.** Все «v3» в коде кроме этого гайда — версии WB API (Seller Analytics v3, Promotion `adv/v3`, Analytics v3). Это апстрим-эндпоинты, потребляемые v2-загрузчиками.
10. **НЕ ждать scrub в `--dry-run`.** Декоратор в `Save*`, а dry-run пропускает save. Но dry-run печатает только счётчики → наблюдаемой разницы нет.
11. **Iterator-ловушка.** Для orders/opsales/sales убедись, что callback маршрутизирует **весь** батч через `Save*`. Если callback что-то агрегирует/считает в обход Writer — декоратор это пропустит.

---

## 8. Граница паритета с SQL-утильой

`fix-scrub-substring` режет **все** текстовые/json колонки `regexp_replace(...,'gi')`. v3 режет **in-memory поля** struct'ов:

| Покрытие | v3 (превентивный) | SQL-утиль (постфактум) |
|---|---|---|
| Топ-level string-поля (`Brand`, `Title`, …) | ✅ | ✅ |
| Вложенные string-поля (`Tags[].Name`, `Char.Name`) | ✅ рекурсия | ✅ |
| `json.RawMessage` (значения характеристик `ValueRaw`) | ✅ по типу | ✅ |
| Колонки, пересобираемые из не-байтовых полей (`skus_json`) | ❌ (не вектор утечки бренда) | ✅ |
| Исторические данные (уже в БД) | ❌ | ✅ |

**Семантика:** case-insensitive по умолчанию (`(?i)` + `QuoteMeta(Find)`), replacement **verbatim** (`ReplaceAllLiteralString`), идемпотентно (повторный скраб уже-скрабленных данных = no-op).

**Вывод:** инструменты **комплементарны**. v3 — новые записи; SQL-утиль — история + редкие non-parity колонки.

---

## 9. Дисциплина аллокаций (для `pkg/scrub`)

- **Zero-boxing рекурсия:** ядро `applyValue(reflect.Value)`. Публичные `ApplySlice/ApplyStruct` боксируют `reflect.ValueOf(s)` **один раз** на входе; внутри — `reflect.Value`, нулевой interface-boxing.
- **No-match gate:** `FindStringIndex`/`Find` перед `ReplaceAll*`. Не-матчащие поля не аллоцируют.
- **Пул regex-машин per-`*Regexp`:** компиляция один раз в `New`; повторные вызовы на одном `*Regexp` ~0-аллокационные после прогрева.
- **Кэш `typePlan`:** `map[reflect.Type]typePlan{stringFields, rawMsgFields, recurseFields}` под `sync.RWMutex` — не резолвить поля заново на каждой странице.
- **Итог:** ~1-2 аллокации на страницу (~100 карт) + аллокация только на реальных матчах. Шум относительно JSON-декода ответа API.
- **Fallback (YAGNI):** если профилирование покажет scrub горячим — hand-written `scrubProductCard(c *wb.ProductCard, r)` прямыми присваиваниями (нулевые аллокации, но не-generic).

---

## 10. Чек-лист верификации миграции

```bash
# 1. Сборка
go build ./pkg/scrub/... ./pkg/<domain>/... \
  ./cmd/data-downloaders/download-wb-<domain>-v2/... \
  ./cmd/data-downloaders/download-wb-<domain>-v3/...

# 2. Тесты (scrub-пакет не менялся; домен не тронут)
go test ./pkg/scrub/... ./pkg/<domain>/...

# 3. v2 заморожен (ПУСТО = ок)
git diff pkg/<domain>/ cmd/data-downloaders/download-wb-<domain>-v2/

# 4. v2 не скрабит (НЕТ строки scrub:)
go run ./cmd/data-downloaders/download-wb-<domain>-v2 \
  --config cmd/.configs/download-all/download-wb-<domain>-v2-PG.yaml --mock --dry-run --limit 10

# 5. v3 скрабит (ЕСТЬ scrub:..., без паники). БЕЗ --dry-run, чтобы Save→декоратор выполнился.
go run ./cmd/data-downloaders/download-wb-<domain>-v3 \
  --config cmd/.configs/download-all/download-wb-<domain>-v3-PG.yaml --mock --limit 20

# 6. gofmt/vet
gofmt -l pkg/scrub/ cmd/data-downloaders/download-wb-<domain>-v3/
go vet ./cmd/data-downloaders/download-wb-<domain>-v3/...
```

**Реальный прогон v3 на прод-БД — только пользователь вручную** (CLAUDE.md: Claude не пишет в `/var/db`). Отладка: `--db /tmp/test.db` или `-PG` с `wb_data_test`.

---

## 11. Приоритеты миграции

По вероятности встретить бренд в текстовых полях:

1. **feedbacks** — текст отзывов/вопросов, имена товаров. Высокий приоритет.
2. **onec** — `onec_goods.brand`, имена товаров из 1С/PIM.
3. **campaigns** — имена кампаний, поисковые запросы в продвижении.
4. **searchvis** — поисковые запросы, кластеры, позиции.
5. orders / opsales / sales — числовые, но есть `subject`/имена (средний).
6. prices / stocks / regionsales — вторичные (низкий).

**Стоимость на домен:** один декоратор (~15 строк) + копия main.go + 2 v3-конфига. `pkg/scrub` и `scrub-rules.yaml` — общие, без правок.

---

## 12. Decision log

- **α vs β** → β (декоратор). Причина: source-агностик, `pkg/<domain>` нетронут. См. §4.
- **Поколение v3 vs in-place.** Pivot после аудита blast radius: пользователь хотел заморозить рабочие утилиты → v3 как отдельное поколение (§1).
- **Scrub на границе Writer vs в цикле Downloader.** Граница Writer = source-агностик + не трогает `pkg/<domain>`.
- **`ScrubRulesPath` остался в общей `V2StorageConfig`.** Это storage-concern, поле аддитивно/dormant для v2. Альтернатива — параллельный конфиг-механизм для v3 — overengineering для одной строки.
- **`pkg/scrub` без доменных зависимостей.** Реusable, без циклов, переиспользует `config.LoadYAML`.

---

## 13. FAQ / известные ограничения

- **Ключи map не режутся.** Map-поля итерируются по значениям; ключи (часто ID) оставлены. Для pkg/wb DTO map-поля держат error-metadata, не текст.
- **Interface-поля пропускаются.** `applyValue` не спускается в `any`/`interface{}` — нельзя безопасно присвоить обратно.
- **Map-значения-структуры не мутируются.** Map values не addressable в Go; scrub применяется только к string-значениям map. Для pkg/wb DTO это допустимо.
- **`--dry-run` не скрабит.** Декоратор в `Save*`, dry-run его пропускает. Наблюдаемой разницы нет (dry-run = счётчики).
- **Декоратор не покрывает данные вне `Save*`.** Если домен пишет мимо Writer (прямые SQL, агрегаты) — scrub их не заденет. Проверяй data flow.
- **DTO без циклов.** `pkg/wb` struct'ы — JSON-decoded деревья; cycle-guard при reflection — YAGNI.
