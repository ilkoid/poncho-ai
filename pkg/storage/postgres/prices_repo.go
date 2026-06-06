package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ilkoid/poncho-ai/pkg/prices"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Compile-time assertion: PgPricesRepo implements prices.PricesWriter.
var _ prices.PricesWriter = (*PgPricesRepo)(nil)

// PgPricesRepo implements prices.PricesWriter for PostgreSQL.
// Focused repository (ISP) — only prices persistence methods.
type PgPricesRepo struct {
	pool *pgxpool.Pool
}

// NewPgPricesRepo creates a new PostgreSQL prices repository.
func NewPgPricesRepo(pool *pgxpool.Pool) *PgPricesRepo {
	return &PgPricesRepo{pool: pool}
}

// InitSchema creates product_prices table if it doesn't exist.
func (r *PgPricesRepo) InitSchema(ctx context.Context) error {
	return initPricesSchema(ctx, r.pool)
}

// SavePrices saves a batch of prices using ON CONFLICT for upsert.
// Chunk size: 500 prices per transaction.
// Returns count of saved rows.
//
// Placeholder count: 10 ($1–$10). downloaded_at uses TO_CHAR (not a placeholder).
func (r *PgPricesRepo) SavePrices(ctx context.Context, prices []wb.ProductPrice, snapshotDate string) (int, error) {
	if len(prices) == 0 {
		return 0, nil
	}

	total := 0
	for i := 0; i < len(prices); i += pgPricesChunkSize {
		end := min(i+pgPricesChunkSize, len(prices))
		chunk := prices[i:end]

		n, err := r.savePricesChunk(ctx, chunk, snapshotDate)
		if err != nil {
			return 0, fmt.Errorf("save prices chunk at offset %d: %w", i, err)
		}
		total += n
	}
	return total, nil
}

// CountPrices returns total price record count.
func (r *PgPricesRepo) CountPrices(ctx context.Context) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM product_prices`).Scan(&count)
	return count, err
}

const pgPricesChunkSize = 500

// savePricesChunk saves up to 500 prices using a single multi-row INSERT.
func (r *PgPricesRepo) savePricesChunk(ctx context.Context, chunk []wb.ProductPrice, snapshotDate string) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	args := make([]any, 0, len(chunk)*insertProductPriceCols)
	for _, p := range chunk {
		args = append(args,
			p.NmID,
			snapshotDate,
			p.Price,
			p.DiscountedPrice,
			p.ClubDiscountedPrice,
			p.Discount,
			p.ClubDiscount,
			p.VendorCode,
			p.Currency,
			p.EditableSizePrice,
		)
	}

	query := insertProductPriceFullChunkSQL
	if len(chunk) < pgPricesChunkSize {
		query = BuildMultiRowInsert(insertProductPricePrefixSQL, insertProductPriceOnConflictSQL, len(chunk), insertProductPriceCols)
	}

	tag, err := tx.Exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("save prices batch (size %d): %w", len(chunk), err)
	}
	return int(tag.RowsAffected()), tx.Commit(ctx)
}

// Multi-row INSERT SQL fragments for product_prices.
const (
	insertProductPriceCols = 10 // $1-$10 (downloaded_at uses TO_CHAR, not a placeholder)

	insertProductPricePrefixSQL = `INSERT INTO product_prices (
	    nm_id, snapshot_date,
	    price, discounted_price, club_discounted_price, discount, club_discount,
	    vendor_code, currency, editable_size_price,
	    downloaded_at
	) VALUES `

	insertProductPriceOnConflictSQL = `
	ON CONFLICT (nm_id, snapshot_date) DO UPDATE SET
	    price = EXCLUDED.price,
	    discounted_price = EXCLUDED.discounted_price,
	    club_discounted_price = EXCLUDED.club_discounted_price,
	    discount = EXCLUDED.discount,
	    club_discount = EXCLUDED.club_discount,
	    vendor_code = EXCLUDED.vendor_code,
	    currency = EXCLUDED.currency,
	    editable_size_price = EXCLUDED.editable_size_price,
	    downloaded_at = EXCLUDED.downloaded_at`
)

// Pre-built query for full chunks (500 rows). Last chunk rebuilt with actual size.
var insertProductPriceFullChunkSQL = BuildMultiRowInsert(insertProductPricePrefixSQL, insertProductPriceOnConflictSQL, pgPricesChunkSize, insertProductPriceCols)
