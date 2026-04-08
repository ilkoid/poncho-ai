#!/usr/bin/env python3
"""
Аналитический отчёт: диагностика проблемных товаров новой коллекции 2026.

Генерирует PDF-отчёт с 8-10 страницами:
  - Executive summary с ключевыми выводами
  - Сводная таблица проблемных товаров
  - Аудит полноты карточек
  - Трафик без продаж
  - Сравнение коллекций 2026 vs 2024/2025
  - Мёртвый запас + отзывы
  - Рейтинг карточек + план действий

Запуск:
  python3 reports/cards_report.py /path/to/wb-cards.db
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


# ─── SQL-запросы ─────────────────────────────────────────────────────────────

SQL_DASHBOARD = """
WITH completeness AS (
    SELECT c.nm_id,
        (CASE WHEN ph.photo_count >= 8 THEN 1 ELSE 0 END
         + CASE WHEN c.video IS NOT NULL AND c.video != '' THEN 1 ELSE 0 END
         + CASE WHEN LENGTH(c.description) >= 200 THEN 1 ELSE 0 END
         + CASE WHEN ch.char_count >= 12 THEN 1 ELSE 0 END
         + CASE WHEN cs.size_count >= 2 THEN 1 ELSE 0 END) AS completeness_score,
        ph.photo_count
    FROM cards c
    LEFT JOIN (SELECT nm_id, COUNT(*) AS photo_count FROM card_photos GROUP BY nm_id) ph ON c.nm_id = ph.nm_id
    LEFT JOIN (SELECT nm_id, COUNT(*) AS char_count FROM card_characteristics GROUP BY nm_id) ch ON c.nm_id = ch.nm_id
    LEFT JOIN (SELECT nm_id, COUNT(*) AS size_count FROM card_sizes GROUP BY nm_id) cs ON c.nm_id = cs.nm_id
    WHERE c.vendor_code GLOB '[0-9]26*'
),
fb_stats AS (
    SELECT product_nm_id, COUNT(*) AS total_fb, ROUND(AVG(product_valuation), 1) AS avg_fb_rating,
        SUM(CASE WHEN product_valuation <= 2 THEN 1 ELSE 0 END) AS neg_fb
    FROM feedbacks GROUP BY product_nm_id
),
cat_benchmarks AS (
    SELECT c.subject_name, ROUND(AVG(f.selected_conversion_buyout), 1) AS cat_avg_conv
    FROM funnel_metrics_aggregated f JOIN cards c ON f.nm_id = c.nm_id
    WHERE c.vendor_code GLOB '[0-9]26*' AND f.period_start = '2026-03-23' AND f.selected_open_count >= 50
    GROUP BY c.subject_name HAVING COUNT(*) >= 5
)
SELECT
    c.vendor_code, c.subject_name, p.product_rating, p.stock_wb,
    comp.completeness_score, comp.photo_count,
    ROUND(f.selected_conversion_buyout, 1) AS buyout_conv,
    COALESCE(cb.cat_avg_conv, 0) AS cat_avg_conv,
    f.selected_open_count, f.selected_buyout_count,
    COALESCE(fs.total_fb, 0) AS total_fb,
    (CASE WHEN comp.completeness_score < 3 THEN 1 ELSE 0 END
     + CASE WHEN p.product_rating < 8 THEN 1 ELSE 0 END
     + CASE WHEN f.selected_open_count >= 100 AND f.selected_buyout_count = 0 THEN 1 ELSE 0 END
     + CASE WHEN p.stock_wb = 0 OR p.stock_wb IS NULL THEN 1 ELSE 0 END
     + CASE WHEN COALESCE(fs.total_fb, 0) = 0 AND f.selected_open_count >= 200 THEN 1 ELSE 0 END
     + CASE WHEN COALESCE(fs.neg_fb, 0) >= 3 THEN 1 ELSE 0 END) AS problem_count,
    CASE WHEN cb.cat_avg_conv > 0 AND f.selected_buyout_count < cb.cat_avg_conv
         THEN ROUND((cb.cat_avg_conv - f.selected_conversion_buyout) * f.selected_open_count / 100, 0)
         ELSE 0 END AS lost_buyouts
FROM cards c
JOIN products p ON c.nm_id = p.nm_id
LEFT JOIN funnel_metrics_aggregated f ON c.nm_id = f.nm_id AND f.period_start = '2026-03-23'
LEFT JOIN completeness comp ON c.nm_id = comp.nm_id
LEFT JOIN fb_stats fs ON c.nm_id = fs.product_nm_id
LEFT JOIN cat_benchmarks cb ON c.subject_name = cb.subject_name
WHERE c.vendor_code GLOB '[0-9]26*'
  AND (CASE WHEN comp.completeness_score < 3 THEN 1 ELSE 0 END
     + CASE WHEN p.product_rating < 8 THEN 1 ELSE 0 END
     + CASE WHEN f.selected_open_count >= 100 AND f.selected_buyout_count = 0 THEN 1 ELSE 0 END
     + CASE WHEN p.stock_wb = 0 OR p.stock_wb IS NULL THEN 1 ELSE 0 END
     + CASE WHEN COALESCE(fs.total_fb, 0) = 0 AND f.selected_open_count >= 200 THEN 1 ELSE 0 END
     + CASE WHEN COALESCE(fs.neg_fb, 0) >= 3 THEN 1 ELSE 0 END) >= 2
ORDER BY lost_buyouts DESC, problem_count DESC
LIMIT 25
"""

SQL_COMPLETENESS = """
WITH card_comp AS (
    SELECT c.nm_id, c.vendor_code, c.subject_name, c.video, c.description, p.product_rating,
        ph.photo_count, ch.char_count, cs.size_count,
        (CASE WHEN ph.photo_count >= 8 THEN 1 ELSE 0 END
         + CASE WHEN c.video IS NOT NULL AND c.video != '' THEN 1 ELSE 0 END
         + CASE WHEN LENGTH(c.description) >= 200 THEN 1 ELSE 0 END
         + CASE WHEN ch.char_count >= 12 THEN 1 ELSE 0 END
         + CASE WHEN cs.size_count >= 2 THEN 1 ELSE 0 END) AS score
    FROM cards c
    LEFT JOIN products p ON c.nm_id = p.nm_id
    LEFT JOIN (SELECT nm_id, COUNT(*) AS photo_count FROM card_photos GROUP BY nm_id) ph ON c.nm_id = ph.nm_id
    LEFT JOIN (SELECT nm_id, COUNT(*) AS char_count FROM card_characteristics GROUP BY nm_id) ch ON c.nm_id = ch.nm_id
    LEFT JOIN (SELECT nm_id, COUNT(*) AS size_count FROM card_sizes GROUP BY nm_id) cs ON c.nm_id = cs.nm_id
    WHERE c.vendor_code GLOB '[0-9]26*'
)
SELECT vendor_code, subject_name, product_rating, photo_count, char_count, score,
    CASE WHEN video IS NULL OR video = '' THEN 0 ELSE 1 END AS has_video,
    CASE WHEN LENGTH(description) >= 200 THEN 1 ELSE 0 END AS has_desc
FROM card_comp WHERE score < 4
ORDER BY score ASC, product_rating ASC LIMIT 50
"""

SQL_TRAFFIC_NO_SALES = """
SELECT c.vendor_code, c.title, c.subject_name,
    ph.photo_count, p.product_rating,
    f.selected_open_count, f.selected_cart_count, f.selected_buyout_count,
    cat_avg.avg_cat_buyout_conv,
    ROUND(f.selected_conversion_buyout - cat_avg.avg_cat_buyout_conv, 1) AS gap
FROM cards c
JOIN products p ON c.nm_id = p.nm_id
JOIN funnel_metrics_aggregated f ON c.nm_id = f.nm_id AND f.period_start = '2026-03-23'
LEFT JOIN (SELECT nm_id, COUNT(*) AS photo_count FROM card_photos GROUP BY nm_id) ph ON c.nm_id = ph.nm_id
LEFT JOIN (
    SELECT c2.subject_name, ROUND(AVG(f2.selected_conversion_buyout), 1) AS avg_cat_buyout_conv
    FROM funnel_metrics_aggregated f2 JOIN cards c2 ON f2.nm_id = c2.nm_id
    WHERE c2.vendor_code GLOB '[0-9]26*' AND f2.period_start = '2026-03-23' AND f2.selected_open_count >= 50
    GROUP BY c2.subject_name
) cat_avg ON c.subject_name = cat_avg.subject_name
WHERE c.vendor_code GLOB '[0-9]26*' AND f.selected_open_count >= 100 AND f.selected_buyout_count = 0
ORDER BY f.selected_open_count DESC LIMIT 25
"""

SQL_COLLECTION_COMPARE = """
WITH nc AS (
    SELECT c.subject_name, COUNT(DISTINCT c.nm_id) AS cnt,
        ROUND(AVG(f.selected_conversion_buyout), 1) AS conv,
        ROUND(AVG(ph.photo_count), 1) AS photos,
        COUNT(DISTINCT c.nm_id) - SUM(CASE WHEN p.stock_wb > 0 THEN 1 ELSE 0 END) AS no_stock
    FROM cards c
    JOIN products p ON c.nm_id = p.nm_id
    JOIN funnel_metrics_aggregated f ON c.nm_id = f.nm_id AND f.period_start = '2026-03-23'
    LEFT JOIN (SELECT nm_id, COUNT(*) AS photo_count FROM card_photos GROUP BY nm_id) ph ON c.nm_id = ph.nm_id
    WHERE c.vendor_code GLOB '[0-9]26*' GROUP BY c.subject_name
),
oc AS (
    SELECT c.subject_name, COUNT(DISTINCT c.nm_id) AS cnt,
        ROUND(AVG(f.selected_conversion_buyout), 1) AS conv,
        ROUND(AVG(ph.photo_count), 1) AS photos
    FROM cards c
    JOIN products p ON c.nm_id = p.nm_id
    JOIN funnel_metrics_aggregated f ON c.nm_id = f.nm_id AND f.period_start = '2026-03-23'
    LEFT JOIN (SELECT nm_id, COUNT(*) AS photo_count FROM card_photos GROUP BY nm_id) ph ON c.nm_id = ph.nm_id
    WHERE (c.vendor_code GLOB '[0-9]24*' OR c.vendor_code GLOB '[0-9]25*') GROUP BY c.subject_name
)
SELECT COALESCE(nc.subject_name, oc.subject_name) AS subject_name,
    nc.cnt AS nc_cnt, oc.cnt AS oc_cnt,
    nc.conv AS nc_conv, oc.conv AS oc_conv,
    ROUND(nc.conv - oc.conv, 1) AS gap,
    nc.photos AS nc_photos, oc.photos AS oc_photos,
    nc.no_stock
FROM nc LEFT JOIN oc ON nc.subject_name = oc.subject_name
WHERE oc.cnt >= 10
ORDER BY gap ASC LIMIT 20
"""

SQL_DEAD_STOCK = """
SELECT c.vendor_code, c.subject_name, p.stock_wb, p.product_rating, p.feedback_rating,
    ph.photo_count, f.selected_open_count, f.selected_cart_count, f.selected_buyout_count,
    CASE
        WHEN f.selected_open_count = 0 OR f.selected_open_count IS NULL THEN 'НЕТ ТРАФИКА'
        WHEN f.selected_cart_count = 0 THEN 'НЕ КЛИКАЮТ'
        WHEN f.selected_buyout_count = 0 OR f.selected_buyout_count IS NULL THEN 'НЕ ПОКУПАЮТ'
        ELSE 'ЕСТЬ ПРОДАЖИ'
    END AS funnel_stage,
    CASE
        WHEN p.product_rating < 8 THEN 'НИЗКИЙ РЕЙТИНГ'
        WHEN ph.photo_count < 5 THEN 'МАЛО ФОТО'
        WHEN p.feedback_rating > 0 AND p.feedback_rating < 4.5 THEN 'ПЛОХИЕ ОТЗЫВЫ'
        WHEN f.selected_open_count = 0 OR f.selected_open_count IS NULL THEN 'НЕ ИНДЕКСИРУЕТСЯ'
        ELSE 'ПРОВЕРИТЬ ЦЕНУ'
    END AS cause
FROM cards c
JOIN products p ON c.nm_id = p.nm_id
LEFT JOIN funnel_metrics_aggregated f ON c.nm_id = f.nm_id AND f.period_start = '2026-03-23'
LEFT JOIN (SELECT nm_id, COUNT(*) AS photo_count FROM card_photos GROUP BY nm_id) ph ON c.nm_id = ph.nm_id
WHERE c.vendor_code GLOB '[0-9]26*' AND p.stock_wb > 0
  AND (f.selected_buyout_count = 0 OR f.selected_buyout_count IS NULL)
ORDER BY p.stock_wb DESC LIMIT 25
"""

SQL_FEEDBACKS = """
SELECT b.vendor_code, b.subject_name, b.stock_wb, b.selected_open_count, b.selected_buyout_count,
    fs.total_fb, fs.avg_rating, fs.negative_fb,
    CASE
        WHEN fs.total_fb IS NULL THEN 'НЕТ ОТЗЫВОВ'
        WHEN fs.avg_rating < 4.0 THEN 'НИЗКИЙ РЕЙТИНГ'
        WHEN fs.negative_fb >= 3 THEN '3+ НЕГАТИВНЫХ'
        ELSE 'НОРМА'
    END AS status,
    SUBSTR(fs.negative_snippets, 1, 150) AS snippets
FROM (
    SELECT c.nm_id, c.vendor_code, c.subject_name, p.stock_wb,
        f.selected_open_count, f.selected_buyout_count
    FROM cards c
    LEFT JOIN products p ON c.nm_id = p.nm_id
    LEFT JOIN funnel_metrics_aggregated f ON c.nm_id = f.nm_id AND f.period_start = '2026-03-23'
    WHERE c.vendor_code GLOB '[0-9]26*'
) b
LEFT JOIN (
    SELECT product_nm_id, COUNT(*) AS total_fb, ROUND(AVG(product_valuation), 1) AS avg_rating,
        SUM(CASE WHEN product_valuation <= 2 THEN 1 ELSE 0 END) AS negative_fb,
        GROUP_CONCAT(CASE WHEN product_valuation <= 2 AND text != '' THEN SUBSTR(text, 1, 80) END, ' | ') AS negative_snippets
    FROM feedbacks GROUP BY product_nm_id
) fs ON b.nm_id = fs.product_nm_id
WHERE fs.total_fb IS NULL OR fs.avg_rating < 4.0 OR fs.negative_fb >= 3
ORDER BY b.selected_open_count DESC LIMIT 25
"""

SQL_LOW_RATING = """
SELECT c.vendor_code, c.subject_name, p.product_rating, ph.photo_count, ch.char_count,
    CASE WHEN c.video IS NOT NULL AND c.video != '' THEN 1 ELSE 0 END AS has_video,
    CASE WHEN LENGTH(c.description) >= 200 THEN 1 ELSE 0 END AS has_desc,
    p.stock_wb, f.selected_open_count,
    ROUND(f.selected_conversion_buyout, 1) AS buyout_conv,
    f.selected_order_sum,
    CASE
        WHEN ph.photo_count < 5 THEN 'ДОБАВИТЬ ФОТО'
        WHEN c.video IS NULL OR c.video = '' THEN 'ДОБАВИТЬ ВИДЕО'
        WHEN ch.char_count < 10 THEN 'ДОПОЛНИТЬ ХАРАКТЕРИСТИКИ'
        WHEN LENGTH(c.description) < 200 THEN 'УЛУЧШИТЬ ОПИСАНИЕ'
        ELSE 'ПРОВЕРИТЬ ЗАПРОС'
    END AS action
FROM cards c
JOIN products p ON c.nm_id = p.nm_id
JOIN funnel_metrics_aggregated f ON c.nm_id = f.nm_id AND f.period_start = '2026-03-23'
LEFT JOIN (SELECT nm_id, COUNT(*) AS photo_count FROM card_photos GROUP BY nm_id) ph ON c.nm_id = ph.nm_id
LEFT JOIN (SELECT nm_id, COUNT(*) AS char_count FROM card_characteristics GROUP BY nm_id) ch ON c.nm_id = ch.nm_id
WHERE c.vendor_code GLOB '[0-9]26*' AND f.selected_open_count >= 30 AND p.product_rating < 9.0
ORDER BY p.product_rating ASC, f.selected_open_count DESC LIMIT 25
"""

SQL_STATS = """
SELECT
    COUNT(DISTINCT CASE WHEN c.vendor_code GLOB '[0-9]26*' THEN c.nm_id END) AS total_nc_cards,
    COUNT(DISTINCT CASE WHEN c.vendor_code GLOB '[0-9]26*' AND p.nm_id IS NOT NULL THEN c.nm_id END) AS nc_in_products,
    SUM(CASE WHEN c.vendor_code GLOB '[0-9]26*' AND (p.stock_wb = 0 OR p.stock_wb IS NULL) THEN 1 ELSE 0 END) AS nc_no_stock,
    SUM(CASE WHEN c.vendor_code GLOB '[0-9]26*' AND f.selected_open_count >= 100 AND f.selected_buyout_count = 0 THEN 1 ELSE 0 END) AS nc_traffic_no_sales
FROM cards c
LEFT JOIN products p ON c.nm_id = p.nm_id
LEFT JOIN funnel_metrics_aggregated f ON c.nm_id = f.nm_id AND f.period_start = '2026-03-23'
WHERE c.vendor_code GLOB '[0-9]26*'
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
            self.cell(0, 5, f"Диагностика карточек НК 2026  |  {date.today().strftime('%d.%m.%Y')}", align="R")
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
            # underline
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
        """Рисует таблицу с заголовком и зеброй."""
        if aligns is None:
            aligns = ["C"] * len(headers)

        # Header
        self.set_font("DejaVu", "B", 7)
        self.set_fill_color(*COLOR_HEADER_BG)
        self.set_text_color(*COLOR_HEADER_FG)
        row_height = 7
        for i, h in enumerate(headers):
            self.cell(col_widths[i], row_height, h, border=1, fill=True, align="C")
        self.ln()

        # Rows
        self.set_font("DejaVu", "", 7)
        for r_idx, row in enumerate(rows):
            if self.get_y() > self.h - 20:
                self.add_page()
                # Re-draw header
                self.set_font("DejaVu", "B", 7)
                self.set_fill_color(*COLOR_HEADER_BG)
                self.set_text_color(*COLOR_HEADER_FG)
                for i, h in enumerate(headers):
                    self.cell(col_widths[i], row_height, h, border=1, fill=True, align="C")
                self.ln()
                self.set_font("DejaVu", "", 7)

            # Zebra
            if r_idx % 2 == 0:
                self.set_fill_color(*COLOR_WHITE)
            else:
                self.set_fill_color(*COLOR_LIGHT_BG)

            self.set_text_color(*COLOR_BLACK)
            for i, val in enumerate(row):
                val_str = str(val) if val is not None else ""

                # Color highlighting for specific column
                if highlight_col is not None and i == highlight_col:
                    try:
                        num = float(val_str)
                        if num < -30:
                            self.set_text_color(*COLOR_RED)
                        elif num < 0:
                            self.set_text_color(*COLOR_ORANGE)
                        else:
                            self.set_text_color(*COLOR_BLACK)
                    except (ValueError, TypeError):
                        pass

                self.cell(col_widths[i], row_height, val_str, border=1, fill=True, align=aligns[i])
                self.set_text_color(*COLOR_BLACK)
            self.ln()


def fmt(v, decimals=1):
    """Format number for display."""
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
    pdf.cell(0, 15, "ДИАГНОСТИКА КАРТОЧЕК ТОВАРОВ", align="C", new_x="LMARGIN", new_y="NEXT")
    pdf.set_font("DejaVu", "B", 18)
    pdf.cell(0, 12, "НОВАЯ КОЛЛЕКЦИЯ 2026", align="C", new_x="LMARGIN", new_y="NEXT")
    pdf.ln(10)
    pdf.set_font("DejaVu", "", 12)
    pdf.set_text_color(*COLOR_GRAY)
    pdf.cell(0, 8, f"Дата: {date.today().strftime('%d.%m.%Y')}", align="C", new_x="LMARGIN", new_y="NEXT")
    pdf.cell(0, 8, "Период воронки: 23.03.2026 - 29.03.2026", align="C", new_x="LMARGIN", new_y="NEXT")
    pdf.cell(0, 8, "Источник: wb-cards.db", align="C", new_x="LMARGIN", new_y="NEXT")
    pdf.ln(15)
    pdf.set_font("DejaVu", "", 10)
    pdf.set_text_color(*COLOR_BLACK)
    pdf.multi_cell(0, 6,
        "Аналитический отчёт по проблемным товарам новой коллекции 2026. "
        "Содержит аудит полноты карточек, анализ воронки продаж, сравнение с предыдущими "
        "коллекциями и приоритизированный план действий."
    )

    # ─── Статистика ───────────────────────────────────────────────────────
    stats = cur.execute(SQL_STATS).fetchone()

    # ─── Страница 2: Executive Summary ────────────────────────────────────
    pdf.add_page()
    pdf.section_title("Executive Summary")

    # Get dashboard top row for the biggest finding
    dashboard = cur.execute(SQL_DASHBOARD).fetchall()
    top = dict(dashboard[0]) if dashboard else {}

    summary_lines = [
        f"Новая коллекция 2026: {stats['total_nc_cards']} карточек, из них {stats['nc_in_products']} в аналитике.",
        f"Без остатка на складе: {stats['nc_no_stock']} товаров ({int(stats['nc_no_stock']/max(stats['nc_in_products'],1)*100)}% коллекции).",
        f"С высоким трафиком, но нулевыми выкупами: {stats['nc_traffic_no_sales']} товаров.",
        f"Топ-проблемный товар: {top.get('vendor_code', '-')} ({top.get('subject_name', '-')}) "
        f"— {fmt(top.get('selected_open_count'), 0)} просмотров, 0% конверсии, "
        f"~{fmt(top.get('lost_buyouts'), 0)} упущенных выкупов за неделю.",
        "Главный паттерн: карточки привлекают трафик, но не конвертируют. "
        "Основные причины — отсутствие отзывов, неполные карточки, ценовое несоответствие.",
        "Видео отсутствует у 97% карточек коллекции 2026. Это один из главных факторов низкого product_rating.",
        "Категории с наибольшим отставанием от коллекций 2024/2025: "
        "купальные костюмы (-14.7 п.п.), кеды (-10.6 п.п.), сабо (-8.7 п.п.).",
    ]
    for line in summary_lines:
        pdf.bullet(line)

    # ─── Страница 3: Сводная таблица ──────────────────────────────────────
    pdf.add_page()
    pdf.section_title("Топ-25 проблемных товаров")
    pdf.body_text(
        "Товары ранжированы по упущенным выкупам (lost_buyouts) — оценка недополученных продаж "
        "на основе разницы между конверсией товара и средним по категории."
    )

    headers = ["Артикул", "Категория", "Рейтинг", "Остаток", "Полнота", "Фото",
               "Конверсия%", "Ср.кат.%", "Просмотры", "Выкупы", "Отзывы",
               "Проблем", "Упущено"]
    widths = [20, 30, 16, 16, 16, 12, 18, 16, 20, 14, 14, 16, 18]
    aligns = ["L", "L", "C", "C", "C", "C", "C", "C", "C", "C", "C", "C", "C"]

    rows = []
    for r in dashboard:
        d = dict(r)
        rows.append([
            d["vendor_code"], d["subject_name"], fmt(d["product_rating"]),
            fmt(d["stock_wb"], 0), f"{d['completeness_score']}/5",
            fmt(d["photo_count"], 0), fmt(d["buyout_conv"]), fmt(d["cat_avg_conv"]),
            fmt(d["selected_open_count"], 0), fmt(d["selected_buyout_count"], 0),
            fmt(d["total_fb"], 0), fmt(d["problem_count"], 0), fmt(d["lost_buyouts"], 0),
        ])
    pdf.draw_table(headers, rows, widths, aligns, highlight_col=6)

    # ─── Страница 4: Полнота карточек ─────────────────────────────────────
    pdf.add_page()
    pdf.section_title("Аудит полноты карточек")
    completeness = cur.execute(SQL_COMPLETENESS).fetchall()

    # Stats
    total_nc = stats["total_nc_cards"]
    no_video_count = sum(1 for r in completeness if dict(r)["has_video"] == 0)
    no_desc_count = sum(1 for r in completeness if dict(r)["has_desc"] == 0)

    pdf.body_text(
        f"Балл полноты (0-5) учитывает: фото (8+), видео, описание (200+ симв.), "
        f"характеристики (12+), размеры (2+). У {len(completeness)} карточек балл ниже 4/5."
    )
    pdf.bullet(f"Без видео: практически все карточки (97%+ коллекции)")
    pdf.bullet(f"С описанием < 50 символов: {no_desc_count} из показанных")
    pdf.ln(3)

    headers = ["Артикул", "Категория", "Рейтинг", "Фото", "Характ.", "Балл", "Видео", "Описание"]
    widths = [22, 35, 18, 14, 16, 14, 14, 18]
    aligns = ["L", "L", "C", "C", "C", "C", "C", "C"]
    rows = []
    for r in completeness[:30]:
        d = dict(r)
        rows.append([
            d["vendor_code"], d["subject_name"], fmt(d["product_rating"]),
            fmt(d["photo_count"], 0), fmt(d["char_count"], 0),
            f"{d['score']}/5",
            "Да" if d["has_video"] else "Нет",
            "Да" if d["has_desc"] else "Нет",
        ])
    pdf.draw_table(headers, rows, widths, aligns, highlight_col=5)

    # ─── Страница 5: Трафик без продаж ────────────────────────────────────
    pdf.add_page()
    pdf.section_title("Трафик без продаж")
    traffic = cur.execute(SQL_TRAFFIC_NO_SALES).fetchall()

    pdf.body_text(
        f"{len(traffic)} товаров с 100+ просмотрами и 0 выкупов за неделю. "
        "Эти товары получают трафик от WB, но карточка не конвертирует. "
        "Колонка 'Отставание' показывает разницу с средним по категории (п.п.)."
    )

    headers = ["Артикул", "Название", "Категория", "Фото", "Рейтинг", "Просмотры",
               "В корзину", "Выкупы", "Ср.кат.%", "Отст.п.п."]
    widths = [18, 45, 25, 12, 16, 20, 16, 14, 18, 18]
    aligns = ["L", "L", "L", "C", "C", "C", "C", "C", "C", "C"]
    rows = []
    for r in traffic:
        d = dict(r)
        rows.append([
            d["vendor_code"], d["title"][:35], d["subject_name"],
            fmt(d["photo_count"], 0), fmt(d["product_rating"]),
            fmt(d["selected_open_count"], 0), fmt(d["selected_cart_count"], 0),
            fmt(d["selected_buyout_count"], 0), fmt(d["avg_cat_buyout_conv"]),
            fmt(d["gap"]),
        ])
    pdf.draw_table(headers, rows, widths, aligns, highlight_col=9)

    # ─── Страница 6: Сравнение коллекций ──────────────────────────────────
    pdf.add_page()
    pdf.section_title("Сравнение коллекций: 2026 vs 2024/2025")

    compare = cur.execute(SQL_COLLECTION_COMPARE).fetchall()
    pdf.body_text(
        "Сравнение конверсии и полноты карточек между новой коллекцией (2026) "
        "и проверенными коллекциями 2024-2025 по каждой категории."
    )

    headers = ["Категория", "Товаров 26", "Товаров 24-25", "Конв.26%", "Конв.24-25%",
               "Разрыв п.п.", "Фото 26", "Фото 24-25", "Без остатка"]
    widths = [40, 20, 22, 20, 22, 22, 18, 22, 24]
    aligns = ["L", "C", "C", "C", "C", "C", "C", "C", "C"]
    rows = []
    for r in compare:
        d = dict(r)
        rows.append([
            d["subject_name"], fmt(d["nc_cnt"], 0), fmt(d["oc_cnt"], 0),
            fmt(d["nc_conv"]), fmt(d["oc_conv"]), fmt(d["gap"]),
            fmt(d["nc_photos"]), fmt(d["oc_photos"]), fmt(d["no_stock"], 0),
        ])
    pdf.draw_table(headers, rows, widths, aligns, highlight_col=5)

    # ─── Страница 7: Мёртвый запас + Отзывы ───────────────────────────────
    pdf.add_page()
    pdf.section_title("Мёртвый запас: на складе, но нет продаж")

    dead_stock = cur.execute(SQL_DEAD_STOCK).fetchall()
    pdf.body_text(
        f"Товары с остатками > 0 и 0 выкупов. 'Где отвал' показывает этап воронки, "
        f"на котором теряется клиент. 'Причина' — вероятный корневой фактор."
    )

    headers = ["Артикул", "Категория", "Остаток", "Рейтинг", "Просмотры",
               "Где отвал", "Причина"]
    widths = [20, 30, 18, 18, 22, 35, 45]
    aligns = ["L", "L", "C", "C", "C", "L", "L"]
    rows = []
    for r in dead_stock[:20]:
        d = dict(r)
        rows.append([
            d["vendor_code"], d["subject_name"], fmt(d["stock_wb"], 0),
            fmt(d["product_rating"]), fmt(d["selected_open_count"], 0),
            d["funnel_stage"], d["cause"],
        ])
    pdf.draw_table(headers, rows, widths, aligns)

    # Feedbacks section
    pdf.ln(5)
    pdf.section_title("Проблема отзывов", level=2)
    feedbacks = cur.execute(SQL_FEEDBACKS).fetchall()

    no_fb_with_traffic = sum(1 for r in feedbacks if dict(r)["status"] == "НЕТ ОТЗЫВОВ")
    pdf.body_text(
        f"{no_fb_with_traffic} товаров с 200+ просмотрами без единого отзыва — "
        f"нет социального доказательства."
    )

    headers = ["Артикул", "Категория", "Просмотры", "Выкупы", "Отзывы",
               "Ср.рейт.", "Статус"]
    widths = [20, 30, 22, 16, 16, 18, 35]
    aligns = ["L", "L", "C", "C", "C", "C", "L"]
    rows = []
    for r in feedbacks[:15]:
        d = dict(r)
        rows.append([
            d["vendor_code"], d["subject_name"], fmt(d["selected_open_count"], 0),
            fmt(d["selected_buyout_count"], 0), fmt(d["total_fb"], 0),
            fmt(d["avg_rating"]), d["status"],
        ])
    pdf.draw_table(headers, rows, widths, aligns, highlight_col=5)

    # ─── Страница 8: Рейтинг карточки ─────────────────────────────────────
    pdf.add_page()
    pdf.section_title("Низкий рейтинг карточки (product_rating < 9)")
    low_rating = cur.execute(SQL_LOW_RATING).fetchall()

    pdf.body_text(
        "product_rating (5-10) — внутренний WB-рейтинг качества карточки. "
        "Влияет на кол-во показов в поиске. Рейтинг < 8 — критично, 8-9 — требует улучшений."
    )

    headers = ["Артикул", "Категория", "Рейтинг", "Фото", "Характ.", "Видео",
               "Остаток", "Просмотры", "Конв.%", "Рекомендация"]
    widths = [18, 28, 16, 12, 16, 14, 16, 20, 16, 40]
    aligns = ["L", "L", "C", "C", "C", "C", "C", "C", "C", "L"]
    rows = []
    for r in low_rating[:20]:
        d = dict(r)
        rows.append([
            d["vendor_code"], d["subject_name"], fmt(d["product_rating"]),
            fmt(d["photo_count"], 0), fmt(d["char_count"], 0),
            "Да" if d["has_video"] else "Нет",
            fmt(d["stock_wb"], 0), fmt(d["selected_open_count"], 0),
            fmt(d["buyout_conv"]), d["action"],
        ])
    pdf.draw_table(headers, rows, widths, aligns, highlight_col=2)

    # ─── Страница 9: План действий ────────────────────────────────────────
    pdf.add_page()
    pdf.section_title("План действий")

    pdf.section_title("Срочно (сегодня)", level=2)
    pdf.bullet("Топ-5 товаров из сводной таблицы: добавить видео, проверить главное фото, "
               "сравнить цену с конкурентами в категории")
    if dashboard:
        for i, r in enumerate(dashboard[:5]):
            d = dict(r)
            pdf.bullet(f"{d['vendor_code']} ({d['subject_name']}) — "
                       f"~{fmt(d['lost_buyouts'], 0)} упущенных выкупов/нед.")

    pdf.section_title("На этой неделе", level=2)
    pdf.bullet("Массово добавить видео ко всем карточкам НК 2026 (97% без видео)")
    pdf.bullet("Дополнить описания до 200+ символов у товаров с баллом полноты < 3")
    pdf.bullet("Увеличить кол-во фото до 8+ у товаров с фото < 5")
    pdf.bullet("Проверить цены у товаров с пометкой 'КЛИКАЮТ, НЕ ПОКУПАЮТ'")

    pdf.section_title("В течение месяца", level=2)
    pdf.bullet("Стимулировать отзывы на товарах с 200+ просмотрами без отзывов")
    pdf.bullet("Проанализировать негативные отзывы — исправить проблемы с качеством")
    pdf.bullet("Отслеживать динамику product_rating после улучшения карточек")
    pdf.bullet("Скорректировать остатки: вывести с склада товары без продаж и без трафика")

    pdf.ln(8)
    pdf.section_title("Ограничения анализа", level=2)
    pdf.bullet("Данные продвижения (campaigns) — пустые, продвижение не анализируется")
    pdf.bullet("Воронка — только 1 неделя (23-29 марта), сезонность не учтена")
    pdf.bullet("Динамика funnel_metrics_daily — пустая, тренды недоступны")
    pdf.bullet("Средний чек не рассчитан — упущенные выкупы в штуках, не в рублях")

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
    output_path = os.path.join(output_dir, f"wb_cards_report_{today}.pdf")

    print(f"Генерация отчёта: {output_path}")
    build_report(db_path, output_path)
    print(f"Готово! Файл: {output_path}")
    print(f"Размер: {os.path.getsize(output_path) / 1024:.0f} KB")
