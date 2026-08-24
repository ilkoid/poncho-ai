# download-wb-fbs-orders-v2

Загрузчик FBS-заданий Wildberries (v2, PG-only). Заменяет разовые скрипты
`fetch-fbs-orders.sh` + `import-fbs-snapshot.sh` (снапшот 2026-08-16).

## Что загружает

| Фаза | Эндпоинт (сваггер) | Таблица | Содержимое |
|---|---|---|---|
| 1 | `GET /api/v3/orders` — `docs/wb_api_swagger/03-orders-fbs.yaml:463` | `public.fbs_orders` | Сборочные задания (глубина 90 дн, окна по 30 дн, курсор `next`). `rid` == `srid` в orders/sales/finance |
| 2 | `POST /api/v3/orders/status` — `03-orders-fbs.yaml:548` | `public.fbs_orders_status` + `fbs_orders_status_log` | Текущий статус (1:1) и журнал уникальных состояний `(order_id, supplier_status, wb_status)` с `first_seen`/`last_seen` |
| 3 | `POST /api/analytics/v1/order-feed` — `11-analytics.yaml:1682` | `public.order_feed` | Лента заказов: `cancel_type` (причина отмены), география доставки, возвраты, `updated_at` текущего статуса. Окно ≤ 31 сут. **Только FBS/DBS (`is_mp=true`)** — API не фильтрует по модели выполнения, FBW-строки качаются, но отбрасываются до записи (`feed_mp_only: false` / `--feed-all-models` — писать все) |

Гарантия полноты статусов: каждый прогон опрашивает статусы **всех** заданий
моложе `status_window_days` **плюс любых незакрытых** (wb_status не терминальный)
любого возраста. Ошибка батча статусов прерывает прогон (выход с ошибкой) —
молчаливых пропусков переходов не бывает. Сбой ленты — нефатален (`FeedErr`).

Утилита начинает **с нуля**, без миграций: создаёт канонические таблицы
(`TIMESTAMPTZ`-даты, `BIGINT`-ID). История журнала статусов начинается с первого
прогона. В БД, где остались легаси-таблицы разового TEXT-снапшота 2026-08-16
(`import-fbs-snapshot.sh`), инициализация падает с подсказкой; перед первым
прогоном снимите их руками:

```sql
DROP TABLE public.fbs_orders, public.fbs_orders_status;
```

Анализатор `cmd/data-analyzers/fbs-orders-report/` работает поверх новых таблиц
без правок (касты `created_at::timestamptz` валидны и на timestamptz).

Не храним (осознанно): адрес покупателя и комментарий (PII, есть в API),
`colorCode`, `offices[]`.

## Запуск

```bash
go run . --mock                                    # синтетика, ноль БД/АПИ
go run . --dry-run --pg-database wb_data_test      # реальный API, без записей
go run . --config config.yaml                      # прод (только вручную!)
go run . --no-feed ...                             # без фазы ленты
go run . --days 7 --status-window-days 30 ...      # инкрементальный прогон
```

Env: `WB_API_KEY` (основной — marketplace-api/v3 его принимает; контент-ключ
даёт 403 `scope is not allowed`, при 401/403 загрузчик сам повторит с
`WB_API_CONTENT_KEY`), `PG_PWD` + `PGHOST/PGPORT/PGUSER/PGDATABASE`.

Rate limits (swagger): v3 задания+статусы — общий бакет 300 req/min (ставим
120), order-feed — 1 req/min. Если WB даст basic-режим ленты (2 req/24h) —
отключите `feed_enabled`.

## SQL-срезы: воронка состояния FBS

Всё — MSK-время (`AT TIME ZONE 'Europe/Moscow'`).

### Воронка по состояниям за период (журнал статусов)

```sql
-- Распределение текущих состояний заданий, созданных за период
SELECT s.supplier_status, s.wb_status, count(*) AS tasks,
       round(100.0 * count(*) / sum(count(*)) OVER (), 1) AS pct
FROM fbs_orders o
JOIN fbs_orders_status s ON s.order_id = o.id
WHERE o.created_at >= now() - interval '30 days'
GROUP BY 1, 2
ORDER BY tasks DESC;
```

### Эффективность реализации: выкуп / отмена / в движении

```sql
SELECT
  count(*) FILTER (WHERE s.wb_status = 'sold')                    AS выкуплено,
  count(*) FILTER (WHERE s.wb_status LIKE 'canceled%')            AS отменено,
  count(*) FILTER (WHERE s.wb_status = 'declined_by_client')      AS отказ_первый_час,
  count(*) FILTER (WHERE s.wb_status = 'defect')                  AS брак,
  count(*) FILTER (WHERE s.wb_status NOT IN
        ('sold','canceled','canceled_by_client','declined_by_client','defect','canceled_by_carrier')) AS в_работе,
  round(100.0 * count(*) FILTER (WHERE s.wb_status = 'sold') / count(*), 1) AS выкуп_пкт
FROM fbs_orders o
JOIN fbs_orders_status s ON s.order_id = o.id
WHERE o.created_at >= now() - interval '30 days';
```

### Причины отмен (только order-feed)

```sql
SELECT f.cancel_type, count(*),
       round(100.0 * count(*) / sum(count(*)) OVER (), 1) AS pct
FROM order_feed f
WHERE f.is_mp AND f.status = 'cancel'
  AND f.created_at >= now() - interval '30 days'
GROUP BY 1 ORDER BY 2 DESC;
-- app=отказ до получения, receipt=отказ при получении, expire=не забрали, other=техническая
```

### География FBS-продаж по округам

```sql
SELECT f.destination_district, f.destination_city, count(*) AS orders,
       count(*) FILTER (WHERE f.status = 'buyout') AS buyout
FROM order_feed f
WHERE f.is_mp AND f.created_at >= now() - interval '30 days'
GROUP BY 1, 2 ORDER BY orders DESC LIMIT 30;
```

### Скорость жизненного цикла: создание → текущий статус

```sql
-- Медианное время от создания задания до его текущего статуса (сутки), по статусам
SELECT percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM f.updated_at - f.created_at) / 86400)
         AS median_days, f.status
FROM order_feed f
WHERE f.is_mp AND f.created_at >= now() - interval '30 days' AND f.status <> 'created'
GROUP BY f.status;
```

### История переходов конкретного задания

```sql
SELECT l.order_id, l.supplier_status, l.wb_status, l.first_seen::date, l.last_seen::date
FROM fbs_orders_status_log l
WHERE l.order_id = <ID>
ORDER BY l.first_seen;
```

### Связка с деньгами/продажами (rid == srid)

```sql
SELECT o.id, o.rid, st.order_dt, os.sale_dt, o.price / 100.0 AS price_rub
FROM fbs_orders o
LEFT JOIN orders st  ON st.srid  = o.rid   -- Statistics API заказы
LEFT JOIN sales os   ON os.srid  = o.rid   -- продажи (выкуп)
WHERE o.created_at >= now() - interval '7 days'
LIMIT 100;
```

## Архитектура

Канон v2 (`dev_v2_postgres.md`): `pkg/fbsorders/` (Source + Writer + Downloader),
адаптер `pkg/storage/postgres/fbsorders_repo.go` (pgxpool, чанки 500,
`ON CONFLICT` upsert, предсобранные SQL для полных чанков), CLI — тонкий
драйвер. `--mock` → `DiscardWriter` (БД не открывается). Guard-тесты:
`fbsorders_cols_test.go` (плейсхолдеры/конфликты), `downloader_test.go`
(полнота статусов, батчи ≤ 1000, фатальность/нефатальность фаз).
