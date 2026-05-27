package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"

	"github.com/xuri/excelize/v2"
	_ "github.com/mattn/go-sqlite3"
)

// Result types for storage.

type SKURanking struct {
	NmID       int
	VendorCode string
	Revenue    float64
	Units      int
	AvgPrice   float64
	CumPct     float64
	ParetoTier string
}

type DistributionRow struct {
	SKUCount    int
	TotalRev    float64
	TotalUnits  int
	Gini        float64
	Top10Share  float64
	Top20Share  float64
	MedianRev   float64
	MeanRev     float64
	DeadSKUCount int
}

type PriceBracketRow struct {
	Bracket    string
	SKUCount   int
	Revenue    float64
	Units      int
	RevenuePct float64
}

type VelocityRow struct {
	NmID        int
	VendorCode  string
	Units       int
	UnitsPerDay float64
	Class       string
}

type TrendRow struct {
	NmID      int
	VendorCode string
	CurrRev   float64
	PrevRev   float64
	ChangePct float64
	Trend     string
}

type DeadCatalogRow struct {
	SubjectName string
	SKUCount    int
}

// categorySchema defines all 6 result tables in category-sales.db.
const categorySchema = `
DROP TABLE IF EXISTS category_sku_ranking;
CREATE TABLE category_sku_ranking (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    subject_name    TEXT NOT NULL,
    nm_id           INTEGER NOT NULL,
    vendor_code     TEXT DEFAULT '',
    total_revenue   REAL DEFAULT 0,
    total_units     INTEGER DEFAULT 0,
    avg_price       REAL DEFAULT 0,
    cum_revenue_pct REAL DEFAULT 0,
    pareto_tier     TEXT DEFAULT '',
    analyzed_at     TEXT DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(subject_name, nm_id)
);

DROP TABLE IF EXISTS category_distribution;
CREATE TABLE category_distribution (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    subject_name    TEXT NOT NULL,
    sku_count       INTEGER DEFAULT 0,
    total_revenue   REAL DEFAULT 0,
    total_units     INTEGER DEFAULT 0,
    gini            REAL DEFAULT 0,
    top10_share     REAL DEFAULT 0,
    top20_share     REAL DEFAULT 0,
    median_rev      REAL DEFAULT 0,
    mean_rev        REAL DEFAULT 0,
    dead_sku_count  INTEGER DEFAULT 0,
    analyzed_at     TEXT DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(subject_name)
);

DROP TABLE IF EXISTS category_price_brackets;
CREATE TABLE category_price_brackets (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    subject_name    TEXT NOT NULL,
    bracket         TEXT NOT NULL,
    sku_count       INTEGER DEFAULT 0,
    total_revenue   REAL DEFAULT 0,
    total_units     INTEGER DEFAULT 0,
    revenue_pct     REAL DEFAULT 0,
    analyzed_at     TEXT DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(subject_name, bracket)
);

DROP TABLE IF EXISTS category_velocity;
CREATE TABLE category_velocity (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    subject_name    TEXT NOT NULL,
    nm_id           INTEGER NOT NULL,
    vendor_code     TEXT DEFAULT '',
    total_units     INTEGER DEFAULT 0,
    units_per_day   REAL DEFAULT 0,
    velocity_class  TEXT DEFAULT '',
    analyzed_at     TEXT DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(subject_name, nm_id)
);

DROP TABLE IF EXISTS category_trend;
CREATE TABLE category_trend (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    subject_name    TEXT NOT NULL,
    nm_id           INTEGER NOT NULL,
    vendor_code     TEXT DEFAULT '',
    curr_revenue    REAL DEFAULT 0,
    prev_revenue    REAL DEFAULT 0,
    change_pct      REAL DEFAULT 0,
    trend           TEXT DEFAULT '',
    analyzed_at     TEXT DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(subject_name, nm_id)
);

DROP TABLE IF EXISTS category_dead_catalog;
CREATE TABLE category_dead_catalog (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    subject_name    TEXT NOT NULL,
    sku_count       INTEGER DEFAULT 0,
    analyzed_at     TEXT DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(subject_name)
);
`

// ResultsRepo manages the category-sales.db — stores analysis results.
type ResultsRepo struct {
	db *sql.DB
}

// NewResultsRepo opens/creates category-sales.db with WAL mode and creates schema.
func NewResultsRepo(dbPath string) (*ResultsRepo, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open results db: %w", err)
	}

	for _, p := range []struct{ key, val string }{
		{"journal_mode", "WAL"},
		{"synchronous", "NORMAL"},
		{"cache_size", "-64000"},
		{"temp_store", "MEMORY"},
	} {
		if _, err := db.Exec(fmt.Sprintf("PRAGMA %s = %s", p.key, p.val)); err != nil {
			db.Close()
			return nil, fmt.Errorf("PRAGMA %s: %w", p.key, err)
		}
	}

	if _, err := db.Exec(categorySchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}

	return &ResultsRepo{db: db}, nil
}

// Close closes the results database.
func (r *ResultsRepo) Close() error {
	return r.db.Close()
}

// SaveSKURankings saves Pareto-ranked SKU rows.
func (r *ResultsRepo) SaveSKURankings(ctx context.Context, rows []SKURanking, subjectName string) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR REPLACE INTO category_sku_ranking
			(subject_name, nm_id, vendor_code, total_revenue, total_units, avg_price, cum_revenue_pct, pareto_tier, analyzed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare: %w", err)
	}
	defer stmt.Close()

	now := time.Now().Format(time.RFC3339)
	for _, row := range rows {
		if _, err := stmt.ExecContext(ctx, subjectName, row.NmID, row.VendorCode,
			row.Revenue, row.Units, row.AvgPrice, row.CumPct, row.ParetoTier, now); err != nil {
			return fmt.Errorf("insert sku ranking nm_id=%d: %w", row.NmID, err)
		}
	}
	return tx.Commit()
}

// SaveDistribution saves the category-level summary.
func (r *ResultsRepo) SaveDistribution(ctx context.Context, d DistributionRow, subjectName string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO category_distribution
			(subject_name, sku_count, total_revenue, total_units, gini, top10_share, top20_share, median_rev, mean_rev, dead_sku_count, analyzed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		subjectName, d.SKUCount, d.TotalRev, d.TotalUnits, d.Gini, d.Top10Share, d.Top20Share,
		d.MedianRev, d.MeanRev, d.DeadSKUCount, time.Now().Format(time.RFC3339))
	return err
}

// SavePriceBrackets saves price segmentation rows.
func (r *ResultsRepo) SavePriceBrackets(ctx context.Context, rows []PriceBracketRow, subjectName string) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR REPLACE INTO category_price_brackets
			(subject_name, bracket, sku_count, total_revenue, total_units, revenue_pct, analyzed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare: %w", err)
	}
	defer stmt.Close()

	now := time.Now().Format(time.RFC3339)
	for _, row := range rows {
		if _, err := stmt.ExecContext(ctx, subjectName, row.Bracket, row.SKUCount,
			row.Revenue, row.Units, row.RevenuePct, now); err != nil {
			return fmt.Errorf("insert bracket %s: %w", row.Bracket, err)
		}
	}
	return tx.Commit()
}

// SaveVelocity saves per-SKU velocity classification.
func (r *ResultsRepo) SaveVelocity(ctx context.Context, rows []VelocityRow, subjectName string) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR REPLACE INTO category_velocity
			(subject_name, nm_id, vendor_code, total_units, units_per_day, velocity_class, analyzed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare: %w", err)
	}
	defer stmt.Close()

	now := time.Now().Format(time.RFC3339)
	for _, row := range rows {
		if _, err := stmt.ExecContext(ctx, subjectName, row.NmID, row.VendorCode,
			row.Units, row.UnitsPerDay, row.Class, now); err != nil {
			return fmt.Errorf("insert velocity nm_id=%d: %w", row.NmID, err)
		}
	}
	return tx.Commit()
}

// SaveTrend saves per-SKU period-over-period trend.
func (r *ResultsRepo) SaveTrend(ctx context.Context, rows []TrendRow, subjectName string) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR REPLACE INTO category_trend
			(subject_name, nm_id, vendor_code, curr_revenue, prev_revenue, change_pct, trend, analyzed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare: %w", err)
	}
	defer stmt.Close()

	now := time.Now().Format(time.RFC3339)
	for _, row := range rows {
		if _, err := stmt.ExecContext(ctx, subjectName, row.NmID, row.VendorCode,
			row.CurrRev, row.PrevRev, row.ChangePct, row.Trend, now); err != nil {
			return fmt.Errorf("insert trend nm_id=%d: %w", row.NmID, err)
		}
	}
	return tx.Commit()
}

// SaveDeadCatalog saves categories with zero sales.
func (r *ResultsRepo) SaveDeadCatalog(ctx context.Context, rows []DeadCatalogRow) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR REPLACE INTO category_dead_catalog (subject_name, sku_count, analyzed_at)
		VALUES (?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare: %w", err)
	}
	defer stmt.Close()

	now := time.Now().Format(time.RFC3339)
	for _, row := range rows {
		if _, err := stmt.ExecContext(ctx, row.SubjectName, row.SKUCount, now); err != nil {
			return fmt.Errorf("insert dead catalog %s: %w", row.SubjectName, err)
		}
	}
	return tx.Commit()
}

// ExportCSV writes all result tables to a single CSV file.
func (r *ResultsRepo) ExportCSV(ctx context.Context, csvPath string) error {
	f, err := os.Create(csvPath)
	if err != nil {
		return fmt.Errorf("create csv: %w", err)
	}
	defer f.Close()

	tables := []struct {
		name string
		sql  string
		cols []string
	}{
		{
			"category_distribution",
			`SELECT subject_name, sku_count, total_revenue, total_units, gini, top10_share, top20_share, median_rev, mean_rev, dead_sku_count FROM category_distribution ORDER BY total_revenue DESC`,
			[]string{"subject_name", "sku_count", "total_revenue", "total_units", "gini", "top10_share", "top20_share", "median_rev", "mean_rev", "dead_sku_count"},
		},
		{
			"category_sku_ranking",
			`SELECT subject_name, nm_id, vendor_code, total_revenue, total_units, avg_price, cum_revenue_pct, pareto_tier FROM category_sku_ranking ORDER BY subject_name, total_revenue DESC`,
			[]string{"subject_name", "nm_id", "vendor_code", "total_revenue", "total_units", "avg_price", "cum_revenue_pct", "pareto_tier"},
		},
		{
			"category_velocity",
			`SELECT subject_name, nm_id, vendor_code, total_units, units_per_day, velocity_class FROM category_velocity ORDER BY subject_name, units_per_day DESC`,
			[]string{"subject_name", "nm_id", "vendor_code", "total_units", "units_per_day", "velocity_class"},
		},
		{
			"category_trend",
			`SELECT subject_name, nm_id, vendor_code, curr_revenue, prev_revenue, change_pct, trend FROM category_trend ORDER BY subject_name, change_pct DESC`,
			[]string{"subject_name", "nm_id", "vendor_code", "curr_revenue", "prev_revenue", "change_pct", "trend"},
		},
		{
			"category_price_brackets",
			`SELECT subject_name, bracket, sku_count, total_revenue, total_units, revenue_pct FROM category_price_brackets ORDER BY subject_name, bracket`,
			[]string{"subject_name", "bracket", "sku_count", "total_revenue", "total_units", "revenue_pct"},
		},
		{
			"category_dead_catalog",
			`SELECT subject_name, sku_count FROM category_dead_catalog ORDER BY sku_count DESC`,
			[]string{"subject_name", "sku_count"},
		},
	}

	for _, t := range tables {
		fmt.Fprintf(f, "# %s\n", t.name)

		// header
		for i, c := range t.cols {
			if i > 0 {
				f.WriteString(",")
			}
			f.WriteString(c)
		}
		f.WriteString("\n")

		rows, err := r.db.QueryContext(ctx, t.sql)
		if err != nil {
			return fmt.Errorf("query %s for csv: %w", t.name, err)
		}

		cols, _ := rows.Columns()
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}

		for rows.Next() {
			if err := rows.Scan(ptrs...); err != nil {
				rows.Close()
				return fmt.Errorf("scan %s csv row: %w", t.name, err)
			}
			for i, v := range vals {
				if i > 0 {
					f.WriteString(",")
				}
				switch val := v.(type) {
				case float64:
					fmt.Fprintf(f, "%.2f", val)
				case int64:
					fmt.Fprintf(f, "%d", val)
				case string:
					f.WriteString(val)
				case []byte:
					f.WriteString(string(val))
				default:
					fmt.Fprintf(f, "%v", val)
				}
			}
			f.WriteString("\n")
		}
		rows.Close()
		f.WriteString("\n")
	}
	return nil
}

// ExportXLSX creates an Excel file with one sheet per category.
func (r *ResultsRepo) ExportXLSX(ctx context.Context, xlsxPath string) error {
	f := excelize.NewFile()
	sheet := "Сводка"
	f.SetSheetName("Sheet1", sheet)

	headerStyle, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "FFFFFF"},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"#4472C4"}},
		Alignment: &excelize.Alignment{Horizontal: "center"},
	})
	sectionStyle, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true, Size: 12},
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"#D9E2F3"}},
	})
	tierStyles := map[string]int{
		"A": xMustStyle(f, &excelize.Style{Font: &excelize.Font{Bold: true, Color: "006100"}, Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"#C6EFCE"}}}),
		"B": xMustStyle(f, &excelize.Style{Font: &excelize.Font{Color: "9C5700"}, Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"#FFEB9C"}}}),
		"C": xMustStyle(f, &excelize.Style{Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"#FFF2CC"}}}),
		"D": xMustStyle(f, &excelize.Style{Font: &excelize.Font{Color: "9C0006"}, Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"#FFC7CE"}}}),
	}
	velStyles := map[string]int{
		"hot":  xMustStyle(f, &excelize.Style{Font: &excelize.Font{Bold: true, Color: "006100"}, Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"#C6EFCE"}}}),
		"warm": xMustStyle(f, &excelize.Style{Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"#FFEB9C"}}}),
		"cold": xMustStyle(f, &excelize.Style{Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"#FFF2CC"}}}),
		"dead": xMustStyle(f, &excelize.Style{Font: &excelize.Font{Color: "9C0006"}, Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"#FFC7CE"}}}),
	}
	trendStyles := map[string]int{
		"growing":   xMustStyle(f, &excelize.Style{Font: &excelize.Font{Bold: true, Color: "006100"}, Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"#C6EFCE"}}}),
		"stable":    xMustStyle(f, &excelize.Style{Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"#FFEB9C"}}}),
		"declining": xMustStyle(f, &excelize.Style{Font: &excelize.Font{Color: "9C0006"}, Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"#FFC7CE"}}}),
		"new":       xMustStyle(f, &excelize.Style{Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"#D9E2F3"}}}),
	}

	// --- Summary sheet ---
	row := 1
	for i, h := range []string{"Категория", "SKU", "Выручка", "Штуки", "Джини", "Топ-10%", "Топ-20%", "Медиана", "Среднее", "Dead SKU"} {
		cell, _ := excelize.CoordinatesToCellName(i+1, row)
		f.SetCellValue(sheet, cell, h)
		f.SetCellStyle(sheet, cell, cell, headerStyle)
	}
	f.SetColWidth(sheet, "A", "A", 20)
	for c := 2; c <= 10; c++ {
		col, _ := excelize.ColumnNumberToName(c)
		f.SetColWidth(sheet, col, col, 14)
	}
	row++

	distRows, err := r.db.QueryContext(ctx, `
		SELECT subject_name, sku_count, total_revenue, total_units,
		       gini, top10_share, top20_share, median_rev, mean_rev, dead_sku_count
		FROM category_distribution ORDER BY total_revenue DESC`)
	if err != nil {
		return fmt.Errorf("query summary: %w", err)
	}
	var categories []string
	for distRows.Next() {
		var sn string
		var sc, tu, dc int
		var tr, g, t10, t20, med, mn float64
		distRows.Scan(&sn, &sc, &tr, &tu, &g, &t10, &t20, &med, &mn, &dc)
		categories = append(categories, sn)
		for i, v := range []any{sn, sc, tr, tu, fmt.Sprintf("%.2f", g), fmt.Sprintf("%.1f%%", t10), fmt.Sprintf("%.1f%%", t20), med, mn, dc} {
			cell, _ := excelize.CoordinatesToCellName(i+1, row)
			f.SetCellValue(sheet, cell, v)
		}
		row++
	}
	distRows.Close()

	row += 2
	f.SetCellValue(sheet, fmt.Sprintf("A%d", row), "МЁРТВЫЙ КАТАЛОГ")
	f.SetCellStyle(sheet, fmt.Sprintf("A%d", row), fmt.Sprintf("A%d", row), sectionStyle)
	row++
	for i, h := range []string{"Категория", "SKU"} {
		cell, _ := excelize.CoordinatesToCellName(i+1, row)
		f.SetCellValue(sheet, cell, h)
		f.SetCellStyle(sheet, cell, cell, headerStyle)
	}
	row++
	deadRows, err := r.db.QueryContext(ctx, `SELECT subject_name, sku_count FROM category_dead_catalog ORDER BY sku_count DESC`)
	if err != nil {
		return fmt.Errorf("query dead xlsx: %w", err)
	}
	for deadRows.Next() {
		var sn string
		var cnt int
		deadRows.Scan(&sn, &cnt)
		f.SetCellValue(sheet, fmt.Sprintf("A%d", row), sn)
		f.SetCellValue(sheet, fmt.Sprintf("B%d", row), cnt)
		row++
	}
	deadRows.Close()

	// --- Per-category sheets ---
	for _, cat := range categories {
		sn := truncateSheetName(cat)
		f.NewSheet(sn)
		row = 1

		xSection(f, sn, &row, "Ранжирование SKU (Парето)", sectionStyle)
		xHeader(f, sn, &row, []string{"#", "vendor_code", "nm_id", "Выручка", "Штуки", "Ср. цена", "Кумул. %", "Эшелон"}, headerStyle)
		rr, _ := r.db.QueryContext(ctx, `SELECT nm_id, vendor_code, total_revenue, total_units, avg_price, cum_revenue_pct, pareto_tier FROM category_sku_ranking WHERE subject_name = ? ORDER BY total_revenue DESC`, cat)
		n := 0
		for rr.Next() {
			var nmID, units int
			var vc, tier string
			var rev, avgP, cum float64
			rr.Scan(&nmID, &vc, &rev, &units, &avgP, &cum, &tier)
			n++
			col := 1
			xSet(f, sn, row, col, n); col++
			xSet(f, sn, row, col, vc); col++
			xSet(f, sn, row, col, nmID); col++
			xSet(f, sn, row, col, fmt.Sprintf("%.0f", rev)); col++
			xSet(f, sn, row, col, units); col++
			xSet(f, sn, row, col, fmt.Sprintf("%.0f", avgP)); col++
			xSet(f, sn, row, col, fmt.Sprintf("%.1f%%", cum)); col++
			xSet(f, sn, row, col, tier)
			if s, ok := tierStyles[tier]; ok {
				c, _ := excelize.CoordinatesToCellName(col, row)
				f.SetCellStyle(sn, c, c, s)
			}
			row++
		}
		rr.Close()
		row++

		xSection(f, sn, &row, "Ценовые сегменты", sectionStyle)
		xHeader(f, sn, &row, []string{"Брекет", "SKU", "Выручка", "Штуки", "Доля"}, headerStyle)
		br, _ := r.db.QueryContext(ctx, `SELECT bracket, sku_count, total_revenue, total_units, revenue_pct FROM category_price_brackets WHERE subject_name = ? ORDER BY bracket`, cat)
		for br.Next() {
			var bracket string
			var sc, units int
			var rev, pct float64
			br.Scan(&bracket, &sc, &rev, &units, &pct)
			col := 1
			xSet(f, sn, row, col, bracket); col++
			xSet(f, sn, row, col, sc); col++
			xSet(f, sn, row, col, fmt.Sprintf("%.0f", rev)); col++
			xSet(f, sn, row, col, units); col++
			xSet(f, sn, row, col, fmt.Sprintf("%.1f%%", pct))
			row++
		}
		br.Close()
		row++

		xSection(f, sn, &row, "Скорость продаж", sectionStyle)
		xHeader(f, sn, &row, []string{"vendor_code", "nm_id", "Штуки", "Шт/день", "Класс"}, headerStyle)
		vr, _ := r.db.QueryContext(ctx, `SELECT nm_id, vendor_code, total_units, units_per_day, velocity_class FROM category_velocity WHERE subject_name = ? ORDER BY units_per_day DESC`, cat)
		for vr.Next() {
			var nmID, units int
			var vc, class string
			var upd float64
			vr.Scan(&nmID, &vc, &units, &upd, &class)
			col := 1
			xSet(f, sn, row, col, vc); col++
			xSet(f, sn, row, col, nmID); col++
			xSet(f, sn, row, col, units); col++
			xSet(f, sn, row, col, fmt.Sprintf("%.2f", upd)); col++
			xSet(f, sn, row, col, class)
			if s, ok := velStyles[class]; ok {
				c, _ := excelize.CoordinatesToCellName(col, row)
				f.SetCellStyle(sn, c, c, s)
			}
			row++
		}
		vr.Close()
		row++

		xSection(f, sn, &row, "Тренд", sectionStyle)
		xHeader(f, sn, &row, []string{"vendor_code", "nm_id", "Текущая", "Предыдущая", "Изменение %", "Тренд"}, headerStyle)
		tr, _ := r.db.QueryContext(ctx, `SELECT nm_id, vendor_code, curr_revenue, prev_revenue, change_pct, trend FROM category_trend WHERE subject_name = ? ORDER BY change_pct DESC`, cat)
		for tr.Next() {
			var nmID int
			var vc, trend string
			var curr, prev, chg float64
			tr.Scan(&nmID, &vc, &curr, &prev, &chg, &trend)
			col := 1
			xSet(f, sn, row, col, vc); col++
			xSet(f, sn, row, col, nmID); col++
			xSet(f, sn, row, col, fmt.Sprintf("%.0f", curr)); col++
			xSet(f, sn, row, col, fmt.Sprintf("%.0f", prev)); col++
			xSet(f, sn, row, col, fmt.Sprintf("%.1f%%", chg)); col++
			xSet(f, sn, row, col, trend)
			if s, ok := trendStyles[trend]; ok {
				c, _ := excelize.CoordinatesToCellName(col, row)
				f.SetCellStyle(sn, c, c, s)
			}
			row++
		}
		tr.Close()

		f.SetColWidth(sn, "A", "A", 14)
		f.SetColWidth(sn, "B", "B", 12)
		f.SetColWidth(sn, "C", "C", 12)
	}

	return f.SaveAs(xlsxPath)
}

func xMustStyle(f *excelize.File, style *excelize.Style) int {
	id, _ := f.NewStyle(style)
	return id
}

func truncateSheetName(name string) string {
	if len(name) <= 31 {
		return name
	}
	return name[:28] + "..."
}

func xSection(f *excelize.File, sheet string, row *int, title string, style int) {
	f.SetCellValue(sheet, fmt.Sprintf("A%d", *row), title)
	f.SetCellStyle(sheet, fmt.Sprintf("A%d", *row), fmt.Sprintf("H%d", *row), style)
	*row++
}

func xHeader(f *excelize.File, sheet string, row *int, headers []string, style int) {
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, *row)
		f.SetCellValue(sheet, cell, h)
		f.SetCellStyle(sheet, cell, cell, style)
	}
	*row++
}

func xSet(f *excelize.File, sheet string, row, col int, value any) {
	cell, _ := excelize.CoordinatesToCellName(col, row)
	f.SetCellValue(sheet, cell, value)
}
