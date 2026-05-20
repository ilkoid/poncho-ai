# fix-fill-material-upper

Разовая утилита для заполнения характеристики "Материал верха" у кроссовок на Wildberries.

## Проблема

У ~41% кроссовок (225 из 548 за 2023–2026) на WB не заполнена характеристика "Материал верха" (char_id `15003971`, subject `105`). При этом 224 из 225 имеют данные о составе в 1C (`onec_goods.composition`).

## Двухэтапный процесс

### Этап 1: `--stage` — Сбор и маппинг

Запрашивает из `wb-sales.db` кроссовки без "Материал верха", джойнит с `onec_goods` для получения состава, маппит по ключевым словам и пишет в staging-таблицу `fix_material_upper`.

```bash
go run . --stage
# или с явным путём к БД:
go run . --stage --db /var/db/wb-sales.db
```

**Логика маппинга** (первое совпадение по приоритету):

| Ключевое слово в 1C composition | WB значение |
|---|---|
| натуральная кожа | натуральная кожа |
| искусственная кожа | искусственная кожа |
| натуральный мех | натуральный мех |
| искусственный мех | искусственный мех |
| текстиль | текстиль |
| полиуретан | полиуретан |
| резина | резина |
| эва | ЭВА |
| пвх | ПВХ |
| полиэстер | полиэстер |

Если ни одно ключевое слово не найдено → `UNMAPPED`. Если composition пустая → тоже `UNMAPPED`.

**После этапа 1 — проверить данные:**

```sql
-- Общий обзор
SELECT nm_id, vendor_code, onec_composition, mapped_value FROM fix_material_upper;

-- Нераспознанные
SELECT * FROM fix_material_upper WHERE mapped_value = 'UNMAPPED';

-- Распределение
SELECT mapped_value, COUNT(*) FROM fix_material_upper GROUP BY mapped_value;

-- Поправить вручную
UPDATE fix_material_upper SET mapped_value = 'искусственная кожа' WHERE nm_id = 145598907;
```

### Этап 2: `--apply` — Обновление через WB API

Читает строки со `status = 'new'` и `mapped_value != 'UNMAPPED'`, отправляет батчами через `POST /content/v2/cards/update`.

```bash
# Сначала dry-run — покажет JSON-payload без реальной отправки
go run . --apply --dry-run

# Применить
go run . --apply

# Нужен API-ключ (любой из):
export WB_API_ANALYTICS_AND_PROMO_KEY=...
# или
export WB_API_KEY=...
```

Rate limit: ~8 req/min, батчи по 30 карточек.

**После применения — проверить результат:**

```sql
-- Пересчёт статистики
SELECT char.json_value, COUNT(*)
FROM cards c
JOIN card_characteristics char ON c.nm_id = char.nm_id AND char.name = 'Материал верха'
WHERE c.subject_id = 105
GROUP BY char.json_value;

-- Результаты обновления
SELECT status, COUNT(*) FROM fix_material_upper GROUP BY status;
SELECT * FROM fix_material_upper WHERE status = 'error';

-- Удалить staging-таблицу
DROP TABLE IF EXISTS fix_material_upper;
```

## Staging-таблица

```sql
fix_material_upper (
    nm_id              -- WB nmID
    vendor_code        -- артикул продавца на WB
    title              -- название товара на WB
    subject_name       -- предмет (Кроссовки)
    wb_material_upper  -- текущее значение на WB (пустое = OK, заполнено = запрос ошибочен)
    onec_article       -- артикул из 1C API (подтверждает что JOIN верный)
    onec_composition   -- сырой состав из 1C
    mapped_value       -- смапленное WB-значение (или UNMAPPED)
    char_id            -- 15003971
    status             -- new → sent | error
    error_msg          -- текст ошибки WB API
)
```

## Фильтр кроссовок

Этапные кроссовки отбираются по:
- `cards.subject_id = 105` (Кроссовки)
- `LENGTH(vendor_code) = 8` (8-значные артикулы)
- `SUBSTR(vendor_code, 2, 2) IN ('23','24','25','26')` (годы 2023–2026)
- `card_characteristics` не содержит "Материал верха" или значение пустое
