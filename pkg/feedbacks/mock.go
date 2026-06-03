package feedbacks

import (
	"context"
	"fmt"
	"sync"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// DiscardWriter is a no-op Writer for --mock mode.
// Thread-safe counters track what would have been saved, but never touch any database.
//
// Mock safety: --mock must NOT open any database. See dev_v2_downloader.md §1.8.
type DiscardWriter struct {
	mu              sync.Mutex
	feedbacksSaved  int
	questionsSaved  int
}

// NewDiscardWriter creates a DiscardWriter with zero counters.
func NewDiscardWriter() *DiscardWriter {
	return &DiscardWriter{}
}

// SavedFeedbacks returns the total feedbacks "saved" (counted, not written).
func (w *DiscardWriter) SavedFeedbacks() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.feedbacksSaved
}

// SavedQuestions returns the total questions "saved" (counted, not written).
func (w *DiscardWriter) SavedQuestions() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.questionsSaved
}

// Saved returns the total rows "saved" across both entity types.
func (w *DiscardWriter) Saved() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.feedbacksSaved + w.questionsSaved
}

func (w *DiscardWriter) SaveFeedbacks(_ context.Context, items []wb.FeedbackFull) (int, error) {
	w.mu.Lock()
	w.feedbacksSaved += len(items)
	w.mu.Unlock()
	return len(items), nil
}

func (w *DiscardWriter) SaveQuestions(_ context.Context, items []wb.QuestionFull) (int, error) {
	w.mu.Lock()
	w.questionsSaved += len(items)
	w.mu.Unlock()
	return len(items), nil
}

func (w *DiscardWriter) CountFeedbacks(_ context.Context) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.feedbacksSaved, nil
}

func (w *DiscardWriter) CountQuestions(_ context.Context) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.questionsSaved, nil
}

// MockSource returns deterministic feedback/question data for --mock mode and tests.
// Thread-safe: supports concurrent access from multiple goroutines.
type MockSource struct {
	mu          sync.RWMutex
	feedbacks   []wb.FeedbackFull
	questions   []wb.QuestionFull
	failCount   int
	failCurrent int
}

// NewMockSource creates a new mock source with empty data.
func NewMockSource() *MockSource {
	return &MockSource{}
}

// SetFailCount configures how many requests should fail before succeeding.
// Useful for testing retry and error handling.
func (m *MockSource) SetFailCount(count int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failCount = count
	m.failCurrent = 0
}

// PopulateFeedbacks fills the mock source with deterministic feedback data.
func (m *MockSource) PopulateFeedbacks(count int) {
	items := make([]wb.FeedbackFull, count)
	for i := range count {
		state := "wbRu"
		answerText := "Thank you for your feedback!"
		items[i] = wb.FeedbackFull{
			ID:                  fmt.Sprintf("mock-fb-%d", i),
			Text:                fmt.Sprintf("Mock feedback text %d", i),
			Pros:                "Good quality",
			Cons:                "None",
			ProductValuation:    (i % 5) + 1,
			CreatedDate:         fmt.Sprintf("2026-05-%02dT12:00:00Z", (i%28)+1),
			State:               state,
			UserName:            fmt.Sprintf("User%d", i),
			WasViewed:           i%2 == 0,
			OrderStatus:         "buyout",
			MatchingSize:        "ok",
			Color:               "colorless",
			SubjectId:           100 + i%10,
			SubjectName:         "Test Subject",
			ProductName:         fmt.Sprintf("Product %d", i),
			Size:                "M",
			ProductNmId:         100000 + i,
			ProductImtId:        10000000 + i,
			Bables:              []string{},
		}
		// Every 3rd feedback has an answer
		if i%3 == 0 {
			items[i].AnswerText = &answerText
			items[i].AnswerState = &state
		}
	}
	m.mu.Lock()
	m.feedbacks = items
	m.mu.Unlock()
}

// PopulateQuestions fills the mock source with deterministic question data.
func (m *MockSource) PopulateQuestions(count int) {
	items := make([]wb.QuestionFull, count)
	for i := range count {
		answerText := "Answer to your question"
		items[i] = wb.QuestionFull{
			ID:             fmt.Sprintf("mock-q-%d", i),
			Text:           fmt.Sprintf("Mock question text %d?", i),
			CreatedDate:    fmt.Sprintf("2026-05-%02dT12:00:00Z", (i%28)+1),
			State:          "suppliersPortalSynch",
			WasViewed:      i%2 == 0,
			IsWarned:       false,
			ProductName:    fmt.Sprintf("Product %d", i),
			SupplierArticle: fmt.Sprintf("ART-%d", i),
			SupplierName:   "Test Supplier",
			BrandName:      "Test Brand",
			ProductNmId:    100000 + i,
			ProductImtId:   10000000 + i,
		}
		// Every 2nd question has an answer
		if i%2 == 0 {
			items[i].AnswerText = &answerText
		}
	}
	m.mu.Lock()
	m.questions = items
	m.mu.Unlock()
}

// GetFeedbacksPage returns mock feedback items (paginated by Take/Skip).
func (m *MockSource) GetFeedbacksPage(_ context.Context, p PageRequest) ([]wb.FeedbackFull, error) {
	m.mu.Lock()
	if m.failCurrent < m.failCount {
		m.failCurrent++
		m.mu.Unlock()
		return nil, fmt.Errorf("mock failure")
	}
	m.mu.Unlock()

	m.mu.RLock()
	defer m.mu.RUnlock()

	total := len(m.feedbacks)
	if p.Skip >= total {
		return nil, nil
	}

	end := min(p.Skip+p.Take, total)
	return m.feedbacks[p.Skip:end], nil
}

// GetQuestionsPage returns mock question items (paginated by Take/Skip).
func (m *MockSource) GetQuestionsPage(_ context.Context, p PageRequest) ([]wb.QuestionFull, error) {
	m.mu.Lock()
	if m.failCurrent < m.failCount {
		m.failCurrent++
		m.mu.Unlock()
		return nil, fmt.Errorf("mock failure")
	}
	m.mu.Unlock()

	m.mu.RLock()
	defer m.mu.RUnlock()

	total := len(m.questions)
	if p.Skip >= total {
		return nil, nil
	}

	end := min(p.Skip+p.Take, total)
	return m.questions[p.Skip:end], nil
}

// Compile-time assertions.
var (
	_ Source = (*MockSource)(nil)
	_ Writer = (*DiscardWriter)(nil)
)
