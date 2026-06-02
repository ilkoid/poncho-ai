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
// Placeholder count: 9 ($1–$9) + 1 SQL function (TO_CHAR) = 10 columns total.
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

// savePricesChunk saves up to 500 prices in a single transaction.
func (r *PgPricesRepo) savePricesChunk(ctx context.Context, chunk []wb.ProductPrice, snapshotDate string) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	for _, p := range chunk {
		_, err := tx.Exec(ctx, pgUpsertPriceSQL,
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
		if err != nil {
			return 0, fmt.Errorf("upsert price nm_id=%d: %w", p.NmID, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}

	return len(chunk), nil
}

var (
	// PostgreSQL upsert — update all fields on conflict (nm_id, snapshot_date).
	// $1-$9 + TO_CHAR function = 10 columns.
	pgUpsertPriceSQL = `
INSERT INTO product_prices (
    nm_id, snapshot_date,
    price, discounted_price, club_discounted_price, discount, club_discount,
    vendor_code, currency, editable_size_price,
    downloaded_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'))
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
