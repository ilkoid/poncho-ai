# download-1c-rests

Загрузчик остатков товаров со складов из 1C RESTs API (`api.playtoday.ru/feeds/ones/rests/`).

## Данные

API возвращает ~82MB JSON с вложенной структурой:
- ~30K товаров (good_guid)
- ~75K SKU (динамические ключи — GUID артикулов)
- ~414K строк остатков по складам

Данные сохраняются в таблицу `onec_rests` в SQLite.

### Таблица onec_rests

| Колонка       | Тип     | Описание                          |
|---------------|---------|-----------------------------------|
| good_guid     | TEXT    | GUID товара (FK → onec_goods.guid) |
| sku_guid      | TEXT    | GUID SKU (размерный ряд)          |
| storage_guid  | TEXT    | GUID склада                       |
| snapshot_date | TEXT    | Дата снепшота (YYYY-MM-DD)        |
| storage_name  | TEXT    | Название склада                   |
| stock         | INT     | Остаток                           |
| reserv        | INT     | В резерве                         |
| free          | INT     | Свободно                          |
| first_stage   | INT     | Первая стадия (bool)              |

Composite PK: `(good_guid, sku_guid, storage_guid, snapshot_date)`

### Сопоставление с WB

```
onec_rests.good_guid → onec_goods.guid → onec_goods.article → cards.vendor_code → cards.nm_id
```

## Retention

- Хранит последние N снепшотов, считая от **вчерашнего дня**
- `retention_days: 7` → вчера + 6 дней назад = 7 снепшотов
- Сегодняшний снепшот всегда сохраняется, но не входит в retention window (день ещё не завершён)
- Старые снепшоты автоматически удаляются после каждого запуска

## Фильтры складов

Фильтрация по складам — через секцию `storage_filter` в YAML:

```yaml
storage_filter:
  guids: ["56806732-8904-11e9-9461-2c768a56a25b"]
  name_patterns: ["Вологда", "Москва"]
```

- Пустые списки = принимать все склады
- Строка проходит фильтр если совпадает GUID **OR** паттерн имени (union)
- Name patterns — case-insensitive substring match

## Использование

```bash
# Через env var
ONEC_API_REST_URL="https://user:pass@api.playtoday.ru/feeds/ones/rests/" \
  go run . --db /tmp/test-1c-rests.db

# Mock mode (без API)
go run . --mock --db /tmp/test-1c-rests.db

# Полная очистка
go run . --clean --db /tmp/test-1c-rests.db
```

## Флаги

| Флаг        | Описание                                  |
|-------------|-------------------------------------------|
| --config    | Путь к конфигу (default: config.yaml)     |
| --db        | Путь к SQLite базе (overrides config)     |
| --clean     | Очистить onec_rests перед загрузкой       |
| --mock      | Тестовые данные без API                   |
| --help, -h  | Справка                                   |

## Env vars

| Переменная          | Описание                              |
|---------------------|---------------------------------------|
| ONEC_API_REST_URL   | URL 1C RESTs API (с basic auth)       |
