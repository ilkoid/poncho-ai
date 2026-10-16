package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ilkoid/poncho-ai/pkg/itemrating"
)

// Compile-time assertion: PgItemRatingRepo implements itemrating.Writer.
var _ itemrating.Writer = (*PgItemRatingRepo)(nil)

// PgItemRatingRepo implements itemrating.Writer for PostgreSQL.
// Focused repository (ISP) — only item rating persistence methods.
type PgItemRatingRepo struct {
	pool *pgxpool.Pool
}

// NewPgItemRatingRepo creates a new PostgreSQL item rating repository.
func NewPgItemRatingRepo(pool *pgxpool.Pool) *PgItemRatingRepo {
	return &PgItemRatingRepo{pool: pool}
}

// InitSchema creates item_ratings and item_rating_seller tables if they don't exist.
func (r *PgItemRatingRepo) InitSchema(ctx context.Context) error {
	return initItemRatingSchema(ctx, r.pool)
}

const (
	pgItemRatingChunkSize = 500

	insertItemRatingPrefixSQL = `INSERT INTO item_ratings (
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
	) VALUES `
	insertItemRatingOnConflictSQL = `
	ON CONFLICT (nm_id, period_start, period_end) DO UPDATE SET
	    vendor_code                   = EXCLUDED.vendor_code,
	    title                         = EXCLUDED.title,
	    subject_id                    = EXCLUDED.subject_id,
	    subject_name                  = EXCLUDED.subject_name,
	    brand_name                    = EXCLUDED.brand_name,
	    tag_id                        = EXCLUDED.tag_id,
	    tag_name                      = EXCLUDED.tag_name,
	    pinned_feedback               = EXCLUDED.pinned_feedback,
	    card_rating                   = EXCLUDED.card_rating,
	    feedback_rating_current       = EXCLUDED.feedback_rating_current,
	    feedback_rating_dynamics      = EXCLUDED.feedback_rating_dynamics,
	    feedback_rating_percentile    = EXCLUDED.feedback_rating_percentile,
	    feedback_count_inc            = EXCLUDED.feedback_count_inc,
	    feedback_count_dynamics       = EXCLUDED.feedback_count_dynamics,
	    five_star_inc                 = EXCLUDED.five_star_inc,
	    five_star_dynamics            = EXCLUDED.five_star_dynamics,
	    four_star_inc                 = EXCLUDED.four_star_inc,
	    four_star_dynamics            = EXCLUDED.four_star_dynamics,
	    three_star_inc                = EXCLUDED.three_star_inc,
	    three_star_dynamics           = EXCLUDED.three_star_dynamics,
	    two_star_inc                  = EXCLUDED.two_star_inc,
	    two_star_dynamics             = EXCLUDED.two_star_dynamics,
	    one_star_inc                  = EXCLUDED.one_star_inc,
	    one_star_dynamics             = EXCLUDED.one_star_dynamics,
	    disqualified                  = EXCLUDED.disqualified,
	    is_shadowed                   = EXCLUDED.is_shadowed,
	    raw_item                      = EXCLUDED.raw_item`
	insertItemRatingCols = 30

	insertItemSellerPrefixSQL = `INSERT INTO item_rating_seller (
	    period_start, period_end,
	    seller_rating_current, seller_rating_dynamics,
	    fb_increase_current, fb_increase_total, fb_increase_dynamics,
	    five_star_inc, five_star_dynamics, five_star_total,
	    four_star_inc, four_star_dynamics, four_star_total,
	    three_star_inc, three_star_dynamics, three_star_total,
	    two_star_inc, two_star_dynamics, two_star_total,
	    one_star_inc, one_star_dynamics, one_star_total,
	    raw_seller, raw_fb_increase
	) VALUES `
	insertItemSellerOnConflictSQL = `
	ON CONFLICT (period_start, period_end) DO UPDATE SET
	    seller_rating_current  = EXCLUDED.seller_rating_current,
	    seller_rating_dynamics = EXCLUDED.seller_rating_dynamics,
	    fb_increase_current    = EXCLUDED.fb_increase_current,
	    fb_increase_total      = EXCLUDED.fb_increase_total,
	    fb_increase_dynamics   = EXCLUDED.fb_increase_dynamics,
	    five_star_inc          = EXCLUDED.five_star_inc,
	    five_star_dynamics     = EXCLUDED.five_star_dynamics,
	    five_star_total        = EXCLUDED.five_star_total,
	    four_star_inc          = EXCLUDED.four_star_inc,
	    four_star_dynamics     = EXCLUDED.four_star_dynamics,
	    four_star_total        = EXCLUDED.four_star_total,
	    three_star_inc         = EXCLUDED.three_star_inc,
	    three_star_dynamics    = EXCLUDED.three_star_dynamics,
	    three_star_total       = EXCLUDED.three_star_total,
	    two_star_inc           = EXCLUDED.two_star_inc,
	    two_star_dynamics      = EXCLUDED.two_star_dynamics,
	    two_star_total         = EXCLUDED.two_star_total,
	    one_star_inc           = EXCLUDED.one_star_inc,
	    one_star_dynamics      = EXCLUDED.one_star_dynamics,
	    one_star_total         = EXCLUDED.one_star_total,
	    raw_seller             = EXCLUDED.raw_seller,
	    raw_fb_increase        = EXCLUDED.raw_fb_increase`
	insertItemSellerCols = 24
)

// SaveItemRatings upserts a batch of item rows in chunks of 500 per transaction.
// Rows are deduplicated by nm_id first: PG ON CONFLICT forbids affecting the
// same row twice within one multi-row INSERT (SQLSTATE 21000) — duplicates can
// legitimately appear when hourly data drift shifts an item between pages.
func (r *PgItemRatingRepo) SaveItemRatings(ctx context.Context, rows []itemrating.Row) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}

	dedup := make(map[int64]itemrating.Row, len(rows))
	for _, row := range rows {
		if row.NmID > 0 {
			dedup[row.NmID] = row // last occurrence wins (drift = newer state)
		}
	}
	uniq := make([]itemrating.Row, 0, len(dedup))
	for _, row := range dedup {
		uniq = append(uniq, row)
	}

	var saved int
	for i := 0; i < len(uniq); i += pgItemRatingChunkSize {
		end := min(i+pgItemRatingChunkSize, len(uniq))
		chunk := uniq[i:end]

		args := make([]any, 0, len(chunk)*insertItemRatingCols)
		for _, row := range chunk {
			args = append(args,
				row.PeriodStart, row.PeriodEnd, row.NmID,
				row.VendorCode, row.Title, row.SubjectID, row.SubjectName, row.BrandName, row.TagID, row.TagName,
				row.PinnedFeedback, row.CardRating,
				row.FeedbackRatingCurrent, row.FeedbackRatingDynamics, row.FeedbackRatingPercentile,
				row.FeedbackCountInc, row.FeedbackCountDynamics,
				row.FiveStarInc, row.FiveStarDynamics,
				row.FourStarInc, row.FourStarDynamics,
				row.ThreeStarInc, row.ThreeStarDynamics,
				row.TwoStarInc, row.TwoStarDynamics,
				row.OneStarInc, row.OneStarDynamics,
				row.Disqualified, row.IsShadowed,
				string(row.RawItem), // string → JSONB ( []byte would encode as bytea )
			)
		}

		query := BuildMultiRowInsert(insertItemRatingPrefixSQL, insertItemRatingOnConflictSQL, len(chunk), insertItemRatingCols)
		tag, err := r.pool.Exec(ctx, query, args...)
		if err != nil {
			return saved, fmt.Errorf("upsert item_ratings chunk [%d:%d] of %d: %w", i, end, len(uniq), err)
		}
		saved += int(tag.RowsAffected())
	}
	return saved, nil
}

// SaveSellerRating upserts the account-level block.
func (r *PgItemRatingRepo) SaveSellerRating(ctx context.Context, row itemrating.SellerRow) (int, error) {
	query := BuildMultiRowInsert(insertItemSellerPrefixSQL, insertItemSellerOnConflictSQL, 1, insertItemSellerCols)
	args := []any{
		row.PeriodStart, row.PeriodEnd,
		row.SellerRatingCurrent, row.SellerRatingDynamics,
		row.FbIncreaseCurrent, row.FbIncreaseTotal, row.FbIncreaseDynamics,
		row.FiveStarInc, row.FiveStarDynamics, row.FiveStarTotal,
		row.FourStarInc, row.FourStarDynamics, row.FourStarTotal,
		row.ThreeStarInc, row.ThreeStarDynamics, row.ThreeStarTotal,
		row.TwoStarInc, row.TwoStarDynamics, row.TwoStarTotal,
		row.OneStarInc, row.OneStarDynamics, row.OneStarTotal,
		string(row.RawSeller), string(row.RawFbIncrease),
	}
	tag, err := r.pool.Exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("upsert item_rating_seller: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// CountItemRatings returns the row count stored for a period window.
func (r *PgItemRatingRepo) CountItemRatings(ctx context.Context, periodStart, periodEnd string) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM item_ratings WHERE period_start = $1 AND period_end = $2`,
		periodStart, periodEnd).Scan(&count)
	return count, err
}
