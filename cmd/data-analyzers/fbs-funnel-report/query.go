// query.go — read-only SQL воронки FBS и структуры строк отчёта.
//
// Источники (все — SELECT, ничего не пишем):
//
//   - public.order_feed        — лента заказов POST /api/analytics/v1/order-feed
//     (docs/wb_api_swagger/11-analytics.yaml:1682).
//     Ключевая семантика: updated_at = дата ТЕКУЩЕГО
//     статуса (11-analytics.yaml:6414), поэтому для строк
//     status='buyout' updated_at — момент выкупа, и
//     GROUP BY updated_at::date — это события по дням.
//     status: created/buyout/cancel/return/returnDefective;
//     cancel_type: app/receipt/expire/other
//     (11-analytics.yaml:6414-6439); is_mp=true → склад
//     продавца (FBS/DBS).
//   - public.fbs_orders        — сборочные задания GET /api/v3/orders;
//   - public.fbs_orders_status — снимок статусов POST /api/v3/orders/status,
//     wbStatus enum — 03-orders-fbs.yaml:664
//     (sold = выкуп; canceled/canceled_by_client/
//     declined_by_client = отмена);
//   - public.cards             — справочник артикулов/предметов (для читаемости).
//
// Параметры всех запросов: $1 = all_models (true → без фильтра is_mp),
// $2 = since (date | NULL → без ограничения снизу). Дни — МСК:
// (ts AT TIME ZONE 'Europe/Moscow')::date.
package main

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// coverageQuery — счётчики и диапазоны обеих таблиц (для «Сводки» и fail-fast).
const coverageQuery = `
SELECT
  (SELECT count(*) FROM public.fbs_orders),
  (SELECT (min(created_at) AT TIME ZONE 'Europe/Moscow')::date::text FROM public.fbs_orders),
  (SELECT (max(created_at) AT TIME ZONE 'Europe/Moscow')::date::text FROM public.fbs_orders),
  (SELECT count(*) FROM public.fbs_orders_status),
  (SELECT count(*) FROM public.order_feed),
  (SELECT count(*) FROM public.order_feed WHERE is_mp),
  (SELECT (min(updated_at) AT TIME ZONE 'Europe/Moscow')::date::text FROM public.order_feed WHERE is_mp),
  (SELECT (max(updated_at) AT TIME ZONE 'Europe/Moscow')::date::text FROM public.order_feed WHERE is_mp)`

// funnelTotalsQuery — итоговая воронка за окно (лента, текущие статусы + деньги).
// seller_price — цена продавца (без комиссий/логистики WB): «заказано» — весь поток,
// «выручка» — только выкупы, «упущено» — отмены и возвраты.
const funnelTotalsQuery = `
SELECT
  count(*),
  count(*) FILTER (WHERE status = 'buyout'),
  count(*) FILTER (WHERE status = 'cancel'),
  count(*) FILTER (WHERE status IN ('return', 'returnDefective')),
  count(*) FILTER (WHERE status = 'created'),
  COALESCE(sum(seller_price), 0)::float8,
  COALESCE(sum(seller_price) FILTER (WHERE status = 'buyout'), 0)::float8,
  COALESCE(sum(seller_price) FILTER (WHERE status IN ('cancel', 'return', 'returnDefective')), 0)::float8
FROM public.order_feed
WHERE ($1 OR is_mp)
  AND ($2::date IS NULL OR (updated_at AT TIME ZONE 'Europe/Moscow')::date >= $2::date)`

// funnelDailyQuery — события по дням: день = дата перехода (updated_at по МСК).
const funnelDailyQuery = `
SELECT
  (updated_at AT TIME ZONE 'Europe/Moscow')::date::text AS day,
  count(*) FILTER (WHERE status = 'buyout') AS buyout,
  count(*) FILTER (WHERE status = 'cancel') AS cancel,
  count(*) FILTER (WHERE status IN ('return', 'returnDefective')) AS returns,
  count(*) FILTER (WHERE status = 'created') AS still_new,
  count(*) AS total,
  COALESCE(sum(seller_price) FILTER (WHERE status = 'buyout'), 0)::float8 AS buyout_rub,
  COALESCE(sum(seller_price) FILTER (WHERE status IN ('cancel', 'return', 'returnDefective')), 0)::float8 AS lost_rub
FROM public.order_feed
WHERE ($1 OR is_mp)
  AND ($2::date IS NULL OR (updated_at AT TIME ZONE 'Europe/Moscow')::date >= $2::date)
GROUP BY 1 ORDER BY 1`

// cohortsFeedQuery — когорты по дню создания (лента). Полноту когорты видно из
// in_flight: зрелая = «в пути» ≤ 10% заказов (markMaturity).
const cohortsFeedQuery = `
SELECT
  (created_at AT TIME ZONE 'Europe/Moscow')::date::text AS cohort,
  count(*) AS orders,
  count(*) FILTER (WHERE status = 'buyout') AS buyout,
  count(*) FILTER (WHERE status = 'cancel') AS cancel,
  count(*) FILTER (WHERE status IN ('return', 'returnDefective')) AS returns,
  count(*) FILTER (WHERE status = 'created') AS in_flight,
  COALESCE(sum(seller_price), 0)::float8 AS ordered_rub,
  COALESCE(sum(seller_price) FILTER (WHERE status = 'buyout'), 0)::float8 AS buyout_rub
FROM public.order_feed
WHERE ($1 OR is_mp)
  AND ($2::date IS NULL OR (created_at AT TIME ZONE 'Europe/Moscow')::date >= $2::date)
GROUP BY 1 ORDER BY 1`

// cohortsV3Query — те же когорты из v3-снимка (fbs_orders × статусы): снимок на
// момент последнего прогона загрузчика, без дат переходов. Единственный
// параметр $1 — since (is_mp-фильтра нет: v3-таблицы домены FBS).
const cohortsV3Query = `
SELECT
  (o.created_at AT TIME ZONE 'Europe/Moscow')::date::text AS cohort,
  count(*) AS orders,
  count(*) FILTER (WHERE s.wb_status = 'sold') AS sold,
  count(*) FILTER (WHERE s.wb_status IN ('canceled', 'canceled_by_client', 'declined_by_client')) AS canceled,
  count(*) FILTER (WHERE s.wb_status = 'defect') AS defect,
  count(*) FILTER (WHERE s.wb_status IN
    ('waiting', 'sorted', 'ready_for_pickup', 'accepted_by_carrier', 'sent_to_carrier', 'postponed_delivery')) AS in_flight
FROM public.fbs_orders o
JOIN public.fbs_orders_status s ON s.order_id = o.id
WHERE ($1::date IS NULL OR (o.created_at AT TIME ZONE 'Europe/Moscow')::date >= $1::date)
GROUP BY 1 ORDER BY 1`

// v3TotalsQuery — итоговая воронка по v3-снимку (для «Сводки»); $1 — since.
const v3TotalsQuery = `
SELECT
  count(*),
  count(*) FILTER (WHERE s.wb_status = 'sold'),
  count(*) FILTER (WHERE s.wb_status IN ('canceled', 'canceled_by_client', 'declined_by_client')),
  count(*) FILTER (WHERE s.wb_status IN
    ('waiting', 'sorted', 'ready_for_pickup', 'accepted_by_carrier', 'sent_to_carrier', 'postponed_delivery'))
FROM public.fbs_orders o
JOIN public.fbs_orders_status s ON s.order_id = o.id
WHERE ($1::date IS NULL OR (o.created_at AT TIME ZONE 'Europe/Moscow')::date >= $1::date)`

// cancelReasonsQuery — причины отмен по дням (только status='cancel').
const cancelReasonsQuery = `
SELECT
  (updated_at AT TIME ZONE 'Europe/Moscow')::date::text AS day,
  count(*) FILTER (WHERE cancel_type = 'receipt') AS receipt,
  count(*) FILTER (WHERE cancel_type = 'app') AS app,
  count(*) FILTER (WHERE cancel_type = 'expire') AS expire,
  count(*) FILTER (WHERE cancel_type = 'other') AS other,
  count(*) FILTER (WHERE cancel_type IS NULL) AS unknown
FROM public.order_feed
WHERE ($1 OR is_mp) AND status = 'cancel'
  AND ($2::date IS NULL OR (updated_at AT TIME ZONE 'Europe/Moscow')::date >= $2::date)
GROUP BY 1 ORDER BY 1`

// lifecycleOverallQuery — скорость цикла заказ→выкуп по всем выкупам окна.
const lifecycleOverallQuery = `
SELECT
  count(*),
  percentile_cont(0.5) WITHIN GROUP (ORDER BY EXTRACT(EPOCH FROM (updated_at - created_at)) / 3600.0),
  percentile_cont(0.9) WITHIN GROUP (ORDER BY EXTRACT(EPOCH FROM (updated_at - created_at)) / 3600.0)
FROM public.order_feed
WHERE ($1 OR is_mp) AND status = 'buyout'
  AND ($2::date IS NULL OR (updated_at AT TIME ZONE 'Europe/Moscow')::date >= $2::date)`

// lifecycleByCohortQuery — медиана/p90 цикла по когортам создания.
const lifecycleByCohortQuery = `
SELECT
  (created_at AT TIME ZONE 'Europe/Moscow')::date::text AS cohort,
  count(*) AS buyouts,
  percentile_cont(0.5) WITHIN GROUP (ORDER BY EXTRACT(EPOCH FROM (updated_at - created_at)) / 3600.0) AS median_h,
  percentile_cont(0.9) WITHIN GROUP (ORDER BY EXTRACT(EPOCH FROM (updated_at - created_at)) / 3600.0) AS p90_h
FROM public.order_feed
WHERE ($1 OR is_mp) AND status = 'buyout'
  AND ($2::date IS NULL OR (created_at AT TIME ZONE 'Europe/Moscow')::date >= $2::date)
GROUP BY 1 ORDER BY 1`

// geoQuery — география доставки (город/район): заказы, выкуп, выручка.
const geoQuery = `
SELECT
  COALESCE(NULLIF(destination_city, ''), '— не указан —') AS city,
  COALESCE(NULLIF(destination_district, ''), '—') AS district,
  count(*) AS orders,
  count(*) FILTER (WHERE status = 'buyout') AS buyout,
  count(*) FILTER (WHERE status = 'cancel') AS cancel,
  count(*) FILTER (WHERE status = 'created') AS in_flight,
  COALESCE(sum(seller_price) FILTER (WHERE status = 'buyout'), 0)::float8 AS buyout_rub
FROM public.order_feed
WHERE ($1 OR is_mp)
  AND ($2::date IS NULL OR (created_at AT TIME ZONE 'Europe/Moscow')::date >= $2::date)
GROUP BY 1, 2 ORDER BY orders DESC, 1 LIMIT 50`

// topNmQuery — воронка по номенклатурам (артикул WB), сортировка по упущенной
// выручке. Проценты у nm с малым числом заказов — шум: ориентируйтесь на колонку
// «Заказы» (фильтр/сортировка в Excel).
const topNmQuery = `
SELECT
  f.nm_id,
  COALESCE(NULLIF(c.vendor_code, ''), f.nm_id::text) AS vendor_code,
  COALESCE(c.subject_name, '') AS subject_name,
  count(*) AS orders,
  count(*) FILTER (WHERE f.status = 'buyout') AS buyout,
  count(*) FILTER (WHERE f.status = 'cancel') AS cancel,
  count(*) FILTER (WHERE f.status IN ('return', 'returnDefective')) AS returns,
  count(*) FILTER (WHERE f.status = 'created') AS in_flight,
  COALESCE(sum(f.seller_price), 0)::float8 AS ordered_rub,
  COALESCE(sum(f.seller_price) FILTER (WHERE f.status = 'buyout'), 0)::float8 AS buyout_rub,
  COALESCE(sum(f.seller_price) FILTER (WHERE f.status IN ('cancel', 'return', 'returnDefective')), 0)::float8 AS lost_rub
FROM public.order_feed f
LEFT JOIN public.cards c ON c.nm_id = f.nm_id
WHERE ($1 OR f.is_mp)
  AND ($2::date IS NULL OR (f.created_at AT TIME ZONE 'Europe/Moscow')::date >= $2::date)
GROUP BY f.nm_id, c.vendor_code, c.subject_name
ORDER BY lost_rub DESC`

// crossCheckQuery — сверка v3 ↔ лента по rid=srid по когортам: полнота ленты
// относительно канонического v3-потока заданий.
const crossCheckQuery = `
SELECT
  (o.created_at AT TIME ZONE 'Europe/Moscow')::date::text AS cohort,
  count(*) AS v3_orders,
  count(*) FILTER (WHERE f.srid IS NOT NULL) AS in_feed,
  count(*) FILTER (WHERE f.srid IS NULL) AS not_in_feed
FROM public.fbs_orders o
LEFT JOIN public.order_feed f ON f.srid = o.rid AND ($1 OR f.is_mp)
WHERE ($2::date IS NULL OR (o.created_at AT TIME ZONE 'Europe/Moscow')::date >= $2::date)
GROUP BY 1 ORDER BY 1`

// ============================================================================
// Приёмка поставок на СЦ — public.fbs_supplies (GET /api/v3/supplies,
// 03-orders-fbs.yaml:2089; scan_dt — :4561 «дата сканирования поставки или
// первого заказа» = момент приёмки на сортировочном центре).
// ============================================================================

// suppliesPerSupCTE — зерно «поставка»: первый заказ поставки и лаг до приёмки.
const suppliesPerSupCTE = `
WITH sup AS (
  SELECT sp.supply_id, sp.scan_dt,
         min(o.created_at) AS first_at,
         count(*) AS n_orders
  FROM public.fbs_supplies sp
  JOIN public.fbs_orders o ON o.supply_id = sp.supply_id
  WHERE sp.scan_dt IS NOT NULL AND o.cross_border_type = 0
  GROUP BY 1, 2
), per_sup AS (
  SELECT supply_id, first_at, n_orders,
         EXTRACT(EPOCH FROM (scan_dt - first_at)) / 3600.0 AS delay_h,
         (first_at AT TIME ZONE 'Europe/Moscow')::date AS cohort_d
  FROM sup
)`

// suppliesCohortsQuery — лаг приёмки по когортам (дата первого заказа поставки).
// Поставочный лаг = scan_dt − первый заказ поставки (сколько ждала самая ранняя
// позиция); ord_med_h — медиана «scan_dt − создание заказа» по заказам когорты
// (типичный заказ когорты). Порог целевого сервиса — 24 часа.
const suppliesCohortsQuery = suppliesPerSupCTE + `
SELECT
  cohort_d::text,
  count(*)::int,
  sum(n_orders)::int,
  percentile_cont(0.5) WITHIN GROUP (ORDER BY delay_h),
  percentile_cont(0.9) WITHIN GROUP (ORDER BY delay_h),
  min(delay_h), max(delay_h),
  100.0 * count(*) FILTER (WHERE delay_h <= 24) / count(*),
  100.0 * COALESCE(sum(n_orders) FILTER (WHERE delay_h <= 24), 0) / sum(n_orders),
  (SELECT percentile_cont(0.5) WITHIN GROUP (ORDER BY
     EXTRACT(EPOCH FROM (sp2.scan_dt - o2.created_at)) / 3600.0)
   FROM public.fbs_orders o2
   JOIN public.fbs_supplies sp2 ON sp2.supply_id = o2.supply_id
   WHERE sp2.scan_dt IS NOT NULL AND o2.cross_border_type = 0
     AND (o2.created_at AT TIME ZONE 'Europe/Moscow')::date = cohort_d)
FROM per_sup
GROUP BY cohort_d
ORDER BY cohort_d`

// suppliesHistQuery — распределение поставочных лагов по бакетам.
const suppliesHistQuery = suppliesPerSupCTE + `
SELECT
  CASE WHEN delay_h <= 12 THEN '0–12ч'
       WHEN delay_h <= 24 THEN '12–24ч'
       WHEN delay_h <= 36 THEN '24–36ч'
       WHEN delay_h <= 48 THEN '36–48ч'
       WHEN delay_h <= 72 THEN '48–72ч'
       WHEN delay_h <= 120 THEN '72–120ч'
       ELSE '>120ч' END AS bucket,
  count(*)::int,
  sum(n_orders)::int
FROM per_sup
GROUP BY 1
ORDER BY min(delay_h)`

// suppliesTotalsQuery — наполнение fbs_supplies (для оговорки о непринятых).
const suppliesTotalsQuery = `
SELECT count(*)::int,
  count(*) FILTER (WHERE scan_dt IS NOT NULL)::int,
  count(*) FILTER (WHERE scan_dt IS NULL)::int
FROM public.fbs_supplies`

// ── Строки отчёта ──

// Coverage — счётчики и диапазоны источников.
type Coverage struct {
	FbsOrders     int64
	FbsOrdersFrom *string
	FbsOrdersTo   *string
	FbsStatuses   int64
	FeedAll       int64
	FeedMp        int64
	FeedFrom      *string
	FeedTo        *string
}

// FunnelTotals — итоговая воронка ленты (штуки + деньги).
type FunnelTotals struct {
	Rows       int64
	Buyout     int64
	Cancel     int64
	Returns    int64
	InFlight   int64
	OrderedRub float64
	BuyoutRub  float64
	LostRub    float64
}

// Finalized — завершившиеся заказы (выкуп + отмена + возврат).
func (t FunnelTotals) Finalized() int64 { return t.Buyout + t.Cancel + t.Returns }

// BuyoutPct — доля выкупа среди завершившихся; -1 = завершившихся нет.
func (t FunnelTotals) BuyoutPct() float64 {
	if f := t.Finalized(); f > 0 {
		return float64(t.Buyout) / float64(f) * 100
	}
	return -1
}

// V3Totals — итоговая воронка по v3-снимку.
type V3Totals struct {
	Orders   int64
	Sold     int64
	Canceled int64
	InFlight int64
}

// DailyEvent — события одного дня (день = дата перехода).
type DailyEvent struct {
	Day       string
	Buyout    int64
	Cancel    int64
	Returns   int64
	StillNew  int64
	Total     int64
	BuyoutRub float64
	LostRub   float64
}

// Finalized — завершившиеся в этот день.
func (d DailyEvent) Finalized() int64 { return d.Buyout + d.Cancel + d.Returns }

// BuyoutPct — доля выкупа среди завершившихся; -1 = завершившихся нет.
func (d DailyEvent) BuyoutPct() float64 {
	if f := d.Finalized(); f > 0 {
		return float64(d.Buyout) / float64(f) * 100
	}
	return -1
}

// CohortFeedRow — когорта по дню создания (лента).
type CohortFeedRow struct {
	Cohort     string
	Orders     int64
	Buyout     int64
	Cancel     int64
	Returns    int64
	InFlight   int64
	OrderedRub float64
	BuyoutRub  float64
	Mature     bool // «в пути» ≤ 10% заказов когорты — проценты можно читать
}

// Finalized — завершившиеся заказы когорты.
func (c CohortFeedRow) Finalized() int64 { return c.Buyout + c.Cancel + c.Returns }

// BuyoutPct — доля выкупа среди завершившихся; -1 = завершившихся нет.
func (c CohortFeedRow) BuyoutPct() float64 {
	if f := c.Finalized(); f > 0 {
		return float64(c.Buyout) / float64(f) * 100
	}
	return -1
}

// CohortV3Row — когорта по дню создания (v3-снимок).
type CohortV3Row struct {
	Cohort   string
	Orders   int64
	Sold     int64
	Canceled int64
	Defect   int64
	InFlight int64
}

// CancelReasonRow — причины отмен одного дня.
type CancelReasonRow struct {
	Day     string
	Receipt int64
	App     int64
	Expire  int64
	Other   int64
	Unknown int64
}

// Total — все отмены дня.
func (r CancelReasonRow) Total() int64 { return r.Receipt + r.App + r.Expire + r.Other + r.Unknown }

// Lifecycle — скорость цикла заказ→выкуп (часы; nil = нет выкупов).
type Lifecycle struct {
	Buyouts int64
	MedianH *float64
	P90H    *float64
}

// LifecycleCohort — скорость цикла одной когорты.
type LifecycleCohort struct {
	Cohort  string
	Buyouts int64
	MedianH *float64
	P90H    *float64
}

// GeoRow — география доставки.
type GeoRow struct {
	City      string
	District  string
	Orders    int64
	Buyout    int64
	Cancel    int64
	InFlight  int64
	BuyoutRub float64
}

// BuyoutPct — доля выкупа среди завершившихся; -1 = завершившихся нет.
func (g GeoRow) BuyoutPct() float64 {
	if f := g.Buyout + g.Cancel; f > 0 {
		return float64(g.Buyout) / float64(f) * 100
	}
	return -1
}

// NmRow — воронка одной номенклатуры.
type NmRow struct {
	NmID        int64
	VendorCode  string
	SubjectName string
	Orders      int64
	Buyout      int64
	Cancel      int64
	Returns     int64
	InFlight    int64
	OrderedRub  float64
	BuyoutRub   float64
	LostRub     float64
}

// BuyoutPct — доля выкупа среди завершившихся; -1 = завершившихся нет.
func (n NmRow) BuyoutPct() float64 {
	if f := n.Buyout + n.Cancel + n.Returns; f > 0 {
		return float64(n.Buyout) / float64(f) * 100
	}
	return -1
}

// CrossRow — сверка v3 ↔ лента одной когорты.
type CrossRow struct {
	Cohort   string
	V3Orders int64
	InFeed   int64
	NotInFee int64
}

// MatchPct — покрытие лентой; -1 = в v3 нет заказов когорты.
func (r CrossRow) MatchPct() float64 {
	if r.V3Orders > 0 {
		return float64(r.InFeed) / float64(r.V3Orders) * 100
	}
	return -1
}

// ReportData — всё, что уходит в XLSX.
type ReportData struct {
	Coverage          Coverage
	Totals            FunnelTotals
	V3                *V3Totals
	Daily             []DailyEvent
	Cohorts           []CohortFeedRow
	CohortsV3         []CohortV3Row
	CancelReasons     []CancelReasonRow
	Lifecycle         Lifecycle
	LifecycleByCohort []LifecycleCohort
	Geo               []GeoRow
	TopNm             []NmRow
	Cross             []CrossRow
	AllModels         bool
	Days              int
	GeneratedAt       string // МСК, «02.01.2006 15:04»
}

// ── Лоадеры ──

func loadCoverage(ctx context.Context, pool *pgxpool.Pool) (Coverage, error) {
	var c Coverage
	err := pool.QueryRow(ctx, coverageQuery).Scan(
		&c.FbsOrders, &c.FbsOrdersFrom, &c.FbsOrdersTo, &c.FbsStatuses,
		&c.FeedAll, &c.FeedMp, &c.FeedFrom, &c.FeedTo)
	if err != nil {
		return c, fmt.Errorf("coverage: %w", err)
	}
	return c, nil
}

func loadFunnelTotals(ctx context.Context, pool *pgxpool.Pool, q queryParams) (FunnelTotals, error) {
	var t FunnelTotals
	err := pool.QueryRow(ctx, funnelTotalsQuery, q.args()...).Scan(
		&t.Rows, &t.Buyout, &t.Cancel, &t.Returns, &t.InFlight,
		&t.OrderedRub, &t.BuyoutRub, &t.LostRub)
	if err != nil {
		return t, fmt.Errorf("funnel totals: %w", err)
	}
	return t, nil
}

func loadV3Totals(ctx context.Context, pool *pgxpool.Pool, q queryParams) (*V3Totals, error) {
	var t V3Totals
	err := pool.QueryRow(ctx, v3TotalsQuery, q.sinceOnly()...).Scan(
		&t.Orders, &t.Sold, &t.Canceled, &t.InFlight)
	if err != nil {
		return nil, fmt.Errorf("v3 totals: %w", err)
	}
	return &t, nil
}

func loadDaily(ctx context.Context, pool *pgxpool.Pool, q queryParams) ([]DailyEvent, error) {
	rows, err := pool.Query(ctx, funnelDailyQuery, q.args()...)
	if err != nil {
		return nil, fmt.Errorf("daily: %w", err)
	}
	defer rows.Close()
	var out []DailyEvent
	for rows.Next() {
		var d DailyEvent
		if err := rows.Scan(&d.Day, &d.Buyout, &d.Cancel, &d.Returns, &d.StillNew, &d.Total, &d.BuyoutRub, &d.LostRub); err != nil {
			return nil, fmt.Errorf("daily scan: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func loadCohorts(ctx context.Context, pool *pgxpool.Pool, q queryParams) ([]CohortFeedRow, error) {
	rows, err := pool.Query(ctx, cohortsFeedQuery, q.args()...)
	if err != nil {
		return nil, fmt.Errorf("cohorts: %w", err)
	}
	defer rows.Close()
	var out []CohortFeedRow
	for rows.Next() {
		var c CohortFeedRow
		if err := rows.Scan(&c.Cohort, &c.Orders, &c.Buyout, &c.Cancel, &c.Returns, &c.InFlight, &c.OrderedRub, &c.BuyoutRub); err != nil {
			return nil, fmt.Errorf("cohorts scan: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func loadCohortsV3(ctx context.Context, pool *pgxpool.Pool, q queryParams) ([]CohortV3Row, error) {
	rows, err := pool.Query(ctx, cohortsV3Query, q.sinceOnly()...)
	if err != nil {
		return nil, fmt.Errorf("cohorts v3: %w", err)
	}
	defer rows.Close()
	var out []CohortV3Row
	for rows.Next() {
		var c CohortV3Row
		if err := rows.Scan(&c.Cohort, &c.Orders, &c.Sold, &c.Canceled, &c.Defect, &c.InFlight); err != nil {
			return nil, fmt.Errorf("cohorts v3 scan: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func loadCancelReasons(ctx context.Context, pool *pgxpool.Pool, q queryParams) ([]CancelReasonRow, error) {
	rows, err := pool.Query(ctx, cancelReasonsQuery, q.args()...)
	if err != nil {
		return nil, fmt.Errorf("cancel reasons: %w", err)
	}
	defer rows.Close()
	var out []CancelReasonRow
	for rows.Next() {
		var r CancelReasonRow
		if err := rows.Scan(&r.Day, &r.Receipt, &r.App, &r.Expire, &r.Other, &r.Unknown); err != nil {
			return nil, fmt.Errorf("cancel reasons scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func loadLifecycle(ctx context.Context, pool *pgxpool.Pool, q queryParams) (Lifecycle, error) {
	var l Lifecycle
	err := pool.QueryRow(ctx, lifecycleOverallQuery, q.args()...).Scan(
		&l.Buyouts, &l.MedianH, &l.P90H)
	if err != nil {
		return l, fmt.Errorf("lifecycle: %w", err)
	}
	return l, nil
}

func loadLifecycleByCohort(ctx context.Context, pool *pgxpool.Pool, q queryParams) ([]LifecycleCohort, error) {
	rows, err := pool.Query(ctx, lifecycleByCohortQuery, q.args()...)
	if err != nil {
		return nil, fmt.Errorf("lifecycle by cohort: %w", err)
	}
	defer rows.Close()
	var out []LifecycleCohort
	for rows.Next() {
		var l LifecycleCohort
		if err := rows.Scan(&l.Cohort, &l.Buyouts, &l.MedianH, &l.P90H); err != nil {
			return nil, fmt.Errorf("lifecycle by cohort scan: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func loadGeo(ctx context.Context, pool *pgxpool.Pool, q queryParams) ([]GeoRow, error) {
	rows, err := pool.Query(ctx, geoQuery, q.args()...)
	if err != nil {
		return nil, fmt.Errorf("geo: %w", err)
	}
	defer rows.Close()
	var out []GeoRow
	for rows.Next() {
		var g GeoRow
		if err := rows.Scan(&g.City, &g.District, &g.Orders, &g.Buyout, &g.Cancel, &g.InFlight, &g.BuyoutRub); err != nil {
			return nil, fmt.Errorf("geo scan: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func loadTopNm(ctx context.Context, pool *pgxpool.Pool, q queryParams) ([]NmRow, error) {
	rows, err := pool.Query(ctx, topNmQuery, q.args()...)
	if err != nil {
		return nil, fmt.Errorf("top nm: %w", err)
	}
	defer rows.Close()
	var out []NmRow
	for rows.Next() {
		var n NmRow
		if err := rows.Scan(&n.NmID, &n.VendorCode, &n.SubjectName, &n.Orders,
			&n.Buyout, &n.Cancel, &n.Returns, &n.InFlight,
			&n.OrderedRub, &n.BuyoutRub, &n.LostRub); err != nil {
			return nil, fmt.Errorf("top nm scan: %w", err)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func loadCross(ctx context.Context, pool *pgxpool.Pool, q queryParams) ([]CrossRow, error) {
	rows, err := pool.Query(ctx, crossCheckQuery, q.args()...)
	if err != nil {
		return nil, fmt.Errorf("cross check: %w", err)
	}
	defer rows.Close()
	var out []CrossRow
	for rows.Next() {
		var r CrossRow
		if err := rows.Scan(&r.Cohort, &r.V3Orders, &r.InFeed, &r.NotInFee); err != nil {
			return nil, fmt.Errorf("cross check scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// queryParams — общие параметры запросов: allModels и окно since ("" = без него).
type queryParams struct {
	allModels bool
	since     string // YYYY-MM-DD или ""
}

func (q queryParams) args() []any {
	var since any
	if q.since != "" {
		since = q.since
	}
	return []any{q.allModels, since}
}

// sinceOnly — параметры для запросов, где фильтра is_mp нет (v3-таблицы).
func (q queryParams) sinceOnly() []any {
	var since any
	if q.since != "" {
		since = q.since
	}
	return []any{since}
}

// loadAll собирает отчёт целиком. Зрелость когорт проставляется здесь же:
// когорта зрелая, когда «в пути» ≤ 10% её заказов.
func loadAll(ctx context.Context, pool *pgxpool.Pool, q queryParams) (*ReportData, error) {
	d := &ReportData{AllModels: q.allModels, GeneratedAt: nowMoscow().Format("02.01.2006 15:04")}
	var err error
	if d.Coverage, err = loadCoverage(ctx, pool); err != nil {
		return nil, err
	}
	if d.Totals, err = loadFunnelTotals(ctx, pool, q); err != nil {
		return nil, err
	}
	if d.V3, err = loadV3Totals(ctx, pool, q); err != nil {
		return nil, err
	}
	if d.Daily, err = loadDaily(ctx, pool, q); err != nil {
		return nil, err
	}
	if d.Cohorts, err = loadCohorts(ctx, pool, q); err != nil {
		return nil, err
	}
	if d.CohortsV3, err = loadCohortsV3(ctx, pool, q); err != nil {
		return nil, err
	}
	if d.CancelReasons, err = loadCancelReasons(ctx, pool, q); err != nil {
		return nil, err
	}
	if d.Lifecycle, err = loadLifecycle(ctx, pool, q); err != nil {
		return nil, err
	}
	if d.LifecycleByCohort, err = loadLifecycleByCohort(ctx, pool, q); err != nil {
		return nil, err
	}
	if d.Geo, err = loadGeo(ctx, pool, q); err != nil {
		return nil, err
	}
	if d.TopNm, err = loadTopNm(ctx, pool, q); err != nil {
		return nil, err
	}
	if d.Cross, err = loadCross(ctx, pool, q); err != nil {
		return nil, err
	}
	markMaturity(d.Cohorts)
	return d, nil
}

// markMaturity помечает зрелые когорты по факту данных: «в пути» ≤ 10% заказов.
// Возраст ≥ p90 цикла заказ→выкуп оказался недостаточным условием: p90 считается
// только по выкупам, а отмены «истёк срок хранения» доезжают позже — когорта
// в 12–13 сут ещё наполовину в полёте. У незрелых когорт доли выкупа ещё
// изменятся — в отчёте они показываются, но помечены.
func markMaturity(cohorts []CohortFeedRow) {
	for i := range cohorts {
		cohorts[i].Mature = cohorts[i].Orders > 0 &&
			float64(cohorts[i].InFlight)/float64(cohorts[i].Orders) <= 0.10
	}
}

// ── Приёмка на СЦ: строки и лоадеры ──

// SuppliesCohortRow — лаг приёмки одной когорты поставок.
// Nullable-сканы: percentile/min/max над группой не бывают NULL (группа
// непуста по построению), но коррелированная по-заказная медиана может.
type SuppliesCohortRow struct {
	Cohort  string
	Sup     int
	Orders  int
	MedH    float64 // медиана поставочного лага (от первого заказа поставки)
	P90H    float64
	MinH    float64
	MaxH    float64
	SupLe24 float64  // % поставок, принятых ≤ 24ч от их первого заказа
	OrdLe24 float64  // % заказов в таких поставках
	OrdMedH *float64 // медиана лага по заказам когорты (типичный заказ)
}

// SuppliesHistRow — бакет распределения поставочных лагов.
type SuppliesHistRow struct {
	Bucket string
	Sup    int
	Orders int
}

// SuppliesTotals — наполнение fbs_supplies.
type SuppliesTotals struct {
	Total   int
	Scanned int
	Open    int // без scan_dt: ещё в пути до СЦ или закрыты без скана
}

// loadSuppliesCohorts — лаг приёмки по когортам.
func loadSuppliesCohorts(ctx context.Context, pool *pgxpool.Pool) ([]SuppliesCohortRow, error) {
	rows, err := pool.Query(ctx, suppliesCohortsQuery)
	if err != nil {
		return nil, fmt.Errorf("supplies cohorts: %w", err)
	}
	defer rows.Close()

	var out []SuppliesCohortRow
	for rows.Next() {
		var r SuppliesCohortRow
		if err := rows.Scan(&r.Cohort, &r.Sup, &r.Orders,
			&r.MedH, &r.P90H, &r.MinH, &r.MaxH, &r.SupLe24, &r.OrdLe24, &r.OrdMedH); err != nil {
			return nil, fmt.Errorf("supplies cohorts scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// loadSuppliesHist — распределение лагов по бакетам.
func loadSuppliesHist(ctx context.Context, pool *pgxpool.Pool) ([]SuppliesHistRow, error) {
	rows, err := pool.Query(ctx, suppliesHistQuery)
	if err != nil {
		return nil, fmt.Errorf("supplies hist: %w", err)
	}
	defer rows.Close()

	var out []SuppliesHistRow
	for rows.Next() {
		var r SuppliesHistRow
		if err := rows.Scan(&r.Bucket, &r.Sup, &r.Orders); err != nil {
			return nil, fmt.Errorf("supplies hist scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// loadSuppliesTotals — счётчики поставок.
func loadSuppliesTotals(ctx context.Context, pool *pgxpool.Pool) (SuppliesTotals, error) {
	var t SuppliesTotals
	if err := pool.QueryRow(ctx, suppliesTotalsQuery).Scan(&t.Total, &t.Scanned, &t.Open); err != nil {
		return t, fmt.Errorf("supplies totals: %w", err)
	}
	return t, nil
}
