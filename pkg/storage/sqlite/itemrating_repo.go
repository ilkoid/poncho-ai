package sqlite

import (
	"context"
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/itemrating"
)

// Compile-time assertion: SQLiteSalesRepository satisfies itemrating.Writer.
var _ itemrating.Writer = (*SQLiteSalesRepository)(nil)

const (
	// upsertItemRatingSQL — idempotent upsert on (nm_id, period_start, period_end).
	upsertItemRatingSQL = `
INSERT INTO item_ratings (
    period_start, period_end, nm_id,
    vendor_code, title, subject_id, subject_name, brand_name, tag_id, tag_name,
    pinned_feedback, card_rating,
    feedback_rating_current, feedback_rating_dynamics, feedback_rating_percentile,
    feedback_count_inc, feedback_count_dynamics,
    five_star_inc, five_star_dynamics,
    four_star_inc, four_star_dynamics,
    three_star_inc, three_star_dynamics,
    two_star_inc, two_star_dynamics,
    one_star_inc, one_star_dynamics,
    disqualified, is_shadowed, raw_item
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(nm_id, period_start, period_end) DO UPDATE SET
    vendor_code=excluded.vendor_code, title=excluded.title,
    subject_id=excluded.subject_id, subject_name=excluded.subject_name,
    brand_name=excluded.brand_name, tag_id=excluded.tag_id, tag_name=excluded.tag_name,
    pinned_feedback=excluded.pinned_feedback, card_rating=excluded.card_rating,
    feedback_rating_current=excluded.feedback_rating_current,
    feedback_rating_dynamics=excluded.feedback_rating_dynamics,
    feedback_rating_percentile=excluded.feedback_rating_percentile,
    feedback_count_inc=excluded.feedback_count_inc,
    feedback_count_dynamics=excluded.feedback_count_dynamics,
    five_star_inc=excluded.five_star_inc, five_star_dynamics=excluded.five_star_dynamics,
    four_star_inc=excluded.four_star_inc, four_star_dynamics=excluded.four_star_dynamics,
    three_star_inc=excluded.three_star_inc, three_star_dynamics=excluded.three_star_dynamics,
    two_star_inc=excluded.two_star_inc, two_star_dynamics=excluded.two_star_dynamics,
    one_star_inc=excluded.one_star_inc, one_star_dynamics=excluded.one_star_dynamics,
    disqualified=excluded.disqualified, is_shadowed=excluded.is_shadowed,
    raw_item=excluded.raw_item`

	upsertItemSellerSQL = `
INSERT INTO item_rating_seller (
    period_start, period_end,
    seller_rating_current, seller_rating_dynamics,
    fb_increase_current, fb_increase_total, fb_increase_dynamics,
    five_star_inc, five_star_dynamics, five_star_total,
    four_star_inc, four_star_dynamics, four_star_total,
    three_star_inc, three_star_dynamics, three_star_total,
    two_star_inc, two_star_dynamics, two_star_total,
    one_star_inc, one_star_dynamics, one_star_total,
    raw_seller, raw_fb_increase
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(period_start, period_end) DO UPDATE SET
    seller_rating_current=excluded.seller_rating_current,
    seller_rating_dynamics=excluded.seller_rating_dynamics,
    fb_increase_current=excluded.fb_increase_current,
    fb_increase_total=excluded.fb_increase_total,
    fb_increase_dynamics=excluded.fb_increase_dynamics,
    five_star_inc=excluded.five_star_inc, five_star_dynamics=excluded.five_star_dynamics, five_star_total=excluded.five_star_total,
    four_star_inc=excluded.four_star_inc, four_star_dynamics=excluded.four_star_dynamics, four_star_total=excluded.four_star_total,
    three_star_inc=excluded.three_star_inc, three_star_dynamics=excluded.three_star_dynamics, three_star_total=excluded.three_star_total,
    two_star_inc=excluded.two_star_inc, two_star_dynamics=excluded.two_star_dynamics, two_star_total=excluded.two_star_total,
    one_star_inc=excluded.one_star_inc, one_star_dynamics=excluded.one_star_dynamics, one_star_total=excluded.one_star_total,
    raw_seller=excluded.raw_seller, raw_fb_increase=excluded.raw_fb_increase`
)

const itemRatingChunkSize = 500

// SaveItemRatings saves a batch of item rating rows (upsert, idempotent).
// Chunk size: 500 per transaction.
func (r *SQLiteSalesRepository) SaveItemRatings(ctx context.Context, rows []itemrating.Row) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}

	r.db.Exec("PRAGMA synchronous = OFF")
	defer r.db.Exec("PRAGMA synchronous = NORMAL")

	total := 0
	for i := 0; i < len(rows); i += itemRatingChunkSize {
		end := i + itemRatingChunkSize
		if end > len(rows) {
			end = len(rows)
		}
		n, err := r.saveItemRatingsChunk(ctx, rows[i:end])
		if err != nil {
			return 0, fmt.Errorf("save item_ratings chunk at offset %d: %w", i, err)
		}
		total += n
	}
	return total, nil
}

func (r *SQLiteSalesRepository) saveItemRatingsChunk(ctx context.Context, chunk []itemrating.Row) (int, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, upsertItemRatingSQL)
	if err != nil {
		return 0, fmt.Errorf("prepare item_ratings statement: %w", err)
	}
	defer stmt.Close()

	saved := 0
	for _, row := range chunk {
		if row.NmID <= 0 {
			continue // invalid row guard
		}
		if _, err := stmt.ExecContext(ctx,
			row.PeriodStart, row.PeriodEnd, row.NmID,
			row.VendorCode, row.Title, row.SubjectID, row.SubjectName, row.BrandName, row.TagID, row.TagName,
			boolToInt(row.PinnedFeedback), row.CardRating,
			row.FeedbackRatingCurrent, row.FeedbackRatingDynamics, row.FeedbackRatingPercentile,
			row.FeedbackCountInc, row.FeedbackCountDynamics,
			row.FiveStarInc, row.FiveStarDynamics,
			row.FourStarInc, row.FourStarDynamics,
			row.ThreeStarInc, row.ThreeStarDynamics,
			row.TwoStarInc, row.TwoStarDynamics,
			row.OneStarInc, row.OneStarDynamics,
			row.Disqualified, boolToInt(row.IsShadowed),
			string(row.RawItem),
		); err != nil {
			return saved, fmt.Errorf("upsert item_ratings nm_id=%d: %w", row.NmID, err)
		}
		saved++
	}

	return saved, tx.Commit()
}

// SaveSellerRating upserts the account-level block.
func (r *SQLiteSalesRepository) SaveSellerRating(ctx context.Context, row itemrating.SellerRow) (int, error) {
	_, err := r.db.ExecContext(ctx, upsertItemSellerSQL,
		row.PeriodStart, row.PeriodEnd,
		row.SellerRatingCurrent, row.SellerRatingDynamics,
		row.FbIncreaseCurrent, row.FbIncreaseTotal, row.FbIncreaseDynamics,
		row.FiveStarInc, row.FiveStarDynamics, row.FiveStarTotal,
		row.FourStarInc, row.FourStarDynamics, row.FourStarTotal,
		row.ThreeStarInc, row.ThreeStarDynamics, row.ThreeStarTotal,
		row.TwoStarInc, row.TwoStarDynamics, row.TwoStarTotal,
		row.OneStarInc, row.OneStarDynamics, row.OneStarTotal,
		string(row.RawSeller), string(row.RawFbIncrease),
	)
	if err != nil {
		return 0, fmt.Errorf("upsert item_rating_seller: %w", err)
	}
	return 1, nil
}

// CountItemRatings returns the row count stored for a period window.
func (r *SQLiteSalesRepository) CountItemRatings(ctx context.Context, periodStart, periodEnd string) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM item_ratings WHERE period_start = ? AND period_end = ?`,
		periodStart, periodEnd).Scan(&count)
	return count, err
}
