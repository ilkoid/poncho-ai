// query.go — read-only SQL отчёта по FBS-заданиям и структуры строк.
//
// buildJoined один раз выполняет тяжёлый джойн снимка (fbs_orders × статусы ×
// orders-статистика × operational_sales × cards) в TEMP-таблицу fbs_report_joined
// и сразу её ANALYZ-ит — все дальнейшие агрегаты идут по 116 тыс. строк локально,
// без повторных джойнов с orders (3.7 млн строк, медленный сервер).
//
// Часовые пояса: fbs_orders.created_at — RFC3339 UTC; orders.order_date — текстовое
// МСК без смещения. Всё приводится к наивному МСК: (created_at::timestamptz AT TIME
// ZONE 'Europe/Moscow'). Лаг и возраст считаются в МСК.
package main

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// buildJoined строит TEMP-таблицу fbs_report_joined на переданном соединении
// (TEMP живёт только внутри сессии — запись в БД не производится).
// ON COMMIT DROP не подходит: в автocommit-режиме pgx таблица удаляется сразу
// после CREATE, поэтому чистим явно в конце сессии (dropJoined).
func dropJoined(ctx context.Context, conn *pgxpool.Conn) {
	_, _ = conn.Exec(ctx, `DROP TABLE IF EXISTS fbs_report_joined`)
}

func buildJoined(ctx context.Context, conn *pgxpool.Conn, lagHours int) error {
	const q = `
CREATE TEMP TABLE fbs_report_joined AS
SELECT
  f.id,
  f.rid,
  f.order_uid,
  f.created_at,
  (f.created_at::timestamptz AT TIME ZONE 'Europe/Moscow')::timestamp AS created_msk,
  f.supply_id,
  f.warehouse_id,
  f.nm_id,
  f.article,
  f.price,
  s.supplier_status,
  s.wb_status,
  s.is_cancellable,
  (st.srid IS NOT NULL)        AS has_stats,
  st.order_date::timestamp     AS order_dt_msk,
  st.warehouse_type            AS stats_wh_type,
  (os.srid IS NOT NULL)        AS sold,
  COALESCE(c.vendor_code, '')  AS vendor_code,
  COALESCE(c.subject_name, '') AS subject_name,
  CASE
    WHEN st.srid IS NULL THEN 'no_stats_row'
    WHEN EXTRACT(EPOCH FROM ((f.created_at::timestamptz AT TIME ZONE 'Europe/Moscow')
                             - st.order_date::timestamp)) > $1 * 3600 THEN 'migrated'
    ELSE 'native'
  END AS origin,
  CASE WHEN st.srid IS NULL THEN NULL
       ELSE EXTRACT(EPOCH FROM ((f.created_at::timestamptz AT TIME ZONE 'Europe/Moscow')
                                - st.order_date::timestamp)) / 3600.0
  END AS lag_h
FROM public.fbs_orders f
JOIN public.fbs_orders_status s  ON s.order_id = f.id
LEFT JOIN public.orders st       ON st.srid = f.rid
LEFT JOIN (SELECT DISTINCT srid FROM public.operational_sales) os ON os.srid = f.rid
LEFT JOIN public.cards c         ON c.nm_id = f.nm_id`
	if _, err := conn.Exec(ctx, q, lagHours); err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	if _, err := conn.Exec(ctx, `ANALYZE fbs_report_joined`); err != nil {
		return fmt.Errorf("analyze temp: %w", err)
	}
	return nil
}

// ── Строки отчёта ──

// OriginRow — сводка по классу происхождения.
type OriginRow struct {
	Origin  string
	Tasks   int64
	Active  int64
	Sold    int64
	MedLagH float64 // медианный лаг created_at−order_date, ч (−1 = нет данных)
}

// DailyRow — создания заданий по дням (разрез origin уже развёрнут в колонки).
type DailyRow struct {
	Day        string // YYYY-MM-DD (МСК)
	Native     int64
	Migrated   int64
	NoStatsRow int64
}

func (d DailyRow) Total() int64 { return d.Native + d.Migrated + d.NoStatsRow }

// AgeRow — возраст активных заданий (supplier_status new/confirm) по бакетам.
type AgeRow struct {
	SupplierStatus string
	Lt1d           int64
	D1to3d         int64
	D3to7d         int64
	Gt7d           int64
	Total          int64
}

// StatusRow — крест origin × supplier_status × wb_status.
type StatusRow struct {
	Origin         string
	SupplierStatus string
	WBStatus       string
	N              int64
}

// DetailRow — строка детализации (старые активные и мигрированные задания).
type DetailRow struct {
	Rid            string
	CreatedMSK     string // YYYY-MM-DD HH:MM
	AgeDays        float64
	SupplierStatus string
	WBStatus       string
	Origin         string
	LagH           *float64
	StatsWHType    string
	VendorCode     string
	Article        string
	NmID           int64
	SubjectName    string
	SupplyID       string
	IsCancellable  bool
	PriceRub       float64
}

// CoverageRow — полнота данных снимка и свежесть orders.
type CoverageRow struct {
	Tasks       int64
	MinDate     string
	MaxDate     string
	Matched     int64
	OrdersFresh string // последняя строка в orders (downloaded_at / order_date)
}

// MatchPct — доля заданий, найденных в Statistics API.
func (c CoverageRow) MatchPct() float64 {
	if c.Tasks == 0 {
		return 0
	}
	return float64(c.Matched) / float64(c.Tasks) * 100
}

// ReportData — всё, что уходит в XLSX и консольную сводку.
type ReportData struct {
	Origins        []OriginRow
	Daily          []DailyRow
	AgeByStatus    []AgeRow
	Statuses       []StatusRow
	MigratedLags   []float64 // лаги (ч) всех migrated-заданий — для гистограммы
	Detail         []DetailRow
	Coverage       CoverageRow
	ActiveCount    int64
	OldActiveCount int64 // активных старше OldDays (считается в loadAll)
}

// ActiveTotal — суммарные активные по всем origin.
func (d *ReportData) ActiveTotal() int64 { return d.ActiveCount }

// loadAll собирает все агрегаты и детализацию из TEMP-таблицы.
func loadAll(ctx context.Context, conn *pgxpool.Conn, oldDays int) (*ReportData, error) {
	data := &ReportData{}

	// 1) сводка по origin.
	rows, err := conn.Query(ctx, `
		SELECT origin, count(*),
		       count(*) FILTER (WHERE supplier_status IN ('new','confirm')),
		       count(*) FILTER (WHERE sold),
		       COALESCE(percentile_disc(0.5) WITHIN GROUP (ORDER BY lag_h), -1)
		FROM fbs_report_joined GROUP BY origin ORDER BY origin`)
	if err != nil {
		return nil, fmt.Errorf("origin summary: %w", err)
	}
	for rows.Next() {
		var o OriginRow
		if err := rows.Scan(&o.Origin, &o.Tasks, &o.Active, &o.Sold, &o.MedLagH); err != nil {
			return nil, fmt.Errorf("origin scan: %w", err)
		}
		data.Origins = append(data.Origins, o)
	}
	rows.Close()

	// 2) динамика по дням (пивот origin в Go).
	rows, err = conn.Query(ctx, `
		SELECT to_char(created_msk, 'YYYY-MM-DD'), origin, count(*)
		FROM fbs_report_joined GROUP BY 1, 2 ORDER BY 1`)
	if err != nil {
		return nil, fmt.Errorf("daily: %w", err)
	}
	byDay := map[string]*DailyRow{}
	var order []string
	for rows.Next() {
		var day, origin string
		var n int64
		if err := rows.Scan(&day, &origin, &n); err != nil {
			return nil, fmt.Errorf("daily scan: %w", err)
		}
		dr, ok := byDay[day]
		if !ok {
			dr = &DailyRow{Day: day}
			byDay[day] = dr
			order = append(order, day)
		}
		switch origin {
		case "native":
			dr.Native = n
		case "migrated":
			dr.Migrated = n
		case "no_stats_row":
			dr.NoStatsRow = n
		}
	}
	rows.Close()
	for _, day := range order {
		data.Daily = append(data.Daily, *byDay[day])
	}

	// 3) возраст активных по статусам.
	rows, err = conn.Query(ctx, `
		SELECT supplier_status,
		       count(*) FILTER (WHERE age_h < 24),
		       count(*) FILTER (WHERE age_h >= 24 AND age_h < 72),
		       count(*) FILTER (WHERE age_h >= 72 AND age_h < 168),
		       count(*) FILTER (WHERE age_h >= 168),
		       count(*)
		FROM (SELECT supplier_status,
		             EXTRACT(EPOCH FROM ((now() AT TIME ZONE 'Europe/Moscow') - created_msk)) / 3600.0 AS age_h
		      FROM fbs_report_joined
		      WHERE supplier_status IN ('new','confirm')) t
		GROUP BY 1 ORDER BY 1`)
	if err != nil {
		return nil, fmt.Errorf("age: %w", err)
	}
	for rows.Next() {
		var a AgeRow
		if err := rows.Scan(&a.SupplierStatus, &a.Lt1d, &a.D1to3d, &a.D3to7d, &a.Gt7d, &a.Total); err != nil {
			return nil, fmt.Errorf("age scan: %w", err)
		}
		data.AgeByStatus = append(data.AgeByStatus, a)
		data.ActiveCount += a.Total
	}
	rows.Close()

	// 3b) активные старше oldDays (для точного счётчика при нестандартном пороге).
	if err := conn.QueryRow(ctx, `
		SELECT count(*) FROM fbs_report_joined
		WHERE supplier_status IN ('new','confirm')
		  AND EXTRACT(EPOCH FROM ((now() AT TIME ZONE 'Europe/Moscow') - created_msk)) > $1 * 86400`, oldDays).
		Scan(&data.OldActiveCount); err != nil {
		return nil, fmt.Errorf("old active: %w", err)
	}

	// 4) крест origin × статусы.
	rows, err = conn.Query(ctx, `
		SELECT origin, supplier_status, wb_status, count(*)
		FROM fbs_report_joined GROUP BY 1, 2, 3 ORDER BY 4 DESC`)
	if err != nil {
		return nil, fmt.Errorf("statuses: %w", err)
	}
	for rows.Next() {
		var s StatusRow
		if err := rows.Scan(&s.Origin, &s.SupplierStatus, &s.WBStatus, &s.N); err != nil {
			return nil, fmt.Errorf("status scan: %w", err)
		}
		data.Statuses = append(data.Statuses, s)
	}
	rows.Close()

	// 5) лаги мигрированных (для гистограммы).
	rows, err = conn.Query(ctx, `
		SELECT lag_h FROM fbs_report_joined WHERE origin = 'migrated' AND lag_h IS NOT NULL`)
	if err != nil {
		return nil, fmt.Errorf("migrated lags: %w", err)
	}
	for rows.Next() {
		var l float64
		if err := rows.Scan(&l); err != nil {
			return nil, fmt.Errorf("lag scan: %w", err)
		}
		data.MigratedLags = append(data.MigratedLags, l)
	}
	rows.Close()

	// 6) детализация: активные И (мигрированные ИЛИ старше oldDays).
	rows, err = conn.Query(ctx, `
		SELECT rid, to_char(created_msk, 'YYYY-MM-DD HH24:MI'), supplier_status, wb_status,
		       origin, lag_h, COALESCE(stats_wh_type, ''), vendor_code, article, nm_id,
		       subject_name, supply_id, is_cancellable, price / 100.0
		FROM fbs_report_joined
		WHERE supplier_status IN ('new','confirm')
		  AND (origin = 'migrated'
		       OR EXTRACT(EPOCH FROM ((now() AT TIME ZONE 'Europe/Moscow') - created_msk)) > $1 * 86400)
		ORDER BY created_msk`, oldDays)
	if err != nil {
		return nil, fmt.Errorf("detail: %w", err)
	}
	nowMSK := nowMoscow()
	for rows.Next() {
		var d DetailRow
		if err := rows.Scan(&d.Rid, &d.CreatedMSK, &d.SupplierStatus, &d.WBStatus, &d.Origin,
			&d.LagH, &d.StatsWHType, &d.VendorCode, &d.Article, &d.NmID,
			&d.SubjectName, &d.SupplyID, &d.IsCancellable, &d.PriceRub); err != nil {
			return nil, fmt.Errorf("detail scan: %w", err)
		}
		if t, err := parseMSK(d.CreatedMSK); err == nil {
			d.AgeDays = nowMSK.Sub(t).Hours() / 24
		}
		data.Detail = append(data.Detail, d)
	}
	rows.Close()
	if rows.Err() != nil {
		return nil, rows.Err()
	}

	// 7) покрытие + свежесть orders.
	var minD, maxD *string
	if err := conn.QueryRow(ctx, `
		SELECT count(*), min(created_msk)::date::text, max(created_msk)::date::text,
		       count(*) FILTER (WHERE has_stats)
		FROM fbs_report_joined`).Scan(&data.Coverage.Tasks, &minD, &maxD, &data.Coverage.Matched); err != nil {
		return nil, fmt.Errorf("coverage: %w", err)
	}
	if minD != nil {
		data.Coverage.MinDate = *minD
	}
	if maxD != nil {
		data.Coverage.MaxDate = *maxD
	}
	var lastOrder, lastDL string
	if err := conn.QueryRow(ctx, `
		SELECT order_date, downloaded_at FROM public.orders ORDER BY id DESC LIMIT 1`).
		Scan(&lastOrder, &lastDL); err != nil && err != pgx.ErrNoRows {
		return nil, fmt.Errorf("orders freshness: %w", err)
	}
	data.Coverage.OrdersFresh = fmt.Sprintf("order_date=%s (скачано %s)", lastOrder, lastDL)

	return data, nil
}
