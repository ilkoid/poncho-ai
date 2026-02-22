// Package wb provides service layer implementations for Wildberries API.
//
// This file contains the FeedbackService implementation with business logic
// for product feedbacks and customer questions.
package wb

import (
	"context"
	"fmt"
	"net/url"
)

// Ensure feedbacksService implements FeedbackService.
var _ FeedbackService = (*feedbacksService)(nil)

// feedbacksService implements FeedbackService using the WB Client.
type feedbacksService struct {
	client *Client
}

// GetFeedbacks retrieves product feedbacks with optional filtering.
//
// Uses Feedbacks API: GET /api/v1/feedbacks
// Supports pagination via take/noffset and filtering by isAnswered/nmID.
func (s *feedbacksService) GetFeedbacks(ctx context.Context, req FeedbacksRequest) (*FeedbacksResponse, error) {
	// Validation
	if req.Take <= 0 || req.Take > 100 {
		req.Take = 100 // Default
	}

	// Mock mode
	if s.client.IsDemoKey() {
		return s.getMockFeedbacks(req)
	}

	// Build query parameters
	params := url.Values{}
	params.Set("take", fmt.Sprintf("%d", req.Take))
	params.Set("skip", fmt.Sprintf("%d", req.Noffset))

	if req.IsAnswered != nil {
		params.Set("isAnswered", fmt.Sprintf("%v", *req.IsAnswered))
	} else {
		params.Set("isAnswered", "true") // Default for WB Feedbacks API
	}
	if req.NmID > 0 {
		params.Set("nmId", fmt.Sprintf("%d", req.NmID))
	}

	var response FeedbacksResponse
	err := s.client.Get(ctx, "get_wb_feedbacks2",
		"https://feedbacks-api.wildberries.ru", 60, 3,
		"/api/v1/feedbacks", params, &response)
	if err != nil {
		return nil, fmt.Errorf("failed to get feedbacks: %w", err)
	}

	return &response, nil
}

// getMockFeedbacks returns mock feedbacks for demo mode.
func (s *feedbacksService) getMockFeedbacks(req FeedbacksRequest) (*FeedbacksResponse, error) {
	count := req.Take
	if count > 10 {
		count = 10 // Limit mock data
	}

	feedbacks := make([]Feedback, 0, count)
	for i := 0; i < count; i++ {
		rating := 4 + (i % 2)
		if i%5 == 0 {
			rating = 3
		}

		feedbacks = append(feedbacks, Feedback{
			ID:               fmt.Sprintf("mock-fb-%d", i+1),
			Text:             fmt.Sprintf("Отличный товар! Качество на высоте. Рекомендую к покупке. (Mock #%d)", i+1),
			ProductValuation: rating,
			CreatedDate:      "2026-02-20T10:30:00Z",
			UserName:         fmt.Sprintf("Пользователь %d", i+1),
			ProductDetails: FeedbackProduct{
				NmId:            req.NmID,
				ProductName:     "Mock Product",
				SupplierArticle: "MOCK-001",
				BrandName:       "Mock Brand",
			},
		})
	}

	return &FeedbacksResponse{
		Data: struct {
			Feedbacks       []Feedback `json:"feedbacks"`
			CountUnanswered int        `json:"countUnanswered"`
			CountArchive    int        `json:"countArchive"`
		}{
			Feedbacks:       feedbacks,
			CountUnanswered: 5,
			CountArchive:    10,
		},
	}, nil
}

// GetQuestions retrieves product questions with optional filtering.
//
// Uses Feedbacks API: GET /api/v1/questions
// Supports pagination via take/noffset and filtering by isAnswered/nmID.
func (s *feedbacksService) GetQuestions(ctx context.Context, req QuestionsRequest) (*QuestionsResponse, error) {
	// Validation
	if req.Take <= 0 || req.Take > 100 {
		req.Take = 100 // Default
	}

	// Mock mode
	if s.client.IsDemoKey() {
		return s.getMockQuestions(req)
	}

	// Build query parameters
	params := url.Values{}
	params.Set("take", fmt.Sprintf("%d", req.Take))
	params.Set("skip", fmt.Sprintf("%d", req.Noffset))

	if req.IsAnswered != nil {
		params.Set("isAnswered", fmt.Sprintf("%v", *req.IsAnswered))
	} else {
		params.Set("isAnswered", "true") // Default for WB Questions API
	}
	if req.NmID > 0 {
		params.Set("nmId", fmt.Sprintf("%d", req.NmID))
	}

	var response QuestionsResponse
	err := s.client.Get(ctx, "get_wb_questions2",
		"https://feedbacks-api.wildberries.ru", 60, 3,
		"/api/v1/questions", params, &response)
	if err != nil {
		return nil, fmt.Errorf("failed to get questions: %w", err)
	}

	return &response, nil
}

// getMockQuestions returns mock questions for demo mode.
func (s *feedbacksService) getMockQuestions(req QuestionsRequest) (*QuestionsResponse, error) {
	count := req.Take
	if count > 5 {
		count = 5 // Limit mock data
	}

	questions := make([]Question, 0, count)
	for i := 0; i < count; i++ {
		answered := i%2 == 0
		var answer *QuestionAnswer
		if answered {
			answer = &QuestionAnswer{
				Text: "Здравствуйте! Это mock ответ на ваш вопрос. Товар в наличии.",
			}
		}

		questions = append(questions, Question{
			ID:          fmt.Sprintf("mock-q-%d", i+1),
			Text:        fmt.Sprintf("Подскажите, какой размер лучше выбрать? (Mock #%d)", i+1),
			CreatedDate: "2026-02-20T14:00:00Z",
			State:       "none",
			Answer:      answer,
			WasViewed:   true,
			ProductDetails: QuestionProduct{
				NmId:            req.NmID,
				ProductName:     "Mock Product",
				SupplierArticle: "MOCK-001",
				SupplierName:    "Mock Supplier",
				BrandName:       "Mock Brand",
			},
		})
	}

	return &QuestionsResponse{
		Data: struct {
			Questions        []Question `json:"questions"`
			CountUnanswered  int        `json:"countUnanswered"`
			CountArchive     int        `json:"countArchive"`
		}{
			Questions:        questions,
			CountUnanswered:  3,
			CountArchive:     5,
		},
	}, nil
}

// GetUnansweredCounts retrieves counts of unanswered feedbacks and questions.
func (s *feedbacksService) GetUnansweredCounts(ctx context.Context) (*UnansweredCountsResponse, error) {
	// Mock mode
	if s.client.IsDemoKey() {
		return &UnansweredCountsResponse{
			FeedbacksUnanswered:      12,
			FeedbacksUnansweredToday: 3,
			QuestionsUnanswered:      5,
			QuestionsUnansweredToday: 1,
		}, nil
	}

	// Get feedbacks counts
	var fbResp UnansweredFeedbacksCountsResponse
	err := s.client.Get(ctx, "get_wb_unanswered_feedbacks_counts2",
		"https://feedbacks-api.wildberries.ru", 60, 3,
		"/api/v1/feedbacks/count-unanswered", nil, &fbResp)
	if err != nil {
		return nil, fmt.Errorf("failed to get feedback counts: %w", err)
	}

	// Get questions counts
	var qResp UnansweredQuestionsCountsResponse
	err = s.client.Get(ctx, "get_wb_unanswered_questions_counts2",
		"https://feedbacks-api.wildberries.ru", 60, 3,
		"/api/v1/questions/count-unanswered", nil, &qResp)
	if err != nil {
		return nil, fmt.Errorf("failed to get question counts: %w", err)
	}

	return &UnansweredCountsResponse{
		FeedbacksUnanswered:      fbResp.Data.CountUnanswered,
		FeedbacksUnansweredToday: fbResp.Data.CountUnansweredToday,
		QuestionsUnanswered:      qResp.Data.CountUnanswered,
		QuestionsUnansweredToday: qResp.Data.CountUnansweredToday,
	}, nil
}
