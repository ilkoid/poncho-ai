package main

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// maSKUDailySchema defines the flat SKU snapshot table in bi2.db.
// All product identifiers, attributes, stock, MA, derived metrics, and risk flags
// are denormalized into a single table so PowerBI can query without JOINs.
const maSKUDailySchema = `
CREATE TABLE IF NOT EXISTS ma_sku_daily (
    snapshot_date   TEXT NOT NULL,
    nm_id           INTEGER NOT NULL,
    chrt_id         INTEGER NOT NULL,
    region_name     TEXT NOT NULL,
    tech_size       TEXT DEFAULT '',

    -- Product identifiers
    article         TEXT DEFAULT '',
    identifier      TEXT DEFAULT '',
    vendor_code     TEXT DEFAULT '',

    -- Product attributes
    name            TEXT DEFAULT '',
    brand           TEXT DEFAULT '',
    type            TEXT DEFAULT '',
    category        TEXT DEFAULT '',
    category_level1 TEXT DEFAULT '',
    category_level2 TEXT DEFAULT '',
    sex             TEXT DEFAULT '',
    season          TEXT DEFAULT '',
    color           TEXT DEFAULT '',
    collection      TEXT DEFAULT '',

    -- Stock
    stock_qty       INTEGER DEFAULT 0,
    total_sizes     INTEGER DEFAULT 0,
    sizes_in_stock  INTEGER DEFAULT 0,
    fill_pct        REAL DEFAULT 0,

    -- MA (global per barcode)
    ma_3            REAL,
    ma_7            REAL,
    ma_14           REAL,
    ma_28           REAL,

    -- Derived
    sdr_days        REAL,
    trend_pct       REAL,

    -- Flags
    risk            INTEGER DEFAULT 0,
    critical        INTEGER DEFAULT 0,
    out_of_stock    INTEGER DEFAULT 0,
    broken_grid     INTEGER DEFAULT 0,

    computed_at     TEXT DEFAULT CURRENT_TIMESTAMP,

    PRIMARY KEY (snapshot_date, nm_id, chrt_id, region_name)
);

CREATE INDEX IF NOT EXISTS idx_msd_snapshot_date ON ma_sku_daily(snapshot_date);
CREATE INDEX IF NOT EXISTS idx_msd_nm_id ON ma_sku_daily(nm_id);
CREATE INDEX IF NOT EXISTS idx_msd_article ON ma_sku_daily(article);
CREATE INDEX IF NOT EXISTS idx_msd_vendor_code ON ma_sku_daily(vendor_code);
CREATE INDEX IF NOT EXISTS idx_msd_region ON ma_sku_daily(region_name, snapshot_date);
CREATE INDEX IF NOT EXISTS idx_msd_brand ON ma_sku_daily(brand);
CREATE INDEX IF NOT EXISTS idx_msd_category ON ma_sku_daily(category);
CREATE INDEX IF NOT EXISTS idx_msd_risk_flags ON ma_sku_daily(critical DESC, risk DESC, out_of_stock DESC);
CREATE INDEX IF NOT EXISTS idx_msd_date_region ON ma_sku_daily(snapshot_date, region_name);
`

// ResultsRepo manages the bi.db — stores flat SKU MA snapshots.
type ResultsRepo struct {
	db *sql.DB
}

// NewResultsRepo opens/creates the bi.db with WAL mode and creates schema.
func NewResultsRepo(dbPath string) (*ResultsRepo, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open results db: %w", err)
	}

	pragmas := []struct{ key, val string }{
		{"journal_mode", "WAL"},
		{"synchronous", "NORMAL"},
		{"cache_size", "-64000"},
		{"temp_store", "MEMORY"},
	}
	for _, p := range pragmas {
		if _, err := db.Exec(fmt.Sprintf("PRAGMA %s = %s", p.key, p.val)); err != nil {
			if p.key == "journal_mode" {
				// WAL fails on WSL2 Windows mounts (NTFS via 9P) — fallback to DELETE
				if _, fbErr := db.Exec("PRAGMA journal_mode = DELETE"); fbErr != nil {
					db.Close()
					return nil, fmt.Errorf("PRAGMA journal_mode WAL failed: %v, DELETE fallback also failed: %w", err, fbErr)
				}
				continue
			}
			db.Close()
			return nil, fmt.Errorf("PRAGMA %s: %w", p.key, err)
		}
	}

	if _, err := db.Exec(maSKUDailySchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create ma_sku_daily schema: %w", err)
	}

	return &ResultsRepo{db: db}, nil
}

// HasSnapshot checks if a snapshot already exists for the given date.
func (r *ResultsRepo) HasSnapshot(ctx context.Context, date string) (bool, error) {
	var count int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM ma_sku_daily WHERE snapshot_date = ? LIMIT 1`, date,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check existing snapshot: %w", err)
	}
	return count > 0, nil
}

// DeleteSnapshot removes all rows for the given date (used with --force).
func (r *ResultsRepo) DeleteSnapshot(ctx context.Context, date string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM ma_sku_daily WHERE snapshot_date = ?`, date)
	if err != nil {
		return fmt.Errorf("delete existing snapshot: %w", err)
	}
	return nil
}

const insertSKUSQL = `
INSERT OR REPLACE INTO ma_sku_daily (
    snapshot_date, nm_id, chrt_id, region_name, tech_size,
    article, identifier, vendor_code,
    name, brand, type, category, category_level1, category_level2,
    sex, season, color, collection,
    stock_qty, total_sizes, sizes_in_stock, fill_pct,
    ma_3, ma_7, ma_14, ma_28,
    sdr_days, trend_pct,
    risk, critical, out_of_stock, broken_grid,
    computed_at
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`

// SaveSKUSnapshots saves flat SKU snapshot rows in batches.
func (r *ResultsRepo) SaveSKUSnapshots(ctx context.Context, rows []SKURow) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, insertSKUSQL)
	if err != nil {
		return 0, fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	now := time.Now().Format(time.RFC3339)
	saved := 0
	const batchSize = 500

	for i, row := range rows {
		_, err := stmt.ExecContext(ctx,
			row.SnapshotDate, row.NmID, row.ChrtID, row.RegionName, row.TechSize,
			row.Article, row.Identifier, row.VendorCode,
			row.Name, row.Brand, row.Type, row.Category, row.CategoryLevel1, row.CategoryLevel2,
			row.Sex, row.Season, row.Color, row.Collection,
			row.StockQty, row.TotalSizes, row.SizesInStock, row.FillPct,
			row.MA3, row.MA7, row.MA14, row.MA28,
			row.SDRDays, row.TrendPct,
			boolToInt(row.Risk), boolToInt(row.Critical), boolToInt(row.OutOfStock), boolToInt(row.BrokenGrid),
			now,
		)
		if err != nil {
			return saved, fmt.Errorf("insert nm_id=%d chrt_id=%d region=%s: %w", row.NmID, row.ChrtID, row.RegionName, err)
		}
		saved++

		if (i+1)%batchSize == 0 {
			if err := tx.Commit(); err != nil {
				return saved, fmt.Errorf("commit batch at %d: %w", i+1, err)
			}
			tx, err = r.db.BeginTx(ctx, nil)
			if err != nil {
				return saved, fmt.Errorf("begin tx after batch: %w", err)
			}
			stmt, err = tx.PrepareContext(ctx, insertSKUSQL)
			if err != nil {
				return saved, fmt.Errorf("prepare insert after batch: %w", err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return saved, fmt.Errorf("commit final: %w", err)
	}
	return saved, nil
}

// Close closes the results database.
func (r *ResultsRepo) Close() error {
	return r.db.Close()
}

// boolToInt converts bool to SQLite integer (0/1).
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
