package main

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// fillSQL — пересборка окна analytical.region_sales_daily из RAW-таблиц.
//
// Инварианты (прикреплены тестом TestFillSQLGuards):
//   - окно ленты — параметрами $1/$2 (timestamptz), не вычисляется в SQL:
//     Go-сторона отвечает за МСК-границы дней и их же передаёт в DELETE;
//   - stat_day — МСК-дата ТЕКУЩЕГО статуса юнита (лента отдаёт события так);
//   - статусы: buyout ≈ «Выкупили, шт.» старого отчёта; возвраты — return
//     И returnDefective; created/cancel — операционные счётчики сверх старого;
//   - гео: город/ФО — лента (destination_*), регион/обл./страна — orders по
//     srid с fallback на operational_sales (выкуп мог ещё не доехать в orders);
//   - деньги: seller_price — только по выкупам (валовая цена продавца);
//     price_with_disc / for_pay — из operational_sales с дискриминатором
//     sale_id 'S*'/'R*' (возвраты положительные — нетто = продажи − возвраты);
//   - os_rows — юниты со сшитой строкой статистики (COUNT по feed-строкам с
//     непустым os-матчем): контроль денежного покрытия выкупов.
const fillSQL = `
INSERT INTO analytical.region_sales_daily (
    stat_day, nm_id, destination_city, destination_district,
    region_name, oblast_okrug_name, country_name, is_mp,
    events_qty, created_qty, buyout_qty, cancel_qty, return_qty,
    seller_price_sum, price_with_disc_sum, for_pay_sales_sum, for_pay_returns_sum,
    os_rows, updated_at
)
WITH feed AS (
    SELECT f.srid, f.nm_id, f.status, f.updated_at, f.seller_price,
           COALESCE(f.destination_city, '')     AS destination_city,
           COALESCE(f.destination_district, '') AS destination_district,
           f.is_mp
    FROM public.order_feed f
    WHERE f.updated_at >= $1::timestamptz
      AND f.updated_at <  $2::timestamptz
),
os_agg AS (
    SELECT os.srid,
           MAX(os.region_name)       AS region_name,
           MAX(os.oblast_okrug_name) AS oblast_okrug_name,
           MAX(os.country_name)      AS country_name,
           COALESCE(SUM(os.price_with_disc) FILTER (WHERE os.sale_id LIKE 'S%'), 0) AS pwd_sales,
           COALESCE(SUM(os.for_pay)         FILTER (WHERE os.sale_id LIKE 'S%'), 0) AS fp_sales,
           COALESCE(SUM(os.for_pay)         FILTER (WHERE os.sale_id LIKE 'R%'), 0) AS fp_returns
    FROM public.operational_sales os
    JOIN feed ON feed.srid = os.srid
    GROUP BY os.srid
)
SELECT
    to_char(feed.updated_at AT TIME ZONE 'Europe/Moscow', 'YYYY-MM-DD') AS stat_day,
    feed.nm_id,
    feed.destination_city,
    feed.destination_district,
    COALESCE(NULLIF(o.region_name, ''),       NULLIF(a.region_name, ''),       '') AS region_name,
    COALESCE(NULLIF(o.oblast_okrug_name, ''), NULLIF(a.oblast_okrug_name, ''), '') AS oblast_okrug_name,
    COALESCE(NULLIF(o.country_name, ''),      NULLIF(a.country_name, ''),      '') AS country_name,
    feed.is_mp,
    COUNT(*)                                                           AS events_qty,
    COUNT(*) FILTER (WHERE feed.status = 'created')                    AS created_qty,
    COUNT(*) FILTER (WHERE feed.status = 'buyout')                     AS buyout_qty,
    COUNT(*) FILTER (WHERE feed.status = 'cancel')                     AS cancel_qty,
    COUNT(*) FILTER (WHERE feed.status IN ('return', 'returnDefective')) AS return_qty,
    COALESCE(SUM(feed.seller_price) FILTER (WHERE feed.status = 'buyout'), 0) AS seller_price_sum,
    COALESCE(SUM(a.pwd_sales), 0)   AS price_with_disc_sum,
    COALESCE(SUM(a.fp_sales), 0)    AS for_pay_sales_sum,
    COALESCE(SUM(a.fp_returns), 0)  AS for_pay_returns_sum,
    COUNT(a.srid)                   AS os_rows,
    TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS') AS updated_at
FROM feed
LEFT JOIN public.orders o ON o.srid = feed.srid
LEFT JOIN os_agg a        ON a.srid  = feed.srid
GROUP BY 1, 2, 3, 4, 5, 6, 7, 8
`

// windowStats — сводка пересобранного окна (для консоли).
type windowStats struct {
	Rows    int64
	Buyouts int64
	Returns int64
}

// fillWindow атомарно пересобирает окно [from, to] (дни МСК, включительно):
// DELETE строк окна + INSERT из ленты, одной транзакцией — конкурентный
// читатель не видит половину окна.
func fillWindow(ctx context.Context, db *pgxpool.Pool, from, to time.Time) (windowStats, error) {
	var st windowStats
	fromDay := from.Format("2006-01-02")
	toDay := to.Format("2006-01-02")
	// Границы окна в timestamptz: [from 00:00 MSK, to+1 00:00 MSK).
	fromTS := from.Format("2006-01-02 15:04:05-07:00")
	toTS := to.AddDate(0, 0, 1).Format("2006-01-02 15:04:05-07:00")

	tx, err := db.Begin(ctx)
	if err != nil {
		return st, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`DELETE FROM analytical.region_sales_daily WHERE stat_day BETWEEN $1 AND $2`,
		fromDay, toDay); err != nil {
		return st, fmt.Errorf("delete window: %w", err)
	}
	if _, err := tx.Exec(ctx, fillSQL, fromTS, toTS); err != nil {
		return st, fmt.Errorf("fill window: %w", err)
	}
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*), COALESCE(SUM(buyout_qty), 0), COALESCE(SUM(return_qty), 0)
		FROM analytical.region_sales_daily
		WHERE stat_day BETWEEN $1 AND $2`,
		fromDay, toDay).Scan(&st.Rows, &st.Buyouts, &st.Returns); err != nil {
		return st, fmt.Errorf("summary: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return st, fmt.Errorf("commit: %w", err)
	}
	return st, nil
}

// minFeedTS — МСК-дата-время самой старой строки ленты ('' = пусто), для --rebuild-all.
func minFeedTS(ctx context.Context, db *pgxpool.Pool) (string, error) {
	var s string
	err := db.QueryRow(ctx, `
		SELECT COALESCE(TO_CHAR(MIN(updated_at) AT TIME ZONE 'Europe/Moscow', 'YYYY-MM-DD HH24:MI:SS'), '')
		FROM public.order_feed`).Scan(&s)
	if err != nil {
		return "", fmt.Errorf("min(order_feed.updated_at): %w", err)
	}
	return s, nil
}

// regionTop — строка топ-регионов окна (сводка в консоль).
type regionTop struct {
	Region string
	Qty    int64
	Net    float64
}

func topRegions(ctx context.Context, db *pgxpool.Pool, from, to time.Time) ([]regionTop, error) {
	rows, err := db.Query(ctx, `
		SELECT region_name, SUM(buyout_qty), SUM(for_pay_sales_sum - for_pay_returns_sum)
		FROM analytical.region_sales_daily
		WHERE stat_day BETWEEN $1 AND $2 AND region_name <> ''
		GROUP BY region_name
		ORDER BY SUM(buyout_qty) DESC
		LIMIT 5`,
		from.Format("2006-01-02"), to.Format("2006-01-02"))
	if err != nil {
		return nil, fmt.Errorf("top regions: %w", err)
	}
	defer rows.Close()

	var out []regionTop
	for rows.Next() {
		var r regionTop
		if err := rows.Scan(&r.Region, &r.Qty, &r.Net); err != nil {
			return nil, fmt.Errorf("scan top region: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
