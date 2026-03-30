package sqlite

import (
	"context"
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

const insertRegionSaleSQL = `
INSERT OR REPLACE INTO region_sales (
    nm_id, sa,
    country_name, fo_name, region_name, city_name,
    date_from, date_to,
    sale_invoice_cost_price, sale_invoice_cost_price_perc, sale_item_invoice_qty
) VALUES (?,?,?,?,?,?,?,?,?,?,?)`

const regionSalesChunkSize = 50_000

// SaveRegionSales saves a batch of region sale items for a given period.
// Returns count of inserted rows.
// Splits into 50K-row transactions to prevent WAL explosion.
func (r *SQLiteSalesRepository) SaveRegionSales(ctx context.Context, dateFrom, dateTo string, items []wb.RegionSaleItem) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}

	total := 0
	for i := 0; i < len(items); i += regionSalesChunkSize {
		end := i + regionSalesChunkSize
		if end > len(items) {
			end = len(items)
		}
		chunk := items[i:end]

		n, err := r.saveRegionSalesChunk(ctx, chunk, dateFrom, dateTo)
		if err != nil {
			return 0, fmt.Errorf("save chunk at offset %d: %w", i, err)
		}
		total += n
	}
	return total, nil
}

// saveRegionSalesChunk saves up to 50K items in a single transaction.
func (r *SQLiteSalesRepository) saveRegionSalesChunk(ctx context.Context, chunk []wb.RegionSaleItem, dateFrom, dateTo string) (int, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, insertRegionSaleSQL)
	if err != nil {
		return 0, fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, item := range chunk {
		_, err := stmt.ExecContext(ctx,
			item.NmID, item.Sa,
			item.CountryName, item.FoName, item.RegionName, item.CityName,
			dateFrom, dateTo,
			item.SaleInvoiceCostPrice, item.SaleInvoiceCostPricePerc, item.SaleItemInvoiceQty,
		)
		if err != nil {
			return 0, fmt.Errorf("insert region sale nm_id=%d city=%s: %w",
				item.NmID, item.CityName, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}

	return len(chunk), nil
}

// CountRegionSales returns total number of region sale rows in the database.
func (r *SQLiteSalesRepository) CountRegionSales(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, "SELECT count(*) FROM region_sales").Scan(&count)
	return count, err
}

// CountRegionSalesForPeriod returns number of rows for a specific date range.
func (r *SQLiteSalesRepository) CountRegionSalesForPeriod(ctx context.Context, dateFrom, dateTo string) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx,
		"SELECT count(*) FROM region_sales WHERE date_from = ? AND date_to = ?",
		dateFrom, dateTo).Scan(&count)
	return count, err
}
