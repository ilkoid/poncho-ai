package main

import (
	"github.com/go-echarts/go-echarts/v2/charts"
	"github.com/go-echarts/go-echarts/v2/opts"
	"github.com/ilkoid/poncho-ai/pkg/dashboard"
)

// --- Revenue Line Chart ---

func buildRevenueLine(dates []string, revenue []float64, _ []int) dashboard.ChartSnippet {
	line := charts.NewLine()
	line.SetGlobalOptions(
		charts.WithInitializationOpts(opts.Initialization{
			Width:  "100%",
			Height: "350px",
		}),
		charts.WithTooltipOpts(opts.Tooltip{Show: opts.Bool(true), Trigger: "axis"}),
		charts.WithLegendOpts(opts.Legend{Show: opts.Bool(true)}),
		charts.WithXAxisOpts(opts.XAxis{Type: "category"}),
		charts.WithYAxisOpts(opts.YAxis{Name: "Выручка (₽)"}),
	)

	line.SetXAxis(dates)

	revData := make([]opts.LineData, len(revenue))
	for i, v := range revenue {
		revData[i] = opts.LineData{Value: v}
	}
	line.AddSeries("Выручка", revData, charts.WithLineStyleOpts(opts.LineStyle{
		Color: "#5470c6", Width: 2,
	}))

	return dashboard.SnippetFromChart(line)
}

// --- Revenue Bar Chart ---

func buildRevenueBar(xLabels []string, values []float64, _ string) dashboard.ChartSnippet {
	bar := charts.NewBar()
	bar.SetGlobalOptions(
		charts.WithInitializationOpts(opts.Initialization{
			Width:  "100%",
			Height: "400px",
		}),
		charts.WithTooltipOpts(opts.Tooltip{Show: opts.Bool(true), Trigger: "axis"}),
		charts.WithXAxisOpts(opts.XAxis{Type: "category", AxisLabel: &opts.AxisLabel{Rotate: 30}}),
		charts.WithYAxisOpts(opts.YAxis{Name: "Выручка (₽)"}),
	)

	bar.SetXAxis(xLabels)
	data := make([]opts.BarData, len(values))
	for i, v := range values {
		data[i] = opts.BarData{Value: v}
	}
	bar.AddSeries("Выручка", data)

	return dashboard.SnippetFromChart(bar)
}

// --- Sales vs Risk Scatter ---

func buildSalesRiskScatter(data []salesRiskRow) dashboard.ChartSnippet {
	sc := charts.NewScatter()
	sc.SetGlobalOptions(
		charts.WithInitializationOpts(opts.Initialization{
			Width:  "100%",
			Height: "400px",
		}),
		charts.WithTooltipOpts(opts.Tooltip{
			Show:    opts.Bool(true),
			Trigger: "item",
		}),
		charts.WithXAxisOpts(opts.XAxis{Name: "Выручка за 7 дней (₽)"}),
		charts.WithYAxisOpts(opts.YAxis{Name: "MA-7 (спрос/день)"}),
	)

	items := make([]opts.ScatterData, len(data))
	for i, d := range data {
		items[i] = opts.ScatterData{Value: []any{d.Revenue7d, d.MA7, d.NmID, d.Brand + " " + d.Category}}
	}
	sc.AddSeries("SKU", items)

	return dashboard.SnippetFromChart(sc)
}
