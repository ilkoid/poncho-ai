package dashboard

import (
	"bytes"
	"html/template"
)

// baseTmpl — HTML-шаблон дашборда с Tailwind CDN, ECharts CDN, табами и формой фильтров.
var baseTmpl = template.Must(template.New("dashboard").Parse(`<!DOCTYPE html>
<html lang="ru">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>{{.Title}}</title>
    <script src="https://cdn.tailwindcss.com"></script>
    <script src="https://cdn.jsdelivr.net/npm/echarts@5/dist/echarts.min.js"></script>
    <style>
        .chart-container { position: relative; }
        .chart-container > div { width: 100% !important; }
        .tab-btn { transition: all 0.15s ease; }
        .tab-btn.active { border-bottom: 2px solid #2563eb; color: #1d4ed8; font-weight: 600; }
        .tab-content { display: none; }
        .tab-content.active { display: block; }
    </style>
</head>
<body class="bg-gray-50 min-h-screen">

<!-- Header -->
<header class="bg-white shadow-sm border-b border-gray-200">
    <div class="max-w-7xl mx-auto px-4 py-4">
        <h1 class="text-2xl font-bold text-gray-900">{{.Title}}</h1>
        {{if .Description}}<p class="text-sm text-gray-500 mt-1">{{.Description}}</p>{{end}}

        <!-- Tabs -->
        {{if .Tabs}}
        <nav class="mt-4 flex gap-1 border-b border-gray-200 -mb-px">
            {{range .Tabs}}
            <button class="tab-btn px-4 py-2 text-sm text-gray-600 hover:text-gray-900 {{if eq .ID $.ActiveTab}}active{{end}}"
                    onclick="switchTab('{{.ID}}')">
                {{if .Icon}}{{.Icon}} {{end}}{{.Title}}
            </button>
            {{end}}
        </nav>
        {{end}}

        <!-- Filter form -->
        <form method="GET" class="mt-4 flex flex-wrap gap-3 items-end">
            <input type="hidden" name="tab" id="tab-input" value="{{.ActiveTab}}">
            <div>
                <label class="block text-xs text-gray-500 mb-1">Дата</label>
                <input type="date" name="date" value="{{.Filter.Date}}"
                    class="border rounded px-2 py-1 text-sm">
            </div>
            <div>
                <label class="block text-xs text-gray-500 mb-1">Бренд</label>
                <select name="brand" class="border rounded px-2 py-1 text-sm">
                    <option value="">Все бренды</option>
                    {{range .Brands}}
                    <option value="{{.}}" {{if eq $.Filter.Brand .}}selected{{end}}>{{.}}</option>
                    {{end}}
                </select>
            </div>
            <div>
                <label class="block text-xs text-gray-500 mb-1">Категория</label>
                <select name="category" class="border rounded px-2 py-1 text-sm">
                    <option value="">Все категории</option>
                    {{range .Categories}}
                    <option value="{{.}}" {{if eq $.Filter.Category .}}selected{{end}}>{{.}}</option>
                    {{end}}
                </select>
            </div>
            <div>
                <label class="block text-xs text-gray-500 mb-1">Регион</label>
                <select name="region" class="border rounded px-2 py-1 text-sm">
                    <option value="">Все регионы</option>
                    {{range .Regions}}
                    <option value="{{.}}" {{if eq $.Filter.Region .}}selected{{end}}>{{.}}</option>
                    {{end}}
                </select>
            </div>
            <div class="flex items-center gap-1 pb-1">
                <input type="checkbox" name="risk_only" value="1" id="risk_only"
                    {{if .Filter.RiskOnly}}checked{{end}}
                    class="rounded border-gray-300">
                <label for="risk_only" class="text-sm text-gray-600">Только проблемы</label>
            </div>
            <button type="submit"
                class="bg-blue-600 text-white px-4 py-1 rounded text-sm hover:bg-blue-700">
                Применить
            </button>
            <a href="?" class="text-sm text-gray-500 hover:text-gray-700 pb-1">Сбросить</a>
        </form>
    </div>
</header>

<!-- Content -->
{{if .Tabs}}
{{range .Tabs}}
<div id="tab-{{.ID}}" class="tab-content {{if eq .ID $.ActiveTab}}active{{end}}">
    <main class="max-w-7xl mx-auto px-4 py-6 space-y-6">
        {{range index $.TabSections .ID}}
        <section id="{{.ID}}" class="{{.Width}}">
            <h2 class="text-lg font-semibold text-gray-800 mb-3">{{.Title}}</h2>

            {{if eq .Type "kpi"}}
            {{.KPIsHTML}}

            {{else if eq .Type "chart"}}
            <div class="grid grid-cols-1 gap-4">
                {{range .Snippets}}
                <div class="bg-white rounded-lg shadow p-4 chart-container">
                    {{.Element}}
                </div>
                {{end}}
            </div>

            {{else if eq .Type "table"}}
            <div class="bg-white rounded-lg shadow p-4">
                {{.TableHTML}}
            </div>
            {{end}}
        </section>
        {{end}}
    </main>
</div>
{{end}}
{{else}}
<!-- Legacy: no tabs, flat sections -->
<main class="max-w-7xl mx-auto px-4 py-6 space-y-6">
    {{range .Sections}}
    <section id="{{.ID}}" class="{{.Width}}">
        <h2 class="text-lg font-semibold text-gray-800 mb-3">{{.Title}}</h2>

        {{if eq .Type "kpi"}}
        {{.KPIsHTML}}

        {{else if eq .Type "chart"}}
        <div class="grid grid-cols-1 gap-4">
            {{range .Snippets}}
            <div class="bg-white rounded-lg shadow p-4 chart-container">
                {{.Element}}
            </div>
            {{end}}
        </div>

        {{else if eq .Type "table"}}
        <div class="bg-white rounded-lg shadow p-4">
            {{.TableHTML}}
        </div>
        {{end}}
    </section>
    {{end}}
</main>
{{end}}

<!-- Tab switching + drill-down -->
<script>
function switchTab(tabId) {
    document.querySelectorAll('.tab-content').forEach(el => el.classList.remove('active'));
    document.querySelectorAll('.tab-btn').forEach(el => el.classList.remove('active'));
    document.getElementById('tab-' + tabId).classList.add('active');
    document.querySelectorAll('.tab-btn').forEach(btn => {
        if (btn.getAttribute('onclick').includes("'" + tabId + "'")) {
            btn.classList.add('active');
        }
    });
    document.getElementById('tab-input').value = tabId;
    // Resize charts after tab switch
    setTimeout(() => window.dispatchEvent(new Event('resize')), 50);
}

function drillFilter(key, value) {
    const url = new URL(window.location);
    url.searchParams.set(key, value);
    window.location = url.toString();
}

// Initialize from URL param
(function() {
    const params = new URLSearchParams(window.location.search);
    const tab = params.get('tab');
    if (tab) switchTab(tab);
})();
</script>

<!-- ECharts init (per tab) -->
{{if .Tabs}}
{{range $tabId, $scripts := .TabScripts}}
{{$options := index $.TabOptions $tabId}}
{{range $i, $s := $scripts}}{{$s}}
{{end}}
{{end}}
{{else}}
{{range .AllScripts}}{{.}}
{{end}}
{{end}}

</body>
</html>`))

// RenderPage рендерит DashboardPage в HTML.
//
// Предварительно рендерит KPI и таблицы в HTML, затем собирает
// все ECharts scripts/options и применяет базовый шаблон.
func RenderPage(page *DashboardPage) ([]byte, error) {
	if len(page.Tabs) > 0 {
		return renderTabbedPage(page)
	}
	return renderFlatPage(page)
}

// renderTabbedPage рендерит страницу с табами.
func renderTabbedPage(page *DashboardPage) ([]byte, error) {
	if page.TabSections == nil {
		page.TabSections = make(map[string][]Section)
	}
	if page.TabScripts == nil {
		page.TabScripts = make(map[string][]template.HTML)
	}
	if page.TabOptions == nil {
		page.TabOptions = make(map[string][]template.HTML)
	}

	for tabID, sections := range page.TabSections {
		for i := range sections {
			s := &sections[i]
			switch s.Type {
			case SectionTypeKPI:
				s.KPIsHTML = RenderKPICards(s.KPIs)
			case SectionTypeTable:
				s.TableHTML = RenderTable(s.Table, 0)
			}
		}
		page.TabSections[tabID] = sections
		page.TabScripts[tabID] = CollectScripts(sections)
		page.TabOptions[tabID] = CollectOptions(sections)
	}

	var buf bytes.Buffer
	if err := baseTmpl.Execute(&buf, page); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// renderFlatPage рендерит страницу без табов (legacy).
func renderFlatPage(page *DashboardPage) ([]byte, error) {
	for i := range page.Sections {
		s := &page.Sections[i]
		switch s.Type {
		case SectionTypeKPI:
			s.KPIsHTML = RenderKPICards(s.KPIs)
		case SectionTypeTable:
			s.TableHTML = RenderTable(s.Table, 0)
		}
	}

	page.AllScripts = CollectScripts(page.Sections)
	page.AllOptions = CollectOptions(page.Sections)

	var buf bytes.Buffer
	if err := baseTmpl.Execute(&buf, page); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
