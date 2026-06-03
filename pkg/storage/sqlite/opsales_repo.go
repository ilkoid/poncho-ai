package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/opsales"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Compile-time assertion: SQLiteSalesRepository satisfies opsales.OpsalesWriter.
var _ opsales.OpsalesWriter = (*SQLiteSalesRepository)(nil)

const (
	insertSaleSQL = `
INSERT OR REPLACE INTO operational_sales (
    sale_id,
    sale_date, last_change_date,
    warehouse_name, warehouse_type, country_name, oblast_okrug_name, region_name,
    supplier_article, nm_id, barcode, category, subject, brand, tech_size,
    income_id, is_supply, is_realization,
    total_price, discount_percent, spp, payment_sale_amount, for_pay, finished_price, price_with_disc,
    sticker, g_number, srid,
    downloaded_at
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,CURRENT_TIMESTAMP)`

	deleteSalesOlderThanSQL = `DELETE FROM operational_sales WHERE sale_date < ?`
)

const salesChunkSize = 500

// SaveSales saves a batch of operational sales using INSERT OR REPLACE.
// Chunk size: 500 sales per transaction.
// Returns count of inserted sales.
func (r *SQLiteSalesRepository) SaveSales(ctx context.Context, sales []wb.SalesItem) (int, error) {
	if len(sales) == 0 {
		return 0, nil
	}

	r.db.Exec("PRAGMA synchronous = OFF")
	defer r.db.Exec("PRAGMA synchronous = NORMAL")

	total := 0
	for i := 0; i < len(sales); i += salesChunkSize {
		end := i + salesChunkSize
		if end > len(sales) {
			end = len(sales)
		}
		chunk := sales[i:end]

		n, err := r.saveSalesChunk(ctx, chunk)
		if err != nil {
			return 0, fmt.Errorf("save sales chunk at offset %d: %w", i, err)
		}
		total += n
	}
	return total, nil
}

// saveSalesChunk saves up to 500 sales in a single transaction.
func (r *SQLiteSalesRepository) saveSalesChunk(ctx context.Context, chunk []wb.SalesItem) (int, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, insertSaleSQL)
	if err != nil {
		return 0, fmt.Errorf("prepare sale statement: %w", err)
	}
	defer stmt.Close()

	for _, s := range chunk {
		_, err := stmt.ExecContext(ctx,
			s.SaleID,
			s.Date, s.LastChangeDate,
			s.WarehouseName, s.WarehouseType, s.CountryName, s.OblastOkrugName, s.RegionName,
			s.SupplierArticle, s.NmID, s.Barcode, s.Category, s.Subject, s.Brand, s.TechSize,
			s.IncomeID, boolToInt(s.IsSupply), boolToInt(s.IsRealization),
			s.TotalPrice, s.DiscountPercent, s.Spp, s.PaymentSaleAmount, s.ForPay, s.FinishedPrice, s.PriceWithDisc,
			s.Sticker, s.GNumber, s.Srid,
		)
		if err != nil {
			return 0, fmt.Errorf("insert sale sale_id=%s: %w", s.SaleID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}

	return len(chunk), nil
}

// DeleteSalesOlderThan removes sales with sale_date before the given time.
func (r *SQLiteSalesRepository) DeleteSalesOlderThan(ctx context.Context, before time.Time) (int64, error) {
	result, err := r.db.ExecContext(ctx, deleteSalesOlderThanSQL, before.Format("2006-01-02"))
	if err != nil {
		return 0, fmt.Errorf("delete old sales: %w", err)
	}
	return result.RowsAffected()
}
