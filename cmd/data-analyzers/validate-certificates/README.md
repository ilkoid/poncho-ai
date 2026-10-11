# validate-certificates — валидация сертификатов через ФГИС РОСАККРЕДИТАЦИИ

Проверка реального статуса и сроков действия сертификатов/деклараций соответствия
через ФГИС РОСАККРЕДИТАЦИИ ([pub.fsa.gov.ru](https://pub.fsa.gov.ru/rss/certificate)).

Читает номера из `onec_goods` (read-only), сверяет с реестром ФСА, выводит отчёт
и дописывает RAW-результаты в PostgreSQL (`public.fsa_certificates`, append-only).

## Как это работает

```
┌─────────────────────────────────────────────────────────────────┐
│ validate-certificates                                           │
│                                                                 │
│  1. SQLite (read-only)                                         │
│     SELECT certificate_number FROM onec_goods                  │
│     WHERE has_certificate=1 AND certificate_number != ''       │
│                           ↓                                     │
│  2. Headless Chromium (chromedp)                               │
│     Navigate → pub.fsa.gov.ru/rss/certificate                 │
│     Angular SPA загружается, делает запрос с Bearer токеном   │
│     Перехватываем токен из HTTP-заголовков                     │
│                           ↓                                     │
│  3. Go net/http (с Bearer токеном)                             │
│     Сертификаты: POST /api/v1/rss/common/certificates/get     │
│       filter: {columnsSearch: [{column: "number", search}]}   │
│     Декларации:  POST /api/v1/rds/common/declarations/get     │
│       filter: {columnsSearch: [{name: "number", search}]}     │
│                           ↓                                     │
│  4. Парсинг ответа                                             │
│     Сертификаты: {items: [{number, date, endDate, idStatus}]} │
│     Декларации:  {items: [{number, declDate, declEndDate}]}   │
│     → сравниваем даты с локальными из 1С                      │
│     → считаем дни до истечения                                 │
│                           ↓                                     │
│  5. Отчёт: консольная таблица + CSV                            │
│     Статус: Действующий / Архивный / Не найден                │
│     Совпадение дат: ✓ / ✗                                     │
│     Дни до истечения: N / -N† (истёк)                          │
└─────────────────────────────────────────────────────────────────┘
```

### Ключевые решения

| Аспект | Решение | Почему |
|--------|---------|--------|
| **Token** | chromedp перехватывает Bearer из network-запросов SPA | Spring Security отклоняет curl (403) — токен привязан к браузерной сессии |
| **API calls** | Go `net/http` с извлечённым токеном | Легче и быстрее чем chromedp.Evaluate для каждого запроса |
| **Search body** | `{page: 0 (int), filter: {columnsSearch: [...]}}` | Выяснено reverse engineering Angular-чанков (стандартный Spring Boot не работает) |
| **Задержка** | Random 1–4 сек между запросами | Anti-detection: ритмичные запросы = бан |

## Использование

```bash
# Показать что будет проверено (без API-запросов)
go run . --dry-run

# Проверить конкретный номер (без БД)
go run . --number 'ЕАЭС RU С-CN.ПФ02.В.07011/23'

# Только российские ЕАЭС сертификаты, первые 20, с экспортом CSV + XLSX
go run . --limit 20 --csv /tmp/certs.csv --xlsx /tmp/certs.xlsx

# Через конфиг (config.yaml)
go run . --config config.yaml

# Тестовый прогон с фейковыми данными
go run . --mock

# Все сертификаты из БД (осторожно: ~2771 уникальных номеров, ~1.5-3 часа)
go run .
```

## Форматы вывода

| Формат | Флаг | Описание |
|--------|------|----------|
| **Консоль** | (всегда) | Текстовая таблица + сводка + ⚠ скоро истекают + † истёкшие (топ-20) |
| **CSV** | `--csv file.csv` | Полные данные без обрезания, для фильтрации в Excel |
| **XLSX** | `--xlsx file.xlsx` | Excel с цветовой разметкой: 🟢 действующий, 🔴 истёкший, 🟡 не найден |

## Конфигурация

Утилита поддерживает YAML-конфиг (по умолчанию `config.yaml`) с CLI-овverрайдами.
Фильтрация использует `pkg/filter.Filter` — тот же тип что в загрузчиках и fix-утилитах.

```yaml
# config.yaml
db_path: "/var/db/wb-sales.db"

# Запись RAW-результатов в PostgreSQL (fsa_certificates + analytical- view)
storage:
  backend: "postgres"
  pg_database: "wb_data_prod"   # wb_data_test для смоук-тестов
  pg_password_env: "PG_PWD"

# Фильтрация (pkg/filter.Filter) — все поля опциональны
# AND между полями, OR внутри списков, пустые = игнорируются
filters:
  vendor_codes: []              # Конкретные артикулы
  nm_ids: []                    # Конкретные nm_id
  allowed_years: [25, 26]       # Год из позиций 2-3 артикула
  exclude_lengths: [5, 6]       # Исключить артикулы по длине
  exclude_vendor_codes: []      # Исключить конкретные артикулы
  vendor_code_prefix: ""        # Первая цифра артикула
  subject: ""                   # Предмет WB (точное совпадение)
  subject_ids: []               # WB subject IDs
  seasons: []                   # Сезон из характеристик
  in_stock: false               # Только с остатками
  onec_type: []                 # 1C тип (Обувь/Одежда/Аксессуары)
  category_level1: []           # 1C категория L1
  category_level2: []           # 1C категория L2
  active_only: false            # Исключить заблокированные

limit: 0
delay_min: 1.0
delay_max: 4.0
chromium_path: "/usr/bin/chromium-browser"
csv: ""
```

Приоритет: CLI флаг > config.yaml > defaults.

## CLI Flags

| Flag | Default | Описание |
|------|---------|----------|
| `--config` | `config.yaml` | Путь к конфигурационному файлу |
| `--db` | `/var/db/wb-sales.db` | Путь к базе данных (read-only) |
| `--limit` | `0` | Максимум записей (0 = все) |
| `--number` | `""` | Проверить один номер (без БД) |
| `--delay` | `1,4` | Мин,макс задержка в секундах |
| `--chromium` | auto | Путь к Chromium |
| `--pg-database` | `wb_data_prod` | Целевая PG база записи (overrides config; `$PGDATABASE`, если задан, перекрывает и флаг — конвенция env > YAML > default) |
| `--no-pg` | `false` | Не записывать результаты в PostgreSQL |
| `--csv` | `""` | Экспорт результатов в CSV |
| `--xlsx` | `""` | Экспорт результатов в XLSX (Excel с форматированием) |
| `--mock` | `false` | Фейковые данные, без браузера |
| `--dry-run` | `false` | Показать список без проверки |

Фильтры настраиваются через `config.yaml` → секция `filters` (см. выше).

## Статусы ФСА

API возвращает `idStatus` — числовой код:

| id | Название | Значение |
|----|----------|----------|
| 1 | Архивный | Истёк / прекращён |
| 2 | Приостановлен | Временно приостановлен |
| 3 | Возобновлён | Был приостановлен, восстановлен |
| 4 | Прекращён | Окончательно прекращён |
| 5 | Аннулирован | Признан недействительным |
| 6 | Действующий | Активный, в силе |
| 7 | Продлён | Срок действия продлён |
| 8 | Внесены изменения | Изменены данные |
| 10 | На рассмотрении | Заявка в обработке |
| 11 | Зарегистрирован | Зарегистрирован в реестре |
| 12 | Выдан | Документ выдан |
| 14 | Окончание срока | Срок действия завершён |

## Формат данных

### Ответ API FSA (`POST /certificates/get`)

```json
{
  "items": [{
    "id": 3254311,
    "number": "ЕАЭС RU С-CN.ПФ02.В.07011/23",
    "date": "2023-05-25",
    "endDate": "24.05.2026",
    "idStatus": 1,
    "certType": "Сертификат соответствия требованиям ТР ЕАЭС...",
    "certObjectType": "Серийный выпуск",
    "applicantLegalSubjectType": "...",
    "technicalReglaments": "..."
  }],
  "totalCount": 1
}
```

### CSV-экспорт

Колонки: `article, good_name, cert_type, cert_number, local_end, fsa_status, fsa_end_date, date_match, days_remaining, error`

## Persist в PostgreSQL (RAW-слой)

Каждый живой прогон дописывает результаты в `public.fsa_certificates` — одна строка
на один поиск одного номера (append-only, история смен статусов бесплатно). Даты
хранятся как вернул API; фильтрация/нормализация — задача consumers, не хранилища.

| Колонка | Что хранит |
|---------|------------|
| `certificate_number`, `doc_kind` | номер, `certificate` \| `declaration` |
| `fsa_id`, `reg_date`, `end_date`, `id_status`, `cert_type`, `object_type` | ответ реестра как есть |
| `found`, `exact_match` | найден ли; точный матч или первый результат (approx) |
| `skipped_reason` | непусто = не искали (не-RU формат KG/KZ/legacy) |
| `error` | непусто = API-запрос упал |
| `checked_at` | момент проверки (`TIMESTAMPTZ`) |

Поверх таблицы создаётся view **`analytical.certificate_validity`** (первый view
аналитического слоя): последняя проверка номера (`DISTINCT ON`) + `LEFT JOIN
onec_goods` + вычисляемые на лету `verdict`, `days_remaining`, `date_match`,
`fsa_status` — всегда свежи относительно текущей даты, ничего не протухает:

```sql
SELECT article, certificate_number, fsa_status, days_remaining, verdict
FROM analytical.certificate_validity
WHERE verdict IN ('❌ Протух', '⚠️ Скоро истекает');
```

Правила записи:

- `--mock` **никогда** не пишет в PG; `--number` — эфемерный пробник, тоже не пишет.
- Прерванный прогон (Ctrl+C) не пишет partial-результаты (проверка `ctx.Err()`), но отчёт печатается.
- Ошибка записи — fatal ДО печати отчёта (результаты не теряются молча); ремедиация — перезапуск с `--no-pg`.
- DSN из env: `PGHOST`/`PGPORT`/`PGUSER`/`PG_PWD`; `$PGDATABASE` перекрывает `pg_database` конфига.
- Read-only права: `InitSchema` выдаёт `USAGE`+`SELECT` на схему `analytical` ролям из env `ANALYTICAL_RO_ROLES` (default `mcp_ro`; роли без login пропускаются; пустое значение = гранты off) — MCP-роль видит view, но не может писать.
- Смоук-тесты — только в `wb_data_test` (`--pg-database wb_data_test`).

## Сертификаты и декларации

Утилита проверяет **оба** типа документов через server-side API:

| Тип | API Endpoint | Скорость |
|-----|-------------|----------|
| Сертификаты (`ЕАЭС RU С-...`) | `POST /api/v1/rss/common/certificates/get` | ~1-2 сек/шт |
| Декларации (`ЕАЭС N RU Д-...`) | `POST /api/v1/rds/common/declarations/get` | ~1-2 сек/шт |

Оба метода используют один и тот же Bearer-токен (захватывается через headless Chrome при старте).
Утилита автоматически определяет тип по полю `certificate` из 1С и использует соответствующий endpoint.

**Различия API:** сертификаты используют `"column": "number"` в фильтре, декларации — `"name": "number", "type": 0`.

## Ограничения

1. **KG417**: Киргизские сертификаты (`ЕАЭС KG417/...`) не находятся в российском реестре ФСА.

2. **Старые форматы**: Номера типа `008и-08-2018`, `122591`, `ЦС/383/25` — не в реестре.

3. **Token TTL**: Время жизни JWT-токена неизвестно. Если токен истечёт во время работы,
   API начнёт возвращать ошибки. Перезапустите утилиту.

4. **FSA uptime**: Сайт pub.fsa.gov.ru иногда возвращает 503. Утилита выводит ошибку
   для каждого неудачного запроса.

## Производительность

| Метрика | Значение |
|---------|----------|
| Token acquisition | ~12 сек (headless Chromium) |
| Один запрос (серт/декл) | 1–4 сек (включая задержку) |
| 100 номеров | ~3–8 минут |
| 1000 номеров | ~30–60 минут |
| Уникальных в БД | ~2771 номеров (из 23K записей) |
| Дедупликация | Одинаковым номерам — один API-запрос |
