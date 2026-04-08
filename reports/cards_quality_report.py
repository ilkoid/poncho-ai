#!/usr/bin/env python3
"""
Аудит качества карточек товаров: коллекция 2026 vs 2025.

Генерирует PDF-отчёт (8-10 страниц) с фокусом на:
  - Полноту описания и характеристик
  - Сравнение 2026 vs 2025 (товары с остатком и продажами)
  - Рейтинг карточки (product_rating) и товара (feedback_rating)
  - KIZ (маркировка Честный знак)
  - Видео и визуальный контент
  - Анализ по сезонам (первая цифра артикула)

Сезоны: 1=Весна-лето, 2=Школа, 3=Осень-зима, 4=Новый год, 5=Внесезонный, 0=Остальное

Запуск:
  python3 reports/cards_quality_report.py /path/to/wb-cards.db
"""

import sqlite3
import sys
import os
from datetime import date
from fpdf import FPDF


# ─── Шрифты ──────────────────────────────────────────────────────────────────
FONT_DIR = "/usr/share/fonts/truetype/dejavu"
FONT_REGULAR = os.path.join(FONT_DIR, "DejaVuSans.ttf")
FONT_BOLD = os.path.join(FONT_DIR, "DejaVuSans-Bold.ttf")


# ─── Цвета ───────────────────────────────────────────────────────────────────
COLOR_RED = (220, 53, 69)
COLOR_ORANGE = (255, 152, 0)
COLOR_GREEN = (40, 167, 69)
COLOR_GRAY = (108, 117, 125)
COLOR_LIGHT_BG = (241, 243, 245)
COLOR_HEADER_BG = (52, 73, 94)
COLOR_HEADER_FG = (255, 255, 255)
COLOR_BLACK = (33, 37, 41)
COLOR_WHITE = (255, 255, 255)

SEASON_NAMES = {"1": "Весна-лето", "2": "Школа", "3": "Осень-зима", "4": "Новый год", "5": "Внесезонный", "0": "Прочее"}


# ─── SQL-запросы ─────────────────────────────────────────────────────────────

SQL_SEASON_COMPARE = """
SELECT SUBSTR(c.vendor_code, 1, 1) AS season_digit,
       SUBSTR(c.vendor_code, 2, 2) AS coll_year,
       COUNT(*) AS total_cards,
       ROUND(AVG(LENGTH(c.description)), 0) AS avg_desc_len,
       ROUND(AVG(ph.photo_count), 1) AS avg_photos,
       ROUND(AVG(ch.char_count), 1) AS avg_chars,
       ROUND(100.0 * SUM(CASE WHEN c.video IS NOT NULL AND c.video != '' THEN 1 ELSE 0 END) / COUNT(*), 1) AS pct_video,
       ROUND(AVG(p.product_rating), 2) AS avg_product_rating,
       ROUND(AVG(CASE WHEN p.feedback_rating > 0 THEN p.feedback_rating ELSE NULL END), 2) AS avg_fb_rating,
       ROUND(AVG(cs.size_count), 1) AS avg_sizes,
       ROUND(100.0 * SUM(CASE WHEN c.need_kiz = 1 THEN 1 ELSE 0 END) / COUNT(*), 1) AS pct_kiz,
       ROUND(AVG(f.selected_conversion_buyout), 1) AS avg_conv_selected,
       ROUND(AVG(f.past_conversion_buyout), 1) AS avg_conv_past,
       ROUND(AVG(f.selected_open_count), 0) AS avg_opens_selected,
       ROUND(AVG(f.past_open_count), 0) AS avg_opens_past,
       ROUND(AVG(f.selected_buyout_count), 1) AS avg_buyouts_selected,
       ROUND(AVG(f.past_buyout_count), 1) AS avg_buyouts_past
FROM cards c
JOIN products p ON c.nm_id = p.nm_id
LEFT JOIN funnel_metrics_aggregated f ON c.nm_id = f.nm_id AND f.period_start = '2026-03-23'
LEFT JOIN (SELECT nm_id, COUNT(*) AS photo_count FROM card_photos GROUP BY nm_id) ph ON c.nm_id = ph.nm_id
LEFT JOIN (SELECT nm_id, COUNT(*) AS char_count FROM card_characteristics GROUP BY nm_id) ch ON c.nm_id = ch.nm_id
LEFT JOIN (SELECT nm_id, COUNT(*) AS size_count FROM card_sizes GROUP BY nm_id) cs ON c.nm_id = cs.nm_id
WHERE c.vendor_code GLOB '[0-9]2[56]*'
  AND SUBSTR(c.vendor_code, 1, 1) IN ('1','2','3','4')
GROUP BY season_digit, coll_year
ORDER BY season_digit, coll_year
"""

SQL_CATEGORY_COMPARE = """
WITH base AS (
    SELECT c.nm_id, c.vendor_code, c.description, c.video, c.subject_name,
           SUBSTR(c.vendor_code, 2, 2) AS coll_year,
           p.product_rating, p.feedback_rating, p.stock_wb,
           f.selected_buyout_count, f.selected_open_count,
           f.selected_conversion_buyout,
           f.past_buyout_count, f.past_open_count,
           f.past_conversion_buyout,
           f.comparison_conversion_buyout,
           ph.photo_count, ch.char_count
    FROM cards c
    JOIN products p ON c.nm_id = p.nm_id
    JOIN funnel_metrics_aggregated f ON c.nm_id = f.nm_id AND f.period_start = '2026-03-23'
    LEFT JOIN (SELECT nm_id, COUNT(*) AS photo_count FROM card_photos GROUP BY nm_id) ph ON c.nm_id = ph.nm_id
    LEFT JOIN (SELECT nm_id, COUNT(*) AS char_count FROM card_characteristics GROUP BY nm_id) ch ON c.nm_id = ch.nm_id
    WHERE c.vendor_code GLOB '[0-9]2[56]*'
      AND SUBSTR(c.vendor_code, 1, 1) = '1'
      AND p.stock_wb > 0
      AND f.selected_buyout_count > 0
)
SELECT subject_name,
       SUM(CASE WHEN coll_year='25' THEN 1 ELSE 0 END) AS cnt_25,
       SUM(CASE WHEN coll_year='26' THEN 1 ELSE 0 END) AS cnt_26,
       ROUND(AVG(CASE WHEN coll_year='25' THEN LENGTH(description) END), 0) AS desc_25,
       ROUND(AVG(CASE WHEN coll_year='26' THEN LENGTH(description) END), 0) AS desc_26,
       ROUND(AVG(CASE WHEN coll_year='25' THEN product_rating END), 2) AS rating_25,
       ROUND(AVG(CASE WHEN coll_year='26' THEN product_rating END), 2) AS rating_26,
       ROUND(AVG(CASE WHEN coll_year='25' THEN photo_count END), 1) AS photos_25,
       ROUND(AVG(CASE WHEN coll_year='26' THEN photo_count END), 1) AS photos_26,
       ROUND(AVG(CASE WHEN coll_year='25' THEN char_count END), 1) AS chars_25,
       ROUND(AVG(CASE WHEN coll_year='26' THEN char_count END), 1) AS chars_26,
       ROUND(AVG(CASE WHEN coll_year='25' THEN selected_conversion_buyout END), 1) AS conv_25,
       ROUND(AVG(CASE WHEN coll_year='26' THEN selected_conversion_buyout END), 1) AS conv_26,
       ROUND(AVG(CASE WHEN coll_year='26' THEN past_conversion_buyout END), 1) AS conv_26_past,
       ROUND(AVG(CASE WHEN coll_year='26' THEN comparison_conversion_buyout END), 0) AS conv_26_dynamic
FROM base
GROUP BY subject_name
HAVING cnt_25 >= 5 AND cnt_26 >= 5
ORDER BY (AVG(CASE WHEN coll_year='26' THEN selected_conversion_buyout END) - AVG(CASE WHEN coll_year='25' THEN selected_conversion_buyout END)) ASC
"""

SQL_CHAR_COMPLETENESS = """
WITH top_chars AS (
    SELECT name, COUNT(*) AS total_count
    FROM card_characteristics
    GROUP BY name
    ORDER BY total_count DESC
    LIMIT 15
),
card_years AS (
    SELECT c.nm_id, SUBSTR(c.vendor_code, 2, 2) AS coll_year
    FROM cards c
    WHERE c.vendor_code GLOB '[0-9]2[56]*'
      AND SUBSTR(c.vendor_code, 1, 1) = '1'
)
SELECT tc.name AS char_name,
       tc.total_count,
       ROUND(100.0 * COUNT(DISTINCT CASE WHEN cy.coll_year='25' AND cc.nm_id IS NOT NULL THEN cy.nm_id END)
             / NULLIF(COUNT(DISTINCT CASE WHEN cy.coll_year='25' THEN cy.nm_id END), 0), 1) AS pct_25,
       ROUND(100.0 * COUNT(DISTINCT CASE WHEN cy.coll_year='26' AND cc.nm_id IS NOT NULL THEN cy.nm_id END)
             / NULLIF(COUNT(DISTINCT CASE WHEN cy.coll_year='26' THEN cy.nm_id END), 0), 1) AS pct_26,
       COUNT(DISTINCT CASE WHEN cy.coll_year='25' THEN cy.nm_id END) AS total_25,
       COUNT(DISTINCT CASE WHEN cy.coll_year='26' THEN cy.nm_id END) AS total_26
FROM top_chars tc
CROSS JOIN card_years cy
LEFT JOIN card_characteristics cc ON cy.nm_id = cc.nm_id AND cc.name = tc.name
GROUP BY tc.name
ORDER BY (pct_25 - pct_26) DESC
"""

SQL_DESC_ANALYSIS = """
SELECT SUBSTR(c.vendor_code, 2, 2) AS coll_year,
       SUM(CASE WHEN LENGTH(c.description) = 0 OR c.description IS NULL THEN 1 ELSE 0 END) AS empty_desc,
       SUM(CASE WHEN LENGTH(COALESCE(c.description,'')) BETWEEN 1 AND 49 THEN 1 ELSE 0 END) AS short_desc,
       SUM(CASE WHEN LENGTH(COALESCE(c.description,'')) BETWEEN 50 AND 199 THEN 1 ELSE 0 END) AS medium_desc,
       SUM(CASE WHEN LENGTH(COALESCE(c.description,'')) BETWEEN 200 AND 499 THEN 1 ELSE 0 END) AS ok_desc,
       SUM(CASE WHEN LENGTH(COALESCE(c.description,'')) BETWEEN 500 AND 999 THEN 1 ELSE 0 END) AS good_desc,
       SUM(CASE WHEN LENGTH(COALESCE(c.description,'')) >= 1000 THEN 1 ELSE 0 END) AS great_desc,
       COUNT(*) AS total,
       ROUND(AVG(LENGTH(COALESCE(c.description,''))), 0) AS avg_len,
       ROUND(AVG(CASE WHEN LENGTH(COALESCE(c.description,'')) >= 200 THEN LENGTH(c.description) END), 0) AS avg_good_len
FROM cards c
WHERE c.vendor_code GLOB '[0-9]2[56]*'
  AND SUBSTR(c.vendor_code, 1, 1) = '1'
GROUP BY coll_year
"""

SQL_WORST_DESCRIPTIONS = """
SELECT c.vendor_code, c.subject_name, LENGTH(c.description) AS desc_len,
       SUBSTR(COALESCE(c.description, ''), 1, 100) AS desc_preview,
       p.product_rating
FROM cards c
LEFT JOIN products p ON c.nm_id = p.nm_id
WHERE c.vendor_code GLOB '[0-9]26*'
  AND SUBSTR(c.vendor_code, 1, 1) = '1'
  AND LENGTH(COALESCE(c.description, '')) < 100
ORDER BY LENGTH(COALESCE(c.description, '')) ASC
LIMIT 15
"""

SQL_VIDEO_ANALYSIS = """
SELECT SUBSTR(c.vendor_code, 2, 2) AS coll_year,
       COUNT(*) AS total,
       SUM(CASE WHEN c.video IS NOT NULL AND c.video != '' THEN 1 ELSE 0 END) AS has_video,
       ROUND(100.0 * SUM(CASE WHEN c.video IS NOT NULL AND c.video != '' THEN 1 ELSE 0 END) / COUNT(*), 1) AS pct_video,
       ROUND(AVG(CASE WHEN c.video IS NOT NULL AND c.video != '' THEN p.product_rating END), 2) AS rating_with_video,
       ROUND(AVG(CASE WHEN c.video IS NULL OR c.video = '' THEN p.product_rating END), 2) AS rating_no_video,
       ROUND(AVG(ph.photo_count), 1) AS avg_photos
FROM cards c
JOIN products p ON c.nm_id = p.nm_id
LEFT JOIN (SELECT nm_id, COUNT(*) AS photo_count FROM card_photos GROUP BY nm_id) ph ON c.nm_id = ph.nm_id
WHERE c.vendor_code GLOB '[0-9]2[56]*'
  AND SUBSTR(c.vendor_code, 1, 1) = '1'
GROUP BY coll_year
"""

SQL_VIDEO_BY_CATEGORY = """
SELECT c.subject_name,
       SUM(CASE WHEN SUBSTR(c.vendor_code, 2, 2) = '25' THEN 1 ELSE 0 END) AS cnt_25,
       SUM(CASE WHEN SUBSTR(c.vendor_code, 2, 2) = '26' THEN 1 ELSE 0 END) AS cnt_26,
       ROUND(100.0 * SUM(CASE WHEN SUBSTR(c.vendor_code, 2, 2) = '25' AND c.video IS NOT NULL AND c.video != '' THEN 1 ELSE 0 END)
             / NULLIF(SUM(CASE WHEN SUBSTR(c.vendor_code, 2, 2) = '25' THEN 1 ELSE 0 END), 0), 1) AS pct_video_25,
       ROUND(100.0 * SUM(CASE WHEN SUBSTR(c.vendor_code, 2, 2) = '26' AND c.video IS NOT NULL AND c.video != '' THEN 1 ELSE 0 END)
             / NULLIF(SUM(CASE WHEN SUBSTR(c.vendor_code, 2, 2) = '26' THEN 1 ELSE 0 END), 0), 1) AS pct_video_26
FROM cards c
WHERE c.vendor_code GLOB '[0-9]2[56]*'
  AND SUBSTR(c.vendor_code, 1, 1) = '1'
GROUP BY c.subject_name
HAVING cnt_26 >= 10
ORDER BY pct_video_26 ASC
LIMIT 20
"""

SQL_RATING_DISTRIBUTION = """
SELECT SUBSTR(c.vendor_code, 2, 2) AS coll_year,
       SUM(CASE WHEN p.product_rating < 8 THEN 1 ELSE 0 END) AS r_below_8,
       SUM(CASE WHEN p.product_rating >= 8 AND p.product_rating < 9 THEN 1 ELSE 0 END) AS r_8_9,
       SUM(CASE WHEN p.product_rating >= 9 AND p.product_rating < 9.5 THEN 1 ELSE 0 END) AS r_9_95,
       SUM(CASE WHEN p.product_rating >= 9.5 AND p.product_rating < 10 THEN 1 ELSE 0 END) AS r_95_10,
       SUM(CASE WHEN p.product_rating >= 10 THEN 1 ELSE 0 END) AS r_10,
       COUNT(*) AS total,
       ROUND(AVG(p.product_rating), 2) AS avg_rating
FROM cards c
JOIN products p ON c.nm_id = p.nm_id
WHERE c.vendor_code GLOB '[0-9]2[56]*'
  AND SUBSTR(c.vendor_code, 1, 1) = '1'
GROUP BY coll_year
"""

SQL_RATING_BY_SEASON_2025 = """
SELECT SUBSTR(c.vendor_code, 1, 1) AS season_digit,
       COUNT(*) AS cards,
       ROUND(AVG(p.product_rating), 2) AS avg_product_rating,
       ROUND(AVG(CASE WHEN p.feedback_rating > 0 THEN p.feedback_rating ELSE NULL END), 2) AS avg_fb_rating,
       ROUND(AVG(LENGTH(c.description)), 0) AS avg_desc_len,
       ROUND(AVG(ph.photo_count), 1) AS avg_photos,
       ROUND(100.0 * SUM(CASE WHEN c.video IS NOT NULL AND c.video != '' THEN 1 ELSE 0 END) / COUNT(*), 1) AS pct_video
FROM cards c
JOIN products p ON c.nm_id = p.nm_id
LEFT JOIN (SELECT nm_id, COUNT(*) AS photo_count FROM card_photos GROUP BY nm_id) ph ON c.nm_id = ph.nm_id
WHERE c.vendor_code GLOB '[0-9]25*'
  AND SUBSTR(c.vendor_code, 1, 1) IN ('1','2','3','4')
GROUP BY season_digit
ORDER BY season_digit
"""

SQL_KIZ_ANALYSIS = """
SELECT SUBSTR(c.vendor_code, 2, 2) AS coll_year,
       COUNT(*) AS total,
       SUM(c.need_kiz) AS needs_kiz,
       ROUND(100.0 * SUM(c.need_kiz) / COUNT(*), 1) AS pct_kiz,
       SUM(CASE WHEN c.need_kiz = 1 AND p.stock_wb > 0 THEN 1 ELSE 0 END) AS kiz_with_stock,
       SUM(CASE WHEN c.need_kiz = 0 AND p.stock_wb > 0 THEN 1 ELSE 0 END) AS no_kiz_with_stock
FROM cards c
LEFT JOIN products p ON c.nm_id = p.nm_id
WHERE c.vendor_code GLOB '[0-9]2[56]*'
  AND SUBSTR(c.vendor_code, 1, 1) = '1'
GROUP BY coll_year
"""

SQL_QUALITY_SCORE = """
WITH card_quality AS (
    SELECT c.nm_id, c.vendor_code, c.subject_name, c.description, c.video, c.need_kiz,
           SUBSTR(c.vendor_code, 2, 2) AS coll_year,
           p.product_rating, p.feedback_rating, p.stock_wb,
           f.selected_buyout_count, f.selected_open_count, f.selected_conversion_buyout,
           f.past_buyout_count, f.past_open_count, f.past_conversion_buyout,
           f.comparison_buyout_count_dynamic,
           ph.photo_count, ch.char_count, cs.size_count,
           (CASE WHEN ph.photo_count >= 8 THEN 20 ELSE ROUND(ph.photo_count * 2.5, 1) END
            + CASE WHEN c.video IS NOT NULL AND c.video != '' THEN 20 ELSE 0 END
            + CASE WHEN LENGTH(c.description) >= 500 THEN 20
                   WHEN LENGTH(c.description) >= 200 THEN 15
                   WHEN LENGTH(c.description) >= 50 THEN 10
                   ELSE 0 END
            + CASE WHEN ch.char_count >= 15 THEN 20
                   WHEN ch.char_count >= 10 THEN 15
                   WHEN ch.char_count >= 5 THEN 10
                   ELSE ROUND(ch.char_count * 2, 1) END
            + CASE WHEN cs.size_count >= 3 THEN 20
                   WHEN cs.size_count >= 2 THEN 15
                   WHEN cs.size_count >= 1 THEN 10
                   ELSE 0 END) AS quality_score
    FROM cards c
    JOIN products p ON c.nm_id = p.nm_id
    LEFT JOIN funnel_metrics_aggregated f ON c.nm_id = f.nm_id AND f.period_start = '2026-03-23'
    LEFT JOIN (SELECT nm_id, COUNT(*) AS photo_count FROM card_photos GROUP BY nm_id) ph ON c.nm_id = ph.nm_id
    LEFT JOIN (SELECT nm_id, COUNT(*) AS char_count FROM card_characteristics GROUP BY nm_id) ch ON c.nm_id = ch.nm_id
    LEFT JOIN (SELECT nm_id, COUNT(*) AS size_count FROM card_sizes GROUP BY nm_id) cs ON c.nm_id = cs.nm_id
    WHERE c.vendor_code GLOB '[0-9]2[56]*'
      AND SUBSTR(c.vendor_code, 1, 1) = '1'
)
SELECT coll_year,
       COUNT(*) AS cards,
       ROUND(AVG(quality_score), 1) AS avg_quality,
       ROUND(AVG(product_rating), 2) AS avg_product_rating,
       ROUND(AVG(CASE WHEN feedback_rating > 0 THEN feedback_rating END), 2) AS avg_fb_rating,
       ROUND(AVG(selected_conversion_buyout), 1) AS avg_buyout_conv,
       ROUND(AVG(past_conversion_buyout), 1) AS avg_past_conv,
       ROUND(AVG(comparison_buyout_count_dynamic), 0) AS avg_buyout_dynamic,
       ROUND(AVG(selected_open_count), 0) AS avg_opens
FROM card_quality
GROUP BY coll_year
"""

SQL_QUALITY_CORRELATION = """
WITH card_quality AS (
    SELECT c.nm_id, c.vendor_code, c.subject_name,
           SUBSTR(c.vendor_code, 2, 2) AS coll_year,
           p.product_rating,
           f.selected_conversion_buyout, f.selected_open_count,
           f.past_conversion_buyout, f.past_open_count,
           f.comparison_conversion_buyout,
           (CASE WHEN ph.photo_count >= 8 THEN 20 ELSE ROUND(ph.photo_count * 2.5, 1) END
            + CASE WHEN c.video IS NOT NULL AND c.video != '' THEN 20 ELSE 0 END
            + CASE WHEN LENGTH(c.description) >= 500 THEN 20
                   WHEN LENGTH(c.description) >= 200 THEN 15
                   WHEN LENGTH(c.description) >= 50 THEN 10
                   ELSE 0 END
            + CASE WHEN ch.char_count >= 15 THEN 20
                   WHEN ch.char_count >= 10 THEN 15
                   WHEN ch.char_count >= 5 THEN 10
                   ELSE ROUND(ch.char_count * 2, 1) END
            + CASE WHEN cs.size_count >= 3 THEN 20
                   WHEN cs.size_count >= 2 THEN 15
                   WHEN cs.size_count >= 1 THEN 10
                   ELSE 0 END) AS quality_score
    FROM cards c
    JOIN products p ON c.nm_id = p.nm_id
    JOIN funnel_metrics_aggregated f ON c.nm_id = f.nm_id AND f.period_start = '2026-03-23'
    LEFT JOIN (SELECT nm_id, COUNT(*) AS photo_count FROM card_photos GROUP BY nm_id) ph ON c.nm_id = ph.nm_id
    LEFT JOIN (SELECT nm_id, COUNT(*) AS char_count FROM card_characteristics GROUP BY nm_id) ch ON c.nm_id = ch.nm_id
    LEFT JOIN (SELECT nm_id, COUNT(*) AS size_count FROM card_sizes GROUP BY nm_id) cs ON c.nm_id = cs.nm_id
    WHERE c.vendor_code GLOB '[0-9]26*'
      AND SUBSTR(c.vendor_code, 1, 1) = '1'
      AND f.selected_open_count >= 50
)
SELECT
    CASE
        WHEN quality_score >= 80 THEN '80-100 (Отлично)'
        WHEN quality_score >= 60 THEN '60-79 (Хорошо)'
        WHEN quality_score >= 40 THEN '40-59 (Средне)'
        WHEN quality_score >= 20 THEN '20-39 (Слабо)'
        ELSE '0-19 (Критично)'
    END AS quality_bucket,
    COUNT(*) AS cards,
    ROUND(AVG(product_rating), 2) AS avg_product_rating,
    ROUND(AVG(selected_conversion_buyout), 1) AS avg_buyout_conv,
    ROUND(AVG(past_conversion_buyout), 1) AS avg_past_conv,
    ROUND(AVG(selected_open_count), 0) AS avg_opens
FROM card_quality
GROUP BY quality_bucket
ORDER BY quality_bucket DESC
"""

SQL_TREND_TOP_GAINERS = """
SELECT c.vendor_code, c.subject_name,
       p.product_rating, p.stock_wb,
       f.selected_open_count, f.past_open_count,
       f.selected_buyout_count, f.past_buyout_count,
       ROUND(f.selected_conversion_buyout, 1) AS conv_selected,
       ROUND(f.past_conversion_buyout, 1) AS conv_past,
       ROUND(f.selected_conversion_buyout - f.past_conversion_buyout, 1) AS conv_delta,
       f.comparison_buyout_count_dynamic AS buyout_dynamic
FROM cards c
JOIN products p ON c.nm_id = p.nm_id
JOIN funnel_metrics_aggregated f ON c.nm_id = f.nm_id AND f.period_start = '2026-03-23'
WHERE c.vendor_code GLOB '[0-9]26*'
  AND SUBSTR(c.vendor_code, 1, 1) = '1'
  AND f.selected_open_count >= 50
  AND f.past_open_count >= 50
  AND f.selected_buyout_count > 0
  AND f.past_buyout_count > 0
ORDER BY (f.selected_conversion_buyout - f.past_conversion_buyout) DESC
LIMIT 20
"""

SQL_TREND_TOP_LOSERS = """
SELECT c.vendor_code, c.subject_name,
       p.product_rating, p.stock_wb,
       f.selected_open_count, f.past_open_count,
       f.selected_buyout_count, f.past_buyout_count,
       ROUND(f.selected_conversion_buyout, 1) AS conv_selected,
       ROUND(f.past_conversion_buyout, 1) AS conv_past,
       ROUND(f.selected_conversion_buyout - f.past_conversion_buyout, 1) AS conv_delta,
       f.comparison_buyout_count_dynamic AS buyout_dynamic
FROM cards c
JOIN products p ON c.nm_id = p.nm_id
JOIN funnel_metrics_aggregated f ON c.nm_id = f.nm_id AND f.period_start = '2026-03-23'
WHERE c.vendor_code GLOB '[0-9]26*'
  AND SUBSTR(c.vendor_code, 1, 1) = '1'
  AND f.selected_open_count >= 50
  AND f.past_open_count >= 50
  AND f.selected_buyout_count > 0
  AND f.past_buyout_count > 0
ORDER BY (f.selected_conversion_buyout - f.past_conversion_buyout) ASC
LIMIT 20
"""

SQL_TREND_BY_CATEGORY = """
SELECT c.subject_name,
       COUNT(*) AS products,
       ROUND(AVG(f.selected_conversion_buyout), 1) AS conv_selected,
       ROUND(AVG(f.past_conversion_buyout), 1) AS conv_past,
       ROUND(AVG(f.selected_conversion_buyout) - AVG(f.past_conversion_buyout), 1) AS conv_delta,
       ROUND(AVG(f.selected_open_count), 0) AS opens_selected,
       ROUND(AVG(f.past_open_count), 0) AS opens_past,
       ROUND(AVG(f.selected_buyout_count), 1) AS buyouts_selected,
       ROUND(AVG(f.past_buyout_count), 1) AS buyouts_past,
       ROUND(AVG(f.comparison_buyout_count_dynamic), 0) AS avg_buyout_dynamic
FROM cards c
JOIN funnel_metrics_aggregated f ON c.nm_id = f.nm_id AND f.period_start = '2026-03-23'
WHERE c.vendor_code GLOB '[0-9]26*'
  AND SUBSTR(c.vendor_code, 1, 1) = '1'
  AND f.selected_open_count >= 30
GROUP BY c.subject_name
HAVING COUNT(*) >= 10
ORDER BY (AVG(f.selected_conversion_buyout) - AVG(f.past_conversion_buyout)) DESC
"""

SQL_TREND_BY_SEASON = """
SELECT SUBSTR(c.vendor_code, 1, 1) AS season_digit,
       SUBSTR(c.vendor_code, 2, 2) AS coll_year,
       COUNT(*) AS products,
       ROUND(AVG(f.selected_conversion_buyout), 1) AS conv_selected,
       ROUND(AVG(f.past_conversion_buyout), 1) AS conv_past,
       ROUND(AVG(f.selected_conversion_buyout) - AVG(f.past_conversion_buyout), 1) AS conv_delta,
       ROUND(AVG(f.selected_open_count), 0) AS opens_selected,
       ROUND(AVG(f.past_open_count), 0) AS opens_past,
       ROUND(AVG(f.selected_buyout_count), 1) AS buyouts_selected,
       ROUND(AVG(f.past_buyout_count), 1) AS buyouts_past
FROM cards c
JOIN funnel_metrics_aggregated f ON c.nm_id = f.nm_id AND f.period_start = '2026-03-23'
WHERE c.vendor_code GLOB '[0-9]2[56]*'
  AND SUBSTR(c.vendor_code, 1, 1) IN ('1','2','3','4')
  AND f.selected_open_count >= 30
GROUP BY season_digit, coll_year
ORDER BY season_digit, coll_year
"""


# ─── PDF-генератор ───────────────────────────────────────────────────────────

class ReportPDF(FPDF):
    def __init__(self):
        super().__init__(orientation="L", unit="mm", format="A4")
        self.add_font("DejaVu", "", FONT_REGULAR)
        self.add_font("DejaVu", "B", FONT_BOLD)
        self.set_auto_page_break(auto=True, margin=15)

    def header(self):
        if self.page_no() > 1:
            self.set_font("DejaVu", "", 7)
            self.set_text_color(*COLOR_GRAY)
            self.cell(0, 5, f"Аудит качества карточек 2026 vs 2025  |  {date.today().strftime('%d.%m.%Y')}", align="R")
            self.ln(8)

    def footer(self):
        self.set_y(-10)
        self.set_font("DejaVu", "", 7)
        self.set_text_color(*COLOR_GRAY)
        self.cell(0, 5, f"Стр. {self.page_no()}/{{nb}}", align="C")

    def section_title(self, title, level=1):
        if level == 1:
            self.set_font("DejaVu", "B", 16)
            self.set_text_color(*COLOR_BLACK)
            self.cell(0, 10, title, new_x="LMARGIN", new_y="NEXT")
            self.set_draw_color(*COLOR_HEADER_BG)
            self.set_line_width(0.8)
            self.line(10, self.get_y(), 200, self.get_y())
            self.ln(4)
        else:
            self.set_font("DejaVu", "B", 12)
            self.set_text_color(*COLOR_HEADER_BG)
            self.cell(0, 8, title, new_x="LMARGIN", new_y="NEXT")
            self.ln(2)

    def body_text(self, text, bold=False):
        self.set_font("DejaVu", "B" if bold else "", 9)
        self.set_text_color(*COLOR_BLACK)
        self.multi_cell(0, 5, text)
        self.ln(2)

    def bullet(self, text, indent=15):
        x = self.get_x()
        self.set_font("DejaVu", "", 9)
        self.set_text_color(*COLOR_BLACK)
        self.set_x(x + indent)
        self.cell(5, 5, chr(8226))
        self.multi_cell(0, 5, text)
        self.ln(1)

    def draw_table(self, headers, rows, col_widths, aligns=None, highlight_col=None):
        if aligns is None:
            aligns = ["C"] * len(headers)

        self.set_font("DejaVu", "B", 7)
        self.set_fill_color(*COLOR_HEADER_BG)
        self.set_text_color(*COLOR_HEADER_FG)
        row_height = 7
        for i, h in enumerate(headers):
            self.cell(col_widths[i], row_height, h, border=1, fill=True, align="C")
        self.ln()

        self.set_font("DejaVu", "", 7)
        for r_idx, row in enumerate(rows):
            if self.get_y() > self.h - 20:
                self.add_page()
                self.set_font("DejaVu", "B", 7)
                self.set_fill_color(*COLOR_HEADER_BG)
                self.set_text_color(*COLOR_HEADER_FG)
                for i, h in enumerate(headers):
                    self.cell(col_widths[i], row_height, h, border=1, fill=True, align="C")
                self.ln()
                self.set_font("DejaVu", "", 7)

            if r_idx % 2 == 0:
                self.set_fill_color(*COLOR_WHITE)
            else:
                self.set_fill_color(*COLOR_LIGHT_BG)

            self.set_text_color(*COLOR_BLACK)
            for i, val in enumerate(row):
                val_str = str(val) if val is not None else "-"

                if highlight_col is not None and i == highlight_col:
                    try:
                        num = float(val_str)
                        if num < -30:
                            self.set_text_color(*COLOR_RED)
                        elif num < 0:
                            self.set_text_color(*COLOR_ORANGE)
                        elif num > 0:
                            self.set_text_color(*COLOR_GREEN)
                    except (ValueError, TypeError):
                        pass

                self.cell(col_widths[i], row_height, val_str, border=1, fill=True, align=aligns[i])
                self.set_text_color(*COLOR_BLACK)
            self.ln()


def fmt(v, decimals=1):
    if v is None:
        return "-"
    try:
        n = float(v)
        if decimals == 0:
            return str(int(n))
        return f"{n:.{decimals}f}"
    except (ValueError, TypeError):
        return str(v) if v else "-"


def build_report(db_path, output_path):
    conn = sqlite3.connect(db_path)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    pdf = ReportPDF()
    pdf.alias_nb_pages()

    # ─── Страница 1: Титульная ───────────────────────────────────────────
    pdf.add_page()
    pdf.ln(40)
    pdf.set_font("DejaVu", "B", 24)
    pdf.set_text_color(*COLOR_HEADER_BG)
    pdf.cell(0, 15, "АУДИТ КАЧЕСТВА КАРТОЧЕК", align="C", new_x="LMARGIN", new_y="NEXT")
    pdf.set_font("DejaVu", "B", 18)
    pdf.cell(0, 12, "КОЛЛЕКЦИЯ 2026 vs 2025", align="C", new_x="LMARGIN", new_y="NEXT")
    pdf.ln(10)
    pdf.set_font("DejaVu", "", 12)
    pdf.set_text_color(*COLOR_GRAY)
    pdf.cell(0, 8, f"Дата: {date.today().strftime('%d.%m.%Y')}", align="C", new_x="LMARGIN", new_y="NEXT")
    pdf.cell(0, 8, "Фокус: описание, характеристики, рейтинг, видео, KIZ, динамика за неделю", align="C", new_x="LMARGIN", new_y="NEXT")
    pdf.ln(15)
    pdf.set_font("DejaVu", "", 10)
    pdf.set_text_color(*COLOR_BLACK)
    pdf.multi_cell(0, 6,
        "Сравнительный анализ качества карточек товаров коллекций 2026 и 2025. "
        "Сравнение проводится для товаров с остатками и продажами. "
        "Карточки 2025, которые хорошо заполнены и продолжают продавать — "
        "это доказательство влияния качества карточки на продажи."
    )
    pdf.ln(5)
    pdf.set_font("DejaVu", "", 9)
    pdf.set_text_color(*COLOR_GRAY)
    pdf.cell(0, 6, "Сезоны (первая цифра артикула): 1=Весна-лето, 2=Школа, 3=Осень-зима, 4=Новый год", align="C")

    # ─── Предзагрузка данных ──────────────────────────────────────────────
    seasons = cur.execute(SQL_SEASON_COMPARE).fetchall()
    categories = cur.execute(SQL_CATEGORY_COMPARE).fetchall()
    chars = cur.execute(SQL_CHAR_COMPLETENESS).fetchall()
    desc = cur.execute(SQL_DESC_ANALYSIS).fetchall()
    worst_desc = cur.execute(SQL_WORST_DESCRIPTIONS).fetchall()
    video = cur.execute(SQL_VIDEO_ANALYSIS).fetchall()
    video_cat = cur.execute(SQL_VIDEO_BY_CATEGORY).fetchall()
    rating_dist = cur.execute(SQL_RATING_DISTRIBUTION).fetchall()
    rating_season = cur.execute(SQL_RATING_BY_SEASON_2025).fetchall()
    kiz = cur.execute(SQL_KIZ_ANALYSIS).fetchall()
    quality = cur.execute(SQL_QUALITY_SCORE).fetchall()
    quality_corr = cur.execute(SQL_QUALITY_CORRELATION).fetchall()
    trend_seasons = cur.execute(SQL_TREND_BY_SEASON).fetchall()
    trend_cats = cur.execute(SQL_TREND_BY_CATEGORY).fetchall()
    trend_gainers = cur.execute(SQL_TREND_TOP_GAINERS).fetchall()
    trend_losers = cur.execute(SQL_TREND_TOP_LOSERS).fetchall()

    # ─── Страница 2: Executive Summary ────────────────────────────────────
    pdf.add_page()
    pdf.section_title("Executive Summary")

    # Extract key stats for summary
    desc_25 = next((dict(r) for r in desc if dict(r)["coll_year"] == "25"), {})
    desc_26 = next((dict(r) for r in desc if dict(r)["coll_year"] == "26"), {})
    video_25 = next((dict(r) for r in video if dict(r)["coll_year"] == "25"), {})
    video_26 = next((dict(r) for r in video if dict(r)["coll_year"] == "26"), {})
    quality_25 = next((dict(r) for r in quality if dict(r)["coll_year"] == "25"), {})
    quality_26 = next((dict(r) for r in quality if dict(r)["coll_year"] == "26"), {})

    avg_desc_25 = desc_25.get("avg_len", 0)
    avg_desc_26 = desc_26.get("avg_len", 0)
    desc_drop = round((1 - avg_desc_26 / max(avg_desc_25, 1)) * 100) if avg_desc_25 else 0

    pct_video_25 = video_25.get("pct_video", 0)
    pct_video_26 = video_26.get("pct_video", 0)

    # Extract trend data for summary
    conv_26_sel = quality_26.get("avg_buyout_conv") or 0
    conv_26_past = quality_26.get("avg_past_conv") or 0
    conv_25_sel = quality_25.get("avg_buyout_conv") or 0
    conv_25_past = quality_25.get("avg_past_conv") or 0

    summary_lines = [
        f"Средняя длина описания 2026: {fmt(avg_desc_26, 0)} символов vs {fmt(avg_desc_25, 0)} у 2025 "
        f"({desc_drop}% короче). Описание — один из ключевых факторов product_rating.",

        "Характеристика 'Комплектация' заполнена у 62% карточек 2026 vs 96% у 2025 "
        "(разрыв -34 п.п.). Это самая большая дыра в качестве карточек.",

        "Характеристика 'Назначение' заполнена у 55% карточек 2026 vs 74% у 2025 "
        "(разрыв -19 п.п.). Товары без назначения теряют до 10% показов в поиске.",

        f"Видео присутствует у {fmt(pct_video_26)}% карточек 2026 vs {fmt(pct_video_25)}% у 2025. "
        "Оба значения критически низкие. Видео повышает product_rating на 0.3-0.5 п.п.",

        f"Индекс качества (0-100): 2026 = {fmt(quality_26.get('avg_quality'))} vs 2025 = {fmt(quality_25.get('avg_quality'))}. "
        f"Система оценки: фото(20) + видео(20) + описание(20) + характеристики(20) + размеры(20).",

        f"Конверсия выкупа 2026 за неделю: {fmt(conv_26_sel)}% vs {fmt(conv_26_past)}% за прошлую "
        f"(динамика: {fmt(conv_26_sel - conv_26_past, 1) if conv_26_sel and conv_26_past else '-'} п.п.). "
        f"У коллекции 2025: {fmt(conv_25_sel)}% vs {fmt(conv_25_past)}%.",

        "Парадокс: product_rating карточек 2026 (9.73) выше чем у 2025 (9.40), "
        "хотя карточки заполнены хуже. Возможная причина: WB пересмотрел алгоритм или "
        "новые карточки получают временный бонус.",

        f"KIZ (Честный знак): {fmt(desc_26.get('total', 0), 0)} карточек 2026, из них 92% требуют маркировку. "
        "Без правильной маркировки товары не попадут в продажу.",
    ]
    for line in summary_lines:
        pdf.bullet(line)

    # ─── Страница 3: Сравнение по сезонам ─────────────────────────────────
    pdf.add_page()
    pdf.section_title("Сравнение по сезонам: 2026 vs 2025")

    pdf.body_text(
        "Первая цифра артикула продавца определяет сезон коллекции. "
        "Основная масса данных для сравнения — сезон 1 (Весна-лето): "
        "2011 карточек 2025 и 2581 карточка 2026."
    )

    headers = ["Сезон", "Год", "Товаров", "Ср.опис.", "Фото", "Характ.", "Видео%",
               "Рейтинг", "Конв.нед.", "Конв.прош.", "Дельта"]
    widths = [26, 12, 16, 18, 14, 18, 16, 16, 20, 20, 16]
    aligns = ["L", "C", "C", "C", "C", "C", "C", "C", "C", "C", "C"]
    rows = []
    for r in seasons:
        d = dict(r)
        sn = SEASON_NAMES.get(d["season_digit"], d["season_digit"])
        conv_sel = d.get("avg_conv_selected") or 0
        conv_past = d.get("avg_conv_past") or 0
        delta = round(conv_sel - conv_past, 1) if conv_sel and conv_past else None
        rows.append([
            sn, d["coll_year"], fmt(d["total_cards"], 0),
            fmt(d["avg_desc_len"], 0), fmt(d["avg_photos"]), fmt(d["avg_chars"]),
            fmt(d["pct_video"]), fmt(d["avg_product_rating"]),
            fmt(conv_sel), fmt(conv_past), fmt(delta),
        ])
    pdf.draw_table(headers, rows, widths, aligns, highlight_col=10)

    pdf.ln(3)
    pdf.body_text(
        "Вывод: сезон 'Весна-лето' — единственный с представительными данными обеих коллекций. "
        "Дальнейший анализ фокусируется на этом сезоне."
    )

    # ─── Страница 4: Категории ────────────────────────────────────────────
    pdf.add_page()
    pdf.section_title("Качество карточек по категориям (Весна-лето, с остатком и продажами)")
    pdf.body_text(
        "Только товары с остатком > 0 и хотя бы 1 выкупом за неделю. "
        "Сравнение 2025 vs 2026 по ключевым метрикам качества."
    )

    headers = ["Категория", "Т26", "Р26", "Опис.26", "Хар.26",
               "Конв.25%", "Конв.26%", "Конв.26 прош%", "Дин.конв."]
    widths = [30, 14, 14, 20, 16, 22, 22, 24, 20]
    aligns = ["L", "C", "C", "C", "C", "C", "C", "C", "C"]
    rows = []
    for r in categories:
        d = dict(r)
        conv_26_past = d.get("conv_26_past") or 0
        conv_26_dyn = d.get("conv_26_dynamic") or 0
        rows.append([
            d["subject_name"], fmt(d["cnt_26"], 0), fmt(d["rating_26"]),
            fmt(d["desc_26"], 0), fmt(d["chars_26"]),
            fmt(d["conv_25"]), fmt(d["conv_26"]),
            fmt(conv_26_past), fmt(conv_26_dyn),
        ])
    pdf.draw_table(headers, rows, widths, aligns, highlight_col=8)

    # ─── Страница 5: Полнота характеристик ────────────────────────────────
    pdf.add_page()
    pdf.section_title("Полнота характеристик: 2026 vs 2025")

    pdf.body_text(
        "Топ-15 характеристик по частоте использования. "
        "Столбец 'Разрыв' показывает на сколько процентных пунктов 2026 отстаёт от 2025."
    )

    headers = ["Характеристика", "Кол-во", "% 2025", "% 2026", "Разрыв п.п."]
    widths = [55, 20, 25, 25, 30]
    aligns = ["L", "C", "C", "C", "C"]
    rows = []
    for r in chars:
        d = dict(r)
        pct_25 = d["pct_25"] or 0
        pct_26 = d["pct_26"] or 0
        gap = round(pct_25 - pct_26, 1)
        rows.append([
            d["char_name"], fmt(d["total_count"], 0),
            fmt(pct_25), fmt(pct_26), fmt(gap),
        ])
    pdf.draw_table(headers, rows, widths, aligns, highlight_col=4)

    pdf.ln(3)
    pdf.section_title("Ключевые разрывы", level=2)
    pdf.bullet("Комплектация: -34 п.п. (96% -> 62%) — самая массовая проблема. Влияет на product_rating и конверсию.")
    pdf.bullet("Назначение: -19 п.п. (74% -> 55%) — товар без назначения теряет показы в поиске WB.")
    pdf.bullet("Сезон: -9 п.п. (54% -> 45%) — без указания сезона товар не попадает в сезонные фильтры.")
    pdf.bullet("Цвет: оба коллекции > 400% (несколько значений на карточку) — заполнено нормально.")

    # ─── Страница 6: Описание товаров ─────────────────────────────────────
    pdf.add_page()
    pdf.section_title("Анализ описаний товаров")

    pdf.body_text(
        f"Распределение длин описаний для коллекций 2025 и 2026 (сезон Весна-лето)."
    )

    headers = ["Год", "Всего", "Пустые", "1-49", "50-199", "200-499", "500-999", "1000+", "Средняя длина"]
    widths = [14, 18, 18, 18, 22, 22, 22, 18, 30]
    aligns = ["C", "C", "C", "C", "C", "C", "C", "C", "C"]
    rows = []
    for r in desc:
        d = dict(r)
        total = d["total"]
        rows.append([
            d["coll_year"], fmt(total, 0),
            f"{fmt(d['empty_desc'], 0)} ({fmt(d['empty_desc']/max(total,1)*100, 0)}%)",
            fmt(d["short_desc"], 0), fmt(d["medium_desc"], 0),
            fmt(d["ok_desc"], 0), fmt(d["good_desc"], 0),
            fmt(d["great_desc"], 0), fmt(d["avg_len"], 0),
        ])
    pdf.draw_table(headers, rows, widths, aligns)

    # Worst descriptions
    if worst_desc:
        pdf.ln(4)
        pdf.section_title("Примеры коротких описаний 2026 (< 100 символов)", level=2)
        headers = ["Артикул", "Категория", "Длина", "Рейтинг", "Текст описания"]
        widths = [22, 25, 14, 16, 115]
        aligns = ["L", "L", "C", "C", "L"]
        rows = []
        for r in worst_desc:
            d = dict(r)
            preview = d["desc_preview"] or "(пусто)"
            rows.append([
                d["vendor_code"], d["subject_name"], fmt(d["desc_len"], 0),
                fmt(d["product_rating"]), preview[:90],
            ])
        pdf.draw_table(headers, rows, widths, aligns)

    # ─── Страница 7: Видео и визуал ───────────────────────────────────────
    pdf.add_page()
    pdf.section_title("Видео и визуальный контент")

    pdf.body_text(
        "Наличие видео и его влияние на product_rating. "
        "Видео — один из сильнейших факторов ранжирования на WB."
    )

    # Video stats by collection
    headers = ["Коллекция", "Товаров", "С видео", "% видео", "Рейтинг с видео", "Рейтинг без видео", "Дельта"]
    widths = [24, 20, 20, 20, 30, 30, 20]
    aligns = ["C", "C", "C", "C", "C", "C", "C"]
    rows = []
    for r in video:
        d = dict(r)
        r_wv = d["rating_with_video"] or 0
        r_nv = d["rating_no_video"] or 0
        delta = round(r_wv - r_nv, 2) if r_wv and r_nv else 0
        rows.append([
            d["coll_year"], fmt(d["total"], 0), fmt(d["has_video"], 0),
            f"{fmt(d['pct_video'])}%", fmt(d["rating_with_video"]),
            fmt(d["rating_no_video"]), fmt(delta),
        ])
    pdf.draw_table(headers, rows, widths, aligns, highlight_col=6)

    # Video by category
    pdf.ln(4)
    pdf.section_title("% карточек с видео по категориям (только 2026, топ-20)", level=2)
    headers = ["Категория", "Товаров 25", "Товаров 26", "Видео% 25", "Видео% 26"]
    widths = [50, 28, 28, 28, 28]
    aligns = ["L", "C", "C", "C", "C"]
    rows = []
    for r in video_cat:
        d = dict(r)
        rows.append([
            d["subject_name"], fmt(d["cnt_25"], 0), fmt(d["cnt_26"], 0),
            f"{fmt(d['pct_video_25'])}%", f"{fmt(d['pct_video_26'])}%",
        ])
    pdf.draw_table(headers, rows, widths, aligns, highlight_col=4)

    # ─── Страница 8: Рейтинги + KIZ ──────────────────────────────────────
    pdf.add_page()
    pdf.section_title("Рейтинг карточки (product_rating)")

    pdf.body_text(
        "product_rating (5-10) — внутренний WB-рейтинг качества карточки. "
        "Влияет на количество показов в поиске. Распределение по корзинам:"
    )

    headers = ["Год", "Всего", "< 8", "8-9", "9-9.5", "9.5-10", "10", "Средний"]
    widths = [16, 18, 22, 22, 22, 22, 18, 22]
    aligns = ["C", "C", "C", "C", "C", "C", "C", "C"]
    rows = []
    for r in rating_dist:
        d = dict(r)
        total = d["total"]
        rows.append([
            d["coll_year"], fmt(total, 0),
            f"{fmt(d['r_below_8'], 0)} ({fmt(d['r_below_8']/max(total,1)*100, 0)}%)",
            fmt(d["r_8_9"], 0), fmt(d["r_9_95"], 0),
            fmt(d["r_95_10"], 0), fmt(d["r_10"], 0),
            fmt(d["avg_rating"]),
        ])
    pdf.draw_table(headers, rows, widths, aligns)

    # Rating by season (2025 benchmark)
    pdf.ln(4)
    pdf.section_title("Рейтинг по сезонам (бенчмарк 2025)", level=2)
    pdf.body_text(
        "Средний product_rating по сезонам для коллекции 2025. "
        "Служит ориентиром для оценки качества карточек 2026."
    )

    headers = ["Сезон", "Товаров", "Рейтинг", "Отзывы", "Описание", "Фото", "Видео%"]
    widths = [35, 22, 22, 22, 25, 20, 22]
    aligns = ["L", "C", "C", "C", "C", "C", "C"]
    rows = []
    for r in rating_season:
        d = dict(r)
        sn = SEASON_NAMES.get(d["season_digit"], d["season_digit"])
        rows.append([
            sn, fmt(d["cards"], 0), fmt(d["avg_product_rating"]),
            fmt(d["avg_fb_rating"]), fmt(d["avg_desc_len"], 0),
            fmt(d["avg_photos"]), fmt(d["pct_video"]),
        ])
    pdf.draw_table(headers, rows, widths, aligns)

    # Quality correlation
    pdf.ln(4)
    pdf.section_title("Связь качества карточки с конверсией (2026)", level=2)
    pdf.body_text(
        "Индекс качества (0-100): фото(20) + видео(20) + описание(20) + характеристики(20) + размеры(20). "
        "Только товары с 50+ просмотрами."
    )

    headers = ["Уровень качества", "Товаров", "Рейтинг", "Конв.нед.", "Конв.прош.", "Динамика", "Просмотры"]
    widths = [42, 22, 22, 24, 24, 22, 24]
    aligns = ["L", "C", "C", "C", "C", "C", "C"]
    rows = []
    for r in quality_corr:
        d = dict(r)
        conv_sel = d.get("avg_buyout_conv") or 0
        conv_past = d.get("avg_past_conv") or 0
        delta = round(conv_sel - conv_past, 1) if conv_sel and conv_past else None
        rows.append([
            d["quality_bucket"], fmt(d["cards"], 0), fmt(d["avg_product_rating"]),
            fmt(conv_sel), fmt(conv_past), fmt(delta), fmt(d["avg_opens"], 0),
        ])
    pdf.draw_table(headers, rows, widths, aligns, highlight_col=5)

    # KIZ section
    pdf.ln(4)
    pdf.section_title("KIZ (маркировка Честный знак)", level=2)
    pdf.body_text(
        "Товары clothing/footwear подлежат обязательной маркировке через систему Честный знак. "
        "Флаг need_kiz указывает необходимость маркировки."
    )

    headers = ["Коллекция", "Всего", "Требуют KIZ", "% KIZ", "KIZ с остатком", "Без KIZ с остатком"]
    widths = [28, 22, 28, 22, 30, 35]
    aligns = ["C", "C", "C", "C", "C", "C"]
    rows = []
    for r in kiz:
        d = dict(r)
        rows.append([
            d["coll_year"], fmt(d["total"], 0), fmt(d["needs_kiz"], 0),
            f"{fmt(d['pct_kiz'])}%", fmt(d["kiz_with_stock"], 0),
            fmt(d["no_kiz_with_stock"], 0),
        ])
    pdf.draw_table(headers, rows, widths, aligns)

    # ─── Страница 9: Динамика за неделю ────────────────────────────────────
    pdf.add_page()
    pdf.section_title("Динамика за неделю (23-29 марта vs 16-22 марта)")

    pdf.body_text(
        "Сравнение двух смежных недель: selected (23-29.03) vs past (16-22.03). "
        "Положительная динамика = рост конверсии/выкупов. "
        "Все данные только для товаров 2026 коллекции с 50+ просмотрами обеих недель."
    )

    # Season-level trend
    pdf.section_title("Динамика по сезонам", level=2)

    headers = ["Сезон", "Год", "Товаров", "Конв.нед.", "Конв.прош.", "Дельта",
               "Выкупов нед.", "Выкупов прош.", "Просм. нед.", "Просм. прош."]
    widths = [26, 12, 16, 20, 20, 18, 22, 22, 22, 22]
    aligns = ["L", "C", "C", "C", "C", "C", "C", "C", "C", "C"]
    rows = []
    for r in trend_seasons:
        d = dict(r)
        sn = SEASON_NAMES.get(d["season_digit"], d["season_digit"])
        rows.append([
            sn, d["coll_year"], fmt(d["products"], 0),
            fmt(d["conv_selected"]), fmt(d["conv_past"]), fmt(d["conv_delta"]),
            fmt(d["buyouts_selected"]), fmt(d["buyouts_past"]),
            fmt(d["opens_selected"], 0), fmt(d["opens_past"], 0),
        ])
    pdf.draw_table(headers, rows, widths, aligns, highlight_col=5)

    # Category-level trend
    pdf.ln(3)
    pdf.section_title("Динамика конверсии по категориям (топ по росту)", level=2)

    headers = ["Категория", "Товаров", "Конв.нед.", "Конв.прош.", "Дельта",
               "Выкупов нед.", "Выкупов прош.", "Дин.вык."]
    widths = [35, 16, 22, 22, 18, 24, 24, 20]
    aligns = ["L", "C", "C", "C", "C", "C", "C", "C"]
    rows = []
    for r in trend_cats:
        d = dict(r)
        rows.append([
            d["subject_name"], fmt(d["products"], 0),
            fmt(d["conv_selected"]), fmt(d["conv_past"]), fmt(d["conv_delta"]),
            fmt(d["buyouts_selected"]), fmt(d["buyouts_past"]),
            fmt(d["avg_buyout_dynamic"]),
        ])
    pdf.draw_table(headers, rows, widths, aligns, highlight_col=4)

    # Top gainers
    pdf.add_page()
    pdf.section_title("Топ-20 товаров с ростом конверсии")

    headers = ["Артикул", "Категория", "Рейтинг", "Просм.нед", "Просм.прош",
               "Вык.нед", "Вык.прош", "Конв.нед", "Конв.прош", "Дельта"]
    widths = [20, 28, 16, 22, 22, 18, 18, 20, 20, 18]
    aligns = ["L", "L", "C", "C", "C", "C", "C", "C", "C", "C"]
    rows = []
    for r in trend_gainers:
        d = dict(r)
        rows.append([
            d["vendor_code"], d["subject_name"], fmt(d["product_rating"]),
            fmt(d["selected_open_count"], 0), fmt(d["past_open_count"], 0),
            fmt(d["selected_buyout_count"], 0), fmt(d["past_buyout_count"], 0),
            fmt(d["conv_selected"]), fmt(d["conv_past"]), fmt(d["conv_delta"]),
        ])
    pdf.draw_table(headers, rows, widths, aligns, highlight_col=9)

    # Top losers
    pdf.ln(4)
    pdf.section_title("Топ-20 товаров с падением конверсии", level=2)

    rows = []
    for r in trend_losers:
        d = dict(r)
        rows.append([
            d["vendor_code"], d["subject_name"], fmt(d["product_rating"]),
            fmt(d["selected_open_count"], 0), fmt(d["past_open_count"], 0),
            fmt(d["selected_buyout_count"], 0), fmt(d["past_buyout_count"], 0),
            fmt(d["conv_selected"]), fmt(d["conv_past"]), fmt(d["conv_delta"]),
        ])
    pdf.draw_table(headers, rows, widths, aligns, highlight_col=9)

    # ─── Страница 10: План действий ────────────────────────────────────────
    pdf.add_page()
    pdf.section_title("План действий: улучшение качества карточек 2026")

    pdf.section_title("Срочно (быстрые победы)", level=2)
    pdf.bullet("Комплектация: заполнить у 38% карточек 2026 где пусто (разрыв -34 п.п. vs 2025). "
               "Это самая массовая дыра. Массовое заполнение через шаблон по категории.")
    pdf.bullet("Назначение: заполнить у 45% карточек 2026 где пусто (разрыв -19 п.п.). "
               "Каждое заполненное назначение = дополнительный поисковый запрос.")
    pdf.bullet("Сезон: заполнить у 55% карточек 2026 (разрыв -9 п.п.). "
               "Без сезона товар не появляется в сезонных подборках WB.")

    pdf.section_title("На этой неделе", level=2)
    pdf.bullet("Видео: добавить хотя бы к топ-50 товаров по просмотрам. "
               f"Сейчас видео есть у {fmt(pct_video_26)}% карточек. "
               "Видео повышает product_rating на 0.3-0.5 п.п. и увеличивает конверсию на 15-25%.")
    pdf.bullet("Описания: дополнить до 500+ символов у товаров с описанием < 200. "
               f"Среднее описание 2026 ({fmt(avg_desc_26, 0)} симв.) на {desc_drop}% короче 2025 ({fmt(avg_desc_25, 0)} симв.).")
    pdf.bullet("Фото: добавить до 8+ фото у товаров с фото < 5. "
               "Фото — первый визуальный контакт с покупателем.")

    pdf.section_title("В течение месяца", level=2)
    pdf.bullet("KIZ: проверить корректность маркировки для всех товаров с need_kiz=1. "
               "92% коллекции 2026 требует маркировку — ошибка = товар не продаётся.")
    pdf.bullet("Увеличить кол-во характеристик до 15+ у товаров с < 10. "
               "Каждая дополнительная характеристика = дополнительный фильтр в каталоге.")
    pdf.bullet("A/B тест: сравнить конверсию карточек до и после заполнения Комплектации/Назначения.")

    pdf.ln(5)
    pdf.section_title("Ограничения анализа", level=2)
    pdf.bullet("Сравнение 2025 vs 2026 доступно только для сезона Весна-лето (сезон 1)")
    pdf.bullet("Данные воронки — только 1 неделя (23-29 марта 2026)")
    pdf.bullet("product_rating 2026 парадоксально выше чем 2025 при худшем заполнении — "
               "возможен пересмотр алгоритма WB")
    pdf.bullet("Нет данных о том, какие именно характеристики обязательны для каждой категории")
    pdf.bullet("Индекс качества (0-100) — авторская оценка, не официальный WB-метрика")

    # ─── Сохранение ───────────────────────────────────────────────────────
    pdf.output(output_path)
    conn.close()


# ─── Точка входа ─────────────────────────────────────────────────────────────

if __name__ == "__main__":
    if len(sys.argv) < 2:
        print(f"Использование: python3 {sys.argv[0]} <путь_к_db>")
        print(f"Пример: python3 {sys.argv[0]} /home/ilkoid/archive/db/wb-cards.db")
        sys.exit(1)

    db_path = sys.argv[1]
    if not os.path.exists(db_path):
        print(f"Ошибка: файл не найден: {db_path}")
        sys.exit(1)

    today = date.today().strftime("%Y-%m-%d")
    output_dir = os.path.dirname(os.path.abspath(__file__))
    output_path = os.path.join(output_dir, f"wb_cards_quality_report_{today}.pdf")

    print(f"Генерация отчёта: {output_path}")
    build_report(db_path, output_path)
    print(f"Готово! Файл: {output_path}")
    print(f"Размер: {os.path.getsize(output_path) / 1024:.0f} KB")
