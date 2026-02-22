// Package wb provides snapshot feedbacks service for E2E testing.
package wb

import (
	"context"
	"database/sql"
	"fmt"
)

// Ensure snapshotFeedbacksService implements FeedbackService.
var _ FeedbackService = (*snapshotFeedbacksService)(nil)

// snapshotFeedbacksService implements FeedbackService backed by SQLite.
type snapshotFeedbacksService struct {
	db *sql.DB
}

// GetFeedbacks retrieves product feedbacks from snapshot database.
// Supports filtering by isAnswered and nmID with pagination.
func (s *snapshotFeedbacksService) GetFeedbacks(ctx context.Context, req FeedbacksRequest) (*FeedbacksResponse, error) {
	// Validation
	if req.Take <= 0 || req.Take > 100 {
		req.Take = 100 // Default
	}

	// Build query
	query := `
		SELECT
			feedback_id, text, product_valuation, created_date, user_name,
			nm_id, product_name, brand_name, supplier_article,
			has_answer, answer_text
		FROM feedbacks_items
		WHERE 1=1
	`
	args := make([]any, 0)

	// Filter by isAnswered
	if req.IsAnswered != nil {
		if *req.IsAnswered {
			query += " AND has_answer = 1"
		} else {
			query += " AND has_answer = 0"
		}
	}

	// Filter by nmID
	if req.NmID > 0 {
		query += " AND nm_id = ?"
		args = append(args, req.NmID)
	}

	// Order and pagination
	query += " ORDER BY created_date DESC LIMIT ? OFFSET ?"
	args = append(args, req.Take, req.Noffset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query feedbacks: %w", err)
	}
	defer rows.Close()

	feedbacks := make([]Feedback, 0)
	for rows.Next() {
		var fb Feedback
		var hasAnswer int
		var answerText sql.NullString
		var productDetails FeedbackProduct

		if err := rows.Scan(
			&fb.ID, &fb.Text, &fb.ProductValuation, &fb.CreatedDate, &fb.UserName,
			&productDetails.NmId, &productDetails.ProductName, &productDetails.BrandName, &productDetails.SupplierArticle,
			&hasAnswer, &answerText,
		); err != nil {
			return nil, fmt.Errorf("scan feedback: %w", err)
		}

		fb.ProductDetails = productDetails

		// Set answer if exists
		if hasAnswer == 1 && answerText.Valid {
			fb.Answer = &FeedbackAnswer{
				Text: answerText.String,
			}
		}

		feedbacks = append(feedbacks, fb)
	}

	// Get count of unanswered
	var countUnanswered int
	countQuery := "SELECT COUNT(*) FROM feedbacks_items WHERE has_answer = 0"
	if req.NmID > 0 {
		countQuery += " AND nm_id = ?"
		s.db.QueryRowContext(ctx, countQuery, req.NmID).Scan(&countUnanswered)
	} else {
		s.db.QueryRowContext(ctx, countQuery).Scan(&countUnanswered)
	}

	return &FeedbacksResponse{
		Data: struct {
			Feedbacks       []Feedback `json:"feedbacks"`
			CountUnanswered int        `json:"countUnanswered"`
			CountArchive    int        `json:"countArchive"`
		}{
			Feedbacks:       feedbacks,
			CountUnanswered: countUnanswered,
			CountArchive:    0, // Not tracked in snapshot
		},
	}, nil
}

// GetQuestions retrieves product questions from snapshot database.
// Supports filtering by isAnswered and nmID with pagination.
func (s *snapshotFeedbacksService) GetQuestions(ctx context.Context, req QuestionsRequest) (*QuestionsResponse, error) {
	// Validation
	if req.Take <= 0 || req.Take > 100 {
		req.Take = 100 // Default
	}

	// Build query
	query := `
		SELECT
			question_id, text, created_date, state, was_viewed,
			nm_id, product_name, brand_name, supplier_article,
			has_answer, answer_text
		FROM questions_items
		WHERE 1=1
	`
	args := make([]any, 0)

	// Filter by isAnswered
	if req.IsAnswered != nil {
		if *req.IsAnswered {
			query += " AND has_answer = 1"
		} else {
			query += " AND has_answer = 0"
		}
	}

	// Filter by nmID
	if req.NmID > 0 {
		query += " AND nm_id = ?"
		args = append(args, req.NmID)
	}

	// Order and pagination
	query += " ORDER BY created_date DESC LIMIT ? OFFSET ?"
	args = append(args, req.Take, req.Noffset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query questions: %w", err)
	}
	defer rows.Close()

	questions := make([]Question, 0)
	for rows.Next() {
		var q Question
		var hasAnswer int
		var answerText sql.NullString
		var productDetails QuestionProduct
		var wasViewed int

		if err := rows.Scan(
			&q.ID, &q.Text, &q.CreatedDate, &q.State, &wasViewed,
			&productDetails.NmId, &productDetails.ProductName, &productDetails.BrandName, &productDetails.SupplierArticle,
			&hasAnswer, &answerText,
		); err != nil {
			return nil, fmt.Errorf("scan question: %w", err)
		}

		q.ProductDetails = productDetails
		q.WasViewed = wasViewed == 1

		// Set answer if exists
		if hasAnswer == 1 && answerText.Valid {
			q.Answer = &QuestionAnswer{
				Text: answerText.String,
			}
		}

		questions = append(questions, q)
	}

	// Get count of unanswered
	var countUnanswered int
	countQuery := "SELECT COUNT(*) FROM questions_items WHERE has_answer = 0"
	if req.NmID > 0 {
		countQuery += " AND nm_id = ?"
		s.db.QueryRowContext(ctx, countQuery, req.NmID).Scan(&countUnanswered)
	} else {
		s.db.QueryRowContext(ctx, countQuery).Scan(&countUnanswered)
	}

	return &QuestionsResponse{
		Data: struct {
			Questions        []Question `json:"questions"`
			CountUnanswered  int        `json:"countUnanswered"`
			CountArchive     int        `json:"countArchive"`
		}{
			Questions:        questions,
			CountUnanswered:  countUnanswered,
			CountArchive:     0, // Not tracked in snapshot
		},
	}, nil
}

// GetUnansweredCounts retrieves counts of unanswered feedbacks and questions.
func (s *snapshotFeedbacksService) GetUnansweredCounts(ctx context.Context) (*UnansweredCountsResponse, error) {
	var feedbacksUnanswered, questionsUnanswered int

	// Count unanswered feedbacks
	err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM feedbacks_items WHERE has_answer = 0",
	).Scan(&feedbacksUnanswered)
	if err != nil {
		return nil, fmt.Errorf("count unanswered feedbacks: %w", err)
	}

	// Count unanswered questions
	err = s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM questions_items WHERE has_answer = 0",
	).Scan(&questionsUnanswered)
	if err != nil {
		return nil, fmt.Errorf("count unanswered questions: %w", err)
	}

	return &UnansweredCountsResponse{
		FeedbacksUnanswered:      feedbacksUnanswered,
		FeedbacksUnansweredToday: 0, // Not tracked in snapshot
		QuestionsUnanswered:      questionsUnanswered,
		QuestionsUnansweredToday: 0, // Not tracked in snapshot
	}, nil
}
