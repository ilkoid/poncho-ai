package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// feedbacksSchemaSQL defines the feedbacks table for PostgreSQL.
//
// Translated from pkg/storage/sqlite/schema.go (feedbacks table):
//   - INTEGER → INTEGER (kept for nullable int fields)
//   - BOOLEAN instead of INTEGER for bool fields
//   - id TEXT PRIMARY KEY (natural PK — UUID string)
//   - ON CONFLICT (id) DO UPDATE SET ... = EXCLUDED for upsert
const feedbacksSchemaSQL = `
CREATE TABLE IF NOT EXISTS feedbacks (
    id TEXT PRIMARY KEY,
    text TEXT NOT NULL DEFAULT '',
    pros TEXT NOT NULL DEFAULT '',
    cons TEXT NOT NULL DEFAULT '',
    product_valuation INTEGER,
    created_date TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL DEFAULT '',
    user_name TEXT NOT NULL DEFAULT '',
    was_viewed BOOLEAN NOT NULL DEFAULT FALSE,
    order_status TEXT NOT NULL DEFAULT '',
    matching_size TEXT NOT NULL DEFAULT '',
    is_able_supplier_feedback_valuation BOOLEAN NOT NULL DEFAULT FALSE,
    supplier_feedback_valuation INTEGER,
    is_able_supplier_product_valuation BOOLEAN NOT NULL DEFAULT FALSE,
    supplier_product_valuation INTEGER,
    is_able_return_product_orders BOOLEAN NOT NULL DEFAULT FALSE,
    return_product_orders_date TEXT,
    bables TEXT,
    last_order_shk_id BIGINT,
    last_order_created_at TEXT NOT NULL DEFAULT '',
    color TEXT NOT NULL DEFAULT '',
    subject_id BIGINT,
    subject_name TEXT NOT NULL DEFAULT '',
    parent_feedback_id TEXT,
    child_feedback_id TEXT,
    answer_text TEXT,
    answer_state TEXT,
    answer_editable BOOLEAN,
    photo_links TEXT,
    video_preview_image TEXT,
    video_link TEXT,
    video_duration_sec INTEGER,
    product_imt_id BIGINT,
    product_nm_id BIGINT,
    product_name TEXT NOT NULL DEFAULT '',
    supplier_article TEXT,
    supplier_name TEXT,
    brand_name TEXT,
    size TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_feedbacks_created_date ON feedbacks(created_date);
CREATE INDEX IF NOT EXISTS idx_feedbacks_nm_id ON feedbacks(product_nm_id);
`

// questionsSchemaSQL defines the questions table for PostgreSQL.
//
// Translated from pkg/storage/sqlite/schema.go (questions table):
//   - BOOLEAN instead of INTEGER for bool fields
//   - id TEXT PRIMARY KEY (natural PK — UUID string)
//   - ON CONFLICT (id) DO UPDATE SET ... = EXCLUDED for upsert
const questionsSchemaSQL = `
CREATE TABLE IF NOT EXISTS questions (
    id TEXT PRIMARY KEY,
    text TEXT NOT NULL DEFAULT '',
    created_date TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL DEFAULT '',
    was_viewed BOOLEAN NOT NULL DEFAULT FALSE,
    is_warned BOOLEAN NOT NULL DEFAULT FALSE,
    answer_text TEXT,
    answer_editable BOOLEAN,
    answer_create_date TEXT,
    product_imt_id BIGINT,
    product_nm_id BIGINT,
    product_name TEXT NOT NULL DEFAULT '',
    supplier_article TEXT NOT NULL DEFAULT '',
    supplier_name TEXT NOT NULL DEFAULT '',
    brand_name TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_questions_created_date ON questions(created_date);
CREATE INDEX IF NOT EXISTS idx_questions_nm_id ON questions(product_nm_id);
`

// feedbacksMigrations widens INTEGER columns to BIGINT for ID fields
// that can exceed int4 max (2,147,483,647). Safe: INTEGER→BIGINT is a
// widening conversion — no data loss. These statements are idempotent
// (re-running on already-BIGINT columns is a no-op).
const feedbacksMigrations = `
ALTER TABLE feedbacks ALTER COLUMN last_order_shk_id TYPE BIGINT;
ALTER TABLE feedbacks ALTER COLUMN subject_id TYPE BIGINT;
ALTER TABLE feedbacks ALTER COLUMN product_imt_id TYPE BIGINT;
ALTER TABLE feedbacks ALTER COLUMN product_nm_id TYPE BIGINT;

ALTER TABLE questions ALTER COLUMN product_imt_id TYPE BIGINT;
ALTER TABLE questions ALTER COLUMN product_nm_id TYPE BIGINT;
`

// initFeedbacksSchema creates feedbacks and questions tables in PostgreSQL.
// Also runs widening migrations for columns that were originally INTEGER.
func initFeedbacksSchema(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, feedbacksSchemaSQL); err != nil {
		return fmt.Errorf("feedbacks schema: %w", err)
	}
	if _, err := pool.Exec(ctx, questionsSchemaSQL); err != nil {
		return fmt.Errorf("questions schema: %w", err)
	}
	if _, err := pool.Exec(ctx, feedbacksMigrations); err != nil {
		return fmt.Errorf("feedbacks migrations (int4→bigint): %w", err)
	}
	return nil
}
