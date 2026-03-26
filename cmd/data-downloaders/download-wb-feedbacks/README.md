# download-wb-feedbacks

Загрузка сырых отзывов и вопросов из WB Feedbacks API в SQLite.

Все поля — 1:1 из API, без вычисляемых значений.

## Использование

```bash
cd cmd/data-downloaders/download-wb-feedbacks

# Последние 7 дней (без сегодняшнего)
WB_API_FEEDBACK_KEY=xxx go run . --days=7

# Произвольный период
WB_API_FEEDBACK_KEY=xxx go run . --begin=2025-01-01 --end=2025-01-31

# Только отзывы или только вопросы
WB_API_FEEDBACK_KEY=xxx go run . --days=30 --config=config-no-questions.yaml

# Очистить БД и скачать заново
WB_API_FEEDBACK_KEY=xxx go run . --clean --days=30 --db=archive.db

# Mock mode (без API)
go run . --mock --days=7
```

## CLI флаги

| Флаг | По умолчанию | Описание |
|------|-------------|----------|
| `--days N` | 7 (из config) | Дней от сегодня (исключая сегодня) |
| `--begin DATE` | | Начало периода (YYYY-MM-DD), приоритет над `--days` |
| `--end DATE` | | Конец периода (YYYY-MM-DD) |
| `--db PATH` | `feedbacks.db` | Путь к SQLite |
| `--clean` | | Удалить БД перед загрузкой |
| `--mock` | | Тестовый режим без API |
| `--config PATH` | `config.yaml` | Путь к конфигу |
| `--help` | | Справка |

## Конфигурация

```yaml
wb:
  api_key: ""           # или env WB_API_FEEDBACK_KEY / WB_API_KEY
  rate_limit: 180       # запросов/мин (3 req/sec × 60)
  burst: 6              # burst capacity

feedbacks:
  db_path: "feedbacks.db"
  days: 7
  feedbacks: true       # скачивать отзывы
  questions: true       # скачивать вопросы
```

## Структура БД

**Таблица `feedbacks`** — 39 колонок (все поля из `responseFeedback`):
- Поля API: `id`, `text`, `pros`, `cons`, `product_valuation`, `created_date`, `state`, `user_name`, `was_viewed`, `order_status`, `matching_size`, `color`, `subject_id`, `subject_name` и др.
- `answer_*` — ответ продавца (text, state, editable)
- `product_*` — данные товара (nm_id, imt_id, product_name, supplier_article, brand_name, size)
- `video_*` — видео (preview_image, link, duration_sec)
- `photo_links`, `bables` — JSON массивы как TEXT

**Таблица `questions`** — 16 колонок:
- Поля API: `id`, `text`, `created_date`, `state`, `was_viewed`, `is_warned`
- `answer_*` — ответ продавца (text, editable, create_date)
- `product_*` — данные товара (nm_id, imt_id, product_name, supplier_article, brand_name)

**Индексы**:
- `idx_feedbacks_created_date` — фильтр по дате
- `idx_feedbacks_nm_date` — отзывы товара за период (композитный)
- `idx_questions_created_date` — фильтр по дате
- `idx_questions_nm_date` — вопросы товара за период (композитный)

## Примеры запросов

```sql
-- Отзывы товара за период
SELECT * FROM feedbacks
WHERE product_nm_id = 123456
  AND created_date >= '2025-01-01'
ORDER BY created_date DESC;

-- Неотвеченные отзывы с низкой оценкой
SELECT id, text, product_valuation, product_name, created_date
FROM feedbacks
WHERE answer_text IS NULL
  AND product_valuation <= 3
ORDER BY created_date DESC;

-- Вопросы без ответа
SELECT * FROM questions
WHERE answer_text IS NULL
ORDER BY created_date DESC;

-- Статистика по оценкам
SELECT product_valuation, count(*)
FROM feedbacks
GROUP BY product_valuation;
```

## Ограничения API

| | Отзывы | Вопросы |
|--|--------|---------|
| Endpoint | `/api/v1/feedbacks` | `/api/v1/questions` |
| Max take | 5 000 | 10 000 |
| Max skip | 199 990 | 10 000 |
| Лимит | `take + skip` свободный | `take + skip <= 10 000` |
| Пагинация | линейная | с разбиением периода |
| Rate limit | 3 req/sec, burst 6 | |
