# dev_data_layers.md — Слои данных PostgreSQL (raw / analytical / recommendation + action-loop)

**Дата:** 2026-07-11
**Статус:** Актуальный (north-star + конвенции)
**Приоритет:** Доменный — для вопросов организации данных по слоям и материализованных представлений **побеждает** `dev_v2_postgres.md`.

**Связанные документы:**
- [dev_v2_postgres.md](dev_v2_postgres.md) — механика Writer одного домена. **Ортогонален:** там — *как* один домен пишет в PG, здесь — *как* все домены связаны по слоям.
- [dev_manifest.md](dev_manifest.md) — архитектурные правила, `pkg/` vs `cmd/`.
- [dev_swagger_reusable_packages.md](dev_swagger_reusable_packages.md) — WB write-безопасность для action-loop (sandbox, readonly interfaces, dry-run).
- [dev_api_tools.md](dev_api_tools.md) — паттерн `Client → Service → Tool` (для будущих write-tools AI).

---

## Принцип

Данные в PostgreSQL организуются в **три слоя**, замкнутых **action-loop**'ом обратно на маркетплейс:

```
                          downloaders (pkg/<domain>/)
API (WB / Ozon / 1C) ───────────────────────────────►   RAW    (схема public)
                                                            │  aggregation / derivation
                                                            ▼
                                                       ANALYTICAL  (схема analytical.)
                                                            │  parametrize (количественный / качественный)
                                                            ▼
                                                    RECOMMENDATION  (схема recommendation.)
                                                            │  tools: pkg/cardupdate, cmd/fix-utilities/
                                                            ▼
                                                      WB API (мутация)
```

| Слой | Что это | Откуда рождается | Для кого |
|------|---------|------------------|----------|
| **RAW** (сырой) | Данные как приходят из API, без преобразований | WB / Ozon / 1C API → даунлоадеры | База для всех расчётов |
| **ANALYTICAL** (аналитический) | Производные от raw, сконцентрированные на конкретном бизнес-вопросе | Агрегации / расчёты поверх raw | Аналитик, дашборды, AI-ассистент |
| **RECOMMENDATION** (рекомендательный) | Конкретные параметры для действия | Надстройка над analytical | Аналитик / AI → action-loop |

**Суть vision:** аналитик или AI-ассистент читает аналитику и рекомендации из PG одним запросом и применяет рекомендации инструментами — меняя состояние на WB (цена, статус рекламного/акционного продвижения).

---

## Слой 1 — Сырой (raw)

**Определение:** данные как есть из API WB / Ozon / 1C. Никакой бизнес-логики — только распаковка ответа в таблицы.

**Где живёт сегодня:** схема `public`, ~70 таблиц, пишут 29 даунлоадеров (14 V2 dual-backend). Примеры по доменам: `cards`, `orders`, `operational_sales`, `sales`, `feedbacks`, `campaigns`, `campaign_bids`, `bid_recommendations`, `onec_goods`.

**Конвенция (гибрид):** существующие raw-таблицы **остаются в `public`** — миграции нет. Новые источники (Ozon и т.п.) тоже кладём в `public`. Логически весь `public` = raw-слой; это зафиксировано в документации, а не enforced схемой БД (почему — см. § «Гибридная схема PG»).

---

## Слой 2 — Аналитический (analytical)

**Определение:** производные от raw, сконцентрированные на конкретном бизнес-вопросе (воронка конверсии по SKU, скорость продаж, ABC-ранжирование, ценовые разрывы). Один аналитический артефакт ≈ один вопрос.

**Схема:** `analytical.` для всех новых таблиц. Имя квалифицируется в SQL: `INSERT INTO analytical.ma_sku_daily ...`.

**Что переносим из сегодняшних SQLite BI-БД** (`bi.db`, `category-sales.db`, отдельных анализаторов):

| analytical-таблица | Откуда переносим | Что хранит |
|--------------------|------------------|------------|
| `analytical.ma_sku_daily` | [build-ma-sku-snapshots](cmd/data-analyzers/build-ma-sku-snapshots/) | MA-3/7/14/28 продаж по nm + size |
| `analytical.ma_article_daily` | [build-ma-snapshots](cmd/data-analyzers/build-ma-snapshots/) | MA продаж по артикулу |
| `analytical.category_sku_ranking` | [analyze-category-sales](cmd/data-analyzers/analyze-category-sales/) | Pareto / ранжирование SKU в категории |
| `analytical.category_velocity` | [analyze-category-sales](cmd/data-analyzers/analyze-category-sales/) | Скорость продаж по категории |
| `analytical.price_comparison` | [compare-wb-1c-prices](cmd/data-analyzers/compare-wb-1c-prices/) | Дельта цены WB ↔ 1C |
| `analytical.barcode_mapping` | [1c_mktpl_mapping](cmd/data-analyzers/1c_mktpl_mapping/) | Сопоставление 1C ↔ nmID ↔ barcode |

### Когда что: таблица vs view vs materialized view

В аналитическом слое выбор носителя зависит от двух факторов: **стоимость запроса** и **допустимая несвежесть**.

| Носитель | Хранит данные? | Свежесть | Скорость чтения | Запись | Когда |
|----------|----------------|----------|-----------------|--------|-------|
| **TABLE** | Да (владеет) | Источник истины | Быстро (индексы) | INSERT / UPDATE / DELETE | Пишешь данные / есть lifecycle (`status`, аудит) |
| **VIEW** | Нет (только текст запроса) | Всегда актуальна | Медленно при тяжёлом запросе (пересчёт при каждом чтении) | Обычно read-only | Лёгкий join / фильтр по индексам, нужна свежесть |
| **MATERIALIZED VIEW** | Да (копия результата) | Снимок, устаревает до `REFRESH` | Быстро (можно индексировать) | Read-only, обновление через `REFRESH` | Тяжёлая агрегация, частое чтение, терпима несвежесть |

Решающее правило:
- Нужно писать данные / есть `status`-lifecycle → **таблица** (raw, recommendation).
- Лёгкая join-проекция, результат должен быть всегда свежим → **view** (напр. `analytical.v_card_enriched` = `cards JOIN onec_goods JOIN product_prices`).
- Тяжёлая агрегация по миллионам сырых строк, читается часто, обновляется периодически → **materialized view** (напр. `analytical.ma_sku_daily` = MA-28d поверх истории продаж).

Гатчи:
- `REFRESH MATERIALIZED VIEW CONCURRENTLY` не блокирует чтение, но требует уникальный индекс на матview.
- Стандартный PG **не умеет инкрементальный refresh** — `REFRESH` пересчитывает целиком. Для огромных растущих сырых таблиц выгоднее **агрегатная таблица**, которую downloader обновляет инкрементально (поэтому сегодня `ma_sku_daily` — таблица, а не матview).

---

## Слой 3 — Рекомендательный (recommendation)

**Определение:** надстройка над analytical. Из аналитики рождаются **конкретные количественные или качественные параметры** для действия — то, что аналитик или AI применит к WB.

**Схема:** `recommendation.`.

**Конвенция структуры таблицы-рекомендации:**

| Поле | Назначение |
|------|------------|
| идентификатор цели | `nm_id` / `advert_id` / `chrt_id` |
| количественный параметр | `suggested_price`, `suggested_bid`, `suggested_status` |
| качественное обоснование | `reason TEXT` |
| **`status`** | `pending` → `applied` / `rejected` (жизненный цикл) |
| аудит | `created_at`, `applied_at`, `wb_response` |

**`status` обязателен** — делает рекомендацию reviewable (аналитик одобряет/отклоняет) и связывает слой с action-loop (применили → `applied` + `wb_response`).

**Seed из сырых WB-рекомендаций:** `bid_recommendations`, `min_bids`, `normquery_*` (см. [promotion_schema.go](pkg/storage/postgres/promotion_schema.go)) — это **рекомендации самого WB**, сырой вход. Слой пончо их refinирует/переопределяет, а не хранит как готовый ответ.

**Существующий прототип:** таблица `card_analysis` в [check-card-consistency](cmd/data-analyzers/check-card-consistency/) — поля `new_title` / `new_description` / `new_characteristics` / `new_subject_id` (LLM-vision предложения) + `wb_updated` / `wb_update_response`. Сегодня это единственный end-to-end цикл «предложение → staging → WB apply», эталон для обобщения.

Примеры будущих recommendation-таблиц: `recommendation.price_suggestion`, `recommendation.campaign_action` (`advert_id` + `suggested_action`: pause / boost / bid).

---

## Action-loop (рекомендация → мутация WB)

Цикл замыкает слои обратно на маркетплейс:

```
recommendation (status = pending)
   │   1. --dry-run  — показать payload
   │   2. --stage    — записать в staging-таблицу
   │   3. --apply    — вызвать WB API
   ▼
WB API (мутация) → recommendation.status = applied + wb_response + аудит
```

**Паттерн `--stage` / `--apply` / `--dry-run`** — уже работает в [cmd/fix-utilities/](cmd/fix-utilities/), например [fix-card-fields](cmd/fix-utilities/fix-card-fields/) со staging-таблицей `fix_card_fields_staging` и `changes_json` на nm.

**Переиспользовать [pkg/cardupdate](pkg/cardupdate/):** `LoadFullCard(nmID)` → мутировать целевое поле → `ToUpdateItem()` → `ApplyBatch(client, items, buildFn)`. PG-бэкенд — [pg.go](pkg/cardupdate/pg.go), SDK-точка входа — [cardupdate.go](pkg/cardupdate/cardupdate.go).

> ❌ **Антипаттерн:** переизобретать apply inline. [check-card-consistency](cmd/data-analyzers/check-card-consistency/) сейчас reimplements build/apply вместо `pkg/cardupdate` — не повторять. Новые recommendation-applier'ы идут через `pkg/cardupdate`.

**Критическое правило WB:** `POST /content/v2/cards/update` **полностью перезаписывает** карточку. Всегда: load-all → mutate-target → write-complete. Частичный payload обнулит непереданные поля (подробнее — [dev_swagger_reusable_packages.md](dev_swagger_reusable_packages.md)).

### Gap-флаг (north-star, не реализовано)

Сегодня из WB-мутаций подключён **только** `UpdateCards` (карточки). Мутаций цены / бида / статуса кампании в коде нет. Все agent-tools в [pkg/tools/std/](pkg/tools/std/) — **read-only** (`wb_*` analytics / search / cards / dictionaries). Пути «AI → мутация WB» пока не существует — всё применяется человеком через CLI-фиксёры.

Видение подразумевает будущие **write-tools** (`change_price`, `set_bid`, `pause_campaign`), вызываемые AI-ассистентом через тот же Tool-интерфейс (`Definition()` + `Execute(ctx, argsJSON)`). Их реализация — **отдельная задача**, здесь фиксируется только как направление. Когда появятся — пройдут через `pkg/cardupdate` / Service Layer и sandbox (см. [dev_api_tools.md](dev_api_tools.md), [dev_swagger_reusable_packages.md](dev_swagger_reusable_packages.md)).

---

## Гибридная схема PG (решение)

**Принято (2026-07-11):** гибридный подход к разделению слоёв.

| Слой | Схема | Что туда |
|------|-------|----------|
| RAW | `public` (как есть) | все ~70 существующих таблиц + новые источники (Ozon) |
| ANALYTICAL | `analytical.` (новая) | новые производные таблицы + matviews |
| RECOMMENDATION | `recommendation.` (новая) | новые таблицы-рекомендации |

**Почему не big-bang миграция в `raw.`:** ~70 таблиц × правка `*_schema.go` и репозиториев у каждого домена = большой разовый effort с риском для рабочих данных. Гибрид даёт тот же enforcement для нового кода, не трогая существующий.

**Почему не naming-only (`raw_` / `an_` / `rec_`):** enforcement только на совести разработчика; нельзя выдать права послойно; имена длиннее. Схемы сильнее при равном эффекте на существующие таблицы.

**Что меняется в коде (минимум):**
- Сырые даунлоадеры — **ничего**. Пишут неквалифицированные имена → `public` → работает (`search_path` по умолчанию).
- Новые analytical/recommendation репозитории в [pkg/storage/postgres/](pkg/storage/postgres/) квалифицируют имя: `INSERT INTO analytical.ma_sku_daily ...`.
- `search_path` глобально **не трогаем**.
- Права (когда созреет): `GRANT SELECT ON ALL TABLES IN SCHEMA analytical, recommendation TO arm_ai_ro;` — AI/аналитик получает читку обоих слоёв одной строкой.

**Cross-layer запрос** — главный payoff: AI-агент читает все слои одним SELECT:

```sql
SELECT c.vendor_code,
       a.ma_28d,
       r.suggested   AS rec_price,
       r.reason,
       r.status
FROM   public.cards c
JOIN   analytical.ma_sku_daily a       USING (nm_id)
LEFT JOIN recommendation.price_suggestion r USING (nm_id)
WHERE  a.ma_28d > 0;
```

---

## Конвенции

1. analytical / recommendation таблицы — **только** в схемах `analytical.` / `recommendation.`. Raw продолжаем в `public`.
2. В SQL квалифицируем имя: `схема.таблица` для analytical/recommendation.
3. **Без префиксов** имён (`raw_` / `an_` / `rec_`) — слой задаёт схема, не префикс.
4. Одинаковые имена таблиц в разных схемах допустимы (`public.cards`, `analytical.cards`).
5. Каждая recommendation-таблица имеет `status` (`pending` / `applied` / `rejected`) + аудит.

---

## Карта текущего состояния (footing vs greenfield)

| Слой | Опора сегодня | Пробел |
|------|---------------|--------|
| **RAW** | Сильная: ~70 таблиц PG + SQLite-зеркало, 29 даунлоадеров, per-domain `*_schema.go` / `*_repo.go`. | Нет изоляции слоёв (всё в `public`). Ozon — пусто на всех слоях. |
| **ANALYTICAL** | Фрагментарная: MA-снапшоты, category-analysis (6 таблиц), price-comparison, mapping — в **разрозненных SQLite** (`bi.db`, `category-sales.db`). `ComputeMA` в [pkg/analytics/ma.go](pkg/analytics/ma.go). | В PG — 0 views / matviews. Нет каталога KPI. AI не может ходить в аналитику рядом с raw. |
| **RECOMMENDATION** | Почти greenfield. Прото-кейс: `card_analysis` (LLM-vision `new_title`/`desc`). Seed WB: `bid_recommendations` / `min_bids`. | Нет persistent-таблицы «предлагаемое изменение», нет модели количественных параметров, нет `status`-lifecycle. |
| **ACTION** | `pkg/cardupdate` + `--stage` / `--apply` / `--dry-run` в fixers. | Только `UpdateCards`. Нет мутаций цены / бида / статуса. Agent-tools 100 % read-only. |

---

## Out of scope (явно)

Эти задачи **не входят** в данную фиксацию — это отдельные будущие работы:
- Big-bang миграция ~70 таблиц в `raw.` (если когда-то понадобится).
- Реализация AI **write-tools** (`change_price` / `set_bid` / `pause_campaign`).
- Per-utility миграционный бэклог: каждая утилита [cmd/data-analyzers/](cmd/data-analyzers/) → конкретная analytical-таблица.
- Реальное `CREATE SCHEMA analytical, recommendation;` — создаётся при первом переносе аналитики в PG, не сейчас.
