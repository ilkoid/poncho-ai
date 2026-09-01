# dev_wb_chat_robot.md — AI-автоответчик на вопросы покупателей о статусе FBS-заказов (чаты WB)

**Дата:** 2026-08-29
**Статус:** План (Stage 0 одобрён к реализации; Stage 1–2 — направление, не реализуются)
**Приоритет:** Доменный. Вопросы слоёв данных → `dev_data_layers.md`; PG-механика → `dev_v2_postgres.md`; размещение кода → `dev_best_practices.md`.

**Зафиксированные решения (владелец, 2026-08-29):**
- **Режим:** shadow — робот пишет черновики в PG и **ничего не отправляет**.
- **Охват:** только чаты, у которых rid найден в `fbs_orders` (FBS-заказы). FBO-заказы и предпродажные вопросы остаются людям.
- **PG-first:** новые таблицы только в PostgreSQL, SQLite вне скоупа.

**Связанные документы:**
- [dev_data_layers.md](dev_data_layers.md) — RAW/RECOMMENDATION + action-loop: схема таблиц робота ложится на эту модель.
- [dev_v2_postgres.md](dev_v2_postgres.md) — механика Writer/репо одного домена в PG.
- [dev_best_practices.md](dev_best_practices.md) — размещение кода, паттерны.
- [dev_swagger_reusable_packages.md](dev_swagger_reusable_packages.md) — WB write-безопасность (почему Stage 0 не шлёт ничего).
- Локальный Swagger: `docs/wb_api_swagger/09-communications.yaml`, `03-orders-fbs.yaml`.

---

## 1. Принцип

Покупатели спрашивают «где мой заказ» в **чатах продавца с покупателем** (не в фиде вопросов!). Робот:

1. следит за новыми сообщениями (поллинг событий чатов),
2. сам извлекает rid заказа из подписи чата,
3. поднимает статус заказа — и статус продавца (`supplierStatus`), и статус обработки WB (`wbStatus`) — плюс таймлайн переходов,
4. генерирует LLM-чepновик ответа: **честный статус** + обоснованное «успокоительное» (только на фактах),
5. кладёт черновик в `recommendation.chat_reply` со `status=pending`. **Никакой отправки.**

```
buyer-chat-api ──events──► watcher (тикер 60с)
                               │ dedup event_id, sender=client
                               ▼
                     rid из replySign ──► fbs_orders.id (PG, индекс по rid)
                               │                    │
                               │            POST /api/v3/orders/status (живой)
                               │            fbs_orders_status_log (таймлайн, медиана ETA)
                               ▼
                     LLM (llm.Provider, промпт ru) ──► черновик ≤1000 симв.
                               ▼
                recommendation.chat_reply (status=pending) ──► ревью человеком
```

---

## 2. Доказательная база

Всё ниже проверено живыми запросами 2026-08-29 (curl + PG readonly) или по локальному Swagger. Цитаты — `файл:строка`.

### 2.1 Где на самом деле живут «вопросы о статусе заказа»

| Канал | Что это | Связь с заказом | Вывод |
|---|---|---|---|
| `feedbacks-api /api/v1/questions` (`09-communications.yaml:322`) | Вопросы **о карточке товара** (размеры, поступления, цвет) | Нет — только `productDetails.nmId` | Не подходит: живая проверка, 1 из 30 вопросов лишь косвенно «про заказ» |
| `buyer-chat-api /api/v1/seller/chats` (`09-communications.yaml:2462`) | **Чаты с покупателями** — 5000 чатов у аккаунта, полны «когда передадите в доставку?» | **Да — через rid в `replySign`** | Настоящий канал робота |
| `marketplace-api /api/v3/orders/client` (`03-orders-fbs.yaml:2022`) | ФИО+телефон покупателя | Только для трансграничных поставок из Турции (`:2041`); живой тест на внутренние заказы → `{"orders":null}` | Выведен из рассмотрения |

### 2.2 Чаты: ключевые факты API

- Сервер `https://buyer-chat-api.wildberries.ru`, авторизация — `WB_API_KEY` (**проверено живьём**, HTTP 200).
- `GET /api/v1/seller/chats` — список всех чатов; `Chat` = `chatID`, `replySign`, `clientName`, `goodCard` (nmID/price), `lastMessage` (`:3384`).
- `GET /api/v1/seller/events?next=<ms>` — события всех чатов, курсорный поллинг: первый запрос без `next`, далее `next` из ответа, пока `totalEvents != 0` (`:2512-2516`). Событие содержит `eventID` (дедуп), `sender: client|seller`, `source` (`rusite`, `seller-public-api`), `isNewChat`, текст, вложения (`:3420`).
- `POST /api/v1/seller/message` (`:2658`) — multipart-form: `replySign` (обязателен) + `message` ≤ **1000 символов** (+ файлы JPEG/PNG/PDF ≤5МБ). `replySign` для нового чата — из события с `isNewChat=true`, для старого — из `/chats`. **На Stage 0 не реализуется.**
- Лимиты: 10 запросов / 10 сек (персональный) → поллинг раз в 30–60 с с большим запасом.

### 2.3 Связь чат ↔ заказ (ядро всей идеи, проверена живьём)

`replySign` чата содержит rid заказа третьим полем через `:`:

```
1:a3819aa7-...:eBX.i1d467f77dac65acbcba0367e0546756f.0.0:411c15a7...
                  └───────────── rid (= srid статистики) ─────────┘
```

Живая проверка (3 чата с вопросами о доставке → PG):

| Вопрос покупателя | rid из replySign | `fbs_orders` | nm_id чата = заказу | Статус |
|---|---|---|---|---|
| «когда передадите в доставку? почему так долго» | `eBh.r2ee1a97…` | id 5583077398, 25.08 | ✔ 498659660 | complete / waiting |
| «если доставки не будет, отмените» | `ebW.rc8c009c…` | id 5615470678, 29.08 | ✔ 259361639 | confirm / waiting |
| «посмотрите моё сообщение» | `eBX.i1d467f7…` | id 5570470567, 24.08 | ✔ 175066000 | complete / waiting |

Совпадение `nm_id` во всех трёх случаях исключает случайность. В PG уже есть `idx_fbs_orders_rid`.

### 2.4 Статусы FBS-заказов

- `POST /api/v3/orders/status` (`03-orders-fbs.yaml:548`) — по числовым ID сборочных заданий (до 1000 за раз), возвращает `supplierStatus` + `wbStatus` + `isCancellable`. Лимит 300 req/min — для точечных ответов робота за глаза.
- **Принимает только int64 ID** — ни rid, ни orderUid, ни gNumber напрямую; маппинг rid→id через PG.
- Семантика статусов — из `:563-593`: `supplierStatus` — действие продавца (new/confirm/complete/cancel); `wbStatus` — система WB (waiting → sorted → ready_for_pickup → sold; отмены: canceled_by_client, declined_by_client, defect…).
- PG-таблицы уже синхронизируются ночным пайплайном (`download-all-pg.sh`, 00:05): `fbs_orders`, `fbs_orders_status` (свежесть до суток), `fbs_orders_status_log` — **история переходов** → таймлайн для покупателя + медиана ETA.

---

## 3. Архитектура Stage 0 (по слоям репо)

### 3.1 `pkg/wb/service_buyerchat.go` — новый сервис

- `GetEvents(ctx, next int64)` и `GetChats(ctx)` — read-only обёртки над buyer-chat-api.
- Хост `buyer-chat-api.wildberries.ru` добавить в клиент (сейчас в `pkg/wb/` его нет — есть feedbacks, marketplace и др.).
- Лимит-флор из спеки: 10 req/10s; toolID **`buyer_chat_events`** — строго одинаковый в `SetRateLimit` и на пути запроса (gotcha ToolID-mismatch из AGENTS.md).
- `SendMessage` **не реализуется** (Stage 0).

### 3.2 PG-схема (новый репо в `pkg/storage/postgres/`)

**RAW — `public.chat_events`** (append-only, как факты wbscraper):

| поле | тип | смысл |
|---|---|---|
| `event_id` | text PK | из события |
| `chat_id` | text | `1:<uuid>` |
| `sender` | text | client / seller |
| `source` | text | rusite / seller-public-api / … |
| `text` | text | сообщение |
| `nm_id` | bigint | из `goodCard` |
| `rid` | text NULL | из replySign (3-е поле), только у чатов с заказом |
| `add_time` | timestamptz | время события |
| `raw` | jsonb | событие целиком |

**RECOMMENDATION — `recommendation.chat_reply`** (обязательные `status` + аудит по `dev_data_layers.md`):

| поле | тип | смысл |
|---|---|---|
| `chat_id`, `event_id` | text | на что отвечаем |
| `class` | text | `order_status` / `other` |
| `facts` | jsonb | что подсунули LLM: статусы, таймлайн, ETA-медиана |
| `draft_text` | text ≤1000 | черновик (лимит WB) |
| `status` | text | `pending` → `applied` / `rejected` |
| `created_at`, `sent_at`, `wb_response` | — | аудит (sent_at/wb_response заполняются только на Stage 1+) |

DDL-механика: `CREATE SCHEMA recommendation` при первой миграции; каждый `CREATE INDEX` отдельным `Exec()`; ID — BIGINT, булевы — BOOLEAN (PG-gotcha из AGENTS.md).

### 3.3 Демон `cmd/data-downloaders/watch-wb-chats/`

- Шаблон жизненного цикла — `cmd/data-downloaders/wb-scraper-collector/` (долгоживущий процесс, os/signal, graceful shutdown). Cron-библиотек нет и не вводим: сам демон тикает.
- Тикер 60с → п.4 (петля).
- Флаги: `--config`, `--once` (один проход — для отладки/cron-режима), `--mock` (Discard-писатель по канону v2).
- `dllog` для строк прогресса, никаких `fmt.Printf` в pkg-слое.

### 3.4 LLM

- `llm.Provider` (`pkg/llm/provider.go:11`), OpenAI-совместимый клиент уже есть.
- Промпт: `prompts/wb/chats/ru.yaml` по образцу `prompts/wb/feedbacks/` (там прецедент LLM-ответов на коммуникации WB).
- Прецедент-референс кода: `cmd/data-analyzers/analyze-wb-feedbacks/analyzer.go` (классификация + генерация над коммуникациями).

### 3.5 Ревью-выжимка

Консольный отчёт (по образцу анализаторов из `cmd/data-analyzers/`): за период — сколько событий, доля `order_status`, черновики рядом с их `facts` для ревью человеком. Это критерий перехода на Stage 1.

---

## 4. Петля работы (пошагово)

1. `GetEvents(next)` → события; курсор сохраняем (место хранения — конфиг демона или служебная таблица; решить при реализации).
2. Дедуп по `event_id` (INSERT … ON CONFLICT DO NOTHING).
3. Фильтр: `sender = client`. События `sender=seller` пишутся в raw (для полноты диалога), но не обрабатываются → защита от самопинга.
4. Извлечь rid из `replySign`; нет rid → предпродажный вопрос, черновика нет (событие сохранено).
5. rid → `fbs_orders.id` (индекс `idx_fbs_orders_rid`); не найден → FBO-заказ или заказ вне окна выгрузки, черновика нет.
6. Живой `POST /api/v3/orders/status` по id (свежесть важна для честного ответа).
7. Собрать `facts`: supplierStatus, wbStatus, isCancellable, `created_at` заказа, таймлайн из `fbs_orders_status_log`, медиана `waiting→ready_for_pickup` по складу/ПВЗ из той же таблицы.
8. Классификация вопроса LLM: `order_status` → генерить черновик; возврат/брак/деньги/юридика (`other`) → черновик-эскалация «передал специалисту» или без черновика (решить на ревью Stage 0).
9. Черновик ≤1000 символов → INSERT `recommendation.chat_reply status=pending`.

---

## 5. Правила генерации ответов (анти-галлюцинация)

- **Только факты из `facts`.** Робот не знает ничего, кроме подсунутых статусов и дат. Промпт обязан запрещать даты/сроки, которых нет в фактах.
- **ETA — не выдумывать, а считать**: медиана времени `waiting → ready_for_pickup` из собственной истории `fbs_orders_status_log` (по складу/ПВЗ). Формулировка «обычно на этом этапе 2–3 дня», не «придёт 2 сентября».
- **Честный статус** — человеческая расшифровка `wbStatus` + таймлайн («передан в доставку 26.08, прибыл в ПВЗ 28.08»).
- **«Успокоительное»** — только с опорой на факт (заказ реально движется, переходы по датам).
- Отмена/проблемы — без обещаний от имени WB: «заказ отменён покупателем при получении» констатируем, возвраты — человеку.
- Один ответ на одно сообщение клиента; повторное «пингующее» сообщение в тот же чат — не раньше, чем придёт новое событие клиента.

---

## 6. Риски и защиты

| Риск | Защита |
|---|---|
| Запись наружу живым людям (чувствительнее WB Content API) | Stage 0 физически не умеет отправлять: `SendMessage` не реализован. Stage 1+ — только через action-loop (`pending → approve → send`) с аудитом |
| Галлюцинация статуса/срока | Грунтинг п.5; `facts` пишутся в БД рядом с черновиком — каждое утверждение проверяемо |
| Самопинг / циклы | Игнорировать `sender=seller` (`source=seller-public-api`) |
| Повторные ответы | Дедуп `event_id`; один черновик на событие клиента |
| Чат не про заказ (предпродажа / FBO) | Нет rid или нет заказа в `fbs_orders` → только raw, без черновика |
| 429 / лимиты | Флор 10 req/10s в адаптивном лимитере `pkg/wb`; поллинг 60с |
| Дрейф API | Swagger локальный (`docs/wb_api_swagger/`) — первоисточник; живые smoke-тесты `--once` |

---

## 7. Roadmap

| Стадия | Что | Критерий перехода дальше |
|---|---|---|
| **Stage 0 (этот план)** | shadow: события → черновики → `pending`. Ничего не отправляется | Ревью выжимки: черновики по классу `order_status` адекватны ≥ несколько недель |
| Stage 1 | draft+approve: человек approves из выжимки → `SendMessage` → `status=applied + sent_at + wb_response` | Доля правок человеком стабильно низкая |
| Stage 2 | авто для `class=order_status` с высокой уверенностью; всё остальное — как Stage 1 | Мониторинг, kill-switch флагом |

Каждая стадия — обратимая точка; следующая не начинается без явного решения владельца.

---

## 8. Открытые проверки (первыми шагами реализации)

1. **Живой `GET /api/v1/seller/events`**: несёт ли вложение `goodCard` поле `rid` прямо в событии (пример спеки `09-communications.yaml:2537-2560` это показывает)? Если да — шаг маппинга rid вообще без обращения к `/chats`. (Сейчас проверить не удалось: plan-mode блокировал curl; см. также другой формат rid в примере — hex без префикса — уточнить соответствие.)
2. **Свежесть `replySign`**: для старых чатов подпись берётся из `/chats` — проверить TTL подписи на практике.
3. **Объём**: замерить реальный поток событий за сутки (поллинг уже на Stage 0 это покажет) — от него зависит, нужен ли батчинг LLM-вызовов.
4. **Хранение курсора `next`** между рестартами демона.

---

## 9. Non-goals (Stage 0)

- Любая отправка сообщений (`SendMessage` не существует в коде).
- FBO-заказы, предпродажные вопросы, возвраты/брак — не классифицируются в авто-ответ.
- SQLite-бэкенд, авто-режим, UI одобрения.
- Изменение `dev_manifest.md` (карта документов) — отдельное решение владельца карты.

---

## 10. Чек-лист реализации Stage 0

- [ ] `pkg/wb/service_buyerchat.go`: GetEvents/GetChats + хост + toolID `buyer_chat_events`
- [ ] PG-миграция: `public.chat_events`, `recommendation.chat_reply` (+ `CREATE SCHEMA recommendation`)
- [ ] `cmd/data-downloaders/watch-wb-chats/`: тикер/`--once`, дедуп, фильтры, rid→id, живой статус, `facts`
- [ ] Промпт `prompts/wb/chats/ru.yaml` + классификация + генерация ≤1000 симв.
- [ ] Ревью-выжимка
- [ ] Тесты: парсинг rid из replySign, дедуп, фильтр sender, лимит длины; PG-схема на тестовой базе
- [ ] `go build ./cmd/...`, `go test ./...`
