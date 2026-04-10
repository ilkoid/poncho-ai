package main

import (
	"context"
	"database/sql"
	"encoding/csv"
	"fmt"
	"os"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// barcodeMappingSchema defines the barcode_mapping table.
const barcodeMappingSchema = `
DROP TABLE IF EXISTS barcode_mapping;

CREATE TABLE barcode_mapping (
    barcode       TEXT    PRIMARY KEY,
    nm_id         INTEGER,
    vendor_code   TEXT,
    article       TEXT    NOT NULL,
    guid          TEXT    NOT NULL,
    pim_nm_id     INTEGER,
    has_wb_sales  INTEGER DEFAULT 0,
    created_at    TEXT    DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_bm_nm_id ON barcode_mapping(nm_id);
CREATE INDEX IF NOT EXISTS idx_bm_vendor_code ON barcode_mapping(vendor_code);
CREATE INDEX IF NOT EXISTS idx_bm_article ON barcode_mapping(article);
CREATE INDEX IF NOT EXISTS idx_bm_guid ON barcode_mapping(guid);
CREATE INDEX IF NOT EXISTS idx_bm_has_wb_sales ON barcode_mapping(has_wb_sales);
`

// pimProductMappingSchema defines the pim_product_mapping table.
const pimProductMappingSchema = `
DROP TABLE IF EXISTS pim_product_mapping;

CREATE TABLE pim_product_mapping (
    pim_article   TEXT    PRIMARY KEY,
    article       TEXT,
    guid          TEXT,
    nm_id         INTEGER,
    vendor_code   TEXT,
    pim_nm_id     INTEGER,
    enabled       INTEGER DEFAULT 1,
    barcode_count INTEGER DEFAULT 0,
    has_wb_sales  INTEGER DEFAULT 0,
    created_at    TEXT    DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_ppm_nm_id ON pim_product_mapping(nm_id);
CREATE INDEX IF NOT EXISTS idx_ppm_vendor_code ON pim_product_mapping(vendor_code);
CREATE INDEX IF NOT EXISTS idx_ppm_article ON pim_product_mapping(article);
CREATE INDEX IF NOT EXISTS idx_ppm_guid ON pim_product_mapping(guid);
CREATE INDEX IF NOT EXISTS idx_ppm_has_wb_sales ON pim_product_mapping(has_wb_sales);
`

// nmProductMappingSchema defines the nm_product_mapping table.
const nmProductMappingSchema = `
DROP TABLE IF EXISTS nm_product_mapping;

CREATE TABLE nm_product_mapping (
    nm_id        INTEGER PRIMARY KEY,
    article      TEXT,
    pim_article  TEXT,
    vendor_code  TEXT,
    enabled      INTEGER DEFAULT 1,
    created_at   TEXT    DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_npm_article ON nm_product_mapping(article);
CREATE INDEX IF NOT EXISTS idx_npm_pim_article ON nm_product_mapping(pim_article);
CREATE INDEX IF NOT EXISTS idx_npm_vendor_code ON nm_product_mapping(vendor_code);
CREATE INDEX IF NOT EXISTS idx_npm_enabled ON nm_product_mapping(enabled);
`

const insertBarcodeSQL = `
INSERT INTO barcode_mapping (
    barcode, nm_id, vendor_code, article, guid, pim_nm_id, has_wb_sales, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
`

const insertPimProductSQL = `
INSERT INTO pim_product_mapping (
    pim_article, article, guid, nm_id, vendor_code, pim_nm_id,
    enabled, barcode_count, has_wb_sales, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`

const insertNmProductSQL = `
INSERT INTO nm_product_mapping (
    nm_id, article, pim_article, vendor_code, enabled, created_at
) VALUES (?, ?, ?, ?, ?, ?)
`

// ResultsRepo manages the bi.db — stores mapping results.
type ResultsRepo struct {
	db *sql.DB
}

// NewResultsRepo opens/creates the bi.db with WAL mode and creates schemas.
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

	if _, err := db.Exec(barcodeMappingSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create barcode_mapping schema: %w", err)
	}

	if _, err := db.Exec(pimProductMappingSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create pim_product_mapping schema: %w", err)
	}

	if _, err := db.Exec(nmProductMappingSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create nm_product_mapping schema: %w", err)
	}

	return &ResultsRepo{db: db}, nil
}

// SaveBarcodeMappings saves barcode mappings in batches.
func (r *ResultsRepo) SaveBarcodeMappings(ctx context.Context, mappings []BarcodeMapping, batchSize int) error {
	return saveBatch(ctx, r.db, insertBarcodeSQL, batchSize, len(mappings), func(stmt *sql.Stmt, i int) error {
		m := mappings[i]
		var hasSales int
		if m.HasWBSales {
			hasSales = 1
		}
		_, err := stmt.ExecContext(ctx,
			m.Barcode,
			nullInt64From(m.NmID),
			nullStringFrom(m.VendorCode),
			m.Article,
			m.GUID,
			nullInt64From(m.PimNmID),
			hasSales,
			time.Now().Format(time.RFC3339),
		)
		return err
	})
}

// SavePimProductMappings saves PIM product mappings in batches.
func (r *ResultsRepo) SavePimProductMappings(ctx context.Context, mappings []PimProductMapping, batchSize int) error {
	return saveBatch(ctx, r.db, insertPimProductSQL, batchSize, len(mappings), func(stmt *sql.Stmt, i int) error {
		m := mappings[i]
		var enabled int
		if m.Enabled {
			enabled = 1
		}
		var hasSales int
		if m.HasWBSales {
			hasSales = 1
		}
		_, err := stmt.ExecContext(ctx,
			m.PimArticle,
			nullStringFrom(m.Article),
			nullStringFrom(m.GUID),
			nullInt64From(m.NmID),
			nullStringFrom(m.VendorCode),
			nullInt64From(m.PimNmID),
			enabled,
			m.BarcodeCount,
			hasSales,
			time.Now().Format(time.RFC3339),
		)
		return err
	})
}

// SaveNmProductMappings saves NM product mappings in batches.
func (r *ResultsRepo) SaveNmProductMappings(ctx context.Context, mappings []NmProductMapping, batchSize int) error {
	return saveBatch(ctx, r.db, insertNmProductSQL, batchSize, len(mappings), func(stmt *sql.Stmt, i int) error {
		m := mappings[i]
		var enabled int
		if m.Enabled {
			enabled = 1
		}
		_, err := stmt.ExecContext(ctx,
			m.NmID,
			nullStringFrom(m.Article),
			nullStringFrom(m.PimArticle),
			m.VendorCode,
			enabled,
			time.Now().Format(time.RFC3339),
		)
		return err
	})
}

// saveBatch is a generic batch insert helper with transaction management.
func saveBatch(ctx context.Context, db *sql.DB, insertSQL string, batchSize, total int, insertFn func(*sql.Stmt, int) error) error {
	if total == 0 {
		return nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, insertSQL)
	if err != nil {
		return fmt.Errorf("prepare insert: %w", err)
	}

	for i := 0; i < total; i++ {
		if err := insertFn(stmt, i); err != nil {
			return fmt.Errorf("insert row %d: %w", i, err)
		}

		if (i+1)%batchSize == 0 {
			if err := tx.Commit(); err != nil {
				return fmt.Errorf("commit batch at %d: %w", i+1, err)
			}
			tx, err = db.BeginTx(ctx, nil)
			if err != nil {
				return fmt.Errorf("begin tx after batch: %w", err)
			}
			stmt, err = tx.PrepareContext(ctx, insertSQL)
			if err != nil {
				return fmt.Errorf("prepare insert after batch: %w", err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit final: %w", err)
	}
	return nil
}

// GetBarcodeStats returns statistics about the barcode_mapping table.
func (r *ResultsRepo) GetBarcodeStats(ctx context.Context) (*BarcodeStats, error) {
	var stats BarcodeStats

	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM barcode_mapping`).Scan(&stats.TotalBarcodes)
	if err != nil {
		return nil, fmt.Errorf("count total barcodes: %w", err)
	}

	err = r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM barcode_mapping WHERE nm_id IS NOT NULL`).Scan(&stats.MappedToNmID)
	if err != nil {
		return nil, fmt.Errorf("count mapped barcodes: %w", err)
	}

	err = r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM barcode_mapping WHERE has_wb_sales = 1`).Scan(&stats.HasWBSales)
	if err != nil {
		return nil, fmt.Errorf("count barcodes with sales: %w", err)
	}

	return &stats, nil
}

// GetPimStats returns statistics about the pim_product_mapping table.
func (r *ResultsRepo) GetPimStats(ctx context.Context) (*PimStats, error) {
	var stats PimStats

	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pim_product_mapping`).Scan(&stats.TotalProducts)
	if err != nil {
		return nil, fmt.Errorf("count total products: %w", err)
	}

	err = r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pim_product_mapping WHERE article IS NOT NULL`).Scan(&stats.MatchedTo1C)
	if err != nil {
		return nil, fmt.Errorf("count matched to 1C: %w", err)
	}

	err = r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pim_product_mapping WHERE nm_id IS NOT NULL`).Scan(&stats.MatchedToWB)
	if err != nil {
		return nil, fmt.Errorf("count matched to WB: %w", err)
	}

	err = r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pim_product_mapping WHERE has_wb_sales = 1`).Scan(&stats.HasWBSales)
	if err != nil {
		return nil, fmt.Errorf("count products with sales: %w", err)
	}

	return &stats, nil
}

// GetNmStats returns statistics about the nm_product_mapping table.
func (r *ResultsRepo) GetNmStats(ctx context.Context) (*NmStats, error) {
	var stats NmStats

	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM nm_product_mapping`).Scan(&stats.TotalProducts)
	if err != nil {
		return nil, fmt.Errorf("count total nm products: %w", err)
	}

	err = r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM nm_product_mapping WHERE article IS NOT NULL`).Scan(&stats.MatchedTo1C)
	if err != nil {
		return nil, fmt.Errorf("count matched to 1C: %w", err)
	}

	err = r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM nm_product_mapping WHERE pim_article IS NOT NULL`).Scan(&stats.MatchedToPIM)
	if err != nil {
		return nil, fmt.Errorf("count matched to PIM: %w", err)
	}

	return &stats, nil
}

// ExportCSV writes a table to a CSV file given column names and a query.
func (r *ResultsRepo) ExportCSV(ctx context.Context, csvPath, query string, header []string, scanFn func(*csv.Writer, *sql.Rows) (int, error)) (int, error) {
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("query for csv: %w", err)
	}
	defer rows.Close()

	f, err := os.Create(csvPath)
	if err != nil {
		return 0, fmt.Errorf("create csv: %w", err)
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()

	if err := w.Write(header); err != nil {
		return 0, fmt.Errorf("write csv header: %w", err)
	}

	return scanFn(w, rows)
}

// Close closes the results database.
func (r *ResultsRepo) Close() error {
	return r.db.Close()
}

// Helper functions for sql.Null* types

func nullInt64From(n sql.NullInt64) any {
	if n.Valid {
		return n.Int64
	}
	return nil
}

func nullStringFrom(n sql.NullString) any {
	if n.Valid {
		return n.String
	}
	return nil
}

func nullStringOrEmpty(n sql.NullString) string {
	if n.Valid {
		return n.String
	}
	return ""
}
