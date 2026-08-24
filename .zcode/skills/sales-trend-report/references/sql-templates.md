# SQL-шаблоны — Sales Trend Report

Все 4 запроса построены на общем базовом CTE (`params` + `daily` + `regress`). Окна **анкорятся от последней доступной даты в БД**, а не от `CURRENT_DATE` — это критично, потому что загрузка идёт через `run-daily-analytics.sh` и последние дни могут быть не догружены. Все запросы — read-only `SELECT`.

## Схема `public.sales` — что используем

Из `pkg/storage/postgres/sales_schema.go`:
- `nm_id BIGINT NOT NULL DEFAULT 0` — ID номенклатуры WB
- `supplier_article TEXT` — артикул продавца (vendor_code)
- `subject_name TEXT` — предмет (категория WB)
- `brand_name TEXT` — бренд
- `quantity BIGINT` — количество в строке транзакции
- `retail_amount DOUBLE PRECISION` — выручка по розничной цене
- `sale_dt TEXT` — дата продажи в RFC3339, **TEXT не DATE**, нужен каст `::date`
- `is_cancel BOOLEAN DEFAULT FALSE` — финальная отмена
- `doc_type_name TEXT` — `'Продажа'` или `'Возврат'`

Есть индексы `idx_sales_nm_id`, `idx_sales_sale_dt`, `idx_sales_cancel_doctype`, `idx_sales_nm_sale_dt` — запросы их используют.

## Анкор окон и свежесть данных

**Важно.** Окна идут от **последней доступной даты в БД**, не от сегодняшнего дня. Дата вычисляется в CTE `params` как:

```sql
params AS (
    SELECT (SELECT MAX(sale_dt::date) FROM sales
            WHERE doc_type_name='Продажа' AND is_cancel=false) AS last_d
)
```

Затем окно 28 дней = `[last_d - 27 .. last_d]`, 14 дней = `[last_d - 13 .. last_d]`, и т.д. `last_d` подставляется в CTE `daily` через `FROM sales, params`.

**Почему так:** загрузка продаж идёт через `run-daily-analytics.sh`, и последние 1-9 дней могут быть не догружены. Если бы окна шли от `CURRENT_DATE`, `w7_qty` и `w3_qty` оказались бы нулями для всех артикулов, и тренд исказился («всё падает»).

**Алерт свежести** — выполняй перед основным запросом, чтобы понять отставание данных:
```sql
SELECT
    MAX(sale_dt::date) AS last_data_date,
    CURRENT_DATE - MAX(sale_dt::date) AS lag_days
FROM sales
WHERE doc_type_name='Продажа' AND is_cancel=false;
```
Если `lag_days > 3` — обязательно сообщи пользователю в шапке отчёта: «⚠ Данные в БД по {last_data_date}, отставание {lag_days} дн. Запусти `run-daily-analytics.sh`, чтобы обновить».

## Базовый CTE (общий для запросов 1–3)

```sql
WITH params AS (
    SELECT (SELECT MAX(sale_dt::date) FROM sales
            WHERE doc_type_name='Продажа' AND is_cancel=false) AS last_d
),
daily AS (
    SELECT
        s.nm_id,
        s.supplier_article,
        s.subject_name,
        s.brand_name,
        s.sale_dt::date AS d,
        SUM(s.quantity)      AS qty,
        SUM(s.retail_amount) AS rev
    FROM sales s, params
    WHERE s.doc_type_name = 'Продажа'
      AND s.is_cancel = false
      AND s.sale_dt::date BETWEEN params.last_d - 27 AND params.last_d
    GROUP BY 1, 2, 3, 4, 5
),
regress AS (
    SELECT
        nm_id,
        supplier_article,
        subject_name,
        brand_name,
        -- Скользящие окна от last_d
        SUM(qty) FILTER (WHERE d >= (SELECT last_d FROM params) - 27) AS w28_qty,
        SUM(qty) FILTER (WHERE d >= (SELECT last_d FROM params) - 13) AS w14_qty,
        SUM(qty) FILTER (WHERE d >= (SELECT last_d FROM params) - 6)  AS w7_qty,
        SUM(qty) FILTER (WHERE d >= (SELECT last_d FROM params) - 2)  AS w3_qty,
        SUM(rev) FILTER (WHERE d >= (SELECT last_d FROM params) - 27) AS w28_rev,
        SUM(rev) FILTER (WHERE d >= (SELECT last_d FROM params) - 13) AS w14_rev,
        SUM(rev) FILTER (WHERE d >= (SELECT last_d FROM params) - 6)  AS w7_rev,
        SUM(rev) FILTER (WHERE d >= (SELECT last_d FROM params) - 2)  AS w3_rev,
        -- Регрессия: regr_slope(Y, X), Y=метрика, X=номер дня (0 = 28 дней назад от last_d)
        regr_slope(qty, (d - ((SELECT last_d FROM params) - 27))::float8) AS slope_qty,
        regr_r2(qty,    (d - ((SELECT last_d FROM params) - 27))::float8) AS r2_qty,
        regr_slope(rev, (d - ((SELECT last_d FROM params) - 27))::float8) AS slope_rev,
        regr_r2(rev,    (d - ((SELECT last_d FROM params) - 27))::float8) AS r2_rev
    FROM daily
    GROUP BY 1, 2, 3, 4
)
SELECT * FROM regress;
```

**Семантика окон:**
- Окно 28 дней = `[last_d - 27 .. last_d]` — 28 календарных дней, последний = последняя дата с данными.
- Окно 14 = `[- 13]`, 7 = `[- 6]`, 3 = `[- 2]`.
- `FILTER` внутри `SUM` — стандартный PG, эффективнее `CASE WHEN`.
- Дни с нулевыми продажами **не попадают** в `daily` (нет строк) — это нормально для `regr_slope`, он просто видит меньше точек. Если артикул продаётся не каждый день — это корректно: точки в регрессии = дни с продажами.

**Почему `FROM sales s, params`:** так можно использовать `params.last_d` в WHERE без повторного вычисления `MAX(...)` на каждую строку. PostgreSQLMaterialизует `params` один раз за запрос.

## Запрос 1. Детальный отчёт по артикулам

Используй когда: пользователь назвал конкретный предмет, бренд или артикул, или хочет видеть список с окнами + трендом.

**Плейсхолдеры для замены:** `:subject`, `:brand`, `:article`, `LIMIT`.

```sql
WITH params AS (
    SELECT (SELECT MAX(sale_dt::date) FROM sales
            WHERE doc_type_name='Продажа' AND is_cancel=false) AS last_d
),
daily AS (
    SELECT s.nm_id, s.supplier_article, s.subject_name, s.brand_name,
           s.sale_dt::date AS d,
           SUM(s.quantity) AS qty, SUM(s.retail_amount) AS rev
    FROM sales s, params
    WHERE s.doc_type_name='Продажа' AND s.is_cancel=false
      AND s.sale_dt::date BETWEEN params.last_d - 27 AND params.last_d
    GROUP BY 1,2,3,4,5
),
regress AS (
    SELECT nm_id, supplier_article, subject_name, brand_name,
        SUM(qty) FILTER (WHERE d >= (SELECT last_d FROM params) - 27) AS w28_qty,
        SUM(qty) FILTER (WHERE d >= (SELECT last_d FROM params) - 13) AS w14_qty,
        SUM(qty) FILTER (WHERE d >= (SELECT last_d FROM params) - 6)  AS w7_qty,
        SUM(qty) FILTER (WHERE d >= (SELECT last_d FROM params) - 2)  AS w3_qty,
        SUM(rev) FILTER (WHERE d >= (SELECT last_d FROM params) - 27) AS w28_rev,
        SUM(rev) FILTER (WHERE d >= (SELECT last_d FROM params) - 13) AS w14_rev,
        SUM(rev) FILTER (WHERE d >= (SELECT last_d FROM params) - 6)  AS w7_rev,
        SUM(rev) FILTER (WHERE d >= (SELECT last_d FROM params) - 2)  AS w3_rev,
        regr_slope(qty, (d - ((SELECT last_d FROM params) - 27))::float8) AS slope_qty,
        regr_r2(qty,    (d - ((SELECT last_d FROM params) - 27))::float8) AS r2_qty,
        regr_slope(rev, (d - ((SELECT last_d FROM params) - 27))::float8) AS slope_rev,
        regr_r2(rev,    (d - ((SELECT last_d FROM params) - 27))::float8) AS r2_rev
    FROM daily GROUP BY 1,2,3,4
)
SELECT
    supplier_article      AS article,
    subject_name          AS subject,
    COALESCE(w28_qty, 0)  AS w28_qty,
    COALESCE(w14_qty, 0)  AS w14_qty,
    COALESCE(w7_qty,  0)  AS w7_qty,
    COALESCE(w3_qty,  0)  AS w3_qty,
    COALESCE(w28_rev, 0)  AS w28_rev,
    ROUND(slope_qty::numeric, 3) AS slope_qty,
    ROUND(r2_qty::numeric, 3)    AS r2_qty,
    ROUND(slope_rev::numeric, 3) AS slope_rev,
    ROUND(r2_rev::numeric, 3)    AS r2_rev,
    CASE
        WHEN w28_qty IS NULL OR w28_qty = 0 THEN 'no_data'
        WHEN ABS(slope_qty / NULLIF(w28_qty / 28.0, 0)) < 0.02 THEN 'neutral'
        WHEN slope_qty > 0 AND r2_qty >= 0.4 THEN 'stable_up'
        WHEN slope_qty < 0 AND r2_qty >= 0.4 THEN 'stable_down'
        WHEN slope_qty > 0 AND r2_qty < 0.4  THEN 'volatile_up'
        WHEN slope_qty < 0 AND r2_qty < 0.4  THEN 'volatile_down'
        ELSE 'neutral'
    END AS trend_qty,
    CASE
        WHEN w28_rev IS NULL OR w28_rev = 0 THEN 'no_data'
        WHEN ABS(slope_rev / NULLIF(w28_rev / 28.0, 0)) < 0.02 THEN 'neutral'
        WHEN slope_rev > 0 AND r2_rev >= 0.4 THEN 'stable_up'
        WHEN slope_rev < 0 AND r2_rev >= 0.4 THEN 'stable_down'
        WHEN slope_rev > 0 AND r2_rev < 0.4  THEN 'volatile_up'
        WHEN slope_rev < 0 AND r2_rev < 0.4  THEN 'volatile_down'
        ELSE 'neutral'
    END AS trend_rev
FROM regress
-- ФИЛЬТРЫ — раскомментируй/подставь нужные под запрос пользователя:
-- WHERE subject_name = 'Панама'
--   AND brand_name = 'BrandX'
--   AND supplier_article = '12345678'
ORDER BY w28_qty DESC  -- DESC=топ по обороту; slope_qty DESC=растущие; slope_qty ASC=падающие
LIMIT 50;
```

**Пример вызова:**
```bash
PGPASSWORD="$PG_PWD" psql -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" -d "$PGDATABASE" -c "
WITH params AS (...), daily AS (...), regress AS (...)
SELECT ... FROM regress WHERE subject_name = 'Панама' ORDER BY w28_qty DESC LIMIT 20;
"
```

## Запрос 2. Топ-N растущих / падающих

Используй когда: «что растёт / падает», «топ падающих артикулов», «какие SKU деградируют». Минимальный порог `w28_qty >= 10` отсеивает шум от случайных продаж.

**Важно про `NULLS LAST`:** `regr_slope` возвращает NULL, если у артикула всего 1 день с продажами в окне (для регрессии нужно ≥2 точек). На боевой БД это ~42% артикулов. Без `NULLS LAST` PostgreSQL ставит NULL'ы **первыми** в `DESC` — и «топ растущих» оказывается забит артикулами без тренда. Поэтому **всегда** `NULLS LAST`.

```sql
-- После базового CTE (params + daily + regress), финальный SELECT:
SELECT
    supplier_article AS article,
    subject_name     AS subject,
    COALESCE(w28_qty, 0) AS w28_qty,
    COALESCE(w7_qty,  0) AS w7_qty,
    COALESCE(w3_qty,  0) AS w3_qty,
    ROUND(slope_qty::numeric, 3) AS slope_qty,
    ROUND(r2_qty::numeric, 3)    AS r2_qty,
    CASE
        WHEN slope_qty IS NULL                THEN 'no_data'   -- <2 точек данных для регрессии
        WHEN w28_qty IS NULL OR w28_qty = 0   THEN 'no_data'
        WHEN slope_qty > 0 AND r2_qty >= 0.4 THEN 'stable_up'
        WHEN slope_qty > 0 AND r2_qty < 0.4  THEN 'volatile_up'
        WHEN slope_qty < 0 AND r2_qty >= 0.4 THEN 'stable_down'
        WHEN slope_qty < 0 AND r2_qty < 0.4  THEN 'volatile_down'
        ELSE 'neutral'
    END AS trend_qty
FROM regress
WHERE w28_qty >= 10
  AND slope_qty IS NOT NULL          -- отсеять «1 день данных» из топа
ORDER BY slope_qty DESC NULLS LAST   -- DESC = растущие; ASC = падающие (NULLS LAST обязательно!)
LIMIT 20;
```

Для топ-падающих поменяй `ORDER BY slope_qty DESC NULLS LAST` на `ORDER BY slope_qty ASC NULLS LAST`. Для revenue-аналога — замени `_qty` на `_rev` (и `slope_qty` на `slope_rev`, `r2_qty` на `r2_rev`).

## Запрос 3. Агрегированный тренд по предметам

Используй когда: «форма продаж по категории», «агрегированный тренд по предмету», «какие предметы падают». Финальная агрегация по `subject_name` поверх дневных сумм.

```sql
WITH params AS (
    SELECT (SELECT MAX(sale_dt::date) FROM sales
            WHERE doc_type_name='Продажа' AND is_cancel=false) AS last_d
),
daily_subject AS (
    SELECT s.subject_name,
           s.sale_dt::date AS d,
           SUM(s.quantity)      AS qty,
           SUM(s.retail_amount) AS rev
    FROM sales s, params
    WHERE s.doc_type_name='Продажа' AND s.is_cancel=false
      AND s.sale_dt::date BETWEEN params.last_d - 27 AND params.last_d
      AND s.subject_name <> ''        -- отсеять пустые
    GROUP BY 1, 2
),
regress_subject AS (
    SELECT subject_name,
        SUM(qty) FILTER (WHERE d >= (SELECT last_d FROM params) - 27) AS w28_qty,
        SUM(qty) FILTER (WHERE d >= (SELECT last_d FROM params) - 13) AS w14_qty,
        SUM(qty) FILTER (WHERE d >= (SELECT last_d FROM params) - 6)  AS w7_qty,
        SUM(qty) FILTER (WHERE d >= (SELECT last_d FROM params) - 2)  AS w3_qty,
        SUM(rev) FILTER (WHERE d >= (SELECT last_d FROM params) - 27) AS w28_rev,
        SUM(rev) FILTER (WHERE d >= (SELECT last_d FROM params) - 13) AS w14_rev,
        SUM(rev) FILTER (WHERE d >= (SELECT last_d FROM params) - 6)  AS w7_rev,
        SUM(rev) FILTER (WHERE d >= (SELECT last_d FROM params) - 2)  AS w3_rev,
        regr_slope(qty, (d - ((SELECT last_d FROM params) - 27))::float8) AS slope_qty,
        regr_r2(qty,    (d - ((SELECT last_d FROM params) - 27))::float8) AS r2_qty,
        regr_slope(rev, (d - ((SELECT last_d FROM params) - 27))::float8) AS slope_rev,
        regr_r2(rev,    (d - ((SELECT last_d FROM params) - 27))::float8) AS r2_rev
    FROM daily_subject
    GROUP BY 1
)
SELECT
    subject_name AS subject,
    COALESCE(w28_qty, 0) AS w28_qty,
    COALESCE(w14_qty, 0) AS w14_qty,
    COALESCE(w7_qty,  0) AS w7_qty,
    COALESCE(w3_qty,  0) AS w3_qty,
    COALESCE(w28_rev, 0) AS w28_rev,
    ROUND(slope_qty::numeric, 3) AS slope_qty,
    ROUND(r2_qty::numeric, 3)    AS r2_qty,
    ROUND(slope_rev::numeric, 3) AS slope_rev,
    ROUND(r2_rev::numeric, 3)    AS r2_rev,
    CASE
        WHEN w28_qty IS NULL OR w28_qty = 0 THEN 'no_data'
        WHEN ABS(slope_qty / NULLIF(w28_qty / 28.0, 0)) < 0.02 THEN 'neutral'
        WHEN slope_qty > 0 AND r2_qty >= 0.4 THEN 'stable_up'
        WHEN slope_qty < 0 AND r2_qty >= 0.4 THEN 'stable_down'
        WHEN slope_qty > 0 AND r2_qty < 0.4  THEN 'volatile_up'
        WHEN slope_qty < 0 AND r2_qty < 0.4  THEN 'volatile_down'
        ELSE 'neutral'
    END AS trend_qty,
    CASE
        WHEN w28_rev IS NULL OR w28_rev = 0 THEN 'no_data'
        WHEN ABS(slope_rev / NULLIF(w28_rev / 28.0, 0)) < 0.02 THEN 'neutral'
        WHEN slope_rev > 0 AND r2_rev >= 0.4 THEN 'stable_up'
        WHEN slope_rev < 0 AND r2_rev >= 0.4 THEN 'stable_down'
        WHEN slope_rev > 0 AND r2_rev < 0.4  THEN 'volatile_up'
        WHEN slope_rev < 0 AND r2_rev < 0.4  THEN 'volatile_down'
        ELSE 'neutral'
    END AS trend_rev
FROM regress_subject
ORDER BY w28_qty DESC
LIMIT 50;
```

## Запрос 4. Drill-down по конкретному артикулу

Используй когда: пользователь хочет увидеть форму продаж конкретного артикула по дням, чтобы понять природу тренда (пик в выходные? просадка на конкретной неделе?). Здесь анкор от `last_d` не обязателен, но удобен — чтобы окно было согласовано с остальными запросами.

```sql
WITH params AS (
    SELECT (SELECT MAX(sale_dt::date) FROM sales
            WHERE doc_type_name='Продажа' AND is_cancel=false) AS last_d
)
SELECT
    s.sale_dt::date                  AS d,
    SUM(s.quantity)                  AS qty,
    ROUND(SUM(s.retail_amount)::numeric, 2) AS rev
FROM sales s, params
WHERE s.doc_type_name='Продажа' AND s.is_cancel=false
  AND s.sale_dt::date BETWEEN params.last_d - 27 AND params.last_d
  AND s.supplier_article = '12345678'    -- подставить артикул
GROUP BY 1
ORDER BY 1;
```

После получения дневных значений модель может словесно описать форму: «ровный фон + пик в выходные», «плато 20-23 числа, затем просадка», «волатильно с амплитудой 3x».

**Сколько строк вернётся:** только дни, когда у артикула были продажи (дни с нулём не попадают в `sales` как строки). Для редкопродаваемых SKU это может быть 5-15 строк вместо 28 — это нормально, так и должно быть. Если нужно заполнить нулями (для графика/визуализации), добавь `RIGHT JOIN generate_series(last_d - 27, last_d, '1 day') AS g(d) ON daily.d = g.d`.

## Заметки по psql

- **Env-источник:** переменные подключаются из `/Users/ilkoid/dev/poncho-ai/.env` (root репозитория) через `set -a; source .../.env; set +a`. Не полагайся на `~/.zshrc` — он не исполняется в `zsh -c` non-interactive.
- **Передача SQL из файла**: если SQL длинный (как Шаблоны 1–7), клади его в `/tmp/q.sql` через `cat > /tmp/q.sql <<'SQL' ... SQL` и вызывай `psql -f /tmp/q.sql`. Для длинных многострочных SQL с кириллицей и `$$`-долларами это **единственный надёжный путь** — `-c "..."` ломается на тройном экранировании zsh + psql + SQL.
- **Не экранируй кириллицу** — `psql` работает в UTF-8, `subject_name = 'Панама'` и `season = 'Школа'` валидны. Внутри heredoc с `<<'SQL'` (кавычки вокруг `SQL`!) одинарные кавычки внутри не нужно дублировать.
- **NULL vs 0**: `regr_slope` возвращает NULL если у артикула 1 день с продажами в окне (нужно минимум 2 точки для регрессии). Это нормально — `CASE` отрабатывает как `neutral`/`no_data`.
- **NULLIF на делении**: `slope_qty / NULLIF(w28_qty / 28.0, 0)` защищает от деления на ноль для свежих артикулов.
- **statement_timeout**: добавляй `SET statement_timeout='30s';` первым `-c`, чтобы не зависнуть на тяжёлом запросе. COUNT(*) по всей `sales` (миллионы строк) может быть медленным без фильтра по дате.

## Фильтрация по 1C-коллекции (например, «Школа»)

Когда пользователь спрашивает про «школьный ассортимент», «коллекцию Школа», «тренд по сезону 1C», «весенние/зимние товары» и т.п. — фильтр живёт в отдельной таблице `public.onec_goods`, **не в `sales` и не в `cards`**. В `sales` нет полей `season`/`collection`; в `cards` их тоже нет (проверено по `pkg/storage/postgres/cards_schema.go`). Школьный признак 1C — единственный надёжный источник (см. AGENTS.md).

### Схема `public.onec_goods` — что используем

Из `pkg/storage/postgres/onec_schema.go`:
- `article TEXT NOT NULL DEFAULT ''` — **JOIN-ключ** с `sales.supplier_article` (= WB `vendor_code`). Индекс `idx_onec_goods_article`.
- `season TEXT DEFAULT ''` — функциональный сезон ткани. Возможные значения: `'Школа'`, `'Весна'`, `'Осень'`, `'Зима'`, `'Лето'`.
- `collection_season TEXT DEFAULT ''` — коммерческий сезон коллекции (заполнен только ~23%, как резерв).

### Точное значение для школьного ассортимента

**`'Школа'`** — существительное, заглавная `Ш`, остальные буквы строчные. Production-эталон: `cmd/data-analyzers/collection-readiness/config.yaml:22` (`seasons: ["Школа"]`).

⚠️ **Критично:** НЕ используй `ILIKE '%школьн%'` — в данных хранится bare noun `'Школа'`, прилагательного `'Школьный'`/`'Школьная'` там нет. ILIKE даст 0 матчей. Только `=`, case-sensitive.

### JOIN-ключ и путь

Прямой: `sales.supplier_article = onec_goods.article`. **Без `cards`** — оба поля содержат одно и то же значение (WB `vendor_code`), промежуточный JOIN через `cards` избыточен и только замедляет запрос.

Production-код `collection-readiness` использует `OR collection_season`, что даёт union 2917 артикулов против 2880 при только `season`. В этом skill'е по умолчанию берём **только `season = 'Школа'`** (AGENTS.md явно указывает `season` как надёжный фильтр; `collection_season` для рудиментарных «School boys/girls YYYY» подколлекций здесь не нужен).

### Индексы и производительность

- `idx_onec_goods_article` есть → подзапрос по `onec_goods` быстрый.
- **На `sales.supplier_article` индекса НЕТ.** Поэтому фильтр коллекции комбинируется с обязательным сужением по дате (`sale_dt::date BETWEEN last_d - 27 AND last_d`), чтобы PG использовал `idx_sales_sale_dt` и потом хэшировал статью по списку из ~2880 значений. Без даты — full seq scan по миллионам строк.

### Паттерн фильтра

В базовый CTE `daily` добавляется одно условие в WHERE (самое дешёвое место — давит до агрегации):

```sql
AND s.supplier_article IN (
    SELECT article FROM onec_goods
    WHERE season = 'Школа'       -- подставить нужный сезон: 'Весна'/'Осень'/'Зима'/'Лето'
)
```

Или через EXISTS (эквивалентно, иногда быстрее при больших списках):

```sql
AND EXISTS (SELECT 1 FROM onec_goods o
            WHERE o.article = s.supplier_article AND o.season = 'Школа')
```

## Запрос 5. Детальный отчёт по артикулам 1C-коллекции

Используй когда: пользователь назвал 1C-коллекцию/сезон («Школа», «Весна» и т.д.) и хочет видеть тренды по всем артикулам этого среза.

```sql
WITH params AS (
    SELECT (SELECT MAX(sale_dt::date) FROM sales
            WHERE doc_type_name='Продажа' AND is_cancel=false) AS last_d
),
daily AS (
    SELECT s.nm_id, s.supplier_article, s.subject_name, s.brand_name,
           s.sale_dt::date AS d,
           SUM(s.quantity) AS qty, SUM(s.retail_amount) AS rev
    FROM sales s, params
    WHERE s.doc_type_name='Продажа' AND s.is_cancel=false
      AND s.sale_dt::date BETWEEN params.last_d - 27 AND params.last_d
      AND s.supplier_article IN (SELECT article FROM onec_goods WHERE season = 'Школа')  -- <-- фильтр коллекции
    GROUP BY 1,2,3,4,5
),
regress AS (
    SELECT nm_id, supplier_article, subject_name, brand_name,
        SUM(qty) FILTER (WHERE d >= (SELECT last_d FROM params) - 27) AS w28_qty,
        SUM(qty) FILTER (WHERE d >= (SELECT last_d FROM params) - 13) AS w14_qty,
        SUM(qty) FILTER (WHERE d >= (SELECT last_d FROM params) - 6)  AS w7_qty,
        SUM(qty) FILTER (WHERE d >= (SELECT last_d FROM params) - 2)  AS w3_qty,
        SUM(rev) FILTER (WHERE d >= (SELECT last_d FROM params) - 27) AS w28_rev,
        SUM(rev) FILTER (WHERE d >= (SELECT last_d FROM params) - 13) AS w14_rev,
        SUM(rev) FILTER (WHERE d >= (SELECT last_d FROM params) - 6)  AS w7_rev,
        SUM(rev) FILTER (WHERE d >= (SELECT last_d FROM params) - 2)  AS w3_rev,
        regr_slope(qty, (d - ((SELECT last_d FROM params) - 27))::float8) AS slope_qty,
        regr_r2(qty,    (d - ((SELECT last_d FROM params) - 27))::float8) AS r2_qty,
        regr_slope(rev, (d - ((SELECT last_d FROM params) - 27))::float8) AS slope_rev,
        regr_r2(rev,    (d - ((SELECT last_d FROM params) - 27))::float8) AS r2_rev
    FROM daily GROUP BY 1,2,3,4
)
SELECT
    supplier_article      AS article,
    subject_name          AS subject,
    COALESCE(w28_qty, 0)  AS w28_qty,
    COALESCE(w14_qty, 0)  AS w14_qty,
    COALESCE(w7_qty,  0)  AS w7_qty,
    COALESCE(w3_qty,  0)  AS w3_qty,
    COALESCE(w28_rev, 0)  AS w28_rev,
    ROUND(slope_qty::numeric, 3) AS slope_qty,
    ROUND(r2_qty::numeric, 3)    AS r2_qty,
    ROUND(slope_rev::numeric, 3) AS slope_rev,
    ROUND(r2_rev::numeric, 3)    AS r2_rev,
    CASE
        WHEN w28_qty IS NULL OR w28_qty = 0 THEN 'no_data'
        WHEN ABS(slope_qty / NULLIF(w28_qty / 28.0, 0)) < 0.02 THEN 'neutral'
        WHEN slope_qty > 0 AND r2_qty >= 0.4 THEN 'stable_up'
        WHEN slope_qty < 0 AND r2_qty >= 0.4 THEN 'stable_down'
        WHEN slope_qty > 0 AND r2_qty < 0.4  THEN 'volatile_up'
        WHEN slope_qty < 0 AND r2_qty < 0.4  THEN 'volatile_down'
        ELSE 'neutral'
    END AS trend_qty,
    CASE
        WHEN w28_rev IS NULL OR w28_rev = 0 THEN 'no_data'
        WHEN ABS(slope_rev / NULLIF(w28_rev / 28.0, 0)) < 0.02 THEN 'neutral'
        WHEN slope_rev > 0 AND r2_rev >= 0.4 THEN 'stable_up'
        WHEN slope_rev < 0 AND r2_rev >= 0.4 THEN 'stable_down'
        WHEN slope_rev > 0 AND r2_rev < 0.4  THEN 'volatile_up'
        WHEN slope_rev < 0 AND r2_rev < 0.4  THEN 'volatile_down'
        ELSE 'neutral'
    END AS trend_rev
FROM regress
ORDER BY w28_qty DESC   -- DESC=топ по обороту; slope_qty DESC=растущие; slope_qty ASC=падающие
LIMIT 50;
```

## Запрос 6. Топ-N растущих / падающих в 1C-коллекции

Используется по умолчанию для запросов вида «что растёт/падает в коллекции Школа». Минимальный порог `w28_qty >= 10` отсеивает шум от случайных продаж; для больших 1C-коллекций (Школа ≈ 2880 артикулов) порог особенно важен, иначе топ завален «1 день данных». См. сноску в `references/trend-rules.md`.

Запусти **дважды** — с `ORDER BY slope_qty DESC NULLS LAST` (топ растущих) и `ORDER BY slope_qty ASC NULLS LAST` (топ падающих). `NULLS LAST` обязательно — иначе NULL-наклоны займут верх.

```sql
-- После базового CTE (params + daily с фильтром коллекции + regress), финальный SELECT:
SELECT
    supplier_article AS article,
    subject_name     AS subject,
    COALESCE(w28_qty, 0) AS w28_qty,
    COALESCE(w7_qty,  0) AS w7_qty,
    COALESCE(w3_qty,  0) AS w3_qty,
    COALESCE(w28_rev, 0) AS w28_rev,
    ROUND(slope_qty::numeric, 3) AS slope_qty,
    ROUND(r2_qty::numeric, 3)    AS r2_qty,
    CASE
        WHEN slope_qty IS NULL                THEN 'no_data'   -- <2 точек данных для регрессии
        WHEN w28_qty IS NULL OR w28_qty = 0   THEN 'no_data'
        WHEN slope_qty > 0 AND r2_qty >= 0.4 THEN 'stable_up'
        WHEN slope_qty > 0 AND r2_qty < 0.4  THEN 'volatile_up'
        WHEN slope_qty < 0 AND r2_qty >= 0.4 THEN 'stable_down'
        WHEN slope_qty < 0 AND r2_qty < 0.4  THEN 'volatile_down'
        ELSE 'neutral'
    END AS trend_qty
FROM regress
WHERE w28_qty >= 10
  AND slope_qty IS NOT NULL          -- отсеять «1 день данных» из топа
ORDER BY slope_qty DESC NULLS LAST   -- DESC = растущие; ASC = падающие (NULLS LAST обязательно!)
LIMIT 20;
```

## Запрос 7. Агрегированный тренд по предметам внутри 1C-коллекции

Используй когда: «какие предметы растут/падают в коллекции Школа», «форма продаж по категориям внутри школьного ассортимента». Финальная агрегация по `subject_name`, плюс тот же фильтр коллекции в `daily_subject`.

```sql
WITH params AS (
    SELECT (SELECT MAX(sale_dt::date) FROM sales
            WHERE doc_type_name='Продажа' AND is_cancel=false) AS last_d
),
daily_subject AS (
    SELECT s.subject_name,
           s.sale_dt::date AS d,
           SUM(s.quantity)      AS qty,
           SUM(s.retail_amount) AS rev
    FROM sales s, params
    WHERE s.doc_type_name='Продажа' AND s.is_cancel=false
      AND s.sale_dt::date BETWEEN params.last_d - 27 AND params.last_d
      AND s.subject_name <> ''        -- отсеять пустые
      AND s.supplier_article IN (SELECT article FROM onec_goods WHERE season = 'Школа')  -- <-- фильтр коллекции
    GROUP BY 1, 2
),
regress_subject AS (
    SELECT subject_name,
        SUM(qty) FILTER (WHERE d >= (SELECT last_d FROM params) - 27) AS w28_qty,
        SUM(qty) FILTER (WHERE d >= (SELECT last_d FROM params) - 13) AS w14_qty,
        SUM(qty) FILTER (WHERE d >= (SELECT last_d FROM params) - 6)  AS w7_qty,
        SUM(qty) FILTER (WHERE d >= (SELECT last_d FROM params) - 2)  AS w3_qty,
        SUM(rev) FILTER (WHERE d >= (SELECT last_d FROM params) - 27) AS w28_rev,
        SUM(rev) FILTER (WHERE d >= (SELECT last_d FROM params) - 13) AS w14_rev,
        SUM(rev) FILTER (WHERE d >= (SELECT last_d FROM params) - 6)  AS w7_rev,
        SUM(rev) FILTER (WHERE d >= (SELECT last_d FROM params) - 2)  AS w3_rev,
        regr_slope(qty, (d - ((SELECT last_d FROM params) - 27))::float8) AS slope_qty,
        regr_r2(qty,    (d - ((SELECT last_d FROM params) - 27))::float8) AS r2_qty,
        regr_slope(rev, (d - ((SELECT last_d FROM params) - 27))::float8) AS slope_rev,
        regr_r2(rev,    (d - ((SELECT last_d FROM params) - 27))::float8) AS r2_rev
    FROM daily_subject
    GROUP BY 1
)
SELECT
    subject_name AS subject,
    COALESCE(w28_qty, 0) AS w28_qty,
    COALESCE(w14_qty, 0) AS w14_qty,
    COALESCE(w7_qty,  0) AS w7_qty,
    COALESCE(w3_qty,  0) AS w3_qty,
    COALESCE(w28_rev, 0) AS w28_rev,
    ROUND(slope_qty::numeric, 3) AS slope_qty,
    ROUND(r2_qty::numeric, 3)    AS r2_qty,
    ROUND(slope_rev::numeric, 3) AS slope_rev,
    ROUND(r2_rev::numeric, 3)    AS r2_rev,
    CASE
        WHEN w28_qty IS NULL OR w28_qty = 0 THEN 'no_data'
        WHEN ABS(slope_qty / NULLIF(w28_qty / 28.0, 0)) < 0.02 THEN 'neutral'
        WHEN slope_qty > 0 AND r2_qty >= 0.4 THEN 'stable_up'
        WHEN slope_qty < 0 AND r2_qty >= 0.4 THEN 'stable_down'
        WHEN slope_qty > 0 AND r2_qty < 0.4 THEN 'volatile_up'
        WHEN slope_qty < 0 AND r2_qty < 0.4 THEN 'volatile_down'
        ELSE 'neutral'
    END AS trend_qty,
    CASE
        WHEN w28_rev IS NULL OR w28_rev = 0 THEN 'no_data'
        WHEN ABS(slope_rev / NULLIF(w28_rev / 28.0, 0)) < 0.02 THEN 'neutral'
        WHEN slope_rev > 0 AND r2_rev >= 0.4 THEN 'stable_up'
        WHEN slope_rev < 0 AND r2_rev >= 0.4 THEN 'stable_down'
        WHEN slope_rev > 0 AND r2_rev < 0.4 THEN 'volatile_up'
        WHEN slope_rev < 0 AND r2_rev < 0.4 THEN 'volatile_down'
        ELSE 'neutral'
    END AS trend_rev
FROM regress_subject
ORDER BY w28_qty DESC
LIMIT 50;
```
