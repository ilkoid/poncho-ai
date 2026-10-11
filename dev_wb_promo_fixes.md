# dev_wb_promo_fixes.md — WB Реклама (нормграфик/бюджет): расхождения с API и план исправления

**Дата аудита**: 2026-10-11
**Источник правды**: `docs/wb_api_swagger/08-promotion.yaml` (свежая выгрузка 2026-10-11, утилита `download-api-specs`)
**Код**: `pkg/wb/promotion.go`, `pkg/wb/promotion_types.go`, `pkg/wb/service_advertising.go`
**Приоритет реализации**: до **16.11.2026** (п. 1 — жёсткий дедлайн WB)

> Документ API-агностичен: описано, ЧТО менять, без привязки к текущему Go-клиенту.
> Реализация планируется в отдельном проекте — все схемы и единицы измерения здесь.

---
**СТАТУС ВЕРИФИКАЦИИ И РЕАЛИЗАЦИИ (poncho-ai, 2026-10-11).** Все ссылки на код и
спеку перепроверены — расхождений с фактами нет (сдвиги номеров строк ≤5).
Сделано в poncho-ai (PG-only):
- §1 бюджет: `GetCampaignBudgets` → POST `/api/advert/v2/budget` (батчи ≤50,
  `budget_v2: 20/min burst 4` в конфигах, колонка `currency` в campaign_budget);
  старый GET-путь удалён. Дедлайн 16.11 закрыт до возобновления promotion.
- §2 ставки: `NormqueryBidItem` + `bid_kopecks` + `currency` (Go-тип, PG-схема
  normquery_bids); комментарий «копейки» у рублёвого `bid` исправлен.
  Поправка к п.1 плана §2: кросс-валидация не `bid*100 ≈ kopecks ±1`, а
  `bid = floor(bid_kopecks/100)` — WB отдаёт УСЕЧЁННЫЕ рубли, расхождение
  до 99 коп. (тест TestNormqueryBidItemUnits).
- §3 кластеры: camelCase-DTO для list (`NormqueryListRequestItem`); `archived`
  сохраняется (`kind` active|excluded|archived вместо is_excluded, бэкфиллом).
- Приложение: лимит v3-fullstats 20→3/min burst 1 (спека :3588). НО:
  `service_advertising.go` НЕ мёртв — инструмент WbCampaignFullstats2Tool
  зарегистрирован в pkg/app/tool_setup.go; пути `/adv/v0/promotion/adverts` и
  `/adv/v2/fullstats` оставлены (живой WB отвечает), помечены комментариями;
  в `/adv/v2/fullstats` замечена опечатка тела `"adverIds"` — проверить живым
  прогоном. `id` vs `ids` у v2-adverts не трогали (см. promotion.go:95).

---

## 1. GET /adv/v1/budget отключается 16 ноября 2026 — миграция на POST /api/advert/v2/budget

### Факт
- Спека: `08-promotion.yaml:1534` — `deprecated: true`, `:1551` — «Метод будет отключен 16 ноября» (release-notes id=582).
- Текущий код: `promotion.go:410` (GET `/adv/v1/budget`).

### Новый контракт (POST /api/advert/v2/budget)
| Параметр | Значение |
|---|---|
| Метод / путь | `POST /api/advert/v2/budget` (`08-promotion.yaml:1470`) |
| Тело запроса | `V2BudgetRequest` (`:4770-4784`): `{ "advertIds": [int, 1..50 шт.] }` — единственное обязательное поле |
| Лимит | 20 req/min, burst 4 |
| Ответ | баланс по каждой переданной кампании (см. `:1476+` в спеке; сверить имена полей при маппинге) |

### План исправления
1. Заменить вызов: GET→POST, путь `/adv/v1/budget` → `/api/advert/v2/budget`, тело `{advertIds: [...]}`.
2. Лимит в конфиге: 20/min, burst 4 (у старого GET был другой).
3. Сверить поля ответа v2 со старым (валюта, «доступно/план/бонус») — имена могли смениться; при отличиях поправить маппинг.
4. После миграции удалить старый путь из клиента — после 16.11 он будет 404.
5. Проверка: запрос по 2 кампаниям (1 существующая + 1 несуществующий ID) — убедиться, как v2 отвечает на частичный успех (ошибка целиком или по-элементно).

---

## 2. Ставки нормграфика: путаница единиц (рубли vs копейки) — риск 100× в деньгах

### Факт (спека)
`GET /adv/v0/normquery/get-bids` возвращает `V0GetNormQueryBidsItem` (`08-promotion.yaml:5494-5523`):
| Поле | Семантика по спеке |
|---|---|
| `bid` | «Текущая ставка в **базовых единицах валюты** (рубли) за тысячу показов», type: integer |
| `bid_kopecks` | «в **разменных единицах — 0,01** от базовой валюты», type: integer |
| `currency` | ISO 4217 (обязательное) |

Установка ставок сегодня: `POST /adv/v1/upd` (`08-promotion.yaml:1250-1291`) принимает **`bid_kopecks`** — копейки.
Новый метод `POST /api/advert/v1/normquery/bids` (`V1SetNormQueryBidsRequestItem`, `:4785-4795`) принимает **`bidMinorUnits`** (копейки) и **camelCase**-ключи: `advertId, nmId, normQuery, bidMinorUnits`.

### Ошибка в коде
`promotion_types.go:524` — комментарий «копейки» для поля `bid`. Спека говорит: `bid` — **рубли** (и притом integer — дробные рубли обрезаются), `bid_kopecks` — копейки. Если значение `bid` (рубли) попадает в поле установки ставки в копейках — ставка занижается в 100 раз; обратно — завышается.

### План исправления
1. **Канонизировать `bid_kopecks`** как единственный источник ставки из get-bids (integer, без потерь). Поле `bid` — только для кросс-валидации: `bid*100 ≈ bid_kopecks (±1)`; расхождение — в лог.
2. При установке ставок: либо `POST /adv/v1/upd` с полем `bid_kopecks`, либо (предпочтительно, новый контракт) `POST /api/advert/v1/normquery/bids` с `bidMinorUnits` и camelCase-телом. Не смешивать: везде, где «ставка на входе», требовать копейки/минорные единицы, в UI — рубли.
3. Убрать поле-парсинг `bid` как «копеек» и комментарий `promotion_types.go:524`.
4. Разобраться с потерей точности: сегодня `bid: integer` в рублях не может передать 5,50 ₽ — ещё одна причина канонизировать минорные единицы.
5. Проверка деньгами: взять кампанию-песочницу, выставить `bid_kopecks=550`, перечитать get-bids → ожидать `bid=5` (integer, усечённо) и `bid_kopecks=550`. Задокументировать усечение.

---

## 3. POST /adv/v0/normquery/list — тело должно быть camelCase

### Факт
- Спека: `V0GetNormQueryListRequestItem` (`08-promotion.yaml:5071-5081`) — обязательные ключи **`advertId`, `nmId`** (camelCase).
- Код: общий `NormqueryItem` (`promotion_types.go:444-445`) шлёт **`advert_id`, `nm_id`** (snake_case) для всех четырёх normquery-методов.

### Важный нюанс — НЕ переименовывать глобально
| Метод | Ключи тела по спеке |
|---|---|
| `POST /adv/v0/normquery/stats` (`:5273+`) | snake_case (`advert_id`, `nm_id`) |
| `POST /adv/v0/normquery/get-bids` (`:5494+`) | snake_case |
| `POST /adv/v0/normquery/get-minus` (`:5536+`) | snake_case |
| `POST /adv/v0/normquery/list` (`:5071`) | **camelCase** (`advertId`, `nmId`) |
| `POST /api/advert/v1/normquery/bids` (`:4785`) | **camelCase** (`advertId`, `nmId`, `bidMinorUnits`) |

Паттерн WB: v0-эндпоинты в основном snake_case, но `list` и все v1+ — camelCase. Один общий DTO здесь вреден.

### План исправления
1. Для `normquery/list` — отдельный request-DTO с ключами `advertId`/`nmId` (маппинг из общего предметного типа).
2. Заодно: в ответе list кластеры содержат массив `archived` (`:4310-4319`) — текущий парсер (`promotion_types.go:491-495`, только active/excluded) его теряет; решить, нужен ли.
3. Проверка: запрос list на кампании с известным кластером — до фикса WB, вероятно, отвечал пусто/частично (молчаливая потеря данных), после — полный набор.

---

## Приложение: другие расхождения в рекламном домене (из того же аудита)

- `GET /adv/v0/promotion/adverts` (`service_advertising.go:35`) и `POST /adv/v2/fullstats` (`:108`) — **отсутствуют в спеке**: актуальны `GET /api/advert/v2/adverts` (параметр `ids`, не `id` — `promotion.go:95` шлёт `id`) и `GET /adv/v3/fullstats`.
- Лимит `/adv/v3/fullstats` в `service_advertising.go:237` захардкожен 20/min при спековых **3/min, burst 1** (`08-promotion.yaml:3588`) — источник 429.
- `GET /api/advert/v2/adverts` у WB жив с параметром `id` вопреки спеке (`ids`) — работает, но при следующем ужесточении отвалится; выправить до спекового вида.

## Чек-лист внедрения (для любого проекта)
- [ ] Бюджет: v2-budget вместо v1 (до 16.11.2026), лимит 20/min burst 4.
- [ ] Ставки: только `bid_kopecks`/`bidMinorUnits`, `bid` (рубли) — валидация `*100`.
- [ ] normquery/list: camelCase-DTO; решение по `archived`.
- [ ] Убрать v0-пути вне спеки (promotion/adverts, fullstats v2) → v2-adverts / v3-fullstats.
- [ ] Лимит fullstats v3: 3/min burst 1.
- [ ] Интеграционный тест-деньги: выставить 550 минорных на песочнице, перечитать, задокументировать усечение `bid`.
