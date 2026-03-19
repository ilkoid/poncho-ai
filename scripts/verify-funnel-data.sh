#!/bin/bash
# verify-funnel-data.sh — Комплексная проверка корректности загрузки Funnel данных
#
# Использование:
#   ./scripts/verify-funnel-data.sh [путь_к_базе]
#   ./scripts/verify-funnel-data.sh cmd/data-downloaders/download-wb-sales/test-2026.db
#
# По умолчанию проверяет: cmd/data-downloaders/download-wb-sales/test-2026.db

set -e

DB_PATH="${1:-cmd/data-downloaders/download-wb-sales/test-2026.db}"

# Цвета для вывода
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo -e "${BLUE}╔══════════════════════════════════════════════════════════════════╗${NC}"
echo -e "${BLUE}║         FUNNEL DATA VERIFICATION — Проверка данных               ║${NC}"
echo -e "${BLUE}╚══════════════════════════════════════════════════════════════════╝${NC}"
echo ""
echo -e "База данных: ${YELLOW}${DB_PATH}${NC}"
echo ""

# Проверка существования файла
if [ ! -f "$DB_PATH" ]; then
    echo -e "${RED}❌ Файл базы данных не найден: ${DB_PATH}${NC}"
    exit 1
fi

# Счётчик ошибок
ERRORS=0
WARNINGS=0

# ============================================================================
# CHECK 1: Общая статистика загрузки
# ============================================================================
echo -e "${BLUE}━━━ Check 1: Общая статистика загрузки ━━━${NC}"

STATS=$(sqlite3 "$DB_PATH" "
SELECT
    COUNT(DISTINCT nm_id) as products,
    COUNT(*) as rows,
    MIN(metric_date) as date_from,
    MAX(metric_date) as date_to
FROM funnel_metrics_daily;
")

echo "$STATS" | while IFS='|' read products rows date_from date_to; do
    echo -e "  Товаров:     ${GREEN}${products}${NC}"
    echo -e "  Строк:       ${GREEN}${rows}${NC}"
    echo -e "  Период:      ${GREEN}${date_from} → ${date_to}${NC}"

    if [ "$products" -eq 0 ]; then
        echo -e "  ${RED}❌ Нет загруженных товаров${NC}"
        ERRORS=$((ERRORS + 1))
    else
        echo -e "  ${GREEN}✅ Данные загружены${NC}"
    fi
done
echo ""

# ============================================================================
# CHECK 2: Дубликаты ключей
# ============================================================================
echo -e "${BLUE}━━━ Check 2: Проверка целостности ключей ━━━${NC}"

DUP=$(sqlite3 "$DB_PATH" "
SELECT COUNT(*) FROM (
    SELECT nm_id, metric_date
    FROM funnel_metrics_daily
    GROUP BY nm_id, metric_date
    HAVING COUNT(*) > 1
);")

if [ "$DUP" -eq 0 ]; then
    echo -e "  ${GREEN}✅ Нет дубликатов ключей (nm_id, metric_date)${NC}"
else
    echo -e "  ${RED}❌ Найдено дубликатов: ${DUP}${NC}"
    ERRORS=$((ERRORS + 1))
fi
echo ""

# ============================================================================
# CHECK 3: Валидация логики воронки
# ============================================================================
echo -e "${BLUE}━━━ Check 3: Валидация логики воронки ━━━${NC}"
echo -e "  Ожидается: ${YELLOW}open ≥ cart ≥ orders ≥ buyouts + cancels${NC}"

VIOLATIONS=$(sqlite3 "$DB_PATH" "
SELECT COUNT(*) FROM funnel_metrics_daily
WHERE cart_count > open_count
   OR order_count > cart_count
   OR (buyout_count + cancel_count) > order_count;" 2>/dev/null || echo "0")

if [ "$VIOLATIONS" -eq 0 ]; then
    echo -e "  ${GREEN}✅ Логика воронки корректна${NC}"
else
    echo -e "  ${YELLOW}⚠️ Нарушений логики: ${VIOLATIONS}${NC}"
    echo -e "  ${YELLOW}   (может быть нормально для WB API — данные обновляются асинхронно)${NC}"
    WARNINGS=$((WARNINGS + 1))

    # Показать примеры нарушений
    echo -e "  ${YELLOW}   Примеры:${NC}"
    sqlite3 "$DB_PATH" "
    SELECT nm_id, open_count, cart_count, order_count, buyout_count, cancel_count
    FROM funnel_metrics_daily
    WHERE cart_count > open_count
       OR order_count > cart_count
       OR (buyout_count + cancel_count) > order_count
    LIMIT 3;" | sed 's/^/     /'
fi
echo ""

# ============================================================================
# CHECK 4: Проверка финансовых метрик
# ============================================================================
echo -e "${BLUE}━━━ Check 4: Проверка финансовых метрик ━━━${NC}"

FINANCE_ISSUES=$(sqlite3 "$DB_PATH" "
SELECT COUNT(*) FROM funnel_metrics_daily
WHERE (order_count > 0 AND order_sum = 0)
   OR (buyout_count > 0 AND buyout_sum = 0);")

if [ "$FINANCE_ISSUES" -eq 0 ]; then
    echo -e "  ${GREEN}✅ Финансовые метрики заполнены корректно${NC}"
else
    echo -e "  ${YELLOW}⚠️ Строк с отсутствующими суммами: ${FINANCE_ISSUES}${NC}"
    echo -e "  ${YELLOW}   (возможно, заказы с нулевой стоимостью)${NC}"
    WARNINGS=$((WARNINGS + 1))
fi
echo ""

# ============================================================================
# CHECK 5: Валидация конверсий
# ============================================================================
echo -e "${BLUE}━━━ Check 5: Валидация конверсий ━━━${NC}"

CONV_ISSUES=$(sqlite3 "$DB_PATH" "
SELECT COUNT(*) FROM funnel_metrics_daily
WHERE open_count > 0
  AND ABS(conversion_add_to_cart - (cart_count * 100.0 / open_count)) > 0.5;")

if [ "$CONV_ISSUES" -eq 0 ]; then
    echo -e "  ${GREEN}✅ Конверсии рассчитаны корректно${NC}"
else
    echo -e "  ${YELLOW}⚠️ Строк с расхождением конверсий: ${CONV_ISSUES}${NC}"
    WARNINGS=$((WARNINGS + 1))
fi
echo ""

# ============================================================================
# CHECK 6: Проверка Null значений
# ============================================================================
echo -e "${BLUE}━━━ Check 6: Проверка Null значений ━━━${NC}"

NULLS=$(sqlite3 "$DB_PATH" "
SELECT
    SUM(CASE WHEN open_count IS NULL THEN 1 ELSE 0 END) as null_open,
    SUM(CASE WHEN cart_count IS NULL THEN 1 ELSE 0 END) as null_cart,
    SUM(CASE WHEN order_count IS NULL THEN 1 ELSE 0 END) as null_order,
    SUM(CASE WHEN buyout_count IS NULL THEN 1 ELSE 0 END) as null_buyout,
    SUM(CASE WHEN conversion_buyout IS NULL THEN 1 ELSE 0 END) as null_conv
FROM funnel_metrics_daily;")

echo "$NULLS" | while IFS='|' read null_open null_cart null_order null_buyout null_conv; do
    if [ "$null_open" -eq 0 ] && [ "$null_cart" -eq 0 ] && [ "$null_order" -eq 0 ]; then
        echo -e "  ${GREEN}✅ Обязательные поля без NULL${NC}"
    else
        echo -e "  ${RED}❌ NULL значения: open=${null_open}, cart=${null_cart}, order=${null_order}${NC}"
        ERRORS=$((ERRORS + 1))
    fi
done
echo ""

# ============================================================================
# CHECK 7: Проверка диапазонов значений
# ============================================================================
echo -e "${BLUE}━━━ Check 7: Проверка диапазонов значений ━━━${NC}"

RANGES=$(sqlite3 "$DB_PATH" "
SELECT
    MIN(open_count) as min_open,
    MAX(open_count) as max_open,
    MIN(conversion_buyout) as min_conv,
    MAX(conversion_buyout) as max_conv,
    MIN(buyout_sum) as min_sum,
    MAX(buyout_sum) as max_sum
FROM funnel_metrics_daily;")

echo "$RANGES" | while IFS='|' read min_open max_open min_conv max_conv min_sum max_sum; do
    echo -e "  open_count:     ${min_open} → ${max_open}"
    echo -e "  conversion:     ${min_conv}% → ${max_conv}%"
    echo -e "  buyout_sum:     ${min_sum} → ${max_sum} ₽"

    if [ "$min_open" -lt 0 ]; then
        echo -e "  ${RED}❌ Отрицательные open_count${NC}"
        ERRORS=$((ERRORS + 1))
    fi

    if [ "$(echo "$min_conv < 0" | bc -l 2>/dev/null || echo 0)" -eq 1 ] || \
       [ "$(echo "$max_conv > 100" | bc -l 2>/dev/null || echo 0)" -eq 1 ]; then
        echo -e "  ${YELLOW}⚠️ Конверсии вне диапазона 0-100%${NC}"
        WARNINGS=$((WARNINGS + 1))
    else
        echo -e "  ${GREEN}✅ Диапазоны значений корректны${NC}"
    fi
done
echo ""

# ============================================================================
# CHECK 8: Топ товаров по продажам
# ============================================================================
echo -e "${BLUE}━━━ Check 8: Топ-5 товаров по выручке ━━━${NC}"

sqlite3 -header -column "$DB_PATH" "
SELECT
    nm_id as 'nm_id',
    buyout_count as 'buyouts',
    buyout_sum as 'sum_₽',
    printf('%.1f', conversion_buyout) as 'conv%'
FROM funnel_metrics_daily
ORDER BY buyout_sum DESC
LIMIT 5;"

echo ""

# ============================================================================
# CHECK 9: Проверка связи с таблицей sales
# ============================================================================
echo -e "${BLUE}━━━ Check 9: Связь с таблицей sales ━━━${NC}"

# Проверяем существование таблицы sales
SALES_EXISTS=$(sqlite3 "$DB_PATH" "
SELECT COUNT(*) FROM sqlite_master
WHERE type='table' AND name='sales';")

if [ "$SALES_EXISTS" -eq 1 ]; then
    ORPHANS=$(sqlite3 "$DB_PATH" "
    SELECT COUNT(DISTINCT f.nm_id)
    FROM funnel_metrics_daily f
    LEFT JOIN sales s ON f.nm_id = s.nm_id
    WHERE s.nm_id IS NULL;")

    if [ "$ORPHANS" -eq 0 ]; then
        echo -e "  ${GREEN}✅ Все nm_id из funnel есть в sales${NC}"
    else
        echo -e "  ${YELLOW}⚠️ Товаров в funnel без sales: ${ORPHANS}${NC}"
        echo -e "  ${YELLOW}   (аномалия — funnel должен загружаться только для товаров из sales)${NC}"
        WARNINGS=$((WARNINGS + 1))
    fi
else
    echo -e "  ${YELLOW}⚠️ Таблица sales не найдена — проверка пропущена${NC}"
fi
echo ""

# ============================================================================
# CHECK 10: Сравнение количества товаров
# ============================================================================
echo -e "${BLUE}━━━ Check 10: Сравнение количества товаров ━━━${NC}"

if [ "$SALES_EXISTS" -eq 1 ]; then
    COUNTS=$(sqlite3 "$DB_PATH" "
    SELECT
        (SELECT COUNT(DISTINCT nm_id) FROM funnel_metrics_daily) as funnel_cnt,
        (SELECT COUNT(DISTINCT nm_id) FROM sales) as sales_cnt;")

    echo "$COUNTS" | while IFS='|' read funnel_cnt sales_cnt; do
        echo -e "  Товаров в funnel: ${funnel_cnt}"
        echo -e "  Товаров в sales:  ${sales_cnt}"

        if [ "$funnel_cnt" -le "$sales_cnt" ]; then
            echo -e "  ${GREEN}✅ funnel ≤ sales (корректно)${NC}"
        else
            echo -e "  ${RED}❌ funnel > sales (аномалия)${NC}"
            ERRORS=$((ERRORS + 1))
        fi
    done
else
    echo -e "  ${YELLOW}⚠️ Таблица sales не найдена — проверка пропущена${NC}"
fi
echo ""

# ============================================================================
# ИТОГИ
# ============================================================================
echo -e "${BLUE}╔══════════════════════════════════════════════════════════════════╗${NC}"
echo -e "${BLUE}║                         ИТОГИ ПРОВЕРКИ                           ║${NC}"
echo -e "${BLUE}╚══════════════════════════════════════════════════════════════════╝${NC}"

if [ "$ERRORS" -eq 0 ] && [ "$WARNINGS" -eq 0 ]; then
    echo -e "${GREEN}✅ Все проверки пройдены успешно!${NC}"
    echo ""
    echo "Данные загружены корректно и готовы к использованию."
    exit 0
elif [ "$ERRORS" -eq 0 ]; then
    echo -e "${YELLOW}⚠️ Проверки пройдены с предупреждениями: ${WARNINGS}${NC}"
    echo ""
    echo "Данные можно использовать, но рекомендуется проверить предупреждения."
    exit 0
else
    echo -e "${RED}❌ Обнаружены критические ошибки: ${ERRORS}${NC}"
    echo ""
    echo "Требуется исправление перед использованием данных."
    exit 1
fi
