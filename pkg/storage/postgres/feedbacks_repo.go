package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ilkoid/poncho-ai/pkg/feedbacks"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Compile-time assertion: PgFeedbacksRepo implements feedbacks.Writer.
var _ feedbacks.Writer = (*PgFeedbacksRepo)(nil)

// PgFeedbacksRepo implements feedbacks.Writer for PostgreSQL.
// Focused repository (ISP) — only feedbacks + questions persistence methods.
type PgFeedbacksRepo struct {
	pool *pgxpool.Pool
}

// NewPgFeedbacksRepo creates a new PostgreSQL feedbacks repository.
func NewPgFeedbacksRepo(pool *pgxpool.Pool) *PgFeedbacksRepo {
	return &PgFeedbacksRepo{pool: pool}
}

// InitSchema creates feedbacks and questions tables if they don't exist.
func (r *PgFeedbacksRepo) InitSchema(ctx context.Context) error {
	return initFeedbacksSchema(ctx, r.pool)
}

const pgFBChunkSize = 500

// Multi-row INSERT SQL fragments for feedbacks table.
const (
	insertFeedbackCols = 39

	insertFeedbackPrefixSQL = `INSERT INTO feedbacks (
	    id, text, pros, cons, product_valuation, created_date, state, user_name,
	    was_viewed, order_status, matching_size,
	    is_able_supplier_feedback_valuation, supplier_feedback_valuation,
	    is_able_supplier_product_valuation, supplier_product_valuation,
	    is_able_return_product_orders, return_product_orders_date, bables,
	    last_order_shk_id, last_order_created_at, color, subject_id, subject_name,
	    parent_feedback_id, child_feedback_id,
	    answer_text, answer_state, answer_editable,
	    photo_links, video_preview_image, video_link, video_duration_sec,
	    product_imt_id, product_nm_id, product_name,
	    supplier_article, supplier_name, brand_name, size
	) VALUES `

	insertFeedbackOnConflictSQL = `
	ON CONFLICT (id) DO UPDATE SET
	    text                                = EXCLUDED.text,
	    pros                                = EXCLUDED.pros,
	    cons                                = EXCLUDED.cons,
	    product_valuation                   = EXCLUDED.product_valuation,
	    created_date                        = EXCLUDED.created_date,
	    state                               = EXCLUDED.state,
	    user_name                           = EXCLUDED.user_name,
	    was_viewed                          = EXCLUDED.was_viewed,
	    order_status                        = EXCLUDED.order_status,
	    matching_size                       = EXCLUDED.matching_size,
	    is_able_supplier_feedback_valuation = EXCLUDED.is_able_supplier_feedback_valuation,
	    supplier_feedback_valuation         = EXCLUDED.supplier_feedback_valuation,
	    is_able_supplier_product_valuation  = EXCLUDED.is_able_supplier_product_valuation,
	    supplier_product_valuation          = EXCLUDED.supplier_product_valuation,
	    is_able_return_product_orders       = EXCLUDED.is_able_return_product_orders,
	    return_product_orders_date          = EXCLUDED.return_product_orders_date,
	    bables                              = EXCLUDED.bables,
	    last_order_shk_id                   = EXCLUDED.last_order_shk_id,
	    last_order_created_at               = EXCLUDED.last_order_created_at,
	    color                               = EXCLUDED.color,
	    subject_id                          = EXCLUDED.subject_id,
	    subject_name                        = EXCLUDED.subject_name,
	    parent_feedback_id                  = EXCLUDED.parent_feedback_id,
	    child_feedback_id                   = EXCLUDED.child_feedback_id,
	    answer_text                         = EXCLUDED.answer_text,
	    answer_state                        = EXCLUDED.answer_state,
	    answer_editable                     = EXCLUDED.answer_editable,
	    photo_links                         = EXCLUDED.photo_links,
	    video_preview_image                 = EXCLUDED.video_preview_image,
	    video_link                          = EXCLUDED.video_link,
	    video_duration_sec                  = EXCLUDED.video_duration_sec,
	    product_imt_id                      = EXCLUDED.product_imt_id,
	    product_nm_id                       = EXCLUDED.product_nm_id,
	    product_name                        = EXCLUDED.product_name,
	    supplier_article                    = EXCLUDED.supplier_article,
	    supplier_name                       = EXCLUDED.supplier_name,
	    brand_name                          = EXCLUDED.brand_name,
	    size                                = EXCLUDED.size`
)

var insertFeedbackFullChunkSQL = BuildMultiRowInsert(insertFeedbackPrefixSQL, insertFeedbackOnConflictSQL, pgFBChunkSize, insertFeedbackCols)

// Multi-row INSERT SQL fragments for questions table.
const (
	insertQuestionCols = 15

	insertQuestionPrefixSQL = `INSERT INTO questions (
	    id, text, created_date, state, was_viewed, is_warned,
	    answer_text, answer_editable, answer_create_date,
	    product_imt_id, product_nm_id, product_name,
	    supplier_article, supplier_name, brand_name
	) VALUES `

	insertQuestionOnConflictSQL = `
	ON CONFLICT (id) DO UPDATE SET
	    text              = EXCLUDED.text,
	    created_date      = EXCLUDED.created_date,
	    state             = EXCLUDED.state,
	    was_viewed        = EXCLUDED.was_viewed,
	    is_warned         = EXCLUDED.is_warned,
	    answer_text       = EXCLUDED.answer_text,
	    answer_editable   = EXCLUDED.answer_editable,
	    answer_create_date = EXCLUDED.answer_create_date,
	    product_imt_id    = EXCLUDED.product_imt_id,
	    product_nm_id     = EXCLUDED.product_nm_id,
	    product_name      = EXCLUDED.product_name,
	    supplier_article  = EXCLUDED.supplier_article,
	    supplier_name     = EXCLUDED.supplier_name,
	    brand_name        = EXCLUDED.brand_name`
)

var insertQuestionFullChunkSQL = BuildMultiRowInsert(insertQuestionPrefixSQL, insertQuestionOnConflictSQL, pgFBChunkSize, insertQuestionCols)

// SaveFeedbacks saves a batch of feedbacks. Returns count of saved rows.
func (r *PgFeedbacksRepo) SaveFeedbacks(ctx context.Context, items []wb.FeedbackFull) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}

	total := 0
	for i := 0; i < len(items); i += pgFBChunkSize {
		end := min(i+pgFBChunkSize, len(items))
		chunk := items[i:end]

		n, err := r.saveFeedbacksChunk(ctx, chunk)
		if err != nil {
			return 0, fmt.Errorf("feedbacks chunk at offset %d: %w", i, err)
		}
		total += n
	}
	return total, nil
}

// saveFeedbacksChunk saves up to pgFBChunkSize feedbacks using a single multi-row INSERT.
func (r *PgFeedbacksRepo) saveFeedbacksChunk(ctx context.Context, chunk []wb.FeedbackFull) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	args := make([]any, 0, len(chunk)*insertFeedbackCols)
	for _, f := range chunk {
		bablesJSON, err := pgStringsToJSON(f.Bables)
		if err != nil {
			return 0, fmt.Errorf("marshal bables for id=%s: %w", f.ID, err)
		}

		args = append(args,
			f.ID, f.Text, f.Pros, f.Cons, f.ProductValuation, f.CreatedDate,
			f.State, f.UserName, f.WasViewed, f.OrderStatus, f.MatchingSize,
			f.IsAbleSupplierFeedbackValuation, f.SupplierFeedbackValuation,
			f.IsAbleSupplierProductValuation, f.SupplierProductValuation,
			f.IsAbleReturnProductOrders, f.ReturnProductOrdersDate, bablesJSON,
			f.LastOrderShkId, f.LastOrderCreatedAt, f.Color, f.SubjectId, f.SubjectName,
			f.ParentFeedbackId, f.ChildFeedbackId,
			f.AnswerText, f.AnswerState, f.AnswerEditable,
			f.PhotoLinksJSON, f.VideoPreviewImage, f.VideoLink, f.VideoDurationSec,
			f.ProductImtId, f.ProductNmId, f.ProductName,
			f.SupplierArticle, f.SupplierName, f.BrandName, f.Size,
		)
	}

	query := insertFeedbackFullChunkSQL
	if len(chunk) < pgFBChunkSize {
		query = BuildMultiRowInsert(insertFeedbackPrefixSQL, insertFeedbackOnConflictSQL, len(chunk), insertFeedbackCols)
	}

	tag, err := tx.Exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("save feedbacks batch (size %d): %w", len(chunk), err)
	}
	return int(tag.RowsAffected()), tx.Commit(ctx)
}

// SaveQuestions saves a batch of questions. Returns count of saved rows.
func (r *PgFeedbacksRepo) SaveQuestions(ctx context.Context, items []wb.QuestionFull) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}

	total := 0
	for i := 0; i < len(items); i += pgFBChunkSize {
		end := min(i+pgFBChunkSize, len(items))
		chunk := items[i:end]

		n, err := r.saveQuestionsChunk(ctx, chunk)
		if err != nil {
			return 0, fmt.Errorf("questions chunk at offset %d: %w", i, err)
		}
		total += n
	}
	return total, nil
}

// saveQuestionsChunk saves up to pgFBChunkSize questions using a single multi-row INSERT.
func (r *PgFeedbacksRepo) saveQuestionsChunk(ctx context.Context, chunk []wb.QuestionFull) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	args := make([]any, 0, len(chunk)*insertQuestionCols)
	for _, q := range chunk {
		args = append(args,
			q.ID, q.Text, q.CreatedDate, q.State, q.WasViewed, q.IsWarned,
			q.AnswerText, q.AnswerEditable, q.AnswerCreateDate,
			q.ProductImtId, q.ProductNmId, q.ProductName,
			q.SupplierArticle, q.SupplierName, q.BrandName,
		)
	}

	query := insertQuestionFullChunkSQL
	if len(chunk) < pgFBChunkSize {
		query = BuildMultiRowInsert(insertQuestionPrefixSQL, insertQuestionOnConflictSQL, len(chunk), insertQuestionCols)
	}

	tag, err := tx.Exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("save questions batch (size %d): %w", len(chunk), err)
	}
	return int(tag.RowsAffected()), tx.Commit(ctx)
}

// CountFeedbacks returns total number of feedbacks in the database.
func (r *PgFeedbacksRepo) CountFeedbacks(ctx context.Context) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx, "SELECT count(*) FROM feedbacks").Scan(&count)
	return count, err
}

// CountQuestions returns total number of questions in the database.
func (r *PgFeedbacksRepo) CountQuestions(ctx context.Context) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx, "SELECT count(*) FROM questions").Scan(&count)
	return count, err
}

// pgStringsToJSON marshals a string slice to JSON for PG TEXT column.
// Returns nil for nil/empty slices (PG NULL).
func pgStringsToJSON(v []string) (any, error) {
	if len(v) == 0 {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	s := string(b)
	return &s, nil
}
