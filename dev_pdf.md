# PDF Documentation Guide

This guide explains how to create PDF documents in the Poncho AI project to maintain consistency across all documentation.

---

## Quick Start

```bash
# 1. Activate the PDF generation environment (already set up)
/tmp/pdf_venv/bin/python reports/database_tables_pdf.py

# 2. Find your PDF
ls -lh reports/database_tables.pdf
```

---

## Tools & Libraries

| Tool | Purpose | Version |
|------|---------|---------|
| **Python** | Scripting language | 3.12+ |
| **reportlab** | PDF generation library | Latest |
| **DejaVu Sans** | Unicode TTF font (Cyrillic support) | System |

### Why reportlab?

- Built-in Unicode support (no manual font encoding)
- Powerful Table API with automatic text wrapping
- `Paragraph` objects for rich text formatting
- `SimpleDocTemplate` for automatic page layout
- Professional styling with `TableStyle`

---

## Project Conventions

### File Location

All PDF scripts go into `reports/` directory:

```
reports/
├── database_tables_pdf.py      # Script
└── database_tables.pdf         # Generated output
```

### Naming Convention

- Script: `{name}_pdf.py`
- Output: `{name}.pdf`
- Example: `database_tables_pdf.py` → `database_tables.pdf`

---

## Template Structure

```python
#!/usr/bin/env python3
"""
PDF generator for [purpose].
Uses reportlab for Unicode support.
"""

from reportlab.lib.pagesizes import A4, landscape
from reportlab.lib import colors
from reportlab.platypus import SimpleDocTemplate, Table, TableStyle, Paragraph, Spacer
from reportlab.lib.styles import getSampleStyleSheet, ParagraphStyle
from reportlab.lib.units import mm
from reportlab.pdfbase import pdfmetrics
from reportlab.pdfbase.ttfonts import TTFont
from reportlab.lib.enums import TA_CENTER, TA_LEFT
from datetime import datetime
import os


def create_pdf(output_path):
    """Generate PDF document."""

    # Register DejaVu Sans font (Cyrillic support)
    dejavu_path = '/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf'
    pdfmetrics.registerFont(TTFont('Cyrillic', dejavu_path))

    # Document setup
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

    title_style = ParagraphStyle(
        'CustomTitle',
        parent=styles['Heading1'],
        fontName='Cyrillic',
        fontSize=20,
        alignment=TA_CENTER,
        spaceAfter=10*mm
    )

    heading_style = ParagraphStyle(
        'CustomHeading',
        parent=styles['Heading2'],
        fontName='Cyrillic',
        fontSize=12,
        spaceAfter=5*mm,
        textColor=colors.HexColor('#1a1a6e')
    )

    normal_style = ParagraphStyle(
        'CustomNormal',
        parent=styles['Normal'],
        fontName='Cyrillic',
        fontSize=10,
        spaceAfter=3*mm
    )

    table_cell_style = ParagraphStyle(
        'TableCell',
        parent=styles['Normal'],
        fontName='Cyrillic',
        fontSize=7,
        wordWrap='CJK'
    )

    # Build content
    story = []

    # Title
    story.append(Paragraph('Document Title', title_style))

    # Add tables with wrapped text
    headers = ['Column 1', 'Column 2', 'Column 3']
    data = [headers]
    for row in source_data:
        data.append([Paragraph(str(cell), table_cell_style) for cell in row])

    table = Table(data, colWidths=[60*mm, 60*mm, 60*mm], repeatRows=1)
    table.setStyle(TableStyle([
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
    story.append(table)

    # Generate PDF
    doc.build(story)
    print(f'PDF created: {output_path}')


def main():
    output_path = '/home/ilkoid/go-workspace/src/poncho-ai/reports/your_document.pdf'
    create_pdf(output_path)


if __name__ == '__main__':
    main()
```

---

## Style Guidelines

### Language
- **Primary:** Russian (for project documentation)
- **Secondary:** English (for code/technical terms)

### Typography

| Element | Font | Size | Usage |
|---------|------|------|-------|
| Title | Cyrillic | 20pt | Main document title |
| Heading | Cyrillic | 12pt | Section headers |
| Body | Cyrillic | 10pt | Regular text |
| Table Header | Cyrillic | 8pt | Column headers |
| Table Cell | Cyrillic | 7pt | Table content |

### Colors

| Purpose | Color |
|---------|-------|
| Headings | `#1a1a6e` (dark blue) |
| Table Header Background | `colors.lightgrey` |
| Category Header Background | `colors.grey` |
| Grid Lines | `colors.black` |

### Layout

| Setting | Value |
|---------|-------|
| Page Size | A4 Landscape |
| Margins | 10mm (sides), 15mm (top), 10mm (bottom) |
| Column Spacing | 3mm padding |

---

## Common Patterns

### 1. Table with Wrapped Text

```python
# Wrap cell content in Paragraph for auto-wrapping
table_cell_style = ParagraphStyle('TableCell', fontName='Cyrillic', fontSize=7)

data = [['Header1', 'Header2']]
for row in rows:
    data.append([Paragraph(cell, table_cell_style) for cell in row])

table = Table(data, colWidths=[50*mm, 100*mm])
```

### 2. Section with Header

```python
story.append(Paragraph('Section Name', heading_style))
story.append(Spacer(1, 3*mm))
# Add content...
```

### 3. Page Break

```python
from reportlab.platypus import PageBreak
story.append(PageBreak())
```

### 4. Two-Column Summary Table

```python
summary_data = [['Total Items:', '42'], ['Status:', 'Complete']]
summary_table = Table(summary_data, colWidths=[80*mm, 100*mm])
summary_table.setStyle(TableStyle([
    ('FONTNAME', (0, 0), (-1, -1), 'Cyrillic'),
    ('FONTSIZE', (0, 0), (-1, -1), 10),
]))
```

---

## Virtual Environment Setup

The PDF generation environment is already set up at `/tmp/pdf_venv/`.

To recreate if needed:

```bash
python3 -m venv /tmp/pdf_venv
/tmp/pdf_venv/bin/pip install reportlab
```

---

## Best Practices

1. **Always use `Paragraph` for table cells** - enables text wrapping
2. **Use `mm` units** - more intuitive than points
3. **Set `repeatRows=1`** for table headers on each page
4. **Use `VALIGN, 'TOP'`** for consistent text alignment
5. **Include padding** - tables look crowded without it
6. **Use `wordWrap='CJK'`** - better wrapping for long URLs/words
7. **Test with real data** - some content may be longer than expected
8. **Keep column widths balanced** - avoid extreme narrow/wide columns

---

## Troubleshooting

### Cyrillic shows as squares
```python
# Ensure font is registered BEFORE creating styles
pdfmetrics.registerFont(TTFont('Cyrillic', '/path/to/DejaVuSans.ttf'))
```

### Text overflow in columns
```python
# Use Paragraph for auto-wrapping
data.append([Paragraph(long_text, table_cell_style)])
```

### Table too wide for page
```python
# Reduce column widths or switch to portrait
pagesize=portrait(A4)  # instead of landscape(A4)
```

---

## References

- **reportlab docs:** https://reportlab.com/docs/
- **Existing example:** `reports/database_tables_pdf.py`
- **DejaVu fonts:** https://dejavu-fonts.github.io/

---

*Last updated: 2026-04-09*
