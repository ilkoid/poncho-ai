package main

import (
	"fmt"
	"strings"

	"github.com/go-echarts/go-echarts/v2/charts"
	"github.com/go-echarts/go-echarts/v2/event"
	"github.com/go-echarts/go-echarts/v2/opts"
	"github.com/go-echarts/go-echarts/v2/types"
	"github.com/ilkoid/poncho-ai/pkg/dashboard"
)

// clickHandler возвращает JS-обработчик клика, вызывающий drillFilter(key, params.name).
func clickHandler(key string) types.FuncStr {
	return opts.FuncOpts(fmt.Sprintf(
		`(params) => { if(params.name) drillFilter('%s', params.name); }`, key,
	))
}

// --- Line Chart: Risk Trends ---

func buildLineChart(dates []string, series map[string][]int, colors map[string]string) dashboard.ChartSnippet {
	line := charts.NewLine()
	line.SetGlobalOptions(
		charts.WithInitializationOpts(opts.Initialization{
			Width:  "100%",
			Height: "350px",
		}),
		charts.WithTooltipOpts(opts.Tooltip{Show: opts.Bool(true), Trigger: "axis"}),
		charts.WithLegendOpts(opts.Legend{Show: opts.Bool(true)}),
		charts.WithXAxisOpts(opts.XAxis{Type: "category"}),
		charts.WithYAxisOpts(opts.YAxis{Name: "SKU"}),
	)

	line.SetXAxis(dates)
	for name, values := range series {
		data := make([]opts.LineData, len(values))
		for i, v := range values {
			data[i] = opts.LineData{Value: v}
		}
		color := ""
		if c, ok := colors[name]; ok {
			color = c
		}
		line.AddSeries(name, data, charts.WithLineStyleOpts(opts.LineStyle{
			Color: color,
			Width: 2,
		}))
	}

	return dashboard.SnippetFromChart(line)
}

// --- Treemap: Brand -> Category ---

func buildTreemap(data []treemapRow) dashboard.ChartSnippet {
	tm := charts.NewTreeMap()
	tm.SetGlobalOptions(
		charts.WithInitializationOpts(opts.Initialization{
			Width:  "100%",
			Height: "400px",
		}),
		charts.WithTooltipOpts(opts.Tooltip{Show: opts.Bool(true)}),
		charts.WithTitleOpts(opts.Title{
			Title: "Риски по брендам и категориям",
		}),
	)

	// Build hierarchical data: brand -> categories
	brandMap := make(map[string][]treemapRow)
	for _, d := range data {
		brandMap[d.Brand] = append(brandMap[d.Brand], d)
	}

	var treeData []opts.TreeMapNode
	for brand, rows := range brandMap {
		children := make([]opts.TreeMapNode, len(rows))
		total := 0
		for i, r := range rows {
			children[i] = opts.TreeMapNode{
				Name:  r.Category,
				Value: r.Count,
			}
			total += r.Count
		}
		treeData = append(treeData, opts.TreeMapNode{
			Name:     brand,
			Value:    total,
			Children: children,
		})
	}

	tm.AddSeries("risks", treeData)

	return dashboard.SnippetFromChart(tm)
}

// --- Scatter: Stock vs Demand ---

func buildScatter(data []scatterRow) dashboard.ChartSnippet {
	sc := charts.NewScatter()
	sc.SetGlobalOptions(
		charts.WithInitializationOpts(opts.Initialization{
			Width:  "100%",
			Height: "400px",
		}),
		charts.WithTooltipOpts(opts.Tooltip{
			Show:    opts.Bool(true),
			Trigger: "item",
			Formatter: opts.FuncOpts(`(params) => {
				return params.data[3] + '<br/>Остаток: ' + params.data[0] + '<br/>MA-7: ' + params.data[1];
			}`),
		}),
		charts.WithXAxisOpts(opts.XAxis{Name: "Остаток"}),
		charts.WithYAxisOpts(opts.YAxis{Name: "MA-7 (спрос/день)"}),
		charts.WithVisualMapOpts(opts.VisualMap{
			Show:   opts.Bool(false),
			InRange: &opts.VisualMapInRange{Color: []string{"#5470c6", "#ee6666"}},
		}),
	)

	// Split by status for color coding
	statusColors := map[string]string{
		"critical": "#ee6666",
		"risk":     "#fac858",
		"oos":      "#fc8452",
		"ok":       "#91cc75",
	}

	byStatus := make(map[string][][]any)
	for _, d := range data {
		byStatus[d.Status] = append(byStatus[d.Status], []any{d.Stock, d.MA7, d.NmID, d.Name})
	}

	statusLabels := map[string]string{
		"critical": "Критичные",
		"risk":     "Риск",
		"oos":      "OOS",
		"ok":       "OK",
	}

	for status, points := range byStatus {
		items := make([]opts.ScatterData, len(points))
		for i, p := range points {
			items[i] = opts.ScatterData{Value: p}
		}
		label := statusLabels[status]
		sc.AddSeries(label, items, charts.WithItemStyleOpts(opts.ItemStyle{
			Color: statusColors[status],
		}))
	}

	// Add click handler to open WB product page
	sc.SetGlobalOptions(
		charts.WithEventListeners(event.Listener{
			EventName: "click",
			Handler: opts.FuncOpts(`(params) => {
				if (params.data && params.data[2]) {
					window.open('https://www.wildberries.ru/catalog/' + params.data[2] + '/detail.aspx', '_blank');
				}
			}`),
		}),
	)

	return dashboard.SnippetFromChart(sc)
}

// --- Heatmap ---

func buildHeatmap(data []regionCategoryRow, regions, categories []string) dashboard.ChartSnippet {
	hm := charts.NewHeatMap()
	hm.SetGlobalOptions(
		charts.WithInitializationOpts(opts.Initialization{
			Width:  "100%",
			Height: "500px",
		}),
		charts.WithGridOpts(opts.Grid{
			ContainLabel: opts.Bool(true),
			Right:        "5%",
		}),
		charts.WithTooltipOpts(opts.Tooltip{Show: opts.Bool(true)}),
		charts.WithXAxisOpts(opts.XAxis{
			Type: "category",
			Data: categories,
			AxisLabel: &opts.AxisLabel{
				Rotate: 45,
			},
		}),
		charts.WithVisualMapOpts(opts.VisualMap{
			Calculable: opts.Bool(true),
			Orient:     "vertical",
			Left:       "right",
			InRange:    &opts.VisualMapInRange{Color: []string{"#50a3ba", "#eac736", "#d94e5d"}},
		}),
		charts.WithYAxisOpts(opts.YAxis{
			Type: "category",
			Data: regions,
		}),
		charts.WithEventListeners(
			event.Listener{
				EventName: "click",
				Handler: opts.FuncOpts(fmt.Sprintf(
					`(params) => {
						if (!params.data || !params.data.value) return;
						var ci = params.data.value[0];
						var ri = params.data.value[1];
						var url = new URL(window.location);
						url.searchParams.set('category', %s[ci]);
						url.searchParams.set('region', %s[ri]);
						window.location = url.toString();
					}`,
					jsStringArray(categories), jsStringArray(regions),
				)),
			},
		),
	)

	regionIdx := make(map[string]int, len(regions))
	for i, r := range regions {
		regionIdx[r] = i
	}
	catIdx := make(map[string]int, len(categories))
	for i, c := range categories {
		catIdx[c] = i
	}

	items := make([]opts.HeatMapData, 0, len(data))
	for _, d := range data {
		items = append(items, opts.HeatMapData{
			Value: []int{catIdx[d.Category], regionIdx[d.Region], d.Count},
		})
	}

	hm.SetXAxis(categories)
	hm.AddSeries("risks", items)

	return dashboard.SnippetFromChart(hm)
}

// --- Stacked Bar ---

func buildStackedBar(filterKey string, xLabels []string, critical, atRisk, oos, grid []int) dashboard.ChartSnippet {
	bar := charts.NewBar()
	bar.SetGlobalOptions(
		charts.WithInitializationOpts(opts.Initialization{
			Width:  "100%",
			Height: "400px",
		}),
		charts.WithTooltipOpts(opts.Tooltip{Show: opts.Bool(true), Trigger: "axis"}),
		charts.WithLegendOpts(opts.Legend{Show: opts.Bool(true)}),
		charts.WithYAxisOpts(opts.YAxis{Name: "SKU"}),
		charts.WithEventListeners(
			event.Listener{
				EventName: "click",
				Handler:   clickHandler(filterKey),
			},
		),
	)

	bar.SetXAxis(xLabels)

	addIntSeries := func(name string, vals []int) {
		data := make([]opts.BarData, len(vals))
		for i, v := range vals {
			data[i] = opts.BarData{Value: v}
		}
		bar.AddSeries(name, data, charts.WithBarChartOpts(opts.BarChart{Stack: "total"}))
	}

	addIntSeries("Критичные", critical)
	addIntSeries("Риск", atRisk)
	addIntSeries("OOS", oos)
	addIntSeries("Сетка", grid)

	return dashboard.SnippetFromChart(bar)
}

// --- Donut ---

func buildDonut(data []deadStockRow) dashboard.ChartSnippet {
	pie := charts.NewPie()
	pie.SetGlobalOptions(
		charts.WithInitializationOpts(opts.Initialization{
			Width:  "100%",
			Height: "400px",
		}),
		charts.WithTooltipOpts(opts.Tooltip{Show: opts.Bool(true), Trigger: "item"}),
		charts.WithEventListeners(
			event.Listener{
				EventName: "click",
				Handler:   clickHandler("category"),
			},
		),
	)

	items := make([]opts.PieData, 0, len(data))
	for _, d := range data {
		items = append(items, opts.PieData{
			Name:  d.Category,
			Value: d.Count,
		})
	}

	pie.AddSeries("dead_stock", items,
		charts.WithPieChartOpts(opts.PieChart{
			Radius: []string{"40%", "75%"},
		}),
	)

	return dashboard.SnippetFromChart(pie)
}

// jsStringArray формирует JS-литерал массива из []string: ["A","B","C"].
func jsStringArray(items []string) string {
	escaped := make([]string, len(items))
	for i, s := range items {
		escaped[i] = fmt.Sprintf(`"%s"`, strings.ReplaceAll(s, `"`, `\"`))
	}
	return "[" + strings.Join(escaped, ",") + "]"
}
