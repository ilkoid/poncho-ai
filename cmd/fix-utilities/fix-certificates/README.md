# fix-certificates

Заполнение сертификатных характеристик карточек WB из данных 1C с полной защитой данных.

## Принцип работы

```
1C API → onec_goods (certificate_*) → --stage → staging table → --diff → --apply --dry-run → --apply
                                               ↓                    ↓            ↓                ↓
                                         фильтр + маппинг      before/after   payload        Smart Merge
                                         (истёкшие → skip)                    без отправки    → WB API
```

### Smart Merge

WB API (`POST /content/v2/cards/update`) **полностью перезаписывает** карточку. Если отправить только сертификатные поля — остальные характеристики, title, description, dimensions, sizes удалятся. Smart Merge решает это:

1. **Проход 1**: Все текущие характеристики итерируются. Сертификатные — заменяются, остальные — передаются как есть
2. **Проход 2**: Сертификатные характеристики (отсутствовали в карточке) добавляются как новые
3. `brand`, `title`, `description`, `dimensions`, `sizes` — всегда включены в payload без изменений

### Фильтрация

Карточки **пропускаются** (не попадают в staging) если:
- В 1C нет `certificate_number` (пустое поле)
- Сертификат уже заполнен на карточке WB (char 15001136 непустой)
- Дата окончания сертификата раньше reference date (`--date` или сегодня)
- Дата начала/окончания = нулевая (`0001-01-01`)

## Маппинг полей

| 1C поле (`onec_goods`) | WB char_id | WB характеристика | Формат |
|------------------------|------------|-------------------|--------|
| `certificate_number` | `15001136` | Номер сертификата соответствия | Строка (как есть из 1C) |
| `certificate_begin` | `15001137` | Дата регистрации сертификата/декларации | DD.MM.YYYY |
| `certificate_end` | `15001138` | Дата окончания действия сертификата/декларации | DD.MM.YYYY |

Дата конвертируется из ISO (`2023-02-07T00:00:00`) → DD.MM.YYYY (`07.02.2023`).

## Конфигурация

```yaml
db_path: "/var/db/wb-sales.db"

# reference_date: "26.05.2026"  # Опционально, default: сегодня. Можно через --date.

wb_update:
  batch_size: 30           # Карточек на один API-запрос
  rate_per_min: 8          # Желаемый rate (req/min)
  rate_burst: 2            # Burst для rate limiter
  api_floor_per_min: 5     # Swagger floor для recovery после 429
  api_floor_burst: 1       # Burst для api floor
  interval_seconds: 8      # Пауза между батчами (sec)
```

## Использование

### Этап 1: Сбор и маппинг

```bash
go run . --stage --config config.yaml

# С явной reference date (по умолчанию — сегодня)
go run . --stage --config config.yaml --date 01.06.2026
```

Создаёт `fix_certificates_staging` в БД, находит карточки без сертификатов, джойнит с `onec_goods`, фильтрует истёкшие, записывает snapshot.

### Проверка staging

```sql
-- Сколько карточек готовы
SELECT status, COUNT(*) FROM fix_certificates_staging GROUP BY status;

-- Примеры данных
SELECT nm_id, vendor_code, onec_certificate_number, onec_certificate_begin, onec_certificate_end
FROM fix_certificates_staging LIMIT 10;

-- Распределение по категориям
SELECT subject_name, COUNT(*) FROM fix_certificates_staging GROUP BY subject_name ORDER BY COUNT(*) DESC;
```

### Diff: before/after

```bash
go run . --diff --config config.yaml
```

Показывает какие сертификатные поля будут добавлены для каждой карточки.

### Этап 2: Применение

```bash
# Dry-run — показать payload без отправки
go run . --apply --dry-run --config config.yaml

# На одной карточке (для тестирования)
# 1. DELETE FROM fix_certificates_staging WHERE nm_id != <ONE_NM_ID>;
# 2. go run . --apply --dry-run --config config.yaml
# 3. Проверить payload — должны быть ВСЕ поля (brand, title, chars, sizes)
# 4. go run . --apply --config config.yaml
```

API-ключ: `WB_API_ANALYTICS_AND_PROMO_KEY` или `WB_API_KEY`.

### Проверка после применения

```sql
SELECT status, COUNT(*) FROM fix_certificates_staging GROUP BY status;
SELECT nm_id, error_msg FROM fix_certificates_staging WHERE status = 'error';

-- Удалить staging-таблицу
DROP TABLE IF EXISTS fix_certificates_staging;
```

## Staging-таблица

```sql
fix_certificates_staging (
    nm_id                   -- WB nmID
    vendor_code             -- артикул продавца
    onec_certificate_number -- номер сертификата из 1C
    onec_certificate_begin  -- дата начала из 1C
    onec_certificate_end    -- дата окончания из 1C
    changes_json            -- [{"char_id":15001136,"old":"","new":"RU C-CN..."}]
    all_chars_json          -- snapshot ВСЕХ характеристик (для Smart Merge)
    sizes_json              -- snapshot размеров
    status                  -- new → sent | error
    error_msg               -- текст ошибки WB API
)
```

`all_chars_json` и `sizes_json` замораживают состояние на момент `--stage`. Даже если данные в БД изменятся до `--apply` — payload будет собран из snapshot.
