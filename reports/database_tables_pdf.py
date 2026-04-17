#!/usr/bin/env python3
"""
Генерация PDF документации по таблицам базы данных Poncho AI.
Использует библиотеку reportlab с поддержкой Unicode.
"""

from reportlab.lib.pagesizes import A4, landscape
from reportlab.lib import colors
from reportlab.platypus import SimpleDocTemplate, Table, TableStyle, Paragraph, Spacer, PageBreak
from reportlab.lib.styles import getSampleStyleSheet, ParagraphStyle
from reportlab.lib.units import mm
from reportlab.pdfbase import pdfmetrics
from reportlab.pdfbase.ttfonts import TTFont
from reportlab.lib.enums import TA_CENTER, TA_LEFT
from datetime import datetime
import os


# Table data organized by category
TABLES_DATA = [
    {
        'category': 'Продажи и услуги (Statistics API)',
        'tables': [
            ('sales', 'POST /api/v5/supplier/reportDetailByPeriod', 'id INTEGER AUTOINCREMENT', 'rrd_id INTEGER UNIQUE'),
            ('service_records', 'POST /api/v5/supplier/reportDetailByPeriod', 'id INTEGER AUTOINCREMENT', 'rrd_id INTEGER UNIQUE'),
        ],
        'api_url': 'https://seller-analytics-api.wildberries.ru',
        'rate_limit': '100/мин',
        'utility': 'download-wb-sales'
    },
    {
        'category': 'Воронка аналитики (Analytics API v3)',
        'tables': [
            ('products', 'Derived from funnel API', 'nm_id INTEGER', '(сам PK)'),
            ('funnel_metrics_daily', 'POST /api/analytics/v3/sales-funnel/products/history', 'id INTEGER AUTOINCREMENT', 'UNIQUE(nm_id, metric_date)'),
            ('funnel_metrics_aggregated', 'POST /api/analytics/v3/sales-funnel/products', 'id INTEGER AUTOINCREMENT', 'UNIQUE(nm_id, period_start, period_end)'),
        ],
        'api_url': 'https://seller-analytics-api.wildberries.ru',
        'rate_limit': '3/мин',
        'utility': 'download-wb-funnel / download-wb-funnel-agg'
    },
    {
        'category': 'Продвижение и реклама (Advertising API)',
        'tables': [
            ('campaigns', 'GET /adv/v1/promotion/count', 'advert_id INTEGER', '(сам PK)'),
            ('campaign_stats_daily', 'GET /adv/v3/fullstats', 'id INTEGER AUTOINCREMENT', 'UNIQUE(advert_id, stats_date)'),
            ('campaign_products', 'GET /adv/v3/fullstats', 'id INTEGER AUTOINCREMENT', 'UNIQUE(advert_id, nm_id)'),
            ('campaign_stats_app', 'GET /adv/v3/fullstats', 'id INTEGER AUTOINCREMENT', 'UNIQUE(advert_id, stats_date, app_type)'),
            ('campaign_stats_nm', 'GET /adv/v3/fullstats', 'id INTEGER AUTOINCREMENT', 'UNIQUE(advert_id, stats_date, app_type, nm_id)'),
            ('campaign_booster_stats', 'GET /adv/v3/fullstats', 'id INTEGER AUTOINCREMENT', 'UNIQUE(advert_id, stats_date, nm_id)'),
        ],
        'api_url': 'https://advert-api.wildberries.ru',
        'rate_limit': '20-100/мин',
        'utility': 'download-wb-promotion'
    },
    {
        'category': 'Отзывы (Feedbacks API)',
        'tables': [
            ('feedbacks', 'GET /api/v1/feedbacks', 'id TEXT', '(сам PK)'),
            ('questions', 'GET /api/v1/questions', 'id TEXT', '(сам PK)'),
            ('product_quality_summary', 'LLM анализ', 'product_nm_id INTEGER', '(сам PK)'),
        ],
        'api_url': 'https://feedbacks-api.wildberries.ru',
        'rate_limit': '3 запр/сек',
        'utility': 'download-wb-feedbacks'
    },
    {
        'category': 'Остатки на складах (Analytics API)',
        'tables': [
            ('stocks_daily_warehouses', 'POST /api/analytics/v1/stocks-report/wb-warehouses', 'id INTEGER AUTOINCREMENT', 'UNIQUE(snapshot_date, nm_id, chrt_id, warehouse_id)'),
            ('stock_history_reports', 'POST /api/v2/nm-report/downloads', 'id TEXT', 'UNIQUE(report_type, start_date, end_date, stock_type)'),
            ('stock_history_metrics', 'CSV загрузка', 'id INTEGER AUTOINCREMENT', 'FK к stock_history_reports'),
            ('stock_history_daily', 'CSV загрузка', 'id INTEGER AUTOINCREMENT', 'FK к stock_history_reports'),
        ],
        'api_url': 'https://seller-analytics-api.wildberries.ru',
        'rate_limit': '100/мин',
        'utility': 'download-wb-stocks / download-wb-stock-history'
    },
    {
        'category': 'Карточки товаров (Content API)',
        'tables': [
            ('cards', 'POST /content/v2/get/cards/list', 'nm_id INTEGER', '(сам PK)'),
            ('card_photos', 'POST /content/v2/get/cards/list', 'id INTEGER AUTOINCREMENT', 'UNIQUE(nm_id, big)'),
            ('card_sizes', 'POST /content/v2/get/cards/list', 'chrt_id INTEGER', '(сам PK)'),
            ('card_characteristics', 'POST /content/v2/get/cards/list', 'id INTEGER AUTOINCREMENT', 'UNIQUE(nm_id, char_id)'),
            ('card_tags', 'POST /content/v2/get/cards/list', 'id INTEGER AUTOINCREMENT', 'UNIQUE(nm_id, tag_id)'),
            ('cards_download_meta', 'Метаданные', 'key TEXT', '(сам PK)'),
        ],
        'api_url': 'https://content-api.wildberries.ru',
        'rate_limit': '100/мин',
        'utility': 'download-wb-cards'
    },
    {
        'category': 'Цены (Discounts-Prices API)',
        'tables': [
            ('product_prices', 'GET /api/v2/list/goods/filter', 'Составной: (nm_id, snapshot_date)', '(сам PK составной)'),
        ],
        'api_url': 'https://discounts-prices-api.wildberries.ru',
        'rate_limit': '100/мин',
        'utility': 'download-wb-prices'
    },
    {
        'category': 'Продажи по регионам (Seller Analytics API)',
        'tables': [
            ('region_sales', 'GET /api/v1/analytics/region-sale', 'id INTEGER AUTOINCREMENT', 'UNIQUE(nm_id, region_name, city_name, country_name, date_from, date_to)'),
        ],
        'api_url': 'https://seller-analytics-api.wildberries.ru',
        'rate_limit': '100/мин',
        'utility': 'download-wb-region-sales'
    },
    {
        'category': 'Данные 1C/PIM (кастомные API)',
        'tables': [
            ('onec_goods', '/feeds/ones/goods/', 'guid TEXT', '(сам PK)'),
            ('onec_goods_sku', '/feeds/ones/goods/', 'Составной: (sku_guid, guid)', '(сам PK составной)'),
            ('onec_prices', '/feeds/ones/prices/', 'Составной: (good_guid, snapshot_date, type_guid)', '(сам PK составной)'),
            ('pim_goods', '/feeds/pim/goods/', 'identifier TEXT', '(сам PK)'),
        ],
        'api_url': 'Кастомные URL через ENV',
        'rate_limit': 'N/A',
        'utility': 'download-1c-data'
    },
    {
        'category': 'Поставки FBW (Supplies API)',
        'tables': [
            ('wb_warehouses', 'GET /api/v1/warehouses', 'id INTEGER', '(сам PK)'),
            ('wb_transit_tariffs', 'GET /api/v1/transit-tariffs', 'id INTEGER AUTOINCREMENT', '(сам PK)'),
            ('supplies', 'POST /api/v1/supplies + GET /api/v1/supplies/{ID}', 'Составной: (supply_id, preorder_id)', '(сам PK составной)'),
            ('supply_goods', 'GET /api/v1/supplies/{ID}/goods', 'id INTEGER AUTOINCREMENT', 'UNIQUE(supply_id, preorder_id, barcode)'),
            ('supply_packages', 'GET /api/v1/supplies/{ID}/package', 'id INTEGER AUTOINCREMENT', 'UNIQUE(supply_id, preorder_id, package_code)'),
        ],
        'api_url': 'https://supplies-api.wildberries.ru',
        'rate_limit': '30/мин',
        'utility': 'download-wb-supplies'
    },
]

API_URLS = [
    ('Statistics API', 'https://seller-analytics-api.wildberries.ru', '100/мин'),
    ('Analytics API v3', 'https://seller-analytics-api.wildberries.ru', '3/мин'),
    ('Feedbacks API', 'https://feedbacks-api.wildberries.ru', '3 запр/сек'),
    ('Content API', 'https://content-api.wildberries.ru', '100/мин'),
    ('Advertising API', 'https://advert-api.wildberries.ru', '20-100/мин'),
    ('Discounts-Prices API', 'https://discounts-prices-api.wildberries.ru', '100/мин'),
    ('Supplies API (FBW)', 'https://supplies-api.wildberries.ru', '30/мин'),
    ('1C/PIM', 'Кастомные URL через ENV', 'N/A'),
]

INSIGHTS = [
    '1. Организация схемы: Модульный подход с 8 отдельными файлами схем, каждый представляет домен данных.',
    '2. Паттерны уникальных ключей: Большинство таблиц используют составные естественные ключи для UNIQUE ограничений (например, nm_id + date), что позволяет безопасные upsert-операции через INSERT OR REPLACE для идемпотентной загрузки данных.',
    '3. Стратегия первичных ключей: Суррогатные ключи (INTEGER AUTOINCREMENT) и естественные ключи. Суррогатные ключи позволяют foreign key связи; естественные ключи предотвращают дубликаты на бизнес-уровне.',
    '4. Стратегия индексов: Целевые индексы для общих паттернов запросов: составные nm_id + date для временных рядов, однострочные индексы на foreign keys для JOIN производительности, частичные индексы для разреженных полей.',
    '5. CASCADE для foreign keys: Таблицы карточек используют ON DELETE CASCADE для автоматической очистки дочерних записей при удалении родительской карточки.',
    '6. Составные первичные ключи: Таблицы типа product_prices и onec_prices используют составные PK, которые служат одновременно как первичный ключ И уникальное ограничение в SQLite.',
]


def create_pdf(output_path):
    """Генерация PDF документа."""

    # Register DejaVu Sans font with Cyrillic support
    dejavu_path = '/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf'
    if os.path.exists(dejavu_path):
        pdfmetrics.registerFont(TTFont('Cyrillic', dejavu_path))
        print(f'Используется шрифт: {dejavu_path}')
    else:
        raise FileNotFoundError(f'Шрифт не найден: {dejavu_path}')

    doc = SimpleDocTemplate(
        output_path,
        pagesize=landscape(A4),
        rightMargin=10*mm,
        leftMargin=10*mm,
        topMargin=15*mm,
        bottomMargin=10*mm
    )

    # Styles
    styles = getSampleStyleSheet()

    # Title style
    title_style = ParagraphStyle(
        'CustomTitle',
        parent=styles['Heading1'],
        fontName='Cyrillic',
        fontSize=20,
        alignment=TA_CENTER,
        spaceAfter=10*mm
    )

    # Subtitle style
    subtitle_style = ParagraphStyle(
        'CustomSubtitle',
        parent=styles['Normal'],
        fontName='Cyrillic',
        fontSize=14,
        alignment=TA_CENTER,
        spaceAfter=15*mm
    )

    # Heading style
    heading_style = ParagraphStyle(
        'CustomHeading',
        parent=styles['Heading2'],
        fontName='Cyrillic',
        fontSize=12,
        spaceAfter=5*mm,
        textColor=colors.HexColor('#1a1a6e')
    )

    # Normal style with proper encoding
    normal_style = ParagraphStyle(
        'CustomNormal',
        parent=styles['Normal'],
        fontName='Cyrillic',
        fontSize=10,
        spaceAfter=3*mm
    )

    # Small style
    small_style = ParagraphStyle(
        'CustomSmall',
        parent=styles['Normal'],
        fontName='Cyrillic',
        fontSize=8,
        spaceAfter=2*mm
    )

    # Table cell style (for wrapped text in tables)
    table_cell_style = ParagraphStyle(
        'TableCell',
        parent=styles['Normal'],
        fontName='Cyrillic',
        fontSize=7,
        wordWrap='CJK'
    )

    # Build story
    story = []

    # Title
    story.append(Paragraph('Таблицы базы данных Poncho AI', title_style))
    story.append(Paragraph('Полная справочная документация', subtitle_style))

    # Summary
    story.append(Paragraph('Краткая сводка', heading_style))
    total_tables = sum(len(cat['tables']) for cat in TABLES_DATA)
    summary_data = [
        ['Всего категорий:', str(len(TABLES_DATA))],
        ['Всего таблиц:', str(total_tables)],
        ['База данных:', 'SQLite'],
        ['Проект:', 'Poncho AI (Go-based LLM-agnostic framework)'],
    ]
    summary_table = Table(summary_data, colWidths=[80*mm, 100*mm])
    summary_table.setStyle(TableStyle([
        ('FONTNAME', (0, 0), (-1, -1), 'Cyrillic'),
        ('FONTSIZE', (0, 0), (-1, -1), 10),
        ('VALIGN', (0, 0), (-1, -1), 'MIDDLE'),
        ('BOTTOMPADDING', (0, 0), (-1, -1), 3),
    ]))
    story.append(summary_table)
    story.append(Spacer(1, 5*mm))

    # Categories overview
    story.append(Paragraph('Обзор категорий', heading_style))
    cat_headers = ['Категория', 'Таблиц', 'API URL', 'Ограничение', 'Утилита']
    cat_data = [cat_headers]
    for cat in TABLES_DATA:
        cat_name = cat['category'].split('(')[0].strip()
        cat_data.append([
            Paragraph(cat_name, table_cell_style),
            str(len(cat['tables'])),
            Paragraph(cat['api_url'], table_cell_style),
            Paragraph(cat['rate_limit'], table_cell_style),
            Paragraph(cat.get('utility', ''), table_cell_style),
        ])

    cat_table = Table(cat_data, colWidths=[60*mm, 12*mm, 80*mm, 25*mm, 28*mm], repeatRows=1)
    cat_table.setStyle(TableStyle([
        ('BACKGROUND', (0, 0), (-1, 0), colors.grey),
        ('FONTNAME', (0, 0), (-1, 0), 'Cyrillic'),
        ('FONTSIZE', (0, 0), (-1, 0), 9),
        ('VALIGN', (0, 0), (-1, -1), 'MIDDLE'),
        ('GRID', (0, 0), (-1, -1), 0.5, colors.black),
        ('TOPPADDING', (0, 0), (-1, -1), 3),
        ('BOTTOMPADDING', (0, 0), (-1, -1), 3),
    ]))
    story.append(cat_table)
    story.append(Spacer(1, 8*mm))

    # Detailed tables per category
    for cat in TABLES_DATA:
        story.append(Paragraph(cat['category'], heading_style))

        # API info
        api_info = f'<b>API URL:</b> {cat["api_url"]}&nbsp;&nbsp;&nbsp;&nbsp;<b>Ограничение:</b> {cat["rate_limit"]}'
        story.append(Paragraph(api_info, normal_style))
        story.append(Spacer(1, 3*mm))

        # Table headers
        table_headers = ['Название таблицы', 'API Endpoint', 'Первичный ключ', 'Уникальное ограничение', 'Утилита']
        table_data = [table_headers]

        for tbl in cat['tables']:
            row = [Paragraph(str(cell), table_cell_style) for cell in tbl]
            row.append(Paragraph(cat.get('utility', ''), table_cell_style))
            table_data.append(row)

        detail_table = Table(table_data, colWidths=[30*mm, 60*mm, 45*mm, 60*mm, 35*mm], repeatRows=1)
        detail_table.setStyle(TableStyle([
            ('BACKGROUND', (0, 0), (-1, 0), colors.lightgrey),
            ('FONTNAME', (0, 0), (-1, 0), 'Cyrillic'),
            ('FONTSIZE', (0, 0), (-1, 0), 8),
            ('VALIGN', (0, 0), (-1, -1), 'TOP'),
            ('GRID', (0, 0), (-1, -1), 0.5, colors.black),
            ('TOPPADDING', (0, 0), (-1, -1), 3),
            ('BOTTOMPADDING', (0, 0), (-1, -1), 3),
            ('LEFTPADDING', (0, 0), (-1, -1), 3),
            ('RIGHTPADDING', (0, 0), (-1, -1), 3),
        ]))
        story.append(detail_table)
        story.append(Spacer(1, 5*mm))

    story.append(PageBreak())

    # Key insights
    story.append(Paragraph('Ключевые особенности', heading_style))
    for insight in INSIGHTS:
        story.append(Paragraph(insight, normal_style))

    story.append(Spacer(1, 10*mm))

    # API endpoints reference
    story.append(Paragraph('Справочник API эндпоинтов', heading_style))
    api_headers = ['API', 'Base URL', 'Ограничение']
    api_data = [api_headers]
    for url in API_URLS:
        api_data.append([
            Paragraph(url[0], table_cell_style),
            Paragraph(url[1], table_cell_style),
            Paragraph(url[2], table_cell_style),
        ])

    api_table = Table(api_data, colWidths=[50*mm, 100*mm, 30*mm], repeatRows=1)
    api_table.setStyle(TableStyle([
        ('BACKGROUND', (0, 0), (-1, 0), colors.grey),
        ('FONTNAME', (0, 0), (-1, 0), 'Cyrillic'),
        ('FONTSIZE', (0, 0), (-1, 0), 9),
        ('VALIGN', (0, 0), (-1, -1), 'MIDDLE'),
        ('GRID', (0, 0), (-1, -1), 0.5, colors.black),
        ('TOPPADDING', (0, 0), (-1, -1), 4),
        ('BOTTOMPADDING', (0, 0), (-1, -1), 4),
    ]))
    story.append(api_table)

    # Footer with timestamp
    story.append(Spacer(1, 15*mm))
    footer = f'<font size="8">Создано: {datetime.now().strftime("%Y-%m-%d %H:%M")}</font>'
    story.append(Paragraph(footer, ParagraphStyle('Footer', parent=styles['Normal'], fontName='Cyrillic', fontSize=8, alignment=TA_CENTER)))

    # Build PDF
    doc.build(story)
    print(f'PDF создан: {output_path}')


def main():
    """Точка входа."""
    output_path = '/home/ilkoid/go-workspace/src/poncho-ai/reports/database_tables.pdf'
    create_pdf(output_path)


if __name__ == '__main__':
    main()
