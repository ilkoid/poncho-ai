# collect-card-characteristics

Сбор исторических характеристик WB по предметам (subject_name) с выгрузкой в XLSX.

Утилита читает все характеристики из `card_characteristics`, группирует по `subject_name`, собирает уникальные значения с дедупликацией и выгружает в XLSX: каждый лист = один предмет WB, строки = характеристики, значения через запятую.

**Цель:** иметь справочник всех реально использованных характеристик и их значений для заполнения новых карточек.

## Источник данных

- **БД:** `wb-sales.db` (read-only, `?mode=ro&_busy_timeout=5000`)
- **Таблицы:** `cards` + `card_characteristics` (JOIN по `nm_id`)
- **Без API-вызовов** — работает полностью офлайн

## Логика обработки

### SQL-запрос (один на всё)

```sql
SELECT c.subject_name,
       c.subject_id,
       cc.char_id,
       cc.name,
       GROUP_CONCAT(DISTINCT je.value) AS all_values,
       COUNT(DISTINCT c.nm_id)         AS card_count
FROM cards c
JOIN card_characteristics cc ON c.nm_id = cc.nm_id,
     json_each(cc.json_value) je
WHERE {filter.Where}
  AND c.subject_name IS NOT NULL AND c.subject_name != ''
GROUP BY c.subject_name, c.subject_id, cc.char_id, cc.name
ORDER BY c.subject_name, card_count DESC
```

### Дедупликация (двойная)

1. **SQL:** `GROUP_CONCAT(DISTINCT je.value)` — группирует уникальные значения в строку
2. **Go:** `map[string]struct{}` — парсит CSV строку, гарантирует уникальность, сортирует по алфавиту

### Структура XLSX

| Лист | Содержание |
|------|-----------|
| **Сводка** | Таблица: Subject ID, Предмет (гиперссылка), Карточек, Характеристик, Всего значений |
| **Предмет 1..N** | Строки: характеристика → Char ID → все значения (через запятую) → уникальных → карточек |

**Столбцы per-subject:**

| A | B | C | D | E |
|---|---|---|---|---|
| Характеристика | Char ID | Все значения | Уникальных | Карточек |

Строки отсортированы по `CardCount DESC` — самые популярные характеристики сверху.

### Гиперссылки в сводке

В листе «Сводка» колонка «Предмет» содержит кликабельные гиперссылки — клик переводит на лист этого предмета. Стиль: синий подчеркнутый текст.

### Имена листов

Лимит Excel = 31 символ. Руно-осознанная обрезка:
- `len([]rune) ≤ 31` — как есть
- иначе `[]rune[:28] + "..."`
- при совпадении — суффикс `" (2)"`, `" (3)"` и т.д.

## Пакетный экспорт (`--export-all`)

Для передачи данных клиентам без доступа к базе. Разбивает все предметы на несколько XLSX файлов.

### Как работает

1. Фильтры определяют **какие** предметы попадают в экспорт (как обычно)
2. `items_per_file` (default: 30) определяет сколько предметов в одном файле
3. Предметы разбиваются равномерно: первые N → файл 1, следующие N → файл 2, и т.д.
4. Каждый файл **автономен**: своя сводка + свои листы предметов

### Именование файлов

При `--output /tmp/chars.xlsx` и 75 предметах:

```
/tmp/chars_part_01.xlsx   (предметы 1-30)
/tmp/chars_part_02.xlsx   (предметы 31-60)
/tmp/chars_part_03.xlsx   (предметы 61-75)
```

### Конфигурация

```yaml
# config.yaml
export_all: false          # включить пакетный режим
items_per_file: 30         # предметов на файл (default: 30)
```

CLI: `--export-all` + `--items-per-file N`

## Фильтрация

Через `pkg/filter.Filter` — AND между полями, OR внутри списков. Настройка через `config.yaml` и CLI флаги (CLI приоритетнее).

| Поле | CLI флаг | Описание |
|------|----------|----------|
| `allowed_years` | `--years 24,25,26` | Год из vendor_code (позиции 2-3) |
| `subject_ids` | `--subject-ids 540,541` | ID предметов WB |
| `subject` | `--subject "Кроссовки"` | Точное совпадение имени (case-insensitive) |
| `vendor_codes` | `--vendor-codes A,B` | Артикулы продавца |
| `nm_ids` | `--nm-ids 123,456` | nm_id |
| `seasons` | `--seasons зима,лето` | Сезон из характеристик |
| `in_stock` | config only | Только с остатками |
| `onec_type` | config only | Тип 1C |
| `category_level1/2` | config only | Категории 1C |
| `active_only` | config only | Исключить заблокированные |

## Запуск

```bash
# Все предметы, годы 24-26 (по умолчанию)
go run ./cmd/data-analyzers/collect-card-characteristics/ --db /var/db/wb-sales.db --output /tmp/chars.xlsx

# Найти ID предмета по подстроке
go run ./cmd/data-analyzers/collect-card-characteristics/ --list-subjects кросс

# Показать все предметы
go run ./cmd/data-analyzers/collect-card-characteristics/ --list-subjects all

# Один предмет по ID
go run ./cmd/data-analyzers/collect-card-characteristics/ --subject-ids 540 --output /tmp/chars_540.xlsx

# Консольный вывод без XLSX
go run ./cmd/data-analyzers/collect-card-characteristics/ --subject-ids 540 --dry-run

# Тестовый прогон (без БД)
go run ./cmd/data-analyzers/collect-card-characteristics/ --mock --dry-run

# Тестовый XLSX
go run ./cmd/data-analyzers/collect-card-characteristics/ --mock --output /tmp/test_chars.xlsx

# Только 25-26 годы + сезон
go run ./cmd/data-analyzers/collect-card-characteristics/ --years 25,26 --seasons зима --output /tmp/chars_winter.xlsx

# С config.yaml (для сложных фильтров)
go run ./cmd/data-analyzers/collect-card-characteristics/ --config ./cmd/data-analyzers/collect-card-characteristics/config.yaml --output /tmp/chars.xlsx
```

### Пакетный экспорт

```bash
# Все предметы, по 30 на файл
go run ./cmd/data-analyzers/collect-card-characteristics/ --export-all --output /tmp/chars.xlsx

# По 20 предметов на файл
go run ./cmd/data-analyzers/collect-card-characteristics/ --export-all --items-per-file 20 --output /tmp/chars.xlsx

# План без записи файлов
go run ./cmd/data-analyzers/collect-card-characteristics/ --export-all --dry-run

# Демо с mock данными (3 файла: 5+5+3 предметов)
go run ./cmd/data-analyzers/collect-card-characteristics/ --mock --export-all --items-per-file 5 --output /tmp/demo.xlsx
```

## Файлы

| Файл | Назначение |
|------|-----------|
| `main.go` | CLI driver: флаги, конфиг, оркестрация, `--list-subjects`, `--export-all`, `--mock`, `--version` |
| `query.go` | `SourceRepo` (read-only, modernc.org/sqlite), `LoadCharacteristics()` через `filter.BuildSQL()`, `LoadAllSubjects()` |
| `storage.go` | `buildXLSX()` (с гиперссылками), `ExportXLSX()`, `ExportXLSXBatch()`, rune-aware обрезка имён |
| `config.yaml` | Конфиг по умолчанию (db_path, filters, output, export_all, items_per_file) |
| `build.sh` | Кросс-компиляция под Mac (Intel + ARM) и Windows |
| `README.md` | Документация |

## Кросс-компиляция (Mac + Windows)

Утилита использует **pure Go** SQLite драйвер (`modernc.org/sqlite`) вместо CGo-зависимого `mattn/go-sqlite3`. Это позволяет кросс-компилировать без C-компиляторов:

```bash
# Собрать все платформы
bash cmd/data-analyzers/collect-card-characteristics/build.sh

# Результат:
#   collect-card-characteristics_darwin_amd64      (Mac Intel)
#   collect-card-characteristics_darwin_arm64      (Mac Apple Silicon)
#   collect-card-characteristics_windows_amd64.exe  (Windows)
```

**Запуск на Mac/Windows:**
```bash
# Mac (ARM)
./collect-card-characteristics_darwin_arm64 --db ./wb-sales.db --output ./chars.xlsx

# Mac — пакетный экспорт
./collect-card-characteristics_darwin_arm64 --export-all --items-per-file 25 --output ./chars.xlsx

# Windows
collect-card-characteristics_windows_amd64.exe --db .\wb-sales.db --output .\chars.xlsx
```

**Примечание:** `config.yaml` должен лежать рядом с бинарником. CLI флаги работают без конфига.

## Переиспользуемые пакеты

| Пакет | Что используется |
|-------|-----------------|
| `pkg/filter` | `Filter` struct + `BuildSQL()` для генерации WHERE/JOIN |
| `pkg/config` | `LoadYAML()` для загрузки config.yaml |
| `excelize/v2` | Создание XLSX с стилями и гиперссылками |
| `modernc.org/sqlite` | Pure Go SQLite драйвер (без CGo, для кросс-компиляции) |
