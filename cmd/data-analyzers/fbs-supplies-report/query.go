// query.go — read-only SQL по поставкам FBS и структуры payload дашборда.
//
// Источники (только SELECT, ничего не пишем):
//
//   - public.fbs_supplies — поставки GET /api/v3/supplies
//     (docs/wb_api_swagger/03-orders-fbs.yaml:2089). scan_dt = «дата
//     сканирования поставки или первого заказа» = приёмка на
//     сортировочном центре (:4561) — момент, когда обязательства
//     продавца считаются выполненными. closed_at = закрытие/передача.
//   - public.fbs_orders — сборочные задания GET /api/v3/orders; строки
//     несут supply_id — текущее назначение задания на момент загрузки.
//   - public.fbs_orders_status_log — наш лог переходов статусов
//     (first_seen): по нему отделяем отмены ДО приёмки поставки
//     (не дошли, из SLA-знаменателя исключаются) от отмен ПОСЛЕ
//     (отказ в ПВЗ — задание было отгружено в срок очереди).
//
// Зерно куба — поставка: ключевые моменты времени + распределение
// ожиданий заданий по бакетам часов (0–6/6–12/…/>72). Порог SLA (24ч)
// в SQL не зашит: бакеты выровнены по 12/24/36, пересчёт доли «в срок»
// делает браузер при смене порога без обращения к данным.
//
// Дни — МСК: (ts AT TIME ZONE 'Europe/Moscow')::date.
package main

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// suppliesQuery — куб по поставкам, одна строка на поставку с заданиями.
// $1 — since по дню формирования ("" → NULL → без ограничения).
//
// Отменённые до приёмки: LEFT JOIN по логу статусов даёт first_seen первой
// отмены; задание исключается из SLA-бакетов, если отмена случилась раньше
// scan_dt. Нет строки лога (поставки до 24.08) — задание остаётся в
// знаменателе (правило «считать всех», см. «Методику»).
const suppliesQuery = `
WITH canc AS (
  SELECT l.order_id, min(l.first_seen) AS first_cancel_seen
  FROM public.fbs_orders_status_log l
  WHERE l.wb_status IN ('canceled', 'canceled_by_client', 'declined_by_client')
  GROUP BY 1
)
SELECT
  sp.supply_id,
  COALESCE(NULLIF(sp.name, ''), sp.supply_id),
  (sp.created_at AT TIME ZONE 'Europe/Moscow')::date::text,
  to_char(sp.created_at AT TIME ZONE 'Europe/Moscow', 'YYYY-MM-DD HH24:MI'),
  COALESCE(to_char(min(o.created_at) AT TIME ZONE 'Europe/Moscow', 'YYYY-MM-DD HH24:MI'), ''),
  COALESCE(to_char(sp.closed_at AT TIME ZONE 'Europe/Moscow', 'YYYY-MM-DD HH24:MI'), ''),
  COALESCE(to_char(sp.scan_dt AT TIME ZONE 'Europe/Moscow', 'YYYY-MM-DD HH24:MI'), ''),
  (sp.reject_dt IS NOT NULL)::int,
  COALESCE(round((EXTRACT(EPOCH FROM (sp.scan_dt - min(o.created_at))) / 3600.0)::numeric, 1), -1),
  (count(o.id))::int,
  (count(o.id) FILTER (WHERE c.first_cancel_seen < sp.scan_dt))::int,
  (count(o.id) FILTER (WHERE c.first_cancel_seen IS NOT NULL AND c.first_cancel_seen >= sp.scan_dt))::int,
  (count(o.id) FILTER (WHERE sp.scan_dt IS NOT NULL AND (c.first_cancel_seen IS NULL OR c.first_cancel_seen >= sp.scan_dt)
    AND EXTRACT(EPOCH FROM (sp.scan_dt - o.created_at)) / 3600.0 <= 6))::int,
  (count(o.id) FILTER (WHERE sp.scan_dt IS NOT NULL AND (c.first_cancel_seen IS NULL OR c.first_cancel_seen >= sp.scan_dt)
    AND EXTRACT(EPOCH FROM (sp.scan_dt - o.created_at)) / 3600.0 > 6
    AND EXTRACT(EPOCH FROM (sp.scan_dt - o.created_at)) / 3600.0 <= 12))::int,
  (count(o.id) FILTER (WHERE sp.scan_dt IS NOT NULL AND (c.first_cancel_seen IS NULL OR c.first_cancel_seen >= sp.scan_dt)
    AND EXTRACT(EPOCH FROM (sp.scan_dt - o.created_at)) / 3600.0 > 12
    AND EXTRACT(EPOCH FROM (sp.scan_dt - o.created_at)) / 3600.0 <= 18))::int,
  (count(o.id) FILTER (WHERE sp.scan_dt IS NOT NULL AND (c.first_cancel_seen IS NULL OR c.first_cancel_seen >= sp.scan_dt)
    AND EXTRACT(EPOCH FROM (sp.scan_dt - o.created_at)) / 3600.0 > 18
    AND EXTRACT(EPOCH FROM (sp.scan_dt - o.created_at)) / 3600.0 <= 24))::int,
  (count(o.id) FILTER (WHERE sp.scan_dt IS NOT NULL AND (c.first_cancel_seen IS NULL OR c.first_cancel_seen >= sp.scan_dt)
    AND EXTRACT(EPOCH FROM (sp.scan_dt - o.created_at)) / 3600.0 > 24
    AND EXTRACT(EPOCH FROM (sp.scan_dt - o.created_at)) / 3600.0 <= 36))::int,
  (count(o.id) FILTER (WHERE sp.scan_dt IS NOT NULL AND (c.first_cancel_seen IS NULL OR c.first_cancel_seen >= sp.scan_dt)
    AND EXTRACT(EPOCH FROM (sp.scan_dt - o.created_at)) / 3600.0 > 36
    AND EXTRACT(EPOCH FROM (sp.scan_dt - o.created_at)) / 3600.0 <= 48))::int,
  (count(o.id) FILTER (WHERE sp.scan_dt IS NOT NULL AND (c.first_cancel_seen IS NULL OR c.first_cancel_seen >= sp.scan_dt)
    AND EXTRACT(EPOCH FROM (sp.scan_dt - o.created_at)) / 3600.0 > 48
    AND EXTRACT(EPOCH FROM (sp.scan_dt - o.created_at)) / 3600.0 <= 72))::int,
  (count(o.id) FILTER (WHERE sp.scan_dt IS NOT NULL AND (c.first_cancel_seen IS NULL OR c.first_cancel_seen >= sp.scan_dt)
    AND EXTRACT(EPOCH FROM (sp.scan_dt - o.created_at)) / 3600.0 > 72))::int
FROM public.fbs_supplies sp
JOIN public.fbs_orders o ON o.supply_id = sp.supply_id AND o.cross_border_type = 0
LEFT JOIN canc c ON c.order_id = o.id
WHERE ($1::date IS NULL OR (sp.created_at AT TIME ZONE 'Europe/Moscow')::date >= $1::date)
GROUP BY sp.supply_id, sp.name, sp.created_at, sp.closed_at, sp.scan_dt, sp.reject_dt
ORDER BY sp.created_at, sp.supply_id`

// suppliesCoverageQuery — свежесть источников для шапки и fail-fast.
const suppliesCoverageQuery = `
SELECT
  (SELECT count(*) FROM public.fbs_supplies),
  (SELECT count(*) FROM public.fbs_orders WHERE cross_border_type = 0),
  (SELECT (min(created_at) AT TIME ZONE 'Europe/Moscow')::date::text FROM public.fbs_supplies),
  (SELECT (max(created_at) AT TIME ZONE 'Europe/Moscow')::date::text FROM public.fbs_supplies)`

// HistBuckets — подписи бакетов ожидания задания (часы до приёмки поставки).
var histBuckets = [8]string{"0–6", "6–12", "12–18", "18–24", "24–36", "36–48", "48–72", ">72"}

// histUpper — верхние границы бакетов в часах; пороги UI (12/24/36) совпадают
// с границами, поэтому доля «в срок» при смене порога — сумма целых бакетов.
var histUpper = [8]int{6, 12, 18, 24, 36, 48, 72, 1 << 30}

// Coverage — счётчики и диапазон дат формирования.
type Coverage struct {
	Supplies int64
	Tasks    int64
	From     *string
	To       *string
}

// supplyRow — строка куба (одна поставка).
type supplyRow struct {
	ID         string
	Name       string
	Day        string // день формирования, YYYY-MM-DD МСК
	Created    string // YYYY-MM-DD HH:MM МСК
	FirstTask  string // момент первого задания (старт SLA-часов); '' нет
	Closed     string // закрытие поставки; '' ещё открыта
	Scanned    string // приёмка на СЦ; '' ждёт приёмки
	Rejected   int
	LagH       float64 // scan_dt − первое задание, ч; -1 = открыт
	NTasks     int
	CancelPre  int    // отменено ДО приёмки (из SLA-знаменателя исключено)
	CancelPost int    // отменено после приёмки (отказ в ПВЗ) — в знаменателе
	Hist       [8]int // распределение ожиданий заданий, часы
}

// SuppliesData — весь payload дашборда.
type SuppliesData struct {
	Meta Meta `json:"meta"`
	Sups Sups `json:"sups"`
}

// Meta — служебные данные (шапка, бейдж БД, период).
type Meta struct {
	GeneratedAt string `json:"generated_at"` // МСК, «02.01.2006 15:04»
	Db          string `json:"db"`           // имя БД (не прод → бейдж «ТЕСТ»)
	From        string `json:"from"`         // дни формирования, YYYY-MM-DD
	To          string `json:"to"`
	Supplies    int    `json:"supplies"`
	Tasks       int    `json:"tasks"`
	Open        int    `json:"open"` // поставок ждёт приёмки
}

// Sups — поставки колонками (колонка = массив одной длины).
type Sups struct {
	ID         []string  `json:"id"`
	Day        []string  `json:"day"` // YYYY-MM-DD, день формирования
	Name       []string  `json:"name"`
	Created    []string  `json:"created"` // YYYY-MM-DD HH:MM
	First      []string  `json:"first"`   // первое задание (старт SLA), YYYY-MM-DD HH:MM
	Closed     []string  `json:"closed"`  // '' — ещё не закрыта
	Scanned    []string  `json:"scanned"` // '' — ждёт приёмки
	Rejected   []int     `json:"rejected"`
	LagH       []float64 `json:"lag_h"` // -1 — открыт
	NTasks     []int     `json:"n_tasks"`
	CancelPre  []int     `json:"cancel_pre"`
	CancelPost []int     `json:"cancel_post"`
	Hist       [][8]int  `json:"hist"`
}

// loadCoverage — шапка и fail-fast.
func loadCoverage(ctx context.Context, pool *pgxpool.Pool) (Coverage, error) {
	var c Coverage
	err := pool.QueryRow(ctx, suppliesCoverageQuery).Scan(
		&c.Supplies, &c.Tasks, &c.From, &c.To)
	if err != nil {
		return c, fmt.Errorf("coverage: %w", err)
	}
	return c, nil
}

// loadSupplies — куб поставок. since — нижняя граница дня формирования
// ("" = весь диапазон).
func loadSupplies(ctx context.Context, pool *pgxpool.Pool, since string) ([]supplyRow, error) {
	var sinceArg any
	if since != "" {
		sinceArg = since
	}
	rows, err := pool.Query(ctx, suppliesQuery, sinceArg)
	if err != nil {
		return nil, fmt.Errorf("supplies cube: %w", err)
	}
	defer rows.Close()

	var out []supplyRow
	for rows.Next() {
		var r supplyRow
		if err := rows.Scan(&r.ID, &r.Name, &r.Day, &r.Created, &r.FirstTask,
			&r.Closed, &r.Scanned, &r.Rejected, &r.LagH,
			&r.NTasks, &r.CancelPre, &r.CancelPost,
			&r.Hist[0], &r.Hist[1], &r.Hist[2], &r.Hist[3],
			&r.Hist[4], &r.Hist[5], &r.Hist[6], &r.Hist[7]); err != nil {
			return nil, fmt.Errorf("supplies scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
