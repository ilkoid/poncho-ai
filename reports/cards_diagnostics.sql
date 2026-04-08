-- ============================================================================
-- ДИАГНОСТИКА ТОВАРОВ НОВОЙ КОЛЛЕКЦИИ 2026
-- База: /home/ilkoid/archive/db/wb-cards.db
--
-- Новая коллекция: vendor_code GLOB '[0-9]26*' (2-я и 3-я цифры = "26")
-- ~2593 карточек, 1824 в products, преимущественно бренд BestBrand
--
-- Рекомендуемый порядок использования:
--   1. Запрос 8 — сводный дашборд (общая картина)
--   2. Запрос 2 — быстрые победы (трафик есть, продаж нет)
--   3. Запрос 1 — массовое улучшение карточек
--   4. Запрос 7 — поднять product_rating
--   5. Запрос 4 — какие категории отстают системно
--   6. Запрос 3 — точечно по категориям
--   7. Запрос 5 — мёртвый запас, решения по ценам
--   8. Запрос 6 — работа с отзывами
-- ============================================================================


-- ╔═══════════════════════════════════════════════════════════════════════════╗
-- ║ ЗАПРОС 1: АУДИТ ПОЛНОТЫ КАРТОЧЕК                                        ║
-- ║ Выявляет карточки с недостаточным наполнением                           ║
-- ║ (мало фото, нет видео, пустое описание, мало характеристик)             ║
-- ╚═══════════════════════════════════════════════════════════════════════════╝
--
-- Проблема: Неполные карточки пессимизируются поисковым алгоритмом WB.
-- Действие: Приоритизировать карточки с баллом 0-2. Добавить видео (3% НК имеют
--           видео), дополнить описания до 200+ символов, добавить характеристики.

WITH card_completeness AS (
    SELECT
        c.nm_id,
        c.vendor_code,
        c.title,
        c.subject_name,
        c.video,
        c.description,
        p.product_rating,
        ph.photo_count,
        ch.char_count,
        cs.size_count,
        CASE WHEN ph.photo_count >= 8 THEN 1 ELSE 0 END AS has_enough_photos,
        CASE WHEN c.video IS NOT NULL AND c.video != '' THEN 1 ELSE 0 END AS has_video,
        CASE WHEN LENGTH(c.description) >= 200 THEN 1 ELSE 0 END AS has_good_desc,
        CASE WHEN ch.char_count >= 12 THEN 1 ELSE 0 END AS has_enough_chars,
        CASE WHEN cs.size_count >= 2 THEN 1 ELSE 0 END AS has_sizes
    FROM cards c
    LEFT JOIN products p ON c.nm_id = p.nm_id
    LEFT JOIN (
        SELECT nm_id, COUNT(*) AS photo_count FROM card_photos GROUP BY nm_id
    ) ph ON c.nm_id = ph.nm_id
    LEFT JOIN (
        SELECT nm_id, COUNT(*) AS char_count FROM card_characteristics GROUP BY nm_id
    ) ch ON c.nm_id = ch.nm_id
    LEFT JOIN (
        SELECT nm_id, COUNT(*) AS size_count FROM card_sizes GROUP BY nm_id
    ) cs ON c.nm_id = cs.nm_id
    WHERE c.vendor_code GLOB '[0-9]26*'
)
SELECT
    nm_id,
    vendor_code,
    subject_name,
    product_rating,
    photo_count,
    char_count,
    size_count,
    (has_enough_photos + has_video + has_good_desc + has_enough_chars + has_sizes) AS completeness_score,
    CASE WHEN video IS NULL OR video = '' THEN 'НЕТ ВИДЕО' END AS missing_video,
    CASE WHEN photo_count < 5 THEN 'МАЛО ФОТО (<5)' END AS missing_photos,
    CASE WHEN LENGTH(description) < 50 THEN 'НЕТ ОПИСАНИЯ' END AS missing_desc,
    CASE WHEN char_count < 10 THEN 'МАЛО ХАРАКТЕРИСТИК (<10)' END AS missing_chars,
    CASE WHEN size_count < 2 THEN 'МАЛО РАЗМЕРОВ' END AS missing_sizes
FROM card_completeness
WHERE (has_enough_photos + has_video + has_good_desc + has_enough_chars + has_sizes) < 4
ORDER BY completeness_score ASC, product_rating ASC
LIMIT 200;


-- ╔═══════════════════════════════════════════════════════════════════════════╗
-- ║ ЗАПРОС 2: ВЫСОКИЙ ТРАФИК — НУЛЕВАЯ КОНВЕРСИЯ                            ║
-- ║ Товары, которые показываются, но не продают                             ║
-- ╚═══════════════════════════════════════════════════════════════════════════╝
--
-- Проблема: 100+ просмотров и 0 выкупов = проблема в карточке
--           (главное фото, цена, заголовок не соответствуют ожиданиям).
-- Действие: Проверить главное фото, цену, заголовок. Сравнить с лидерами категории.

SELECT
    c.nm_id,
    c.vendor_code,
    c.title,
    c.subject_name,
    ph.photo_count,
    p.product_rating,
    p.feedback_rating,
    p.stock_wb,
    f.selected_open_count,
    f.selected_cart_count,
    f.selected_order_count,
    f.selected_buyout_count,
    ROUND(f.selected_conversion_add_to_cart, 1) AS conv_open_to_cart,
    ROUND(f.selected_conversion_cart_to_order, 1) AS conv_cart_to_order,
    ROUND(f.selected_conversion_buyout, 1) AS conv_buyout,
    cat_avg.avg_cat_buyout_conv,
    ROUND(f.selected_conversion_buyout - cat_avg.avg_cat_buyout_conv, 1) AS vs_category
FROM cards c
JOIN products p ON c.nm_id = p.nm_id
JOIN funnel_metrics_aggregated f ON c.nm_id = f.nm_id
    AND f.period_start = '2026-03-23'
LEFT JOIN (
    SELECT nm_id, COUNT(*) AS photo_count FROM card_photos GROUP BY nm_id
) ph ON c.nm_id = ph.nm_id
LEFT JOIN (
    SELECT
        c2.subject_name,
        ROUND(AVG(f2.selected_conversion_buyout), 1) AS avg_cat_buyout_conv
    FROM funnel_metrics_aggregated f2
    JOIN cards c2 ON f2.nm_id = c2.nm_id
    WHERE c2.vendor_code GLOB '[0-9]26*'
      AND f2.period_start = '2026-03-23'
      AND f2.selected_open_count >= 50
    GROUP BY c2.subject_name
) cat_avg ON c.subject_name = cat_avg.subject_name
WHERE c.vendor_code GLOB '[0-9]26*'
  AND f.selected_open_count >= 100
  AND f.selected_buyout_count = 0
ORDER BY f.selected_open_count DESC
LIMIT 150;


-- ╔═══════════════════════════════════════════════════════════════════════════╗
-- ║ ЗАПРОС 3: ОТСТАЮЩИЕ ПО КОНВЕРСИИ ВНУТРИ КАТЕГОРИИ                      ║
-- ║ Продукты с конверсией < 50% от среднего по категории                    ║
-- ╚═══════════════════════════════════════════════════════════════════════════╝
--
-- Проблема: WB алгоритм даёт больше показов товарам с высокой конверсией.
--           Отстающие попадают в спираль смерти: меньше показов → меньше продаж.
-- Действие: Если карточка полная, но конверсия низкая — проблема в цене или конкуренции.

WITH category_benchmarks AS (
    SELECT
        c.subject_name,
        COUNT(*) AS product_count,
        ROUND(AVG(f.selected_conversion_buyout), 1) AS avg_buyout_conv,
        ROUND(AVG(f.selected_conversion_add_to_cart), 1) AS avg_cart_conv
    FROM funnel_metrics_aggregated f
    JOIN cards c ON f.nm_id = c.nm_id
    WHERE c.vendor_code GLOB '[0-9]26*'
      AND f.period_start = '2026-03-23'
      AND f.selected_open_count >= 50
    GROUP BY c.subject_name
    HAVING COUNT(*) >= 5
),
product_metrics AS (
    SELECT
        c.nm_id,
        c.vendor_code,
        c.title,
        c.subject_name,
        ph.photo_count,
        p.product_rating,
        p.feedback_rating,
        p.stock_wb,
        f.selected_open_count,
        f.selected_buyout_count,
        f.selected_conversion_add_to_cart AS cart_conv,
        f.selected_conversion_buyout AS buyout_conv
    FROM cards c
    JOIN products p ON c.nm_id = p.nm_id
    JOIN funnel_metrics_aggregated f ON c.nm_id = f.nm_id
        AND f.period_start = '2026-03-23'
    LEFT JOIN (
        SELECT nm_id, COUNT(*) AS photo_count FROM card_photos GROUP BY nm_id
    ) ph ON c.nm_id = ph.nm_id
    WHERE c.vendor_code GLOB '[0-9]26*'
      AND f.selected_open_count >= 50
)
SELECT
    pm.nm_id,
    pm.vendor_code,
    pm.subject_name,
    pm.product_rating,
    pm.feedback_rating,
    pm.photo_count,
    pm.stock_wb,
    pm.selected_open_count,
    pm.selected_buyout_count,
    ROUND(pm.buyout_conv, 1) AS product_conv,
    cb.avg_buyout_conv AS category_avg_conv,
    ROUND(pm.buyout_conv - cb.avg_buyout_conv, 1) AS gap_vs_category,
    ROUND(pm.cart_conv, 1) AS product_cart_conv,
    cb.avg_cart_conv AS category_avg_cart_conv
FROM product_metrics pm
JOIN category_benchmarks cb ON pm.subject_name = cb.subject_name
WHERE pm.buyout_conv < cb.avg_buyout_conv * 0.5
  AND cb.product_count >= 10
ORDER BY gap_vs_category ASC
LIMIT 200;


-- ╔═══════════════════════════════════════════════════════════════════════════╗
-- ║ ЗАПРОС 4: СРАВНЕНИЕ КОЛЛЕКЦИЙ — 2026 vs 2024/2025                      ║
-- ║ Показывает, в каких категориях новая коллекция отстаёт от старой         ║
-- ╚═══════════════════════════════════════════════════════════════════════════╝
--
-- Проблема: Системное отставание НК от проверенных старых коллекций.
-- Действие: Категории с наибольшим разрывом конверсии — приоритет для работы.
--           Сравнить фото, описания, рейтинги между коллекциями.

WITH nc_metrics AS (
    SELECT
        c.subject_name,
        COUNT(DISTINCT c.nm_id) AS product_count,
        ROUND(AVG(f.selected_open_count), 0) AS avg_opens,
        ROUND(AVG(f.selected_buyout_count), 1) AS avg_buyouts,
        ROUND(AVG(f.selected_conversion_buyout), 1) AS avg_buyout_conv,
        ROUND(AVG(f.selected_conversion_add_to_cart), 1) AS avg_cart_conv,
        ROUND(AVG(ph.photo_count), 1) AS avg_photos,
        ROUND(AVG(p.product_rating), 1) AS avg_product_rating,
        ROUND(AVG(p.feedback_rating), 1) AS avg_feedback_rating,
        SUM(CASE WHEN p.stock_wb > 0 THEN 1 ELSE 0 END) AS in_stock_count
    FROM cards c
    JOIN products p ON c.nm_id = p.nm_id
    JOIN funnel_metrics_aggregated f ON c.nm_id = f.nm_id
        AND f.period_start = '2026-03-23'
    LEFT JOIN (
        SELECT nm_id, COUNT(*) AS photo_count FROM card_photos GROUP BY nm_id
    ) ph ON c.nm_id = ph.nm_id
    WHERE c.vendor_code GLOB '[0-9]26*'
    GROUP BY c.subject_name
),
oc_metrics AS (
    SELECT
        c.subject_name,
        COUNT(DISTINCT c.nm_id) AS product_count,
        ROUND(AVG(f.selected_open_count), 0) AS avg_opens,
        ROUND(AVG(f.selected_buyout_count), 1) AS avg_buyouts,
        ROUND(AVG(f.selected_conversion_buyout), 1) AS avg_buyout_conv,
        ROUND(AVG(f.selected_conversion_add_to_cart), 1) AS avg_cart_conv,
        ROUND(AVG(ph.photo_count), 1) AS avg_photos,
        ROUND(AVG(p.product_rating), 1) AS avg_product_rating,
        ROUND(AVG(p.feedback_rating), 1) AS avg_feedback_rating,
        SUM(CASE WHEN p.stock_wb > 0 THEN 1 ELSE 0 END) AS in_stock_count
    FROM cards c
    JOIN products p ON c.nm_id = p.nm_id
    JOIN funnel_metrics_aggregated f ON c.nm_id = f.nm_id
        AND f.period_start = '2026-03-23'
    LEFT JOIN (
        SELECT nm_id, COUNT(*) AS photo_count FROM card_photos GROUP BY nm_id
    ) ph ON c.nm_id = ph.nm_id
    WHERE (c.vendor_code GLOB '[0-9]24*' OR c.vendor_code GLOB '[0-9]25*')
    GROUP BY c.subject_name
)
SELECT
    COALESCE(nc.subject_name, oc.subject_name) AS subject_name,
    nc.product_count AS nc_products,
    oc.product_count AS oc_products,
    nc.avg_buyout_conv AS nc_buyout_conv,
    oc.avg_buyout_conv AS oc_buyout_conv,
    ROUND(nc.avg_buyout_conv - oc.avg_buyout_conv, 1) AS conv_gap,
    nc.avg_cart_conv AS nc_cart_conv,
    oc.avg_cart_conv AS oc_cart_conv,
    nc.avg_photos AS nc_avg_photos,
    oc.avg_photos AS oc_avg_photos,
    nc.avg_product_rating AS nc_quality_rating,
    oc.avg_product_rating AS oc_quality_rating,
    nc.avg_feedback_rating AS nc_feedback_rating,
    oc.avg_feedback_rating AS oc_feedback_rating,
    nc.in_stock_count AS nc_in_stock,
    nc.product_count - nc.in_stock_count AS nc_no_stock
FROM nc_metrics nc
LEFT JOIN oc_metrics oc ON nc.subject_name = oc.subject_name
WHERE oc.product_count >= 10
ORDER BY conv_gap ASC
LIMIT 30;


-- ╔═══════════════════════════════════════════════════════════════════════════╗
-- ║ ЗАПРОС 5: МЁРТВЫЙ ЗАПАС — НА СКЛАДЕ, НО НЕТ ПРОДАЖ                     ║
-- ║ Товары с остатками, которые не продаются                                ║
-- ╚═══════════════════════════════════════════════════════════════════════════╝
--
-- Проблема: Товар занимает место на складе и генерит расходы на хранение.
-- Действие: Нет трафика → проверить индексацию. Трафик есть, но не кликают →
--           карточка/цена. Добавляют в корзину, но не покупают → цена/доставка/отзывы.

WITH nc_stock_sales AS (
    SELECT
        c.nm_id,
        c.vendor_code,
        c.title,
        c.subject_name,
        p.stock_wb,
        p.product_rating,
        p.feedback_rating,
        ph.photo_count,
        f.selected_open_count,
        f.selected_cart_count,
        f.selected_buyout_count,
        f.selected_conversion_buyout,
        fb.recent_fb_count,
        fb.avg_fb_rating
    FROM cards c
    JOIN products p ON c.nm_id = p.nm_id
    LEFT JOIN funnel_metrics_aggregated f ON c.nm_id = f.nm_id
        AND f.period_start = '2026-03-23'
    LEFT JOIN (
        SELECT nm_id, COUNT(*) AS photo_count FROM card_photos GROUP BY nm_id
    ) ph ON c.nm_id = ph.nm_id
    LEFT JOIN (
        SELECT
            product_nm_id,
            COUNT(*) AS recent_fb_count,
            ROUND(AVG(product_valuation), 1) AS avg_fb_rating
        FROM feedbacks
        WHERE created_date >= '2026-03-01'
        GROUP BY product_nm_id
    ) fb ON c.nm_id = fb.product_nm_id
    WHERE c.vendor_code GLOB '[0-9]26*'
      AND p.stock_wb > 0
)
SELECT
    nm_id,
    vendor_code,
    subject_name,
    stock_wb,
    product_rating,
    feedback_rating,
    photo_count,
    selected_open_count,
    selected_cart_count,
    selected_buyout_count,
    CASE
        WHEN selected_open_count = 0 OR selected_open_count IS NULL THEN 'НЕТ ТРАФИКА'
        WHEN selected_open_count > 0 AND selected_cart_count = 0 THEN 'ТРАФИК ЕСТЬ, НО НЕ КЛИКАЮТ'
        WHEN selected_cart_count > 0 AND (selected_buyout_count = 0 OR selected_buyout_count IS NULL)
            THEN 'В КОРЗИНУ ДОБАВЛЯЮТ, НО НЕ ПОКУПАЮТ'
        ELSE 'ЕСТЬ ПРОДАЖИ'
    END AS funnel_problem,
    recent_fb_count,
    avg_fb_rating,
    CASE
        WHEN product_rating < 8 THEN 'НИЗКИЙ РЕЙТИНГ КАЧЕСТВА'
        WHEN photo_count < 5 THEN 'МАЛО ФОТО'
        WHEN feedback_rating > 0 AND feedback_rating < 4.5 THEN 'ПЛОХИЕ ОТЗЫВЫ'
        WHEN selected_open_count = 0 OR selected_open_count IS NULL THEN 'ПРОВЕРИТЬ ИНДЕКСАЦИЮ'
        ELSE 'ПРОВЕРИТЬ ЦЕНУ'
    END AS likely_cause
FROM nc_stock_sales
WHERE selected_buyout_count = 0 OR selected_buyout_count IS NULL
ORDER BY stock_wb DESC, selected_open_count ASC
LIMIT 200;


-- ╔═══════════════════════════════════════════════════════════════════════════╗
-- ║ ЗАПРОС 6: АНАЛИЗ ОТЗЫВОВ — ПРОБЛЕМНЫЕ ТОВАРЫ                           ║
-- ║ Плохие отзывы подавляют конверсию, отсутствие отзывов = нет соц.док-ва  ║
-- ╚═══════════════════════════════════════════════════════════════════════════╝
--
-- Проблема: Негативные отзывы → смерть конверсии. Нет отзывов → нет доверия.
-- Действие: Прочитать негативные отзывы → исправить проблему (размер, материал,
--           несоответствие фото). Товары с 200+ просмотров без отзывов — стимулировать.

WITH fb_stats AS (
    SELECT
        fb.product_nm_id,
        COUNT(*) AS total_feedbacks,
        SUM(CASE WHEN fb.product_valuation >= 4 THEN 1 ELSE 0 END) AS positive_fb,
        SUM(CASE WHEN fb.product_valuation <= 2 THEN 1 ELSE 0 END) AS negative_fb,
        ROUND(AVG(fb.product_valuation), 2) AS avg_rating,
        GROUP_CONCAT(
            CASE WHEN fb.product_valuation <= 2 AND fb.text != ''
                 THEN SUBSTR(fb.text, 1, 80)
            END, ' | '
        ) AS negative_snippets
    FROM feedbacks fb
    JOIN cards c ON fb.product_nm_id = c.nm_id
    WHERE c.vendor_code GLOB '[0-9]26*'
    GROUP BY fb.product_nm_id
),
nc_base AS (
    SELECT
        c.nm_id,
        c.vendor_code,
        c.title,
        c.subject_name,
        p.product_rating,
        p.feedback_rating,
        p.stock_wb,
        f.selected_open_count,
        f.selected_buyout_count,
        f.selected_conversion_buyout
    FROM cards c
    LEFT JOIN products p ON c.nm_id = p.nm_id
    LEFT JOIN funnel_metrics_aggregated f ON c.nm_id = f.nm_id
        AND f.period_start = '2026-03-23'
    WHERE c.vendor_code GLOB '[0-9]26*'
)
SELECT
    b.nm_id,
    b.vendor_code,
    b.subject_name,
    b.product_rating,
    b.feedback_rating,
    b.stock_wb,
    b.selected_open_count,
    b.selected_buyout_count,
    ROUND(b.selected_conversion_buyout, 1) AS buyout_conv,
    fs.total_feedbacks,
    fs.avg_rating AS avg_fb_rating,
    fs.negative_fb,
    fs.positive_fb,
    CASE
        WHEN fs.total_feedbacks IS NULL THEN 'НЕТ ОТЗЫВОВ - НЕТ СОЦ. ДОКАЗАТЕЛЬСТВА'
        WHEN fs.avg_rating < 4.0 THEN 'НИЗКИЙ РЕЙТИНГ ОТЗЫВОВ'
        WHEN fs.negative_fb >= 3 THEN 'ЕСТЬ 3+ НЕГАТИВНЫХ ОТЗЫВА'
        ELSE 'ОТЗЫВЫ НОРМАЛЬНЫЕ'
    END AS feedback_status,
    SUBSTR(fs.negative_snippets, 1, 200) AS negative_snippets
FROM nc_base b
LEFT JOIN fb_stats fs ON b.nm_id = fs.product_nm_id
WHERE fs.total_feedbacks IS NULL
   OR fs.avg_rating < 4.0
   OR fs.negative_fb >= 3
ORDER BY
    CASE
        WHEN fs.avg_rating < 3.5 THEN 0
        WHEN fs.total_feedbacks IS NULL AND b.selected_open_count > 200 THEN 1
        WHEN fs.negative_fb >= 3 THEN 2
        ELSE 3
    END,
    b.selected_open_count DESC
LIMIT 150;


-- ╔═══════════════════════════════════════════════════════════════════════════╗
-- ║ ЗАПРОС 7: НИЗКИЙ PRODUCT_RATING — РЕЙТИНГ КАЧЕСТВА КАРТОЧКИ             ║
-- ║ product_rating < 9 → меньше показов, конверсия ~33% (vs 44% при 9.5-10) ║
-- ╚═══════════════════════════════════════════════════════════════════════════╝
--
-- Проблема: Низкий product_rating = меньше показов в поиске WB.
-- Действие: Колонка recommended_action указывает что именно улучшить для каждого товара.

WITH rating_analysis AS (
    SELECT
        c.nm_id,
        c.vendor_code,
        c.title,
        c.subject_name,
        p.product_rating,
        p.feedback_rating,
        p.stock_wb,
        ph.photo_count,
        ch.char_count,
        CASE WHEN c.video IS NOT NULL AND c.video != '' THEN 1 ELSE 0 END AS has_video,
        CASE WHEN LENGTH(c.description) >= 200 THEN 1 ELSE 0 END AS has_good_desc,
        f.selected_open_count,
        f.selected_buyout_count,
        f.selected_conversion_buyout AS buyout_conv,
        f.selected_order_sum
    FROM cards c
    JOIN products p ON c.nm_id = p.nm_id
    JOIN funnel_metrics_aggregated f ON c.nm_id = f.nm_id
        AND f.period_start = '2026-03-23'
    LEFT JOIN (
        SELECT nm_id, COUNT(*) AS photo_count FROM card_photos GROUP BY nm_id
    ) ph ON c.nm_id = ph.nm_id
    LEFT JOIN (
        SELECT nm_id, COUNT(*) AS char_count FROM card_characteristics GROUP BY nm_id
    ) ch ON c.nm_id = ch.nm_id
    WHERE c.vendor_code GLOB '[0-9]26*'
      AND f.selected_open_count >= 30
)
SELECT
    nm_id,
    vendor_code,
    subject_name,
    product_rating,
    feedback_rating,
    photo_count,
    char_count,
    has_video,
    has_good_desc,
    stock_wb,
    selected_open_count,
    selected_buyout_count,
    ROUND(buyout_conv, 1) AS buyout_conv,
    selected_order_sum,
    CASE
        WHEN photo_count < 5 THEN 'ДОБАВИТЬ ФОТО'
        WHEN has_video = 0 THEN 'ДОБАВИТЬ ВИДЕО'
        WHEN char_count < 10 THEN 'ДОПОЛНИТЬ ХАРАКТЕРИСТИКИ'
        WHEN has_good_desc = 0 THEN 'УЛУЧШИТЬ ОПИСАНИЕ'
        WHEN feedback_rating > 0 AND feedback_rating < 4.0 THEN 'ПРОБЛЕМА С КАЧЕСТВОМ ТОВАРА'
        ELSE 'ПРОВЕРИТЬ ЦЕЛЕВОЙ ЗАПРОС'
    END AS recommended_action
FROM rating_analysis
WHERE product_rating < 9.0
ORDER BY product_rating ASC, selected_open_count DESC
LIMIT 150;


-- ╔═══════════════════════════════════════════════════════════════════════════╗
-- ║ ЗАПРОС 8: СВОДНЫЙ ДАШБОРД — РАНЖИРОВАНИЕ ПРОБЛЕМНЫХ ТОВАРОВ            ║
-- ║ Объединяет все диагнозы, считает кол-во проблем и упущенные выкупы      ║
-- ╚═══════════════════════════════════════════════════════════════════════════╝
--
-- Проблема: Нужен единый приоритизированный список для менеджера.
-- Действие: Сортировать по estimated_lost_buyouts — это рейтинг упущенной выгоды.
--           Начать с товаров с 3+ проблемами и высоким трафиком.

WITH completeness AS (
    SELECT
        c.nm_id,
        (CASE WHEN ph.photo_count >= 8 THEN 1 ELSE 0 END
         + CASE WHEN c.video IS NOT NULL AND c.video != '' THEN 1 ELSE 0 END
         + CASE WHEN LENGTH(c.description) >= 200 THEN 1 ELSE 0 END
         + CASE WHEN ch.char_count >= 12 THEN 1 ELSE 0 END
         + CASE WHEN cs.size_count >= 2 THEN 1 ELSE 0 END
        ) AS completeness_score,
        ph.photo_count,
        CASE WHEN c.video IS NOT NULL AND c.video != '' THEN 1 ELSE 0 END AS has_video,
        ch.char_count
    FROM cards c
    LEFT JOIN (SELECT nm_id, COUNT(*) AS photo_count FROM card_photos GROUP BY nm_id) ph ON c.nm_id = ph.nm_id
    LEFT JOIN (SELECT nm_id, COUNT(*) AS char_count FROM card_characteristics GROUP BY nm_id) ch ON c.nm_id = ch.nm_id
    LEFT JOIN (SELECT nm_id, COUNT(*) AS size_count FROM card_sizes GROUP BY nm_id) cs ON c.nm_id = cs.nm_id
    WHERE c.vendor_code GLOB '[0-9]26*'
),
fb_stats AS (
    SELECT
        product_nm_id,
        COUNT(*) AS total_fb,
        ROUND(AVG(product_valuation), 1) AS avg_fb_rating,
        SUM(CASE WHEN product_valuation <= 2 THEN 1 ELSE 0 END) AS neg_fb
    FROM feedbacks
    GROUP BY product_nm_id
),
cat_benchmarks AS (
    SELECT
        c.subject_name,
        ROUND(AVG(f.selected_conversion_buyout), 1) AS cat_avg_conv
    FROM funnel_metrics_aggregated f
    JOIN cards c ON f.nm_id = c.nm_id
    WHERE c.vendor_code GLOB '[0-9]26*'
      AND f.period_start = '2026-03-23'
      AND f.selected_open_count >= 50
    GROUP BY c.subject_name
    HAVING COUNT(*) >= 5
),
problem_products AS (
    SELECT
        c.nm_id,
        c.vendor_code,
        c.title,
        c.subject_name,
        p.product_rating,
        p.feedback_rating,
        p.stock_wb,
        comp.completeness_score,
        comp.photo_count,
        comp.has_video,
        comp.char_count,
        f.selected_open_count,
        f.selected_cart_count,
        f.selected_order_count,
        f.selected_buyout_count,
        ROUND(f.selected_conversion_buyout, 1) AS buyout_conv,
        COALESCE(cb.cat_avg_conv, 0) AS cat_avg_conv,
        COALESCE(fs.total_fb, 0) AS total_fb,
        COALESCE(fs.avg_fb_rating, 0) AS avg_fb_rating,
        COALESCE(fs.neg_fb, 0) AS neg_fb,
        CASE WHEN comp.completeness_score < 3 THEN 1 ELSE 0 END AS flag_incomplete,
        CASE WHEN p.product_rating < 8 THEN 1 ELSE 0 END AS flag_low_quality,
        CASE WHEN f.selected_open_count >= 100 AND f.selected_buyout_count = 0 THEN 1 ELSE 0 END AS flag_no_buyout_with_traffic,
        CASE WHEN p.stock_wb = 0 OR p.stock_wb IS NULL THEN 1 ELSE 0 END AS flag_no_stock,
        CASE WHEN f.selected_buyout_count > 0 AND cb.cat_avg_conv > 0
             AND f.selected_conversion_buyout < cb.cat_avg_conv * 0.5 THEN 1 ELSE 0 END AS flag_below_category,
        CASE WHEN COALESCE(fs.total_fb, 0) = 0 AND f.selected_open_count >= 200 THEN 1 ELSE 0 END AS flag_no_feedbacks,
        CASE WHEN COALESCE(fs.neg_fb, 0) >= 3 THEN 1 ELSE 0 END AS flag_negative_feedbacks
    FROM cards c
    JOIN products p ON c.nm_id = p.nm_id
    LEFT JOIN funnel_metrics_aggregated f ON c.nm_id = f.nm_id
        AND f.period_start = '2026-03-23'
    LEFT JOIN completeness comp ON c.nm_id = comp.nm_id
    LEFT JOIN fb_stats fs ON c.nm_id = fs.product_nm_id
    LEFT JOIN cat_benchmarks cb ON c.subject_name = cb.subject_name
    WHERE c.vendor_code GLOB '[0-9]26*'
)
SELECT
    nm_id,
    vendor_code,
    subject_name,
    product_rating,
    stock_wb,
    completeness_score,
    photo_count,
    buyout_conv,
    cat_avg_conv,
    selected_open_count,
    selected_buyout_count,
    total_fb,
    avg_fb_rating,
    (flag_incomplete + flag_low_quality + flag_no_buyout_with_traffic
     + flag_no_stock + flag_below_category + flag_no_feedbacks + flag_negative_feedbacks
    ) AS problem_count,
    CASE WHEN flag_no_stock = 1 THEN 'НЕТ НА СКЛАДЕ; ' ELSE '' END
    || CASE WHEN flag_no_buyout_with_traffic = 1 THEN 'НЕТ ВЫКУПОВ ПРИ ТРАФИКЕ; ' ELSE '' END
    || CASE WHEN flag_incomplete = 1 THEN 'НЕПОЛНАЯ КАРТОЧКА (' || completeness_score || '/5); ' ELSE '' END
    || CASE WHEN flag_low_quality = 1 THEN 'НИЗКИЙ PRODUCT_RATING; ' ELSE '' END
    || CASE WHEN flag_below_category = 1 THEN 'НИЖЕ МЕДИАНЫ КАТЕГОРИИ; ' ELSE '' END
    || CASE WHEN flag_no_feedbacks = 1 THEN 'НЕТ ОТЗЫВОВ; ' ELSE '' END
    || CASE WHEN flag_negative_feedbacks = 1 THEN 'НЕГАТИВНЫЕ ОТЗЫВЫ (' || neg_fb || '); ' ELSE '' END
    AS problems_summary,
    CASE WHEN cat_avg_conv > 0 AND selected_buyout_count < cat_avg_conv
         THEN ROUND((cat_avg_conv - buyout_conv) * selected_open_count / 100, 1)
         ELSE 0
    END AS estimated_lost_buyouts
FROM problem_products
WHERE (flag_incomplete + flag_low_quality + flag_no_buyout_with_traffic
       + flag_no_stock + flag_below_category + flag_no_feedbacks + flag_negative_feedbacks) >= 2
ORDER BY
    estimated_lost_buyouts DESC,
    problem_count DESC,
    selected_open_count DESC
LIMIT 300;
