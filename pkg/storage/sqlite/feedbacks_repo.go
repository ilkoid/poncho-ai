// Package sqlite provides feedbacks storage methods on SQLiteSalesRepository.
//
// Methods for saving and counting feedbacks/questions from WB Feedbacks API.
// Schema is created by initSchema() in repository.go.
package sqlite

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

const insertFeedbackSQL = `
INSERT OR REPLACE INTO feedbacks (
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
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`

// SaveFeedbacks saves batch of feedbacks. Returns count of inserted rows.
func (r *SQLiteSalesRepository) SaveFeedbacks(ctx context.Context, items []wb.FeedbackFull) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, insertFeedbackSQL)
	if err != nil {
		return 0, fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, f := range items {
		_, err := stmt.ExecContext(ctx,
			f.ID, f.Text, f.Pros, f.Cons, f.ProductValuation, f.CreatedDate, f.State, f.UserName,
			fbBoolToInt(f.WasViewed), f.OrderStatus, f.MatchingSize,
			fbBoolToInt(f.IsAbleSupplierFeedbackValuation), fbNullInt(f.SupplierFeedbackValuation),
			fbBoolToInt(f.IsAbleSupplierProductValuation), fbNullInt(f.SupplierProductValuation),
			fbBoolToInt(f.IsAbleReturnProductOrders), f.ReturnProductOrdersDate, fbStringsToJSON(f.Bables),
			f.LastOrderShkId, f.LastOrderCreatedAt, f.Color, f.SubjectId, f.SubjectName,
			f.ParentFeedbackId, f.ChildFeedbackId,
			f.AnswerText, f.AnswerState, fbBoolPtrToInt(f.AnswerEditable),
			f.PhotoLinksJSON, f.VideoPreviewImage, f.VideoLink, fbNullIntPtr(f.VideoDurationSec),
			f.ProductImtId, f.ProductNmId, f.ProductName,
			f.SupplierArticle, f.SupplierName, f.BrandName, f.Size,
		)
		if err != nil {
			return 0, fmt.Errorf("insert feedback id=%s: %w", f.ID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}

	return len(items), nil
}

const insertQuestionSQL = `
INSERT OR REPLACE INTO questions (
    id, text, created_date, state, was_viewed, is_warned,
    answer_text, answer_editable, answer_create_date,
    product_imt_id, product_nm_id, product_name,
    supplier_article, supplier_name, brand_name
) VALUES (
    ?,?,?,?,?,?,
    ?,?,?,
    ?,?,?,?,
    ?,?
)`

// SaveQuestions saves batch of questions. Returns count of inserted rows.
func (r *SQLiteSalesRepository) SaveQuestions(ctx context.Context, items []wb.QuestionFull) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, insertQuestionSQL)
	if err != nil {
		return 0, fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, q := range items {
		_, err := stmt.ExecContext(ctx,
			q.ID, q.Text, q.CreatedDate, q.State,
			fbBoolToInt(q.WasViewed), fbBoolToInt(q.IsWarned),
			q.AnswerText, fbBoolPtrToInt(q.AnswerEditable), q.AnswerCreateDate,
			q.ProductImtId, q.ProductNmId, q.ProductName,
			q.SupplierArticle, q.SupplierName, q.BrandName,
		)
		if err != nil {
			return 0, fmt.Errorf("insert question id=%s: %w", q.ID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}

	return len(items), nil
}

// CountFeedbacks returns total number of feedbacks in the database.
func (r *SQLiteSalesRepository) CountFeedbacks(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, "SELECT count(*) FROM feedbacks").Scan(&count)
	return count, err
}

// CountFeedbacksWithAnswer returns number of feedbacks that have a seller answer.
func (r *SQLiteSalesRepository) CountFeedbacksWithAnswer(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, "SELECT count(*) FROM feedbacks WHERE answer_text IS NOT NULL").Scan(&count)
	return count, err
}

// CountQuestions returns total number of questions in the database.
func (r *SQLiteSalesRepository) CountQuestions(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, "SELECT count(*) FROM questions").Scan(&count)
	return count, err
}

// CountQuestionsWithAnswer returns number of questions that have a seller answer.
func (r *SQLiteSalesRepository) CountQuestionsWithAnswer(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, "SELECT count(*) FROM questions WHERE answer_text IS NOT NULL").Scan(&count)
	return count, err
}

// ============================================================================
// Helpers for Go → SQLite type conversion
// ============================================================================

func fbBoolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func fbBoolPtrToInt(b *bool) *int {
	if b == nil {
		return nil
	}
	v := fbBoolToInt(*b)
	return &v
}

func fbNullInt(v int) *int {
	return &v
}

func fbNullIntPtr(v *int) *int {
	return v
}

func fbStringsToJSON(v []string) *string {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	s := string(b)
	return &s
}
