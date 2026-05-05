package dashboard

import "html/template"

// SectionType определяет тип секции дашборда.
type SectionType string

const (
	SectionTypeKPI   SectionType = "kpi"
	SectionTypeChart SectionType = "chart"
	SectionTypeTable SectionType = "table"
)

// Section — одна визуальная секция дашборда.
//
// Тип определяет рендеринг: KPI-карточки, чарт или HTML-таблица.
type Section struct {
	ID    string
	Title string
	Type  SectionType
	Width string // "w-full", "w-1/2" и т.д.

	// Данные (заполняются в зависимости от Type):
	KPIs     []KPICard
	Snippets []ChartSnippet
	Table    *TableData

	// Pre-rendered HTML (заполняется в RenderPage):
	KPIsHTML  template.HTML
	TableHTML template.HTML
}

// ChartSnippet — адаптированный результат go-echarts RenderSnippet.
//
// Строковые поля go-echarts (render.ChartSnippet) конвертируются
// в template.HTML для безопасного встраивания в шаблон.
type ChartSnippet struct {
	Element template.HTML
	Script  template.HTML
	Option  template.HTML
}

// KPICard — карточка с одним ключевым показателем.
type KPICard struct {
	Label string
	Value string
	Badge string // "red", "yellow", "green", "" (no badge)
	Delta string // e.g. "+12% vs вчера", "−5 шт"
	Up    *bool  // true=green arrow ↑, false=red arrow ↓, nil=no arrow
}

// TableCell — ячейка таблицы с опциональным CSS-классом.
type TableCell struct {
	Text  string
	Class string // "text-red-600 font-bold", "text-yellow-600", ""
	Link  string // optional URL — renders as <a>
}

// TableData — данные для рендеринга HTML-таблицы.
type TableData struct {
	Headers []string
	Rows    [][]TableCell
}

// SimpleRow оборачивает []string в []TableCell (для backward compat).
func SimpleRow(cells ...string) []TableCell {
	tc := make([]TableCell, len(cells))
	for i, c := range cells {
		tc[i] = TableCell{Text: c}
	}
	return tc
}

// SimpleRows оборачивает [][]string в [][]TableCell.
func SimpleRows(rows [][]string) [][]TableCell {
	result := make([][]TableCell, len(rows))
	for i, row := range rows {
		result[i] = SimpleRow(row...)
	}
	return result
}

// FilterParams — параметры фильтрации из URL query.
type FilterParams struct {
	Date     string // snapshot_date (пусто = MAX)
	Brand    string // brand (пусто = все)
	Category string // category (пусто = все)
	Region   string // region_name (пусто = все)
	RiskOnly bool   // только critical/risk/OOS
	Tab      string // активная вкладка (пусто = первая)
}

// Tab — вкладка дашборда.
type Tab struct {
	ID      string
	Title   string
	Icon    string // emoji or empty
	Default bool
}

// DashboardPage — полная страница дашборда для шаблона.
type DashboardPage struct {
	Title       string
	Description string

	Filter     FilterParams
	Brands     []string
	Categories []string
	Regions    []string

	Tabs      []Tab
	ActiveTab string

	TabSections map[string][]Section
	TabScripts  map[string][]template.HTML
	TabOptions  map[string][]template.HTML

	// Deprecated: use TabSections instead
	Sections    []Section
	AllScripts  []template.HTML
	AllOptions  []template.HTML
}
