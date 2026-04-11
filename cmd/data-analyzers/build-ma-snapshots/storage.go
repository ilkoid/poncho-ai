package main

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// maDailySchema defines the flat MA snapshot table in bi.db.
// All product identifiers and attributes are denormalized into a single table
// so PowerBI can query without JOINs.
// Delta columns are intentionally omitted — PowerBI computes them as DAX measures.
const maDailySchema = `
CREATE TABLE IF NOT EXISTS ma_daily (
    snapshot_date   TEXT NOT NULL,
    nm_id           INTEGER NOT NULL,

    -- Product identifiers
    article         TEXT DEFAULT '',
    identifier      TEXT DEFAULT '',
    vendor_code     TEXT DEFAULT '',

    -- Product attributes (from onec_goods/pim_goods)
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

    -- Actual sales
    sold            INTEGER DEFAULT 0,
    sold_prev       INTEGER DEFAULT 0,

    -- Moving averages (N complete days BEFORE snapshot_date, not including it)
    ma_3            REAL,
    ma_7            REAL,
    ma_14           REAL,
    ma_28           REAL,

    computed_at     TEXT DEFAULT CURRENT_TIMESTAMP,

    PRIMARY KEY (snapshot_date, nm_id)
);

CREATE INDEX IF NOT EXISTS idx_ma_snapshot_date ON ma_daily(snapshot_date);
CREATE INDEX IF NOT EXISTS idx_ma_nm_id ON ma_daily(nm_id);
CREATE INDEX IF NOT EXISTS idx_ma_article ON ma_daily(article);
CREATE INDEX IF NOT EXISTS idx_ma_vendor_code ON ma_daily(vendor_code);
CREATE INDEX IF NOT EXISTS idx_ma_date_article ON ma_daily(snapshot_date, article);
CREATE INDEX IF NOT EXISTS idx_ma_brand ON ma_daily(brand);
CREATE INDEX IF NOT EXISTS idx_ma_type ON ma_daily(type);
CREATE INDEX IF NOT EXISTS idx_ma_category ON ma_daily(category);
CREATE INDEX IF NOT EXISTS idx_ma_season ON ma_daily(season);
CREATE INDEX IF NOT EXISTS idx_ma_date_brand ON ma_daily(snapshot_date, brand);
CREATE INDEX IF NOT EXISTS idx_ma_date_category ON ma_daily(snapshot_date, category);
`

// ResultsRepo manages the bi.db — stores flat MA snapshots.
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

	if _, err := db.Exec(maDailySchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create ma_daily schema: %w", err)
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
    snapshot_date, nm_id,
    article, identifier, vendor_code,
    name, name_im, brand, type,
    category, category_level1, category_level2,
    sex, season, color, collection,
    sold, sold_prev,
    ma_3, ma_7, ma_14, ma_28,
    computed_at
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`

// SaveMASnapshots saves flat MA snapshot rows in batches.
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
			row.SnapshotDate, row.NmID,
			row.Article, row.Identifier, row.VendorCode,
			row.Name, row.NameIM, row.Brand, row.Type,
			row.Category, row.CategoryLevel1, row.CategoryLevel2,
			row.Sex, row.Season, row.Color, row.Collection,
			row.Sold, row.SoldPrev,
			row.MA3, row.MA7, row.MA14, row.MA28,
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

// Close closes the results database.
func (r *ResultsRepo) Close() error {
	return r.db.Close()
}
