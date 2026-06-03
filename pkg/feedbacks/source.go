package feedbacks

import (
	"context"
	"fmt"
	"net/url"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

const feedbacksBaseURL = "https://feedbacks-api.wildberries.ru"

// WBSource adapts *wb.Client to the Source interface.
// Isolates API URL construction and response unwrapping from the downloader.
type WBSource struct {
	client *wb.Client
}

// NewWBSource creates a Source backed by the real WB Feedbacks API.
func NewWBSource(client *wb.Client) *WBSource {
	return &WBSource{client: client}
}

// GetFeedbacksPage fetches one page of feedbacks from the WB API.
// Returns parsed FeedbackFull items or an API error.
func (s *WBSource) GetFeedbacksPage(ctx context.Context, p PageRequest) ([]wb.FeedbackFull, error) {
	params := url.Values{
		"isAnswered": {fmt.Sprintf("%t", p.IsAnswered)},
		"take":       {fmt.Sprintf("%d", p.Take)},
		"skip":       {fmt.Sprintf("%d", p.Skip)},
		"order":      {"dateAsc"},
		"dateFrom":   {fmt.Sprintf("%d", p.DateFrom)},
		"dateTo":     {fmt.Sprintf("%d", p.DateTo)},
	}

	var resp feedbacksAPIResponse
	if err := s.client.Get(ctx, ToolIDFeedbacks, feedbacksBaseURL,
		p.RateLimit, p.Burst,
		"/api/v1/feedbacks", params, &resp); err != nil {
		return nil, fmt.Errorf("feedbacks API: %w", err)
	}

	if resp.Error {
		return nil, fmt.Errorf("feedbacks API error: %s", resp.ErrorText)
	}

	return resp.Data.Feedbacks, nil
}

// GetQuestionsPage fetches one page of questions from the WB API.
// Returns parsed QuestionFull items or an API error.
func (s *WBSource) GetQuestionsPage(ctx context.Context, p PageRequest) ([]wb.QuestionFull, error) {
	params := url.Values{
		"isAnswered": {fmt.Sprintf("%t", p.IsAnswered)},
		"take":       {fmt.Sprintf("%d", p.Take)},
		"skip":       {fmt.Sprintf("%d", p.Skip)},
		"order":      {"dateAsc"},
		"dateFrom":   {fmt.Sprintf("%d", p.DateFrom)},
		"dateTo":     {fmt.Sprintf("%d", p.DateTo)},
	}

	var resp questionsAPIResponse
	if err := s.client.Get(ctx, ToolIDQuestions, feedbacksBaseURL,
		p.RateLimit, p.Burst,
		"/api/v1/questions", params, &resp); err != nil {
		return nil, fmt.Errorf("questions API: %w", err)
	}

	if resp.Error {
		return nil, fmt.Errorf("questions API error: %s", resp.ErrorText)
	}

	return resp.Data.Questions, nil
}

// ============================================================================
// Unexported API response types — single location for JSON unwrapping.
// Full domain types (FeedbackFull, QuestionFull) live in pkg/wb/feedbacks_full.go.
// ============================================================================

type feedbacksAPIResponse struct {
	Data struct {
		CountUnanswered int              `json:"countUnanswered"`
		CountArchive    int              `json:"countArchive"`
		Feedbacks       []wb.FeedbackFull `json:"feedbacks"`
	} `json:"data"`
	Error            bool     `json:"error"`
	ErrorText        string   `json:"errorText"`
	AdditionalErrors []string `json:"additionalErrors"`
}

type questionsAPIResponse struct {
	Data struct {
		CountUnanswered int               `json:"countUnanswered"`
		CountArchive    int               `json:"countArchive"`
		Questions       []wb.QuestionFull `json:"questions"`
	} `json:"data"`
	Error            bool     `json:"error"`
	ErrorText        string   `json:"errorText"`
	AdditionalErrors []string `json:"additionalErrors"`
}
