# build-ma-sku-snapshots

Анализ остатков по размерам (SKU) на региональных складах Wildberries с расчётом скользящих средних и флагами рисков.

Читает данные из `wb-sales.db` (остатки, продажи, карточки, 1C/PIM атрибуты), рассчитывает MA-3/7/14/28 продаж по каждому размеру (barcode), сравнивает с текущими остатками и записывает плоскую таблицу в `bi2.db` для PowerBI.

## Запуск

```bash
go run .                                      # снэпшот за вчера
go run . --date 2026-04-15                    # конкретная дата
go run . --dry-run                            # сводка в консоль, без записи в БД
go run . --force                              # пересчитать даже если снэпшот есть
go run . --config /path/to/config.yaml        # кастомный путь к конфигу
go run . --source /path/to/source.db          # override source DB
go run . --db /path/to/bi.db                  # override results DB
```

### Загрузка данных

Перед запуском убедитесь, что данные актуальны:

```bash
bash download-ma-sku.sh     # загрузить 4 нужных загрузчика
bash download-ma-sku.sh 30  # продажи за 30 дней (для MA-28)
```

## Что делает утилита

1. **Фильтрация по годам** — оставляет только товары 2025-2026 (год извлекается из 2-3 цифры артикула продавца)
2. **Остатки** — суммирует `stocks_daily_warehouses` по (nm_id, chrt_id, region_name)
3. **Размерные ряды** — определяет сколько размеров у артикула всего и сколько с остатком
4. **MA продаж** — рассчитывает скользящие средние по barcode (глобально, без привязки к региону)
5. **Метрики** — SDR (дней до окончания), тренд спроса, заполненность рядов
6. **Флаги рисков** — критично / риск / нет в наличии / выбитый ряд

## Источники данных

| Таблица | Загрузчик | Что даёт |
|---------|-----------|----------|
| `stocks_daily_warehouses` | `download-wb-stocks` | Остатки по (nm_id, chrt_id, warehouse, region) |
| `sales` | `download-wb-sales` | Продажи по barcode за 29 дней (для MA) |
| `card_sizes` | `download-wb-cards` | Маппинг chrt_id → barcode + tech_size |
| `products` | `download-wb-cards` | nm_id → vendor_code |
| `onec_goods` | `download-1c-data` | Атрибуты: бренд, категория, пол, сезон, цвет |
| `pim_goods` | `download-1c-data` | Дополнительные атрибуты (identifier) |

## Маппинг chrt_id ↔ barcode

WB использует два идентификатора размера:
- `chrt_id` (числовой, напр. `292237814`) — в остатках
- `barcode` (EAN-13, напр. `"4630047636342"`) — в продажах

Связка через `card_sizes`:
```
card_sizes: chrt_id=292237814, skus_json=["4630047636342"], tech_size="104"
```

MA считается по barcode из `sales`, затем маппится на `chrt_id` через `card_sizes.skus_json`.

## Расчёт метрик

| Метрика | Формула | Описание |
|---------|---------|----------|
| `ma_3/7/14/28` | Среднее продаж за N дней до даты снэпшота | Глобально по barcode |
| `sdr_days` | `stock_qty / MA-7` | Дней до окончания остатков |
| `trend_pct` | `(MA-3 − MA-7) / MA-7 × 100` | Тренд спроса |
| `fill_pct` | `sizes_in_stock / total_sizes × 100` | Заполненность размерного ряда |

## Флаги рисков

| Флаг | Условие | Что означает |
|------|---------|--------------|
| `critical` | `stock > threshold` AND `SDR ≤ critical_days` | Заканчивается критически быстро |
| `risk` | `stock > threshold` AND `SDR ≤ reorder_window` | Нужна подсортировка |
| `out_of_stock` | `stock ≤ threshold` AND `MA-7 > 0` | Нет товара, но есть спрос |
| `broken_grid` | `sizes_in_stock < total_sizes` | Выбитый размерный ряд |

## Параметры конфигурации

```yaml
source:
  db_path: "db/wb-sales.db"          # База источников (только чтение)

results:
  db_path: "db/bi2.db"               # База аналитики для BI

ma:
  windows: [3, 7, 14, 28]            # Окна MA
  min_days: 1                        # Минимум дней с данными для расчёта MA

alerts:
  zero_stock_threshold: 1            # stock ≤ N = "нет товара"
  reorder_window: 7                  # SDR ≤ N дней → риск
  critical_days: 3                   # SDR ≤ N дней → критично

filter:
  allowed_years: [25, 26]            # Только товары 2025-2026 (пусто = все)

force: false                         # Пересчитать если снэпшот существует
```

## Выходные данные

Таблица `ma_sku_daily` в `bi2.db`, 32 колонки:
- Идентификация: nm_id, chrt_id, region_name, tech_size
- Атрибуты: article, vendor_code, brand, category, sex, season, color
- Остатки: stock_qty, total_sizes, sizes_in_stock, fill_pct
- MA: ma_3, ma_7, ma_14, ma_28
- Производные: sdr_days, trend_pct
- Флаги: risk, critical, out_of_stock, broken_grid

Первичный ключ: `(snapshot_date, nm_id, chrt_id, region_name)`.

Примеры запросов для BI:

```sql
-- Самые критичные позиции
SELECT article, brand, tech_size, region_name, stock_qty, ma_7, sdr_days
FROM ma_sku_daily
WHERE snapshot_date = '2026-04-17' AND critical = 1
ORDER BY sdr_days;

-- Выбитые размерные ряды по регионам
SELECT article, brand, region_name, total_sizes, sizes_in_stock, fill_pct
FROM ma_sku_daily
WHERE snapshot_date = '2026-04-17' AND broken_grid = 1
GROUP BY article, region_name;
```
