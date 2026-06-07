package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ilkoid/poncho-ai/pkg/regionsales"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Compile-time assertion: PgRegionSalesRepo implements regionsales.RegionSalesWriter.
var _ regionsales.RegionSalesWriter = (*PgRegionSalesRepo)(nil)

// PgRegionSalesRepo implements regionsales.RegionSalesWriter for PostgreSQL.
// Focused repository (ISP) — only region sales persistence.
type PgRegionSalesRepo struct {
	pool *pgxpool.Pool
}

// NewPgRegionSalesRepo creates a new PostgreSQL region sales repository.
func NewPgRegionSalesRepo(pool *pgxpool.Pool) *PgRegionSalesRepo {
	return &PgRegionSalesRepo{pool: pool}
}

// InitSchema creates region_sales table if it doesn't exist.
func (r *PgRegionSalesRepo) InitSchema(ctx context.Context) error {
	return initRegionSalesSchema(ctx, r.pool)
}

const pgRegionSalesChunkSize = 1000

// Multi-row INSERT SQL fragments for region_sales.
const (
	insertRegionSaleCols = 11 // $1-$11 (downloaded_at uses TO_CHAR in ON CONFLICT, not a placeholder)

	insertRegionSalePrefixSQL = `INSERT INTO region_sales (
	    nm_id, sa,
	    country_name, fo_name, region_name, city_name,
	    date_from, date_to,
	    sale_invoice_cost_price, sale_invoice_cost_price_perc, sale_item_invoice_qty
	) VALUES `

	insertRegionSaleOnConflictSQL = `
	ON CONFLICT (nm_id, region_name, city_name, country_name, date_from, date_to) DO UPDATE SET
	    sa                         = EXCLUDED.sa,
	    fo_name                    = EXCLUDED.fo_name,
	    sale_invoice_cost_price    = EXCLUDED.sale_invoice_cost_price,
	    sale_invoice_cost_price_perc = EXCLUDED.sale_invoice_cost_price_perc,
	    sale_item_invoice_qty      = EXCLUDED.sale_item_invoice_qty,
	    downloaded_at              = TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')`
)

// Pre-built query for full chunks (500 rows). Last chunk rebuilt with actual size.
var insertRegionSaleFullChunkSQL = BuildMultiRowInsert(insertRegionSalePrefixSQL, insertRegionSaleOnConflictSQL, pgRegionSalesChunkSize, insertRegionSaleCols)

// SaveRegionSales saves a batch of region sale items for a given period.
// Returns count of saved rows. Splits into 500-row transactions.
func (r *PgRegionSalesRepo) SaveRegionSales(ctx context.Context, dateFrom, dateTo string, items []wb.RegionSaleItem) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}

	total := 0
	for i := 0; i < len(items); i += pgRegionSalesChunkSize {
		end := min(i+pgRegionSalesChunkSize, len(items))
		chunk := items[i:end]

		n, err := r.saveRegionSalesChunk(ctx, chunk, dateFrom, dateTo)
		if err != nil {
			return 0, fmt.Errorf("save chunk at offset %d: %w", i, err)
		}
		total += n
	}
	return total, nil
}

// saveRegionSalesChunk saves up to pgRegionSalesChunkSize items using a single multi-row INSERT.
func (r *PgRegionSalesRepo) saveRegionSalesChunk(ctx context.Context, chunk []wb.RegionSaleItem, dateFrom, dateTo string) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	args := make([]any, 0, len(chunk)*insertRegionSaleCols)
	for _, item := range chunk {
		args = append(args,
			item.NmID, item.Sa,
			item.CountryName, item.FoName, item.RegionName, item.CityName,
			dateFrom, dateTo,
			item.SaleInvoiceCostPrice, item.SaleInvoiceCostPricePerc, item.SaleItemInvoiceQty,
		)
	}

	query := insertRegionSaleFullChunkSQL
	if len(chunk) < pgRegionSalesChunkSize {
		query = BuildMultiRowInsert(insertRegionSalePrefixSQL, insertRegionSaleOnConflictSQL, len(chunk), insertRegionSaleCols)
	}

	tag, err := tx.Exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("save region_sales batch (size %d): %w", len(chunk), err)
	}
	return int(tag.RowsAffected()), tx.Commit(ctx)
}
