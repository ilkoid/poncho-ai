# fix-card-fields

YAML-конфигурируемая утилита для точечного изменения характеристик карточек WB с полной защитой данных.

## Принцип работы

```
config.yaml → --stage → staging table → --diff → --apply --dry-run → --apply → --check
                  ↓                         ↓            ↓                ↓         ↓
           фильтр + маппинг         before/after     payload без     Smart Merge  WB
                                                    отправки        → WB API    errors
```

### Smart Merge

WB API (`POST /content/v2/cards/update`) **полностью перезаписывает** карточку. Если отправить только изменённую характеристику — остальные удалятся. Smart Merge решает это:

1. **Проход 1**: Все текущие характеристики итерируются. Целевые — заменяются, остальные — передаются как есть, protected — всегда без изменений
2. **Проход 2**: Новые характеристики (отсутствовали в карточке) добавляются
3. `title`, `description`, `sizes` — всегда включены в payload без изменений
4. `kizMarked` — подтверждение маркировки «Честный ЗНАК». Переносится из `cards.kiz_marked` (3-value logic, см. «Известные ограничения»)

### Известные ограничения

- **`kizMarked` и NULL.** WB `POST /content/v2/cards/update` полностью перезаписывает карточку. Поле подтверждения маркировки «Честный ЗНАК» (`kizMarked`) переносится в payload из `cards.kiz_marked` как `*bool` (3-value logic):
  - `cards.kiz_marked = 1` → `"kizMarked":true` (подтверждение сохранено)
  - `cards.kiz_marked = 0` → `"kizMarked":false` (явный отказ)
  - `cards.kiz_marked IS NULL` → поле опускается → WB применяет default `false`
- **WB НЕ возвращает `kizMarked` в `/content/v2/get/cards/list`**, поэтому для большинства существующих карточек колонка `NULL`. Apply на маркированной карточке (`need_kiz=1`) с `NULL` сбросит подтверждение в ЛК WB. Перед `--apply` проверьте пересечение:
  ```sql
  SELECT nm_id, vendor_code, subject_name FROM cards WHERE need_kiz=1 AND kiz_marked IS NULL;
  ```
  Для суженного scope предзаполните `cards.kiz_marked` вручную (источник истины — ЛК WB продавца) либо начните apply с НЕ-маркированных категорий. Memory: `cardupdate_kizmarked_gap`.

### Защита системных полей

Конфигурационный блоклист `protected_char_ids` запрещает изменение критических полей. Если `fix_rules` случайно содержит protected char_id — конфиг не пройдёт валидацию.

Защищённые по умолчанию: ТНВЭД, ИКПУ, Описание, SKU, Бренд, Размер, Вес товара, сертификаты, декларации, страна производства, НДС, артикул OZON, NTIN, коды ТРУ.

Title и description защищены архитектурно — они не характеристики, а поля карточки. Smart Merge всегда передаёт текущие значения без изменений.

## Конфигурация

```yaml
db_path: "/var/db/wb-sales.db"

fix_rules:
  - char_id: 15003971           # "Материал верха"
    search_value: ""            # Пустое/отсутствующее → заменить
    replace_value: "Искусственная кожа"
    # value_type: "string"      # string (default), number, boolean

filters:
  subject_ids: [105]           # Категория (Кроссовки)
  vendor_codes: []              # Конкретные артикулы
  nm_ids: []                    # Конкретные nm_id (для тестирования на 1 карточке)
  vendor_code_prefix: ""        # Первая цифра артикула
  vendor_code_years: [23, 24]   # Год из позиций 2-3 артикула
  in_stock: false               # Только с остатками > 0

protected_char_ids:
  - 15000001  # ТНВЭД
  - 15001650  # ИКПУ
  - 14177452  # Описание
  - 14177453  # SKU/Артикул продавца
  - 14177446  # Бренд
  - 54337     # Размер
  - 88952     # Вес товара с упаковкой

wb_update:
  batch_size: 30
  rate_per_min: 8
  rate_burst: 2
  api_floor_per_min: 5
  api_floor_burst: 1
  interval_seconds: 8
```

Все фильтры комбинируются через AND. Карточка должна пройти все условия.

## Использование

### Справочник характеристик

```bash
# Узнать charc_id для категории (нужен API-ключ)
go run . --config config.yaml --list-chars 105
```

### Этап 1: Сбор и маппинг

```bash
go run . --config config.yaml --stage
```

Создаёт таблицу `fix_card_fields_staging` в `wb-sales.db`, собирает карточки по фильтрам, проверяет правила, записывает snapshot характеристик.

### Проверка staging

```sql
-- Все отстейдженные карточки
SELECT nm_id, vendor_code, changes_json FROM fix_card_fields_staging;

-- Распределение по правилам
SELECT changes_json, COUNT(*) FROM fix_card_fields_staging GROUP BY changes_json;

-- Поправить вручную перед apply
UPDATE fix_card_fields_staging SET changes_json = '[{"char_id":15003971,"old":"","new":"натуральная кожа"}]' WHERE nm_id = 12345;
```

### Diff: before/after

```bash
go run . --config config.yaml --diff
```

Показывает какие поля меняются для каждой карточки.

### Этап 2: Применение

```bash
# Dry-run — показать payload без отправки
go run . --config config.yaml --apply --dry-run

# Реальная отправка (нужен API-ключ)
go run . --config config.yaml --apply
```

API-ключ: `WB_API_ANALYTICS_AND_PROMO_KEY` или `WB_API_KEY`.

### Проверка ошибок WB

```bash
# Запросить список ошибок валидации карточек с WB
go run . --check

# Не требует конфиг или БД — только API-ключ
# Выводит ошибки на экран + сохраняет в wb-errors-<timestamp>.json
```

### Проверка после применения

```sql
SELECT status, COUNT(*) FROM fix_card_fields_staging GROUP BY status;
SELECT * FROM fix_card_fields_staging WHERE status = 'error';

-- Удалить staging-таблицу
DROP TABLE IF EXISTS fix_card_fields_staging;
```

## Staging-таблица

```sql
fix_card_fields_staging (
    nm_id          -- WB nmID
    vendor_code    -- артикул продавца
    title          -- название товара
    subject_id     -- ID категории
    subject_name   -- название категории
    changes_json   -- [{"char_id":15003971,"old":"","new":"Искусственная кожа"}]
    all_chars_json -- snapshot ВСЕХ характеристик на момент stage
    sizes_json     -- snapshot размеров
    status         -- new → sent | error
    error_msg      -- текст ошибки WB API
)
```

Ключевой момент: `all_chars_json` и `sizes_json` замораживают состояние карточки на момент `--stage`. Даже если данные в БД изменятся до `--apply`, набор карточек и их характеристики останутся неизменными.

## Правила поиска (search_value)

| search_value | Что матчит |
|---|---|
| `""` (пусто) | Характеристика отсутствует, `null`, `[]`, пустая строка |
| `"текст"` | Точное совпадение (case-insensitive) |
| `"42"` | Числовое совпадение (с `value_type: "number"`) |
| `"true"` | Булево совпадение (с `value_type: "boolean"`) |

## Тестирование

```bash
# Юнит-тесты (17 тестов, без API)
go test ./cmd/fix-utilities/fix-card-fields/ -v

# Ручное тестирование на 1 карточке
# 1. В config.yaml: nm_ids: [ОДИН_NM_ID]
# 2. --stage → проверить staging
# 3. --diff → визуально проверить
# 4. --apply --dry-run → проверить payload
# 5. --apply → отправить
# 6. Проверить на WB портале
```
