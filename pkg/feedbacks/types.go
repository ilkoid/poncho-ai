// Package feedbacks provides the v2 domain logic for downloading WB Feedbacks & Questions.
//
// Architecture: Source/Writer interfaces + Downloader. Business logic lives here;
// CLI driver in cmd/ does only flags → config → DI → Run.
//
// WB API:
//   - GET /api/v1/feedbacks  (feedbacks-api.wildberries.ru)
//   - GET /api/v1/questions  (feedbacks-api.wildberries.ru)
//   - Rate limit: 3 req/sec per endpoint
//   - API key: WB_API_FEEDBACK_KEY
package feedbacks

import (
	"context"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// ToolIDs for rate limiter keys — must match SetRateLimit() calls in CLI.
const (
	ToolIDFeedbacks = "download_feedbacks"
	ToolIDQuestions = "download_questions"
)

// Pagination limits (from docs/09-communications.yaml).
const (
	FeedbacksMaxTake = 5000   // max items per feedbacks page
	FeedbacksMaxSkip = 199990 // max skip offset for feedbacks
	QuestionsMaxTake = 5000   // max items per questions page
	QuestionsMaxSkip = 5000   // take + skip ≤ 10000 for questions
	MaxSplitDepth    = 10     // max recursion depth for questions period splitting
)

// PageRequest holds parameters for a single API page request.
// Used instead of 8+ positional arguments — both feedbacks and questions
// share the same parameter set.
type PageRequest struct {
	IsAnswered bool  // Filter by answer status (two-pass: false, then true)
	Take       int   // Items per page
	Skip       int   // Offset
	DateFrom   int64 // Unix timestamp — period start
	DateTo     int64 // Unix timestamp — period end
	RateLimit  int   // Desired rate (req/min)
	Burst      int   // Desired burst
}

// Source is the data source interface for feedbacks and questions.
// WBSource (wrapping *wb.Client) satisfies this.
type Source interface {
	// GetFeedbacksPage fetches one page of feedbacks from WB API.
	GetFeedbacksPage(ctx context.Context, p PageRequest) ([]wb.FeedbackFull, error)
	// GetQuestionsPage fetches one page of questions from WB API.
	GetQuestionsPage(ctx context.Context, p PageRequest) ([]wb.QuestionFull, error)
}

// Writer is the persistence interface for feedbacks and questions data.
// ISP: 4 methods — only what Downloader.Run() actually calls.
type Writer interface {
	// SaveFeedbacks saves a batch of feedbacks. Returns count of saved rows.
	SaveFeedbacks(ctx context.Context, items []wb.FeedbackFull) (int, error)
	// SaveQuestions saves a batch of questions. Returns count of saved rows.
	SaveQuestions(ctx context.Context, items []wb.QuestionFull) (int, error)
	// CountFeedbacks returns total number of feedbacks in the database.
	CountFeedbacks(ctx context.Context) (int, error)
	// CountQuestions returns total number of questions in the database.
	CountQuestions(ctx context.Context) (int, error)
}

// DownloadOptions configures the feedbacks download behavior.
type DownloadOptions struct {
	// Enable/disable entity types (both default to true).
	Feedbacks bool
	Questions bool

	// Date range (YYYY-MM-DD). If empty, computed from Days.
	DateFrom string
	DateTo   string
	// Days fallback when DateFrom/DateTo not set (default: 7).
	Days int

	// DryRun skips all DB writes.
	DryRun bool

	// Rate limiting per endpoint.
	FeedbacksRate  int // Desired rate for feedbacks endpoint (req/min)
	FeedbacksBurst int
	QuestionsRate  int // Desired rate for questions endpoint (req/min)
	QuestionsBurst int

	// OnProgress callback for status messages (nil = silent, e.g. Tool mode).
	OnProgress func(msg string)
}

// DownloadResult holds the outcome of a feedbacks download run.
type DownloadResult struct {
	FeedbacksSaved int
	QuestionsSaved int
	FeedbacksPages int
	QuestionsPages int
	Duration       time.Duration
}
