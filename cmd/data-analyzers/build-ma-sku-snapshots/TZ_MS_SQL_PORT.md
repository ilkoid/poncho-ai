# Техническое задание: Порт утилиты build-ma-sku-snapshots на MS SQL Server (T-SQL)

## Контекст

Утилита `build-ma-sku-snapshots` (Go/SQLite) формирует ежедневный плоский датасет SKU-аналитики для PowerBI: скользящие средние продаж (MA-3/7/14/28), флаги рисков, размерная сетка, поставки в пути. Необходимо создать аналог на T-SQL для MS SQL Server 2019+.

Сырые данные из WB API, 1C/PIM и данных о поставках уже загружены в MS SQL. Структура таблиц частично отличается от исходной SQLite-схемы — в ТЗ указаны оба варианта (SQLite-исходник + ожидаемый T-SQL).

---

## 1. Источники данных: WB API эндпоинты

Сырые данные загружаются следующими скачивалками (для понимания происхождения данных):

### 1.1. Остатки по складам (stocks_daily_warehouses)

| Параметр | Значение |
|----------|----------|
| **Эндпоинт** | `POST https://seller-analytics-api.wildberries.ru/api/analytics/v1/stocks-report/wb-warehouses` |
| **Скачивалка** | `download-wb-stocks` |
| **Тело запроса** | `{"limit": 250000, "offset": 0}` |
| **Rate limit** | 3 req/min |
| **API-ключ** | `WB_API_KEY` |

**Ответ (items[]):**

| Поле ответа | Колонка БД | Тип | Описание |
|-------------|------------|-----|----------|
| `nmId` | `nm_id` | INT | Артикул WB (номенклатура) |
| `chrtId` | `chrt_id` | BIGINT | Конкретный размер внутри nm_id |
| `warehouseId` | `warehouse_id` | INT | ID склада |
| `warehouseName` | `warehouse_name` | NVARCHAR | Название склада |
| `regionName` | `region_name` | NVARCHAR | Регион из ответа API |
| `quantity` | `quantity` | INT | Текущий остаток |
| `inWayToClient` | `in_way_to_client` | INT | Единицы в пути к клиенту |
| `inWayFromClient` | `in_way_from_client` | INT | **Возвраты от клиентов** (включаются в доступный остаток) |

**Гранулярность:** одна строка = один размер (chrt_id) на одном складе на дату. UNIQUE: `(snapshot_date, nm_id, chrt_id, warehouse_id)`.

### 1.2. Продажи (sales)

| Параметр | Значение |
|----------|----------|
| **Эндпоинт** | `GET https://statistics-api.wildberries.ru/api/v5/supplier/reportDetailByPeriod` |
| **Скачивалка** | `download-wb-sales` |
| **Параметры** | `dateFrom`, `dateTo`, `limit=100000`, `rrdid` (курсор пагинации) |
| **Rate limit** | 6 req/min |
| **API-ключ** | `WB_STAT_API_KEY` |

**Ключевые поля для расчёта MA:**

| Поле | Колонка БД | Описание |
|------|------------|----------|
| `nmId` | `nm_id` | Артикул WB |
| `barcode` | `barcode` | Штрихкод размера |
| `docTypeName` | `doc_type_name` | `"Продажа"` или `"Возврат"` |
| `quantity` | `quantity` | Количество |
| `saleDt` | `sale_dt` | Дата/время продажи |
| `isCancel` | `is_cancel` | Флаг отмены (0/1) |
| `officeName` | `office_name` | Склад отгрузки (маппится → ФО) |

**Фильтрация для MA:** `doc_type_name = 'Продажа' AND is_cancel = 0`.

### 1.3. Карточки товаров (cards, card_sizes)

| Параметр | Значение |
|----------|----------|
| **Эндпоинт** | `GET https://content-api.wildberries.ru/content/v2/cards/cursor/list` |
| **Скачивалка** | `download-wb-cards` |
| **Rate limit** | 100 req/min |
| **API-ключ** | `WB_API_KEY` |

**cards** — мастер-данные номенклатуры: `nm_id`, `vendor_code`, `brand`, `subject_name`.

**card_sizes** — маппинг размеров:

| Колонка | Тип | Описание |
|---------|-----|----------|
| `chrt_id` | BIGINT PK | ID размера (уникален глобально) |
| `nm_id` | INT | Артикул WB |
| `tech_size` | NVARCHAR | Размер (например "104", "50") |
| `wb_size` | NVARCHAR | Размер по шкале WB |
| `skus_json` | NVARCHAR | JSON-массив штрихкодов: `["4630047636342"]` |

### 1.4. Данные 1C/PIM (onec_goods, pim_goods)

| Параметр | Значение |
|----------|----------|
| **Источник** | 1C API + PIM API |
| **Скачивалка** | `download-1c-data` |

**onec_goods** — справочник товаров из учётной системы:

| Колонка | Описание |
|---------|----------|
| `article` | PK, артикул продавца (JOIN с `cards.vendor_code`) |
| `name`, `brand`, `type`, `category`, `category_level1`, `category_level2` | Атрибуты товара |
| `sex`, `season`, `color`, `collection` | Характеристики |

**pim_goods** — валидированные данные PIM:

| Колонка | Описание |
|---------|----------|
| `identifier` | PK (равен article), фоллбэк для identifier |

**JOIN-цепочка:** `cards.vendor_code = onec_goods.article = pim_goods.identifier`

### 1.5. Поставки (supplies, supply_goods)

| Параметр | Значение |
|----------|----------|
| **Эндпоинт-1** | `POST https://supplies-api.wildberries.ru/api/v1/supplies` (список поставок) |
| **Эндпоинт-2** | `GET https://supplies-api.wildberries.ru/api/v1/supplies/{ID}/goods` (товары в поставке) |
| **Эндпоинт-3** | `GET https://supplies-api.wildberries.ru/api/v1/warehouses` (справочник складов) |
| **Скачивалка** | `download-wb-supplies` |

**supplies:**

| Колонка | Описание |
|---------|----------|
| `supply_id`, `preorder_id` | Составной PK |
| `status_id` | Статус (5 = завершена, **исключается**) |

**supply_goods:**

| Колонка | Описание |
|---------|----------|
| `barcode` | Штрихкод |
| `quantity` | Всего единиц |
| `ready_for_sale_quantity` | Уже на складе |
| `supply_id`, `preorder_id` | FK → supplies |

**Расчёт incoming:** `SUM(quantity) - SUM(ready_for_sale_quantity)` где `status_id != 5`.

### 1.6. Справочник складов (wb_warehouses)

| Параметр | Значение |
|----------|----------|
| **Эндпоинт** | `GET https://supplies-api.wildberries.ru/api/v1/warehouses` |

| Колонка | Описание |
|---------|----------|
| `id` | PK |
| `name` | Название склада |
| `address` | Адрес (используется для маппинга → ФО) |

---

## 2. Описание набора данных в источнике для подготовки аналитики

### 2.1. Схема таблиц-источников (SQLite → T-SQL)

```sql
-- === SQLite-схема (исходник) → T-SQL-аналог ===

-- 1. Остатки по складам (ежедневный снапшот)
-- SQLite:
--   stocks_daily_warehouses (id, snapshot_date, nm_id, chrt_id, warehouse_id,
--     warehouse_name, region_name, quantity, in_way_to_client, in_way_from_client)
-- T-SQL: типы TEXT → DATE/NVARCHAR, INTEGER → INT/BIGINT
CREATE TABLE stocks_daily_warehouses (
    id              BIGINT IDENTITY(1,1) PRIMARY KEY,
    snapshot_date   DATE NOT NULL,
    nm_id           INT NOT NULL,
    chrt_id         BIGINT NOT NULL,
    warehouse_id    INT NOT NULL,
    warehouse_name  NVARCHAR(200) NOT NULL DEFAULT '',
    region_name     NVARCHAR(200) NOT NULL DEFAULT '',
    quantity        INT NOT NULL DEFAULT 0,
    in_way_to_client   INT NOT NULL DEFAULT 0,
    in_way_from_client INT NOT NULL DEFAULT 0,
    created_at      DATETIME2 DEFAULT SYSUTCDATETIME(),
    CONSTRAINT UQ_stock UNIQUE (snapshot_date, nm_id, chrt_id, warehouse_id)
);

-- 2. Продажи
-- SQLite: sales (rrd_id, nm_id, barcode, doc_type_name, sale_dt, quantity, is_cancel, office_name, ...)
CREATE TABLE sales (
    rrd_id          BIGINT PRIMARY KEY,
    nm_id           INT NOT NULL,
    barcode         NVARCHAR(50),
    doc_type_name   NVARCHAR(50),
    sale_dt         DATETIME2 NOT NULL,
    quantity        INT NOT NULL DEFAULT 0,
    is_cancel       BIT NOT NULL DEFAULT 0,
    office_name     NVARCHAR(200),
    -- + 30+ финансовых полей
);

-- 3. Карточки товаров
CREATE TABLE cards (
    nm_id           INT PRIMARY KEY,
    vendor_code     NVARCHAR(100),
    brand           NVARCHAR(200),
    subject_id      INT,
    subject_name    NVARCHAR(200)
);

-- 4. Размеры карточек
CREATE TABLE card_sizes (
    chrt_id         BIGINT PRIMARY KEY,
    nm_id           INT NOT NULL,
    tech_size       NVARCHAR(50) NOT NULL DEFAULT '',
    wb_size         NVARCHAR(50) NOT NULL DEFAULT '',
    skus_json       NVARCHAR(MAX) NOT NULL DEFAULT '[]'
);

-- 5. Справочник 1C
CREATE TABLE onec_goods (
    article         NVARCHAR(100) PRIMARY KEY,
    name            NVARCHAR(500),
    brand           NVARCHAR(200),
    type            NVARCHAR(200),
    category        NVARCHAR(200),
    category_level1 NVARCHAR(200),
    category_level2 NVARCHAR(200),
    sex             NVARCHAR(50),
    season          NVARCHAR(50),
    color           NVARCHAR(100),
    collection      NVARCHAR(200)
);

-- 6. Справочник PIM
CREATE TABLE pim_goods (
    identifier      NVARCHAR(100) PRIMARY KEY,
    enabled         BIT,
    wb_nm_id        INT,
    year_collection NVARCHAR(20)
);

-- 7. Поставки
CREATE TABLE supplies (
    supply_id       BIGINT NOT NULL,
    preorder_id     BIGINT NOT NULL,
    status_id       INT NOT NULL,
    -- + даты, склад и др.
    CONSTRAINT PK_supplies PRIMARY KEY (supply_id, preorder_id)
);

CREATE TABLE supply_goods (
    supply_id              BIGINT NOT NULL,
    preorder_id            BIGINT NOT NULL,
    barcode                NVARCHAR(50),
    quantity               INT NOT NULL DEFAULT 0,
    ready_for_sale_quantity INT NOT NULL DEFAULT 0,
    -- FK → supplies
);

-- 8. Справочник складов WB
CREATE TABLE wb_warehouses (
    id              INT PRIMARY KEY,
    name            NVARCHAR(200) NOT NULL,
    address         NVARCHAR(500),
    work_time       NVARCHAR(200),
    is_active       BIT,
    downloaded_at   DATETIME2 NOT NULL
);
```

> **Важно для программиста:** Реальная структура MS SQL может отличаться (имена колонок, типы данных). ТЗ описывает логику — программист должен адаптировать SQL под свою схему, сохраняя семантику полей.

---

## 3. Целевая таблица аналитики: ma_sku_daily

### 3.1. Описание

**Одна строка = один размер (chrt_id) одного товара (nm_id) в одном федеральном округе на конкретную дату.**

Это плоская денормализованная таблица для consumption в PowerBI. Все расчёты выполнены заранее — BI-инструмент только фильтрует и агрегирует.

### 3.2. DDL целевой таблицы (T-SQL)

```sql
CREATE TABLE ma_sku_daily (
    -- === Идентификаторы ===
    snapshot_date   DATE NOT NULL,          -- Дата снапшота (обычно вчера)
    nm_id           INT NOT NULL,           -- Артикул WB (номенклатура)
    chrt_id         BIGINT NOT NULL,        -- ID конкретного размера внутри nm_id
    region_name     NVARCHAR(100) NOT NULL, -- Федеральный округ (Центральный, Приволжский и т.д.)
    tech_size       NVARCHAR(50) DEFAULT '',-- Размер из card_sizes (например "104", "50")

    -- === Идентификаторы продавца ===
    article         NVARCHAR(100) DEFAULT '',      -- Артикул из 1C
    identifier      NVARCHAR(100) DEFAULT '',      -- Код из PIM (или article)
    vendor_code     NVARCHAR(100) DEFAULT '',      -- Артикул продавца из WB

    -- === Атрибуты товара (из 1C/PIM) ===
    name            NVARCHAR(500) DEFAULT '',      -- Название
    brand           NVARCHAR(200) DEFAULT '',      -- Бренд
    type            NVARCHAR(200) DEFAULT '',      -- Тип товара
    category        NVARCHAR(200) DEFAULT '',      -- Категория
    category_level1 NVARCHAR(200) DEFAULT '',      -- Категория уровень 1
    category_level2 NVARCHAR(200) DEFAULT '',      -- Категория уровень 2
    sex             NVARCHAR(50) DEFAULT '',       -- Пол
    season          NVARCHAR(50) DEFAULT '',       -- Сезон
    color           NVARCHAR(100) DEFAULT '',      -- Цвет
    collection      NVARCHAR(200) DEFAULT '',      -- Коллекция

    -- === Остатки ===
    stock_qty       INT DEFAULT 0,          -- Штук на складах в этом ФО (quantity + возвраты)
    supply_incoming INT DEFAULT 0,          -- Штук в пути (из поставок: заказано − уже на складе)
    total_sizes     INT DEFAULT 0,          -- Всего размеров у карточки
    sizes_in_stock  INT DEFAULT 0,          -- Размеров в наличии в этом ФО (с дедупликацией по складам)
    fill_pct        DECIMAL(5,2) DEFAULT 0, -- Заполненность размерного ряда, %

    -- === Скользящие средние (MA) ===
    ma_regional     BIT DEFAULT 0,          -- 1 = MA рассчитана по этому региону
    ma_3            DECIMAL(10,2) NULL,     -- Среднедневные продажи за 3 дня
    ma_7            DECIMAL(10,2) NULL,     -- Среднедневные продаж за 7 дней (базовый показатель)
    ma_14           DECIMAL(10,2) NULL,     -- Среднедневные продажи за 14 дней
    ma_28           DECIMAL(10,2) NULL,     -- Среднедневные продажи за 28 дней

    -- === Производные метрики ===
    sdr_days        DECIMAL(10,2) NULL,     -- На сколько дней хватит остатка (stock_qty / ma_7)
    trend_pct       DECIMAL(10,2) NULL,     -- Тренд спроса, % ((ma_3 - ma_7) / ma_7 * 100)

    -- === Флаги рисков (0/1) ===
    risk            BIT DEFAULT 0,          -- Риск дефицита: stock > 0 AND sdr_days <= 12
    critical        BIT DEFAULT 0,          -- Критично: stock > 0 AND sdr_days <= 7
    out_of_stock    BIT DEFAULT 0,          -- Нет в наличии: stock <= 0 AND ma_7 > 0
    broken_grid     BIT DEFAULT 0,          -- Выбит размерный ряд: sizes_in_stock < total_sizes

    computed_at     DATETIME2 DEFAULT SYSUTCDATETIME(),

    CONSTRAINT PK_ma_sku_daily PRIMARY KEY (snapshot_date, nm_id, chrt_id, region_name)
);

-- Индексы для аналитических запросов
CREATE INDEX IX_ma_sku_daily_snapshot_date ON ma_sku_daily(snapshot_date);
CREATE INDEX IX_ma_sku_daily_nm_id ON ma_sku_daily(nm_id);
CREATE INDEX IX_ma_sku_daily_article ON ma_sku_daily(article);
CREATE INDEX IX_ma_sku_daily_vendor_code ON ma_sku_daily(vendor_code);
CREATE INDEX IX_ma_sku_daily_region ON ma_sku_daily(region_name, snapshot_date);
CREATE INDEX IX_ma_sku_daily_brand ON ma_sku_daily(brand);
CREATE INDEX IX_ma_sku_daily_category ON ma_sku_daily(category);
CREATE INDEX IX_ma_sku_daily_risk_flags ON ma_sku_daily(critical DESC, risk DESC, out_of_stock DESC);
CREATE INDEX IX_ma_sku_daily_date_region ON ma_sku_daily(snapshot_date, region_name);
```

### 3.3. Бизнесовый смысл и методика расчёта каждого поля

#### Идентификаторы

| Поле | Описание | Источник |
|------|----------|----------|
| `snapshot_date` | Дата среза (обычно вчера) | Параметр запуска |
| `nm_id` | Артикул WB (карточка товара) | `stocks_daily_warehouses.nm_id` |
| `chrt_id` | ID конкретного размера | `stocks_daily_warehouses.chrt_id` |
| `region_name` | Федеральный округ | Рассчитывается: `warehouse_id → ФО` через справочник адресов |
| `tech_size` | Размер | `card_sizes.tech_size` по `chrt_id` |

#### Атрибуты товара

| Поле | Описание | Источник |
|------|----------|----------|
| `article` | Артикул продавца | `cards.vendor_code → onec_goods.article` |
| `identifier` | Внутренний код | `COALESCE(pim_goods.identifier, onec_goods.article)` |
| `vendor_code` | Код продавца из WB | `cards.vendor_code` |
| `name`..`collection` | Атрибуты товара | `COALESCE(onec_goods.*, '')` — из 1C/PIM |

#### Остатки

| Поле | Методика расчёта |
|------|-------------------|
| `stock_qty` | `SUM(quantity + in_way_from_client)` по `(nm_id, chrt_id, ФО)` из `stocks_daily_warehouses` на дату. Возвраты от клиентов включаются как доступный остаток. |
| `supply_incoming` | `SUM(quantity) - SUM(ready_for_sale_quantity)` по barcode из `supply_goods` JOIN `supplies` WHERE `status_id != 5`. Маппинг: `barcode → chrt_id` через `card_sizes.skus_json`. Агрегация по `chrt_id`. |
| `total_sizes` | `COUNT(*)` из `card_sizes` по `nm_id` — общее число размеров карточки (глобально, не по региону). |
| `sizes_in_stock` | `COUNT(DISTINCT chrt_id)` из `stocks_daily_warehouses` где `(quantity + in_way_from_client) > 0` в данном ФО. **Критично:** дедупликация по складам — один `chrt_id` на разных складах в одном ФО считается один раз. |
| `fill_pct` | `sizes_in_stock / total_sizes * 100` — процент заполненности размерного ряда в данном ФО. |

#### Скользящие средние (MA)

| Поле | Методика расчёта |
|------|-------------------|
| `ma_3` | Среднедневные продажи за 3 дня до snapshot_date. **Только по этому (nm_id, chrt_id, region_name).** |
| `ma_7` | Среднедневные продажи за 7 дней до snapshot_date. **Базовый показатель для SDR.** |
| `ma_14` | Среднедневные продажи за 14 дней до snapshot_date. |
| `ma_28` | Среднедневные продажи за 28 дней до snapshot_date. |

**Алгоритм расчёта MA (КРИТИЧНО):**

1. За основу берутся продажи из `sales` за 29 дней до snapshot_date
2. Фильтр: `doc_type_name = 'Продажа' AND is_cancel = 0`
3. Группировка по `(nm_id, barcode, office_name, DATE(sale_dt))`
4. Маппинг: `barcode → chrt_id` (через `card_sizes`), `office_name → region_name` (через справочник складов)
5. Для каждого окна (3, 7, 14, 28 дней):
   - Сумма продаж за N дней до snapshot_date (не включая сам snapshot_date)
   - **Дни без продаж (отсутствующие ключи) считаются как 0**, НЕ пропускаются
   - Среднее = `Сумма / N` (делится на размер окна, а не на количество дней с данными)
   - Если дней с ненулевыми продажами < `min_days` (по умолчанию 1), то MA = NULL
6. **Фоллбэк на глобальный MA отсутствует** — если продаж в регионе нет, MA = NULL
7. `ma_regional = 1` только если MA рассчитан по данному региону

#### Производные метрики

| Поле | Формула | Бизнесовый смысл |
|------|---------|------------------|
| `sdr_days` | `stock_qty / ma_7` | Дни до обнуления остатка. NULL если ma_7 = 0 или NULL. Чем меньше — тем быстрее товар кончится. |
| `trend_pct` | `(ma_3 - ma_7) / ma_7 * 100` | Тренд спроса в %. Положительный = спрос растёт, отрицательный = падает. NULL если ma_3 или ma_7 = NULL или ma_7 = 0. |

#### Флаги рисков

| Флаг | Условие | Бизнесовый смысл |
|------|---------|------------------|
| `critical` | `stock_qty > 0 AND sdr_days IS NOT NULL AND sdr_days > 0 AND sdr_days <= 7` | Товар закончится через 7 дней или раньше. Приоритет P0 — создавать срочную поставку. |
| `risk` | `stock_qty > 0 AND sdr_days IS NOT NULL AND sdr_days > 0 AND sdr_days <= 12` | Товар закончится через 12 дней или раньше. Требуется подсортировка. |
| `out_of_stock` | `stock_qty <= 0 AND ma_7 IS NOT NULL AND ma_7 > 0` | Товара нет на складе, но спрос есть. Упущенная выручка. |
| `broken_grid` | `sizes_in_stock < total_sizes` | Часть размеров выбита — покупатель уходит к конкуренту. |

> **Важно:** critical ⊂ risk. Товар с `critical = 1` всегда имеет `risk = 1`.

---

## 4. SQL-запросы для подготовки данных (T-SQL)

### 4.1. Справочник маппинга: склад → федеральный округ

Это критически важный справочник. В Go-версии маппинг строится динамически парсингом адресов. Для MS SQL рекомендуется создать статическую таблицу-справочник.

```sql
-- Таблица маппинга адресов/названий складов → ФО
-- Заполняется на основе парсинга адресов (см. раздел 4.1.1)
CREATE TABLE wh_fo_mapping (
    warehouse_id    INT NOT NULL,
    warehouse_name  NVARCHAR(200) NOT NULL,
    fo_name         NVARCHAR(100) NOT NULL,
    PRIMARY KEY (warehouse_id)
);
```

#### 4.1.1. Логика формирования маппинга (программисту: заполнить справочник)

Маппинг строится в два этапа:

**Этап 1: Парсинг адресов из `wb_warehouses`**

Адрес склада проверяется на вхождение подстрок в порядке приоритета (первое совпадение выигрывает). Полный список паттернов (245 штук) — в Приложении А.

Порядок проверки:
1. Международные (Беларусь, Казахстан, Армения, и т.д.)
2. Северо-Кавказский ФО
3. Южный ФО
4. Дальневосточный ФО
5. Сибирский ФО
6. Уральский ФО
7. Приволжский ФО
8. Северо-Западный ФО
9. Центральный ФО
10. Городские фоллбэки (для адресов без указания региона)

**Этап 2: Обогащение из `stocks_daily_warehouses`**

Некоторые склады (FBS-склады продавцов, мелкие/закрытые склады) есть в остатках, но отсутствуют в `wb_warehouses`. Для них:
- Парсится `warehouse_name` из остатков по тем же паттернам
- Записывается в `wh_fo_mapping`

**T-SQL для формирования справочника:**

```sql
-- Этап 1: Маппинг из wb_warehouses по адресам
-- (программисту: реализовать через CASE с LIKE-паттернами или заполнить статически)
-- Пример для Центрального ФО:
INSERT INTO wh_fo_mapping (warehouse_id, warehouse_name, fo_name)
SELECT id, name,
    CASE
        WHEN LOWER(address) LIKE '%беларусь%' OR LOWER(name) LIKE '%беларусь%' THEN 'Беларусь'
        WHEN LOWER(address) LIKE '%казахстан%' OR LOWER(name) LIKE '%казахстан%' THEN 'Казахстан'
        -- ... полный список паттернов — см. Приложение А
        WHEN LOWER(address) LIKE '%московская обл%' THEN 'Центральный'
        WHEN LOWER(address) LIKE '%подольск%' THEN 'Центральный'
        WHEN LOWER(address) LIKE '%коледино%' THEN 'Центральный'
        WHEN LOWER(address) LIKE '%москва%' THEN 'Центральный'
        -- ... и т.д.
        ELSE NULL
    END AS fo_name
FROM wb_warehouses
WHERE address IS NOT NULL;

-- Этап 2: Добавить склады из остатков, которых нет в wb_warehouses
INSERT INTO wh_fo_mapping (warehouse_id, warehouse_name, fo_name)
SELECT DISTINCT sdw.warehouse_id, sdw.warehouse_name,
    CASE
        -- Те же паттерны, но по warehouse_name
        WHEN LOWER(sdw.warehouse_name) LIKE '%коледино%' THEN 'Центральный'
        WHEN LOWER(sdw.warehouse_name) LIKE '%казань%' THEN 'Приволжский'
        -- ... и т.д.
        ELSE NULL
    END
FROM stocks_daily_warehouses sdw
WHERE sdw.snapshot_date = @snapshot_date
  AND sdw.warehouse_id NOT IN (SELECT warehouse_id FROM wh_fo_mapping)
  AND /* fo_name is not null */;
```

> **Рекомендация программисту:** При 245 паттернах CASE-выражение будет громоздким. Рассмотрите:
> - Временную таблицу паттернов с приоритетом + CROSS APPLY
> - Или заполните `wh_fo_mapping` статически на основе текущего списка складов WB

### 4.2. Остатки по (nm_id, chrt_id, ФО)

```sql
-- Результат: по одному итогу на (nm_id, chrt_id, fo_name)
SELECT
    sdw.nm_id,
    sdw.chrt_id,
    m.fo_name AS region_name,
    SUM(sdw.quantity + ISNULL(sdw.in_way_from_client, 0)) AS stock_qty
INTO #stocks_by_fo
FROM stocks_daily_warehouses sdw
JOIN wh_fo_mapping m ON m.warehouse_id = sdw.warehouse_id
WHERE sdw.snapshot_date = @snapshot_date
GROUP BY sdw.nm_id, sdw.chrt_id, m.fo_name;
```

### 4.3. Всего размеров (total_sizes)

```sql
-- Глобально по карточке, не по региону
SELECT
    nm_id,
    COUNT(*) AS total_sizes
INTO #total_sizes
FROM card_sizes
GROUP BY nm_id;
```

### 4.4. Размеров в наличии с дедупликацией (sizes_in_stock)

```sql
-- КРИТИЧНО: COUNT(DISTINCT chrt_id) — дедупликация по складам внутри ФО
SELECT
    sdw.nm_id,
    m.fo_name AS region_name,
    COUNT(DISTINCT sdw.chrt_id) AS sizes_in_stock
INTO #sizes_in_stock
FROM stocks_daily_warehouses sdw
JOIN wh_fo_mapping m ON m.warehouse_id = sdw.warehouse_id
WHERE sdw.snapshot_date = @snapshot_date
  AND (sdw.quantity + ISNULL(sdw.in_way_from_client, 0)) > @zero_stock_threshold
GROUP BY sdw.nm_id, m.fo_name;
```

### 4.5. Штрихкод → chrt_id (для маппинга продаж и поставок)

```sql
-- card_sizes.skus_json содержит JSON-массив штрихкодов
-- MS SQL 2019+: OPENJSON для разбора
SELECT
    cs.chrt_id,
    cs.nm_id,
    cs.tech_size,
    j.[value] AS barcode
INTO #barcode_chrt
FROM card_sizes cs
CROSS APPLY OPENJSON(cs.skus_json) j
WHERE cs.skus_json IS NOT NULL
  AND cs.skus_json != '[]'
  AND cs.skus_json != '';
```

### 4.6. Продажи для расчёта MA (29-дневное окно)

```sql
-- office_name из sales маппится на region_name через wh_fo_mapping.warehouse_name
-- Маппинг: точное совпадение или вхождение подстроки
SELECT
    s.nm_id,
    bc.chrt_id,
    ISNULL(m.fo_name, m2.fo_name) AS region_name,
    CAST(s.sale_dt AS DATE) AS sale_date,
    SUM(CASE WHEN s.doc_type_name = N'Продажа' THEN s.quantity ELSE 0 END) AS sold_qty
INTO #daily_sales_regional
FROM sales s
JOIN #barcode_chrt bc ON bc.barcode = s.barcode
-- Маппинг office_name → ФО (точное совпадение)
LEFT JOIN wh_fo_mapping m ON m.warehouse_name = s.office_name
-- Фоллбэк: подстрока (office_name содержится в warehouse_name или наоборот)
OUTER APPLY (
    SELECT TOP 1 fo_name
    FROM wh_fo_mapping wm
    WHERE s.office_name IS NOT NULL
      AND (wm.warehouse_name LIKE '%' + s.office_name + '%'
        OR s.office_name LIKE '%' + wm.warehouse_name + '%')
      AND m.fo_name IS NULL  -- только если точного совпадения нет
    ORDER BY LEN(wm.warehouse_name) DESC  -- приоритет более длинному совпадению
) m2
WHERE CAST(s.sale_dt AS DATE) >= DATEADD(DAY, -29, @snapshot_date)
  AND CAST(s.sale_dt AS DATE) <= @snapshot_date
  AND s.is_cancel = 0
  AND s.doc_type_name = N'Продажа'
GROUP BY s.nm_id, bc.chrt_id, ISNULL(m.fo_name, m2.fo_name), CAST(s.sale_dt AS DATE)
HAVING ISNULL(m.fo_name, m2.fo_name) IS NOT NULL;
```

### 4.7. Расчёт скользящих средних (MA)

```sql
-- Расчёт MA для каждого (nm_id, chrt_id, region_name) и каждого окна
-- КРИТИЧНО: дни без продаж = 0 (участвуют в делителе), а не NULL
SELECT
    dsr.nm_id,
    dsr.chrt_id,
    dsr.region_name,
    -- MA-3: продажи за 3 дня до snapshot_date
    -- Окно: [snapshot_date - 3, snapshot_date - 1] (не включая snapshot_date)
    CASE
        WHEN SUM(CASE WHEN dsr.sale_date >= DATEADD(DAY, -3, @snapshot_date)
                       AND dsr.sale_date < @snapshot_date
                  THEN dsr.sold_qty ELSE 0 END) > 0  -- min_days = 1
        THEN CAST(
            SUM(CASE WHEN dsr.sale_date >= DATEADD(DAY, -3, @snapshot_date)
                      AND dsr.sale_date < @snapshot_date
                 THEN dsr.sold_qty ELSE 0 END) / 3.0
            AS DECIMAL(10,2))
        ELSE NULL
    END AS ma_3,

    -- MA-7: продажи за 7 дней до snapshot_date
    CASE
        WHEN SUM(CASE WHEN dsr.sale_date >= DATEADD(DAY, -7, @snapshot_date)
                       AND dsr.sale_date < @snapshot_date
                  THEN dsr.sold_qty ELSE 0 END) > 0
        THEN CAST(
            SUM(CASE WHEN dsr.sale_date >= DATEADD(DAY, -7, @snapshot_date)
                      AND dsr.sale_date < @snapshot_date
                 THEN dsr.sold_qty ELSE 0 END) / 7.0
            AS DECIMAL(10,2))
        ELSE NULL
    END AS ma_7,

    -- MA-14
    CASE
        WHEN SUM(CASE WHEN dsr.sale_date >= DATEADD(DAY, -14, @snapshot_date)
                       AND dsr.sale_date < @snapshot_date
                  THEN dsr.sold_qty ELSE 0 END) > 0
        THEN CAST(
            SUM(CASE WHEN dsr.sale_date >= DATEADD(DAY, -14, @snapshot_date)
                      AND dsr.sale_date < @snapshot_date
                 THEN dsr.sold_qty ELSE 0 END) / 14.0
            AS DECIMAL(10,2))
        ELSE NULL
    END AS ma_14,

    -- MA-28
    CASE
        WHEN SUM(CASE WHEN dsr.sale_date >= DATEADD(DAY, -28, @snapshot_date)
                       AND dsr.sale_date < @snapshot_date
                  THEN dsr.sold_qty ELSE 0 END) > 0
        THEN CAST(
            SUM(CASE WHEN dsr.sale_date >= DATEADD(DAY, -28, @snapshot_date)
                      AND dsr.sale_date < @snapshot_date
                 THEN dsr.sold_qty ELSE 0 END) / 28.0
            AS DECIMAL(10,2))
        ELSE NULL
    END AS ma_28

INTO #ma_calc
FROM #daily_sales_regional dsr
GROUP BY dsr.nm_id, dsr.chrt_id, dsr.region_name;
```

> **Пояснение по алгоритму MA:**
> - Формула: `MA-N = SUM(sales за N дней) / N`
> - Окно: N дней **до** snapshot_date (не включая сам snapshot_date)
> - Дни без продаж = 0 — но сумма за N дней должна быть > 0 (min_days = 1)
> - Если все дни нулевые → MA = NULL (нет данных о спросе)
> - **Делитель = размер окна (N), а не количество дней с продажами**

### 4.8. Поставки в пути (supply_incoming)

```sql
-- incoming = SUM(quantity) - SUM(ready_for_sale_quantity) по barcode
-- Только активные поставки (status_id != 5)
-- Затем barcode → chrt_id через #barcode_chrt
SELECT
    bc.chrt_id,
    SUM(sg.quantity) - SUM(sg.ready_for_sale_quantity) AS supply_incoming
INTO #supply_incoming
FROM supply_goods sg
JOIN supplies s ON s.supply_id = sg.supply_id AND s.preorder_id = sg.preorder_id
JOIN #barcode_chrt bc ON bc.barcode = sg.barcode
WHERE s.status_id NOT IN (5)
GROUP BY bc.chrt_id
HAVING SUM(sg.quantity) - SUM(sg.ready_for_sale_quantity) > 0;
```

### 4.9. Атрибуты товаров

```sql
SELECT
    c.nm_id,
    ISNULL(og.article, '')           AS article,
    ISNULL(pg.identifier, og.article, '') AS identifier,
    ISNULL(c.vendor_code, '')        AS vendor_code,
    ISNULL(og.name, '')              AS name,
    ISNULL(og.brand, '')             AS brand,
    ISNULL(og.type, '')              AS type,
    ISNULL(og.category, '')          AS category,
    ISNULL(og.category_level1, '')   AS category_level1,
    ISNULL(og.category_level2, '')   AS category_level2,
    ISNULL(og.sex, '')               AS sex,
    ISNULL(og.season, '')            AS season,
    ISNULL(og.color, '')             AS color,
    ISNULL(og.collection, '')        AS collection
INTO #product_attrs
FROM cards c
LEFT JOIN onec_goods og ON og.article = c.vendor_code
LEFT JOIN pim_goods pg  ON pg.identifier = c.vendor_code;
```

### 4.10. Финальная сборка: INSERT в ma_sku_daily

```sql
-- Фильтр по году производства (извлекается из 2-3 цифры vendor_code)
-- Пример: vendor_code = '12621749' → год '26' (2026)
-- allowed_years = [25, 26]

INSERT INTO ma_sku_daily (
    snapshot_date, nm_id, chrt_id, region_name, tech_size,
    article, identifier, vendor_code,
    name, brand, type, category, category_level1, category_level2,
    sex, season, color, collection,
    stock_qty, supply_incoming, total_sizes, sizes_in_stock, fill_pct,
    ma_regional, ma_3, ma_7, ma_14, ma_28,
    sdr_days, trend_pct,
    risk, critical, out_of_stock, broken_grid,
    computed_at
)
SELECT
    @snapshot_date,
    s.nm_id,
    s.chrt_id,
    s.region_name,
    ISNULL(bc.tech_size, ''),

    pa.article, pa.identifier, pa.vendor_code,
    pa.name, pa.brand, pa.type, pa.category, pa.category_level1, pa.category_level2,
    pa.sex, pa.season, pa.color, pa.collection,

    s.stock_qty,
    ISNULL(si.supply_incoming, 0),
    ISNULL(ts.total_sizes, 0),
    ISNULL(sis.sizes_in_stock, 0),
    CASE WHEN ISNULL(ts.total_sizes, 0) > 0
         THEN CAST(ISNULL(sis.sizes_in_stock, 0) * 100.0 / ts.total_sizes AS DECIMAL(5,2))
         ELSE 0 END,

    CASE WHEN ma.ma_7 IS NOT NULL THEN 1 ELSE 0 END,  -- ma_regional
    ma.ma_3, ma.ma_7, ma.ma_14, ma.ma_28,

    -- SDR = stock_qty / ma_7
    CASE WHEN ma.ma_7 IS NOT NULL AND ma.ma_7 > 0
         THEN CAST(s.stock_qty / ma.ma_7 AS DECIMAL(10,2))
         ELSE NULL END,

    -- trend = (ma_3 - ma_7) / ma_7 * 100
    CASE WHEN ma.ma_3 IS NOT NULL AND ma.ma_7 IS NOT NULL AND ma.ma_7 > 0
         THEN CAST((ma.ma_3 - ma.ma_7) / ma.ma_7 * 100 AS DECIMAL(10,2))
         ELSE NULL END,

    -- Флаги рисков:
    -- risk: stock > 0 AND sdr > 0 AND sdr <= 12
    CASE WHEN s.stock_qty > 0
          AND ma.ma_7 IS NOT NULL AND ma.ma_7 > 0
          AND s.stock_qty / ma.ma_7 <= 12
         THEN 1 ELSE 0 END,

    -- critical: stock > 0 AND sdr > 0 AND sdr <= 7
    CASE WHEN s.stock_qty > 0
          AND ma.ma_7 IS NOT NULL AND ma.ma_7 > 0
          AND s.stock_qty / ma.ma_7 <= 7
         THEN 1 ELSE 0 END,

    -- out_of_stock: stock <= 0 AND ma_7 > 0
    CASE WHEN s.stock_qty <= 0
          AND ma.ma_7 IS NOT NULL AND ma.ma_7 > 0
         THEN 1 ELSE 0 END,

    -- broken_grid: sizes_in_stock < total_sizes
    CASE WHEN ISNULL(sis.sizes_in_stock, 0) < ISNULL(ts.total_sizes, 0)
         THEN 1 ELSE 0 END,

    SYSUTCDATETIME()

FROM #stocks_by_fo s
-- Размеры
LEFT JOIN #barcode_chrt bc ON bc.chrt_id = s.chrt_id
-- Всего размеров
LEFT JOIN #total_sizes ts ON ts.nm_id = s.nm_id
-- Размеров в наличии
LEFT JOIN #sizes_in_stock sis ON sis.nm_id = s.nm_id AND sis.region_name = s.region_name
-- MA
LEFT JOIN #ma_calc ma ON ma.nm_id = s.nm_id AND ma.chrt_id = s.chrt_id AND ma.region_name = s.region_name
-- Поставки в пути
LEFT JOIN #supply_incoming si ON si.chrt_id = s.chrt_id
-- Атрибуты товаров
LEFT JOIN #product_attrs pa ON pa.nm_id = s.nm_id
-- Фильтр по году производства (если применим)
WHERE
    -- Фильтр: год производства из vendor_code
    -- Пропустить, если фильтрация не нужна
    (
        pa.vendor_code IS NULL
        OR LEN(pa.vendor_code) < 3
        OR SUBSTRING(pa.vendor_code, 2, 2) IN ('25', '26')  -- allowed_years
    )
-- Удалить существующий снапшот при перерасчёте
OPTION (MAXDOP 4);
```

### 4.11. Полный сценарий: обёртка хранимой процедуры

```sql
CREATE PROCEDURE sp_build_ma_sku_snapshots
    @snapshot_date   DATE = NULL,         -- NULL = вчера
    @force_rebuild   BIT = 0,             -- 1 = пересчитать даже если снапшот есть
    @zero_stock_threshold INT = 0,        -- Порог нулевого остатка
    @reorder_window INT = 12,             -- SDR <= этого = риск
    @critical_days  INT = 7,              -- SDR <= этого = критично
    @min_days       INT = 1,              -- Минимум дней с данными для MA
    @allowed_years  NVARCHAR(100) = '25,26' -- Года производства (CSV)
AS
BEGIN
    SET NOCOUNT ON;

    -- Дата по умолчанию = вчера
    IF @snapshot_date IS NULL
        SET @snapshot_date = DATEADD(DAY, -1, CAST(GETDATE() AS DATE));

    -- Проверка существующего снапшота
    IF @force_rebuild = 0 AND EXISTS (
        SELECT 1 FROM ma_sku_daily WHERE snapshot_date = @snapshot_date
    ) BEGIN
        PRINT 'Снапшот уже существует для ' + CONVERT(NVARCHAR, @snapshot_date);
        RETURN;
    END

    -- Удалить существующий снапшот при перерасчёте
    IF @force_rebuild = 1
        DELETE FROM ma_sku_daily WHERE snapshot_date = @snapshot_date;

    -- Шаг 1: Остатки по ФО
    -- (см. раздел 4.2)

    -- Шаг 2: Всего размеров
    -- (см. раздел 4.3)

    -- Шаг 3: Размеров в наличии
    -- (см. раздел 4.4)

    -- Шаг 4: Штрихкод → chrt_id
    -- (см. раздел 4.5)

    -- Шаг 5: Продажи по регионам
    -- (см. раздел 4.6)

    -- Шаг 6: Расчёт MA
    -- (см. раздел 4.7)

    -- Шаг 7: Поставки в пути
    -- (см. раздел 4.8)

    -- Шаг 8: Атрибуты товаров
    -- (см. раздел 4.9)

    -- Шаг 9: Финальная сборка
    -- (см. раздел 4.10)

    -- Очистка временных таблиц
    DROP TABLE IF EXISTS #stocks_by_fo, #total_sizes, #sizes_in_stock,
        #barcode_chrt, #daily_sales_regional, #ma_calc, #supply_incoming, #product_attrs;

    PRINT 'Снапшот построен для ' + CONVERT(NVARCHAR, @snapshot_date);
END
```

---

## 5. Дополнительные сведения

### 5.1. Фильтрация по году производства

Из `vendor_code` (например `"12621749"`) извлекается год: **символы 2-3** → `"26"` → 2026.

```sql
-- T-SQL реализация:
SUBSTRING(vendor_code, 2, 2) IN ('25', '26')
```

Если `vendor_code` короче 3 символов или NULL — товар не фильтруется (проходит).

### 5.2. Маппинг office_name → region_name (для продаж)

Продажи в `sales` содержат `office_name` — название склада отгрузки. Оно может не совпадать с `warehouse_name` из `wb_warehouses` или `stocks_daily_warehouses`.

**Алгоритм маппинга (порядок проверки):**
1. Точное совпадение `office_name = warehouse_name` из `wh_fo_mapping`
2. Вхождение подстроки: `office_name` содержится в `warehouse_name` ИЛИ наоборот
3. Если нет совпадения — продажа **пропускается** (MA для этой позиции не рассчитывается)

Примеры реальных расхождений:
- `"Кемерово"` (office) vs `"СЦ Кемерово"` (warehouse name)
- `"Краснодар"` vs `"Краснодар (Тихорецкая)"`

### 5.3. Дедупликация sizes_in_stock

**Проблема:** Один и тот же `chrt_id` может быть на нескольких складах в одном ФО. Если считать наивно, `sizes_in_stock` превысит `total_sizes` → `fill_pct > 100%`.

**Решение:** `COUNT(DISTINCT chrt_id)` после группировки по `(nm_id, fo_name)`.

### 5.4. Заполнение регионов без размеров в наличии

Если у карточки есть остатки в ФО, но ни один размер не проходит порог (> threshold), то `sizes_in_stock = 0`, `fill_pct = 0`. Строка всё равно создаётся — это нужно для корректных флагов `broken_grid` и `out_of_stock`.

### 5.5. Параметры конфигурации

| Параметр | По умолчанию | Описание |
|----------|-------------|----------|
| `ma.windows` | `[3, 7, 14, 28]` | Окна MA в днях |
| `ma.min_days` | `1` | Минимум дней с ненулевыми продажами для расчёта MA |
| `alerts.zero_stock_threshold` | `0` | `stock_qty <= порога` = "товара нет" |
| `alerts.reorder_window` | `12` | `sdr_days <= 12` → риск |
| `alerts.critical_days` | `7` | `sdr_days <= 7` → критично |
| `filter.allowed_years` | `[25, 26]` | Года производства для фильтрации |

### 5.6. Ожидаемый объём данных

На дату 2026-04-19 (реальный снапшот из SQLite):
- **~100 000 строк** в `ma_sku_daily` за один день
- **~5 400 товаров** (nm_id)
- **~24 000 размеров** (chrt_id)
- **~12 регионов** (8 ФО + международные)

### 5.7. Готовые аналитические запросы (для проверки)

После заполнения `ma_sku_daily` можно выполнять типовые аналитические запросы. Примеры из текущей эксплуатации:

```sql
-- Идеальный шторм: товар кончается, спрос растёт, поставок нет
SELECT name, tech_size, brand, category, region_name,
       stock_qty, ma_7, trend_pct, sdr_days, supply_incoming
FROM ma_sku_daily
WHERE critical = 1
  AND (supply_incoming IS NULL OR supply_incoming = 0)
  AND (trend_pct IS NULL OR trend_pct > 0)
ORDER BY ma_7 DESC;

-- Упущенная выручка: товар закончился, а его покупают
SELECT name, tech_size, brand, category, region_name,
       ma_7 AS demand_per_day,
       ma_7 * 14 AS lost_2weeks,
       supply_incoming
FROM ma_sku_daily
WHERE out_of_stock = 1 AND ma_7 > 0
ORDER BY ma_7 DESC;

-- Неликвид: много на складе, нет спроса
SELECT name, tech_size, brand, category, season, region_name,
       stock_qty, ma_28,
       CASE WHEN ma_28 > 0 THEN stock_qty / ma_28 ELSE NULL END AS days_of_stock
FROM ma_sku_daily
WHERE stock_qty >= 30 AND (ma_28 IS NULL OR ma_28 < 0.1)
ORDER BY stock_qty DESC;
```

### 5.8. Типичные ошибки при реализации

1. **MA считается по GLOBAL продажам, а не по REGIONAL** — каждый размер в каждом ФО должен иметь свой MA, основанный на продажах именно в этом ФО
2. **Дни без продаж пропускаются** — они должны считаться как 0 и входить в делитель
3. **Дедупликация sizes_in_stock не делается** — один chrt_id на нескольких складах в одном ФО должен считаться один раз
4. **Возвраты от клиентов не включаются в stock_qty** — `in_way_from_client` — это доступный остаток
5. **Фоллбэк на глобальный MA** — если продаж в регионе нет, MA должен быть NULL, а не средним по всем регионам
6. **Завершённые поставки (status_id = 5) включаются в supply_incoming** — они уже отражены в остатках

---

## Приложение А: Полный список паттернов маппинга адресов → ФО

> Всего 245 паттернов. Программисту: перенести в CASE-выражение или таблицу-справочник.
> Порядок важен — первое совпадение выигрывает.

### Международные (проверять первыми)

| Паттерн | ФО |
|---------|-----|
| Беларусь | Беларусь |
| Минская обл | Беларусь |
| Гродно | Беларусь |
| Брест | Беларусь |
| Гомель | Беларусь |
| Минск | Беларусь |
| Казахстан | Казахстан |
| Нур-Султан | Казахстан |
| Астана | Казахстан |
| Алматы | Казахстан |
| Атакент | Казахстан |
| Актобе | Казахстан |
| Шымкент | Казахстан |
| Байсерке | Казахстан |
| Караганда | Казахстан |
| Армения | Армения |
| Ереван | Армения |
| Узбекистан | Узбекистан |
| Ташкент | Узбекистан |
| Таджикистан | Таджикистан |
| Душанбе | Таджикистан |
| Dushanbe | Таджикистан |
| Грузия | Грузия |
| Тбилиси | Грузия |
| Tbilisi | Грузия |
| Кыргызстан | Кыргызстан |

### Северо-Кавказский ФО

| Паттерн | ФО |
|---------|-----|
| Дагестан | Северо-Кавказский |
| РСО-Алания | Северо-Кавказский |
| Северная Осетия | Северо-Кавказский |
| Ставропольский край | Северо-Кавказский |
| Ингушетия | Северо-Кавказский |
| Кабардино-Балкар | Северо-Кавказский |
| Карачаево-Черкес | Северо-Кавказский |
| Чеченск | Северо-Кавказский |
| Владикавказ | Северо-Кавказский |
| Махачкала | Северо-Кавказский |
| Невинномысск | Северо-Кавказский |
| Пятигорск | Северо-Кавказский |

### Южный ФО

| Паттерн | ФО |
|---------|-----|
| Краснодарский край | Южный |
| Астраханская обл | Южный |
| Волгоградская обл | Южный |
| Ростовская обл | Южный |
| Республика Адыгея | Южный |
| Калмыкия | Южный |
| Республика Крым | Южный |
| Симферополь | Южный |
| Севастополь | Южный |
| Краснодар | Южный |
| Астрахань | Южный |
| Волгоград | Южный |
| Ростов | Южный |
| Крыловская | Южный |

### Дальневосточный ФО

| Паттерн | ФО |
|---------|-----|
| Приморский край | Дальневосточный |
| Хабаровский край | Дальневосточный |
| Амурская обл | Дальневосточный |
| Забайкальский край | Дальневосточный |
| Республика Саха | Дальневосточный |
| Камчатский край | Дальневосточный |
| Магаданская обл | Дальневосточный |
| Сахалинская обл | Дальневосточный |
| Чукотск | Дальневосточный |
| Бурятия | Дальневосточный |
| Хабаровск | Дальневосточный |
| Владивосток | Дальневосточный |
| Артем | Дальневосточный |
| Белогорск | Дальневосточный |
| Чита | Дальневосточный |

### Сибирский ФО

| Паттерн | ФО |
|---------|-----|
| Новосибирская обл | Сибирский |
| Кемеровская обл | Сибирский |
| Кемеровская область | Сибирский |
| Томская обл | Сибирский |
| Омская обл | Сибирский |
| Иркутская обл | Сибирский |
| Алтайский край | Сибирский |
| Красноярский край | Сибирский |
| Республика Хакасия | Сибирский |
| Республика Алтай | Сибирский |
| Республика Тыва | Сибирский |
| Новосибирск | Сибирский |
| Кемерово | Сибирский |
| Томск | Сибирский |
| Омск | Сибирский |
| Барнаул | Сибирский |
| Абакан | Сибирский |
| Новокузнецк | Сибирский |
| Иркутск | Сибирский |
| Красноярск | Сибирский |
| Юрга | Сибирский |

### Уральский ФО

| Паттерн | ФО |
|---------|-----|
| Свердловская обл | Уральский |
| Тюменская обл | Уральский |
| Челябинская обл | Уральский |
| Курганская обл | Уральский |
| Ханты-Мансий | Уральский |
| Ямало-Ненецк | Уральский |
| Екатеринбург | Уральский |
| Тюмень | Уральский |
| Сургут | Уральский |
| Челябинск | Уральский |
| Нижний Тагил | Уральский |
| Ноябрьск | Уральский |

### Приволжский ФО

| Паттерн | ФО |
|---------|-----|
| Республика Татарстан | Приволжский |
| Республика Башкортостан | Приволжский |
| Башкортостан | Приволжский |
| Удмуртск | Приволжский |
| Чувашск | Приволжский |
| Мордовия | Приволжский |
| Марий Эл | Приволжский |
| Пермский край | Приволжский |
| Кировская обл | Приволжский |
| Нижегородская обл | Приволжский |
| Оренбургская обл | Приволжский |
| Пензенская обл | Приволжский |
| Самарская обл | Приволжский |
| Саратовская обл | Приволжский |
| Ульяновская обл | Приволжский |
| Казань | Приволжский |
| Ульяновск | Приволжский |
| Уфа | Приволжский |
| Пермь | Приволжский |
| Чебоксары | Приволжский |
| Сарапул | Приволжский |
| Новосемейкино | Приволжский |
| Ижевск | Приволжский |
| Оренбург | Приволжский |
| Кузнецк | Приволжский |
| Киров | Приволжский |
| Нижний Новгород | Приволжский |
| Пенза | Приволжский |
| Набережные Челны | Приволжский |

### Северо-Западный ФО

| Паттерн | ФО |
|---------|-----|
| Ленинградская обл | Северо-Западный |
| Архангельская обл | Северо-Западный |
| Вологодская обл | Северо-Западный |
| Калининградская обл | Северо-Западный |
| Мурманская обл | Северо-Западный |
| Новгородская обл | Северо-Западный |
| Псковская обл | Северо-Западный |
| Республика Карелия | Северо-Западный |
| Республика Коми | Северо-Западный |
| Ненецк | Северо-Западный |
| Шушары | Северо-Западный |
| Мурманск | Северо-Западный |
| Псков | Северо-Западный |
| Вологда | Северо-Западный |
| Череповец | Северо-Западный |
| Калининград | Северо-Западный |
| Сыктывкар | Северо-Западный |
| Архангельск | Северо-Западный |
| Красный Бор | Северо-Западный |
| Ломоносовский | Северо-Западный |

### Центральный ФО

| Паттерн | ФО |
|---------|-----|
| Московская обл | Центральный |
| Московская область | Центральный |
| Белгородская обл | Центральный |
| Брянская обл | Центральный |
| Владимирская обл | Центральный |
| Воронежская обл | Центральный |
| Ивановская обл | Центральный |
| Калужская обл | Центральный |
| Костромская обл | Центральный |
| Курская обл | Центральный |
| Липецкая обл | Центральный |
| Орловская обл | Центральный |
| Рязанская обл | Центральный |
| Смоленская обл | Центральный |
| Тамбовская обл | Центральный |
| Тверская обл | Центральный |
| Тульская обл | Центральный |
| Ярославская обл | Центральный |
| Подольск | Центральный |
| Коледино | Центральный |
| Электросталь | Центральный |
| Чехов | Центральный |
| Домодедово | Центральный |
| Калуга | Центральный |
| Тверь | Центральный |
| Курск | Центральный |
| Липецк | Центральный |
| Владимир | Центральный |
| Смоленск | Центральный |
| Воронеж | Центральный |
| Рязань | Центральный |
| Ярославль | Центральный |
| Брянск | Центральный |
| Тамбов | Центральный |
| Котовск | Центральный |
| Иваново | Центральный |
| Обухово | Центральный |
| Софьино | Центральный |
| Чашниково | Центральный |
| Солнечногорск | Центральный |
| Пушкино | Центральный |
| Истра | Центральный |
| Раменский | Центральный |
| Дмитровск | Центральный |
| Климовск | Центральный |
| Щербинка | Центральный |
| Голицыно | Центральный |
| Никольское | Центральный |
| Радумля | Центральный |
| Софрино | Центральный |
| Белая Дача | Центральный |
| Белые Столбы | Центральный |
| Москва | Центральный |
| Тула | Центральный |
| Внуково | Центральный |
| Остальные | Центральный |
| Вёшки | Центральный |
| Вешки | Центральный |

---

## Приложение Б: Контрольная сумма для валидации

После заполнения `ma_sku_daily` за дату, сверить с эталонным снапшотом (данные от 2026-04-19):

| Метрика | Ожидаемое значение |
|---------|-------------------|
| Всего строк | ~100 000 |
| Уникальных nm_id | ~5 400 |
| Уникальных chrt_id | ~24 000 |
| Регионов | ~12 |
| С MA-7 не NULL | ~40% строк |
| critical = 1 | ~переменный, зависит от данных |
| out_of_stock = 1 | ~переменный |
| broken_grid = 1 | ~переменный |

---

## Верификация

Проверка корректности реализации:

1. **Запустить процедуру** для известной даты: `EXEC sp_build_ma_sku_snapshots @snapshot_date = '2026-04-19', @force_rebuild = 1`
2. **Сравнить** количество строк с эталонным ~100 000
3. **Проверить** конкретный товар: выбрать nm_id с продажами, сверить ma_7 вручную
4. **Проверить флаги:** товар с stock > 0 и ma_7 = 2 должен иметь sdr = stock/2, risk/critical если sdr <= 12/7
5. **Проверить broken_grid:** карточка с 5 размерами, где в наличии 3 → broken_grid = 1, fill_pct = 60
