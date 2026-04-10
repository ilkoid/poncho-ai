#!/usr/bin/env python3
"""
Генерация PDF отчёта по анализу утилиты compare-wb-1c-prices.
Альбомный A4, шрифт DejaVu Sans для кириллицы.
"""

from reportlab.lib.pagesizes import A4, landscape
from reportlab.lib import colors
from reportlab.platypus import (
    SimpleDocTemplate, Table, TableStyle, Paragraph, Spacer, PageBreak,
    KeepTogether
)
from reportlab.lib.styles import getSampleStyleSheet, ParagraphStyle
from reportlab.lib.units import mm
from reportlab.pdfbase import pdfmetrics
from reportlab.pdfbase.ttfonts import TTFont
from reportlab.lib.enums import TA_CENTER, TA_LEFT, TA_JUSTIFY
from datetime import datetime
import os


def create_pdf(output_path):
    """Генерация PDF отчёта."""

    # Шрифт с кириллицей
    dejavu_path = '/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf'
    if not os.path.exists(dejavu_path):
        raise FileNotFoundError(f'Шрифт не найден: {dejavu_path}')
    pdfmetrics.registerFont(TTFont('Cyrillic', dejavu_path))

    doc = SimpleDocTemplate(
        output_path,
        pagesize=landscape(A4),
        rightMargin=12*mm,
        leftMargin=12*mm,
        topMargin=12*mm,
        bottomMargin=10*mm
    )

    styles = getSampleStyleSheet()

    # Стили
    title_style = ParagraphStyle(
        'CustomTitle', parent=styles['Heading1'],
        fontName='Cyrillic', fontSize=18,
        alignment=TA_CENTER, spaceAfter=4*mm
    )
    subtitle_style = ParagraphStyle(
        'CustomSubtitle', parent=styles['Normal'],
        fontName='Cyrillic', fontSize=11,
        alignment=TA_CENTER, spaceAfter=6*mm,
        textColor=colors.HexColor('#555555')
    )
    heading_style = ParagraphStyle(
        'CustomHeading', parent=styles['Heading2'],
        fontName='Cyrillic', fontSize=13,
        spaceAfter=4*mm, spaceBefore=6*mm,
        textColor=colors.HexColor('#1a1a6e')
    )
    subheading_style = ParagraphStyle(
        'CustomSubHeading', parent=styles['Heading3'],
        fontName='Cyrillic', fontSize=11,
        spaceAfter=3*mm, spaceBefore=4*mm,
        textColor=colors.HexColor('#333399')
    )
    normal_style = ParagraphStyle(
        'CustomNormal', parent=styles['Normal'],
        fontName='Cyrillic', fontSize=9,
        spaceAfter=2*mm, leading=13
    )
    code_style = ParagraphStyle(
        'CodeStyle', parent=styles['Normal'],
        fontName='Cyrillic', fontSize=8,
        spaceAfter=2*mm, leading=11,
        leftIndent=10*mm,
        backColor=colors.HexColor('#f5f5f5'),
        borderColor=colors.HexColor('#dddddd'),
        borderWidth=0.5,
        borderPadding=4,
    )
    cell_style = ParagraphStyle(
        'TableCell', parent=styles['Normal'],
        fontName='Cyrillic', fontSize=8,
        wordWrap='CJK', leading=10
    )
    cell_style_bold = ParagraphStyle(
        'TableCellBold', parent=cell_style,
        fontSize=8, leading=10
    )
    small_style = ParagraphStyle(
        'Small', parent=styles['Normal'],
        fontName='Cyrillic', fontSize=7,
        spaceAfter=1*mm, textColor=colors.HexColor('#666666')
    )
    question_style = ParagraphStyle(
        'Question', parent=styles['Normal'],
        fontName='Cyrillic', fontSize=9,
        spaceAfter=3*mm, leading=13,
        leftIndent=5*mm,
        backColor=colors.HexColor('#fffff0'),
        borderColor=colors.HexColor('#cccc99'),
        borderWidth=0.5,
        borderPadding=5,
    )

    story = []

    # ====================== ТИТУЛЬНАЯ СТРАНИЦА ======================
    story.append(Spacer(1, 30*mm))
    story.append(Paragraph('Анализ утилиты compare-wb-1c-prices', title_style))
    story.append(Paragraph('Проблемы расчёта СПП и отклонений цен', subtitle_style))
    story.append(Spacer(1, 10*mm))

    meta_data = [
        [Paragraph('<b>Дата:</b>', cell_style), Paragraph('2026-04-10', cell_style)],
        [Paragraph('<b>Утилита:</b>', cell_style), Paragraph('cmd/data-analyzers/compare-wb-1c-prices/', cell_style)],
        [Paragraph('<b>Источник данных:</b>', cell_style), Paragraph('db/wb-sales.db → bi.db (таблица price_comparison)', cell_style)],
        [Paragraph('<b>Заказчик:</b>', cell_style), Paragraph('Финансовый директор (PowerBI отчёт)', cell_style)],
    ]
    meta_table = Table(meta_data, colWidths=[50*mm, 180*mm])
    meta_table.setStyle(TableStyle([
        ('FONTNAME', (0, 0), (-1, -1), 'Cyrillic'),
        ('FONTSIZE', (0, 0), (-1, -1), 10),
        ('VALIGN', (0, 0), (-1, -1), 'MIDDLE'),
        ('BOTTOMPADDING', (0, 0), (-1, -1), 4),
    ]))
    story.append(meta_table)

    story.append(PageBreak())

    # ====================== РАЗДЕЛ 1: ТЕКУЩАЯ ЛОГИКА ======================
    story.append(Paragraph('1. Текущая логика расчётов', heading_style))

    story.append(Paragraph('1.1. Цены из источников', subheading_style))

    prices_headers = ['Поле в БД', 'Источник', 'Описание']
    prices_data = [prices_headers]
    prices_rows = [
        ('onec_base_price', '1C onec_prices (тип "Розничная цена ОЭК")', 'Розничная цена без скидок'),
        ('onec_spp25_price', 'Вычисляется: onec_base_price × 0.75', 'ОЭК с захардкоженным СПП 25%'),
        ('onec_sr_price', '1C onec_prices (тип "Розничная цена СР")', 'Цена для розничных магазинов'),
        ('onec_special_price', '1C onec_prices (тип "Спец цена для акции")', 'Флаг акции (цена=1 → активна)'),
        ('wb_price', 'WB API product_prices.price', 'Розничная цена WB (без скидки)'),
        ('wb_discounted_price', 'WB API product_prices.discounted_price', 'Цена со скидкой продавца'),
        ('wb_discount_pct', 'WB API product_prices.discount', 'Скидка продавца %'),
        ('wb_club_price', 'WB API product_prices.club_discounted_price', 'Цена WB Club'),
        ('avg_wb_spp_3d', 'AVG(ppvz_spp_prc) из sales за 3 дня', 'Реальный СПП WB из продаж'),
        ('avg_wb_spp_assortment', 'AVG(ppvz_spp_prc) по всем товарам', 'Средний СПП по ассортименту'),
    ]
    for row in prices_rows:
        prices_data.append([Paragraph(c, cell_style) for c in row])

    prices_table = Table(prices_data, colWidths=[55*mm, 90*mm, 90*mm], repeatRows=1)
    prices_table.setStyle(TableStyle([
        ('BACKGROUND', (0, 0), (-1, 0), colors.HexColor('#1a1a6e')),
        ('TEXTCOLOR', (0, 0), (-1, 0), colors.white),
        ('FONTNAME', (0, 0), (-1, 0), 'Cyrillic'),
        ('FONTSIZE', (0, 0), (-1, 0), 9),
        ('VALIGN', (0, 0), (-1, -1), 'TOP'),
        ('GRID', (0, 0), (-1, -1), 0.5, colors.grey),
        ('TOPPADDING', (0, 0), (-1, -1), 3),
        ('BOTTOMPADDING', (0, 0), (-1, -1), 3),
        ('LEFTPADDING', (0, 0), (-1, -1), 4),
        ('RIGHTPADDING', (0, 0), (-1, -1), 4),
        ('ROWBACKGROUNDS', (0, 1), (-1, -1), [colors.white, colors.HexColor('#f8f8ff')]),
    ]))
    story.append(prices_table)
    story.append(Spacer(1, 5*mm))

    story.append(Paragraph('1.2. Формулы отклонений (текущие)', subheading_style))
    story.append(Paragraph(
        '<b>diff_base</b> = wb_price − onec_base_price<br/>'
        '<b>diff_base_pct</b> = diff_base / onec_base_price × 100<br/><br/>'
        '<b>diff_discounted</b> = wb_discounted_price − onec_spp25_price<br/>'
        '<b>diff_discounted_pct</b> = diff_discounted / onec_spp25_price × 100<br/><br/>'
        'Где <b>onec_spp25_price = onec_base_price × 0.75</b> (захардкождено в query.go:127).',
        code_style
    ))

    story.append(Paragraph('1.3. Источник СПП (текущая логика)', subheading_style))
    story.append(Paragraph(
        'avg_wb_spp_3d = COALESCE(товарный_СПП, ассортиментный_СПП)<br/><br/>'
        '• Если есть продажи товара за 3 дня → средний СПП по этому товару<br/>'
        '• Если продаж нет → fallback на средний СПП по всему ассортименту<br/>'
        '• <b>Пользовательский SPP (spp25_percent из конфига) используется ТОЛЬКО '
        'для onec_spp25_price, но НЕ как fallback для отсутствующих продаж</b>',
        normal_style
    ))

    # ====================== РАЗДЕЛ 2: ПРОБЛЕМЫ ======================
    story.append(Paragraph('2. Маппинг проблем на код', heading_style))

    # --- Проблема 1 ---
    story.append(Paragraph('Проблема 1: "СПП задаваемый влияет на Отклонение СР от РЦ WB, а не на Отклонение СР от ОЭК с СПП"', subheading_style))
    story.append(Paragraph(
        '<b>Файл:</b> main.go:221<br/>'
        '<b>Код:</b> diffDiscounted := s.WBDiscountPrice − s.OneCSPP25Price<br/><br/>'
        '<b>Причина:</b> OneCSPP25Price = onec_base_price × (1 − 25/100) — это ОЭК с СПП, а не РЦ WB. '
        'Поэтому изменение spp25_percent в конфиге меняет diff_discounted, что PowerBI показывает как '
        '"Отклонение СР от ОЭК с СПП".<br/><br/>'
        '<b>Вывод:</b> Требуется уточнение — какие именно колонки в PowerBI соответствуют каким полям в price_comparison.',
        normal_style
    ))

    # --- Проблема 2 ---
    story.append(Paragraph('Проблема 2: "Цена ОЭК с СПП задаваемой не выведена"', subheading_style))
    story.append(Paragraph(
        '<b>Файл:</b> query.go:127<br/>'
        '<b>Код:</b> ROUND(op.price × 0.75, 2) AS onec_spp25_price<br/><br/>'
        '<b>Причина:</b> Поле onec_spp25_price использует захардкожденный коэффициент 0.75 (СПП=25%). '
        'Нет отдельного поля, которое считает onec_base_price × (1 − effective_spp / 100), '
        'где effective_spp — реальный СПП из WB продаж.<br/><br/>'
        '<b>Статус:</b> Поле onec_spp25_price есть, но не отражает реальный СПП. Нужна новая колонка.',
        normal_style
    ))

    # --- Проблема 3 ---
    story.append(Paragraph('Проблема 3: "Отклонение СР от ОЭК с СПП % не меняется при изменении никаких параметров"', subheading_style))
    story.append(Paragraph(
        '<b>Файл:</b> main.go:227-229<br/>'
        '<b>Код:</b> diffDiscPct = (diffDiscounted / s.OneCSPP25Price) × 100<br/><br/>'
        '<b>Причина (подтверждено):</b> OneCSPP25Price = onec_base_price × 0.75 — константа, '
        'которая <b>не зависит</b> от:<br/>'
        '  • avg_wb_spp_3d (реальный СПП из продаж)<br/>'
        '  • spp25_percent (пользовательский SPP в конфиге)<br/>'
        '  • onec_special_price (скидка по программе лояльности)<br/><br/>'
        '<b>Статус: 100% баг.</b> Поле diff_discounted_pct всегда считает через один и тот же знаменатель. '
        'Нужно пересчитывать через effective_spp.',
        normal_style
    ))

    # --- Проблема 4 ---
    story.append(Paragraph('Проблема 4: "Розничная цена WB с СПП должно считаться через СПП WB из API"', subheading_style))
    story.append(Paragraph(
        '<b>Файл:</b> storage.go:59-67 (схема БД)<br/><br/>'
        '<b>Причина:</b> В таблице price_comparison нет поля wb_price_with_spp = wb_price × (1 − effective_spp / 100). '
        'Есть только wb_discounted_price из WB API (скидка продавца).<br/><br/>'
        '<b>Статус:</b> Нужна новая колонка wb_price_with_spp.',
        normal_style
    ))

    story.append(PageBreak())

    # ====================== РАЗДЕЛ 3: ТРЕБУЕМАЯ ЛОГИКА ======================
    story.append(Paragraph('3. Требуемая бизнес-логика', heading_style))

    story.append(Paragraph('3.1. Эффективный СПП', subheading_style))
    story.append(Paragraph(
        '<b>СПП_эффективный</b> = {<br/>'
        '&nbsp;&nbsp;&nbsp;&nbsp;avg_wb_spp_3d, &nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;'
        'если есть продажи за 3 дня (spp_source = "product")<br/>'
        '&nbsp;&nbsp;&nbsp;&nbsp;spp_user (пользовательский), &nbsp;&nbsp;'
        'если продаж нет<br/>'
        '}<br/><br/>'
        '<b>spp_type</b> = {<br/>'
        '&nbsp;&nbsp;&nbsp;&nbsp;"wb_api" &nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;— есть продажи, используется СПП из WB<br/>'
        '&nbsp;&nbsp;&nbsp;&nbsp;"user_fallback" — продаж нет, используется пользовательский СПП<br/>'
        '}<br/><br/>'
        '<b>Важно:</b> Ассортиментный средний СПП (avg_wb_spp_assortment) больше НЕ используется как fallback — '
        'вместо него берётся пользовательский SPP.',
        code_style
    ))

    story.append(Paragraph('3.2. Новые расчётные поля', subheading_style))
    story.append(Paragraph(
        '<b>ОЭК_с_СПП</b> &nbsp;&nbsp;&nbsp;= onec_base_price × (1 − effective_spp / 100)<br/>'
        '<b>WB_цена_с_СПП</b> = wb_price × (1 − effective_spp / 100)',
        code_style
    ))

    story.append(Paragraph('3.3. Отклонения (исправленные)', subheading_style))
    story.append(Paragraph(
        '<b>Отклонение СР от ОЭК с СПП</b> &nbsp;&nbsp;= wb_discounted_price − ОЭК_с_СПП<br/>'
        '<b>Отклонение СР от ОЭК с СПП %</b> = Отклонение / ОЭК_с_СПП × 100',
        code_style
    ))

    story.append(Paragraph('3.4. Сравнение с другими маркетплейсами (будущее)', subheading_style))
    story.append(Paragraph(
        'Пользовательский SPP нужен для расчёта цен на Ozon/Яндекс:<br/>'
        '<b>Цена_на_другом_маркете</b> = onec_base_price × (1 − spp_другого_маркета / 100)<br/><br/>'
        'На первом этапе — только WB, с одним пользовательским SPP.',
        normal_style
    ))

    # ====================== РАЗДЕЛ 4: ПЛАН ИЗМЕНЕНИЙ ======================
    story.append(Paragraph('4. План изменений', heading_style))

    plan_headers = ['№', 'Изменение', 'Файл', 'Суть']
    plan_data = [plan_headers]
    plan_rows = [
        ('1', 'Новые колонки в БД', 'storage.go',
         'effective_spp, spp_type, onec_price_with_spp, wb_price_with_spp'),
        ('2', 'Расчёт effective_spp', 'main.go',
         'Приоритет: WB продажи → пользовательский SPP'),
        ('3', 'Пересчёт diff_discounted', 'main.go',
         'Через onec_price_with_spp вместо onec_spp25_price'),
        ('4', 'Обновление CSV экспорта', 'storage.go',
         'Добавить новые колонки'),
        ('5', 'Сохранение onec_spp25_price', '—',
         'Оставить для обратной совместимости (не удалять)'),
    ]
    for row in plan_rows:
        plan_data.append([Paragraph(c, cell_style) for c in row])

    plan_table = Table(plan_data, colWidths=[10*mm, 55*mm, 40*mm, 130*mm], repeatRows=1)
    plan_table.setStyle(TableStyle([
        ('BACKGROUND', (0, 0), (-1, 0), colors.HexColor('#1a1a6e')),
        ('TEXTCOLOR', (0, 0), (-1, 0), colors.white),
        ('FONTNAME', (0, 0), (-1, 0), 'Cyrillic'),
        ('FONTSIZE', (0, 0), (-1, 0), 9),
        ('VALIGN', (0, 0), (-1, -1), 'TOP'),
        ('GRID', (0, 0), (-1, -1), 0.5, colors.grey),
        ('TOPPADDING', (0, 0), (-1, -1), 3),
        ('BOTTOMPADDING', (0, 0), (-1, -1), 3),
        ('LEFTPADDING', (0, 0), (-1, -1), 4),
        ('RIGHTPADDING', (0, 0), (-1, -1), 4),
        ('ROWBACKGROUNDS', (0, 1), (-1, -1), [colors.white, colors.HexColor('#f8f8ff')]),
    ]))
    story.append(plan_table)

    story.append(PageBreak())

    # ====================== РАЗДЕЛ 5: ВОПРОСЫ ЗАКАЗЧИКУ ======================
    story.append(Paragraph('5. Вопросы заказчику', heading_style))
    story.append(Paragraph(
        'Для корректной реализации требуются уточнения от финансового директора:',
        normal_style
    ))
    story.append(Spacer(1, 3*mm))

    questions = [
        (
            'В1. Приоритет источников СПП',
            'Текущая логика использует трёхуровневый fallback: товарный СПП → ассортиментный → нет данных.<br/><br/>'
            '<b>Вопрос:</b> Подтвердите порядок приоритета:<br/>'
            '1. СПП из продаж WB за 3 дня (товарный) — приоритет 1<br/>'
            '2. Пользовательский СПП — приоритет 2 (если продаж нет)<br/><br/>'
            'Ассортиментный средний СПП (avg_wb_spp_assortment) исключается из расчётов?'
        ),
        (
            'В2. WB Discounted Price vs WB цена с СПП',
            'Поле wb_discounted_price из WB API — это цена <b>со скидкой продавца</b> '
            '(та, что видит клиент в карточке товара без учёта персональных скидок).<br/><br/>'
            'СПП (скидка постоянного покупателя) — это <b>дополнительная</b> скидка поверх скидки продавца, '
            'которая применяется в корзине/при оформлении заказа.<br/><br/>'
            '<b>Вопрос:</b> Верно ли, что:<br/>'
            '• wb_discounted_price = цена для клиента <b>без СПП</b> (скидка продавца)<br/>'
            '• wb_discounted_price × (1 − spp/100) = цена для клиента <b>с СПП</b> (итоговая)<br/><br/>'
            'Именно итоговая цена (с СПП) должна сравниваться с ОЭК?'
        ),
        (
            'В3. "Отклонение СР от РЦ WB" — что это?',
            '<b>Вопрос:</b> "Отклонение СР от РЦ WB" в отчёте PowerBI — это:<br/>'
            '• <b>Вариант А:</b> wb_discounted_price − wb_price (размер скидки WB в рублях, всегда отрицательное)<br/>'
            '• <b>Вариант Б:</b> wb_price − onec_base_price (разница розничных цен WB vs 1C, текущий diff_base)<br/>'
            '• <b>Вариант В:</b> что-то другое?<br/><br/>'
            'Нужно точное определение для правильного маппинга.'
        ),
        (
            'В4. СПП для других маркетплейсов',
            '<b>Вопрос:</b> На первом этапе — один пользовательский SPP для всех маркетплейсов, '
            'или нужен отдельный параметр для Ozon/Яндекс? Если отдельный — какие значения по умолчанию?'
        ),
    ]

    for title, body in questions:
        story.append(Paragraph(f'<b>{title}</b>', normal_style))
        story.append(Paragraph(body, question_style))
        story.append(Spacer(1, 2*mm))

    # ====================== ПРИЛОЖЕНИЕ ======================
    story.append(PageBreak())
    story.append(Paragraph('Приложение А. Схема данных', heading_style))

    story.append(Paragraph('Источник (wb-sales.db)', subheading_style))
    story.append(Paragraph(
        'onec_goods → onec_prices (по guid + snapshot_date + type_name)<br/>'
        '&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;'
        '→ cards (по article = vendor_code)<br/>'
        '&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;'
        '→ product_prices (по nm_id + snapshot_date)<br/>'
        '&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;'
        '→ products (по nm_id)<br/>'
        '&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;'
        '→ sales (по nm_id, для СПП)<br/>'
        '&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;'
        '→ pim_goods (по article = identifier)',
        code_style
    ))

    story.append(Paragraph('Результат (bi.db)', subheading_style))
    story.append(Paragraph(
        '<b>price_comparison</b>: 48 колонок (идентификация, иерархия, атрибуты, цены 1С, цены WB, '
        'отклонения, статусы, СПП)<br/>'
        'UNIQUE(vendor_code, nm_id, wb_snapshot_date, onec_snapshot_date)',
        normal_style
    ))

    story.append(Paragraph('Ключевое поле для расчёта СПП', subheading_style))
    story.append(Paragraph(
        'Таблица <b>sales</b>, поле <b>ppvz_spp_prc</b> — фактический процент СПП из каждой продажи. '
        'Усредняется за 3 дня.<br/><br/>'
        'Текущая логика (query.go:77-87):<br/>'
        '• CTE product_spp: AVG(ppvz_spp_prc) WHERE sale_dt ≥ date(?, "-3 days") GROUP BY nm_id<br/>'
        '• CTE overall_spp: AVG(ppvz_spp_prc) WHERE sale_dt ≥ date(?, "-3 days") — по всему ассортименту<br/>'
        '• COALESCE(product_spp, overall_spp) — итоговое значение avg_wb_spp_3d<br/><br/>'
        '<b>Проблема:</b> avg_wb_spp_3d вычисляется и сохраняется, но НЕ используется в calculateComparison(). '
        'Все 4 проблемы заказчика — следствие этого разрыва.',
        normal_style
    ))

    # Футер
    story.append(Spacer(1, 15*mm))
    footer_style = ParagraphStyle(
        'Footer', parent=styles['Normal'],
        fontName='Cyrillic', fontSize=8,
        alignment=TA_CENTER,
        textColor=colors.HexColor('#888888')
    )
    story.append(Paragraph(
        f'Создано: {datetime.now().strftime("%Y-%m-%d %H:%M")} | '
        f'Утилита: cmd/data-analyzers/compare-wb-1c-prices/ | '
        f'Poncho AI v13.1',
        footer_style
    ))

    doc.build(story)
    print(f'PDF создан: {output_path}')


def main():
    output_path = '/home/ilkoid/go-workspace/src/poncho-ai/reports/compare-wb-1c-prices-analysis.pdf'
    create_pdf(output_path)


if __name__ == '__main__':
    main()
