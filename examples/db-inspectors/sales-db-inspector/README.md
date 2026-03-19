# Sales DB Inspector

Утилита диагностики базы данных продаж WB (READ-ONLY).

## Быстрый старт

```bash
# Запуск
go run ./examples/db-inspectors/sales-db-inspector/main.go --db /path/to/sales.db

# Или скомпилировать
go build -o sales-inspector ./examples/db-inspectors/sales-db-inspector/main.go
./sales-inspector --db sales-2026.db
```

## Флаги

| Флаг | Описание | По умолчанию |
|------|----------|--------------|
| `--db` | Путь к базе (обязательный) | — |
| `--table` | Таблица: `sales`, `service`, `funnel`, `all` | `all` |
| `--from` | Начало периода (YYYY-MM-DD) | — |
| `--to` | Конец периода (YYYY-MM-DD) | — |

## Сценарии использования

### 1. Проверить общее состояние базы
```bash
./sales-inspector --db sales-2026.db
```
Показывает все таблицы, период данных, пробелы.

### 2. Найти записи за конкретный период
```bash
./sales-inspector --db sales-2026.db --table sales --from 2026-01-16 --to 2026-01-31
```

### 3. Проверить только service_records
```bash
./sales-inspector --db sales-2026.db --table service
```

### 4. Проверить funnel metrics
```bash
./sales-inspector --db sales-2026.db --table funnel
```

---

## Примеры запуска (copy-paste ready)

```bash
# Из любой директории - проверить первую неделю января 2026
go run /home/ilkoid/go-workspace/src/poncho-ai/examples/db-inspectors/sales-db-inspector/main.go \
  --db /home/ilkoid/go-workspace/src/poncho-ai/cmd/data-downloaders/download-wb-sales/sales-2026.db \
  --table sales \
  --from 2026-01-01 \
  --to 2026-01-07

# Полная диагностика базы
go run /home/ilkoid/go-workspace/src/poncho-ai/examples/db-inspectors/sales-db-inspector/main.go \
  --db /home/ilkoid/go-workspace/src/poncho-ai/cmd/data-downloaders/download-wb-sales/sales-2026.db
```

## Что показывает

- ✅ Целостность БД (`PRAGMA integrity_check`)
- 📅 Период данных (первая/последняя запись)
- 📊 Записи по дням с разбивкой: Продажа / Возврат
- ⚠️ Обнаруженные пробелы в данных

## Безопасность

- **Только чтение** — утилита открывает БД в режиме `ro`
- Никаких изменений данных
