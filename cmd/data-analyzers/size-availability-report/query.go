// query.go — SQL снапшота доступности и payload дашборда.
//
// Источники (RAW, schema public):
//
//   - stocks_daily_warehouses — остатки на складах WB на день-снапшот;
//     грейн (snapshot_date, nm_id, chrt_id, warehouse_id), поэтому остаток
//     размера = SUM(quantity) по (nm_id, chrt_id).
//   - card_sizes — полная сетка размеров карточки (chrt_id глобально уникален);
//     размеры, которых нет в стоках, получают qty=0 (не «отсутствующая строка»).
//   - cards — связка article ↔ nm_id (vendor_code = onec_goods.article,
//     конвенция collection-readiness).
//   - onec_goods — справочник артикулов 1С (бренд, категории, сезон).
//   - onec_rests — остатки 1С в разрезе SKU и склада; берётся ТОЛЬКО склад из
//     конфига (Внуково): розница/«Товар в пути»/зеркала учёта («Склад ОПТ»
//     дублирует Внуково до единицы) в меру не входят.
//
// Universe снапшота — артикулы со свободным остатком (free > 0) на складе
// конфига. Пороги представленности ($2/$4) зашиты параметрами, а не констан-
// тами SQL: дашборд пересчитывает вердикты на клиенте при смене порогов.
package main

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fillDailySQL — наполняет analytical.size_availability_daily снапшотом $1.
// $2 = min_units_per_size (строго больше), $3 = склад 1С, $4 = min_sizes_presented.
// Год выпуска: символы 2-3 артикула (конвенция репо, как allowed_years
// collection-readiness и pkg/config FilterNmIDsByYear).
const fillDailySQL = `
WITH latest_stock AS (SELECT MAX(snapshot_date) AS d FROM public.stocks_daily_warehouses),
latest_rests  AS (SELECT MAX(snapshot_date) AS d FROM public.onec_rests),
wb_size AS (
    SELECT nm_id, chrt_id, SUM(quantity) AS qty
    FROM public.stocks_daily_warehouses
    WHERE snapshot_date = (SELECT d FROM latest_stock)
    GROUP BY nm_id, chrt_id
),
sizes_total AS (
    SELECT nm_id, COUNT(*) AS cnt FROM public.card_sizes GROUP BY nm_id
),
presented AS (
    SELECT nm_id,
           COUNT(*) FILTER (WHERE qty > $2) AS presented_cnt,
           COALESCE(SUM(qty), 0) AS wb_qty
    FROM wb_size
    GROUP BY nm_id
),
rests AS (
    SELECT good_guid, COALESCE(SUM(reserv), 0) AS reserv, COALESCE(SUM(free), 0) AS free
    FROM public.onec_rests
    WHERE snapshot_date = (SELECT d FROM latest_rests) AND storage_name = $3
    GROUP BY good_guid
)
INSERT INTO analytical.size_availability_daily (
    snapshot_date, article, nm_id, brand, category_l1, category_l2, season, year_of_issue,
    sizes_total, sizes_presented, wb_qty_total, onec_free, onec_reserv, has_wb_card, unavailable, updated_at)
SELECT
    $1,
    o.article,
    c.nm_id,
    COALESCE(o.brand, ''),
    COALESCE(NULLIF(o.category_level1_name, ''), o.category, ''),
    COALESCE(o.category_level2_name, ''),
    COALESCE(o.season, ''),
    CASE WHEN substring(o.article FROM 2 FOR 2) ~ '^[0-9]{2}$'
         THEN 2000 + substring(o.article FROM 2 FOR 2)::int ELSE 0 END,
    COALESCE(st.cnt, 0),
    COALESCE(p.presented_cnt, 0),
    COALESCE(p.wb_qty, 0),
    COALESCE(r.free, 0),
    COALESCE(r.reserv, 0),
    c.nm_id IS NOT NULL,
    COALESCE(p.presented_cnt, 0) < $4,
    TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
FROM public.onec_goods o
LEFT JOIN public.cards c      ON c.vendor_code = o.article
LEFT JOIN sizes_total st      ON st.nm_id = c.nm_id
LEFT JOIN presented p         ON p.nm_id = c.nm_id
LEFT JOIN rests r             ON r.good_guid = o.guid
WHERE COALESCE(r.free, 0) > 0
ON CONFLICT (snapshot_date, article) DO UPDATE SET
    nm_id = EXCLUDED.nm_id, brand = EXCLUDED.brand,
    category_l1 = EXCLUDED.category_l1, category_l2 = EXCLUDED.category_l2,
    season = EXCLUDED.season, year_of_issue = EXCLUDED.year_of_issue,
    sizes_total = EXCLUDED.sizes_total, sizes_presented = EXCLUDED.sizes_presented,
    wb_qty_total = EXCLUDED.wb_qty_total, onec_free = EXCLUDED.onec_free,
    onec_reserv = EXCLUDED.onec_reserv, has_wb_card = EXCLUDED.has_wb_card,
    unavailable = EXCLUDED.unavailable, updated_at = EXCLUDED.updated_at`

// fillSizesSQL — полная сетка размеров universe-артикулов снапшота $1
// (включая нулевые — «размер есть в карточке, но на WB его нет»).
// $2 = min_units_per_size.
const fillSizesSQL = `
WITH latest_stock AS (SELECT MAX(snapshot_date) AS d FROM public.stocks_daily_warehouses),
wb_size AS (
    SELECT chrt_id, SUM(quantity) AS qty
    FROM public.stocks_daily_warehouses
    WHERE snapshot_date = (SELECT d FROM latest_stock)
    GROUP BY chrt_id
),
universe AS (
    SELECT DISTINCT nm_id FROM analytical.size_availability_daily
    WHERE snapshot_date = $1 AND nm_id IS NOT NULL
)
INSERT INTO analytical.size_availability_sizes_daily (snapshot_date, nm_id, chrt_id, article, tech_size, wb_qty, is_presented)
SELECT $1, cs.nm_id, cs.chrt_id, COALESCE(c.vendor_code, ''), COALESCE(cs.tech_size, ''),
       COALESCE(w.qty, 0), COALESCE(w.qty, 0) > $2
FROM public.card_sizes cs
JOIN universe u            ON u.nm_id = cs.nm_id
LEFT JOIN public.cards c   ON c.nm_id = cs.nm_id
LEFT JOIN wb_size w        ON w.chrt_id = cs.chrt_id
ON CONFLICT (snapshot_date, nm_id, chrt_id) DO UPDATE SET
    article = EXCLUDED.article, tech_size = EXCLUDED.tech_size,
    wb_qty = EXCLUDED.wb_qty, is_presented = EXCLUDED.is_presented`

// loadArticlesSQL — payload: артикулы последнего собранного снапшота $1.
const loadArticlesSQL = `
SELECT article, COALESCE(nm_id, 0), brand, category_l1, category_l2, season, year_of_issue,
       sizes_total, sizes_presented, wb_qty_total, onec_free, onec_reserv, has_wb_card
FROM analytical.size_availability_daily
WHERE snapshot_date = $1
ORDER BY onec_free DESC, article`

// loadSizesSQL — payload: размеры снапшота $1 (упорядочены по nm — в JS
// группируются одним проходом).
const loadSizesSQL = `
SELECT s.nm_id, s.tech_size, s.wb_qty
FROM analytical.size_availability_sizes_daily s
WHERE s.snapshot_date = $1
ORDER BY s.nm_id, s.chrt_id`

// prevKpiSQL — агрегат предыдущего снапшота (дельты KPI на дашборде).
const prevKpiSQL = `
SELECT snapshot_date,
       COUNT(*) FILTER (WHERE unavailable),
       COALESCE(SUM(onec_free) FILTER (WHERE unavailable), 0),
       COUNT(*) FILTER (WHERE unavailable AND NOT has_wb_card),
       COUNT(*) FILTER (WHERE unavailable AND has_wb_card AND sizes_presented = 0)
FROM analytical.size_availability_daily
WHERE snapshot_date = (
    SELECT MAX(snapshot_date) FROM analytical.size_availability_daily WHERE snapshot_date < $1
)
GROUP BY snapshot_date`

// coverageSQL — свежесть RAW-источников для шапки и fail-fast.
const coverageSQL = `
SELECT
  (SELECT COALESCE(MAX(snapshot_date), '') FROM public.stocks_daily_warehouses),
  (SELECT COALESCE(MAX(snapshot_date), '') FROM public.onec_rests),
  (SELECT COALESCE(MAX(downloaded_at), '') FROM public.onec_goods),
  (SELECT COUNT(*) FROM public.card_sizes)`

// Coverage — свежесть источников.
type Coverage struct {
	StocksDate    string // последний снапшот стоков WB (он же день снапшота)
	RestsDate     string // последний снапшот остатков 1С
	OnecGoodsDate string // последняя выгрузка справочника 1С
	CardSizes     int64  // размеров карточек всего
}

// articleRow — строка снапшота (артикул).
type articleRow struct {
	Article        string
	NmID           int64 // 0 = карточки WB нет
	Brand          string
	Cat1           string
	Cat2           string
	Season         string
	Year           int
	SizesTotal     int
	SizesPresented int
	WbQty          int64
	OnecFree       int64
	OnecReserv     int64
	HasCard        bool
}

// sizeRow — строка пер-размерной детализации.
type sizeRow struct {
	NmID  int64
	Size  string
	Qty   int64
}

func loadCoverage(ctx context.Context, pool *pgxpool.Pool) (Coverage, error) {
	var c Coverage
	err := pool.QueryRow(ctx, coverageSQL).Scan(
		&c.StocksDate, &c.RestsDate, &c.OnecGoodsDate, &c.CardSizes)
	if err != nil {
		return c, fmt.Errorf("coverage: %w", err)
	}
	return c, nil
}

func fillDaily(ctx context.Context, pool *pgxpool.Pool, date string, cfg *Config) (int64, error) {
	tag, err := pool.Exec(ctx, fillDailySQL,
		date, cfg.MinUnitsPerSize, cfg.OnecStorage, cfg.MinSizesPresented)
	if err != nil {
		return 0, fmt.Errorf("fill daily: %w", err)
	}
	return tag.RowsAffected(), nil
}

func fillSizes(ctx context.Context, pool *pgxpool.Pool, date string, minUnits int) (int64, error) {
	tag, err := pool.Exec(ctx, fillSizesSQL, date, minUnits)
	if err != nil {
		return 0, fmt.Errorf("fill sizes: %w", err)
	}
	return tag.RowsAffected(), nil
}

// trimRetention удаляет снапшоты старше retentionDays из обеих таблиц.
// Возвращает число удалённых дней-снапшотов; чистка не фатальна для отчёта.
func trimRetention(ctx context.Context, pool *pgxpool.Pool, retentionDays int) int {
	if retentionDays <= 0 {
		return 0
	}
	cutoff := nowMoscow().AddDate(0, 0, -retentionDays).Format("2006-01-02")
	var n int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(DISTINCT snapshot_date) FROM analytical.size_availability_daily
		WHERE snapshot_date < $1`, cutoff).Scan(&n); err != nil {
		return 0
	}
	if n == 0 {
		return 0
	}
	for _, table := range []string{
		`analytical.size_availability_daily`, `analytical.size_availability_sizes_daily`} {
		if _, err := pool.Exec(ctx,
			fmt.Sprintf(`DELETE FROM %s WHERE snapshot_date < $1`, table), cutoff); err != nil {
			return 0
		}
	}
	return n
}

func loadArticles(ctx context.Context, pool *pgxpool.Pool, date string) ([]articleRow, error) {
	rows, err := pool.Query(ctx, loadArticlesSQL, date)
	if err != nil {
		return nil, fmt.Errorf("load articles: %w", err)
	}
	defer rows.Close()

	var out []articleRow
	for rows.Next() {
		var r articleRow
		if err := rows.Scan(&r.Article, &r.NmID, &r.Brand, &r.Cat1, &r.Cat2, &r.Season, &r.Year,
			&r.SizesTotal, &r.SizesPresented, &r.WbQty, &r.OnecFree, &r.OnecReserv, &r.HasCard); err != nil {
			return nil, fmt.Errorf("articles scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func loadSizes(ctx context.Context, pool *pgxpool.Pool, date string) ([]sizeRow, error) {
	rows, err := pool.Query(ctx, loadSizesSQL, date)
	if err != nil {
		return nil, fmt.Errorf("load sizes: %w", err)
	}
	defer rows.Close()

	var out []sizeRow
	for rows.Next() {
		var r sizeRow
		if err := rows.Scan(&r.NmID, &r.Size, &r.Qty); err != nil {
			return nil, fmt.Errorf("sizes scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// loadPrevKpi — агрегат прошлого снапшота; nil, если истории ещё нет.
func loadPrevKpi(ctx context.Context, pool *pgxpool.Pool, date string) (*PrevKpi, error) {
	var p PrevKpi
	err := pool.QueryRow(ctx, prevKpiSQL, date).Scan(
		&p.Date, &p.Unavailable, &p.OnecFree, &p.NoCard, &p.ZeroSizes)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("prev kpi: %w", err)
	}
	return &p, nil
}
