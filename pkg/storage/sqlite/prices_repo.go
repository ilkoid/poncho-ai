package sqlite

import (
	"context"
	"fmt"
	"strings"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// SavePrices saves a batch of product prices using INSERT OR REPLACE.
// snapshotDate is applied to all rows (YYYY-MM-DD, set by caller).
// Returns number of rows inserted/replaced.
func (r *SQLiteSalesRepository) SavePrices(ctx context.Context, prices []wb.ProductPrice, snapshotDate string) (int, error) {
	if len(prices) == 0 {
		return 0, nil
	}

	const batchSize = 500
	totalSaved := 0

	for i := 0; i < len(prices); i += batchSize {
		end := min(i+batchSize, len(prices))
		batch := prices[i:end]

		var placeholders []string
		var args []any

		for _, p := range batch {
			placeholders = append(placeholders, "(?, ?, ?, ?, ?, ?, ?, ?, ?)")
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
			)
		}

		query := fmt.Sprintf(`
			INSERT OR REPLACE INTO product_prices
				(nm_id, snapshot_date, price, discounted_price, club_discounted_price, discount, club_discount, vendor_code, currency)
			VALUES %s
		`, strings.Join(placeholders, ", "))

		result, err := r.db.ExecContext(ctx, query, args...)
		if err != nil {
			return totalSaved, fmt.Errorf("save prices batch (offset %d): %w", i, err)
		}

		affected, _ := result.RowsAffected()
		totalSaved += int(affected)
	}

	return totalSaved, nil
}

// CountPrices returns total number of price records in the database.
func (r *SQLiteSalesRepository) CountPrices(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM product_prices").Scan(&count)
	return count, err
}

// HasSnapshotForDate checks if a price snapshot exists for the given date.
func (r *SQLiteSalesRepository) HasSnapshotForDate(ctx context.Context, date string) (bool, error) {
	var count int
	err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM product_prices WHERE snapshot_date = ?", date).Scan(&count)
	return count > 0, err
}
