# download-wb-funnel

Утилита для загрузки истории воронки продаж из **WB Analytics API v3** (`/api/analytics/v3/sales-funnel/products/history`).

## Возможности

- **15+ метрик воронки**: просмотры, корзины, заказы, выкупы, отмены, wishlist, суммы заказов, средний чек, WB Club, остатки, рейтинги и др.
- **История по дням**: ежедневная статистика для каждого товара (1-365 дней, 7 дней бесплатно, до 365 с подпиской)
- **Адаптивный rate limiting**: двухуровневая защита от 429 (desired → api floor → probe)
- **Refresh Window**: обновление только последних N дней (снижение нагрузки на API)
- **Фильтрация по артикулам**: исключение старых товаров по длине артикула и году производства

## Метрики Analytics API v3

| Метрика | API v2 | API v3 |
|---------|--------|--------|
| Просмотры | ✅ | ✅ |
| Корзины | ✅ | ✅ |
| Заказы | ✅ | ✅ |
| **Выкупы** | ❌ | ✅ |
| **Отмены** | ❌ | ✅ |
| **Wishlist** | ❌ | ✅ |
| **Суммы заказов** | ❌ | ✅ |
| **Средний чек** | ❌ | ✅ |
| **WB Club** | ❌ | ✅ |
| **Остатки (WB/MP)** | ❌ | ✅ |
| **Рейтинг товара** | ❌ | ✅ |
| **Рейтинг отзывов** | ❌ | ✅ |
| **Time to Ready** | ❌ | ✅ |
| **Localization %** | ❌ | ✅ |

## Установка

```bash
cd cmd/data-downloaders/download-wb-funnel
go build -o download-wb-funnel
```

## Конфигурация

### Базовая конфигурация (config.yaml)

```yaml
wb:
  analytics_api_key: ${WB_API_ANALYTICS_AND_PROMO_KEY}
  timeout: 30m

funnel:
  days: 5                           # Дней истории (1-365)
  batch_size: 20                    # Товаров на запрос (max 20)

  # Адаптивный rate limiting (см. dev_limits.md)
  funnel_rate_limit: 3              # desired — пробуем 3 req/min
  funnel_rate_limit_burst: 3
  funnel_rate_limit_api: 3          # api floor — восстановление после 429
  funnel_rate_limit_api_burst: 3

  adaptive_probe_after: 10          # OKs на api floor перед пробой desired
  max_backoff_seconds: 60           # Cap для exponential backoff

  # Опциональные даты (приоритет над days)
  # from: "2026-03-12"
  # to: "2026-03-12"

  max_batches: 0                    # 0 = загрузить все (для тестов)

storage:
  db_path: "/path/to/wb-sales.db"
  funnel_refresh_window: 4          # Дней для обновления (0 = всегда REPLACE)
```

### Фильтрация по артикулам (supplier_article)

**Новое!** Можно фильтровать товары по длине артикула и году производства (извлекается из 2-3 цифры артикула).

```yaml
# Фильтрация товаров по артикулу (supplier_article)
filter:
  # Исключить старые 6-значные артикулы
  exclude_lengths: [6]

  # Только товары 2024-2026 года (извлекается из 2-3 цифры артикула)
  # Например:
  #   12621749 → год "26" (2026) ✅
  #   32311215 → год "23" (2023) ❌
  #   395302   → длина 6 (старый) ❌
  allowed_years: [24, 25, 26]
```

#### Как работает фильтрация

1. **`exclude_lengths: [6]`** — исключает артикулы указанной длины
   - Полезно для исключения старых 6-значных артикулов

2. **`allowed_years: [24, 25, 26]`** — фильтрует по году производства
   - Год извлекается из **2-3 цифры** артикула (индексация с 0)
   - `12621749[1:3]` → `"26"` → год 2026
   - `32311215[1:3]` → `"23"` → год 2023

#### Примеры артикулов

| Артикул | Длина | Год (2-3 цифра) | Результат при `exclude_lengths: [6]`, `allowed_years: [24,25,26]` |
|---------|-------|-----------------|-----------------------------------------------------------|
| `12621749` | 8 | 26 | ✅ Включён (длина 8, год 26) |
| `32421088` | 8 | 24 | ✅ Включён (длина 8, год 24) |
| `32311215` | 8 | 23 | ❌ Исключён (год 23 не в списке) |
| `395302` | 6 | — | ❌ Исключён (длина 6) |
| `22317113` | 8 | 23 | ❌ Исключён (год 23 не в списке) |

## Использование

### Базовый запуск

```bash
# Загрузить за последние 5 дней
./download-wb-funnel

# Свой конфиг
./download-wb-funnel -config /path/to/config.yaml
```

### С фильтрацией по артикулам

```bash
# 1. Раскомментируйте секцию filter в config.yaml
# 2. Запустите утилиту
./download-wb-funnel

# Ожидаемый вывод:
# 📊 Найдено товаров: 14510
# 🔍 После фильтрации: 12034 товаров (исключено 2476)
```

### Refresh Window (обновление последних N дней)

```yaml
storage:
  funnel_refresh_window: 4  # Обновлять только последние 4 дня
```

**Логика работы:**
- **Последние 4 дня**: `INSERT OR REPLACE` (обновление данных)
- **Старее 4 дней**: `IGNORE` (сохранение существующих данных)

## Rate Limiting

**Analytics API v3 ограничения:**
- **3 запроса в минуту** (swagger)
- **Максимум 20 nmIds** за запрос

### Адаптивный rate limiting (двухуровневый)

Утилита использует двухуровневый rate limiting для безопасного превышения лимитов:

```
desired (агрессивный)
    ↓ 429
api floor (swagger — 3 req/min)
    ↓ 5 OKs
probe desired (проба после 10 OKs)
    ↓ 429 → повтор цикла
```

**Настройка:**
```yaml
funnel:
  funnel_rate_limit: 3              # desired — пробуем
  funnel_rate_limit_api: 3          # api floor — восстановление
  adaptive_probe_after: 10          # OKs на api floor перед пробой
```

Подробнее: [dev_limits.md](../../../dev_limits.md)

## База данных

### Таблица: funnel_metrics_daily

```sql
CREATE TABLE funnel_metrics_daily (
    nm_id INTEGER NOT NULL,
    metric_date TEXT NOT NULL,

    -- Воронка
    open_count INTEGER,            -- Просмотры
    cart_count INTEGER,            -- Добавления в корзину
    order_count INTEGER,           -- Заказы
    buyout_count INTEGER,          -- Выкупы
    add_to_wishlist INTEGER,       -- Wishlist

    -- Финансы
    order_sum REAL,                -- Сумма заказов
    buyout_sum REAL,               -- Сумма выкупов

    -- Конверсия
    conversion_add_to_cart REAL,   -- Просмотры → Корзина
    conversion_cart_to_order REAL, -- Корзина → Заказ
    conversion_buyout REAL,        -- Заказы → Выкуп

    PRIMARY KEY (nm_id, metric_date)
);
```

### SQL примеры

```sql
-- ТОП-10 товаров по выкупам за последние 7 дней
SELECT
    s.nm_id,
    s.supplier_article,
    SUM(f.buyout_count) as total_buyouts,
    SUM(f.order_sum) as revenue
FROM funnel_metrics_daily f
JOIN sales s ON s.nm_id = f.nm_id
WHERE f.metric_date >= DATE('now', '-7 days')
GROUP BY f.nm_id
ORDER BY total_buyouts DESC
LIMIT 10;

-- Динамика выкупов по дням
SELECT
    metric_date,
    SUM(buyout_count) as daily_buyouts,
    SUM(order_count) as daily_orders,
    ROUND(100.0 * SUM(buyout_count) / NULLIF(SUM(order_count), 0), 2) as buyout_rate
FROM funnel_metrics_daily
GROUP BY metric_date
ORDER BY metric_date DESC;
```

## Диагностика

```bash
# Проверить какие товары будут исключены фильтром
sqlite3 /path/to/wb-sales.db "
SELECT
    supplier_article,
    LENGTH(supplier_article) as len,
    SUBSTR(supplier_article, 2, 2) as year_digits
FROM sales
WHERE supplier_article IS NOT NULL
ORDER BY len DESC
LIMIT 20;
"

# Подсчитать распределение по длине артикулов
sqlite3 /path/to/wb-sales.db "
SELECT
    LENGTH(supplier_article) as len,
    COUNT(DISTINCT nm_id) as products
FROM sales
WHERE supplier_article IS NOT NULL
GROUP BY len
ORDER BY len;
"
```

## Переменные окружения

| Переменная | Описание |
|------------|----------|
| `WB_API_ANALYTICS_AND_PROMO_KEY` | Analytics API ключ (тоже что `WB_API_KEY`) |
| `WB_API_KEY` | Альтернативный ключ для Analytics API |

## Смотрите также

- [download-wb-funnel-agg](../download-wb-funnel-agg/) — агрегированная воронка за период
- [download-wb-sales](../download-wb-sales/) — загрузка данных о продажах
- [pkg/tools/std/wb_analytics.go](../../../pkg/tools/std/wb_analytics.go) — инструменты для LLM агентов
- [dev_limits.md](../../../dev_limits.md) — адаптивный rate limiting

## Версия API

- **Analytics API v3**: `/api/analytics/v3/sales-funnel/products/history`
- Swagger: [analytics-api.wildberries.ru](https://analytics-api.wildberries.ru/swagger/index.html)
