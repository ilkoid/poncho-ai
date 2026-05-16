# Card Consistency Pipeline

Проверка карточек WB на расхождения: **фото vs описание vs характеристики**.
Фото — истина. Pipeline фильтрует дешёвым текстовым анализом, затем подтверждает через Vision.

## Логика работы

```
Этап 1: ~2500 карточек ──текст LLM──► ~30-50% рискованных     (~$0.30)
Этап 3:   рискованные  ──Vision 2-3 фото──► подтверждённые     (~$1-2)
Этап 4:   подтверждённые ──LLM──► новые параметры              (~$0.20)
Этап 5:   исправленные ──WB API──► обновление карточек         (API calls)
```

Каждый этап — отдельный запуск. Между этапами — ревью через SQL.

**Базы данных:**
- `/var/db/wb-sales.db` — источник (read-only): карточки, фото, характеристики
- `/var/db/card-analysis.db` — результат: анализ + новые параметры + лог изменений

## Быстрый старт

```bash
cd cmd/data-analyzers/check-card-consistency

# Этап 1: текстовый аудит (все SS2026)
go run . --stage 1

# Посмотреть что нашлось
sqlite3 /var/db/card-analysis.db \
  "SELECT vendor_code, substr(title,1,40), text_summary
   FROM card_analysis WHERE text_has_discrepancy = 1 LIMIT 20"

# Этап 3: Vision анализ рискованных (берёт тех у кого discrepancy=1)
go run . --stage 3

# Посмотреть подтверждённые
sqlite3 /var/db/card-analysis.db \
  "SELECT vendor_code, vision_product_type, vision_summary
   FROM card_analysis WHERE vision_has_discrepancy = 1"

# Этап 4: генерация новых параметров
go run . --stage 4

# Посмотреть что будет изменено
sqlite3 /var/db/card-analysis.db \
  "SELECT vendor_code, new_title, length(new_description)
   FROM card_analysis WHERE new_description != ''"

# Этап 5: моковый прогон (БЕЗ отправки в WB)
go run . --stage 5 --mock

# Этап 5: реальное обновление (ТРЕБУЕТ --yes)
go run . --stage 5 --yes
```

## Флаги

| Флаг | Описание | По умолчанию |
|------|----------|-------------|
| `--stage` | Этап: 1, 3, 4, 5 | 1 |
| `--limit` | Ограничить кол-во карточек (0=все) | 0 |
| `--mock` | Этап 5: мок, без отправки в WB | false |
| `--yes` | Этап 5: подтвердить реальное обновление | false |
| `--config` | Путь к config.yaml | config.yaml |

## Конфигурация (config.yaml)

```yaml
# Модель LLM (одна для всех этапов)
llm:
  provider: "openrouter"
  api_key: "${OPENROUTER_API_KEY}"
  base_url: "https://openrouter.ai/api/v1"

text:
  model: "openai/gpt-5.4-nano"
  temperature: 0.2
  max_tokens: 2000
  timeout: 120s

vision:
  model: "openai/gpt-5.4-nano"
  photos_per_card: 3       # сколько фото анализировать

source:
  db_path: "/var/db/wb-sales.db"     # read-only

results:
  db_path: "/var/db/card-analysis.db"

filter:
  allowed_years: [26]      # год из vendor_code (26=2026). Пусто = все
  vendor_codes: []          # конкретные артикулы. Пусто = все

analysis:
  concurrency: 5            # параллельных LLM запросов
  limit: 0                  # 0 = все карточки
```

## Таблицы в card-analysis.db

### card_analysis

Одна строка на артикул, растёт по этапам:

| Поле | Этап | Описание |
|------|------|----------|
| nm_id, vendor_code, title | 0 | Идентификация карточки |
| text_has_discrepancy | 1 | Расхождение текст/характеристики (0/1) |
| text_summary | 1 | Кратко: в чём косяк |
| vision_product_type | 3 | Тип изделия по фото |
| vision_attributes | 3 | JSON: цвет, длина, рукав и т.д. |
| vision_has_discrepancy | 3 | Подтверждённое расхождение (0/1) |
| vision_summary | 3 | Что именно не совпадает |
| new_title, new_description | 4 | Новые параметры от LLM |
| new_characteristics | 4 | JSON: [{id, value}] |
| wb_updated | 5 | Обновлено через API (0/1) |

### card_change_log

Аудит изменений для отката:

```sql
SELECT vendor_code, field, old_value, new_value, changed_at
FROM card_change_log ORDER BY changed_at DESC;
```

## Полезные запросы

```sql
-- Сколько карточек на каждом этапе
SELECT
  COUNT(*) as total,
  SUM(CASE WHEN text_checked_at IS NOT NULL THEN 1 END) as text_done,
  SUM(CASE WHEN text_has_discrepancy = 1 THEN 1 END) as text_disc,
  SUM(CASE WHEN vision_checked_at IS NOT NULL THEN 1 END) as vision_done,
  SUM(CASE WHEN vision_has_discrepancy = 1 THEN 1 END) as vision_disc,
  SUM(CASE WHEN new_title != '' THEN 1 END) as new_params,
  SUM(CASE WHEN wb_updated = 1 THEN 1 END) as updated
FROM card_analysis;

-- Самые проблемные типы товаров
SELECT subject_name, COUNT(*) as cnt
FROM card_analysis
WHERE text_has_discrepancy = 1
GROUP BY subject_name ORDER BY cnt DESC LIMIT 20;

-- Очистить и начать заново
DELETE FROM card_analysis;
DELETE FROM card_change_log;
```

## Стоимость (ориентировочно)

| Этап | Модель | ~2500 карточек |
|------|--------|---------------|
| 1 (text) | gpt-5.4-nano | ~$0.30 |
| 3 (vision) | gpt-5.4-nano | ~$1-2 (только рискованные) |
| 4 (generate) | gpt-5.4-nano | ~$0.20 (только подтверждённые) |
| 5 (update) | WB API | бесплатно |
| **Итого** | | **~$1.50-2.50** |
