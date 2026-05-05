# WB Promotion V2 Downloader

Расширенная загрузка данных WB Promotion API: normquery, bid-рекомендации, финансы, календарь акций.

Требует данных от [download-wb-promotion](../download-wb-promotion/) (таблицы campaigns, campaign_products).

## Запуск

```bash
# Стандартный запуск (7 дней, активные + на паузе)
WB_API_KEY=xxx go run . --days=7

# Мок-режим (без API-вызовов)
go run . --mock --days=7

# Сборка бинарника
go build -o download-wb-promotion-v2 .
```

## Флаги

| Флаг | По умолч. | Описание |
|------|-----------|----------|
| `--config` | `config.yaml` | Путь к конфигу |
| `--days` | 7 | Дней от сегодня |
| `--begin` | — | Начало периода (YYYY-MM-DD) |
| `--end` | — | Конец периода (YYYY-MM-DD) |
| `--statuses` | `9,11` | Статусы кампаний (9=активна, 11=пауза) |
| `--db` | из конфига | Путь к БД |
| `--mock` | false | Мок-режим |
| `--help` | — | Справка |

## Фазы (14 шагов)

| # | Фаза | API | Что сохраняет |
|---|------|-----|---------------|
| 1 | Campaign Bids | `/api/advert/v2/adverts` | Ставки (search/reco) по товарам |
| 2 | Normquery Stats | `POST /adv/v0/normquery/stats` | Статистика по кластерам (клики, CTR, spend) |
| 3 | Normquery Clusters | `POST /adv/v0/normquery/list` | Активные/исключённые кластеры |
| 4 | Normquery Bids | `POST /adv/v0/normquery/get-bids` | Текущие ставки по кластерам |
| 5 | Normquery Minus | `POST /adv/v0/normquery/get-minus` | Минус-фразы |
| 6 | Bid Recommendations | `GET /api/advert/v0/bids/recommendations` | Рекомендованные ставки (3 уровня) |
| 7 | Expenses | `GET /adv/v1/upd` | История списаний |
| 8 | Balance | `GET /adv/v1/balance` | Баланс аккаунта |
| 9 | Payments | `GET /adv/v1/payments` | Пополнения |
| 10 | Calendar Promotions | `GET /api/v1/calendar/promotions` | Список акций WB |
| 11 | Calendar Details | `GET /api/v1/calendar/promotions/details` | Условия, буст, преимущества |
| 12 | Calendar Nomenclatures | `GET /api/v1/calendar/promotions/nomenclatures` | Товары в акции, цены, скидки |
| 13 | Campaign Budgets | `GET /adv/v1/budget` | Бюджет кампаний |
| 14 | Minimum Bids | `POST /api/advert/v1/bids/min` | Минимальные ставки |

## Пропуск фаз

В `config.yaml` есть флаги пропуска (по умолчанию все `false`):

```yaml
promotion_v2:
  skip_bids: false
  skip_normquery: false
  skip_recommendations: false
  skip_finance: false
  skip_calendar: false
```
