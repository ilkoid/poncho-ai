# download-wb-item-rating-v2

Загрузчик отчёта **«Оценка товара»** WB Seller Analytics API:
`POST /api/analytics/v2/item-rating` (спека: `docs/wb_api_swagger/11-analytics.yaml:1773`).

V2-архитектура: домен `pkg/itemrating/`, двойной бэкенд (SQLite + PostgreSQL),
`--mock` = DiscardWriter (ноль обращений к БД).

## Что собирает (полный захват, RAW as-is)

Три блока ответа, ничего не выбрасывается:

- `item_ratings` — по каждому товару: идентификация (nm_id, артикул, предмет,
  бренд, ярлык), `pinned_feedback`, `card_rating` (WB-поле `rating` — «Рейтинг
  карточки товара», не звёзды), звёздный рейтинг `{current, dynamics, percentile}`
  (перцентиль — позиция среди товаров предмета), прирост отзывов всего и по
  каждой звезде `{inc, dynamics}`, `disqualified` (отзывы, исключённые WB из
  рейтинга), `is_shadowed` (товар скрыт из каталога) + **`raw_item`** — точный
  JSON item'а как его отдал WB.
- `item_rating_seller` — аккаунт-уровень: рейтинг продавца + прирост оценок
  (тоталы и по звёздам) + **raw-JSON обоих блоков**.

Grain: `UNIQUE(nm_id, period_start, period_end)` / `UNIQUE(period_start, period_end)`.
Период по умолчанию — окно «вчера» (days: 1), прошлый период (для динамики) —
равное окно перед ним. `--begin/--end` — произвольное окно (история до 364 дней).

## Семантика feedbackRating.current (проверено живым прогоном 12.10)

`feedbackRating.current` — **средняя оценка по отзывам, полученным ЗА ОКНО периода**,
а НЕ абсолютный текущий рейтинг товара. При days=1 и нуле новых отзывов за день
WB возвращает 0 у всех items (проверено: окно 11.10 → current=0 везде при
fb_increase_current=0 аккаунт-шире; окно 28 дней → current>0 у 33 117/33 893,
макс 5.00). `percentile` при этом считается по общему рейтингу товара и бывает
не-NULL даже у «спящих» карточек.

Следствия:
- дневной прогон (days=1) даёт: дневные приросты по звёздам, перцентиль,
  disqualified, isShadowed — уникальные поля; rating-current в нём почти всегда 0;
- абсолютный звёздный рейтинг на дату уже снапшотится funnel-meta
  (`products.feedback_rating`);
- периодные средние оценки + распределения — прогоном с широким окном,
  напр. `--begin <D-28> --end <вчера>` (28-дневное окно проверено в wb_data_test).

## Полнота каталога

Фильтры не применяются (`isNotIncludeNmsWithoutSales=false`, `onlyShadowedNms=false`):
берём все товары, включая без продаж и скрытые. Фактический каталог — **33,7 тыс.
карточек** (34 страницы по 1000), полный проход ≈ 15 мин при 3 req/min. Пагинация
офсетом до исчерпания (`apiCount < page_size`), `orderBy=feedbackCount desc`.
Данные WB обновляются раз в час — дрейф между страницами безопасен (upsert по
grain идемпотентен, дубли в рамках батча дедупятся до вставки — SQLSTATE 21000).

## Дрейф спеки

`raw_item`/`raw_seller`/`raw_fb_increase` сохраняют payload целиком — поля,
добавленные WB между рефрешами спеки, не теряются. После каждого
`download-api-specs.sh` сверять состав `DistributionTableItem` с колонками
(эталон — `goldenItemKeys` в `pkg/itemrating/downloader_test.go`).

## Лимиты

Swagger: **3 req/min, интервал 20 сек, burst 3** (на аккаунт). Весь каталог
~4–5 тыс. товаров = 5–6 запросов ≈ 2 минуты. ToolID: `wb.ToolIDItemRating`
(`"item_rating"`), свой бакет, без ShareRateLimit.

## Запуск

```bash
go run . --mock                                          # мок, без API и БД
go run . --mock --backend postgres --pg-database wb_data_test
go run . --dry-run --backend postgres --pg-database wb_data_test   # живой API, без записи
go run . --config ../../.configs/download-all/download-wb-item-rating-v2-PG.yaml --backend postgres
```

Тесты: `go test ./pkg/itemrating/ -v` (включая golden-payload тест полноты).
