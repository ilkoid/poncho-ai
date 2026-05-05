package dashboard

import (
	"fmt"
	"html/template"
	"strings"
)

var badgeColors = map[string]string{
	"red":    "bg-red-100 text-red-800",
	"yellow": "bg-yellow-100 text-yellow-800",
	"green":  "bg-green-100 text-green-800",
}

// RenderKPICards рендерит ряд KPI-карточек с цветными badge и delta.
func RenderKPICards(cards []KPICard) template.HTML {
	if len(cards) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(`<div class="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-5 gap-4">`)

	for _, c := range cards {
		sb.WriteString(`<div class="bg-white rounded-lg shadow p-4">`)
		sb.WriteString(fmt.Sprintf(`<div class="text-sm text-gray-500">%s</div>`,
			template.HTMLEscapeString(c.Label)))
		sb.WriteString(fmt.Sprintf(`<div class="text-2xl font-bold text-gray-900 mt-1">%s</div>`,
			template.HTMLEscapeString(c.Value)))

		if c.Delta != "" {
			deltaClass := "text-gray-500"
			if c.Up != nil {
				if *c.Up {
					deltaClass = "text-green-600"
				} else {
					deltaClass = "text-red-600"
				}
			}
			sb.WriteString(fmt.Sprintf(`<div class="text-xs %s mt-0.5">%s</div>`,
				deltaClass, template.HTMLEscapeString(c.Delta)))
		}

		if c.Badge != "" {
			colorClass := badgeColors[c.Badge]
			if colorClass == "" {
				colorClass = "bg-gray-100 text-gray-800"
			}
			sb.WriteString(fmt.Sprintf(`<span class="inline-block mt-1 px-2 py-0.5 rounded text-xs font-medium %s">%s</span>`,
				colorClass, template.HTMLEscapeString(c.Badge)))
		}

		sb.WriteString(`</div>`)
	}

	sb.WriteString(`</div>`)
	return template.HTML(sb.String())
}
