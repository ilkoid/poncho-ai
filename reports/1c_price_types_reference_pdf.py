#!/usr/bin/env python3
"""
Генерация PDF справочника типов цен 1C и их сравнения с Wildberries.
Альбомный A4, шрифт DejaVu Sans для кириллицы.
Аудитория: финансовый директор и BI-консультант.
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
    """Генерация PDF справочника."""

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

    # --- Стили ---
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
    note_style = ParagraphStyle(
        'Note', parent=styles['Normal'],
        fontName='Cyrillic', fontSize=8,
        spaceAfter=2*mm, leading=11,
        leftIndent=5*mm,
        backColor=colors.HexColor('#f0f8ff'),
        borderColor=colors.HexColor('#aaccdd'),
        borderWidth=0.5,
        borderPadding=4,
    )

    story = []

    # ==================== ПОМОГАТЕЛЬНЫЕ ТАБЛИЦЫ ====================
    def make_table(headers, rows, col_widths, highlight_first_col=False):
        data = [[Paragraph(f'<b>{h}</b>', cell_style_bold) for h in headers]]
        for row in rows:
            data.append([Paragraph(c, cell_style) for c in row])
        t = Table(data, colWidths=col_widths, repeatRows=1)
        style_cmds = [
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
        ]
        if highlight_first_col:
            style_cmds.append(('FONTNAME', (0, 1), (0, -1), 'Cyrillic'))
        t.setStyle(TableStyle(style_cmds))
        return t

    # ==================== ТИТУЛЬНАЯ СТРАНИЦА ====================
    story.append(Spacer(1, 30*mm))
    story.append(Paragraph('Справочник типов цен 1C', title_style))
    story.append(Paragraph('Использование для сравнения с ценами Wildberries', subtitle_style))
    story.append(Spacer(1, 10*mm))

    meta_data = [
        [Paragraph('<b>Дата:</b>', cell_style), Paragraph('2026-04-10', cell_style)],
        [Paragraph('<b>Аудитория:</b>', cell_style), Paragraph('Финансовый директор, BI-консультант', cell_style)],
        [Paragraph('<b>Источник данных:</b>', cell_style), Paragraph('Учётная система 1C (25 типов цен) + API Wildberries', cell_style)],
        [Paragraph('<b>Каталог:</b>', cell_style), Paragraph('26 968 товаров, 445 822 ценовых записи на снимок', cell_style)],
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

    # ==================== РАЗДЕЛ 1: ГЛОССАРИЙ ====================
    story.append(Paragraph('1. Глоссарий', heading_style))

    glossary_rows = [
        ('ОЭК', 'Отдел Электронной Коммерции', 'Подразделение компании, отвечающее за онлайн-продажи (маркетплейсы WB, Ozon, Яндекс.Маркет)'),
        ('СР', 'Собственная Розница', 'Розничные (офлайн) магазины компании'),
        ('РЦ', 'Розничная Цена', 'Базовая цена товара без скидок'),
        ('СПП', 'Скидка Постоянного Покупателя', 'Программа лояльности WB — персональная скидка для зарегистрированных покупателей'),
        ('Скидка продавца', '—', 'Скидка, которую продавец устанавливает сам (отображается в карточке товара на WB)'),
        ('WB Club', 'Wildberries Club', 'Платная подписка WB с дополнительными скидками'),
        ('Артикул (vendor_code)', '—', 'Уникальный идентификатор товара у продавца'),
        ('nm_id', 'NM ID', 'Уникальный числовой идентификатор товара на Wildberries'),
    ]
    story.append(make_table(
        ['Термин', 'Расшифровка', 'Описание'],
        glossary_rows,
        [40*mm, 50*mm, 145*mm]
    ))
    story.append(Spacer(1, 5*mm))

    # ==================== РАЗДЕЛ 2: ТИПЫ ЦЕН 1C ====================
    story.append(Paragraph('2. Все типы цен 1C (25 типов)', heading_style))
    story.append(Paragraph(
        'Цены поступают из учётной системы 1C через API. Каждый товар может иметь до 25 типов цен одновременно. '
        'Цены обновляются ежедневно (snapshot). Всего в каталоге 26 968 товаров.',
        normal_style
    ))
    story.append(Spacer(1, 3*mm))

    # --- 2.1 Розничные ---
    story.append(Paragraph('2.1. Розничные цены (используются для сравнения с WB)', subheading_style))

    retail_rows = [
        ('<b>Розничная цена ОЭК</b>', '26 210',
         '<b>Основная цена для маркетплейсов.</b> Базовая розничная цена, которую ОЭК устанавливает для продажи на WB/Ozon/Яндекс. Используется как отправная точка для сравнения.'),
        ('Розничная цена ОЭК минус СПП 25%', '26 210',
         'ОЭК цена со скидкой 25% для постоянных покупателей. = ОЭК × 0.75. 1C считает эту цену на своей стороне.'),
        ('Розничная цена СР', '26 379',
         'Цена для собственных розничных (офлайн) магазинов. Может отличаться от ОЭК.'),
        ('Рекомендованная розничная цена СР', '26 166',
         'Рекомендованная (необязательная) цена для розничных партнёров. Справочно.'),
        ('Максимальная розничная цена СР', '3 878',
         'Верхняя граница розничной цены в канале СР.'),
        ('<b>Спец цена для акции</b>', '26 451',
         '<b>Флаг, а не цена.</b> Значение = 1.0 означает, что товар участвует в акции/программе лояльности. Значение = 0.0 — не участвует.'),
        ('Акция', '935',
         'Цена для конкретной акционной кампании (охватывает мало товаров).'),
        ('АКЦИЯ -40% ОПТ (часть 2)', '827',
         'Оптовая акционная цена со скидкой 40%.'),
    ]
    story.append(make_table(
        ['Тип цены', 'Покрытие', 'Описание'],
        retail_rows,
        [55*mm, 20*mm, 160*mm],
        highlight_first_col=True
    ))
    story.append(Spacer(1, 5*mm))

    # --- 2.2 Оптовые ---
    story.append(Paragraph('2.2. Оптовые цены (B2B каналы)', subheading_style))

    wholesale_rows = [
        ('Мелкооптовая', '26 412', 'Цена для мелкооптовых закупщиков'),
        ('Мелкооптовая минус НДС', '26 411', 'Мелкооптовая без НДС (для юрлиц на УСН)'),
        ('Мелкооптовые МАКС минус НДС', '26 411', 'Максимальная мелкооптовая без НДС'),
        ('Мелкооптовая оффлайн', '18 343', 'Мелкооптовая для офлайн-канала продаж'),
        ('Мелкооптовая цена -10%', '2 169', 'Мелкооптовая с дополнительной скидкой 10%'),
        ('Опт Дистр', '26 411', 'Оптовая цена для дистрибьюторов'),
        ('Опт СП', '26 411', 'Оптовая цена для торговых сетей-партнёров'),
    ]
    story.append(make_table(
        ['Тип цены', 'Покрытие', 'Описание'],
        wholesale_rows,
        [55*mm, 20*mm, 160*mm]
    ))
    story.append(Spacer(1, 5*mm))

    # --- 2.3 Справочные ---
    story.append(Paragraph('2.3. Справочные / системные цены', subheading_style))

    system_rows = [
        ('FullRetailPriceMAX', '26 465', 'Максимальная розничная цена (справочно)'),
        ('FullOptMaxPrice', '26 465', 'Максимальная оптовая цена (справочно)'),
        ('FullOptMaxPriceSP', '26 465', 'Максимальная оптовая цена СП (справочно)'),
    ]
    story.append(make_table(
        ['Тип цены', 'Покрытие', 'Описание'],
        system_rows,
        [55*mm, 20*mm, 160*mm]
    ))
    story.append(Spacer(1, 5*mm))

    # --- 2.4 Региональные ---
    story.append(Paragraph('2.4. Региональные / партнёрские цены', subheading_style))

    regional_rows = [
        ('Детский мир опт', '22 218', 'Оптовая цена для сети магазинов "Детский мир"'),
        ('Казахстан (тенге)', '15 396', 'Цена для рынка Казахстана в тенге'),
        ('Казахстан (тенге) МАКС', '15 396', 'Максимальная цена для Казахстана'),
        ('Розничная цена Глобус', '11 822', 'Цена для сети гипермаркетов "Глобус"'),
        ('ЗЕЛЬГРОС', '5 273', 'Цена для сети cash&amp;carry "Зельгрос"'),
        ('ФР: ИП Украинцева (Иркутск)', '6 690', 'Цена для франчайзингового партнёра в Иркутске'),
        ('Розничная цена Сити Парк Град Воронеж', '8', 'Цена для конкретного ТЦ в Воронеже'),
    ]
    story.append(make_table(
        ['Тип цены', 'Покрытие', 'Описание'],
        regional_rows,
        [55*mm, 20*mm, 160*mm]
    ))

    story.append(PageBreak())

    # ==================== РАЗДЕЛ 3: ЦЕНЫ WILDBERRIES ====================
    story.append(Paragraph('3. Цены Wildberries (из API)', heading_style))

    story.append(Paragraph(
        'Цены WB поступают из API Wildberries (Discounts-Prices API). Каждый товар имеет следующие ценовые поля:',
        normal_style
    ))

    wb_rows = [
        ('Розничная цена (price)', '1 999 ₽', 'Полная розничная цена без скидок. Аналог ОЭК в 1C.'),
        ('Цена со скидкой продавца (discounted_price)', '1 699 ₽',
         'Цена, которую видит клиент в карточке товара. = Розничная цена − Скидка продавца. <b>Не включает СПП.</b>'),
        ('Скидка продавца % (discount)', '15%', 'Процент скидки, которую продавец установил сам.'),
        ('Цена WB Club (club_discounted_price)', '1 599 ₽', 'Специальная цена для подписчиков WB Club.'),
        ('Скидка WB Club % (club_discount)', '20%', 'Процент скидки для подписчиков WB Club.'),
    ]
    story.append(make_table(
        ['Поле из API', 'Пример', 'Описание'],
        wb_rows,
        [60*mm, 25*mm, 150*mm]
    ))
    story.append(Spacer(1, 5*mm))

    # --- 3.1 Схема формирования цены ---
    story.append(Paragraph('3.1. Как формируется цена для клиента на WB', subheading_style))
    story.append(Paragraph(
        '<b>Розничная цена WB</b> (price)&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;1 999 ₽<br/>'
        '<b>− Скидка продавца</b> (discount %)&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;− 15%<br/>'
        '<b>= Цена со скидкой продавца</b> (discounted_price)&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;1 699 ₽&nbsp;&nbsp;&nbsp;&nbsp;← видна в карточке<br/><br/>'
        '&nbsp;&nbsp;&nbsp;&nbsp;<b>При оформлении заказа:</b><br/>'
        '<b>− СПП</b> (скидка постоянного покупателя)&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;− ~25%<br/>'
        '<b>= Итоговая цена для клиента</b>&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;~1 274 ₽&nbsp;&nbsp;&nbsp;&nbsp;← видна в корзине',
        code_style
    ))
    story.append(Spacer(1, 3*mm))
    story.append(Paragraph(
        '<b>Важно:</b> Скидка продавца и СПП — это <b>разные скидки</b>, которые складываются. '
        'Скидка продавца видна всем и отображается в карточке товара. '
        'СПП — персональная, применяется только в корзине/при оформлении.',
        note_style
    ))

    # --- 3.2 СПП из продаж ---
    story.append(Paragraph('3.2. СПП из реальных продаж', subheading_style))
    story.append(Paragraph(
        'СПП не приходит напрямую из API цен. Он извлекается из отчёта о продажах за каждый заказ '
        '(поле ppvz_spp_prc). Усредняется за последние 3 дня по каждому товару отдельно.<br/><br/>'
        'Если по товару были продажи за 3 дня → используется средний СПП по этому товару.<br/>'
        'Если продаж нет → используется пользовательский СПП (из параметров отчёта).',
        normal_style
    ))

    # ==================== РАЗДЕЛ 4: ФОРМУЛЫ ====================
    story.append(PageBreak())
    story.append(Paragraph('4. Сравнение цен 1C и WB: формулы', heading_style))

    story.append(Paragraph('4.1. Какие цены 1C участвуют в сравнении', subheading_style))
    story.append(Paragraph(
        'Из 25 типов цен в сравнении используются <b>3 типа</b>:',
        normal_style
    ))

    used_rows = [
        ('Розничная цена ОЭК', 'onec_base_price', 'Базовая цена для сравнения'),
        ('Розничная цена СР', 'onec_sr_price', 'Справочно: цена офлайн-канала'),
        ('Спец цена для акции', 'is_special_price', 'Флаг: товар участвует в акции (0/1)'),
    ]
    story.append(make_table(
        ['Тип цены 1C', 'Поле в отчёте', 'Роль'],
        used_rows,
        [55*mm, 45*mm, 135*mm]
    ))
    story.append(Spacer(1, 3*mm))
    story.append(Paragraph(
        'Дополнительно вычисляется: <b>ОЭК с СПП 25%</b> = Розничная цена ОЭК × 0.75<br/><br/>'
        '<b>Примечание:</b> В 1C уже существует тип "Розничная цена ОЭК минус СПП 25%" — '
        'это то же самое значение, которое вычисляется вручную в отчёте.',
        note_style
    ))

    # --- 4.2 Текущие формулы ---
    story.append(Paragraph('4.2. Текущие формулы отклонений', subheading_style))
    story.append(Paragraph(
        '<b>Отклонение розничной цены (без скидок):</b><br/>'
        '&nbsp;&nbsp;&nbsp;&nbsp;diff_base &nbsp;&nbsp;&nbsp;&nbsp;= Розничная цена WB − Розничная цена ОЭК<br/>'
        '&nbsp;&nbsp;&nbsp;&nbsp;diff_base_pct = diff_base / Розничная цена ОЭК × 100<br/><br/>'
        '<b>Отклонение цены со скидкой:</b><br/>'
        '&nbsp;&nbsp;&nbsp;&nbsp;diff_discounted &nbsp;&nbsp;&nbsp;&nbsp;= Цена WB со скидкой продавца − ОЭК × 0.75<br/>'
        '&nbsp;&nbsp;&nbsp;&nbsp;diff_discounted_pct = diff_discounted / (ОЭК × 0.75) × 100',
        code_style
    ))

    # --- 4.3 Проблемы ---
    story.append(Paragraph('4.3. Проблемы текущих формул', subheading_style))

    problems_rows = [
        ('<b>1</b>',
         'Изменение пользовательского СПП влияет не на тот показатель',
         'ОЭК × 0.75 используется в обоих отклонениях',
         'Пользовательский СПП должен менять "Отклонение СР от ОЭК с СПП", но сейчас влияет на оба показателя'),
        ('<b>2</b>',
         'Цена ОЭК с реальным СПП не выведена в отчёт',
         'Используется фиксированный СПП 25%, а не реальный из продаж WB',
         'Нет колонки, показывающей ОЭК × (1 − реальный СПП)'),
        ('<b>3</b>',
         'Процент отклонения не меняется при изменении параметров',
         'Знаменатель формулы всегда = ОЭК × 0.75 (константа)',
         'При изменении СПП или других параметров % отклонения визуально не меняется'),
        ('<b>4</b>',
         'Нет "Цены WB с СПП"',
         'СПП из реальных продаж вычисляется, но не используется для расчёта цены WB с СПП',
         'Невозможно сравнить итоговую цену для клиента с ОЭК'),
    ]
    story.append(make_table(
        ['№', 'Проблема', 'Причина', 'Влияние на PowerBI'],
        problems_rows,
        [8*mm, 50*mm, 60*mm, 117*mm]
    ))

    # --- 4.4 Требуемые формулы ---
    story.append(Spacer(1, 5*mm))
    story.append(Paragraph('4.4. Требуемые формулы (после исправления)', subheading_style))
    story.append(Paragraph(
        '<b>Эффективный СПП:</b><br/>'
        '&nbsp;&nbsp;&nbsp;&nbsp;СПП_эффективный = Средний СПП из продаж WB за 3 дня, если есть продажи<br/>'
        '&nbsp;&nbsp;&nbsp;&nbsp;СПП_эффективный = Пользовательский СПП (из параметров), если продаж нет<br/><br/>'
        '<b>Новые расчётные показатели:</b><br/>'
        '&nbsp;&nbsp;&nbsp;&nbsp;ОЭК_с_СПП &nbsp;&nbsp;&nbsp;&nbsp;= Розничная цена ОЭК × (1 − СПП_эффективный / 100)<br/>'
        '&nbsp;&nbsp;&nbsp;&nbsp;WB_цена_с_СПП = Розничная цена WB × (1 − СПП_эффективный / 100)<br/><br/>'
        '<b>Исправленные отклонения:</b><br/>'
        '&nbsp;&nbsp;&nbsp;&nbsp;Отклонение СР от ОЭК с СПП &nbsp;&nbsp;= Цена WB со скидкой продавца − ОЭК_с_СПП<br/>'
        '&nbsp;&nbsp;&nbsp;&nbsp;Отклонение СР от ОЭК с СПП % = Отклонение / ОЭК_с_СПП × 100',
        code_style
    ))

    # ==================== РАЗДЕЛ 5: СХЕМА ====================
    story.append(PageBreak())
    story.append(Paragraph('5. Сводная схема: цены 1C → цены WB → отчёт', heading_style))

    story.append(Paragraph(
        '<b>ЦЕНЫ 1C</b>&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;'
        '<b>ЦЕНЫ WILDBERRIES</b><br/><br/>'
        'Розничная цена ОЭК ────────────── <i>сравнение</i> ──── Розничная цена WB (price)<br/>'
        '&nbsp;&nbsp;&nbsp;&nbsp;│&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;'
        'Цена со скидкой продавца (discounted_price)<br/>'
        '&nbsp;&nbsp;&nbsp;&nbsp;└ × 0.75 = ОЭК с СПП 25% ─ <i>сравнение</i> ── ↑<br/>'
        '&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;'
        'Цена WB Club<br/>'
        'Розничная цена СР ──────── <i>справочно</i> ──────────── СПП из продаж (avg за 3 дня)<br/>'
        'Спец цена для акции ── <i>флаг</i> ──────────────────────── ↑<br/>',
        code_style
    ))

    # ==================== РАЗДЕЛ 6: ВОПРОСЫ ====================
    story.append(Spacer(1, 5*mm))
    story.append(Paragraph('6. Вопросы для уточнения', heading_style))
    story.append(Paragraph(
        'Для корректной настройки формул требуются уточнения:',
        normal_style
    ))
    story.append(Spacer(1, 3*mm))

    questions = [
        (
            'В1. Приоритет источников СПП',
            'Текущая логика: товарный СПП → ассортиментный → нет данных.<br/><br/>'
            '<b>Вопрос:</b> Подтвердите порядок приоритета:<br/>'
            '1. СПП из продаж WB за 3 дня — приоритет 1<br/>'
            '2. Пользовательский СПП — приоритет 2 (если продаж нет)<br/><br/>'
            'Ассортиментный средний СПП исключается из расчётов?'
        ),
        (
            'В2. Цена WB со скидкой vs Цена WB с СПП',
            'Цена со скидкой продавца (discounted_price) — это цена <b>без СПП</b> (видна в карточке товара).<br/>'
            'СПП — <b>дополнительная</b> скидка, которая применяется в корзине.<br/><br/>'
            '<b>Вопрос:</b> Верно ли, что:<br/>'
            '• Цена со скидкой продавца = цена для клиента <b>без СПП</b><br/>'
            '• Цена со скидкой продавца × (1 − СПП/100) = цена для клиента <b>с СПП</b> (итоговая)<br/><br/>'
            'Именно итоговая цена (с СПП) должна сравниваться с ОЭК?'
        ),
        (
            'В3. "Отклонение СР от РЦ WB" — определение',
            '<b>Вопрос:</b> "Отклонение СР от РЦ WB" в отчёте PowerBI — это:<br/>'
            '• <b>Вариант А:</b> Цена со скидкой продавца − Розничная цена WB (размер скидки WB, всегда отрицательное)<br/>'
            '• <b>Вариант Б:</b> Розничная цена WB − Розничная цена ОЭК (разница розничных цен WB vs 1C)<br/>'
            '• <b>Вариант В:</b> что-то другое?'
        ),
        (
            'В4. СПП для других маркетплейсов',
            '<b>Вопрос:</b> На первом этапе — один пользовательский СПП для всех маркетплейсов, '
            'или нужен отдельный параметр для Ozon/Яндекс? Если отдельный — какие значения по умолчанию?'
        ),
    ]

    for title, body in questions:
        story.append(Paragraph(f'<b>{title}</b>', normal_style))
        story.append(Paragraph(body, question_style))
        story.append(Spacer(1, 2*mm))

    # ==================== ФУТЕР ====================
    story.append(Spacer(1, 15*mm))
    footer_style = ParagraphStyle(
        'Footer', parent=styles['Normal'],
        fontName='Cyrillic', fontSize=8,
        alignment=TA_CENTER,
        textColor=colors.HexColor('#888888')
    )
    story.append(Paragraph(
        f'Создано: {datetime.now().strftime("%Y-%m-%d %H:%M")} | '
        f'Справочник типов цен 1C | '
        f'Poncho AI',
        footer_style
    ))

    doc.build(story)
    print(f'PDF создан: {output_path}')


def main():
    output_path = '/home/ilkoid/go-workspace/src/poncho-ai/reports/1c-price-types-reference.pdf'
    create_pdf(output_path)


if __name__ == '__main__':
    main()
