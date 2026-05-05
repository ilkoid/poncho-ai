package dashboard

import (
	"html/template"

	"github.com/go-echarts/go-echarts/v2/render"
)

// Snippeter — интерфейс для go-echarts чартов, которые могут вернуть ChartSnippet.
//
// Все чарты (Bar, Pie, HeatMap) наследуют RenderSnippet() от BaseConfiguration,
// которая embed'ит render.Renderer.
type Snippeter interface {
	RenderSnippet() render.ChartSnippet
}

// SnippetFromChart извлекает ChartSnippet из go-echarts чарта.
//
// Адаптирует render.ChartSnippet (string) → ChartSnippet (template.HTML)
// для безопасного встраивания в html/template.
func SnippetFromChart(c Snippeter) ChartSnippet {
	s := c.RenderSnippet()
	return ChartSnippet{
		Element: template.HTML(s.Element),
		Script:  template.HTML(s.Script),
		Option:  template.HTML(s.Option),
	}
}

// CollectScripts собирает все Script из секций в один slice.
func CollectScripts(sections []Section) []template.HTML {
	var scripts []template.HTML
	for i := range sections {
		for j := range sections[i].Snippets {
			scripts = append(scripts, sections[i].Snippets[j].Script)
		}
	}
	return scripts
}

// CollectOptions собирает все Option из секций в один slice.
func CollectOptions(sections []Section) []template.HTML {
	var options []template.HTML
	for i := range sections {
		for j := range sections[i].Snippets {
			options = append(options, sections[i].Snippets[j].Option)
		}
	}
	return options
}
