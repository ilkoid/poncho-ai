package dashboard

import (
	"fmt"
	"html/template"
	"strings"
)

// RowClassFn возвращает CSS-класс для строки по её индексу и данным.
// Возвращает "" для default стиля.
type RowClassFn func(idx int, row []TableCell) string

// RenderTable рендерит TableData в HTML-таблицу с Tailwind-стилями.
//
// maxRows ограничивает количество отображаемых строк (0 = все).
func RenderTable(td *TableData, maxRows int) template.HTML {
	return RenderTableWithRowClass(td, maxRows, nil)
}

// RenderTableWithRowClass рендерит таблицу с кастомной подсветкой строк.
func RenderTableWithRowClass(td *TableData, maxRows int, rowClass RowClassFn) template.HTML {
	if td == nil || len(td.Headers) == 0 {
		return "<p class='text-gray-500'>Нет данных</p>"
	}

	rows := td.Rows
	if maxRows > 0 && len(rows) > maxRows {
		rows = rows[:maxRows]
	}

	var sb strings.Builder

	sb.WriteString(`<div class="overflow-x-auto"><table class="min-w-full text-sm">`)

	// Header
	sb.WriteString(`<thead class="bg-gray-50"><tr>`)
	for _, h := range td.Headers {
		sb.WriteString(fmt.Sprintf(`<th class="px-3 py-2 text-left font-medium text-gray-700 whitespace-nowrap">%s</th>`,
			template.HTMLEscapeString(h)))
	}
	sb.WriteString(`</tr></thead>`)

	// Body
	sb.WriteString(`<tbody class="divide-y divide-gray-200">`)
	for i, row := range rows {
		bg := ""
		if i%2 == 1 {
			bg = " bg-gray-50"
		}
		if rowClass != nil {
			if rc := rowClass(i, row); rc != "" {
				bg = " " + rc
			}
		}
		sb.WriteString(fmt.Sprintf(`<tr class="hover:bg-gray-100%s">`, bg))
		for _, cell := range row {
			class := ""
			if cell.Class != "" {
				class = " " + cell.Class
			}
			if cell.Link != "" {
				sb.WriteString(fmt.Sprintf(`<td class="px-3 py-2 text-gray-700 whitespace-nowrap%s"><a href="%s" target="_blank" class="text-blue-600 hover:text-blue-800 underline">%s</a></td>`,
					class, template.HTMLEscapeString(cell.Link), template.HTMLEscapeString(cell.Text)))
			} else {
				sb.WriteString(fmt.Sprintf(`<td class="px-3 py-2 text-gray-700 whitespace-nowrap%s">%s</td>`,
					class, template.HTMLEscapeString(cell.Text)))
			}
		}
		sb.WriteString(`</tr>`)
	}
	sb.WriteString(`</tbody></table></div>`)

	if maxRows > 0 && len(td.Rows) > maxRows {
		sb.WriteString(fmt.Sprintf(`<p class="text-xs text-gray-400 mt-1">Показано %d из %d строк</p>`, maxRows, len(td.Rows)))
	}

	return template.HTML(sb.String())
}
