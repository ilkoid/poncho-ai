# Card Consistency Pipeline

Проверка карточек WB на расхождения: **фото + описание vs характеристики**.
Фото — основной источник истины, описание — дополнительный. Характеристикам не верим — их проверяем и корректируем.

## Логика работы

```
Этап 0: карточки ──фильтрация + rule-based──► превью без LLM и БД   (бесплатно)
Этап 1: карточки ──Vision+текст LLM──► расхождения + issues          (~$0.30/2500 шт)
Этап 2: карточки ──подтягивание метрик──► рейтинги, видимость        (бесплатно)
Этап 4: с расхождениями ──LLM──► новые параметры                     (~$0.20)
Этап 5: исправленные ──WB API──► обновление карточек                 (API calls)
```

Каждый этап — отдельный запуск. Между этапами — ревью через SQL. Этапы идемпотентны: повторный пропуск уже обработанных карточек.

**Базы данных:**
- `/var/db/wb-sales.db` — источник (read-only): карточки, фото, характеристики
- `card-analysis.db` — результат: анализ + новые параметры + лог изменений (путь в config.yaml)
- `~/.cache/poncho/char-dict-cache.db` — справочник характеристик WB (read-only)

## Быстрый старт

Все команды — из каталога утилиты. Конфиг подхватывается из `config.yaml` рядом с `main.go`.

```bash
cd cmd/data-analyzers/check-card-consistency
```

### Справочники (без LLM, без записи в БД)

```bash
# Посмотреть предметы WB (для подбора subject_id)
go run . --list-subjects="сабо"
go run . --list-subjects="all"

# Посмотреть характеристики предмета WB
go run . --list-charcs=105
```

### Этап 0: превью (без LLM, без записи в БД)

```bash
go run . --stage 0
go run . --stage 0 --limit 50
```

### Этап 1: аудит (текст + Vision)

```bash
go run . --stage 1
go run . --stage 1 --limit 10
go run . --stage 1 --force                # перепроверить уже обработанные
```

### Посмотреть что нашлось

```bash
sqlite3 /mnt/d/db/card-correction-final.db \
  "SELECT vendor_code, substr(title,1,40), vision_summary
   FROM card_analysis WHERE vision_has_discrepancy = 1 LIMIT 20"
```

### Этап 2: подтянуть метрики (без LLM)

```bash
go run . --stage 2
```

### Этап 4: генерация новых параметров

```bash
go run . --stage 4
go run . --stage 4 --force                # перегенерировать уже готовые

# Diff до/после (без генерации, только просмотр)
go run . --stage 4 --diff
go run . --stage 4 --diff --full          # все характеристики, не только изменённые

# Генерация без смены предмета WB
go run . --stage 4 --keep-subject
```

### Этап 5: обновление WB API

```bash
go run . --stage 5 --mock                 # мок — показать payloads без отправки
go run . --stage 5 --check                # проверить ошибки валидации WB
go run . --stage 5 --yes                  # реальное обновление
go run . --stage 5 --yes --force          # повторно отправить уже обновлённые
```

### Экспорт в XLSX

```bash
go run . --export result.xlsx             # с фильтрами из config.yaml
```

## Флаги

| Флаг | Описание | По умолчанию |
|------|----------|-------------|
| `--stage` | Этап: 0 (превью), 1 (аудит), 2 (метрики), 4 (генерация), 5 (обновление) | 1 |
| `--limit` | Ограничить кол-во карточек (0=все) | 0 |
| `--mock` | Этап 5: мок, без отправки в WB (песочница) | false |
| `--yes` | Этап 5: подтвердить реальное обновление | false |
| `--force` | Повторная обработка уже завершённых карточек (этапы 1, 4, 5) | false |
| `--check` | Этап 5: проверить ошибки валидации WB (без обновления) | false |
| `--list-subjects` | Вывести предметы WB: `"all"` или поисковый запрос | "" |
| `--list-charcs` | Вывести характеристики предмета WB по subject_id | 0 |
| `--diff` | Этап 4: показать diff вместо генерации | false |
| `--full` | Этап 4 --diff: показать ВСЕ характеристики (заполненные + пустые) | false |
| `--keep-subject` | Этап 4: игнорировать смену предмета от LLM | false |
| `--config` | Путь к config.yaml | config.yaml |
| `--export` | Экспорт card_analysis в XLSX | "" |

## Конфигурация (config.yaml)

```yaml
# Бренд — используется в промптах для генерации
#brand: "PlayToday"

# LLM провайдер
llm:
  provider: "openrouter"
  api_key: "${OPENROUTER_API_KEY}"
  base_url: "https://openrouter.ai/api/v1"

# Текстовая модель (этапы 1, 4)
text:
  model: "~google/gemini-flash-latest"
  temperature: 0.4
  max_tokens: 1000
  timeout: 120s

# Vision модель (этап 1)
vision:
  model: "~google/gemini-flash-latest"
  temperature: 0.3
  max_tokens: 2000
  timeout: 120s
  photos_per_card: 5       # сколько фото анализировать (1-5)

# Источник — сырые данные карточек (read-only)
source:
  db_path: "/var/db/wb-sales.db"

# Результат — куда пишем анализ и новые параметры
results:
  db_path: "/mnt/d/db/card-analysis.db"

# Справочник характеристик WB (read-only)
char_dict:
  db_path: "~/.cache/poncho/char-dict-cache.db"

# Фильтрация карточек
filter:
  in_stock: true           # только товары в наличии
  nm_ids: [740178129]      # артикулы WB (приоритетный фильтр)
  vendor_codes: []          # артикулы продавца (8 симв)
  allowed_years: [26]       # год из vendor_code
  subject: ""               # по названию предмета (регистронезависимо)
  subject_ids: []           # по ID предметов WB
  seasons: []               # по сезону из характеристик
  exclude_lengths: [5, 6]   # исключить мусор/устаревшие
  max_visibility: 70.0      # порог видимости (худшие)
  problems:                 # фильтрация по статусам пайплайна
    any_discrepancy: false
    has_parse_errors: false
    pending_wb_update: false

# Параметры анализа
analysis:
  concurrency: 2            # параллельных LLM запросов
  limit: 0                  # 0 = все карточки

# Промпты (пустое поле = hardcoded default)
# Шаблоны используют {placeholder} для подстановки.
prompts:
  stage1_system: |          # Системный промпт Stage 1 (можно переопределить)
    ...
  stage1_user: |            # User промпт Stage 1
    ...

# Правила генерации по аудиториям (для Stage 4)
audience_rules:
  "взрослая женщина":
    title_rules: |
      ...
    desc_rules: |
      ...
    seo_context: "женская одежда, аудитория — взрослая женщина"
```

**Приоритет фильтров:** `nm_ids` > `vendor_codes` > `allowed_years`. Остальные фильтры (`subject`, `subject_ids`, `seasons`, `in_stock`, `exclude_lengths`) применяются дополнительно.

## Что проверяет Stage 1

LLM анализирует фото + описание и сверяет с характеристиками карточки:

- **Тип изделия** — соответствует ли фото заявленному типу
- **Цвет** — порядок важен (доминирующий первый)
- **Декоративный элемент** — принт vs декоративные ленты/кант/тейпы
- **Свойства ткани** — соответствуют ли реальности (поплин не тянется → "эластичный" ошибочно)
- **Рисунок/узор** — на всём изделии, а не на отдельных элементах
- **Назначение** — полнота заполнения (летнее платье = "повседневная" + "летняя" + "пляжная")
- **Пустые характеристики** — если значение можно определить по фото/описанию, пропуск = ошибка
- **Комплектность** — комплект vs единое изделие
- **Целевая аудитория** — по модели и размерной сетке

Результат: JSON с массивом `issues` — каждое расхождение с полями `field`, `card_value`, `correct_value`, `reason`.

## Прогресс и статистика

```
  Filter: 32435 total → 2569 filtered
  Created 3 new rows in card_analysis
  Backfilled metrics: 3 updates
Stage 1: analyzing 3 cards with ~google/gemini-flash-latest
  Resume: 0 already done, 3 pending
  [1/3] 11:24:00 | 1.9s | DISCREPANCY | ETA ~3s
  [2/3] 11:24:01 | 2.4s | ok          | ETA ~2s
  [3/3] 11:24:02 | 2.3s | DISCREPANCY | ETA ~0s
Stage 1 complete: 3 checked, 2 discrepancies (67%), 0 errors

=== Summary ===
Total in DB:          3
Audit done:           3 (discrepancies: 2, 67%)
Params generated:     0
WB updated:           0
```

Per-card строка: `[N/Total] время | длительность | результат | ETA`.

## Таблицы в card-analysis.db

### card_analysis

Одна строка на артикул, растёт по этапам:

| Поле | Этап | Описание |
|------|------|----------|
| nm_id, vendor_code, title, subject_name | 0 | Идентификация карточки |
| audit_done | 1 | Флаг завершения аудита |
| vision_product_type | 1 | Тип изделия по фото |
| vision_attributes | 1 | JSON: цвет, длина, рукав, аудитория и т.д. |
| vision_has_discrepancy | 1 | Найдены расхождения (0/1) |
| vision_summary | 1 | Описание расхождений + `[ISSUES]` JSON массив |
| vision_photo_urls | 1 | URL фото, которые анализировались |
| product_rating, feedback_rating | 2 | Рейтинги WB |
| max_visibility, avg_position | 2 | Метрики поисковой видимости |
| open_card_30d, orders_30d | 2 | Метрики воронки |
| priority_score | 2 | Приоритет (0-2+) |
| generate_done | 4 | Флаг завершения генерации |
| new_title, new_description | 4 | Новые параметры от LLM |
| new_characteristics | 4 | JSON: `[{charc_id, value}]` |
| new_subject_id, new_subject_name | 4 | Новый предмет WB |
| wb_updated | 5 | Обновлено через API (0/1) |
| error_count | — | Счётчик ошибок парсинга (макс 3) |

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
  SUM(CASE WHEN audit_done = 1 THEN 1 END) as audit_done,
  SUM(CASE WHEN vision_has_discrepancy = 1 THEN 1 END) as has_discrepancy,
  SUM(CASE WHEN generate_done = 1 THEN 1 END) as generated,
  SUM(CASE WHEN wb_updated = 1 THEN 1 END) as updated
FROM card_analysis;

-- Самые проблемные типы товаров
SELECT subject_name, COUNT(*) as cnt
FROM card_analysis
WHERE vision_has_discrepancy = 1
GROUP BY subject_name ORDER BY cnt DESC LIMIT 20;

-- Карточки с конкретными issues
SELECT nm_id, vendor_code, vision_summary
FROM card_analysis
WHERE vision_summary LIKE '%[ISSUES]%'
LIMIT 20;

-- Очистить и начать заново
DELETE FROM card_analysis;
DELETE FROM card_change_log;

-- Сбросить Stage 1 для конкретной карточки (перепроверить)
UPDATE card_analysis SET audit_done = 0, vision_summary = '', vision_has_discrepancy = NULL
WHERE nm_id = 740106115;

-- Сбросить Stage 4 (перегенерация)
UPDATE card_analysis SET generate_done = 0, new_title = '', new_description = '', new_characteristics = '';
```

## Стоимость (ориентировочно)

| Этап | Модель | ~2500 карточек |
|------|--------|---------------|
| 0 (превью) | — | бесплатно (нет LLM, нет записи в БД) |
| 2 (метрики) | — | бесплатно (нет LLM) |
| 1 (аудит) | gemini-flash-latest | ~$0.30 |
| 4 (генерация) | gemini-flash-latest | ~$0.20 (только с расхождениями) |
| 5 (обновление) | WB API | бесплатно |
| **Итого** | | **~$0.50** |
