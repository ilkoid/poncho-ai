# Эталонные SQL для проверки метрик поставок вручную

Эти запросы можно запускать через `psql` без Python — например, для аудита
или когда нужен запрос «прямо сейчас» без запуска скрипта. Все запросы —
read-only SELECT.

## Запуск psql

Длинный SQL клади в файл, не в `-c` (тройное экранирование zsh+psql+SQL
ломается на кириллице и `$$`):

```bash
cat > /tmp/q.sql <<'SQL'
-- твой SQL
SQL

zsh -c '
  set -a; source /Users/ilkoid/dev/poncho-ai/.env 2>/dev/null; set +a
  PGPASSWORD="$PG_PWD" psql -h "${PGHOST:-192.168.10.7}" -p "${PGPORT:-15432}" \
    -U "${PGUSER:-postgres}" -d "${PGDATABASE:-wb_data_prod}" \
    -f /tmp/q.sql
'
```

Для машинного парсинга добавь флаги `-A -F"|" --pset=footer=off`.

---

## 1. Свежесть данных

```sql
SELECT
  MAX(substring(create_date from 1 for 10)::date) AS last_supply_create,
  MAX(substring(updated_date from 1 for 10)::date) AS last_supply_update,
  CURRENT_DATE - MAX(substring(create_date from 1 for 10)::date) AS lag_days
FROM supplies
WHERE status_id = 5 AND ready_for_sale_quantity > 0;
```

Алерт: если `lag_days > 3` — запустите `download-wb-supplies-v2`.

## 2. Распределение статусов

```sql
SELECT status_id, COUNT(*) AS n,
       MIN(substring(create_date from 1 for 10)) AS min_create,
       MAX(substring(create_date from 1 for 10)) AS max_create
FROM supplies
GROUP BY status_id
ORDER BY status_id;
```

## 3. Наполненность полей по статусам

Полезно для аудита — понять, какие поля когда заполняются.

```sql
SELECT status_id, COUNT(*) AS n,
       COUNT(*) FILTER (WHERE fact_date  IS NOT NULL) AS fact_filled,
       COUNT(*) FILTER (WHERE updated_date IS NOT NULL) AS updated_filled,
       COUNT(*) FILTER (WHERE ready_for_sale_quantity > 0) AS ready_gt0,
       COUNT(*) FILTER (WHERE accepted_quantity > 0) AS accepted_gt0,
       COUNT(*) FILTER (WHERE warehouse_name IS NOT NULL) AS wh_filled
FROM supplies
GROUP BY status_id
ORDER BY status_id;
```

## 4. Среднее время поставки по складам (pure SQL)

Primary-метрика навыка, посчитанная в SQL. Должна совпадать с выводом
`supply_time.py` (с точностью до numpy-интерполяции перцентилей).

```sql
SELECT
  warehouse_name,
  COUNT(*) AS n_supplies,
  ROUND(AVG(substring(updated_date from 1 for 10)::date
          - substring(create_date from 1 for 10)::date)::numeric, 2) AS mean_days,
  PERCENTILE_CONT(0.50) WITHIN GROUP (
    ORDER BY substring(updated_date from 1 for 10)::date
          - substring(create_date from 1 for 10)::date
  ) AS p50_days,
  PERCENTILE_CONT(0.25) WITHIN GROUP (
    ORDER BY substring(updated_date from 1 for 10)::date
          - substring(create_date from 1 for 10)::date
  ) AS p25_days,
  PERCENTILE_CONT(0.75) WITHIN GROUP (
    ORDER BY substring(updated_date from 1 for 10)::date
          - substring(create_date from 1 for 10)::date
  ) AS p75_days,
  PERCENTILE_CONT(0.90) WITHIN GROUP (
    ORDER BY substring(updated_date from 1 for 10)::date
          - substring(create_date from 1 for 10)::date
  ) AS p90_days,
  MIN(substring(updated_date from 1 for 10)::date
    - substring(create_date from 1 for 10)::date) AS min_days,
  MAX(substring(updated_date from 1 for 10)::date
    - substring(create_date from 1 for 10)::date) AS max_days
FROM supplies
WHERE status_id = 5
  AND ready_for_sale_quantity > 0
  AND create_date IS NOT NULL AND updated_date IS NOT NULL
  AND warehouse_name IS NOT NULL
GROUP BY warehouse_name
ORDER BY n_supplies DESC;
```

> Замечание: `PERCENTILE_CONT` использует linear interpolation (как numpy);
> fallback в `supply_time.py` (без numpy) — nearest-rank. На больших выборках
> разница пренебрежима, на маленьких (< 5 поставок) — может отличаться на 1 день.

## 5. Drill-down по конкретному складу

Замените `:wh` на имя склада (например, `'Электросталь'`):

```sql
SELECT supply_id, preorder_id,
       substring(create_date  from 1 for 10) AS create_d,
       substring(updated_date from 1 for 10) AS updated_d,
       substring(updated_date from 1 for 10)::date
         - substring(create_date from 1 for 10)::date AS lag_days,
       ready_for_sale_quantity AS ready,
       accepted_quantity AS accepted
FROM supplies
WHERE status_id = 5
  AND ready_for_sale_quantity > 0
  AND warehouse_name = :wh
ORDER BY create_date;
```

## 6. Cross-check через stocks_daily_warehouses

⚠️ **Важно: cross-check — ПРИБЛИЖЕНИЕ, а не измерение.** Один и тот же
`nm_id` лежит на складе WB месяцами от множества поставок. API стоков даёт
aggregate `quantity` по `(nm_id, chrt_id, warehouse, day)` без tracking, какая
единица из какой поставки. Поэтому по остаткам нельзя точно сказать, какая
единица прибыла с текущей поставкой. Полный разбор — `methodology.md` §9.

**Текущая логика** (соответствует `scripts/supply_time.py`): для каждого nm
из поставки ищем первую дату `quantity > 0` в окне `create_date..+30 дней`,
затем берём **медиану** по всем nm поставки (робастно к шуму от старых
остатков). Покрытие 100% — это медиана, поэтому почти всегда есть результат,
но это не значит «точное совпадение».

```sql
WITH picked AS (
  SELECT s.supply_id, s.preorder_id,
         substring(s.create_date from 1 for 10)::date AS create_d,
         substring(s.updated_date from 1 for 10)::date AS updated_d,
         s.warehouse_name AS wh
  FROM supplies s
  WHERE s.status_id = 5
    AND s.ready_for_sale_quantity > 0
    AND s.box_type_id > 0
    AND substring(s.updated_date from 1 for 10)::date >= '2026-06-06'  -- в пределах истории стоков
    AND s.warehouse_name = :wh
),
nm_per_supply AS (
  SELECT DISTINCT p.supply_id, p.create_d, p.updated_d, p.wh, sg.nm_id
  FROM picked p
  JOIN supply_goods sg
    ON sg.supply_id = p.supply_id AND sg.preorder_id = p.preorder_id
  WHERE sg.nm_id IS NOT NULL
),
nm_first_seen AS (
  SELECT n.supply_id, n.nm_id,
         MIN(sd.snapshot_date::date) AS first_seen
  FROM nm_per_supply n
  JOIN stocks_daily_warehouses sd
    ON sd.nm_id = n.nm_id
   AND sd.warehouse_name = n.wh
   AND sd.snapshot_date::date BETWEEN n.create_d AND n.create_d + 30
   AND sd.quantity > 0
  GROUP BY n.supply_id, n.nm_id
),
agg AS (
  SELECT
    supply_id,
    PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY (first_seen - DATE '2000-01-01')) AS med_off,
    COUNT(*) AS n_nm,
    COUNT(*) FILTER (WHERE first_seen IS NULL) AS n_no_stock
  FROM nm_first_seen
  GROUP BY supply_id
)
SELECT
  p.supply_id, p.wh, p.create_d, p.updated_d,
  p.updated_d - p.create_d AS metric_lag,
  COALESCE(a.n_nm, 0) AS n_nm_with_stock,
  (DATE '2000-01-01' + a.med_off::int) AS median_first_seen,
  (DATE '2000-01-01' + a.med_off::int) - p.updated_d AS median_minus_updated
FROM picked p
LEFT JOIN agg a ON a.supply_id = p.supply_id
ORDER BY p.create_d;
```

Интерпретация:
- `median_minus_updated ≈ +1..2` — для поставок с **уникальными nm** (новые
  артикулы). Товары появляются на остатках на 1–2 дня позже готовности в API.
  Это эмпирическое подтверждение primary-метрики с зазором.
- `median_minus_updated < 0` — для поставок, где nm уже лежали от прошлых
  поставок. **Артефакт, не верьте этой цифре.**
- `median_minus_updated >> 0` (например, +9..+17) — поставка с `updated_date`
  вне истории стоков (раньше 06-06), либо nm вовсе не появились на складе.

⚠️ **История cross-check.** Ранние версии давали ложное покрытие 60–100% с
отрицательными лагами, потому что ловили старые остатки того же nm_id.
Текущая версия (медиана) честнее, но всё ещё приближение.

## 7. Покрытие cross-check по складам

Сколько поставок на каждом складе удалось сверить со стоками:

```sql
WITH picked AS (
  SELECT s.supply_id, s.warehouse_name,
         substring(s.create_date from 1 for 10)::date AS create_d,
         substring(s.updated_date from 1 for 10)::date AS updated_d
  FROM supplies s
  WHERE s.status_id = 5 AND s.ready_for_sale_quantity > 0
)
SELECT
  p.warehouse_name,
  COUNT(*) AS n_supplies,
  COUNT(*) FILTER (
    WHERE EXISTS (
      SELECT 1 FROM stocks_daily_warehouses sd
      JOIN supply_goods sg ON sg.supply_id = p.supply_id
                          AND sg.preorder_id = p.preorder_id
                          AND sg.nm_id = sd.nm_id
      WHERE sd.warehouse_name = p.warehouse_name
        AND sd.snapshot_date::date >= p.create_d
        AND sd.quantity > 0
    )
  ) AS matched_with_stock
FROM picked p
GROUP BY p.warehouse_name
ORDER BY n_supplies DESC;
```

Если `matched_with_stock` значительно меньше `n_supplies` — нужно дополнить
`warehouse_aliases.yaml` синонимами для этого склада.

## 8. Проверка «status_id=5 ≠ ready_for_sale>0»

Сколько поставок на статусе 5 ещё не поступили в продажу (на раскладке):

```sql
SELECT
  COUNT(*) FILTER (WHERE ready_for_sale_quantity > 0) AS ready_gt0,
  COUNT(*) FILTER (WHERE ready_for_sale_quantity IS NULL OR ready_for_sale_quantity = 0) AS not_ready,
  COUNT(*) FILTER (WHERE unloading_quantity > 0) AS on_unloading,
  COUNT(*) AS total_status5
FROM supplies
WHERE status_id = 5;
```

Ожидается: ~12% `not_ready` — именно эти строки отсекаются quality-фильтром
`ready_for_sale_quantity > 0`.

## 9. Разделение ручных vs автоматических поставок

Скрипт по умолчанию считает только ручные (`box_type_id > 0`). Этот запрос
показывает разницу между потоками:

```sql
SELECT
  CASE WHEN box_type_id > 0 THEN 'A_ручные (менеджер)' ELSE 'B_авто (WB)' END AS origin,
  COUNT(*) AS n,
  COUNT(*) FILTER (WHERE preorder_id > 0) AS with_preorder,
  ROUND(AVG(ready_for_sale_quantity)::numeric, 1) AS avg_ready,
  PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY ready_for_sale_quantity) AS p50_ready,
  MIN(substring(create_date from 12 for 2) || ':' || substring(create_date from 15 for 2)) AS min_create_time,
  MAX(substring(create_date from 12 for 2) || ':' || substring(create_date from 15 for 2)) AS max_create_time
FROM supplies
WHERE status_id = 5 AND ready_for_sale_quantity > 0
GROUP BY 1
ORDER BY 1;
```

Ожидаемый паттерн: ручные — `with_preorder=100%`, точное время в рабочие часы;
авто — `with_preorder=0%`, время в окне 00:01–00:03 (ночной джоб WB).

### Все автоматические поставки по типу (`virtual_type_id`)

```sql
SELECT box_type_id, virtual_type_id, COUNT(*) AS n
FROM supplies
WHERE status_id = 5 AND ready_for_sale_quantity > 0
  AND box_type_id = 0
GROUP BY 1, 2
ORDER BY n DESC;
```

`virtual_type_id` enum (из Swagger): 0=Перенос остатков, 1=Обезличка,
4=QR-поставка, 5=Допринято, 6=Скан-приёмка.
