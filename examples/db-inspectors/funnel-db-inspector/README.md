# Funnel DB Inspector

READ-ONLY утилита для проверки качества и полноты данных воронки (funnel) из WB Analytics API v3.

## Назначение

Инспекция базы данных SQLite с метриками воронки продаж:
- Проверка целостности данных
- Анализ ежедневных метрик
- Выявление пробелов в данных
- Проверка качества данных (нулевые значения, аномалии)
- Сравнение со схемой WB API Swagger

## Использование

```bash
# Базовая проверка
go run main.go --db /path/to/sales.db

# Фильтр по периоду
go run main.go --db sales.db --from 2026-01-01 --to 2026-01-31

# Сравнение со схемой Swagger
go run main.go --db sales.db --compare

# Подробный вывод с примерами записей
go run main.go --db sales.db -v

# Все опции вместе
go run main.go --db sales.db --from 2026-01-01 --to 2026-01-31 --compare -v
```

## Флаги

| Флаг | Описание | Обязательный |
|------|----------|--------------|
| `--db` | Путь к SQLite базе данных | Да |
| `--from` | Начальная дата (YYYY-MM-DD) | Нет |
| `--to` | Конечная дата (YYYY-MM-DD) | Нет |
| `--compare` | Сравнить со схемой WB API Swagger | Нет |
| `-v` | Подробный вывод с примерами записей | Нет |

## Выходные данные

### FUNNEL METADATA
- Общее количество записей
- Количество уникальных товаров
- Диапазон дат
- Наличие метаданных товаров

### DAILY METRICS
- Ежедневная агрегация метрик:
  - Records — количество записей
  - Products — количество товаров
  - Views — просмотры (open_count)
  - Cart — добавления в корзину
  - Orders — заказы
  - Buyouts — выкупы
  - Conv% — средняя конверсия
  - ZeroViews — записи с нулевыми просмотрами

### DATA QUALITY
- Пропущенные даты (gaps)
- Нулевые метрики
- Аномалии (buyouts > orders)
- Аномалии конверсии (< 0 или > 100)
- Товары без метаданных
- Статистика конверсий
- Наличие WB Club метрик

### SCHEMA COMPARISON (--compare)
- Сравнение колонок БД со схемой Swagger
- Показывает отсутствующие и дополнительные поля

## Таблицы БД

### funnel_metrics_daily
Основная таблица с ежедневными метриками воронки:
- `nm_id` — ID товара
- `metric_date` — дата метрики
- `open_count`, `cart_count`, `order_count`, `buyout_count`, `cancel_count`
- `order_sum`, `buyout_sum`, `cancel_sum`, `avg_price`
- `conversion_add_to_cart`, `conversion_cart_to_order`, `conversion_buyout`
- `wb_club_order_count`, `wb_club_buyout_count`, `wb_club_buyout_percent`
- `time_to_ready_*`, `localization_percent`

### products
Метаданные товаров:
- `nm_id`, `vendor_code`, `title`, `brand_name`
- `subject_id`, `subject_name`
- `product_rating`, `feedback_rating`
- `stock_wb`, `stock_mp`, `stock_balance_sum`

## Пример вывода

```
============================================================
     FUNNEL DATABASE INSPECTION TOOL (READ-ONLY MODE)
============================================================
   File: sales-2026.db
   Size: 2.4 MB
   Modified: 2026-01-19 15:30:00
============================================================

✅ Database integrity: OK

=== FUNNEL METADATA ===
   Total records: 1302
   Unique products: 42
   Date range: 2026-01-01 ... 2026-01-31
   Days covered: 31
   Products metadata: 42 records

=== DAILY METRICS ===
   Date         Records  Products  Views   Cart   Orders  Buyouts  Conv%  ZeroViews
   ----         -------  --------  -----   ----   ------  -------  -----  ---------
   2026-01-01   42       42        15234   1523   892     756      84.7   0
   2026-01-02   42       42        14892   1489   845     712      84.3   0
   ...

   TOTAL: 31 records, 450K views, 45K cart, 26K orders, 22K buyouts

=== DATA QUALITY ===
   Date continuity: ✅ No gaps detected
   Products metadata: ✅ All products have metadata
   Conversion range: 45.2% ... 98.3% (avg: 84.1%)
   WB Club data: ✅ 1302 records with WB Club metrics

=== SCHEMA COMPARISON (--compare) ===
   Swagger fields present: 12/12 ✅
   DB columns total: 24
   Extra DB fields (12): add_to_wishlist, avg_price, cancel_count, ...
   Products table: ✅ Present
```

## Связанные утилиты

- [sales-db-inspector](../sales-db-inspector/) — инспекция таблицы продаж
- [download-wb-sales](../../../cmd/data-downloaders/download-wb-sales/) — загрузка данных воронки
