package main

import (
	"github.com/go-echarts/go-echarts/v2/charts"
	"github.com/go-echarts/go-echarts/v2/opts"
	"github.com/ilkoid/poncho-ai/pkg/dashboard"
)

// --- Warehouse Stock Bar ---

func buildWarehouseBar(labels []string, stock []int, products []int) dashboard.ChartSnippet {
	bar := charts.NewBar()
	bar.SetGlobalOptions(
		charts.WithInitializationOpts(opts.Initialization{
			Width:  "100%",
			Height: "400px",
		}),
		charts.WithTooltipOpts(opts.Tooltip{Show: opts.Bool(true), Trigger: "axis"}),
		charts.WithLegendOpts(opts.Legend{Show: opts.Bool(true)}),
		charts.WithXAxisOpts(opts.XAxis{Type: "category", AxisLabel: &opts.AxisLabel{Rotate: 30}}),
		charts.WithYAxisOpts(opts.YAxis{Name: "Штук"}),
	)

	bar.SetXAxis(labels)

	stockData := make([]opts.BarData, len(stock))
	for i, v := range stock {
		stockData[i] = opts.BarData{Value: v}
	}
	prodData := make([]opts.BarData, len(products))
	for i, v := range products {
		prodData[i] = opts.BarData{Value: v}
	}

	bar.AddSeries("Остаток", stockData)
	bar.AddSeries("Товаров", prodData)

	return dashboard.SnippetFromChart(bar)
}

// --- Stock Trend Line ---

func buildStockTrendLine(dates []string, stock []int, transit []int) dashboard.ChartSnippet {
	line := charts.NewLine()
	line.SetGlobalOptions(
		charts.WithInitializationOpts(opts.Initialization{
			Width:  "100%",
			Height: "350px",
		}),
		charts.WithTooltipOpts(opts.Tooltip{Show: opts.Bool(true), Trigger: "axis"}),
		charts.WithLegendOpts(opts.Legend{Show: opts.Bool(true)}),
		charts.WithXAxisOpts(opts.XAxis{Type: "category"}),
		charts.WithYAxisOpts(opts.YAxis{Name: "Штук"}),
	)

	line.SetXAxis(dates)

	stockData := make([]opts.LineData, len(stock))
	for i, v := range stock {
		stockData[i] = opts.LineData{Value: v}
	}
	transitData := make([]opts.LineData, len(transit))
	for i, v := range transit {
		transitData[i] = opts.LineData{Value: v}
	}

	line.AddSeries("Остаток", stockData, charts.WithLineStyleOpts(opts.LineStyle{Color: "#5470c6", Width: 2}))
	line.AddSeries("В пути", transitData, charts.WithLineStyleOpts(opts.LineStyle{Color: "#91cc75", Width: 2}))

	return dashboard.SnippetFromChart(line)
}

// --- Fill Pct Horizontal Bar ---

func buildFillPctBar(categories []string, values []float64) dashboard.ChartSnippet {
	bar := charts.NewBar()
	bar.SetGlobalOptions(
		charts.WithInitializationOpts(opts.Initialization{
			Width:  "100%",
			Height: "350px",
		}),
		charts.WithTooltipOpts(opts.Tooltip{Show: opts.Bool(true), Trigger: "axis"}),
		charts.WithXAxisOpts(opts.XAxis{Type: "value", Max: 100}),
		charts.WithYAxisOpts(opts.YAxis{Type: "category"}),
	)

	bar.SetXAxis(categories)

	data := make([]opts.BarData, len(values))
	for i, v := range values {
		data[i] = opts.BarData{Value: v}
	}
	bar.AddSeries("Заполненность %", data)

	return dashboard.SnippetFromChart(bar)
}
