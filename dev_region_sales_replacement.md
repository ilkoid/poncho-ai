# dev_region_sales_replacement.md — Замена отчёта «Продажи по регионам»

**Дата**: 2026-10-11. Компаньон к `dev_wb_promo_fixes.md` и `dev_wb_stocks_data_notes.md`
(тот же аудит-цикл; region-sale в них пропущен — найден отдельно по объявлению WB).

---

## Факт

WB отключает **GET /api/v1/analytics/region-sale** («Продажи по регионам»)
**3 ноября 2026** — `deprecated: true`, release-notes id=590
(`docs/wb_api_swagger/12-reports.yaml:1661`, свежая выгрузка 2026-10-11).
У нас это **активная фаза** конвейера: `download-all.sh` → `download-wb-region-sales-v2`
→ PG `public.region_sales` (домен `pkg/regionsales`). Читателей таблицы в коде нет
(только pg-maintenance/freshness) — после 3.11 перестанет поступать статистика
и начнёт падать фаза. Рекомендация WB: «используйте данные отчёта Лента заказов».

## Совместимость семантического ядра (проверено дважды: спека + код/PG)

Старый отчёт (`RegionSaleItem`, `12-reports.yaml:3602+`): гран
nm × город × регион × страна, меры **«Выкупили, шт»** (`saleItemInvoiceQty`),
**«К перечислению за товар, ₽»** (`saleInvoiceCostPrice`, точное нетто из
реализаций), «Доля, %»; произвольный период запроса.

Замена собирается из УЖЕ скачиваемых таблиц — новый загрузчик не нужен:

| Ядро region-sale | Источник замены | Вердикт |
|---|---|---|
| страна | `public.orders.country_name` (по `srid`) | ✅ |
| федеральный округ | `public.order_feed.destination_district` | ✅ |
| регион | `public.orders.region_name` (fallback `operational_sales`) | ✅ |
| город (до н.п.) | `public.order_feed.destination_city` | ✅ |
| артикул WB/продавца | `nm_id` (+ `sa` через cards) | ✅ |
| «Выкупили, шт» | `order_feed.status='buyout'` | ✅ |
| отмена/возврат/брак | `status` = `cancel`/`return`/`returnDefective` + `cancel_type` | ✅ богаче старого |
| **«К перечислению, ₽» точное** | — нигде: у реализаций нет гео | ⚠️ **утрачено** |
| деньги-приближения | `order_feed.seller_price` (валовая цена продавца); `operational_sales.for_pay` (упрощённый нетто WB, продажи 'S*' − возвраты 'R*') | ⚠️ две семантики |
| период назад | история копится у нас; лента в PG — с запуска FBS-конвейера (~27.09.2026); глубже — регион без города/ФО | ⚠️ частично |

«Лента заказов» ≠ статистика «Заказы»: это POST `/api/analytics/v1/order-feed`
(`11-analytics.yaml:1848`), окно 31 день, 1 req/min. Она уже качается
конвейером: `download-wb-fbs-orders-v2` с `feed_mp_only: false` (конфиг
`download-wb-fbs-orders-PG.yaml`) пишет ВСЕ модели в `public.order_feed`.

## Реализация (2026-10-11)

- **`analytical.region_sales_daily`** (`pkg/storage/postgres/region_sales_daily_schema.go`):
  гран день-статуса × nm × город × ФО × регион × страна × is_mp; счётчики
  events/created/buyout/cancel/return; деньги `seller_price_sum` (валовая),
  `price_with_disc_sum`, `for_pay_sales_sum`/`for_pay_returns_sum` (упрощённый
  нетто), `os_rows` (денежное покрытие). Day = МСК-дата ТЕКУЩЕГО статуса юнита
  (семантика ленты). Права read-only — `grantAnalyticalReadonly`.
- **Анализатор** `cmd/data-analyzers/build-region-sales-daily`: идемпотентный
  пересборок окна (DELETE+INSERT одной транзакцией), `--days` (default 7 —
  как `feed_days` загрузчика ленты), `--from/--to`, `--rebuild-all` (вся
  история ленты в PG). Добавлен srid-индекс на `operational_sales`
  (канонически — `opsalesSchemaSQL`).
- **Конвейер**: `download-all.sh`, фаза `region-sales-agg` сразу после
  `fbs-orders` (лента) — конфиг `cmd/.configs/download-all/region-sales-daily-PG.yaml`.
- Smoke-тест пройден на одноразовой БД (fixture: выкуп с S/R-деньгами, отмена
  с fallback-регионом, возврат+брак через МСК-полночь, created без гео).

## Оговорки (важно для интерпретации цифр)

1. **Статусы ленты — «текущие»**: `order_feed` хранит последний статус юнита
   (upsert по srid); свежесть ограничена окном загрузчика (`feed_days: 7`).
   Поздний возврат (>7 дней после последнего обновления статуса) может не
   долететь — «Выкупили» слегка оптимистичнее реального нетто-выкупа.
2. **День = дата статуса, не дата оформления/продажи**: старый отчёт мерил по
   дате продажи; сверка ниже показывает дрейф на границах дней.
3. **Город/ФО начинаются с запуска ленты в PG** (~27.09.2026): за 31 день
   от «сегодня» бэкфилл ещё возможен (`--rebuild-all` его не расширит —
   WB не отдаёт глубже). До этой даты — только регион (orders/opsales).
4. **Точное «к перечислению» не восстановимо** —принято осознанно; денежные
   колонки несут явную семантику (валовая/упрощённый нетто), не эмуляцию.

## Сверка до 3.11 (параллельный контроль, read-only)

После первого ночного прогона с `region-sales-agg` (таблица появится в
`wb_data_prod` сама) — сравнить на пересекающемся окне:

```sql
-- Выкупы по регионам: новый агрегат vs старый отчёт region_sales.
-- Допуск: дни статуса ≠ дни продажи — сравниваем по неделям, не по дням.
WITH new AS (
    SELECT date_trunc('week', stat_day::date) AS wk, region_name,
           SUM(buyout_qty) AS buyouts
    FROM analytical.region_sales_daily
    WHERE buyout_qty > 0 AND region_name <> ''
    GROUP BY 1, 2
),
old AS (
    SELECT date_trunc('week', date_to::date) AS wk, region_name,
           SUM(sale_item_invoice_qty) AS buyouts          -- «Выкупили, шт»
    FROM public.region_sales
    WHERE region_name <> ''
    GROUP BY 1, 2
)
SELECT COALESCE(n.region_name, o.region_name) AS region,
       COALESCE(n.wk, o.wk)::date AS week,
       COALESCE(n.buyouts, 0)  AS new_buyouts,
       COALESCE(o.buyouts, 0)  AS old_buyouts,
       COALESCE(n.buyouts, 0) - COALESCE(o.buyouts, 0) AS diff
FROM new n FULL JOIN old o USING (region_name) -- + wk в JOIN при расхождении недель
ORDER BY ABS(COALESCE(n.buyouts,0)-COALESCE(o.buyouts,0)) DESC
LIMIT 30;
```

Ожидание: малые расхождения (единицы процентов) — семантика дней и
«текущего статуса». Систематический двойной счёт/недосчёт — разбираться
до отключения старого метода.

## Чек-лист 3.11.2026

- [ ] Сверка прошла, расхождения задокументированы (комментарий сюда).
- [ ] `download-all.sh`: закомментировать фазу `region-sales` (строка
      `run ... download-wb-region-sales-v2 ...`) с маркером
      `# off 3.11.2026 (release-notes id=590), замена: region-sales-agg → analytical.region_sales_daily`.
- [ ] `pkg/regionsales` и `public.region_sales` (история) — не трогать.
- [ ] pg-maintenance/freshness: убрать `region_sales` из «ожидаемых свежих»
      списков, если числится.
