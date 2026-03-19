-- funnel-data-queries.sql
-- Все SQL запросы для проверки корректности загрузки Funnel данных
--
-- Использование:
--   sqlite3 test-2026.db < scripts/funnel-data-queries.sql
--   sqlite3 test-2026.db ".read scripts/funnel-data-queries.sql"

-- ============================================================================
-- CHECK 1: Общая статистика загрузки
-- ============================================================================
SELECT
    'Check 1: Общая статистика' as check_name,
    COUNT(DISTINCT nm_id) as products,
    COUNT(*) as total_rows,
    MIN(metric_date) as date_from,
    MAX(metric_date) as date_to
FROM funnel_metrics_daily;

-- ============================================================================
-- CHECK 2: Проверка целостности ключей (дубликаты)
-- ============================================================================
-- Ожидается: пустой результат
SELECT
    'Check 2: Дубликаты ключей' as check_name,
    nm_id,
    metric_date,
    COUNT(*) as cnt
FROM funnel_metrics_daily
GROUP BY nm_id, metric_date
HAVING cnt > 1;

-- ============================================================================
-- CHECK 3: Валидация логики воронки
-- ============================================================================
-- Логика: open_count >= cart_count >= order_count >= buyout_count + cancel_count
-- Ожидается: пустой результат или объяснимые аномалии
SELECT
    'Check 3: Нарушения логики воронки' as check_name,
    nm_id,
    open_count,
    cart_count,
    order_count,
    buyout_count,
    cancel_count,
    CASE
        WHEN cart_count > open_count THEN 'ERROR: cart > open'
        WHEN order_count > cart_count THEN 'ERROR: order > cart'
        WHEN buyout_count + cancel_count > order_count THEN 'ERROR: buyout+cancel > order'
        ELSE 'OK'
    END as funnel_check
FROM funnel_metrics_daily
WHERE cart_count > open_count
   OR order_count > cart_count
   OR (buyout_count + cancel_count) > order_count
LIMIT 10;

-- ============================================================================
-- CHECK 4: Проверка финансовых метрик
-- ============================================================================
-- Ожидается: пустой результат (суммы должны быть при заказах)
SELECT
    'Check 4: Финансовые аномалии' as check_name,
    nm_id,
    order_count,
    order_sum,
    buyout_count,
    buyout_sum,
    CASE
        WHEN order_count > 0 AND order_sum = 0 THEN 'WARN: orders without sum'
        WHEN buyout_count > 0 AND buyout_sum = 0 THEN 'WARN: buyouts without sum'
        ELSE 'OK'
    END as finance_check
FROM funnel_metrics_daily
WHERE (order_count > 0 AND order_sum = 0)
   OR (buyout_count > 0 AND buyout_sum = 0)
LIMIT 10;

-- ============================================================================
-- CHECK 5: Валидация конверсий
-- ============================================================================
-- Проверяем расхождение между сохранённой конверсией и расчётной
-- Ожидается: пустой результат (расхождение < 0.5%)
SELECT
    'Check 5: Расхождения конверсий' as check_name,
    nm_id,
    open_count,
    cart_count,
    conversion_add_to_cart as stored_conv,
    ROUND(cart_count * 100.0 / open_count, 2) as calc_conv,
    ABS(conversion_add_to_cart - ROUND(cart_count * 100.0 / open_count, 2)) as diff
FROM funnel_metrics_daily
WHERE open_count > 0
  AND ABS(conversion_add_to_cart - ROUND(cart_count * 100.0 / open_count, 2)) > 0.5
LIMIT 10;

-- ============================================================================
-- CHECK 6: Проверка Null значений
-- ============================================================================
SELECT
    'Check 6: Null значения' as check_name,
    COUNT(*) as total,
    SUM(CASE WHEN open_count IS NULL THEN 1 ELSE 0 END) as null_open,
    SUM(CASE WHEN cart_count IS NULL THEN 1 ELSE 0 END) as null_cart,
    SUM(CASE WHEN order_count IS NULL THEN 1 ELSE 0 END) as null_order,
    SUM(CASE WHEN buyout_count IS NULL THEN 1 ELSE 0 END) as null_buyout,
    SUM(CASE WHEN conversion_buyout IS NULL THEN 1 ELSE 0 END) as null_conv
FROM funnel_metrics_daily;

-- ============================================================================
-- CHECK 7: Проверка диапазонов значений
-- ============================================================================
SELECT
    'Check 7: Диапазоны значений' as check_name,
    MIN(open_count) as min_open,
    MAX(open_count) as max_open,
    MIN(conversion_buyout) as min_conv_buyout,
    MAX(conversion_buyout) as max_conv_buyout,
    MIN(buyout_sum) as min_buyout_sum,
    MAX(buyout_sum) as max_buyout_sum
FROM funnel_metrics_daily;

-- ============================================================================
-- CHECK 8: Топ товаров по выручке
-- ============================================================================
SELECT
    'Check 8: Топ по выручке' as check_name,
    nm_id,
    open_count,
    cart_count,
    order_count,
    buyout_count,
    buyout_sum,
    printf('%.1f', conversion_buyout) as conv_buyout_pct
FROM funnel_metrics_daily
ORDER BY buyout_sum DESC
LIMIT 10;

-- ============================================================================
-- CHECK 9: Проверка связи с таблицей sales (orphan nm_id)
-- ============================================================================
-- Ожидается: пустой результат (все nm_id из funnel есть в sales)
SELECT
    'Check 9: Orphan nm_id в funnel' as check_name,
    f.nm_id
FROM funnel_metrics_daily f
LEFT JOIN sales s ON f.nm_id = s.nm_id
WHERE s.nm_id IS NULL
GROUP BY f.nm_id
LIMIT 10;

-- ============================================================================
-- CHECK 10: Сравнение количества товаров funnel vs sales
-- ============================================================================
SELECT
    'Check 10: Сравнение товаров' as check_name,
    (SELECT COUNT(DISTINCT nm_id) FROM funnel_metrics_daily) as funnel_products,
    (SELECT COUNT(DISTINCT nm_id) FROM sales) as sales_products,
    CASE
        WHEN (SELECT COUNT(DISTINCT nm_id) FROM funnel_metrics_daily) <=
             (SELECT COUNT(DISTINCT nm_id) FROM sales)
        THEN 'OK: funnel <= sales'
        ELSE 'ERROR: funnel > sales'
    END as check_result;

-- ============================================================================
-- ДОПОЛНИТЕЛЬНЫЕ ЗАПРОСЫ
-- ============================================================================

-- Распределение товаров по количеству заказов
SELECT
    'Распределение заказов' as query_name,
    CASE
        WHEN order_count = 0 THEN '0 заказов'
        WHEN order_count BETWEEN 1 AND 5 THEN '1-5 заказов'
        WHEN order_count BETWEEN 6 AND 20 THEN '6-20 заказов'
        WHEN order_count > 20 THEN '> 20 заказов'
    END as bucket,
    COUNT(*) as products_count
FROM funnel_metrics_daily
GROUP BY bucket
ORDER BY
    CASE bucket
        WHEN '0 заказов' THEN 1
        WHEN '1-5 заказов' THEN 2
        WHEN '6-20 заказов' THEN 3
        WHEN '> 20 заказов' THEN 4
    END;

-- Средние конверсии по базе
SELECT
    'Средние конверсии' as query_name,
    COUNT(*) as total_rows,
    AVG(conversion_add_to_cart) as avg_conv_to_cart,
    AVG(conversion_cart_to_order) as avg_conv_cart_to_order,
    AVG(conversion_buyout) as avg_conv_buyout
FROM funnel_metrics_daily
WHERE open_count > 0;

-- Товары с аномально высокой конверсией (> 50%)
SELECT
    'Аномально высокая конверсия' as query_name,
    nm_id,
    open_count,
    cart_count,
    order_count,
    conversion_add_to_cart,
    conversion_cart_to_order,
    conversion_buyout
FROM funnel_metrics_daily
WHERE conversion_buyout > 50
ORDER BY conversion_buyout DESC
LIMIT 10;

-- Сводка по периоду
SELECT
    'Сводка по периоду' as query_name,
    metric_date,
    COUNT(DISTINCT nm_id) as products,
    SUM(open_count) as total_opens,
    SUM(cart_count) as total_carts,
    SUM(order_count) as total_orders,
    SUM(buyout_count) as total_buyouts,
    SUM(buyout_sum) as total_revenue
FROM funnel_metrics_daily
GROUP BY metric_date
ORDER BY metric_date;
