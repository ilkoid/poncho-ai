# fix-card-dimensions

Обновление весо-габаритных характеристик карточек WB из данных 1С WMS.

Данные из XLS (дециметры) → таблица `onec_dimensions` → агрегация MAX по размерам → staging → WB API.

## Примеры запуска

```bash
# 1. Импорт XLS (15 284 строки) в БД
go run . --import-xls "Размеры (дециметры) номенклатуры.xlsx" --db /var/db/wb-sales.db

# 2. Staging: найти карточки с нулевыми габаритами, замапить из onec_dimensions
go run . --stage --db /var/db/wb-sales.db

# 3. Staging через конфиг (с фильтрами)
go run . --config config.yaml --stage

# 4. Diff: показать before/after
go run . --diff --db /var/db/wb-sales.db

# 5. Dry-run: показать JSON payloads без отправки
go run . --apply --dry-run --db /var/db/wb-sales.db

# 6. Применить: отправить в WB API (production!)
go run . --apply --db /var/db/wb-sales.db

# Только конкретные артикулы
go run . --stage --db /var/db/wb-sales.db --config <(echo 'filters: {vendor_codes: ["12611236", "12612158"]}')

# Тест на временной БД
go run . --import-xls file.xlsx --db /tmp/test.db
go run . --stage --db /tmp/test.db
go run . --diff --db /tmp/test.db
```

## Фильтры

Все фильтры AND-combined. Пустое значение = фильтр не применяется.

| Фильтр | Описание |
|--------|----------|
| `in_stock` | Только товары с остатками > 0 |
| `allowed_years` | Год из позиций 2-3 vendor_code (26 = 2026) |
| `exclude_lengths` | Исключить артикулы указанной длины |
| `seasons` | По характеристике "Сезон" |
| `subject` / `subject_ids` | По предмету WB |
| `vendor_codes` | Конкретные артикулы |
| `nm_ids` | Конкретные nm_id |

## Безопасность

- `--dry-run` обязателен перед `--apply` — показывает payloads без отправки
- Столбцы с уже заполненными габаритами не перезаписываются (только нулевые)
- Staging таблица очищается перед каждым `--stage`
