// Package main provides SQLite storage for WB feedbacks and questions.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	_ "github.com/mattn/go-sqlite3"
)

// FeedbacksRepo manages SQLite storage for feedbacks and questions.
// Concrete type (no interface) — only 1 implementation per dev_solid.md.
type FeedbacksRepo struct {
	db *sql.DB
}

// NewFeedbacksRepo creates repository with optimized SQLite settings.
func NewFeedbacksRepo(dbPath string) (*FeedbacksRepo, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// PRAGMA settings for write-heavy workloads
	pragmas := []struct {
		key, val string
	}{
		{"journal_mode", "WAL"},      // Write-Ahead Logging: faster INSERT, parallel reads
		{"synchronous", "NORMAL"},     // balance speed vs safety
		{"cache_size", "-64000"},      // 64MB cache (default 2MB)
		{"temp_store", "MEMORY"},      // temp tables in RAM
	}
	for _, p := range pragmas {
		if _, err := db.Exec(fmt.Sprintf("PRAGMA %s = %s", p.key, p.val)); err != nil {
			db.Close()
			return nil, fmt.Errorf("PRAGMA %s: %w", p.key, err)
		}
	}

	repo := &FeedbacksRepo{db: db}
	if err := repo.initSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}

	return repo, nil
}

const createFeedbacksTable = `
CREATE TABLE IF NOT EXISTS feedbacks (
    id                              TEXT PRIMARY KEY,
    text                            TEXT NOT NULL DEFAULT '',
    pros                            TEXT NOT NULL DEFAULT '',
    cons                            TEXT NOT NULL DEFAULT '',
    product_valuation               INTEGER,
    created_date                    TEXT NOT NULL,
    state                           TEXT NOT NULL DEFAULT '',
    user_name                       TEXT NOT NULL DEFAULT '',
    was_viewed                      INTEGER NOT NULL DEFAULT 0,
    order_status                    TEXT NOT NULL DEFAULT '',
    matching_size                   TEXT NOT NULL DEFAULT '',
    is_able_supplier_feedback_valuation INTEGER NOT NULL DEFAULT 0,
    supplier_feedback_valuation     INTEGER,
    is_able_supplier_product_valuation INTEGER NOT NULL DEFAULT 0,
    supplier_product_valuation      INTEGER,
    is_able_return_product_orders   INTEGER NOT NULL DEFAULT 0,
    return_product_orders_date      TEXT,
    bables                          TEXT,
    last_order_shk_id               INTEGER,
    last_order_created_at           TEXT,
    color                           TEXT NOT NULL DEFAULT '',
    subject_id                      INTEGER,
    subject_name                    TEXT NOT NULL DEFAULT '',
    parent_feedback_id              TEXT,
    child_feedback_id               TEXT,
    answer_text                     TEXT,
    answer_state                    TEXT,
    answer_editable                 INTEGER,
    photo_links                     TEXT,
    video_preview_image             TEXT,
    video_link                      TEXT,
    video_duration_sec              INTEGER,
    product_imt_id                  INTEGER,
    product_nm_id                   INTEGER,
    product_name                    TEXT NOT NULL DEFAULT '',
    supplier_article                TEXT,
    supplier_name                   TEXT,
    brand_name                      TEXT,
    size                            TEXT NOT NULL DEFAULT ''
);`

const createQuestionsTable = `
CREATE TABLE IF NOT EXISTS questions (
    id                  TEXT PRIMARY KEY,
    text                TEXT NOT NULL DEFAULT '',
    created_date        TEXT NOT NULL,
    state               TEXT NOT NULL DEFAULT '',
    was_viewed          INTEGER NOT NULL DEFAULT 0,
    is_warned           INTEGER NOT NULL DEFAULT 0,
    answer_text         TEXT,
    answer_editable     INTEGER,
    answer_create_date  TEXT,
    product_imt_id      INTEGER,
    product_nm_id       INTEGER,
    product_name        TEXT NOT NULL DEFAULT '',
    supplier_article    TEXT NOT NULL DEFAULT '',
    supplier_name       TEXT NOT NULL DEFAULT '',
    brand_name          TEXT NOT NULL DEFAULT ''
);`

func (r *FeedbacksRepo) initSchema() error {
	for _, ddl := range []string{createFeedbacksTable, createQuestionsTable} {
		if _, err := r.db.Exec(ddl); err != nil {
			return fmt.Errorf("create table: %w", err)
		}
	}

	// Indexes on API fields (no computed columns)
	indexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_feedbacks_created_date ON feedbacks(created_date)",
		"CREATE INDEX IF NOT EXISTS idx_questions_created_date ON questions(created_date)",
		"CREATE INDEX IF NOT EXISTS idx_feedbacks_nm_date ON feedbacks(product_nm_id, created_date)",
		"CREATE INDEX IF NOT EXISTS idx_questions_nm_date ON questions(product_nm_id, created_date)",
	}
	for _, idx := range indexes {
		if _, err := r.db.Exec(idx); err != nil {
			return fmt.Errorf("create index: %w", err)
		}
	}

	return nil
}

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
func (r *FeedbacksRepo) SaveFeedbacks(ctx context.Context, items []FeedbackFull) (int, error) {
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
			boolToInt(f.WasViewed), f.OrderStatus, f.MatchingSize,
			boolToInt(f.IsAbleSupplierFeedbackValuation), nullInt(f.SupplierFeedbackValuation),
			boolToInt(f.IsAbleSupplierProductValuation), nullInt(f.SupplierProductValuation),
			boolToInt(f.IsAbleReturnProductOrders), f.ReturnProductOrdersDate, stringsToJSON(f.Bables),
			f.LastOrderShkId, f.LastOrderCreatedAt, f.Color, f.SubjectId, f.SubjectName,
			f.ParentFeedbackId, f.ChildFeedbackId,
			f.AnswerText, f.AnswerState, boolPtrToInt(f.AnswerEditable),
			f.PhotoLinksJSON, f.VideoPreviewImage, f.VideoLink, nullIntPtr(f.VideoDurationSec),
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
func (r *FeedbacksRepo) SaveQuestions(ctx context.Context, items []QuestionFull) (int, error) {
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
			boolToInt(q.WasViewed), boolToInt(q.IsWarned),
			q.AnswerText, boolPtrToInt(q.AnswerEditable), q.AnswerCreateDate,
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
func (r *FeedbacksRepo) CountFeedbacks(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, "SELECT count(*) FROM feedbacks").Scan(&count)
	return count, err
}

// CountFeedbacksWithAnswer returns number of feedbacks that have a seller answer.
func (r *FeedbacksRepo) CountFeedbacksWithAnswer(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, "SELECT count(*) FROM feedbacks WHERE answer_text IS NOT NULL").Scan(&count)
	return count, err
}

// CountQuestions returns total number of questions in the database.
func (r *FeedbacksRepo) CountQuestions(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, "SELECT count(*) FROM questions").Scan(&count)
	return count, err
}

// CountQuestionsWithAnswer returns number of questions that have a seller answer.
func (r *FeedbacksRepo) CountQuestionsWithAnswer(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, "SELECT count(*) FROM questions WHERE answer_text IS NOT NULL").Scan(&count)
	return count, err
}

// Close closes the database connection.
func (r *FeedbacksRepo) Close() error {
	return r.db.Close()
}

// ============================================================================
// Helpers for Go → SQLite type conversion
// ============================================================================

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func boolPtrToInt(b *bool) *int {
	if b == nil {
		return nil
	}
	v := boolToInt(*b)
	return &v
}

func nullInt(v int) *int {
	return &v
}

func nullIntPtr(v *int) *int {
	return v
}

func stringsToJSON(v []string) *string {
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
