// query.go — read-only SQL остатков WB в разрезе склад × сезон и структуры строк.
//
// Два запроса:
//   - aggregateQuery — агрегат на стороне PG: склад × сезон → 3 типа остатков
//     (quantity / in_way_to_client / in_way_from_client). Это основа отчёта.
//   - detailsQuery — drill-down: артикул × склад × сезон (для листа «Детали»).
//
// Цепочка сезона: stocks.nm_id → cards.nm_id → cards.vendor_code = onec_goods.article
// → onec_goods.season. Покрытие ~86% nm имеют сезон; остальные попадают в бакет
// «(без сезона)» через COALESCE.
package main

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AggRow — одна строка агрегата: склад × сезон → 3 типа остатков.
type AggRow struct {
	WarehouseID     int64
	WarehouseName   string
	RegionName      string
	Season          string
	OnStock         int64 // quantity — «на складе»
	InWayToClient   int64 // «в пути к клиенту»
	InWayFromClient int64 // «в пути от клиента»
}

// DetailRow — drill-down строка: артикул × склад × сезон.
type DetailRow struct {
	VendorCode      string
	NmID            int64
	Brand           string
	SubjectName     string
	Season          string
	WarehouseName   string
	RegionName      string
	OnStock         int64
	InWayToClient   int64
	InWayFromClient int64
}

// Total возвращает суммарные остатки строки (для колонки «Всего»).
func (r AggRow) Total() int64 { return r.OnStock + r.InWayToClient + r.InWayFromClient }

// noSeason — бакет для nm без сезона (NULL/пусто в onec_goods.season).
const noSeason = "(без сезона)"

// aggregateQuery — основной агрегат на стороне PG.
//
// snapshot_date хранится как TEXT в ISO-формате (YYYY-MM-DD), поэтому лексикографическое
// сравнение/max корректны и используются индексы (idx_stocks_date). GROUP BY по
// warehouse + season даёт ровно одну строку на пару.
const aggregateQuery = `
SELECT
  s.warehouse_id,
  COALESCE(NULLIF(s.warehouse_name,''), 'Склад ' || s.warehouse_id::text) AS warehouse_name,
  COALESCE(NULLIF(s.region_name,''), '(без региона)') AS region_name,
  COALESCE(NULLIF(TRIM(o.season),''), $2) AS season,
  COALESCE(SUM(s.quantity),0)         AS on_stock,
  COALESCE(SUM(s.in_way_to_client),0) AS in_way_to_client,
  COALESCE(SUM(s.in_way_from_client),0) AS in_way_from_client
FROM stocks_daily_warehouses s
LEFT JOIN cards      c ON c.nm_id = s.nm_id
LEFT JOIN onec_goods o ON o.article = c.vendor_code
WHERE s.snapshot_date = $1
GROUP BY s.warehouse_id, warehouse_name, region_name, o.season
ORDER BY region_name, warehouse_name, season`

// detailsQuery — drill-down: одна строка на артикул × склад (для листа «Детали»).
//
// Тот же джойн, но без агрегации — чтобы финдир мог провалиться до конкретной позиции.
// Артикулы без карточки WB (c.vendor_code IS NULL) сюда не попадают (INNER-семантика
// через равенство в WHERE не нужна — LEFT JOIN cards даёт NULL, фильтруем только
// действительно пустые строки остатков).
const detailsQuery = `
SELECT
  COALESCE(c.vendor_code, '')        AS vendor_code,
  COALESCE(c.nm_id, 0)               AS nm_id,
  COALESCE(NULLIF(c.brand,''), '')   AS brand,
  COALESCE(NULLIF(c.subject_name,''), '') AS subject_name,
  COALESCE(NULLIF(TRIM(o.season),''), $2) AS season,
  COALESCE(NULLIF(s.warehouse_name,''), 'Склад ' || s.warehouse_id::text) AS warehouse_name,
  COALESCE(NULLIF(s.region_name,''), '(без региона)') AS region_name,
  COALESCE(SUM(s.quantity),0)         AS on_stock,
  COALESCE(SUM(s.in_way_to_client),0) AS in_way_to_client,
  COALESCE(SUM(s.in_way_from_client),0) AS in_way_from_client
FROM stocks_daily_warehouses s
LEFT JOIN cards      c ON c.nm_id = s.nm_id
LEFT JOIN onec_goods o ON o.article = c.vendor_code
WHERE s.snapshot_date = $1
GROUP BY c.vendor_code, c.nm_id, c.brand, c.subject_name, o.season,
         s.warehouse_id, s.warehouse_name, s.region_name
ORDER BY COALESCE(NULLIF(TRIM(o.season),''), $2), c.vendor_code, s.region_name, s.warehouse_name`

// latestSnapshotDate возвращает последний доступный день среза (MAX как text).
//
// Используется когда --date не задан. Возвращает ("", nil) если таблица пуста.
func latestSnapshotDate(ctx context.Context, conn *pgxpool.Pool) (string, error) {
	const q = `SELECT MAX(snapshot_date) FROM stocks_daily_warehouses`
	var d *string
	if err := conn.QueryRow(ctx, q).Scan(&d); err != nil {
		return "", fmt.Errorf("latest snapshot_date: %w", err)
	}
	if d == nil {
		return "", nil
	}
	return *d, nil
}

// snapshotExists проверяет, что срез на дату есть и непустой.
//
// Возвращает число строк. Если 0 — срез отсутствует (отчёт не из чего строить).
func snapshotExists(ctx context.Context, conn *pgxpool.Pool, date string) (int64, error) {
	const q = `SELECT COUNT(*) FROM stocks_daily_warehouses WHERE snapshot_date = $1`
	var n int64
	if err := conn.QueryRow(ctx, q, date).Scan(&n); err != nil {
		return 0, fmt.Errorf("snapshot count %s: %w", date, err)
	}
	return n, nil
}

// loadAggregate выполняет агрегирующий запрос и возвращает строки склад × сезон.
func loadAggregate(ctx context.Context, conn *pgxpool.Pool, date string) ([]AggRow, error) {
	rows, err := conn.Query(ctx, aggregateQuery, date, noSeason)
	if err != nil {
		return nil, fmt.Errorf("aggregate query: %w", err)
	}
	defer rows.Close()
	var out []AggRow
	for rows.Next() {
		var r AggRow
		if err := rows.Scan(
			&r.WarehouseID, &r.WarehouseName, &r.RegionName, &r.Season,
			&r.OnStock, &r.InWayToClient, &r.InWayFromClient,
		); err != nil {
			return nil, fmt.Errorf("aggregate scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// loadDetails выполняет drill-down запрос и возвращает строки артикул × склад.
func loadDetails(ctx context.Context, conn *pgxpool.Pool, date string) ([]DetailRow, error) {
	rows, err := conn.Query(ctx, detailsQuery, date, noSeason)
	if err != nil {
		return nil, fmt.Errorf("details query: %w", err)
	}
	defer rows.Close()
	var out []DetailRow
	// Собираем типы один раз — все колонки SUM() не бывают NULL (COALESCE), скан в int64 безопасен.
	for rows.Next() {
		var r DetailRow
		if err := rows.Scan(
			&r.VendorCode, &r.NmID, &r.Brand, &r.SubjectName, &r.Season,
			&r.WarehouseName, &r.RegionName,
			&r.OnStock, &r.InWayToClient, &r.InWayFromClient,
		); err != nil {
			return nil, fmt.Errorf("details scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// verifyTotal возвращает контрольную сумму quantity по дате для сверки отчёта.
//
// Простая сумма без джойнов — независима от цепочки сезона, поэтому совпадение с
// суммой OnStock по листу «Сводка» подтверждает, что агрегация ничего не потеряла.
func verifyTotal(ctx context.Context, conn *pgxpool.Pool, date string) (int64, int64, int64, error) {
	const q = `SELECT COALESCE(SUM(quantity),0), COALESCE(SUM(in_way_to_client),0), COALESCE(SUM(in_way_from_client),0)
	           FROM stocks_daily_warehouses WHERE snapshot_date = $1`
	var onStock, toClient, fromClient int64
	err := conn.QueryRow(ctx, q, date).Scan(&onStock, &toClient, &fromClient)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("verify total: %w", err)
	}
	return onStock, toClient, fromClient, nil
}
