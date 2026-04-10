package main

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// maDailySchema defines the main MA snapshot table in bi.db.
const maDailySchema = `
CREATE TABLE IF NOT EXISTS ma_daily (
    snapshot_date  TEXT NOT NULL,
    nm_id          INTEGER NOT NULL,

    -- Product identifiers
    article        TEXT DEFAULT '',
    identifier     TEXT DEFAULT '',
    vendor_code    TEXT DEFAULT '',

    -- Actual sales
    sold           INTEGER DEFAULT 0,
    sold_prev      INTEGER DEFAULT 0,

    -- Moving averages (N complete days BEFORE snapshot_date, not including it)
    ma_3           REAL,
    ma_7           REAL,
    ma_14          REAL,
    ma_28          REAL,

    -- Deltas vs previous day
    delta_1d       INTEGER,
    delta_1d_pct   REAL,

    -- Deltas vs moving averages
    delta_ma3      REAL,
    delta_ma3_pct  REAL,
    delta_ma7      REAL,
    delta_ma7_pct  REAL,
    delta_ma14     REAL,
    delta_ma14_pct REAL,
    delta_ma28     REAL,
    delta_ma28_pct REAL,

    computed_at    TEXT DEFAULT CURRENT_TIMESTAMP,

    PRIMARY KEY (snapshot_date, nm_id)
);

CREATE INDEX IF NOT EXISTS idx_ma_snapshot_date ON ma_daily(snapshot_date);
CREATE INDEX IF NOT EXISTS idx_ma_nm_id ON ma_daily(nm_id);
CREATE INDEX IF NOT EXISTS idx_ma_article ON ma_daily(article);
CREATE INDEX IF NOT EXISTS idx_ma_vendor_code ON ma_daily(vendor_code);
CREATE INDEX IF NOT EXISTS idx_ma_date_article ON ma_daily(snapshot_date, article);
`

// productAttrsSchema defines the product dimension table for PowerBI filtering.
const productAttrsSchema = `
CREATE TABLE IF NOT EXISTS product_attrs (
    nm_id           INTEGER PRIMARY KEY,
    article         TEXT DEFAULT '',
    identifier      TEXT DEFAULT '',
    vendor_code     TEXT DEFAULT '',
    name            TEXT DEFAULT '',
    name_im         TEXT DEFAULT '',
    brand           TEXT DEFAULT '',
    type            TEXT DEFAULT '',
    category        TEXT DEFAULT '',
    category_level1 TEXT DEFAULT '',
    category_level2 TEXT DEFAULT '',
    sex             TEXT DEFAULT '',
    season          TEXT DEFAULT '',
    color           TEXT DEFAULT '',
    collection      TEXT DEFAULT '',
    updated_at      TEXT DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_pa_article ON product_attrs(article);
CREATE INDEX IF NOT EXISTS idx_pa_brand ON product_attrs(brand);
CREATE INDEX IF NOT EXISTS idx_pa_type ON product_attrs(type);
CREATE INDEX IF NOT EXISTS idx_pa_category ON product_attrs(category);
CREATE INDEX IF NOT EXISTS idx_pa_season ON product_attrs(season);
`

// ResultsRepo manages the bi.db — stores MA snapshots and product attributes.
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
			db.Close()
			return nil, fmt.Errorf("PRAGMA %s: %w", p.key, err)
		}
	}

	if _, err := db.Exec(maDailySchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create ma_daily schema: %w", err)
	}
	if _, err := db.Exec(productAttrsSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create product_attrs schema: %w", err)
	}

	return &ResultsRepo{db: db}, nil
}

// HasSnapshot checks if a snapshot already exists for the given date.
func (r *ResultsRepo) HasSnapshot(ctx context.Context, date string) (bool, error) {
	var count int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM ma_daily WHERE snapshot_date = ? LIMIT 1`, date,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check existing snapshot: %w", err)
	}
	return count > 0, nil
}

const insertMADailySQL = `
INSERT OR REPLACE INTO ma_daily (
    snapshot_date, nm_id, article, identifier, vendor_code,
    sold, sold_prev,
    ma_3, ma_7, ma_14, ma_28,
    delta_1d, delta_1d_pct,
    delta_ma3, delta_ma3_pct,
    delta_ma7, delta_ma7_pct,
    delta_ma14, delta_ma14_pct,
    delta_ma28, delta_ma28_pct,
    computed_at
) VALUES (
    ?,?,?,?,?,
    ?,?,
    ?,?,?,?,
    ?,?,
    ?,?,
    ?,?,
    ?,?,
    ?,?,
    ?
)`

// SaveMASnapshots saves MA snapshot rows in batches.
func (r *ResultsRepo) SaveMASnapshots(ctx context.Context, rows []MARow) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, insertMADailySQL)
	if err != nil {
		return 0, fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	now := time.Now().Format(time.RFC3339)
	saved := 0
	const batchSize = 500

	for i, row := range rows {
		_, err := stmt.ExecContext(ctx,
			row.SnapshotDate, row.NmID, row.Article, row.Identifier, row.VendorCode,
			row.Sold, row.SoldPrev,
			row.MA3, row.MA7, row.MA14, row.MA28,
			row.Delta1D, row.Delta1DPct,
			row.DeltaMA3, row.DeltaMA3Pct,
			row.DeltaMA7, row.DeltaMA7Pct,
			row.DeltaMA14, row.DeltaMA14Pct,
			row.DeltaMA28, row.DeltaMA28Pct,
			now,
		)
		if err != nil {
			return saved, fmt.Errorf("insert nm_id=%d: %w", row.NmID, err)
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
			stmt, err = tx.PrepareContext(ctx, insertMADailySQL)
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

const insertProductAttrSQL = `
INSERT OR REPLACE INTO product_attrs (
    nm_id, article, identifier, vendor_code,
    name, name_im, brand, type, category, category_level1, category_level2,
    sex, season, color, collection, updated_at
) VALUES (
    ?,?,?,?,?,?,?,?,?,?,
    ?,?,?,?,?,?
)`

// SaveProductAttrs saves product attributes in batches.
func (r *ResultsRepo) SaveProductAttrs(ctx context.Context, attrs []ProductAttrs) (int, error) {
	if len(attrs) == 0 {
		return 0, nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, insertProductAttrSQL)
	if err != nil {
		return 0, fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	now := time.Now().Format(time.RFC3339)
	saved := 0

	for _, a := range attrs {
		_, err := stmt.ExecContext(ctx,
			a.NmID, a.Article, a.Identifier, a.VendorCode,
			a.Name, a.NameIM, a.Brand, a.Type, a.Category, a.CategoryLevel1, a.CategoryLevel2,
			a.Sex, a.Season, a.Color, a.Collection, now,
		)
		if err != nil {
			return saved, fmt.Errorf("insert attrs nm_id=%d: %w", a.NmID, err)
		}
		saved++
	}

	if err := tx.Commit(); err != nil {
		return saved, fmt.Errorf("commit attrs: %w", err)
	}
	return saved, nil
}

// Close closes the results database.
func (r *ResultsRepo) Close() error {
	return r.db.Close()
}
